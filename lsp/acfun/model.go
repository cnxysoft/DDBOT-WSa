package acfun

import (
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"strconv"
	"sync"
)

type UserInfo struct {
	Uid      int64  `json:"uid"`
	Name     string `json:"name"`
	Followed int    `json:"followed"`
	UserImg  string `json:"user_img"`
	LiveUrl  string `json:"live_url"`
}

func (u *UserInfo) GetUid() interface{} {
	return u.Uid
}

func (u *UserInfo) GetName() string {
	return u.Name
}

type LiveInfo struct {
	UserInfo
	LiveId   string `json:"live_id"`
	Title    string `json:"title"`
	Cover    string `json:"cover"`
	StartTs  int64  `json:"start_ts"`
	IsLiving bool   `json:"living"`

	once              sync.Once
	msgCache          *mmsg.MSG
	liveStatusChanged bool
	liveTitleChanged  bool
}

func (l *LiveInfo) IsLive() bool {
	return true
}

func (l *LiveInfo) Living() bool {
	return l.IsLiving
}

func (l *LiveInfo) LiveStatusChanged() bool {
	return l.liveStatusChanged
}

func (l *LiveInfo) TitleChanged() bool {
	return l.liveTitleChanged
}

func (l *LiveInfo) Site() string {
	return Site
}

func (l *LiveInfo) Type() concern_type.Type {
	return Live
}

func (l *LiveInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site":  Site,
		"Uid":   l.Uid,
		"Name":  l.Name,
		"Title": l.Title,
		"Type":  l.Type().String(),
	})
}

func (l *LiveInfo) GetMSG() *mmsg.MSG {
	l.once.Do(func() {
		var data = map[string]interface{}{
			"title":  l.Title,
			"name":   l.Name,
			"url":    l.LiveUrl,
			"cover":  l.Cover,
			"living": l.Living(),
		}
		var err error
		l.msgCache, err = template.LoadAndExec("notify.group.acfun.live.tmpl", data)
		if err != nil {
			logger.Errorf("acfun: LiveInfo LoadAndExec error %v", err)
		}
		return
	})
	return l.msgCache
}

type ConcernLiveNotify struct {
	GroupCode int64
	*LiveInfo
}

func (notify *ConcernLiveNotify) GetGroupCode() int64 {
	return notify.GroupCode
}

func (notify *ConcernLiveNotify) ToMessage() (m *mmsg.MSG) {
	return notify.LiveInfo.GetMSG()
}

func (notify *ConcernLiveNotify) Logger() *logrus.Entry {
	if notify == nil {
		return logger
	}
	return notify.LiveInfo.Logger().WithFields(localutils.GroupLogFields(notify.GroupCode))
}

func NewConcernLiveNotify(groupCode int64, info *LiveInfo) *ConcernLiveNotify {
	return &ConcernLiveNotify{
		GroupCode: groupCode,
		LiveInfo:  info,
	}
}

type NewsInfo struct {
	UserInfo
	LastDynamicId int64       `json:"last_dynamic_id"`
	Timestamp     int64       `json:"timestamp"`
	Cards         []*FeedItem `json:"-"`
}

func (n *NewsInfo) Site() string {
	return Site
}

func (n *NewsInfo) Type() concern_type.Type {
	return News
}

func (n *NewsInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site":     Site,
		"Uid":      n.Uid,
		"Name":     n.Name,
		"CardSize": len(n.Cards),
		"Type":     n.Type().String(),
	})
}

type ConcernNewsNotify struct {
	GroupCode int64 `json:"group_code"`
	*UserInfo
	Card *CacheCard

	// 用于联合投稿和转发的时候防止多人同时推送
	shouldCompact bool
	compactKey    string
	concern       *Concern
}

func (notify *ConcernNewsNotify) IsLive() bool {
	return false
}

func (notify *ConcernNewsNotify) Living() bool {
	return false
}

