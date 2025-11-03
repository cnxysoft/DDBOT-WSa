package twitter

import (
	"github.com/Mrs4s/MiraiGo/message"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

// GroupConcernConfig 创建一个新结构，准备重写 FilterHook
type GroupConcernConfig struct {
	concern.IConfig
	concern *twitterConcern
}

// FilterHook 可以在这里自定义过滤逻辑
func (g *GroupConcernConfig) FilterHook(concern.Notify) *concern.HookResult {
	return concern.HookResultPass
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

func (g *GroupConcernConfig) NotifyAfterCallback(inotify concern.Notify, msg *message.GroupMessage) {
	if msg == nil || msg.Id == -1 {
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
