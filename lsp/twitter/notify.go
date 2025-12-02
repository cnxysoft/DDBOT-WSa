package twitter

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/ffmpeg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/google/uuid"
)

// TwitterDynamic 推文动态数据结构，用于模板渲染
type TwitterDynamic struct {
	User          TwitterUser    `json:"user"`
	Type          int            `json:"type"`
	Content       string         `json:"content"`
	Date          string         `json:"date"`
	Url           string         `json:"url"`
	Media         []TwitterMedia `json:"media"`
	IsRetweet     bool           `json:"is_retweet"`
	WithQuote     bool           `json:"with_quote"`
	Retweet       TwitterRetweet `json:"retweet"`
	Quote         TwitterQuote   `json:"quote"`
	ShouldCompact bool           `json:"should_compact"`
	CompactKey    string         `json:"compact_key"`
}

// TwitterUser 用户信息
type TwitterUser struct {
	Name string `json:"name"`
	Id   string `json:"id"`
}

// TwitterMedia 媒体信息
type TwitterMedia struct {
	Type   string `json:"type"`
	Url    string `json:"url"`
	Base64 string `json:"base64"`
}

// TwitterRetweet 转发信息
type TwitterRetweet struct {
	User    TwitterUser    `json:"user"`
	Content string         `json:"content"`
	Media   []TwitterMedia `json:"media"`
}

// TwitterQuote 引用信息
type TwitterQuote struct {
	User    TwitterUser    `json:"user"`
	Content string         `json:"content"`
	Media   []TwitterMedia `json:"media"`
	Date    string         `json:"date"`
}

type ConcernNewsNotify struct {
	GroupCode int64 `json:"group_code"`
	*NewsInfo
	shouldCompact bool
	compactKey    string
	concern       *twitterConcern
}

func (n *ConcernNewsNotify) GetGroupCode() int64 {
	return n.GroupCode
}

func (n *ConcernNewsNotify) ToMessage() (m *mmsg.MSG) {
	return n.GetMSG(n)
}

// buildTwitterDynamic 构建TwitterDynamic数据
func (n *ConcernNewsNotify) buildTwitterDynamic() TwitterDynamic {
	dynamic := TwitterDynamic{}

	// 基本信息
	dynamic.User = TwitterUser{
		Name: n.Name,
		Id:   n.UserInfo.Id,
	}
	dynamic.Type = n.Tweet.RtType()
	dynamic.Content = n.Tweet.Content
	dynamic.Url = n.Tweet.Url
	dynamic.ShouldCompact = n.shouldCompact
	dynamic.CompactKey = n.compactKey

	// 时间处理
	var createdAt time.Time
	if n.Tweet.RtType() == RETWEET {
		createdAt = time.Now().UTC()
	} else {
		createdAt = n.Tweet.CreatedAt
	}
	dynamic.Date = CSTTime(createdAt).Format(time.DateTime)

	// 媒体处理
	dynamic.Media = make([]TwitterMedia, 0, len(n.Tweet.Media))
	for _, media := range n.Tweet.Media {
		processedUrl := n.processMediaUrl(media.Url, media.Type)

		// 处理媒体文件，下载并保存到本地
		localPath, err := n.processMediaFile(media)
		if err != nil {
			logger.WithField("mediaUrl", media.Url).
				WithField("mediaType", media.Type).
				Errorf("processMediaFile failed, will use URL directly: %v", err)
		}

		mediaBytes, err := os.ReadFile(localPath)
		if err != nil {
			logger.WithField("localPath", localPath).
				Errorf("ReadFile failed: %v", err)
		}

		dynamic.Media = append(dynamic.Media, TwitterMedia{
			Type:   media.Type,
			Url:    processedUrl,
			Base64: base64.StdEncoding.EncodeToString(mediaBytes),
		})
	}

	// 转发信息
	dynamic.IsRetweet = n.Tweet.IsRetweet
	if n.Tweet.IsRetweet && n.Tweet.OrgUser != nil {
		dynamic.Retweet = TwitterRetweet{
			User: TwitterUser{
				Name: n.Tweet.OrgUser.Name,
				Id:   n.Tweet.OrgUser.ScreenName, // UserProfile没有Id字段，使用ScreenName代替
			},
			Content: n.Tweet.Content,
		}
	}

	// 引用信息
	dynamic.WithQuote = n.Tweet.QuoteTweet != nil
	if n.Tweet.QuoteTweet != nil {
		quote := n.Tweet.QuoteTweet
		quoteMedia := make([]TwitterMedia, 0, len(quote.Media))
		for _, media := range quote.Media {
			processedUrl := n.processMediaUrl(media.Url, media.Type)

			// 处理引用的媒体文件，下载并保存到本地
			localPath, err := n.processMediaFile(media)
			if err != nil {
				logger.WithField("mediaUrl", media.Url).
					WithField("mediaType", media.Type).
					Errorf("processMediaFile for quote tweet failed, will use URL directly: %v", err)
			}

			mediaBytes, err := os.ReadFile(localPath)
			if err != nil {
				logger.WithField("localPath", localPath).
					Errorf("ReadFile failed: %v", err)
			}

			quoteMedia = append(quoteMedia, TwitterMedia{
				Type:   media.Type,
				Url:    processedUrl,
				Base64: base64.StdEncoding.EncodeToString(mediaBytes),
			})
		}
		dynamic.Quote = TwitterQuote{
			User: TwitterUser{
				Name: quote.OrgUser.Name,
				Id:   quote.OrgUser.ScreenName, // UserProfile没有Id字段，使用ScreenName代替
			},
			Content: quote.Content,
			Media:   quoteMedia,
			Date:    CSTTime(quote.CreatedAt).Format(time.DateTime),
		}
	}

	return dynamic
}

