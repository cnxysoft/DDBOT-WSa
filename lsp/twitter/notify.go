package twitter

import (
	"fmt"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/google/uuid"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
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
		unescape := m.Url
		if strings.HasPrefix(unescape, "/") {
			Url, err := setMirrorHost(tweet.MirrorHost, *m)
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
				switch m.Type {
				case "image":
					if tweet.MirrorHost == XImgHost {
						unescape = strings.TrimLeft(unescape, "/pic/")
					}
					fullURL, err := Url.Parse(unescape)
					if err != nil {
						logger.WithField("stack", string(debug.Stack())).
							WithField("tweetId", tweet.ID).
							Errorf("concern notify recoverd %v", err)
					}
					m.Url = fullURL.String()
					addCut(message, nil)
					message.Append(
						mmsg.NewImageByUrl(m.Url,
							requests.ProxyOption(proxy_pool.PreferOversea),
							requests.AddUAOption(UserAgent),
							requests.WithCookieJar(Cookie)))
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
		err = convMediaWithProxy(Url, filePath, proxyStr, fileExt)
	} else {
		err = convMediaWithProxy(Url, filePath, proxyStr, fileExt)
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

func convMediaWithProxy(Url, outputPath, proxyURL, Type string) error {
	args := []string{
		"-v", "error",
		"-i", Url,
		"-f", Type,
		outputPath,
	}

	if Type == "mp4" {
		args = []string{
			"-v", "error",
			"-i", Url,
			"-c", "copy",
			"-movflags",
			"+faststart",
			"-f", Type,
			outputPath,
		}
	}

	cmd := exec.Command("ffmpeg", args...)
	if proxyURL != "" {
		cmd.Env = append(os.Environ(), "http_proxy="+proxyURL, "https_proxy="+proxyURL, "rw_timeout=30000000")
	}

	cmd.Stdout = nil
	cmd.Stderr = os.Stderr

	return cmd.Run()
}

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

func setMirrorHost(mirrorHost string, m Media) (url.URL, error) {
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
	return Url, nil
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
