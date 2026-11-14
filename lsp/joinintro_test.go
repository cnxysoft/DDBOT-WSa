package lsp

// 机器人被拉进群时发「自我介绍」的单测。
//
// 分工：
//   - 短提示 qqofficial.join-intro-tip 也由 adapter 发，但**由这次调用一起发**：
//     lsp 只负责渲染模板、把长文案交给 adapter 的 SendJoinIntro；
//   - 长文案走全局模板 trigger.group.bot_joined.tmpl，lsp 渲染后调 adapter 的
//     SendJoinIntro（结构化 interface 断言出来的扩展能力）。
//
// 三个已经踩过的坑，这组用例专门钉住：
//  1. **不能**对 localutils.GetBot().Bot 直接断言 —— 它的动态类型是 *bot.Bot，只提升
//     *adapter.Messenger 的方法集，Messenger.Adapter 是具名接口字段、不是嵌入，
//     具体 adapter 的额外方法穿透不上来；必须落到 Messenger.Adapter 再断言。
//  2. **不能**把裸 *mmsg.MSG 交给 adapter —— toSegments 的 switch 没有这个类型，
//     必须先 ToCombineMessage 转成 *adapter.SendingMessage。
//  3. **模板渲染为空 / 渲染出错也必须调 SendJoinIntro** —— tip 就在这个方法里，
//     不调 = 只配了 tip 的用户一条都收不到（上一版的真实缺陷）。

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
)

// fakeJoinIntroAdapter 只实现 SendJoinIntro，其余 adapter.Adapter 方法靠嵌入接口占位
// （用不到；真调用会 nil panic，所以用例里只走这一条路）。
type fakeJoinIntroAdapter struct {
	adapter.Adapter

	mu      sync.Mutex
	calls   int
	gotCode int64
	gotMsg  interface{}
	err     error
}

func (f *fakeJoinIntroAdapter) SendJoinIntro(groupCode int64, message interface{}) (int32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.gotCode = groupCode
	f.gotMsg = message
	return 1, f.err
}

