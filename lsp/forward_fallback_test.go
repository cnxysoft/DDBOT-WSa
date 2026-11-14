package lsp

// 合并转发在「没有合并转发的通道」上必须降级成一条普通文本消息，不能整条推送失败。
//
// 背景：DDBOT 模板可以用 {{ forward (list ...) }} 构建合并转发；QQ 官方 v2 只支持
// msg_type 0/2/7/8，没有合并转发，qqofficial 的 SendGroupForwardMessage 直接返回
// unsupported —— 以前的结果是**整条推送没了**（不是跳过这一段）。
//
// 兜底必须做在 lsp：转发节点在 lsp 这层还完整（ForwardElement.Nodes），到了 adapter
// 层 ForwardElement.PackToElement 返回 nil，内容已经没了。
//
// 能力判断走**可选接口**：adapter 实现 SupportsForward() bool 时才问它支不支持；
// 不实现（onebot / satori）一律按支持处理，行为必须和以前完全一致。
//
// 断言必须落在 Messenger.Adapter 上 —— bot.Instance / localutils.GetBot().Bot 的
// 动态类型是 *bot.Bot，只提升 *adapter.Messenger 的方法集，而 Messenger.Adapter 是
// 具名接口字段、不是嵌入，具体 adapter 的扩展方法不会穿透上来。

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
)

// fakeRecordingAdapter 记录 adapter 层真正收到的请求：普通发送与合并转发分开数。
type fakeRecordingAdapter struct {
	adapter.Adapter

	mu              sync.Mutex
	groupSends      []string
	privateSends    []string
	groupForwards   int
	privateForwards int
	sendErr         error
}

func (f *fakeRecordingAdapter) IsConnected() bool      { return true }
func (f *fakeRecordingAdapter) GetAdapterName() string { return "fake-recording" }

func (f *fakeRecordingAdapter) SendGroupMessage(groupID int64, message interface{}) (int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupSends = append(f.groupSends, segmentsText(message))
	if f.sendErr != nil {
		return 0, f.sendErr
	}
	return int32(len(f.groupSends)), nil
}

func (f *fakeRecordingAdapter) SendPrivateMessage(userID int64, message interface{}) (int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.privateSends = append(f.privateSends, segmentsText(message))
	if f.sendErr != nil {
		return 0, f.sendErr
	}
	return int32(len(f.privateSends)), nil
}

func (f *fakeRecordingAdapter) SendGroupForwardMessage(groupCode int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groupForwards++
	return 1, "", nil
}

func (f *fakeRecordingAdapter) SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.privateForwards++
	return 1, "", nil
}

func (f *fakeRecordingAdapter) snapshot() (group []string, private []string, groupFwd int, privateFwd int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.groupSends...), append([]string(nil), f.privateSends...), f.groupForwards, f.privateForwards
}

