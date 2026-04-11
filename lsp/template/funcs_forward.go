package template

import (
	"strconv"
	"time"

	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
)

// forward 构建合并转发消息
//
// Usage:
//
//	{{ forward (list
//	  (dict "message" $msg)
//	) }}
//
//	{{ forward (list
//	  (dict "senderName" "张三" "senderId" 123456 "time" 1234567890 "message" $msg)
//	  (dict "senderName" "李四" "senderId" 654321 "time" 1234567891 "elements" $msg.Elements "content" "文本")
//	) (dict "prompt" "转发标题") }}
//
// 最简用法（不提供 sender 信息，自动使用 BOT 信息）:
//
//	{{ forward (list
//	  (dict "content" "最简单的用法")
//	) (dict "prompt" "转发标题") }}
//
// 嵌套转发（支持最多3层）:
//
//	{{- $innerForward := forward (list
//	  (dict "senderName" "内层发送者" "senderId" 111111 "content" "这是内层消息")
//	) -}}
//	{{- forward (list
//	  (dict "senderName" "外层发送者" "senderId" 222222 "content" $innerForward)
//	) (dict "prompt" "嵌套转发测试") -}}
//
// Parameters:
//   - nodes []interface{}: 转发节点列表，每个节点包含（字段优先级从高到低）：
//   - message: 原始消息（*message.GroupMessage/*message.PrivateMessage），会提取发送人信息和元素
//   - elements: 消息元素列表，会覆盖 message 的元素
//   - content: 文本内容、*mmsg.ForwardElement（嵌套转发）或其他可转发内容
//   - senderName: 发送者昵称（可选，未提供时使用 "BOT"）
//   - senderId: 发送者QQ号（可选，未提供时使用 BOT 的 Uin）
//   - time: Unix时间戳（可选，未提供时使用当前时间）
//   - options map[string]interface{}: 顶层参数（可选）:
//   - prompt string: 转发消息外显标题 (LLOneBot/NapCatQQ 支持)
//   - source string: 转发来源 (LLOneBot/NapCatQQ 支持)
//   - summary string: 转发摘要 (LLOneBot/NapCatQQ 支持)
//
// Returns: *mmsg.ForwardElement - 可以直接 Append 到 MSG 中
//
// Sender 信息默认值（当未提供时）：
//   - senderName: "BOT"
//   - senderId: BOT 的 Uin
//   - time: 当前 Unix 时间戳
//
// 框架兼容性说明:
//   - NapCatQQ: 必须字段 name, uin, content
//   - LLOneBot: 必须字段 content；可选 name, uin
func forward(nodes []interface{}, options ...map[string]interface{}) *mmsg.ForwardElement {
	ob11Nodes := make([]map[string]interface{}, 0, len(nodes))

	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}

		// 提取 sender 信息（优先级：指定 > message > 默认）
		senderName, _ := nodeMap["senderName"].(string)
		senderId, _ := nodeMap["senderId"].(int64)
		timeVal, _ := nodeMap["time"].(int32)

		var content interface{}
		var msgSender *senderInfo

		// 如果有 message，提取发送人信息和元素
		if msg := nodeMap["message"]; msg != nil {
			msgSender = extractMessageSender(msg)
			content = extractMessageContent(msg)
			// 如果提取结果为空或只有空数组，回退到 content
			if contentList, ok := content.([]map[string]interface{}); !ok || len(contentList) == 0 {
				if fallback, ok := nodeMap["content"].(string); ok && fallback != "" {
					content = fallback
				} else {
					content = "" // 确保 content 有值
				}
			}
		} else if elements, ok := nodeMap["elements"].([]interface{}); ok && len(elements) > 0 {
			// 有 elements，使用 elements
			content = extractElementsContent(elements)
			if contentList, ok := content.([]map[string]interface{}); !ok || len(contentList) == 0 {
				if fallback, ok := nodeMap["content"].(string); ok && fallback != "" {
					content = fallback
				} else {
					content = ""
				}
			}
		} else {
			// 只有 content
			content = nodeMap["content"]
		}

		// 如果有 message，message 的发送人信息覆盖指定值
		if msgSender != nil {
			if senderName == "" {
				// 优先用群昵称，其次昵称
				if msgSender.CardName != "" {
					senderName = msgSender.CardName
				} else if msgSender.Nickname != "" {
					senderName = msgSender.Nickname
				}
			}
			if senderId == 0 {
				senderId = msgSender.Uin
			}
		}

		// 如果 sender 信息仍然为空，使用 BOT 信息作为默认值
		if senderName == "" {
			senderName = "BOT"
		}
		if senderId == 0 {
			senderId = localutils.GetBot().GetUin()
		}
		if timeVal == 0 {
			timeVal = int32(time.Now().Unix())
		}

		// 如果 content 是 *mmsg.ForwardElement，递归提取内层节点作为嵌套内容
		// QQ 支持最多 3 层嵌套
		if fe, ok := content.(*mmsg.ForwardElement); ok && fe != nil {
			// 将内层节点的 Nodes 转换为嵌套的 node 节点
			var nestedContent []interface{}
			for _, innerNode := range fe.Nodes {
				nestedContent = append(nestedContent, innerNode)
			}
			content = nestedContent
		}

		// 构建 onebot-v11 node 格式
		nodeData := map[string]interface{}{
			"name":    senderName,
			"uin":     senderId,
			"time":    timeVal,
			"content": content,
		}
		ob11Nodes = append(ob11Nodes, map[string]interface{}{
			"type": "node",
			"data": nodeData,
		})
	}

	return buildForwardElement(ob11Nodes, options...)
}