// processMediaUrl 处理媒体URL，确保返回完整的HTTP URL
func (n *ConcernNewsNotify) processMediaUrl(mediaUrl string, mediaType string) string {
	// 如果已经是完整的HTTP URL，直接返回
	if strings.HasPrefix(mediaUrl, "http://") || strings.HasPrefix(mediaUrl, "https://") {
		return mediaUrl
	}

	// 处理URL编码
	unescape := mediaUrl
	if isURIEncoded(unescape) {
		processedUrl, err := processMediaURL(unescape)
		if err == nil {
			unescape = processedUrl
		}
	}

	// 处理视频URL，特别是包含完整URL的情况
	// 例如：/video/2C05D02B4228B/https%3A%2F%2Fvideo.twimg.com%2F...
	if strings.Contains(unescape, "video.twimg.com") {
		// 提取video.twimg.com及其之后的部分
		idx := strings.Index(unescape, "video.twimg.com")
		if idx != -1 {
			// 直接构造完整的视频URL
			return fmt.Sprintf("https://%s", unescape[idx:])
		}
	}

	// 处理相对路径，根据MirrorHost生成完整URL
	mirrorHost := n.Tweet.MirrorHost
	if mirrorHost == "" {
		// 默认使用Twitter的图片主机
		mirrorHost = XImgHost
	}

	// 处理nitter的/pic/代理路径，剔除前缀以便还原
	if mirrorHost == XImgHost {
		unescape = strings.TrimLeft(unescape, "/pic/")
	}

	// 构造完整URL
	var fullUrl string
	if mirrorHost == XImgHost {
		// 如果是Twitter的图片主机，直接使用pbs.twimg.com
		fullUrl = fmt.Sprintf("https://pbs.twimg.com/%s", unescape)
	} else {
		// 其他主机直接拼接
		fullUrl = fmt.Sprintf("https://%s%s", mirrorHost, unescape)
	}

	// 规范化媒体URL
	if mediaType == "image" {
		fullUrl = NormalizeMediaURLToPBSOrig(fullUrl)
	}

	return fullUrl
}

// processMediaFile 处理媒体文件，下载并保存到本地
func (n *ConcernNewsNotify) processMediaFile(media *Media) (string, error) {
	// 处理媒体URL，确保是完整的HTTP URL
	mediaUrl := n.processMediaUrl(media.Url, media.Type)

	var localPath string
	var err error

	// 根据媒体类型选择不同的处理方式
	switch media.Type {
	case "image":
		// 处理图片文件
		localPath, err = n.processImageFile(mediaUrl)
	case "gif":
		// 处理GIF文件
		localPath, err = n.processGifFile(mediaUrl)
	case "video", "video(m3u8)":
		// 处理视频文件
		localPath, err = n.processVideoFile(mediaUrl, media.Type)
	default:
		// 不支持的媒体类型
		err = fmt.Errorf("unsupported media type: %s", media.Type)
	}

	if err != nil {
		logger.WithField("mediaUrl", mediaUrl).
			WithField("mediaType", media.Type).
			Errorf("processMediaFile failed: %v", err)
		return "", err
	}

	return localPath, nil
}

