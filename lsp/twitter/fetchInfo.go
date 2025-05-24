package twitter

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"errors"
	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/brotli"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
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
	Replies    int64        `json:"replies"`
	Media      []*Media     `json:"media"`
	IsRetweet  bool         `json:"is_retweet"`
	OrgUser    *UserProfile `json:"org_user"`
	Url        string       `json:"url"`
	MirrorHost string       `json:"mirror_url"`
}

type Media struct {
	Type string `json:"type"`
	Url  string `json:"url"`
}

func (t *Tweet) RtType() int {
	if t.IsRetweet {
		return RETWEET
	} else {
		return TWEET
	}
}

func ParseResp(htmlContent []byte, Url string) (*UserProfile, []Tweet, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(htmlContent))
	if err != nil {
		return nil, nil, err
	}

	title := doc.Find("title").Text()
	if title == "Just a moment..." {
		return nil, nil, errors.New("cf_clearance has expired!")
	} else if strings.HasPrefix(title, "Error") {
		message := doc.Find("div[class='error-panel']").Text()
		if strings.Contains(message, "suspended") {
			return nil, nil, errors.New(message)
		}
		return nil, nil, errors.New("Twitter has been Error.")
	}

	parsedURL, _ := url.Parse(Url)

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
	var tweets []Tweet
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
		item.Find("div[class='attachment image'] img").Each(func(i int, img *goquery.Selection) {
			if src := img.AttrOr("src", ""); src != "" {
				tweet.Media = append(tweet.Media, &Media{
					Type: "image",
					Url:  src,
				})
			}
		})

		// 解析GIF
		item.Find(".gallery-gif video").Each(func(i int, img *goquery.Selection) {
			if src := img.AttrOr("src", ""); src != "" {
				tweet.Media = append(tweet.Media, &Media{
					Type: "gif",
					Url:  src,
				})
			}
		})

		// 解析视频
		item.Find(".gallery-video video").Each(func(i int, img *goquery.Selection) {
			if src := img.AttrOr("src", ""); src != "" {
				tweet.Media = append(tweet.Media, &Media{
					Type: "video",
					Url:  src,
				})
			}
		})

		// 解析视频(m3u8)
		item.Find(".gallery-video video").Each(func(i int, img *goquery.Selection) {
			if dataUrl := img.AttrOr("data-url", ""); dataUrl != "" {
				tweet.Media = append(tweet.Media, &Media{
					Type: "video(m3u8)",
					Url:  dataUrl,
				})
			}
		})

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
		tweets = append(tweets, tweet)
	})
	return &profile, tweets, nil
}

// 解压HTTP数据
func decompressGzip(data []byte) ([]byte, error) {
	var b bytes.Buffer
	r, _ := gzip.NewReader(bytes.NewReader(data))
	_, _ = io.Copy(&b, r)
	r.Close()
	return b.Bytes(), nil
}

func decompressDeflate(data []byte) ([]byte, error) {
	reader, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

func decompressBrotli(data []byte) ([]byte, error) {
	reader := brotli.NewReader(bytes.NewReader(data))
	return io.ReadAll(reader)
}

func decompressZstd(data []byte) ([]byte, error) {
	dctx, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer dctx.Close()
	return io.ReadAll(dctx)
}
