package lsp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/sirupsen/logrus"
)

// MessageLinkParser 由站点实现：把聊天中出现的链接解析为可以回发的消息。
// Resolve 返回 (规范化链接, 解析详情, 是否命中, 错误)；
// 只有命中 true 的结果才会进入 Render 渲染并回发。
// Enabled 接收一个 private 参数：true 表示私聊会话，站点据此分别判断群/私聊开关。
// 依据：成品类型元数据 recovered/types/types-closure-x64.tsv 中
// lsp.MessageLinkParser 为 Enabled func(bool) bool。
type MessageLinkParser interface {
	Name() string
	DisplayName() string
	Enabled(private bool) bool
	DedupWindow() time.Duration
	Timeout() time.Duration
	Resolve(ctx context.Context, rawLink string) (string, interface{}, bool, error)
	Render(ctx context.Context, detail interface{}) (*mmsg.MSG, error)
}

// MessageLinkParserAuthorizer 由站点按需实现：决定某个群/私聊是否允许触发链接解析。
// 未实现时默认允许，由站点侧配置自行收紧。
type MessageLinkParserAuthorizer interface {
	Allowed(private bool, groupCode int64, userID int64) bool
}

var (
	messageLinkParsersMu sync.RWMutex
	messageLinkParsers   []MessageLinkParser

	messageLinkParseMu sync.Mutex
	// messageLinkParseSeen 记录每个解析 key 的预约到期时间；到期前重复 key 不再解析。
	messageLinkParseSeen map[string]time.Time
)

// RegisterMessageLinkParser 注册一个站点的链接解析器，重复注册同一 Name 会被忽略。
func RegisterMessageLinkParser(parser MessageLinkParser) {
	if parser == nil {
		return
	}
	messageLinkParsersMu.Lock()
	defer messageLinkParsersMu.Unlock()
	for _, p := range messageLinkParsers {
		if p.Name() == parser.Name() {
			logger.WithField("parser", parser.Name()).Warn("链接解析器重复注册，忽略本次注册")
			return
		}
	}
	messageLinkParsers = append(messageLinkParsers, parser)
	logger.WithFields(logrus.Fields{
		"parser":       parser.Name(),
		"display_name": parser.DisplayName(),
	}).Info("注册链接解析器")
}

// registeredMessageLinkParsers 返回当前全部已注册解析器的快照。
func registeredMessageLinkParsers() []MessageLinkParser {
	messageLinkParsersMu.RLock()
	defer messageLinkParsersMu.RUnlock()
	result := make([]MessageLinkParser, len(messageLinkParsers))
	copy(result, messageLinkParsers)
	return result
}

// defaultMessageLinkParseWindow 是调用方未给出（<=0）预约窗口时的默认去重时长。
// 成品证据：reserveMessageLinkParse prologue 里 cmovle 0xdf8475800 ns = 60s。
const defaultMessageLinkParseWindow = time.Minute

// reserveMessageLinkParse 为一次解析做去重预约。
// 同一 key 在预约窗口内只解析一次；窗口 <=0 时使用默认窗口。
// 返回 true 表示本次应解析，解析结束后必须调用 releaseMessageLinkParse(key)。
//
// 成品证据（reserveMessageLinkParse @link_parser.go:65，736B）：
// callees 只有 time.Now / internal/sync.Mutex.lockSlow / mapIterStart(mapIterNext) /
// time.Time.After / mapdelete_faststr / mapaccess2_faststr / time.Time.Add /
// mapassign_faststr——没有 semaphore.TryAcquire、没有 logrus、没有 runtime.rand。
// 流程为：取 now -> 加锁 -> 默认窗口 -> 遍历 map 删除已过期项（!expiry.After(now)）
// -> 命中且 expiry.After(now) 则返回 false -> 否则 seen[key]=now.Add(window) 并返回 true。
func reserveMessageLinkParse(key string, dedupWindow time.Duration) bool {
	now := time.Now()
	messageLinkParseMu.Lock()
	defer messageLinkParseMu.Unlock()
	if messageLinkParseSeen == nil {
		messageLinkParseSeen = make(map[string]time.Time)
	}
	if dedupWindow <= 0 {
		dedupWindow = defaultMessageLinkParseWindow
	}
	// 顺带清理已到期的预约，防止 map 无限增长。
	for k, expiry := range messageLinkParseSeen {
		if !expiry.After(now) {
			delete(messageLinkParseSeen, k)
		}
	}
	if expiry, seen := messageLinkParseSeen[key]; seen && expiry.After(now) {
		return false
	}
	messageLinkParseSeen[key] = now.Add(dedupWindow)
	return true
}

