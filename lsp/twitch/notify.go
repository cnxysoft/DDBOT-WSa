package twitch

import (
	"errors"
	"fmt"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
)

var (
	ErrUserNotFound = errors.New("twitch 用户不存在")
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

func (e *LiveEvent) Logger() *logrus.Entry {
	return logger.WithFields(logrus.Fields{
		"Id":    e.Id,
		"Login": e.Login,
		"Name":  e.Name,
		"Live":  e.Live,
	})
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

	if !n.Live {
		n.Logger().Trace("构建下播通知消息")
		return mmsg.NewTextf("%v 的 Twitch 直播已结束。", n.Name)
	}

	msg := mmsg.NewTextf("%v 正在 Twitch 直播", n.Name)

	if n.Title != "" {
		msg.Textf("\n标题: %v", n.Title)
	}

	if n.GameName != "" {
		msg.Textf("\n游戏: %v", n.GameName)
	}

	if n.ViewerCount > 0 {
		msg.Textf("\n观看人数: %v", n.ViewerCount)
	}

	msg.Textf("\n直播间: %v", fmt.Sprintf("https://www.twitch.tv/%v", n.Login))

	if n.ThumbnailURL != "" {
		thumbURL := FormatThumbnailURL(n.ThumbnailURL, 1280, 720)
		msg.ImageByUrl(thumbURL, "\n[直播封面获取失败]", requests.ProxyOption(proxy_pool.PreferOversea))
	}

	return msg
}

func (n *LiveNotify) Logger() *logrus.Entry {
	return n.LiveEvent.Logger().WithFields(localutils.GroupLogFields(n.groupCode))
}