// processImageFile 处理图片文件，下载并保存到本地
func (n *ConcernNewsNotify) processImageFile(url string) (string, error) {
	// 规范化图片URL
	finalURL := NormalizeMediaURLToPBSOrig(url)

	// 尝试下载最佳质量的图片
	filePath, err := tryDownloadBestImage(finalURL)
	if err != nil {
		logger.WithField("url", url).Errorf("tryDownloadBestImage failed: %v", err)
		return "", err
	}

	return filePath, nil
}

// processGifFile 处理GIF文件，下载并保存到本地
func (n *ConcernNewsNotify) processGifFile(url string) (string, error) {
	// GIF文件处理逻辑，复用原始代码中的实现
	// 确保保留动画效果
	filePath, err := downloadMedia(url, true)
	if err != nil {
		logger.WithField("url", url).Errorf("downloadMedia failed: %v", err)
		return "", err
	}

	return filePath, nil
}

// processVideoFile 处理视频文件，下载并保存到本地
func (n *ConcernNewsNotify) processVideoFile(url string, mediaType string) (string, error) {
	// 视频文件处理逻辑，复用原始代码中的实现
	// 支持直接MP4和M3U8格式
	filePath, err := downloadMedia(url, false)
	if err != nil {
		logger.WithField("url", url).
			WithField("mediaType", mediaType).
			Errorf("downloadMedia failed: %v", err)
		return "", err
	}

	return filePath, nil
}

// fallbackMSG 模板加载失败时的回退消息生成
func (n *ConcernNewsNotify) fallbackMSG() *mmsg.MSG {
	m := mmsg.NewMSG()
	var addedUrl bool
	if n.shouldCompact {
		// 先推送了转发，才推送原文
		// 这种直接放弃，避免二次推送
		if n.Tweet.OrgUser == nil && n.Tweet.QuoteTweet == nil {
			logger.Debug("compact notify ignored: already pushed.")
			return nil
		}
		// 通过回复之前消息的方式简化推送
		msg, _ := n.concern.GetNotifyMsg(n.GroupCode, n.compactKey)
		if msg != nil {
			m.Append(message.NewReply(msg))
		}
		logger.WithField("compact_key", n.compactKey).Debug("compact notify")
		Tips := "转发"
		var OrgUserName string
		if n.Tweet.QuoteTweet != nil {
			OrgUserName = n.Tweet.QuoteTweet.OrgUser.Name
			Tips = "引用"
		} else {
			OrgUserName = n.Tweet.OrgUser.Name
		}
		m.Textf("X-%s%s了%s的推文：\n%s\n%s\n",
			n.Name,
			Tips,
			OrgUserName,
			CSTTime(time.Now().UTC()).Format(time.DateTime),
			n.Tweet.Content,
		)
		addTweetUrl(m, n.Tweet.Url, &addedUrl)
	} else {
		// 构造消息
		if n.Tweet.ID == "" {
			return nil
		}
		var CreatedAt time.Time
		if n.Tweet.RtType() == RETWEET {
			CreatedAt = time.Now().UTC()
			m.Textf("X-%s转发了%s的推文：\n",
				n.Name, n.Tweet.OrgUser.Name)
		} else {
			CreatedAt = n.Tweet.CreatedAt
			m.Textf("X-%s发布了新推文：\n", n.Name)
		}
		m.Text(CSTTime(CreatedAt).Format(time.DateTime) + "\n")
		// msg加入推文
		if n.Tweet.Content != "" {
			content := n.Tweet.Content
			if n.Tweet.Media != nil || content[len(content)-1] != '\n' {
				content += "\n"
			}
			m.Text(content)
		}
		// msg加入媒体
		addMedia(n.Tweet, m, true, &addedUrl)
		// msg加入被引用推文
		if QuoteTweet := n.Tweet.QuoteTweet; QuoteTweet != nil {
			var CreatedAt time.Time
			quoteTxt := "\n%v引用了%v的推文：\n"
			CreatedAt = QuoteTweet.CreatedAt
			// 检查是否需要插入cut
			addCut(m, &quoteTxt)
			m.Textf(quoteTxt, n.Tweet.OrgUser.Name, QuoteTweet.OrgUser.Name)
			m.Text(CSTTime(CreatedAt).Format(time.DateTime) + "\n")
			// msg加入推文
			if QuoteTweet.Content != "" {
				m.Text(QuoteTweet.Content + "\n")
			}
			// msg加入媒体
			addMedia(QuoteTweet, m, false, &addedUrl)
		}
		addTweetUrl(m, n.Tweet.Url, &addedUrl)
	}
	return m
}

