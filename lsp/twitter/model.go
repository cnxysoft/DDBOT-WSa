package twitter

import (
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/sirupsen/logrus"
)

const MaxLatestWteetIds = 20

type NewsInfo struct {
	*UserInfo
	dynamic TwitterDynamic
	Tweet   *Tweet
	once    sync.Once
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

func (e *NewsInfo) GetMSG(n *ConcernNewsNotify) (m *mmsg.MSG) {
	defer func() {
		if err := recover(); err != nil {
			logger.WithField("stack", string(debug.Stack())).
				WithField("tweet", n.Tweet).
				Errorf("concern notify recoverd %v", err)
		}
	}()

	e.once.Do(func() {
		// 构造TwitterDynamic数据
		e.dynamic = n.buildTwitterDynamic()
		return
	})
	var msg *message.GroupMessage
	if n.shouldCompact {
		// 先推送了转发，才推送原文
		// 这种直接放弃，避免二次推送
		if n.Tweet.OrgUser == nil && n.Tweet.QuoteTweet == nil {
			logger.Debug("compact notify ignored: already pushed.")
		} else {
			// 通过回复之前消息的方式简化推送
			msg, _ = n.concern.GetNotifyMsg(n.GroupCode, n.compactKey)
		}
	}

	// 使用模板渲染消息
	var data = map[string]interface{}{
		"dynamic": e.dynamic,
		"msg":     msg,
	}

	var err error
	m, err = template.LoadAndExec("notify.group.twitter.news.tmpl", data)
	if err != nil {
		logger.Errorf("twitter: NewsInfo LoadAndExec error %v", err)
		// 如果模板加载失败，回退到默认消息
		m = n.fallbackMSG()
	}
	return
}

type LatestTweetIds struct {
	TweetId  []string
	PinnedId string
}

func (l *LatestTweetIds) GetLatestTweetTs() *time.Time {
	if len(l.TweetId) == 0 {
		return nil
	}
	ts, err := ParseSnowflakeTimestamp(l.TweetId[0])
	if err != nil {
		logger.WithError(err).Error("ParseSnowflakeTimestamp")
		return &time.Time{}
	}
	return &ts
}

func (l *LatestTweetIds) GetLatestTweetId() string {
	if l == nil || len(l.TweetId) == 0 {
		return ""
	}
	return l.TweetId[len(l.TweetId)-1]
}

func (l *LatestTweetIds) SetLatestTweetId(tweetId string) {
	if l == nil {
		return
	}
	po := l.HasTweetId(tweetId)
	if po != -1 {
		var tmpLatestTweetId []string
		for i := range l.TweetId {
			if i == po {
				continue
			}
			tmpLatestTweetId = append(tmpLatestTweetId, l.TweetId[i])
		}
		l.SetLatestTweetIds(tmpLatestTweetId)
	}
	if len(l.TweetId) >= MaxLatestWteetIds {
		l.TweetId = l.TweetId[1:len(l.TweetId)]
	}
	l.TweetId = append(l.TweetId, tweetId)
}

func (l *LatestTweetIds) SetLatestTweetIds(tweets []string) {
	if l == nil {
		return
	}
	l.TweetId = tweets
}

func (l *LatestTweetIds) HasTweetId(tweetId string) int {
	if l == nil {
		return -1
	}
	for i, TweetId := range l.TweetId {
		if TweetId == tweetId {
			return i
		}
	}
	return -1
}

func (l *LatestTweetIds) SetPinnedTweet(tweetId string) bool {
	if l == nil || tweetId == "" {
		logger.Debug("SetPinnedTweet failed: Empty tweetId")
		return false
	}
	l.PinnedId = tweetId
	return true
}

func (l *LatestTweetIds) GetPinnedTweet() string {
	if l == nil {
		return ""
	}
	return l.PinnedId
}

type UserInfo struct {
	Id   string
	Name string
}

func (u *UserInfo) GetUid() interface{} {
	return u.Id
}

func (u *UserInfo) GetName() string {
	return u.Name
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