// releaseMessageLinkParse 结束一次解析预约（删除该 key）。
// 成品证据（releaseMessageLinkParse @link_parser.go:84，192B）：
// 加锁后直接 mapdelete_faststr(messageLinkParseSeen, key)，再解锁；无信号量 Release。
func releaseMessageLinkParse(key string) {
	messageLinkParseMu.Lock()
	delete(messageLinkParseSeen, key)
	messageLinkParseMu.Unlock()
}

// handleGroupLinks 处理群聊消息中的链接解析。
//
// 成品 x64 0xcae4e0 入参：RAX=*Lsp、RBX=*adapter.GroupMessage（函数入口 test rbx,rbx
// 非空判断，为 nil 直接返回）。内部字段访问（x64 偏移）：
//   - +0x8  GroupCode            -> permission.(*StateManager).CheckGroupBlocked(+0x41)
//   - +0x30/+0x38/+0x40 Elements -> msgstringer.AdapterMsgToString(+0x62)
//   - +0x20 Sender(*adapter.SenderInfo) -> 取首字段 UserID（Sender 为 nil 时用 0）
//
// 调用顺序：CheckGroupBlocked、AdapterMsgToString、fmt.Sprintf("group:%d")、
// newobject(mmsg.GroupTarget)、handleMessageLinks(+0x158)。全过程无 logrus 调用。
func (l *Lsp) handleGroupLinks(msg *adapter.GroupMessage) {
	if msg == nil {
		return
	}
	if l.PermissionStateManager.CheckGroupBlocked(msg.GroupCode) {
		return
	}
	var senderID int64
	if msg.Sender != nil {
		senderID = msg.Sender.UserID
	}
	l.handleMessageLinks(
		msgstringer.AdapterMsgToString(msg.Elements),
		fmt.Sprintf("group:%d", msg.GroupCode),
		false,
		mmsg.NewGroupTarget(msg.GroupCode),
		int32(msg.ID),
		senderID,
		msg.GroupCode,
	)
}

// handlePrivateLinks 处理私聊消息中的链接解析。
//
// 成品 x64 0xcae680 入参：RAX=*Lsp、RBX=*adapter.PrivateMessage（入口 test rbx,rbx）。
// 内部字段访问（x64 偏移）：
//   - +0x8  UserID；为 0 时回退到 +0x18 Sender(*adapter.SenderInfo) 的首字段 UserID
//   - +0x28/+0x30/+0x38 Elements -> msgstringer.AdapterMsgToString(+0x60)
//
// 调用顺序：AdapterMsgToString、fmt.Sprintf("private:%d")、newobject(mmsg.PrivateTarget)、
// handleMessageLinks(+0x112)。全过程无 logrus 调用。
func (l *Lsp) handlePrivateLinks(msg *adapter.PrivateMessage) {
	if msg == nil {
		return
	}
	senderID := msg.UserID
	if senderID == 0 && msg.Sender != nil {
		senderID = msg.Sender.UserID
	}
	l.handleMessageLinks(
		msgstringer.AdapterMsgToString(msg.Elements),
		fmt.Sprintf("private:%d", senderID),
		true,
		mmsg.NewPrivateTarget(senderID),
		0,
		senderID,
		0,
	)
}