func (f *fakeJoinIntroAdapter) snapshot() (int, int64, interface{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.gotCode, f.gotMsg
}

// fakePlainAdapter 是「没实现 SendJoinIntro 的 adapter」——onebot / satori 就是这样。
type fakePlainAdapter struct {
	adapter.Adapter
}

// installTestBot 把 global bot 换成「Messenger.Adapter 是给定 adapter」的 *bot.Bot。
//
// 用 Messenger 复合字面量而不是 adapter.NewMessenger：后者会调 adapter 的 OnXxx 注册回调，
// 嵌入 nil 接口的假 adapter 会直接 panic。
func installTestBot(t *testing.T, impl adapter.Adapter) *bot.Bot {
	t.Helper()
	messenger := &adapter.Messenger{Adapter: impl}
	instance := &bot.Bot{Messenger: messenger}
	prev := localutils.GetBot().Bot
	localutils.GetBot().Bot = instance
	t.Cleanup(func() { localutils.GetBot().Bot = prev })
	return instance
}

// captureLogs 抓 logrus 的日志（lsp 的 logger 挂在标准 logger 上）。
//
// 这个 logrus 版本的 LevelHooks 只有 Add、没有 Remove，所以用完不是摘掉 hook，
// 而是把 enabled 置 false 让它空转 —— 对后面的用例没有任何影响。
type captureLogs struct {
	enabled atomic.Bool

	mu      sync.Mutex
	entries []*logrus.Entry
}

func (c *captureLogs) Levels() []logrus.Level { return logrus.AllLevels }

func (c *captureLogs) Fire(e *logrus.Entry) error {
	if !c.enabled.Load() {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
	return nil
}

func (c *captureLogs) byLevel(level logrus.Level) []*logrus.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*logrus.Entry
	for _, e := range c.entries {
		if e.Level == level {
			out = append(out, e)
		}
	}
	return out
}

func (c *captureLogs) errors() []*logrus.Entry { return c.byLevel(logrus.ErrorLevel) }

func (c *captureLogs) warnings() []*logrus.Entry { return c.byLevel(logrus.WarnLevel) }

func (c *captureLogs) dump() string {
	var b strings.Builder
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.entries {
		b.WriteString(e.Level.String())
		b.WriteString(": ")
		b.WriteString(e.Message)
		b.WriteString(" ")
	}
	return b.String()
}

func attachLogCapture(t *testing.T) *captureLogs {
	t.Helper()
	c := &captureLogs{}
	c.enabled.Store(true)
	logrus.StandardLogger().Hooks.Add(c)
	t.Cleanup(func() { c.enabled.Store(false) })
	return c
}

// stubRender 换掉模板渲染，返回捕获到的模板数据。
func stubRender(t *testing.T, text string) *map[string]interface{} {
	t.Helper()
	got := make(map[string]interface{})
	prev := renderJoinIntro
	renderJoinIntro = func(data map[string]interface{}) (*mmsg.MSG, error) {
		for k, v := range data {
			got[k] = v
		}
		m := mmsg.NewMSG()
		m.Text(text)
		return m, nil
	}
	t.Cleanup(func() { renderJoinIntro = prev })
	return &got
}

// isEmptyIntroMsg 判断 lsp 交出去的是不是「没有模板文案」：nil 或 0 元素的 SendingMessage
// 都算（typed-nil 也算，交给 adapter 的必须是 *adapter.SendingMessage）。
func isEmptyIntroMsg(msg interface{}) bool {
	if msg == nil {
		return true
	}
	sm, ok := msg.(*adapter.SendingMessage)
	if !ok {
		return false
	}
	return sm == nil || len(sm.Elements) == 0
}

// adapter 不实现 SendJoinIntro（onebot / satori）：一条消息都不发，连渲染都不做。
func TestLspGroupJoinIntroSkippedWithoutCapability(t *testing.T) {
	attachLogCapture(t)
	installTestBot(t, &fakePlainAdapter{})

	// 没有任何断言手段能证明「没发」时，反证：渲染器不该被调用。
	called := false
	prev := renderJoinIntro
	renderJoinIntro = func(data map[string]interface{}) (*mmsg.MSG, error) {
		called = true
		m := mmsg.NewMSG()
		m.Text("不该发出去")
		return m, nil
	}
	t.Cleanup(func() { renderJoinIntro = prev })

	Instance.sendGroupJoinIntro(&adapter.GroupInfo{Code: 123456, Name: "测试群"})
	if called {
		t.Fatal("adapter 不实现 SendJoinIntro 时不该走到渲染（onebot / satori 必须完全没变）")
	}
}

// adapter 实现了就发，且模板数据 group_code / group_name / bot_code / bot_name 都对，
// 交出去的必须是 *adapter.SendingMessage（不是裸 *mmsg.MSG）。
func TestLspGroupJoinIntroRendersAndSends(t *testing.T) {
	attachLogCapture(t)
	fake := &fakeJoinIntroAdapter{}
	instance := installTestBot(t, fake)
	instance.Messenger.Uin.Store(10001)
	instance.Nickname = "测试机器人"

	data := stubRender(t, "本机器人负责推送直播与动态更新")

	Instance.sendGroupJoinIntro(&adapter.GroupInfo{Code: 123456, Name: "测试群"})

	calls, code, msg := fake.snapshot()
	if calls != 1 {
		t.Fatalf("实现了 SendJoinIntro 时应当调用 1 次，实际 %d 次", calls)
	}
	if code != 123456 {
		t.Fatalf("群号必须原样传下去，期望 123456，实际 %d", code)
	}
	sending, ok := msg.(*adapter.SendingMessage)
	if !ok {
		t.Fatalf("必须先把渲染结果 ToCombineMessage 成 *adapter.SendingMessage 再交给 adapter"+
			"（toSegments 不认裸 *mmsg.MSG），实际类型 %T", msg)
	}
	if got := textOf(sending); !strings.Contains(got, "本机器人负责推送直播与动态更新") {
		t.Fatalf("发出去的内容应当来自模板渲染，实际 %q", got)
	}

	want := map[string]interface{}{
		"group_code": int64(123456),
		"group_name": "测试群",
		"bot_code":   int64(10001),
		"bot_name":   "测试机器人",
	}
	for k, v := range want {
		if (*data)[k] != v {
			t.Fatalf("模板数据 %s 期望 %v，实际 %v（全部数据：%v）", k, v, (*data)[k], *data)
		}
	}
}

// 模板为空（默认就是 0 字节）：**照样调一次**，只带 nil（tip 在 adapter 那边发）。
//
// 这条是回归线：以前模板空就直接 return，只配了 tip 的用户一条都收不到。
func TestLspGroupJoinIntroEmptyTemplateStillCallsSender(t *testing.T) {
	logs := attachLogCapture(t)
	fake := &fakeJoinIntroAdapter{}
	installTestBot(t, fake)

	// 默认渲染器读的就是那个 0 字节模板，结果为空。
	Instance.sendGroupJoinIntro(&adapter.GroupInfo{Code: 123456, Name: "测试群"})

	calls, _, msg := fake.snapshot()
	if calls != 1 {
		t.Fatalf("模板为空也必须调 SendJoinIntro 一次（否则 tip 永远发不出去），实际 %d 次", calls)
	}
	if !isEmptyIntroMsg(msg) {
		t.Fatalf("模板为空时只能带 nil / 空消息，实际传了 %T %+v", msg, msg)
	}
	if errs := logs.errors(); len(errs) != 0 {
		t.Fatalf("模板为空不是错误，不该打 Error，实际打了 %d 条：%s", len(errs), logs.dump())
	}
}

// 纯空白模板：同样照样调，消息按「空」处理。
func TestLspGroupJoinIntroWhitespaceTemplateStillCallsSender(t *testing.T) {
	logs := attachLogCapture(t)
	fake := &fakeJoinIntroAdapter{}
	installTestBot(t, fake)

	prev := renderJoinIntro
	renderJoinIntro = func(data map[string]interface{}) (*mmsg.MSG, error) {
		m := mmsg.NewMSG()
		m.Text(" \n\t  ")
		return m, nil
	}
	t.Cleanup(func() { renderJoinIntro = prev })

	Instance.sendGroupJoinIntro(&adapter.GroupInfo{Code: 123456, Name: "测试群"})

	calls, _, msg := fake.snapshot()
	if calls != 1 {
		t.Fatalf("纯空白模板也要调 SendJoinIntro（tip 还要发），实际 %d 次", calls)
	}
	if !isEmptyIntroMsg(msg) {
		t.Fatalf("纯空白不是内容，应当当空消息传，实际 %T %+v", msg, msg)
	}
	if errs := logs.errors(); len(errs) != 0 {
		t.Fatalf("纯空白不是错误，不该打 Error，实际打了 %d 条：%s", len(errs), logs.dump())
	}
}

// 模板渲染失败（缺失 / 语法错）：**照样调**，只带 tip，并打一条 Warn 说清原因。
func TestLspGroupJoinIntroRenderErrorStillCallsSender(t *testing.T) {
	logs := attachLogCapture(t)
	fake := &fakeJoinIntroAdapter{}
	installTestBot(t, fake)

	prev := renderJoinIntro
	renderJoinIntro = func(data map[string]interface{}) (*mmsg.MSG, error) {
		return nil, errors.New("template: trigger.group.bot_joined.tmpl: unexpected EOF")
	}
	t.Cleanup(func() { renderJoinIntro = prev })

	Instance.sendGroupJoinIntro(&adapter.GroupInfo{Code: 123456, Name: "测试群"})

	calls, _, msg := fake.snapshot()
	if calls != 1 {
		t.Fatalf("渲染失败只该少发一条长文案，tip 还得发：SendJoinIntro 应当被调 1 次，实际 %d 次", calls)
	}
	if !isEmptyIntroMsg(msg) {
		t.Fatalf("渲染失败时只能带 tip（空消息），实际传了 %T %+v", msg, msg)
	}
	var warned bool
	for _, e := range logs.warnings() {
		if strings.Contains(e.Message, "123456") && strings.Contains(e.Message, "unexpected EOF") {
			warned = true
			break
		}
	}
	if !warned {
		t.Fatalf("渲染失败要打一条 Warn（含群号与原因），实际日志：%s", logs.dump())
	}
	if errs := logs.errors(); len(errs) != 0 {
		t.Fatalf("渲染失败本身不该当成发送失败：不该打 Error（adapter 没报错），实际：%s", logs.dump())
	}
}

// 发送失败：打一条 Error，含群号与原因。
func TestLspGroupJoinIntroSendFailureLogsError(t *testing.T) {
	logs := attachLogCapture(t)
	fake := &fakeJoinIntroAdapter{err: errors.New("假平台 40034105 主动消息失败, 无权限")}
	installTestBot(t, fake)
	stubRender(t, "本机器人负责推送直播与动态更新")

	Instance.sendGroupJoinIntro(&adapter.GroupInfo{Code: 123456, Name: "测试群"})

	errs := logs.errors()
	if len(errs) == 0 {
		t.Fatalf("发送失败必须打 Error，实际一条都没有：%s", logs.dump())
	}
	var found bool
	for _, e := range errs {
		if strings.Contains(e.Message, "123456") && strings.Contains(e.Message, "40034105") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("Error 日志必须含群号与失败原因，实际：%s", logs.dump())
	}
}

// 钉住取值路径：GetBot().Bot（adapter.BotCaller）本身拿不到扩展能力，
// 必须落到 Messenger.Adapter 上断言。这条用例红了就说明有人改回了错误的那条路。
func TestLspJoinIntroSenderMustComeFromMessengerAdapter(t *testing.T) {
	fake := &fakeJoinIntroAdapter{}
	installTestBot(t, fake)

	if _, ok := localutils.GetBot().Bot.(joinIntroSender); ok {
		t.Fatal("GetBot().Bot 的动态类型是 *bot.Bot，不该直接满足 joinIntroSender" +
			"（Messenger.Adapter 是具名字段、不是嵌入，方法不会穿透）")
	}
	sender, ok := findJoinIntroSender()
	if !ok || sender == nil {
		t.Fatal("从 Messenger.Adapter 必须能断言出 joinIntroSender")
	}
	if reflect.ValueOf(sender).Pointer() != reflect.ValueOf(fake).Pointer() {
		t.Fatalf("取到的应当是装进 Messenger.Adapter 的那个 adapter，实际 %T", sender)
	}
}

// ------------------------------------------------------------------ bot_name 的四级取值链

// fakeNamerAdapter 在能发自我介绍的基础上再加 GetBotName —— 名字是**独立**的可选能力，
// 故意不并进 joinIntroSender：只想支持自我介绍、不提供名字的 adapter 不该被卡住。
type fakeNamerAdapter struct {
	fakeJoinIntroAdapter
	name string
}

func (f *fakeNamerAdapter) GetBotName() string { return f.name }

// installJoinIntroGroup 给测试 bot 装一张成员表：机器人自己在群里的昵称是 nickname。
func installJoinIntroGroup(t *testing.T, instance *bot.Bot, groupCode, botUin int64, nickname string) {
	t.Helper()
	instance.Messenger.Uin.Store(botUin)
	instance.Messenger.GroupList = []*adapter.GroupInfo{{
		Code: groupCode,
		Name: "测试群",
		Members: []*adapter.GroupMemberInfo{{
			Uin:      botUin,
			Nickname: nickname,
		}},
	}}
}

// captureJoinIntroBotName 跑一次自我介绍，返回模板数据里的 bot_name。
func captureJoinIntroBotName(t *testing.T) *map[string]interface{} {
	t.Helper()
	data := stubRender(t, "本机器人负责推送直播与动态更新")
	Instance.sendGroupJoinIntro(&adapter.GroupInfo{Code: 123456, Name: "测试群"})
	return data
}

// 第 1 级：*bot.Bot.Nickname 非空时最优先，adapter 名字与成员表都不许抢先。
func TestLspJoinIntroBotNamePrefersBotNickname(t *testing.T) {
	attachLogCapture(t)
	fake := &fakeNamerAdapter{name: "adapter 名字"}
	instance := installTestBot(t, fake)
	instance.Nickname = "bot 昵称"
	installJoinIntroGroup(t, instance, 123456, 10001, "成员表名字")

	data := captureJoinIntroBotName(t)

	if got := (*data)["bot_name"]; got != "bot 昵称" {
		t.Fatalf("Nickname 非空时必须最优先，期望 bot 昵称，实际 %v（全部数据：%v）", got, *data)
	}
}

// 第 2 级：Nickname 空、adapter 能提供名字 → 用 adapter 的（成员表不许抢先）。
func TestLspJoinIntroBotNameFallsBackToAdapterName(t *testing.T) {
	attachLogCapture(t)
	fake := &fakeNamerAdapter{name: "adapter 名字"}
	instance := installTestBot(t, fake)
	installJoinIntroGroup(t, instance, 123456, 10001, "成员表名字")

	data := captureJoinIntroBotName(t)

	if got := (*data)["bot_name"]; got != "adapter 名字" {
		t.Fatalf("Nickname 空时应当采用 adapter 的 GetBotName()，期望 adapter 名字，实际 %v（全部数据：%v）", got, *data)
	}
}

// 第 3 级：Nickname 与 adapter 名字都空 → 回落到群成员表里机器人自己那条。
func TestLspJoinIntroBotNameFallsBackToMemberList(t *testing.T) {
	attachLogCapture(t)
	fake := &fakeNamerAdapter{name: ""}
	instance := installTestBot(t, fake)
	installJoinIntroGroup(t, instance, 123456, 10001, "成员表名字")

	data := captureJoinIntroBotName(t)

	if got := (*data)["bot_name"]; got != "成员表名字" {
		t.Fatalf("前两级都空时应当回落到成员表，期望 成员表名字，实际 %v（全部数据：%v）", got, *data)
	}
}

// 第 4 级：全都取不到 → 空串，不报错（模板自己兜底）。
func TestLspJoinIntroBotNameEmptyEverywhere(t *testing.T) {
	logs := attachLogCapture(t)
	fake := &fakeNamerAdapter{name: ""}
	instance := installTestBot(t, fake)
	installJoinIntroGroup(t, instance, 123456, 10001, "")

	data := captureJoinIntroBotName(t)

	if got := (*data)["bot_name"]; got != "" {
		t.Fatalf("四级都取不到时 bot_name 必须是空串，实际 %v（全部数据：%v）", got, *data)
	}
	if errs := logs.errors(); len(errs) != 0 {
		t.Fatalf("取不到名字不是错误，不该打 Error，实际打了 %d 条：%s", len(errs), logs.dump())
	}
}

// adapter 不实现 GetBotName（onebot / satori，或将来只做了自我介绍的 adapter）：
// 行为与加名字之前完全一样 —— 直接回落到成员表，不 panic、不报错。
func TestLspJoinIntroBotNameWithoutNamerFallsBackToMemberList(t *testing.T) {
	attachLogCapture(t)
	fake := &fakeJoinIntroAdapter{}
	if _, ok := interface{}(fake).(interface{ GetBotName() string }); ok {
		t.Fatal("fakeJoinIntroAdapter 不该实现 GetBotName，这条用例才有意义")
	}
	instance := installTestBot(t, fake)
	installJoinIntroGroup(t, instance, 123456, 10001, "成员表名字")

	data := captureJoinIntroBotName(t)

	if got := (*data)["bot_name"]; got != "成员表名字" {
		t.Fatalf("adapter 没有 GetBotName 时应保持旧行为（回落到成员表），期望 成员表名字，实际 %v（全部数据：%v）", got, *data)
	}
}

// textOf 把 SendingMessage 的文本段拼起来，方便断言内容。
func textOf(m *adapter.SendingMessage) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	for _, e := range m.Elements {
		if ts, ok := e.(*adapter.TextSegment); ok {
			b.WriteString(ts.Content)
		}
	}
	return b.String()
}