// senderInfo 用于统一发送人信息
type senderInfo struct {
	Uin      int64
	Nickname string
	CardName string
}

// extractMessageContent 从原始消息中提取转发内容
func extractMessageContent(msg interface{}) interface{} {
	switch m := msg.(type) {
	case *message.GroupMessage:
		return extractIMessageElements(m.Elements)
	case *message.PrivateMessage:
		return extractIMessageElements(m.Elements)
	case *adapter.GetMsgResult:
		return extractIMessageElements(m.Elements)
	}
	return nil
}

// extractMessageSender 从原始消息中提取发送人信息
func extractMessageSender(msg interface{}) *senderInfo {
	switch m := msg.(type) {
	case *message.GroupMessage:
		return &senderInfo{Uin: m.Sender.Uin, Nickname: m.Sender.Nickname, CardName: m.Sender.CardName}
	case *message.PrivateMessage:
		return &senderInfo{Uin: m.Sender.Uin, Nickname: m.Sender.Nickname, CardName: m.Sender.CardName}
	case *adapter.GetMsgResult:
		if m.Sender != nil {
			return &senderInfo{Uin: m.Sender.UserID, Nickname: m.Sender.Nickname, CardName: m.Sender.Card}
		}
	}
	return nil
}

// extractIMessageElements 将 []message.IMessageElement 转换为转发格式
// 返回 []map[string]interface{}，每个元素包含 type 和 data 字段（onebot 标准格式）
func extractIMessageElements(elems []message.IMessageElement) []map[string]interface{} {
	var contentList []map[string]interface{}
	for _, elem := range elems {
		if elem == nil {
			continue
		}
		seg := convertToMessageSegment(elem)
		if seg.Type != "" && seg.Data != nil {
			contentList = append(contentList, map[string]interface{}{
				"type": seg.Type,
				"data": seg.Data,
			})
		}
	}
	return contentList
}

// extractElementsContent 将 []interface{} 转换为转发格式
// 返回 []map[string]interface{}，每个元素包含 type 和 data 字段（onebot 标准格式）
// 支持 string 类型自动转换为 text 消息段
func extractElementsContent(elems []interface{}) interface{} {
	var contentList []map[string]interface{}
	for _, elem := range elems {
		// 处理字符串类型，自动转换为 text 消息段
		if s, ok := elem.(string); ok && s != "" {
			contentList = append(contentList, map[string]interface{}{
				"type": "text",
				"data": map[string]interface{}{"text": s},
			})
			continue
		}
		// 处理 message.IMessageElement 类型
		if imsg, ok := elem.(message.IMessageElement); ok {
			seg := convertToMessageSegment(imsg)
			if seg.Type != "" {
				contentList = append(contentList, map[string]interface{}{
					"type": seg.Type,
					"data": seg.Data,
				})
			}
		}
	}
	return contentList
}