func (n *ConcernNewsNotify) IsLive() bool {
	return false
}

func (n *ConcernNewsNotify) Living() bool {
	return false
}

func NewConcernNewsNotify(groupCode int64, newsInfo *NewsInfo, c *twitterConcern) *ConcernNewsNotify {
	if newsInfo == nil {
		return nil
	}
	var result = &ConcernNewsNotify{
		GroupCode: groupCode,
		NewsInfo:  newsInfo,
		concern:   c,
	}
	return result
}

func addMedia(tweet *Tweet, message *mmsg.MSG, mainTweet bool, addedUrl *bool) {
	for _, m := range tweet.Media {
		var err error
		var Url *url.URL
		// https://video.twimg.com/amplify_video/1978345183696781312/vid/avc1/2160x2880/BgoVYgPoqLbeir2E.mp4
		unescape := m.Url
		if strings.HasPrefix(unescape, "/") {
			Url, err = setMirrorHost(tweet.MirrorHost, *m)
			if err != nil {
				logger.WithField("stack", string(debug.Stack())).
					WithField("tweetId", tweet.ID).
					Errorf("concern notify recoverd %v", err)
				continue
			}
			if Url.Hostname() != "" {
				if Url.Hostname() == XImgHost || Url.Hostname() == XVideoHost {
					unescape, err = processMediaURL(m.Url)
					if err != nil {
						logger.WithField("stack", string(debug.Stack())).
							WithField("tweetId", tweet.ID).
							Errorf("concern notify recoverd: %v", err)
						continue
					}
				}
			}
		} else {
			UrlStr, err := processMediaURL(unescape)
			if err != nil {
				logger.WithField("stack", string(debug.Stack())).
					WithField("tweetId", tweet.ID).
					Errorf("concern notify recoverd: %v", err)
				continue
			}
			Url, err = url.Parse(UrlStr)
		}

		switch m.Type {

		case "image":
			if tweet.MirrorHost == XImgHost {
				// nitter 的 /pic/ 代理路径，剔除前缀以便还原
				unescape = strings.TrimLeft(unescape, "/pic/")
			}
			fullURL, err := Url.Parse(unescape)
			if err != nil {
				logger.WithField("stack", string(debug.Stack())).
					WithField("tweetId", tweet.ID).
					Errorf("concern notify recovered %v", err)
				break
			}

			// 规范化为 pbs 最大尺寸，并下载到本地
			finalURL := NormalizeMediaURLToPBSOrig(fullURL.String())
			filePath, derr := tryDownloadBestImage(finalURL)
			if derr != nil {
				// 下载失败 → 兜底仍以 URL 方式发送，避免消息丢失
				logger.WithField("stack", string(debug.Stack())).
					WithField("tweetId", tweet.ID).
					Errorf("download orig image failed: %v", derr)

				var opts []requests.Option
				if tweet.MirrorHost == "nitter.privacyredirect.com" {
					opts = []requests.Option{
						requests.RequestAutoHostOption(),
						requests.HeaderOption("Accept-Encoding", "gzip, deflate, br, zstd"),
						requests.HeaderOption("sec-fetch-site", "none"),
						requests.HeaderOption("sec-fetch-mode", "navigate"),
						requests.HeaderOption("sec-fetch-dest", "document"),
						requests.HeaderOption("accept-language", "zh-CN,zh;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6"),
						requests.HeaderOption("accept",
							"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7"),
					}
				}
				addCut(message, nil)
				opts = append(opts,
					requests.ProxyOption(proxy_pool.PreferOversea),
					requests.AddUAOption(UserAgent),
					requests.WithCookieJar(Cookie),
				)
				message.Append(mmsg.NewImageByUrl(fullURL.String(), opts...))
				break
			}

			// 关键：使用“本地图片”上传到 QQ，达到“原图”体验
			addCut(message, nil)
			message.Append(mmsg.NewImageByLocal(filePath))

		case "video":
			if strings.Contains(unescape, "video.twimg.com") {
				idx := strings.Index(unescape, "video.twimg.com")
				unescape, err = processMediaURL(unescape[idx:])
				if err != nil {
					logger.WithField("stack", string(debug.Stack())).
						WithField("tweetId", tweet.ID).
						Errorf("concern notify recoverd: %v", err)
					continue
				}
				m.Url = unescape
			}
			if mainTweet {
				addTweetUrl(message, tweet.Url, addedUrl)
			}
			message.Cut()
			message.Append(
				mmsg.NewVideoByUrl(m.Url,
					requests.ProxyOption(proxy_pool.PreferOversea),
					requests.AddUAOption(UserAgent),
					requests.WithCookieJar(Cookie)))
		case "gif":
			if strings.Contains(unescape, "video.twimg.com") {
				idx := strings.Index(unescape, "video.twimg.com")
				unescape, err = processMediaURL(unescape[idx:])
				if err != nil {
					logger.WithField("stack", string(debug.Stack())).
						WithField("tweetId", tweet.ID).
						Errorf("concern notify recoverd: %v", err)
					continue
				}
				m.Url = "https://" + unescape
			}
			// 下载并转码
			filePath, err := downloadMedia(m.Url, true)
			if err != nil {
				logger.WithField("stack", string(debug.Stack())).
					WithField("tweetId", tweet.ID).
					Errorf("concern notify recoverd: %v", err)
				continue
			}
			message.Append(mmsg.NewImageByLocal(filePath))
		case "video(m3u8)":
			var fullURL *url.URL
			var err error
			if tweet.MirrorHost == XVideoHost {
				idx := findNthIndex(unescape, '/', 3)
				if idx != -1 {
					unescape = unescape[idx+1:]
				}
			} else if strings.Contains(unescape, "https%3A%2F%2Fvideo.twimg.com") {
				idx := strings.Index(unescape, "https%3A%2F%2F")
				unescape, err = processMediaURL(unescape[idx:])
				if err != nil {
					logger.WithField("stack", string(debug.Stack())).
						WithField("tweetId", tweet.ID).
						Errorf("concern notify recoverd: %v", err)
					continue
				}
				idx = findNthIndex(unescape, '?', 1)
				if idx != -1 {
					unescape = unescape[:idx]
				}
				m.Url = unescape
			} else {
				fullURL, err = Url.Parse(unescape)
				if err != nil {
					logger.WithField("stack", string(debug.Stack())).
						WithField("tweetId", tweet.ID).
						Errorf("concern notify recoverd %v", err)
				}
				m.Url = fullURL.String()
			}
			// 下载并转码
			filePath, err := downloadMedia(m.Url, false)
			if err != nil {
				logger.WithField("stack", string(debug.Stack())).
					WithField("tweetId", tweet.ID).
					Errorf("concern notify recoverd: %v", err)
				continue
			}
			if mainTweet {
				addTweetUrl(message, tweet.Url, addedUrl)
			}
			message.Cut()
			message.Append(mmsg.NewVideoByLocal(filePath))
		}
	}
}

