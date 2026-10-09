package douyin

import (
	"fmt"
	"sync"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
)

type UserInfo struct {
	Uid       string `json:"uid"`
	SecUid    string `json:"sec_uid"`
	Gender    int32  `json:"gender"`
	NikeName  string `json:"nickname"`
	AvatarUrl string `json:"avatar_url"`
	DisplayId string `json:"display_id"`
	Signature string `json:"signature"`
	WebRoomId string `json:"web_rid"`
	// 成品 reflect 元数据（types-closure-x64.tsv:10735）size=152：
	// LastLiveTitle string `json:"last_live_title,omitempty"` @+120、
	// LastLiveCover string `json:"last_live_cover,omitempty"` @+136。
	LastLiveTitle string `json:"last_live_title,omitempty"`
	LastLiveCover string `json:"last_live_cover,omitempty"`
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
	IsLiving  bool   `json:"living"`
	GroupCode int64  `json:"group_code"`
	Cover     string `json:"cover"`
	LiveTitle string `json:"live_title"`

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
			"title":      l.LiveTitle,
			"cover":      l.Cover,
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
			Card:      NewCacheCard(card, newsInfo.SecUid),
			concern:   c,
		})
	}
	return result
}

type CacheCard struct {
	*UserPostsResponse_AwemeList
	GroupCode int64
	// 成品 reflect 元数据（types-closure-x64.tsv:11057）size=64：
	// FallbackSecUID string @+16（在 GroupCode@8 与 once@32 之间）。
	FallbackSecUID string
	once           sync.Once
	msgCache       *mmsg.MSG
	orgMsg         *adapter.GroupMessage
}

func (c *CacheCard) GetMSG() *mmsg.MSG {
	c.once.Do(func() {
		// 图集/图片扩展元数据（成品 (*CacheCard).GetMSG.func1 调用
		// douyinPostImages / douyinPostGalleryItems，模板消费 .pictures/.gallery/.live_photos）。
		pictures := douyinPostImages(c.AwemeId)
		gallery := douyinPostGalleryItems(c.AwemeId)
		livePhotos := make([]string, 0, len(gallery))
		for _, item := range gallery {
			if item.VideoURL != "" {
				livePhotos = append(livePhotos, item.VideoURL)
			}
		}
		// 成品 (*CacheCard).GetMSG.func1 @0xd93108-d93120：取 c.Author.SecUid
		// （Author 结构 SecUid 位于 +0x30/+0x38），长度为零时回退到 CacheCard.FallbackSecUID
		// （c+0x10/+0x18，@0xd92ca2 的 test %r15,%r15; jne 后 mov 0x18(%r11)/0x10(%r11)），
		// 再调用 DynamicUrl(secUid, c.AwemeId)。
		secUid := c.GetAuthor().GetSecUid()
		if secUid == "" {
			secUid = c.FallbackSecUID
		}
		var data = map[string]interface{}{
			"dynamic":     c,
			"msg":         c.orgMsg,
			"name":        c.GetAuthor().GetNickname(),
			"desc":        c.GetDesc(),
			"date":        localutils.TimestampFormat(int64(c.GetCreateTime())),
			"cover":       c.GetVideo().GetCover().GetUrlList()[0],
			"pictures":    pictures,
			"gallery":     gallery,
			"live_photos": livePhotos,
			"url":         DynamicUrl(secUid, c.AwemeId),
			"group_code":  c.GroupCode,
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

// NewCacheCard 构造 CacheCard。
// 成品 NewCacheCard 无独立符号（被内联进 NewConcernNewsNotify @0xd924e0），
// 构造时写入 FallbackSecUID = newsInfo.UserInfo.SecUid
// （@0xd9260b/d92614：newsInfo+0x18/+0x10 → CacheCard+0x18/+0x10）。
func NewCacheCard(card *UserPostsResponse_AwemeList, fallbackSecUID string) *CacheCard {
	cacheCard := new(CacheCard)
	cacheCard.UserPostsResponse_AwemeList = card
	cacheCard.FallbackSecUID = fallbackSecUID
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
