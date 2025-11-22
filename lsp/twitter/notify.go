package twitter

import (
	"bytes"
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
	defer func() {
		if err := recover(); err != nil {
			logger.WithField("stack", string(debug.Stack())).
				WithField("tweet", n.Tweet).
				Errorf("concern notify recoverd %v", err)
		}
	}()
	m = mmsg.NewMSG()
	var addedUrl bool
	if n.shouldCompact {
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
			return
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
	return
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