func addCut(msg *mmsg.MSG, quo *string) {
	ele := msg.Elements()
	if ele[len(ele)-1].Type() == mmsg.Video {
		msg.Cut()
		if quo != nil {
			*quo = strings.TrimPrefix(*quo, "\n")
		}
	}
}

func addTweetUrl(msg *mmsg.MSG, url string, added *bool) {
	if !*added {
		*added = true
		msg.Text(url + "\n")
	}
}

func downloadMedia(Url string, IsGif bool) (string, error) {
	var proxyStr string
	proxy, err := proxy_pool.Get(proxy_pool.PreferOversea)
	if err != nil {
		return "", err
	} else {
		proxyStr = proxy.ProxyString()
	}
	if _, err = os.Stat("./res"); os.IsNotExist(err) {
		if err = os.MkdirAll("./res", 0755); err != nil {
			return "", err
		}
	}
	fileExt := "mp4"
	if IsGif {
		fileExt = "gif"
	}
	filePath, _ := filepath.Abs("./res/" + uuid.New().String() + "." + fileExt)

	if IsGif {
		err = ffmpeg.ConvMediaWithProxy(Url, filePath, proxyStr, fileExt)
	} else {
		err = ffmpeg.ConvMediaWithProxy(Url, filePath, proxyStr, fileExt)
	}
	if err != nil {
		return "", err
	}
	go func(path string) {
		time.Sleep(time.Second * 180)
		logger.Debugf("Delete temporary files: %s", path)
		err := os.Remove(path)
		if err != nil {
			logger.WithField("stack", string(debug.Stack())).
				WithField("filePath", path).
				Errorf("Delete temporary files error: %v", err)
		}
	}(filePath)
	return filePath, nil
}