// handleMessageLinks 链接解析总线入口：把整段消息文本交给每个已注册解析器，命中的渲染并回发。
// 成品 0xcae7e0 内没有任何 regexp 调用（FindAllString 在整个二进制里只被 douyin.ResolveLink
// 调用）；strings.TrimSpace 仅用于判断行首 '/'，随后原文本原样传给 parser.Resolve(ctx, text)。
//
// 成品 x64 0xcae7e0 入参（Go 寄存器 ABI，RAX=接收者）：
//   - RBX/RCX  第一个 string = 消息文本（AdapterMsgToString 结果）；函数开头先
//     strings.TrimSpace，若修剪后以 '/' 开头则直接返回
//   - RDI/RSI  第二个 string = 会话 scope（调用点用 fmt.Sprintf("group:%d") /
//     "private:%d" 生成，函数内用作 logrus 字段）
//   - R8b      private bool
//   - R9/R10   mmsg.Target 接口（itab 0x1131148=*mmsg.GroupTarget、
//     0x1131058=*mmsg.PrivateTarget）
//   - R11d     int32 回复序号（群聊 int32(msg.ID)，私聊 0；透传给
//     sendMessageLinkResult 作为 ReplySegment.ReplySeq）
//   - stack0   int64 senderID（群聊 msg.Sender.UserID，私聊 msg.UserID）
//   - stack1   int64 groupCode（群聊 msg.GroupCode，私聊 0）
//
// 函数末尾把 target/replySeq/senderID/groupCode 原样转发给 sendMessageLinkResult(0xcafb40)。
func (l *Lsp) handleMessageLinks(text string, scope string, private bool, target mmsg.Target, replySeq int32, senderID int64, groupCode int64) {
	if !l.started.Load() {
		return
	}
	if len(registeredMessageLinkParsers()) == 0 {
		return
	}
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		return
	}

	for _, parser := range registeredMessageLinkParsers() {
		l.dispatchMessageLink(parser, private, groupCode, senderID, target, replySeq, text, scope)
	}
}

// dispatchMessageLink 让单个解析器尝试解析整段消息文本，完成鉴权、去重、渲染与回发。
func (l *Lsp) dispatchMessageLink(parser MessageLinkParser, private bool, groupCode int64, senderID int64, target mmsg.Target, replySeq int32, text string, scope string) {
	log := logger.WithFields(logrus.Fields{
		"parser":     parser.Name(),
		"link":       text,
		"private":    private,
		"group_code": groupCode,
		"user_id":    senderID,
	})
	if !parser.Enabled(private) {
		return
	}
	if authorizer, ok := parser.(MessageLinkParserAuthorizer); ok && !authorizer.Allowed(private, groupCode, senderID) {
		log.Debug("链接解析未通过站点侧鉴权，跳过")
		return
	}

	key := parser.Name() + "|" + scope + "|" + text
	if !reserveMessageLinkParse(key, parser.DedupWindow()) {
		log.Debug("重复链接已跳过")
		return
	}
	defer releaseMessageLinkParse(key)

	ctx, cancel := context.WithTimeout(context.Background(), parser.Timeout())
	defer cancel()
	_, detail, hit, err := parser.Resolve(ctx, text)
	if err != nil {
		log.WithField("error", err.Error()).Warnf("链接解析失败: %v", err)
		l.sendMessageLinkResult(target, replySeq, senderID, groupCode, mmsg.NewText("链接解析失败，请稍后重试"))
		return
	}
	if !hit {
		return
	}
	if detail == nil {
		log.Debug("链接解析详情为空，跳过渲染")
		return
	}

	renderCtx, renderCancel := context.WithTimeout(context.Background(), parser.Timeout())
	defer renderCancel()
	result, err := parser.Render(renderCtx, detail)
	if err != nil {
		log.WithField("error", err.Error()).Warnf("链接解析失败: %v", err)
		l.sendMessageLinkResult(target, replySeq, senderID, groupCode, mmsg.NewText("链接详情获取失败"))
		return
	}

	l.sendMessageLinkResult(target, replySeq, senderID, groupCode, result)
	log.Debug("链接解析完成")
}

