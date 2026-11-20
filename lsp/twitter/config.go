package twitter

import (
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

// GroupConcernConfig 在通用配置上扩展Twitter推送回调。
type GroupConcernConfig struct {
	concern.IConfig
	concern *twitterConcern
}

// FilterHook 保留通用关键词过滤；ConcernNewsNotify会直接提供模板动态文字，避免提前渲染消息。
func (g *GroupConcernConfig) FilterHook(notify concern.Notify) *concern.HookResult {
	return g.IConfig.FilterHook(notify)
}

// 还有更多方法可以重载

// NewGroupConcernConfig 创建一个新的 GroupConcernConfig
func NewGroupConcernConfig(g concern.IConfig, c *twitterConcern) *GroupConcernConfig {
	return &GroupConcernConfig{g, c}
}

func (g *GroupConcernConfig) NotifyBeforeCallback(inotify concern.Notify) {
	reQuery := false
	notify := inotify.(*ConcernNewsNotify)
	// 解决一起转发的时候刷屏
	notify.compactKey = notify.Tweet.ID
retry:
	err := g.concern.SetGroupCompactMarkIfNotExist(notify.GetGroupCode(), notify.compactKey)
	if localdb.IsRollback(err) {
		notify.shouldCompact = true
	} else if !reQuery && notify.Tweet.QuoteTweet != nil {
		// 解决引用的时候刷屏
		notify.compactKey = notify.Tweet.QuoteTweet.ID
		reQuery = true
		goto retry
	}
}

func (g *GroupConcernConfig) NotifyAfterCallback(inotify concern.Notify, msg *adapter.GroupMessage) {
	if msg == nil || msg.ID == -1 {
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