//func convMediaWithProxy(Url, outputPath, proxyURL, Type string) error {
//	args := []string{
//		"-v", "error",
//		"-i", Url,
//		"-f", Type,
//		outputPath,
//	}
//
//	if Type == "mp4" {
//		args = []string{
//			"-v", "error",
//			"-i", Url,
//			"-c", "copy",
//			"-movflags",
//			"+faststart",
//			"-f", Type,
//			outputPath,
//		}
//	}
//
//	cmd := exec.Command("ffmpeg", args...)
//	if proxyURL != "" {
//		cmd.Env = append(os.Environ(), "http_proxy="+proxyURL, "https_proxy="+proxyURL, "rw_timeout=30000000")
//	}
//
//	cmd.Stdout = nil
//	cmd.Stderr = os.Stderr
//
//	return cmd.Run()
//}

func findNthIndex(s string, sep byte, n int) int {
	count := 0
	for i := range s {
		if s[i] == sep {
			count++
			if count == n {
				return i
			}
		}
	}
	return -1
}

func setMirrorHost(mirrorHost string, m Media) (*url.URL, error) {
	if mirrorHost == "" || mirrorHost == XImgHost || mirrorHost == XVideoHost {
		logger.WithField("mediaUrl", m.Url).
			Trace("No MirrorHost was found, using the default Host of X.")
		if m.Type == "image" {
			mirrorHost = XImgHost
		} else {
			mirrorHost = XVideoHost
		}
	}
	Url := url.URL{
		Scheme: "https",
		Host:   mirrorHost,
	}
	return &Url, nil
}

// 检测是否包含URI编码特征
func isURIEncoded(s string) bool {
	// 匹配URI编码特征（%后跟两个十六进制字符）
	re := regexp.MustCompile(`%(?i)[0-9a-f]{2}`)
	return re.MatchString(s)
}

// 处理Twitter媒体URL
func processMediaURL(encodedURL string) (string, error) {
	// 判断是否需要解码
	if !isURIEncoded(encodedURL) {
		return encodedURL, nil
	}

	// 解除所有层级编码
	decodedURL, err := safeDecodeURIComponent(encodedURL)
	if err != nil {
		return "", fmt.Errorf("多级URI解码失败: %v", err)
	}

	return decodedURL, nil
}

// 安全的URI解码器
func safeDecodeURIComponent(s string) (string, error) {
	maxIterations := 10
	decoded := s
	for i := 0; i < maxIterations; i++ {
		nextDecoded, err := url.QueryUnescape(decoded)
		if err != nil {
			return decoded, err
		}
		if nextDecoded == decoded {
			break
		}
		decoded = nextDecoded
	}
	return decoded, nil
}

// ======== 规范化 Twitter/Nitter 图片到 pbs 最大尺寸（name=orig） ========