// segmentsText 把 adapter 收到的那条普通消息拼成文本，方便断言内容与顺序。
func segmentsText(message interface{}) string {
	var b strings.Builder
	switch msg := message.(type) {
	case []adapter.MessageSegment:
		for _, seg := range msg {
			if seg.Type != "text" {
				continue
			}
			if s, ok := seg.Data["text"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

// 三种能力形态：不实现 SupportsForward（onebot / satori）、明确 false（qqofficial）、明确 true。
type fakeForwardNoCapability struct{ *fakeRecordingAdapter }

type fakeForwardUnsupported struct{ *fakeRecordingAdapter }

func (f *fakeForwardUnsupported) SupportsForward() bool { return false }

type fakeForwardSupported struct{ *fakeRecordingAdapter }

func (f *fakeForwardSupported) SupportsForward() bool { return true }

// installForwardBot 把全局 bot.Instance 换成「Messenger.Adapter 是给定 adapter」的 *bot.Bot。
// module.go 走的是 bot.Instance（不是 GetBot().Bot），这里必须换同一个。
func installForwardBot(t *testing.T, impl adapter.Adapter) *bot.Bot {
	t.Helper()
	messenger := &adapter.Messenger{Adapter: impl}
	messenger.Online.Store(true)
	instance := &bot.Bot{Messenger: messenger}
	prev := bot.Instance
	bot.Instance = instance
	t.Cleanup(func() { bot.Instance = prev })
	return instance
}

// newForwardLsp 建一个只带发送所需状态管理器的 Lsp（不碰全局 Instance）。
func newForwardLsp(t *testing.T) *Lsp {
	t.Helper()
	test.InitBuntdb(t)
	t.Cleanup(func() { test.CloseBuntdb(t) })
	return &Lsp{
		PermissionStateManager: permission.NewStateManager(),
		LspStateManager:        NewStateManager(),
	}
}

// forwardNode 造一个 onebot-v11 节点；content 可以是字符串，也可以是分片数组。
func forwardNode(senderName string, content interface{}) map[string]interface{} {
	data := map[string]interface{}{"content": content}
	if senderName != "" {
		data["senderName"] = senderName
	}
	return map[string]interface{}{"type": "node", "data": data}
}

func textSeg(s string) map[string]interface{} {
	return map[string]interface{}{"type": "text", "data": map[string]interface{}{"text": s}}
}

func imageSeg(url string) map[string]interface{} {
	return map[string]interface{}{"type": "image", "data": map[string]interface{}{"file": url}}
}

// buildForwardMSG 造一条「只含合并转发」的消息。
func buildForwardMSG(nodes []map[string]interface{}, opts *adapter.ForwardOptions) *mmsg.MSG {
	m := mmsg.NewMSG()
	var fe *mmsg.ForwardElement
	if opts != nil {
		fe = mmsg.NewForwardElementWithOptions(nodes, &mmsg.ForwardOptions{
			Prompt: opts.Prompt, Source: opts.Source, Summary: opts.Summary, News: opts.News,
		})
	} else {
		fe = mmsg.NewForwardElement(nodes)
	}
	m.Append(fe)
	return m
}

// 核心用例：3 个节点（两条有文本、一条纯图片）→ 只发 1 条普通消息，
// 文本顺序正确、senderName 作前缀、图片被跳过、图片那个节点什么都不留。
func TestLspForwardFallbackMergesNodesIntoOneMessage(t *testing.T) {
	_ = attachLogCapture(t)
	fake := &fakeForwardUnsupported{&fakeRecordingAdapter{}}
	installForwardBot(t, fake)
	l := newForwardLsp(t)

	nodes := []map[string]interface{}{
		forwardNode("张三", "第一条：B站开播了"),
		forwardNode("李四", []map[string]interface{}{
			textSeg("第二条：带分片的内容"),
			imageSeg("https://example.com/cover.png"),
		}),
		forwardNode("王五", []map[string]interface{}{imageSeg("https://example.com/only.png")}),
	}
	res := l.SendMsg(buildForwardMSG(nodes, nil), mmsg.NewGroupTarget(123456))

	group, private, groupFwd, privateFwd := fake.snapshot()
	if groupFwd != 0 || privateFwd != 0 {
		t.Fatalf("adapter 明确说不支持合并转发时不该再走 SendGroupForwardMessage，实际 group=%d private=%d", groupFwd, privateFwd)
	}
	if len(private) != 0 {
		t.Fatalf("群消息不该走私聊通道，实际 %d 条", len(private))
	}
	if len(group) != 1 {
		t.Fatalf("合并转发必须降级成**一条**普通消息，实际发了 %d 条：%q", len(group), group)
	}
	text := group[0]
	for _, want := range []string{"第一条：B站开播了", "第二条：带分片的内容"} {
		if !strings.Contains(text, want) {
			t.Fatalf("降级文本必须包含节点内容 %q，实际 %q", want, text)
		}
	}
	if strings.Contains(text, "example.com") || strings.Contains(text, "王五") {
		t.Fatalf("纯图片节点不该留下任何痕迹（图没法在普通消息里还原，发送者也不该单独出现），实际 %q", text)
	}
	if !strings.Contains(text, "张三：第一条：B站开播了") {
		t.Fatalf("有 senderName 时必须输出成「发送者：内容」，实际 %q", text)
	}
	if !strings.Contains(text, "李四：第二条：带分片的内容") {
		t.Fatalf("分片数组里的文本分片必须拼起来并带 senderName 前缀，实际 %q", text)
	}
	if i, j := strings.Index(text, "张三"), strings.Index(text, "李四"); i < 0 || j < 0 || i > j {
		t.Fatalf("节点顺序必须保持原样（张三在前的），实际 %q", text)
	}
	if len(res) != 1 {
		t.Fatalf("SendMsg 必须返回一条结果，实际 %d 条", len(res))
	}
	if gm, ok := res[0].(*adapter.GroupMessage); !ok || gm.ID == -1 {
		t.Fatalf("降级发送成功后要返回带 id 的 GroupMessage，实际 %#v", res[0])
	}
}

// options.Prompt 非空 → 作为第一行标题。
func TestLspForwardFallbackPromptBecomesFirstLine(t *testing.T) {
	_ = attachLogCapture(t)
	fake := &fakeForwardUnsupported{&fakeRecordingAdapter{}}
	installForwardBot(t, fake)
	l := newForwardLsp(t)

	nodes := []map[string]interface{}{forwardNode("张三", "开播了")}
	l.SendMsg(buildForwardMSG(nodes, &adapter.ForwardOptions{Prompt: "直播汇总"}), mmsg.NewGroupTarget(123456))

	group, _, _, _ := fake.snapshot()
	if len(group) != 1 {
		t.Fatalf("应当只发一条普通消息，实际 %d 条", len(group))
	}
	lines := strings.Split(group[0], "\n")
	if len(lines) < 2 {
		t.Fatalf("标题与内容要分行，实际 %q", group[0])
	}
	if lines[0] != "直播汇总" {
		t.Fatalf("options.Prompt 必须是第一行，实际第一行 %q（全文 %q）", lines[0], group[0])
	}
	if !strings.Contains(lines[1], "开播了") {
		t.Fatalf("第二行应当是节点内容，实际 %q", lines[1])
	}
}

// 全是图片 → 不发空消息，只打一条 Warn。
func TestLspForwardFallbackNoTextLogsWarnAndSendsNothing(t *testing.T) {
	logs := attachLogCapture(t)
	fake := &fakeForwardUnsupported{&fakeRecordingAdapter{}}
	installForwardBot(t, fake)
	l := newForwardLsp(t)

	nodes := []map[string]interface{}{
		forwardNode("张三", []map[string]interface{}{imageSeg("https://example.com/1.png")}),
		forwardNode("李四", []map[string]interface{}{imageSeg("https://example.com/2.png")}),
	}
	l.SendMsg(buildForwardMSG(nodes, nil), mmsg.NewGroupTarget(123456))

	group, private, groupFwd, _ := fake.snapshot()
	if len(group)+len(private)+groupFwd != 0 {
		t.Fatalf("没有可降级的文本时一个请求都不许发，实际 group=%d private=%d forward=%d", len(group), len(private), groupFwd)
	}
	warns := logs.warnings()
	if len(warns) != 1 {
		t.Fatalf("必须打且只打一条 Warn，实际 %d 条：%s", len(warns), logs.dump())
	}
	if !strings.Contains(warns[0].Message, "没有可降级的文本") {
		t.Fatalf("Warn 要说清原因（合并转发内容里没有可降级的文本），实际 %q", warns[0].Message)
	}
}

// onebot / satori：不实现 SupportsForward → 走的还是原来的 SendGroupForwardMessage，
// 一条普通消息都不许发（行为完全不变）。
func TestLspForwardUnchangedWithoutCapability(t *testing.T) {
	_ = attachLogCapture(t)
	fake := &fakeForwardNoCapability{&fakeRecordingAdapter{}}
	installForwardBot(t, fake)
	l := newForwardLsp(t)

	nodes := []map[string]interface{}{forwardNode("张三", "原样转发")}
	l.SendMsg(buildForwardMSG(nodes, nil), mmsg.NewGroupTarget(123456))

	group, _, groupFwd, _ := fake.snapshot()
	if groupFwd != 1 {
		t.Fatalf("adapter 不实现 SupportsForward 时必须按支持处理、走原来的合并转发接口，实际调用 %d 次", groupFwd)
	}
	if len(group) != 0 {
		t.Fatalf("不该降级成普通消息，实际发了 %d 条：%q", len(group), group)
	}
}

// 明确返回 true（真的支持合并转发的 adapter）→ 也走原路径。
func TestLspForwardUnchangedWhenCapabilityReportsTrue(t *testing.T) {
	_ = attachLogCapture(t)
	fake := &fakeForwardSupported{&fakeRecordingAdapter{}}
	installForwardBot(t, fake)
	l := newForwardLsp(t)

	nodes := []map[string]interface{}{forwardNode("张三", "原样转发")}
	l.SendMsg(buildForwardMSG(nodes, nil), mmsg.NewGroupTarget(123456))

	group, _, groupFwd, _ := fake.snapshot()
	if groupFwd != 1 {
		t.Fatalf("SupportsForward() 为 true 时必须走原合并转发接口，实际调用 %d 次", groupFwd)
	}
	if len(group) != 0 {
		t.Fatalf("支持合并转发时不该降级，实际发了 %d 条普通消息", len(group))
	}
}

// 私聊入口同样要兜底。
func TestLspForwardFallbackCoversPrivateTarget(t *testing.T) {
	_ = attachLogCapture(t)
	fake := &fakeForwardUnsupported{&fakeRecordingAdapter{}}
	installForwardBot(t, fake)
	l := newForwardLsp(t)

	nodes := []map[string]interface{}{
		forwardNode("张三", "私聊第一条"),
		forwardNode("李四", []map[string]interface{}{imageSeg("https://example.com/x.png")}),
	}
	l.SendMsg(buildForwardMSG(nodes, nil), mmsg.NewPrivateTarget(10001))

	group, private, groupFwd, privateFwd := fake.snapshot()
	if privateFwd != 0 || groupFwd != 0 {
		t.Fatalf("不支持合并转发时哪条转发接口都不该调，实际 group=%d private=%d", groupFwd, privateFwd)
	}
	if len(group) != 0 {
		t.Fatalf("私聊不该走群通道，实际 %d 条", len(group))
	}
	if len(private) != 1 {
		t.Fatalf("私聊也要降级成一条普通消息，实际 %d 条：%q", len(private), private)
	}
	if !strings.Contains(private[0], "张三：私聊第一条") {
		t.Fatalf("私聊降级文本不对：%q", private[0])
	}
}

// 降级发送失败：打 Error，含群号；adapter 的真实错误也要留在日志里（原因）。
func TestLspForwardFallbackSendFailureLogsError(t *testing.T) {
	logs := attachLogCapture(t)
	fake := &fakeForwardUnsupported{&fakeRecordingAdapter{sendErr: errors.New("假平台 40034105 主动消息失败")}}
	installForwardBot(t, fake)
	l := newForwardLsp(t)

	nodes := []map[string]interface{}{forwardNode("张三", "发不出去的内容")}
	res := l.SendMsg(buildForwardMSG(nodes, nil), mmsg.NewGroupTarget(123456))

	errs := logs.errors()
	if len(errs) == 0 {
		t.Fatalf("发送失败必须打 Error，实际一条都没有：%s", logs.dump())
	}
	var hasCode, hasReason bool
	for _, e := range errs {
		if strings.Contains(e.Message, "123456") {
			hasCode = true
		}
		if strings.Contains(e.Message, "40034105") {
			hasReason = true
		}
	}
	if !hasCode {
		t.Fatalf("Error 日志必须含群号，实际：%s", logs.dump())
	}
	if !hasReason {
		t.Fatalf("Error 日志必须含失败原因，实际：%s", logs.dump())
	}
	if gm, ok := res[0].(*adapter.GroupMessage); !ok || gm.ID != -1 {
		t.Fatalf("发送失败时 SendMsg 应返回 ID=-1 的占位结果，实际 %#v", res[0])
	}
}