func (notify *ConcernNewsNotify) ToMessage() (m *mmsg.MSG) {
	var (
		card = notify.Card
		log  = notify.Logger()
	)
	// 推送一条简化动态防止刷屏，主要是联合投稿和转发的时候
	if notify.shouldCompact {
		// 通过回复之前消息的方式简化推送
		m = mmsg.NewMSG()
		msg, _ := notify.concern.GetNotifyMsg(notify.GroupCode, notify.compactKey)
		if msg != nil {
			card.orgMsg = msg
		}
		log.WithField("compact_key", notify.compactKey).Debug("compact notify")
	}
	m = notify.Card.GetMSG()
	return
}

func (notify *ConcernNewsNotify) Type() concern_type.Type {
	return News
}

func (notify *ConcernNewsNotify) Site() string {
	return Site
}

func (notify *ConcernNewsNotify) GetGroupCode() int64 {
	return notify.GroupCode
}

func (notify *ConcernNewsNotify) GetUid() interface{} {
	return notify.Uid
}

func (notify *ConcernNewsNotify) Logger() *logrus.Entry {
	if notify == nil {
		return logger
	}
	return logger.WithFields(localutils.GroupLogFields(notify.GroupCode)).
		WithFields(logrus.Fields{
			"Site":      Site,
			"Uid":       notify.Uid,
			"Name":      notify.Name,
			"DynamicId": notify.Card.GetMoment().GetMomentId(),
			"IsRePost":  notify.Card.GetRepostSource() != nil,
			"Type":      notify.Type().String(),
		})
}

type CacheCard struct {
	*FeedItem
	once     sync.Once
	msgCache *mmsg.MSG
	orgMsg   *message.GroupMessage
}

func (c *CacheCard) GetMSG() *mmsg.MSG {
	c.once.Do(func() {
		var url string
		resourceId := strconv.FormatInt(c.GetResourceId(), 10)
		if c.GetVideoId() != "" {
			url = VideoUrl(resourceId)
		} else if c.GetMoment() != nil {
			url = DynamicUrl(resourceId)
		}
		var data = map[string]interface{}{
			"dynamic": c.FeedItem,
			"msg":     c.orgMsg,
			"name":    c.GetUser().GetUserName(),
			"date":    localutils.NTimestampFormat(c.GetCreateTime()),
			"cover":   c.GetCoverUrl(),
			"moment":  c.GetMoment(),
			"repost":  c.GetRepostSource(),
			"url":     url,
		}
		var err error
		c.msgCache, err = template.LoadAndExec("notify.group.acfun.news.tmpl", data)
		if err != nil {
			logger.Errorf("acfun: NewsInfo LoadAndExec error %v", err)
		}
		return
	})
	return c.msgCache
}

func NewCacheCard(card *FeedItem) *CacheCard {
	cacheCard := new(CacheCard)
	cacheCard.FeedItem = card
	return cacheCard
}

func NewNewsInfoWithDetail(userInfo *UserInfo, cards []*FeedItem) *NewsInfo {
	var dynamicId int64
	var timestamp int64
	if len(cards) > 0 {
		dynamicId = cards[0].GetResourceId()
		timestamp = cards[0].GetCreateTime()
	}
	return &NewsInfo{
		UserInfo:      *userInfo,
		LastDynamicId: dynamicId,
		Timestamp:     timestamp,
		Cards:         cards,
	}
}

func NewConcernNewsNotify(groupCode int64, newsInfo *NewsInfo, c *Concern) []*ConcernNewsNotify {
	if newsInfo == nil {
		return nil
	}
	var result []*ConcernNewsNotify
	for _, card := range newsInfo.Cards {
		result = append(result, &ConcernNewsNotify{
			GroupCode: groupCode,
			UserInfo:  &newsInfo.UserInfo,
			Card:      NewCacheCard(card),
			concern:   c,
		})
	}
	return result
}
