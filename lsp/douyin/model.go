package douyin

import (
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
	IsLiving bool `json:"living"`

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
			"uid":    l.Uid,
			"name":   l.NikeName,
			"roomId": l.WebRoomId,
			"living": l.Living(),
			"url":    BaseLiveHost + "/" + l.WebRoomId,
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
