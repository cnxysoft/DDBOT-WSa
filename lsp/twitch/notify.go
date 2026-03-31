package twitch

import (
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
)

// LiveEvent 表示一次直播状态变更事件
type LiveEvent struct {
	Id              string
	Login           string
	Name            string
	Live            bool
	Title           string
	GameName        string
	ViewerCount     int
	ThumbnailURL    string
	ProfileImageURL string
	OfflineImageURL string

	// 内部状态
	titleChanged      bool
	liveStatusChanged bool
}

func (e *LiveEvent) Site() string {
	return Site
}

func (e *LiveEvent) Type() concern_type.Type {
	return Live
}

func (e *LiveEvent) GetUid() interface{} {
	return e.Id
}

// NotifyLiveExt 接口实现

func (e *LiveEvent) IsLive() bool {
	return true
}

func (e *LiveEvent) Living() bool {
	return e.Live
}

func (e *LiveEvent) TitleChanged() bool {
	return e.titleChanged
}

func (e *LiveEvent) LiveStatusChanged() bool {
	return e.liveStatusChanged
}

func (e *LiveEvent) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Id":    e.Id,
		"Login": e.Login,
		"Name":  e.Name,
		"Live":  e.Live,
	})
}

// GetMSG 生成 Twitch 通知消息
func (e *LiveEvent) GetMSG() *mmsg.MSG {
	var data = map[string]interface{}{
		"login":            e.Login,
		"name":             e.Name,
		"living":           e.Living(),
		"title":            e.Title,
		"game_name":        e.GameName,
		"viewer_count":     e.ViewerCount,
		"thumbnail":        FormatThumbnailURL(e.ThumbnailURL, 1280, 720),
		"profile_image":    e.ProfileImageURL,
		"offline_image":    e.OfflineImageURL,
		"title_changed":    e.titleChanged,
		"live_changed":     e.liveStatusChanged,
	}
	msg, err := template.LoadAndExec("notify.group.twitch.live.tmpl", data)
	if err != nil {
		logger.Errorf("twitch: LiveEvent LoadAndExec error %v", err)
	}
	return msg
}

// LiveNotify 表示需要推送到群的直播通知
type LiveNotify struct {
	groupCode int64
	LiveEvent
}

func (n *LiveNotify) GetGroupCode() int64 {
	return n.groupCode
}

func (n *LiveNotify) ToMessage() *mmsg.MSG {
	n.Logger().Trace("正在构建 Twitch 推送消息")
	return n.LiveEvent.GetMSG()
}

func (n *LiveNotify) Logger() *logrus.Entry {
	return n.LiveEvent.Logger().WithFields(localutils.GroupLogFields(n.groupCode))
}
