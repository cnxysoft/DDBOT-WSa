package twitter

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"math/rand"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type UserProfile struct {
	ScreenName  string `json:"screen_name"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Following   int64  `json:"following"`
	Followers   int64  `json:"followers"`
	TweetsCount int64  `json:"tweets_count"`
	LikesCount  int64  `json:"likes_count"`
	AvatarURL   string `json:"avatar_url"`
	BannerURL   string `json:"banner_url"`
	Website     string `json:"website"`
	JoinedDate  string `json:"joined_date"`
}

type Tweet struct {
	ID         string       `json:"id"`
	Content    string       `json:"content"`
	CreatedAt  time.Time    `json:"created_at"`
	Likes      int64        `json:"likes"`
	Retweets   int64        `json:"retweets"`
	Pinned     bool         `json:"pinned"`
	Replies    int64        `json:"replies"`
	Media      []*Media     `json:"media"`
	IsRetweet  bool         `json:"is_retweet"`
	OrgUser    *UserProfile `json:"org_user"`
	Url        string       `json:"url"`
	MirrorHost string       `json:"mirror_url"`
	QuoteTweet *Tweet       `json:"quote_tweet"`
}

type Media struct {
	Type string `json:"type"`
	Url  string `json:"url"`
}

type AnubisChallenge struct {
	Rules struct {
		Algorithm  string `json:"algorithm"`
		Difficulty int    `json:"difficulty"`
		ReportAs   int    `json:"report_as"`
	} `json:"rules"`
	Challenge any `json:"challenge"`
}

type AnubisChallengeSub struct {
	Id       string    `json:"id"`
	IssuedAt time.Time `json:"issuedAt"`
	Metadata struct {
		UserAgent string `json:"User-Agent"`
		XRealIp   string `json:"X-Real-Ip"`
	} `json:"metadata"`
	Method     string `json:"method"`
	RandomData string `json:"randomData"`
}

type AnubisResult struct {
	Hash  string
	Nonce int
	Time  int
	Host  string
	Id    string
}

func (t *Tweet) RtType() int {
	if t.IsRetweet {
		return RETWEET
	} else {
		return TWEET
	}
}

func (t *Tweet) IsPinned() bool {
	if t.Pinned {
		return true
	} else {
		return false
	}
}

func GetIdList(tweets []*Tweet) []string {
	var idList []string
	for t := range tweets {
		idList = append(idList, tweets[t].ID)
	}
	return idList
}

func ParseResp(htmlContent []byte, Url string) (*UserProfile, []*Tweet, *AnubisResult, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(htmlContent))
	if err != nil {
		return nil, nil, nil, err
	}
	parsedURL, _ := url.Parse(Url)

	title := doc.Find("title").Text()
	if title == "Just a moment..." {
		return nil, nil, nil, errors.New("cf_clearance has expired!")
	} else if strings.HasPrefix(title, "Error") {
		message := doc.Find("div[class='error-panel']").Text()
		if strings.Contains(message, "suspended") {
			return nil, nil, nil, errors.New(message)
		}
		return nil, nil, nil, errors.New("Twitter has been Error.")
	} else if strings.HasPrefix(title, "正在确认你是不是机器人！") {
		challengeJson := doc.Find("script[id='anubis_challenge']").Text()
		challenge := new(AnubisChallenge)
		var challengeTarget string
		var challengeSub *AnubisChallengeSub
		err = json.Unmarshal([]byte(challengeJson), &challenge)
		if err != nil {
			return nil, nil, nil, err
		}
		switch challenge.Challenge.(type) {
		case string:
			challengeTarget = challenge.Challenge.(string)
		case map[string]interface{}:
			marshal, err := json.Marshal(challenge.Challenge)
			if err != nil {
				return nil, nil, nil, err
			}
			err = json.Unmarshal(marshal, &challengeSub)
			if err != nil {
				return nil, nil, nil, err
			}
			challengeTarget = challengeSub.RandomData
		}
		nonce, hash := ComputePoW(challengeTarget, challenge.Rules.Difficulty, challenge.Rules.Algorithm)
		result := &AnubisResult{
			Hash:  hash,
			Nonce: nonce,
			Time:  rand.Intn(100),
			Host:  parsedURL.Hostname(),
		}
		if challengeSub != nil {
			result.Id = challengeSub.Id
		}
		return nil, nil, result, nil
	}

	// 解析用户基本信息
	var profile UserProfile
	profile.Website = XUrl + doc.Find("a[class='profile-card-username']").AttrOr("href", "")
	doc.Find("meta[property='og:title']").Each(func(i int, s *goquery.Selection) {
		title := s.AttrOr("content", "")
		parts := strings.Split(title, " (")
		if len(parts) > 0 {
			profile.Name = strings.TrimSpace(parts[0])
			if len(parts) > 1 {
				profile.ScreenName = strings.Trim(parts[1], "@)")
			}
		}
	})

	// 解析用户说明
	doc.Find("meta[property='og:description']").Each(func(i int, s *goquery.Selection) {
		if description := s.AttrOr("content", ""); description != "" {
			profile.Description = description
		}
	})
	// 解析头像和横幅
	doc.Find("link[rel='preload'][as='image']").Each(func(i int, img *goquery.Selection) {
		if src := img.AttrOr("href", ""); i == 0 && src != "" {
			profile.BannerURL = src
		}
		if src := img.AttrOr("href", ""); i == 1 && src != "" {
			profile.AvatarURL = src
		}
	})
	// 解析加入日期
	profile.JoinedDate = strings.Trim(
		doc.Find("div[class='profile-joindate'] div.icon-container").Text(),
		"Joined ")

	// 解析统计数据
	doc.Find(".profile-statlist li").Each(func(i int, s *goquery.Selection) {
		statType := s.AttrOr("class", "")
		valueStr := s.Find(".profile-stat-num").Text()
		value, _ := strconv.ParseInt(strings.ReplaceAll(valueStr, ",", ""), 10, 64)

		switch statType {
		case "posts":
			profile.TweetsCount = value
		case "following":
			profile.Following = value
		case "followers":
			profile.Followers = value
		case "likes":
			profile.LikesCount = value
		}
	})

	// 解析推文列表
	var tweets []*Tweet
	doc.Find(".timeline-item").Each(func(i int, item *goquery.Selection) {
		tweet := Tweet{
			MirrorHost: parsedURL.Hostname(),
		}

		// 解析基础信息
		tweet.ID = ExtractTweetID(item.Find(".tweet-link").AttrOr("href", ""))
		tweet.Content = strings.TrimSpace(item.Find(".tweet-content").Text())

		// 解析时间
		timeStr := item.Find(".tweet-date a").AttrOr("title", "")
		// 原格式：Mon Jan 2 15:04:05 -0700 MST 2006 的变体
		if parsedTime, err := time.Parse("Jan 2, 2006 · 3:04 PM MST", timeStr); err == nil {
			tweet.CreatedAt = parsedTime
		}

		// 解析互动数据
		item.Find(".tweet-stat").Each(func(i int, s *goquery.Selection) {
			count, err := strconv.ParseInt(
				strings.ReplaceAll(strings.TrimSpace(s.Text()), ",", ""), 10, 64)
			if err != nil {
				count = 0
			}
			htmlContent, _ := s.Html() // 获取HTML内容并显式忽略错误
			switch {
			case strings.Contains(htmlContent, "icon-heart"):
				tweet.Likes = count
			case strings.Contains(htmlContent, "icon-retweet"):
				tweet.Retweets = count
			case strings.Contains(htmlContent, "icon-comment"):
				tweet.Replies = count
			}
		})

		// 解析图片
		item.Find(".tweet-body > .attachments div[class='attachment image'] img").Each(func(i int, img *goquery.Selection) {
			if src := img.AttrOr("src", ""); src != "" {
				tweet.Media = append(tweet.Media, &Media{
					Type: "image",
					Url:  src,
				})
			}
		})

		// 解析GIF
		item.Find(".tweet-body > .attachments .gallery-gif source").Each(func(i int, gif *goquery.Selection) {
			if src := gif.AttrOr("src", ""); src != "" {
				tweet.Media = append(tweet.Media, &Media{
					Type: "gif",
					Url:  src,
				})
			}
		})

		// 解析视频
		item.Find(".tweet-body > .attachments .gallery-video source").Each(func(i int, video *goquery.Selection) {
			if src := video.AttrOr("src", ""); src != "" {
				tweet.Media = append(tweet.Media, &Media{
					Type: "video",
					Url:  src,
				})
			}
		})

		// 解析视频(m3u8)
		item.Find(".tweet-body > .attachments .gallery-video video").Each(func(i int, video *goquery.Selection) {
			if dataUrl := video.AttrOr("data-url", ""); dataUrl != "" {
				tweet.Media = append(tweet.Media, &Media{
					Type: "video(m3u8)",
					Url:  dataUrl,
				})
			}
		})

		// 判断是否置顶
		tweet.Pinned = item.Find(".pinned").Length() > 0

		// 判断是否转推
		tweet.IsRetweet = item.Find(".retweet-header").Length() > 0
		if tweet.IsRetweet {
			// 解析被转推用户基本信息
			var reProfile UserProfile
			item.Find(".fullname-and-username a").Each(func(i int, s *goquery.Selection) {
				if i == 0 {
					reProfile.Name = strings.TrimSpace(s.Text())
				} else {
					reProfile.ScreenName = strings.Trim(s.Text(), "@)")
					reProfile.Website = XUrl + "/" +
						strings.Trim(item.Find(".timeline-item .username").
							AttrOr("title", ""), "@")
				}
			})
			// 添加原推主
			tweet.OrgUser = &reProfile
			// 添加URL
			tweet.Url = XUrl + strings.TrimRight(item.Find(".tweet-link").AttrOr("href", ""), "#m")
		} else {
			// 添加URL
			tweet.Url = XUrl + "/" + profile.ScreenName + "/status/" + tweet.ID
		}

		// 解析引用推文
		item.Find(".quote-big").Each(func(i int, s *goquery.Selection) {
			QuoteTweet := &Tweet{
				MirrorHost: parsedURL.Hostname(),
			}
			// 解析推文ID
			QuoteTweet.ID = ExtractTweetID(item.Find(".quote-link").AttrOr("href", ""))
			// 解析时间
			timeStr := s.Find(".tweet-date a").AttrOr("title", "")
			// 原格式：Mon Jan 2 15:04:05 -0700 MST 2006 的变体
			if parsedTime, err := time.Parse("Jan 2, 2006 · 3:04 PM MST", timeStr); err == nil {
				QuoteTweet.CreatedAt = parsedTime
			}

			// 解析用户基本信息
			var QuoteUser UserProfile
			s.Find(".fullname-and-username a").Each(func(i int, s *goquery.Selection) {
				if i == 0 {
					QuoteUser.Name = strings.TrimSpace(s.Text())
				} else {
					QuoteUser.ScreenName = strings.Trim(s.Text(), "@)")
					QuoteUser.Website = XUrl + "/" +
						strings.Trim(item.Find(".timeline-item .username").
							AttrOr("title", ""), "@")
				}
			})
			// 添加原推主
			QuoteTweet.OrgUser = &QuoteUser
			// 解析内容
			QuoteTweet.Content = strings.TrimSpace(s.Find(".quote-text").Text())

			// 解析图片
			s.Find("div[class='attachment image'] img").Each(func(i int, img *goquery.Selection) {
				if src := img.AttrOr("src", ""); src != "" {
					QuoteTweet.Media = append(QuoteTweet.Media, &Media{
						Type: "image",
						Url:  src,
					})
				}
			})

			// 解析GIF
			s.Find(".gallery-gif source").Each(func(i int, gif *goquery.Selection) {
				if src := gif.AttrOr("src", ""); src != "" {
					QuoteTweet.Media = append(QuoteTweet.Media, &Media{
						Type: "gif",
						Url:  src,
					})
				}
			})

			// 解析视频
			s.Find(".gallery-video source").Each(func(i int, video *goquery.Selection) {
				if src := video.AttrOr("src", ""); src != "" {
					QuoteTweet.Media = append(QuoteTweet.Media, &Media{
						Type: "video",
						Url:  src,
					})
				}
			})

			// 解析视频(m3u8)
			s.Find(".gallery-video video").Each(func(i int, video *goquery.Selection) {
				if dataUrl := video.AttrOr("data-url", ""); dataUrl != "" {
					QuoteTweet.Media = append(QuoteTweet.Media, &Media{
						Type: "video(m3u8)",
						Url:  dataUrl,
					})
				}
			})
			tweet.QuoteTweet = QuoteTweet
		})
		if tweet.QuoteTweet != nil && tweet.OrgUser == nil {
			tweet.OrgUser = &UserProfile{
				Name:       profile.Name,
				ScreenName: profile.ScreenName,
				Website:    profile.Website,
			}
		}
		tweets = append(tweets, &tweet)
	})
	return &profile, tweets, nil, nil
}

// 计算工作量证明（支持两种算法）
func ComputePoW(challenge string, difficulty int, algorithm string) (nonce int, hash string) {
	switch algorithm {
	case "fast":
		return computeFastPoW(challenge, difficulty)
	case "slow":
		fallthrough
	default:
		return computeSlowPoW(challenge, difficulty)
	}
}

// 实现 SLOW 算法（检查完整字符）
func computeSlowPoW(challenge string, difficulty int) (nonce int, hash string) {
	prefix := strings.Repeat("0", difficulty)
	nonce = 0

	for {
		data := challenge + fmt.Sprintf("%d", nonce)
		sum := sha256.Sum256([]byte(data))
		hash = hex.EncodeToString(sum[:])

		if strings.HasPrefix(hash, prefix) {
			return nonce, hash
		}
		nonce++
	}
}

// 实现 FAST 算法（检查半字节）
func computeFastPoW(challenge string, difficulty int) (nonce int, hash string) {
	nonce = 0

	for {
		data := challenge + fmt.Sprintf("%d", nonce)
		sum := sha256.Sum256([]byte(data))

		// 检查是否满足难度要求
		if checkNibbles(sum, difficulty) {
			hash = hex.EncodeToString(sum[:])
			return nonce, hash
		}
		nonce++
	}
}

// 检查前N个半字节是否为0
func checkNibbles(hash [32]byte, difficulty int) bool {
	nibblesChecked := 0

	for _, b := range hash {
		// 检查高4位（第一个半字节）
		if (b >> 4) != 0 {
			return false
		}
		nibblesChecked++
		if nibblesChecked >= difficulty {
			return true
		}

		// 检查低4位（第二个半字节）
		if (b & 0x0F) != 0 {
			return false
		}
		nibblesChecked++
		if nibblesChecked >= difficulty {
			return true
		}
	}

	return false
}

func FreshCookie(anubis *AnubisResult) {
	opts := []requests.Option{
		requests.RequestAutoHostOption(),
		requests.ProxyOption(proxy_pool.PreferOversea),
		requests.AddUAOption(UserAgent),
		requests.RetryOption(3),
		requests.WithCookieJar(Cookie),
		requests.HeaderOption("accept-language", "zh-CN,zh;q=0.9"),
	}
	var addAnubisId string
	if anubis.Id != "" {
		addAnubisId = "id=" + anubis.Id + "&"
	}
	path := fmt.Sprintf("https://%s/.within.website/x/cmd/anubis/api/pass-challenge?"+
		"%sresponse=%s&nonce=%d&redir=https://%s/&elapsedTime=%d", anubis.Host, addAnubisId, anubis.Hash, anubis.Nonce, url.QueryEscape(anubis.Host), anubis.Time)
	var resp bytes.Buffer
	err := requests.Get(path, nil, &resp, opts...)
	if err != nil {
		logger.Errorf("twitter: fresh %s cookie error %v", anubis.Host, err)
	}
}
