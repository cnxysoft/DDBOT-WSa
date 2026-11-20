package douyin

import (
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

// GroupConcernConfig 创建一个新结构，准备重写 FilterHook
type GroupConcernConfig struct {
	concern.IConfig
	concern *Concern
}

// FilterHook 委托给基础实现，支持关键字黑白名单过滤
func (g *GroupConcernConfig) FilterHook(notify concern.Notify) *concern.HookResult {
	return g.IConfig.FilterHook(notify)
}

// 还有更多方法可以重载

// NewGroupConcernConfig 创建一个新的 GroupConcernConfig
func NewGroupConcernConfig(g concern.IConfig, c *Concern) *GroupConcernConfig {
	return &GroupConcernConfig{g, c}
}

func (g *GroupConcernConfig) NotifyBeforeCallback(inotify concern.Notify) {
	if inotify.Type() != News {
		return
	}
	notify := inotify.(*ConcernNewsNotify)
	// 解决联合投稿的时候刷屏
	notify.compactKey = notify.Card.GetAwemeId()
	err := g.concern.SetGroupCompactMarkIfNotExist(notify.GetGroupCode(), notify.compactKey)
	if localdb.IsRollback(err) {
		notify.shouldCompact = true
	}
}

func (g *GroupConcernConfig) NotifyAfterCallback(inotify concern.Notify, msg *adapter.GroupMessage) {
	if inotify.Type() != News || msg == nil || msg.ID == -1 {
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
