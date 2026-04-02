package twitch

import (
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
)

type GroupConcernConfig struct {
	concern.IConfig
}

func (g *GroupConcernConfig) ShouldSendHook(notify concern.Notify) *concern.HookResult {
	// 委托给基类处理 OfflineNotify 和 TitleChangeNotify 配置检查
	return g.IConfig.ShouldSendHook(notify)
}

func NewGroupConcernConfig(g concern.IConfig) *GroupConcernConfig {
	return &GroupConcernConfig{g}
}
