package mmsg

import (
	"github.com/Mrs4s/MiraiGo/message"
)

// ForwardOptions 合并转发消息的顶层参数
// 对应 onebot-v11 send_group_forward_msg API 的顶层字段
type ForwardOptions struct {
	Prompt  string   // 转发消息外显标题 (LLOneBot/NapCatQQ 支持)
	Source  string   // 转发来源 (LLOneBot/NapCatQQ 支持)
	Summary string   // 转发摘要 (LLOneBot/NapCatQQ 支持)
	News    []string // 转发预览文本列表 (LLOneBot/NapCatQQ 支持)
}

// ForwardElement 用于在模板中构建合并转发消息
// 当 Append 到 MSG 时，会将节点列表复制到 MSG.ForwardNodes
type ForwardElement struct {
	Nodes   []map[string]interface{} // onebot-v11 格式的节点列表
	Options *ForwardOptions           // 顶层参数 (prompt, source, summary, news)
}

// Type 实现 message.IMessageElement 接口
func (f *ForwardElement) Type() message.ElementType {
	return message.Forward
}

// PackToElement 实现 CustomElement 接口，返回 message.ForwardElement
// 由于 onebot-v11 的转发消息不走 MiraiGo 的转发协议，这里返回 nil
// 实际转发消息通过 SendGroupForwardMessage / SendPrivateForwardMessage API 发送
func (f *ForwardElement) PackToElement(target Target) message.IMessageElement {
	return nil // 不通过 MiraiGo 原生协议发送
}

// NewForwardElement 创建一个转发消息元素
func NewForwardElement(nodes []map[string]interface{}) *ForwardElement {
	return &ForwardElement{Nodes: nodes}
}

// NewForwardElementWithOptions 创建一个带顶层参数的转发消息元素
func NewForwardElementWithOptions(nodes []map[string]interface{}, options *ForwardOptions) *ForwardElement {
	return &ForwardElement{Nodes: nodes, Options: options}
}
