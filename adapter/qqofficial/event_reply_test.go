package qqofficial

// 「用事件 id（event_id）做被动回复」的单测。
//
// 一手实测（2026-11-12，别推翻）：
//  1. GROUP_ADD_ROBOT 的 WS 帧**顶层**带 id（事件体 d 里没有），格式 GROUP_ADD_ROBOT:<uuid>：
//     {"op":0,"s":3,"t":"GROUP_ADD_ROBOT","id":"GROUP_ADD_ROBOT:1fbf…","d":{…}}
//     也就是说 ws.go 的 wsPayload.ID 解析得到它，但事件处理里要一路传下去才能用上。
//  2. 把它当 event_id 发消息，在 allow_proactive_msg=false 的群里也能发出去：
//     {"content":"…","msg_type":0,"event_id":"GROUP_ADD_ROBOT:1fbf…"}            -> http=200
//     {"content":"…","msg_type":0,"event_id":"GROUP_ADD_ROBOT:1fbf…","msg_seq":1} -> http=200
//     同一个群同一天用 msg_id 主动发是 40034105 无权限，用 event_id 却成功 —— 它是被动通道。
//
// 这组用例固定的是下面的分工（**不是「欢迎」，是机器人进群后的自我介绍**）：
//  1. handleGroupAdd **什么都不发**，只把进群帧的 event_id 按内部群号登记成一次性锚点；
//  2. lsp 收到 GroupJoinEvent 后调 SendJoinIntro，tip 与长文案都在**这一次调用里**发：
//     先发短提示 qqofficial.join-intro-tip，再发 lsp 渲染的 trigger.group.bot_joined.tmpl；
//     顺序由同一次调用保证，不再靠等待，也不再是两条并发链路；
//  3. 两条共用**同一个** event_id（实测同一个 event_id 连发两条都是 http=200）；
//  4. 锚点只有 SendJoinIntro take，取不到就报错、绝不回退主动发送；
//  5. 普通 SendGroupMessage **不带** event_id、也不读写锚点（回归）；
//  6. msg_id 那条被动老路行为不变（回归）；
//  7. event_id 能让没有主动消息权限的群发出消息；
//  8. 模板渲染为空时 lsp **照样**调（只带 tip），只配 tip 的用户不会一条都收不到。
//
// 全部走 httptest 起的假平台（回环地址），不联网、不碰真机。

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/gorilla/websocket"
)

// eventIDFakeFrameID 是实测那条进群帧的顶层 id（格式与真机一致，只换了 uuid 之外的部分）。
const eventIDFakeFrameID = "GROUP_ADD_ROBOT:1fbfcd00-71bc-4971-b47f-66a2b7bc7794"

// eventIDProactiveDeniedBody 是实测的无主动消息权限响应体（HTTP 200，业务码在 body 里）。
const eventIDProactiveDeniedBody = `{"message":"主动消息失败, 无权限","code":40034105,"err_code":40034105}`

// 进群自我介绍的两条文案（用互不重叠的标记词，断言才不会互相串）。
//
// 注意这里用的是**完整键名**（"qqofficial." + 键），故意不在测试里引用常量：
// 键名就是写进 application.yaml 的对外契约，改了就得分清是「实现变了」还是「配置名变了」。
const (
	joinIntroTipKey  = "qqofficial.join-intro-tip"
	joinIntroTipText = "@我发送 菜单 查看订阅列表"
	joinIntroTipMark = "菜单"

	// joinIntroTemplateText 是 lsp 渲染 trigger.group.bot_joined.tmpl 之后交给
	// adapter 的那条长文案（内容由用户自己填，adapter 只负责照发）。
	joinIntroTemplateText = "本机器人负责推送直播与动态更新，发送 帮助 查看全部命令"
	joinIntroTemplateMark = "帮助"

	// 下面两个是**已废弃的键名**，本适配层一概不认：join-welcome-tip（正确键名是
	// join-intro-tip）、join-welcome-text（长文案走全局模板，不是配置项）。留着这两个
	// 名字专门钉住「废弃键名不应再生效」，免得以后有人把这条路悄悄加回来。
	joinIntroOldTipKey  = "qqofficial.join-welcome-tip"
	joinIntroOldTextKey = "qqofficial.join-welcome-text"
	joinIntroOldText    = "本机器人负责推送直播与动态更新，发送 帮助 查看全部命令"
)

// setJoinIntroTip 配好短提示，用例结束清回空（配置是进程级全局的，不清会漏到别的用例里）。
func setJoinIntroTip(t *testing.T, tip string) {
	t.Helper()
	config.GlobalConfig.Set(joinIntroTipKey, tip)
	t.Cleanup(func() {
		config.GlobalConfig.Set(joinIntroTipKey, "")
		config.GlobalConfig.Set(joinIntroOldTipKey, "")
		config.GlobalConfig.Set(joinIntroOldTextKey, "")
	})
}

// joinIntroSendingMessage 造一条「lsp 已经渲染好」的消息。
//
// 真实路径里 lsp 传的就是 *adapter.SendingMessage（m.ToCombineMessage(...) 的结果）——
// 裸 *mmsg.MSG 传不过去，toSegments 的 switch 没有那个类型。
func joinIntroSendingMessage(text string) *adapter.SendingMessage {
	return &adapter.SendingMessage{Elements: []adapter.IMessageElement{&adapter.TextSegment{Content: text}}}
}