// NormalizeMediaURLToPBSOrig 将媒体 URL 规范化到 pbs 的最大可用版本（name=orig），并保留 format。
// 对 nitter 的 /pic/ 路由做还原；失败时返回原始 URL（后续再兜底）。
func NormalizeMediaURLToPBSOrig(src string) string {
	u, err := url.Parse(src)
	if err != nil {
		return src
	}

	// 1) pbs 链接：直接规范化为 name=orig
	if u.Host == "pbs.twimg.com" {
		q := u.Query()
		if q.Has("format") {
			q.Set("name", "orig")
			u.RawQuery = q.Encode()
			return u.String()
		}
		// 扩展名路径 -> 转成 query
		p := u.Path
		if i := strings.LastIndex(p, "."); i > 0 {
			ext := strings.ToLower(p[i+1:])
			switch ext {
			case "jpg", "jpeg", "png", "webp":
				u.Path = p[:i] // 去掉扩展名
				q.Set("format", ext)
				q.Set("name", "orig")
				u.RawQuery = q.Encode()
				return u.String()
			}
		}
		return src
	}

	// 2) nitter 镜像：尝试从 /pic/ 解码出 pbs 路径（常见：/pic/media%2F<id>...）
	if strings.Contains(u.Host, "nitter") {
		if strings.HasPrefix(u.Path, "/pic/") {
			tail := strings.TrimPrefix(u.Path, "/pic/")
			decoded, err := url.PathUnescape(tail) // "media%2FFoo.jpg?name=small" -> "media/Foo.jpg?name=small"
			if err == nil {
				var pathOnly, rawQ string
				if idx := strings.Index(decoded, "?"); idx >= 0 {
					pathOnly = decoded[:idx]
					rawQ = decoded[idx+1:]
				} else {
					pathOnly = decoded
				}
				pb := &url.URL{Scheme: "https", Host: "pbs.twimg.com", Path: "/" + strings.TrimPrefix(pathOnly, "/")}
				q2, _ := url.ParseQuery(rawQ)
				if q2.Has("format") {
					q2.Set("name", "orig")
					pb.RawQuery = q2.Encode()
				} else {
					if i := strings.LastIndex(pathOnly, "."); i > 0 {
						ext := strings.ToLower(pathOnly[i+1:])
						switch ext {
						case "jpg", "jpeg", "png", "webp":
							pb.Path = pathOnly[:i] // 去掉扩展名
							q2.Set("format", ext)
							q2.Set("name", "orig")
							pb.RawQuery = q2.Encode()
						}
					}
				}
				return pb.String()
			}
		}
		return src
	}

	return src
}

// ======== 按 orig/large/medium 下载到本地文件（兜底） ========

func tryDownloadBestImage(finalURL string) (string, error) {
	// 优先 orig
	candidates := []string{NormalizeMediaURLToPBSOrig(finalURL)}

	// 根据已有 query，兜底 large / medium
	if uu, err := url.Parse(finalURL); err == nil {
		q := uu.Query()
		if q.Has("name") {
			q.Set("name", "large")
			uu.RawQuery = q.Encode()
			candidates = append(candidates, uu.String())
			q.Set("name", "medium")
			uu.RawQuery = q.Encode()
			candidates = append(candidates, uu.String())
		}
	}

	var lastErr error
	for _, c := range candidates {
		path, err := downloadImageLocal(c)
		if err == nil {
			return path, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no candidates to download")
	}
	return "", lastErr
}

// ======== 下载图片到 ./res/<uuid>.<ext> 并异步清理 ========

func downloadImageLocal(imgURL string) (string, error) {
	var buf bytes.Buffer
	var hdr requests.RespHeader
	// 复用统一请求配置（代理、UA、Cookie、超时等）
	opts := SetRequestOptions()
	if err := requests.GetWithHeader(imgURL, nil, &buf, &hdr, opts...); err != nil {
		return "", err
	}

	if _, err := os.Stat("./res"); os.IsNotExist(err) {
		if err = os.MkdirAll("./res", 0755); err != nil {
			return "", err
		}
	}

	// 根据 Content-Type 或 URL 推断扩展名
	ext := "jpg"
	ct := strings.ToLower(hdr.ContentType)
	switch {
	case strings.Contains(ct, "png"):
		ext = "png"
	case strings.Contains(ct, "webp"):
		ext = "webp"
	case strings.Contains(ct, "jpeg"), strings.Contains(ct, "jpg"):
		ext = "jpg"
	}
	// 如果 URL 里有 format=xxx，以它为准
	if u, err := url.Parse(imgURL); err == nil {
		q := u.Query()
		if f := strings.ToLower(q.Get("format")); f != "" {
			switch f {
			case "png", "webp", "jpg", "jpeg":
				ext = f
			}
		} else {
			// 路径扩展名也可作为备用判断
			p := u.Path
			if i := strings.LastIndex(p, "."); i > 0 {
				e := strings.ToLower(p[i+1:])
				switch e {
				case "png", "webp", "jpg", "jpeg":
					ext = e
				}
			}
		}
	}

	filePath, _ := filepath.Abs("./res/" + uuid.New().String() + "." + ext)
	if err := os.WriteFile(filePath, buf.Bytes(), 0644); err != nil {
		return "", err
	}

	// 180 秒后清理临时文件（与你现有 downloadMedia 一致）
	go func(path string) {
		time.Sleep(time.Second * 180)
		logger.Debugf("Delete temporary image: %s", path)
		if err := os.Remove(path); err != nil {
			logger.WithField("filePath", path).
				Errorf("Delete temporary image error: %v", err)
		}
	}(filePath)

	return filePath, nil
}
