package twitter

import "github.com/cnxysoft/DDBOT-WSa/lsp/concern"

// GroupConcernConfig 创建一个新结构，准备重写 FilterHook
type GroupConcernConfig struct {
	concern.IConfig
}

// FilterHook 可以在这里自定义过滤逻辑
func (g *GroupConcernConfig) FilterHook(concern.Notify) *concern.HookResult {
	return concern.HookResultPass
}

// 还有更多方法可以重载

// NewGroupConcernConfig 创建一个新的 GroupConcernConfig
func NewGroupConcernConfig(g concern.IConfig) *GroupConcernConfig {
	return &GroupConcernConfig{g}
}