// eventIDGroupAddFrame 是实测 GROUP_ADD_ROBOT 的 d（注意：**没有** id 字段）。
func eventIDGroupAddFrame(openid string) string {
	return `{"group_openid":"` + openid + `","op_member_openid":"` + testOwnerOpen + `","timestamp":1794482058}`
}

// eventIDFakeSend 是一条被假平台收到的发送请求（多记 event_id / msg_id / msg_seq / 状态码 / 原始体）。
type eventIDFakeSend struct {
	path    string
	msgType int
	content string
	eventID string
	msgID   string
	msgSeq  int
	status  int
	raw     string
}

// eventIDFakePlatform 是只认「换 token / 发消息」的假平台，顺便把请求体全录下来。
//
// denyUnanchored 复现实测那个群：allow_proactive_msg=false，请求里既没有 event_id
// 也没有 msg_id 就回 40034105（主动消息无权限）。这是这个通道上绕不过去的一道墙。
//
// failContent 是「正文含这段字的发送一律 500」：用来造「第一条成功、第二条失败」或反过来。
type eventIDFakePlatform struct {
	*httptest.Server

	denyUnanchored bool
	failContent    string

	mu    sync.Mutex
	sends []eventIDFakeSend
}

func newEventIDFakePlatform(t *testing.T) *eventIDFakePlatform {
	t.Helper()
	p := &eventIDFakePlatform{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", ContentTypeJSON)
		switch {
		case strings.HasSuffix(r.URL.Path, PathGetAppAccessToken):
			_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":7200}`))
		case strings.HasSuffix(r.URL.Path, "/messages"):
			var req sendMessageRequest
			_ = json.Unmarshal(raw, &req)
			status := http.StatusOK
			switch {
			case p.failContent != "" && strings.Contains(req.Content, p.failContent):
				status = http.StatusInternalServerError
			case p.denyUnanchored && req.EventID == "" && req.MsgID == "":
				// 40034105 是 HTTP 200 + body 里的业务码，和真机一致。
			}
			p.mu.Lock()
			p.sends = append(p.sends, eventIDFakeSend{
				path:    r.URL.Path,
				msgType: req.MsgType,
				content: req.Content,
				eventID: req.EventID,
				msgID:   req.MsgID,
				msgSeq:  req.MsgSeq,
				status:  status,
				raw:     string(raw),
			})
			p.mu.Unlock()
			if status != http.StatusOK {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"message":"网关内部错误"}`))
				return
			}
			if p.denyUnanchored && req.EventID == "" && req.MsgID == "" {
				_, _ = w.Write([]byte(eventIDProactiveDeniedBody))
				return
			}
			_, _ = w.Write([]byte(`{"id":"MSG_EVENT_FAKE_1","timestamp":"2026-11-12T19:30:00+08:00","ext_info":{"ref_idx":"REFIDX_EVENT_FAKE_1"}}`))
		default:
			// bot_state / info 之类（进群事件会顺手起后台任务打它们）：给空对象，别刷屏。
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(p.Server.Close)
	return p
}

func (p *eventIDFakePlatform) sent() []eventIDFakeSend {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]eventIDFakeSend(nil), p.sends...)
}

// dump 把收到的请求体拼成一行，断言失败时贴出来（比只报「期望 X」好排查）。
func (p *eventIDFakePlatform) dump() string {
	var b strings.Builder
	for i, s := range p.sent() {
		b.WriteString("[")
		b.WriteString(string(rune('0' + i)))
		b.WriteString("] status=")
		b.WriteString(string(rune('0' + s.status/100)))
		b.WriteString("xx ")
		b.WriteString(s.raw)
		b.WriteString(" ")
	}
	return b.String()
}

// waitForSends 等到假平台收到的发送请求满足 want 为止。
//
// 进群自我介绍现在是**同步**在 SendJoinIntro 里发的，普通用例直接断言就行；
// 这个 helper 留个兜底，给后续真的异步的场合用。
func waitForSends(t *testing.T, p *eventIDFakePlatform, what string, want func([]eventIDFakeSend) bool) []eventIDFakeSend {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		sends := p.sent()
		if want(sends) {
			return sends
		}
		if time.Now().After(deadline) {
			t.Fatalf("5 秒内没有等到 %s（假平台收到 %d 条请求：%s）", what, len(sends), p.dump())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// ---------------------------------------------------------------- 发送层：请求体

// 请求体里必须有 event_id，且**不许**带 msg_seq / msg_id（event_id 优先，并且不参与
// msg_id 那套序号账本；实测不带 msg_seq 也能发出去）。
func TestQQOfficialSendEventIDWithoutMsgSeq(t *testing.T) {
	p := newEventIDFakePlatform(t)
	c := newSendTestClient(t, p.Server, sendRetryTestPolicy(2))

	res, err := c.Send(context.Background(), sendTestGroup(), SendOptions{
		Content: "机器人进群了",
		// 故意把两条通道都给上：event_id 必须赢，msg_id/msg_seq 一条都不许发。
		MsgID:   "ROBOT1.0_SHOULD_NOT_BE_SENT",
		EventID: eventIDFakeFrameID,
	})
	if err != nil {
		t.Fatalf("带 event_id 的发送不该失败：%v（假平台收到：%s）", err, p.dump())
	}
	sends := p.sent()
	if len(sends) != 1 {
		t.Fatalf("一次 Send 只该发 1 条请求，实际 %d 条：%s", len(sends), p.dump())
	}
	got := sends[0]
	if got.eventID != eventIDFakeFrameID {
		t.Fatalf("请求体里必须带上 event_id=%q，实际 %q（原文 %s）", eventIDFakeFrameID, got.eventID, got.raw)
	}
	if !strings.Contains(got.raw, "event_id") {
		t.Fatalf("请求体的 JSON 键必须就叫 event_id，实际原文 %s", got.raw)
	}
	if got.msgSeq != 0 || strings.Contains(got.raw, "msg_seq") {
		t.Fatalf("走 event_id 时不该发 msg_seq（实测不需要，硬发还会和 msg_id 的序号账本打架），实际原文 %s", got.raw)
	}
	if got.msgID != "" || strings.Contains(got.raw, "msg_id") {
		t.Fatalf("event_id 与 msg_id 互斥：给了 event_id 就不许再发 msg_id，实际原文 %s", got.raw)
	}
	if !res.EventPassive {
		t.Fatalf("SendResult 必须能看出这次走了 event 被动（EventPassive=true），实际 %+v", *res)
	}
	if res.Passive {
		t.Errorf("Passive 专指 msg_id+msg_seq 那条路，event_id 不该把它置真：%+v", *res)
	}
}

// ---------------------------------------------------------------- 进群自我介绍（tip 与模板都在 SendJoinIntro 里发）

// handleGroupAdd 只登记锚点，**一条都不发** —— 顺序能保证的前提就是它不再抢跑。
func TestQQOfficialGroupAddSendsNothing(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, joinIntroTipText)

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))

	time.Sleep(200 * time.Millisecond)
	if n := len(p.sent()); n != 0 {
		t.Fatalf("进群事件自己不该发任何东西（tip 已经挪进 SendJoinIntro，抢跑就打乱顺序），"+
			"实际发了 %d 条：%s", n, p.dump())
	}
	if n := a.introAnchors.count(); n != 1 {
		t.Fatalf("进群事件必须留下 1 条锚点给 SendJoinIntro 取，实际表里 %d 条", n)
	}
}

// 坑 1 回归：锚点必须在 emitNoticeEvent **之前**登记。
//
// lsp 的 GroupJoinEvent 回调是**同步**的：emitNoticeEvent 那一行里它就会立刻回来
// 调 SendJoinIntro 取锚点（真机日志实证：进群后 1 秒就报「没有进群事件锚点」）。
// 这里让假 lsp 一模一样地同步回调，喂一条 GROUP_ADD_ROBOT，断言它取得到锚点、
// tip 与模板两条都发出去、且共用同一个 event_id。
//
// 把 handleGroupAdd 里的 introAnchors.put 挪到 emitNoticeEvent 之后，本用例立刻变红。
func TestQQOfficialGroupAddAnchorRegisteredBeforeNoticeDispatch(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, joinIntroTipText)

	var (
		mu       sync.Mutex
		introErr error
		calls    int
	)
	// 同步：回调就在 emitNoticeEvent 那一行里跑，复现 lsp 的真实行为。
	a.OnNoticeEvent(func(ev *adapter.NoticeEvent) {
		if ev.NoticeType != "group_increase" {
			t.Errorf("进群事件的通知类型应当是 group_increase，实际 %q", ev.NoticeType)
			return
		}
		_, err := a.SendJoinIntro(ev.GroupID, joinIntroSendingMessage(joinIntroTemplateText))
		mu.Lock()
		introErr = err
		calls++
		mu.Unlock()
	})

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))

	mu.Lock()
	gotErr, gotCalls := introErr, calls
	mu.Unlock()
	if gotCalls != 1 {
		t.Fatalf("进群事件必须同步回调 lsp 一次，实际 %d 次", gotCalls)
	}
	if gotErr != nil {
		t.Fatalf("同步回调里 SendJoinIntro 必须取得到刚才登记的锚点，实际报错：%v（假平台收到：%s）",
			gotErr, p.dump())
	}

	sends := p.sent()
	if len(sends) != 2 {
		t.Fatalf("tip 与模板文案应当各发 1 条（共 2 条），实际 %d 条：%s", len(sends), p.dump())
	}
	for i, s := range sends {
		if s.eventID != eventIDFakeFrameID {
			t.Fatalf("第 %d 条必须带进群帧的 event_id=%q，实际 %q（原文 %s）",
				i+1, eventIDFakeFrameID, s.eventID, s.raw)
		}
	}
	if !strings.Contains(sends[0].content, joinIntroTipMark) {
		t.Fatalf("第一条必须是短提示（含 %q），实际 %q", joinIntroTipMark, sends[0].content)
	}
	if !strings.Contains(sends[1].content, joinIntroTemplateMark) {
		t.Fatalf("第二条必须是模板长文案（含 %q），实际 %q", joinIntroTemplateMark, sends[1].content)
	}
}

// 只配 tip、模板为空（lsp 传 nil）：**照样收到 1 条**。
//
// 这是「先一短后一长」改造最容易踩的坑：模板空就干脆不调 SendJoinIntro，
// 只配了 tip 的用户一条都收不到。
func TestQQOfficialSendJoinIntroTipOnlyWhenTemplateEmpty(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, joinIntroTipText)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))

	if _, err := a.SendJoinIntro(groupID, nil); err != nil {
		t.Fatalf("模板为空时只发 tip 也算成功，实际报错：%v（假平台收到：%s）", err, p.dump())
	}
	sends := p.sent()
	if len(sends) != 1 {
		t.Fatalf("只配 tip 时应当收到恰好 1 条（一条都不能丢），实际 %d 条：%s", len(sends), p.dump())
	}
	if !strings.Contains(sends[0].content, joinIntroTipMark) {
		t.Fatalf("这唯一一条应是短提示（含 %q），实际 %q", joinIntroTipMark, sends[0].content)
	}
	if sends[0].eventID != eventIDFakeFrameID {
		t.Fatalf("这一条必须带 event_id=%q，实际 %q（原文 %s）", eventIDFakeFrameID, sends[0].eventID, sends[0].raw)
	}
}

// 「0 元素的 *adapter.SendingMessage」与 nil 等价：只发 tip。
func TestQQOfficialSendJoinIntroEmptySendingMessageSendsTipOnly(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, joinIntroTipText)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))

	if _, err := a.SendJoinIntro(groupID, &adapter.SendingMessage{}); err != nil {
		t.Fatalf("0 元素的消息等于没有模板文案，不该报错：%v（假平台收到：%s）", err, p.dump())
	}
	sends := p.sent()
	if len(sends) != 1 || !strings.Contains(sends[0].content, joinIntroTipMark) {
		t.Fatalf("0 元素消息应当只发 tip 一条，实际 %d 条：%s", len(sends), p.dump())
	}
}

// tip + 模板都有：**先 tip，后长文案**，两条同一个 event_id。
//
// 顺序由**同一次调用**保证：调用一返回就立刻断言，不 sleep、不重试。
func TestQQOfficialSendJoinIntroTipThenTemplate(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, joinIntroTipText)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))
	if _, err := a.SendJoinIntro(groupID, joinIntroSendingMessage(joinIntroTemplateText)); err != nil {
		t.Fatalf("SendJoinIntro 应当成功：%v（假平台收到：%s）", err, p.dump())
	}

	// 同步调用已经返回：此刻假平台必须恰好收到 2 条，顺序已定。
	sends := p.sent()
	if len(sends) != 2 {
		t.Fatalf("tip + 模板文案应当只发 2 条，实际 %d 条：%s", len(sends), p.dump())
	}
	for i, s := range sends {
		if s.eventID != eventIDFakeFrameID {
			t.Fatalf("第 %d 条必须带上进群帧的 event_id=%q，实际 %q（原文 %s）", i+1, eventIDFakeFrameID, s.eventID, s.raw)
		}
		if s.msgID != "" || strings.Contains(s.raw, "msg_id") {
			t.Fatalf("第 %d 条不许掺 msg_id（event_id 与 msg_id 互斥），实际原文 %s", i+1, s.raw)
		}
		if s.msgSeq != 0 || strings.Contains(s.raw, "msg_seq") {
			t.Fatalf("第 %d 条不许掺 msg_seq，实际原文 %s", i+1, s.raw)
		}
	}
	if !strings.Contains(sends[0].content, joinIntroTipMark) {
		t.Fatalf("第一条必须是短提示（含 %q），实际 %q", joinIntroTipMark, sends[0].content)
	}
	if !strings.Contains(sends[1].content, joinIntroTemplateMark) {
		t.Fatalf("第二条必须是模板长文案（含 %q），实际 %q", joinIntroTemplateMark, sends[1].content)
	}
}

// tip 发送失败（假平台对含「菜单」的请求回 500）：模板那条**仍然发出**，整体返回成功。
func TestQQOfficialSendJoinIntroTemplateSentEvenIfTipFails(t *testing.T) {
	p := newEventIDFakePlatform(t)
	// 正文含「菜单」的发送一律 500 —— 只打中 tip，不打中模板文案。
	p.failContent = joinIntroTipMark
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, joinIntroTipText)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))
	if _, err := a.SendJoinIntro(groupID, joinIntroSendingMessage(joinIntroTemplateText)); err != nil {
		t.Fatalf("tip 失败不该让整体失败（上层会当成一条都没发出去）：%v（假平台收到：%s）", err, p.dump())
	}

	var tipTries, templateSent int
	for _, s := range p.sent() {
		if strings.Contains(s.content, joinIntroTipMark) {
			tipTries++
			if s.status == http.StatusOK {
				t.Fatalf("用例前置不成立：tip 那条假平台应当一直回 500，实际 status=%d（原文 %s）", s.status, s.raw)
			}
		}
		if strings.Contains(s.content, joinIntroTemplateMark) {
			templateSent++
			if s.status != http.StatusOK {
				t.Fatalf("模板那条不该受 tip 失败影响，实际 status=%d（原文 %s）", s.status, s.raw)
			}
			if s.eventID != eventIDFakeFrameID {
				t.Fatalf("模板那条必须带 event_id=%q，实际 %q（原文 %s）", eventIDFakeFrameID, s.eventID, s.raw)
			}
		}
	}
	if tipTries == 0 {
		t.Fatalf("前置不成立：一条 tip 请求都没发出去，测不到「失败不影响下一条」：%s", p.dump())
	}
	if templateSent != 1 {
		t.Fatalf("tip 失败之后模板那条应当照发且只发一条，实际 %d 条：%s", templateSent, p.dump())
	}
}

// tip 不配：只发模板那条。
func TestQQOfficialSendJoinIntroTemplateOnly(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, "")
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))
	if _, err := a.SendJoinIntro(groupID, joinIntroSendingMessage(joinIntroTemplateText)); err != nil {
		t.Fatalf("tip 为空时模板那条照样要发：%v（假平台收到：%s）", err, p.dump())
	}
	sends := p.sent()
	if len(sends) != 1 {
		t.Fatalf("tip 为空时只该发 1 条模板文案，实际 %d 条：%s", len(sends), p.dump())
	}
	if !strings.Contains(sends[0].content, joinIntroTemplateMark) {
		t.Fatalf("这唯一一条应是模板文案（含 %q），实际 %q", joinIntroTemplateMark, sends[0].content)
	}
}

// 两都空（tip 不配 + 模板空）：**一条请求都不发**，而且这不算错误。
func TestQQOfficialSendJoinIntroNothingToSend(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, "")
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))
	if _, err := a.SendJoinIntro(groupID, nil); err != nil {
		t.Fatalf("两条都没内容不是错误，不该报错：%v", err)
	}
	if n := len(p.sent()); n != 0 {
		t.Fatalf("tip 与模板都空时一条请求都不该发，实际 %d 条：%s", n, p.dump())
	}
}

// 废弃的键名不应再生效：配 qqofficial.join-welcome-tip / join-welcome-text 也一条不发。
func TestQQOfficialOldJoinWelcomeKeysNoLongerSend(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	config.GlobalConfig.Set(joinIntroOldTipKey, joinIntroTipText)
	config.GlobalConfig.Set(joinIntroOldTextKey, joinIntroOldText)
	t.Cleanup(func() {
		config.GlobalConfig.Set(joinIntroOldTipKey, "")
		config.GlobalConfig.Set(joinIntroOldTextKey, "")
	})
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))
	if _, err := a.SendJoinIntro(groupID, nil); err != nil {
		t.Fatalf("废弃键名没有 tip、模板为空，应当静默成功：%v", err)
	}
	if n := len(p.sent()); n != 0 {
		t.Fatalf("废弃的键名不该再生效，实际发了 %d 条：%s", n, p.dump())
	}
}

// ---------------------------------------------------------------- 进群锚点（一次性，只有 SendJoinIntro 碰）

// 另一个群的 openid / 进群帧 id：用来钉「不同群互不影响」。
const (
	joinIntroOtherGroupOpen = "B1C2D3E4F5061728394A5B6C7D8E9F01"
	joinIntroOtherFrameID   = "GROUP_ADD_ROBOT:9a8b7c6d-0000-1111-2222-333344445555"
)

// 锚点是一次性 take：第二次调用必须报错，且不再发任何请求。
func TestQQOfficialSendJoinIntroAnchorIsOneShot(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, "")
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))

	if _, err := a.SendJoinIntro(groupID, joinIntroSendingMessage(joinIntroTemplateText)); err != nil {
		t.Fatalf("第一次 SendJoinIntro 应当取到锚点：%v（假平台收到：%s）", err, p.dump())
	}
	before := len(p.sent())

	_, err := a.SendJoinIntro(groupID, joinIntroSendingMessage(joinIntroTemplateText))
	if err == nil {
		t.Fatal("锚点是一次性的：第二次 SendJoinIntro 必须报错，实际成功发出去了")
	}
	for _, want := range []string{"没有进群事件锚点", fmt.Sprintf("%d", groupID)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误信息必须含 %q（要能看出是哪个群、为什么发不出去），实际 %q", want, err.Error())
		}
	}
	time.Sleep(100 * time.Millisecond)
	if n := len(p.sent()); n != before {
		t.Fatalf("取不到锚点时一条请求都不许发（绝不回退主动发送），实际多发 %d 条：%s", n-before, p.dump())
	}
}

// 从未收到进群事件：报错且**一条请求都不发**（0 请求就是「没回退主动发送」的证据）。
func TestQQOfficialSendJoinIntroWithoutAnchorFails(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, joinIntroTipText)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	_, err := a.SendJoinIntro(groupID, joinIntroSendingMessage(joinIntroTemplateText))
	if err == nil {
		t.Fatal("没有进群事件锚点时必须报错，绝不退回主动发送")
	}
	for _, want := range []string{"没有进群事件锚点", fmt.Sprintf("%d", groupID)} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误信息必须含 %q，实际 %q", want, err.Error())
		}
	}
	if n := len(p.sent()); n != 0 {
		t.Fatalf("没有锚点就该什么都不发（tip 也不许发），实际发了 %d 条：%s", n, p.dump())
	}
}

// 锚点过期（TTL 到点）：报错不发，且写入新锚点时顺带把过期项清掉。
func TestQQOfficialSendJoinIntroAnchorExpires(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, "")
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))
	if n := a.introAnchors.count(); n != 1 {
		t.Fatalf("进群事件后锚点表里应当有 1 条，实际 %d 条", n)
	}

	// 真等 2 分钟不现实：直接把锚点表的时钟拨到 TTL 之后。
	a.introAnchors.now = func() time.Time { return time.Now().Add(introAnchorTTL + time.Second) }

	before := len(p.sent())
	_, err := a.SendJoinIntro(groupID, joinIntroSendingMessage(joinIntroTemplateText))
	if err == nil || !strings.Contains(err.Error(), "没有进群事件锚点") {
		t.Fatalf("锚点过期后必须报「没有进群事件锚点」，实际 err=%v", err)
	}
	if n := len(p.sent()); n != before {
		t.Fatalf("锚点过期后一条请求都不许发，实际多发 %d 条：%s", n-before, p.dump())
	}

	// 写入时顺带清过期项：给另一个群写一条，旧群那条应当被清掉。
	a.introAnchors.put(a.ids.ToInt64(testAppID, joinIntroOtherGroupOpen), joinIntroOtherFrameID)
	if n := a.introAnchors.count(); n != 1 {
		t.Fatalf("写入新锚点时应当顺带清掉过期项，实际锚点表里还有 %d 条", n)
	}
}

// 不同群的锚点互不影响：消费掉 A 的，B 仍然能取到自己的那条。
func TestQQOfficialSendJoinIntroAnchorsArePerGroup(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, "")
	firstID := a.ids.ToInt64(testAppID, testGroupOpen)
	secondID := a.ids.ToInt64(testAppID, joinIntroOtherGroupOpen)
	if firstID == secondID {
		t.Fatalf("用例前置不成立：两个群映射到了同一个内部 id %d", firstID)
	}

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))
	a.onDispatchEvent(EventGroupAddRobot, joinIntroOtherFrameID, json.RawMessage(eventIDGroupAddFrame(joinIntroOtherGroupOpen)))

	if _, err := a.SendJoinIntro(firstID, joinIntroSendingMessage(joinIntroTemplateText)); err != nil {
		t.Fatalf("群 %d 的锚点应当能取到：%v（假平台收到：%s）", firstID, err, p.dump())
	}
	if _, err := a.SendJoinIntro(secondID, joinIntroSendingMessage(joinIntroTemplateText)); err != nil {
		t.Fatalf("不同群互不影响：群 %d 的锚点不该被群 %d 用掉：%v（假平台收到：%s）",
			secondID, firstID, err, p.dump())
	}

	sends := p.sent()
	if len(sends) != 2 {
		t.Fatalf("两个群各一条，应当收到 2 条请求，实际 %d 条：%s", len(sends), p.dump())
	}
	if sends[0].eventID != eventIDFakeFrameID {
		t.Fatalf("群 %d 那条该带自己的 event_id=%q，实际 %q（原文 %s）", firstID, eventIDFakeFrameID, sends[0].eventID, sends[0].raw)
	}
	if sends[1].eventID != joinIntroOtherFrameID {
		t.Fatalf("群 %d 那条该带自己的 event_id=%q，实际 %q（原文 %s）", secondID, joinIntroOtherFrameID, sends[1].eventID, sends[1].raw)
	}

	// 各自的锚点都已被自己消费：再取都该报错。
	for _, id := range []int64{firstID, secondID} {
		if _, err := a.SendJoinIntro(id, joinIntroSendingMessage(joinIntroTemplateText)); err == nil {
			t.Fatalf("群 %d 的锚点应当已经被它自己消费掉，第二次仍成功就是没做到一次性", id)
		}
	}
}

// ---------------------------------------------------------------- 回归 / 无主动权限

// 回归：普通 SendGroupMessage 发出的请求里**没有** event_id，且**不消费**进群锚点。
//
// 这是上一版被推翻的原因：普通发送路径一旦读写锚点表，lsp 那侧就拿不到 event_id 了。
func TestQQOfficialSendGroupMessageCarriesNoEventID(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, "")
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	// 先进群，让锚点表里有一条待用的 event_id。
	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))

	if _, err := a.SendGroupMessage(groupID, "订阅推送"); err != nil {
		t.Fatalf("普通发送应当照旧发得出去：%v（假平台收到：%s）", err, p.dump())
	}
	sends := p.sent()
	if len(sends) != 1 {
		t.Fatalf("应发出 1 条消息，实际 %d 条：%s", len(sends), p.dump())
	}
	if sends[0].eventID != "" || strings.Contains(sends[0].raw, "event_id") {
		t.Fatalf("普通 SendGroupMessage 不许带 event_id（那是进群自我介绍专用的被动通道），实际原文 %s", sends[0].raw)
	}

	// 锚点必须还在：普通发送没读过它、更没消费掉它。
	if _, err := a.SendJoinIntro(groupID, joinIntroSendingMessage(joinIntroTemplateText)); err != nil {
		t.Fatalf("普通 SendGroupMessage 不许消费进群锚点，实际之后 SendJoinIntro 取不到了：%v", err)
	}
	after := p.sent()
	got := after[len(after)-1]
	if got.eventID != eventIDFakeFrameID {
		t.Fatalf("锚点被普通发送动过：SendJoinIntro 发出去的 event_id 应是 %q，实际 %q（原文 %s）",
			eventIDFakeFrameID, got.eventID, got.raw)
	}
}

// msg_id 那条被动老路行为不变：带 msg_id + msg_seq=1，且不掺 event_id。
func TestQQOfficialMsgIDPassivePathUnchanged(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	const msgID = "ROBOT1.0_MSG_ID_PASSIVE_REGRESSION"
	a.passive.set(TargetGroup, groupID, testGroupOpen, msgID)

	if _, err := a.SendGroupMessage(groupID, "命令答复"); err != nil {
		t.Fatalf("有 msg_id 锚点时应当能发出去：%v（假平台收到：%s）", err, p.dump())
	}
	sends := p.sent()
	if len(sends) != 1 {
		t.Fatalf("应发出 1 条消息，实际 %d 条：%s", len(sends), p.dump())
	}
	got := sends[0]
	if got.msgID != msgID {
		t.Fatalf("msg_id 被动路径应当照旧带上 msg_id=%q，实际 %q（原文 %s）", msgID, got.msgID, got.raw)
	}
	if got.msgSeq != DefaultMsgSeq {
		t.Fatalf("msg_id 被动路径的 msg_seq 应当从 %d 开始，实际 %d（原文 %s）", DefaultMsgSeq, got.msgSeq, got.raw)
	}
	if got.eventID != "" {
		t.Fatalf("msg_id 路径不该掺 event_id，实际 %q（原文 %s）", got.eventID, got.raw)
	}
}

// allow_proactive_msg=false 的群（假平台只认 event_id，其余一律 40034105）：
// tip 与模板文案都必须靠 event_id 发得出去 —— 这正是锚点存在的意义。
func TestQQOfficialSendJoinIntroDeliversInNoProactiveGroup(t *testing.T) {
	resetProactiveDenyWarns(t)
	p := newEventIDFakePlatform(t)
	p.denyUnanchored = true
	a := newSendTestAdapter(t, p.Server.URL)
	setJoinIntroTip(t, joinIntroTipText)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	// 前置：这个群确实不许主动发（复现实测的 40034105 墙）。
	if _, err := a.SendGroupMessage(groupID, "订阅推送"); err == nil {
		t.Fatal("前置不成立：模拟无主动权限的假平台居然接受了不带锚点的主动消息")
	}

	a.onDispatchEvent(EventGroupAddRobot, eventIDFakeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))
	if _, err := a.SendJoinIntro(groupID, joinIntroSendingMessage(joinIntroTemplateText)); err != nil {
		t.Fatalf("没有主动权限的群里，自我介绍必须靠锚点的 event_id 发出去：%v（假平台收到：%s）", err, p.dump())
	}

	var tipOK, templateOK int
	for _, one := range p.sent() {
		if one.status != http.StatusOK {
			continue
		}
		switch {
		case strings.Contains(one.content, joinIntroTipMark):
			tipOK++
			if one.eventID != eventIDFakeFrameID {
				t.Fatalf("tip 必须靠 event_id=%q 过（否则假平台会回 40034105），实际 %q（原文 %s）",
					eventIDFakeFrameID, one.eventID, one.raw)
			}
		case strings.Contains(one.content, joinIntroTemplateMark):
			templateOK++
			if one.eventID != eventIDFakeFrameID {
				t.Fatalf("模板文案必须靠 event_id=%q 过，实际 %q（原文 %s）",
					eventIDFakeFrameID, one.eventID, one.raw)
			}
		}
	}
	if tipOK != 1 || templateOK != 1 {
		t.Fatalf("tip 与模板文案应当各成功 1 条，实际 tip=%d 模板=%d：%s", tipOK, templateOK, p.dump())
	}
}

// ---------------------------------------------------------------- 网关层：帧顶层 id 要传下去

// ws.go 的读循环必须把帧**顶层**的 id 一路传到事件处理里（d 里没有它）。
func TestQQOfficialGatewayDispatchCarriesFrameEventID(t *testing.T) {
	type captured struct {
		eventType string
		id        string
		data      json.RawMessage
	}
	got := make(chan captured, 4)

	fg := newFakeGateway(t, func(index int, conn *websocket.Conn) {
		wsTestHello(conn)
		if _, _, err := wsTestRead(conn); err != nil { // Identify
			return
		}
		wsTestReady(conn, "sess-event-id")
		wsTestWrite(conn, map[string]any{
			"op": OpDispatch, "s": 3, "t": EventGroupAddRobot,
			"id": eventIDFakeFrameID,
			"d": map[string]any{
				"group_openid":     testGroupOpen,
				"op_member_openid": testOwnerOpen,
				"timestamp":        1794482058,
			},
		})
		// 挂着等测试收尾：对端一关连接这里就返回
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	var mu sync.Mutex
	gw := newGatewayForTest(t, fg, EventHandlerFunc(func(eventType string, id string, data json.RawMessage) {
		mu.Lock()
		defer mu.Unlock()
		select {
		case got <- captured{eventType: eventType, id: id, data: data}:
		default:
		}
	}))
	defer func() { _ = gw.Stop() }()

	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case c := <-got:
		if c.eventType != EventGroupAddRobot {
			t.Fatalf("事件类型应为 %s，实际 %s", EventGroupAddRobot, c.eventType)
		}
		if c.id != eventIDFakeFrameID {
			t.Fatalf("帧顶层的 id 必须传到事件处理里：期望 %q，实际 %q（d=%s）",
				eventIDFakeFrameID, c.id, string(c.data))
		}
		if !strings.Contains(string(c.data), testGroupOpen) {
			t.Errorf("d 应当原样透传，实际 %s", string(c.data))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("5 秒内没有收到 GROUP_ADD_ROBOT 事件（帧顶层的 id 没传下去？）")
	}
}

// ------------------------------------------------------------------ 机器人自己的名字

// botNameFakePlatform 只认「换 token」与 GET /users/@me 两条路径的假平台。
//
// /users/@me 的响应形状来自一手实测（2026-11-12）：
// {"id":"14103455547383994665","username":"DDBOT","avatar":"…","share_url":"…","welcome_msg":""}
// username 就是机器人名字。
type botNameFakePlatform struct {
	*httptest.Server

	username string
	status   int

	mu   sync.Mutex
	hits int
}

func newBotNameFakePlatform(t *testing.T, username string, status int) *botNameFakePlatform {
	t.Helper()
	p := &botNameFakePlatform{username: username, status: status}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ContentTypeJSON)
		switch {
		case strings.HasSuffix(r.URL.Path, PathGetAppAccessToken):
			_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":7200}`))
		case r.URL.Path == PathUsersMe:
			p.mu.Lock()
			p.hits++
			status, username := p.status, p.username
			p.mu.Unlock()
			w.WriteHeader(status)
			body, _ := json.Marshal(map[string]string{
				"id":          "14103455547383994665",
				"username":    username,
				"avatar":      "https://example.com/avatar.png",
				"share_url":   "",
				"welcome_msg": "",
			})
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(p.Server.Close)
	return p
}

