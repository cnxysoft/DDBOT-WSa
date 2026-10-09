package missevan

import (
	"fmt"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
)

// UserState 是猫耳FM订阅用户的持久化状态，按 uid 存储在 MissevanUserState 键下。
type UserState struct {
	UserID           int64  `json:"user_id"`
	Name             string `json:"name"`
	Avatar           string `json:"avatar"`
	RoomID           int64  `json:"room_id"`
	ProfileCheckedAt int64  `json:"profile_checked_at"`
	NewsInitialized  bool   `json:"news_initialized"`
	LatestSoundID    int64  `json:"latest_sound_id"`
	LiveInitialized  bool   `json:"live_initialized"`
	Living           bool   `json:"living"`
	LastLiveTitle    string `json:"last_live_title"`
	LastLiveCover    string `json:"last_live_cover"`
}

func (u *UserState) GetUid() interface{} {
	if u == nil {
		return int64(0)
	}
	return u.UserID
}

func (u *UserState) GetName() string {
	if u == nil {
		return ""
	}
	return u.Name
}

// LiveInfo 是一次直播状态变化产生的事件，实现 concern.Event 与 concern.NotifyLiveExt。
type LiveInfo struct {
	UserID int64
	Name   string
	Avatar string
	RoomID int64
	Title  string
	Cover  string

	IsLiving         bool
	StatusChanged    bool
	LiveTitleChanged bool
}

func (l *LiveInfo) Site() string {
	return Site
}

func (l *LiveInfo) Type() concern_type.Type {
	return Live
}

func (l *LiveInfo) GetUid() interface{} {
	return l.UserID
}

func (l *LiveInfo) IsLive() bool {
	return true
}

func (l *LiveInfo) Living() bool {
	return l.IsLiving
}

func (l *LiveInfo) TitleChanged() bool {
	return l.LiveTitleChanged
}

func (l *LiveInfo) LiveStatusChanged() bool {
	return l.StatusChanged
}

func (l *LiveInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site":   l.Site(),
		"Uid":    l.UserID,
		"Name":   l.Name,
		"RoomId": l.RoomID,
		"Title":  l.Title,
		"Living": l.IsLiving,
		"Type":   l.Type().String(),
	})
}

// messageFor 按群渲染直播模板，可用字段 name/uid/avatar/roomId/title/cover/living/url/group_code。
func (l *LiveInfo) messageFor(groupCode int64) *mmsg.MSG {
	data := map[string]interface{}{
		"name":       l.Name,
		"uid":        l.UserID,
		"avatar":     l.Avatar,
		"roomId":     l.RoomID,
		"title":      l.Title,
		"cover":      l.Cover,
		"living":     l.Living(),
		"url":        fmt.Sprintf("%s/%d", apiHost, l.RoomID),
		"group_code": groupCode,
	}
	msg, err := template.LoadAndExec("notify.group.missevan.live.tmpl", data)
	if err != nil {
		l.Logger().WithError(err).Errorf("执行猫耳FM直播模板失败 %v", err)
		return nil
	}
	return msg
}

// LiveNotify 是推送给单个群的直播通知。
type LiveNotify struct {
	GroupCode int64
	*LiveInfo
}

func (notify *LiveNotify) GetGroupCode() int64 {
	return notify.GroupCode
}

func (notify *LiveNotify) ToMessage() (m *mmsg.MSG) {
	return notify.LiveInfo.messageFor(notify.GroupCode)
}

func (notify *LiveNotify) Logger() *logrus.Entry {
	if notify == nil {
		return logger
	}
	return notify.LiveInfo.Logger().WithFields(localutils.GroupLogFields(notify.GroupCode))
}

// NewsInfo 是一次新声音检查产生的事件，Sounds 为本次检查新发现的声音。
type NewsInfo struct {
	UserID int64
	Name   string
	Avatar string
	Sounds []*Sound
}

func (n *NewsInfo) Site() string {
	return Site
}

func (n *NewsInfo) Type() concern_type.Type {
	return News
}

func (n *NewsInfo) GetUid() interface{} {
	return n.UserID
}

func (n *NewsInfo) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Site":  n.Site(),
		"Uid":   n.UserID,
		"Name":  n.Name,
		"Count": len(n.Sounds),
		"Type":  n.Type().String(),
	})
}

// NewsNotify 是推送给单个群的新声音通知，每个声音一条。
type NewsNotify struct {
	GroupCode int64
	UserID    int64
	Name      string
	Avatar    string
	Sound     *Sound
}

func (n *NewsNotify) Site() string {
	return Site
}

func (n *NewsNotify) Type() concern_type.Type {
	return News
}

func (n *NewsNotify) GetUid() interface{} {
	return n.UserID
}

func (n *NewsNotify) GetGroupCode() int64 {
	return n.GroupCode
}

func (n *NewsNotify) Logger() *logrus.Entry {
	if n == nil {
		return logger
	}
	return logger.WithFields(logrus.Fields{
		"Site":      n.Site(),
		"Uid":       n.UserID,
		"Name":      n.Name,
		"GroupCode": n.GroupCode,
		"Type":      n.Type().String(),
	})
}

// ToMessage 按群渲染声音模板，可用字段 name/uid/avatar/soundId/title/cover/duration/date/url/group_code。
func (n *NewsNotify) ToMessage() (m *mmsg.MSG) {
	if n == nil || n.Sound == nil {
		return nil
	}
	data := map[string]interface{}{
		"name":       n.Name,
		"uid":        n.UserID,
		"avatar":     n.Avatar,
		"soundId":    n.Sound.ID,
		"title":      n.Sound.Title,
		"cover":      n.Sound.Cover,
		"duration":   n.Sound.DurationMS,
		"date":       localutils.TimestampFormat(n.Sound.CreateTime),
		"url":        fmt.Sprintf(soundURLFormat, personalHost, n.Sound.ID),
		"group_code": n.GroupCode,
	}
	msg, err := template.LoadAndExec("notify.group.missevan.news.tmpl", data)
	if err != nil {
		n.Logger().WithError(err).Errorf("执行猫耳FM声音模板失败 %v", err)
		return nil
	}
	return msg
}
