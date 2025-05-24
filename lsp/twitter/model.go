package twitter

import (
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/mmcdole/gofeed"
	"github.com/sirupsen/logrus"
	"strconv"
	"strings"
	"time"
)

// 这里面可以定义推送中使用的结构

type NewsInfo struct {
	*UserInfo
	Tweet         Tweet
	LatestNewsTs  time.Time
	LatestTweetId string
}

func (e *NewsInfo) Site() string {
	return Site
}

func (e *NewsInfo) Type() concern_type.Type {
	return Tweets
}

func (e *NewsInfo) GetUid() interface{} {
	return e.UserInfo.Id
}

func (e *NewsInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site": e.Site(),
		"Type": e.Type(),
		"Uid":  e.GetUid(),
		"Name": e.UserInfo.Name,
	})
}

func (e *NewsInfo) GetLatestTweetTs() time.Time {
	ts, err := ParseSnowflakeTimestamp(e.LatestTweetId)
	if err != nil {
		e.Logger().WithError(err).Error("ParseSnowflakeTimestamp")
		return time.Time{}
	}
	return ts
}

type UserInfo struct {
	Id              string
	Name            string
	ProfileImageUrl string
}

func (u *UserInfo) GetUid() interface{} {
	return u.Id
}

func (u *UserInfo) GetName() string {
	return u.Name
}

func GetShortName(feed *gofeed.Feed) string {
	//if strings.HasPrefix(feed.Title, "twitter ") {
	//	return strings.Split(feed.Title, " ")[1]
	//}
	if idx := strings.LastIndex(feed.Title, "/ @"); idx != -1 {
		return strings.TrimSpace(feed.Title[:idx])
	}
	return feed.Title
}

const (
	TWEET   = 1
	RETWEET = 2
)

type TweetItem struct {
	Type        int
	Title       string
	Description string
	Link        string
	Media       []string
	Published   time.Time
	Author      *UserInfo
}

func (e *TweetItem) GetId() string {
	return ExtractTweetID(e.Link)
}

func ExtractTweetID(url string) string {
	// 分割URL路径
	parts := strings.Split(url, "/")
	for i, part := range parts {
		if part == "status" && i+1 < len(parts) {
			// 去除可能的锚点或参数
			if hashIndex := strings.Index(parts[i+1], "#"); hashIndex != -1 {
				return parts[i+1][:hashIndex]
			}
			return parts[i+1]
		}
	}
	return ""
}

// 雪花ID解析参数（需与生成器配置保持一致）
const (
	epoch              = int64(1288834974657)                           // 起始时间戳（毫秒）
	datacenterIdBits   = uint(5)                                        // 数据中心位数
	workerIdBits       = uint(5)                                        // 工作节点位数
	sequenceBits       = uint(12)                                       // 序列号位数
	timestampLeftShift = datacenterIdBits + workerIdBits + sequenceBits // 时间戳偏移量
)

// ParseSnowflakeTimestamp 解析雪花ID中的时间戳
func ParseSnowflakeTimestamp(id string) (time.Time, error) {
	Id, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	timestamp := (Id >> timestampLeftShift) + epoch
	return time.UnixMilli(timestamp), nil
}
