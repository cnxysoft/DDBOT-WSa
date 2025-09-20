package acfun

import (
	"github.com/Mrs4s/MiraiGo/message"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"strconv"
)

const (
	DynamicDescType_Normal = iota
	DynamicDescType_WithVideo
	DynamicDescType_WithOrigin
)

type GroupConcernConfig struct {
	concern.IConfig
	concern *Concern
}

func NewGroupConcernConfig(g concern.IConfig, c *Concern) *GroupConcernConfig {
	return &GroupConcernConfig{g, c}
}

func (g *GroupConcernConfig) NotifyBeforeCallback(inotify concern.Notify) {
	if inotify.Type() != News {
		return
	}
	notify := inotify.(*ConcernNewsNotify)
	CardType := DynamicDescType_Normal
	CompactKey := ""
	if notify.Card.GetRepostSource() != nil {
		if notify.Card.GetRepostSource().GetVideoId() != "" {
			CardType = DynamicDescType_WithVideo
		} else {
			CardType = DynamicDescType_WithOrigin
		}
		CompactKey = strconv.FormatInt(notify.Card.GetRepostSource().GetResourceId(), 10)
	} else {
		if notify.Card.GetVideoId() != "" {
			CardType = DynamicDescType_WithVideo
		}
		CompactKey = strconv.FormatInt(notify.Card.GetResourceId(), 10)
	}
	switch CardType {
	case DynamicDescType_WithVideo:
		// 解决联合投稿的时候刷屏
		notify.compactKey = CompactKey
		err := g.concern.SetGroupCompactMarkIfNotExist(notify.GetGroupCode(), notify.compactKey)
		if localdb.IsRollback(err) {
			notify.shouldCompact = true
		}
	case DynamicDescType_WithOrigin:
		// 解决一起转发的时候刷屏
		notify.compactKey = CompactKey
		err := g.concern.SetGroupCompactMarkIfNotExist(notify.GetGroupCode(), notify.compactKey)
		if localdb.IsRollback(err) {
			notify.shouldCompact = true
		}
	default:
		// 其他动态也设置一下
		notify.compactKey = CompactKey
		err := g.concern.SetGroupCompactMarkIfNotExist(notify.GetGroupCode(), notify.compactKey)
		if err != nil && !localdb.IsRollback(err) {
			logger.Errorf("SetGroupOriginMarkIfNotExist error %v", err)
		}
	}
}

func (g *GroupConcernConfig) NotifyAfterCallback(inotify concern.Notify, msg *message.GroupMessage) {
	if inotify.Type() != News || msg == nil || msg.Id == -1 {
		return
	}
	notify := inotify.(*ConcernNewsNotify)
	if notify.shouldCompact || len(notify.compactKey) == 0 {
		return
	}
	err := g.concern.SetNotifyMsg(notify.compactKey, msg)
	if err != nil && !localdb.IsRollback(err) {
		notify.Logger().Errorf("set notify msg error %v", err)
	}
}