// sendMessageLinkResult 把解析结果回发到会话。
//
// 成品 x64 0xcafb40 入参（Go 寄存器 ABI，RAX=接收者）：
//   - RBX/RCX  mmsg.Target 接口
//   - EDI      int32 回复序号（ReplySegment.ReplySeq）
//   - RSI      int64 senderID（ReplySegment.Sender）
//   - R8       int64 groupID（ReplySegment.GroupID）
//   - R9       *mmsg.MSG 渲染结果（入口 test r9,r9 非空判断）
//
// callees：splitMessageLinkResult(+0x1eb)、mmsg.(*MSG).Append 三个调用点
// (+0x420/+0x580/+0x783)、(*Lsp).SendMsg 两个调用点(+0x716/+0x7a3)；无 logrus。
// 三处 Append 追加的元素（由 itab/type 地址确定）：
//  1. +0x420：&adapter.ReplySegment{ReplySeq, Sender, GroupID}。newobject 类型
//     0x1006240 = adapter.ReplySegment，接口 itab 0x1131120 = *adapter.ReplySegment；
//     仅当目标 TargetType()==TargetGroup(0) 且 ReplySeq != 0 时追加。
//  2. +0x580：split 出的非 ForwardElement 元素（Append(chunk.Elements()...)）。
//  3. +0x783：*mmsg.ForwardElement（itab 0x11310a8），每个合并转发元素单独成一条
//     消息 append 后 SendMsg。
func (l *Lsp) sendMessageLinkResult(target mmsg.Target, replySeq int32, senderID int64, groupID int64, result *mmsg.MSG) {
	if result == nil {
		return
	}
	chunk, forwards := splitMessageLinkResult(result)
	if chunk == nil {
		return
	}

	msg := chunk
	if len(chunk.Elements()) > 0 && target != nil && target.TargetType() == mmsg.TargetGroup && replySeq != 0 {
		msg = mmsg.NewMSG()
		msg.Append(&adapter.ReplySegment{
			ReplySeq: replySeq,
			Sender:   senderID,
			GroupID:  groupID,
		})
		msg.Append(chunk.Elements()...)
	}
	if len(msg.Elements()) > 0 {
		l.SendMsg(msg, target)
	}

	for _, forward := range forwards {
		m := mmsg.NewMSG()
		m.Append(forward)
		l.SendMsg(m, target)
	}
}

// splitMessageLinkResult 把渲染结果拆成「普通元素」与「合并转发元素」两部分。
//
// 成品 x64 0xcb03c0：先 flush result 的文本缓冲（内联 Append，
// itab 0x11311e8 = *adapter.TextSegment），再遍历 result.Elements()：
//   - itab == 0x11310a8（*mmsg.ForwardElement）的元素收进 []*mmsg.ForwardElement；
//   - 其余元素 mmsg.Append 到同一个 mmsg.MSG。
//
// 返回 (chunk *mmsg.MSG, forwards []*mmsg.ForwardElement)；result 为 nil 时返回空 MSG。
// 注：成品此处没有 CutElement 的 itab 比较，不按 Cut 拆分；重建此前的 Cut 拆分属偏差，
// 现按成品对齐（当前唯一解析器 lsp/douyin 不会产出 CutElement）。
func splitMessageLinkResult(result *mmsg.MSG) (*mmsg.MSG, []*mmsg.ForwardElement) {
	msg := mmsg.NewMSG()
	if result == nil {
		return msg, nil
	}
	var forwards []*mmsg.ForwardElement
	for _, e := range result.Elements() {
		if forward, ok := e.(*mmsg.ForwardElement); ok {
			forwards = append(forwards, forward)
			continue
		}
		msg.Append(e)
	}
	return msg, forwards
}

// messageElementsText 从消息元素中提取全部文本内容。
func messageElementsText(elements []adapter.IMessageElement) string {
	var builder strings.Builder
	for _, e := range elements {
		if t, ok := e.(*adapter.TextSegment); ok && t != nil {
			builder.WriteString(t.Content)
		}
	}
	return builder.String()
}