// buildForwardElement 根据节点和选项创建 ForwardElement
func buildForwardElement(nodes []map[string]interface{}, options ...map[string]interface{}) *mmsg.ForwardElement {
	if len(options) == 0 || options[0] == nil {
		return mmsg.NewForwardElement(nodes)
	}

	opts := &mmsg.ForwardOptions{}
	if v, ok := options[0]["prompt"].(string); ok {
		opts.Prompt = v
	}
	if v, ok := options[0]["source"].(string); ok {
		opts.Source = v
	}
	if v, ok := options[0]["summary"].(string); ok {
		opts.Summary = v
	}
	if v, ok := options[0]["news"].([]interface{}); ok {
		for _, n := range v {
			if s, ok := n.(string); ok {
				opts.News = append(opts.News, s)
			}
		}
	}

	return mmsg.NewForwardElementWithOptions(nodes, opts)
}

// forwardMsgSegment 是消息段的内部表示
type forwardMsgSegment struct {
	Type string
	Data map[string]interface{}
}

// convertToMessageSegment 将 message.IMessageElement 转换为 MessageSegment 格式
func convertToMessageSegment(elem message.IMessageElement) forwardMsgSegment {
	switch e := elem.(type) {
	// message 包类型
	case *message.TextElement:
		return forwardMsgSegment{
			Type: "text",
			Data: map[string]interface{}{"text": e.Content},
		}
	case *message.AtElement:
		qq := "all"
		if e.Target != 0 {
			qq = strconv.FormatInt(e.Target, 10)
		}
		return forwardMsgSegment{
			Type: "at",
			Data: map[string]interface{}{"qq": qq},
		}
	case *message.FaceElement:
		return forwardMsgSegment{
			Type: "face",
			Data: map[string]interface{}{"id": e.Index},
		}
	case *message.GroupImageElement:
		return forwardMsgSegment{
			Type: "image",
			Data: map[string]interface{}{
				"name": e.Name,
				"file": e.Url,
			},
		}
	case *message.FriendImageElement:
		return forwardMsgSegment{
			Type: "image",
			Data: map[string]interface{}{
				"file": e.Url,
			},
		}
	case *message.VoiceElement:
		return forwardMsgSegment{
			Type: "record",
			Data: map[string]interface{}{
				"name": e.Name,
				"file": e.Url,
			},
		}
	case *message.ReplyElement:
		return forwardMsgSegment{
			Type: "reply",
			Data: map[string]interface{}{"id": e.ReplySeq},
		}
	case *message.LightAppElement:
		return forwardMsgSegment{
			Type: "json",
			Data: map[string]interface{}{"data": e.Content},
		}
	// mmsg 包类型
	case *mmsg.ImageBytesElement:
		file := e.GetFile()
		if file != "" {
			return forwardMsgSegment{
				Type: "image",
				Data: map[string]interface{}{
					"file": file,
				},
			}
		}
		return forwardMsgSegment{}
	case *mmsg.AtElement:
		qq := "all"
		if e.Target != 0 {
			qq = strconv.FormatInt(e.Target, 10)
		}
		return forwardMsgSegment{
			Type: "at",
			Data: map[string]interface{}{"qq": qq},
		}
	case *mmsg.PokeElement:
		return forwardMsgSegment{
			Type: "poke",
			Data: map[string]interface{}{"uin": e.Uin},
		}
	case *mmsg.CutElement:
		return forwardMsgSegment{
			Type: "cut",
			Data: nil,
		}
	case *mmsg.RecordElement:
		file := e.GetFile()
		if file != "" {
			return forwardMsgSegment{
				Type: "record",
				Data: map[string]interface{}{
					"file": file,
				},
			}
		}
		return forwardMsgSegment{}
	case *mmsg.VideoElement:
		file := e.GetFile()
		if file != "" {
			return forwardMsgSegment{
				Type: "video",
				Data: map[string]interface{}{
					"file": file,
				},
			}
		}
		return forwardMsgSegment{}
	case *mmsg.FileElement:
		file := e.GetFile()
		if file != "" {
			return forwardMsgSegment{
				Type: "file",
				Data: map[string]interface{}{
					"file": file,
				},
			}
		}
		return forwardMsgSegment{}
	}
	return forwardMsgSegment{}
}

func init() {
	RegisterExtFunc("forward", forward)
}
