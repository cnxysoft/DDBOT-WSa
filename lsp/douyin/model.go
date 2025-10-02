package douyin

import (
	"fmt"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"sync"
)

type UserInfo struct {
	Uid       string `json:"uid"`
	SecUid    string `json:"secUid"`
	NikeName  string `json:"nickname"`
	RealName  string `json:"realName"`
	Desc      string `json:"desc"`
	WebRoomId string `json:"web_rid"`
}

func (u *UserInfo) GetUid() interface{} {
	if u == nil {
		return ""
	}
	return u.SecUid
}

func (u *UserInfo) GetName() string {
	if u == nil {
		return ""
	}
	return u.NikeName
}

func (u *UserInfo) SetRoomId(strId string) {
	if u == nil {
		return
	}
	u.WebRoomId = strId
}

func (u *UserInfo) GetRoomId() string {
	if u == nil {
		return ""
	}
	return u.WebRoomId
}

type LiveInfo struct {
	UserInfo
	IsLiving  bool  `json:"living"`
	GroupCode int64 `json:"group_code"`

	once              sync.Once
	msgCache          *mmsg.MSG
	liveTitleChanged  bool
	liveStatusChanged bool
}

func (l *LiveInfo) IsLive() bool {
	return true
}

func (l *LiveInfo) Living() bool {
	return l.IsLiving
}

func (l *LiveInfo) TitleChanged() bool {
	return l.liveTitleChanged
}

func (l *LiveInfo) LiveStatusChanged() bool {
	return l.liveStatusChanged
}

func (l *LiveInfo) Site() string {
	return Site
}

func (l *LiveInfo) Type() concern_type.Type {
	return Live
}

func (l *LiveInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site": Site,
		"Uid":  l.Uid,
		"Name": l.NikeName,
		"Room": l.WebRoomId,
		"Type": l.Type().String(),
	})
}

func (l *LiveInfo) GetMSG() *mmsg.MSG {
	l.once.Do(func() {
		var data = map[string]interface{}{
			"uid":        l.Uid,
			"name":       l.NikeName,
			"roomId":     l.WebRoomId,
			"living":     l.Living(),
			"url":        BaseLiveHost + "/" + l.WebRoomId,
			"group_code": l.GroupCode,
		}
		var err error
		l.msgCache, err = template.LoadAndExec("notify.group.douyin.live.tmpl", data)
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
	notify.LiveInfo.GroupCode = notify.GroupCode
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
		//dynamicUrl = DynamicUrl(notify.SecUid, card.GetAwemeId())
		//date       = localutils.TimestampFormat(int64(card.GetCreateTime()))
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
	notify.Card.GroupCode = notify.GroupCode
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
	return notify.SecUid
}

func (notify *ConcernNewsNotify) Logger() *logrus.Entry {
	if notify == nil {
		return logger
	}
	return logger.WithFields(localutils.GroupLogFields(notify.GroupCode)).
		WithFields(logrus.Fields{
			"Site":      Site,
			"Mid":       notify.SecUid,
			"Name":      notify.NikeName,
			"DynamicId": notify.Card.GetAwemeId(),
			"DescType":  "post",
			"Type":      notify.Type().String(),
		})
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

type CacheCard struct {
	*UserPostsResponse_AwemeList
	GroupCode int64
	once      sync.Once
	msgCache  *mmsg.MSG
	orgMsg    *message.GroupMessage
}

func (c *CacheCard) GetMSG() *mmsg.MSG {
	c.once.Do(func() {
		var data = map[string]interface{}{
			"dynamic":    c,
			"msg":        c.orgMsg,
			"name":       c.GetAuthor().GetNickname(),
			"desc":       c.GetDesc(),
			"date":       localutils.TimestampFormat(int64(c.GetCreateTime())),
			"cover":      c.GetVideo().GetCover().GetUrlList()[0],
			"url":        DynamicUrl(c.Author.SecUid, c.AwemeId),
			"group_code": c.GroupCode,
		}
		var err error
		c.msgCache, err = template.LoadAndExec("notify.group.douyin.news.tmpl", data)
		if err != nil {
			logger.Errorf("douyin: NewsInfo LoadAndExec error %v", err)
		}
		return
	})
	return c.msgCache
}

func NewCacheCard(card *UserPostsResponse_AwemeList) *CacheCard {
	cacheCard := new(CacheCard)
	cacheCard.UserPostsResponse_AwemeList = card
	return cacheCard
}

type NewsInfo struct {
	UserInfo
	LastDynamicId string                         `json:"last_dynamic_id"`
	Timestamp     int64                          `json:"timestamp"`
	Cards         []*UserPostsResponse_AwemeList `json:"-"`
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
		"Mid":      n.SecUid,
		"Name":     n.NikeName,
		"CardSize": len(n.Cards),
		"Type":     n.Type().String(),
	})
}

func NewNewsInfoWithDetail(userInfo *UserInfo, cards []*UserPostsResponse_AwemeList) *NewsInfo {
	var dynamicId string
	var timestamp int64
	if len(cards) > 0 {
		dynamicId = cards[0].GetAwemeId()
		timestamp = int64(cards[0].GetCreateTime())
	}
	return &NewsInfo{
		UserInfo:      *userInfo,
		LastDynamicId: dynamicId,
		Timestamp:     timestamp,
		Cards:         cards,
	}
}

func DynamicUrl(secUid, dynamicIdStr string) string {
	return fmt.Sprintf("%v%v?modal_id=%v", DPath(PathGetUserInfo), secUid, dynamicIdStr)
}