func (p *botNameFakePlatform) hitCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hits
}

// /users/@me 返回 username：GetBotName() 直接拿到，成功时不该重试。
func TestQQOfficialBotNameLoadedFromUsersMe(t *testing.T) {
	p := newBotNameFakePlatform(t, "DDBOT", http.StatusOK)
	a := newSendTestAdapter(t, p.Server.URL)

	if got := a.GetBotName(); got != "" {
		t.Fatalf("还没拉过 /users/@me 时 GetBotName() 必须是空串，实际 %q", got)
	}

	a.loadBotName(context.Background())

	if got := a.GetBotName(); got != "DDBOT" {
		t.Fatalf("GetBotName() 应当是 /users/@me 的 username（DDBOT），实际 %q", got)
	}
	if n := p.hitCount(); n != 1 {
		t.Fatalf("成功拿到 username 后不该重试，/users/@me 期望 1 次，实际 %d 次", n)
	}
}

// 接口失败：有限次重试后留空，不 panic、不影响 adapter 的其它能力。
func TestQQOfficialBotNameFailureRetriesThenKeepsEmpty(t *testing.T) {
	p := newBotNameFakePlatform(t, "DDBOT", http.StatusInternalServerError)
	a := newSendTestAdapter(t, p.Server.URL)
	a.botNameRetryBackoff = 0

	a.loadBotName(context.Background()) // 不许 panic

	if got := a.GetBotName(); got != "" {
		t.Fatalf("拉取失败时 GetBotName() 必须是空串（模板自己兜底），实际 %q", got)
	}
	if n := p.hitCount(); n != botNameRetryAttempts {
		t.Fatalf("失败要按有限次数重试：期望 %d 次，实际 %d 次", botNameRetryAttempts, n)
	}
	// 「不影响其它流程」：失败之后 adapter 照常可用，别的接口不受牵连。
	if a.GetAdapterName() != AdapterName {
		t.Fatalf("拉名字失败不该影响 adapter 身份，实际 %q", a.GetAdapterName())
	}
	if a.IsConnected() {
		t.Fatal("拉名字失败不该把连接状态改成在线")
	}
}

// 返回空 username 也算失败：按失败重试，且绝不覆盖已经存下的名字。
func TestQQOfficialBotNameEmptyUsernameDoesNotOverwrite(t *testing.T) {
	p := newBotNameFakePlatform(t, "", http.StatusOK)
	a := newSendTestAdapter(t, p.Server.URL)
	a.botNameRetryBackoff = 0
	a.botName.Store("已有名字")

	a.loadBotName(context.Background())

	if got := a.GetBotName(); got != "已有名字" {
		t.Fatalf("空 username 不是有效名字，不许覆盖已有的值；期望 已有名字，实际 %q", got)
	}
	if n := p.hitCount(); n != botNameRetryAttempts {
		t.Fatalf("空 username 按失败处理，应当重试满 %d 次，实际 %d 次", botNameRetryAttempts, n)
	}
}

// 并发读 GetBotName()：写入与读取同时进行，-race 下不能报竞态（没跑 -race 时至少钉住逻辑）。
func TestQQOfficialBotNameConcurrentRead(t *testing.T) {
	p := newBotNameFakePlatform(t, "DDBOT", http.StatusOK)
	a := newSendTestAdapter(t, p.Server.URL)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = a.GetBotName()
				}
			}
		}()
	}

	a.loadBotName(context.Background())
	close(stop)
	wg.Wait()

	if got := a.GetBotName(); got != "DDBOT" {
		t.Fatalf("并发读之后名字仍应是 DDBOT，实际 %q", got)
	}
}
