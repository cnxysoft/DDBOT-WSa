package qqofficial

// adapter.go 硬化项（P2）：这一轮钉住六件事，都是「公开后不返工」里最容易漏的边角。
//
//  1. nil dedup：手搓 &QQOfficialAdapter{} 时三条去重调用点都该安全（不 panic）。
//  2. handleInfoEvent 解析失败不许照常发 notify（其它 handler 一律 return）。
//  3. fetchGroupInfoAsync 的单槽闸门不许静默丢掉后来者（两个群几乎同时入群）。
//  4. GetGroupInfo 取不到群信息要报错，与 GetStrangerInfo / GetGroupMemberInfo 一致。
//  5. 通知事件幂等要覆盖 FRIEND_ADD/DEL、INTERACTION_CREATE、GROUP_JOIN_REQUEST。
//  6. base64 解码失败的原因不许丢，要带进最终错误文案。
//
// 符号统一带 hd 前缀，避免与包内既有测试撞名。

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

const (
	hdGroupA = "CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
	hdGroupB = "DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD"
)

// hdC2CFrame 是一条最小可用的 C2C_MESSAGE_CREATE 事件体。
const hdC2CFrame = `{"author":{"id":"` + testOtherOpen + `","username":"某人"},"content":"hi",` +
	`"id":"ROBOT1.0_HARDENING_C2C","timestamp":"2026-11-11T16:40:00+08:00"}`

// waitUntil 轮询一个断言，超时就带上 hint 失败。
func waitUntil(t *testing.T, d time.Duration, hint string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待超时（%s）：%s", d, hint)
}

// TestNilDedupDoesNotPanic：dedup 为空时三条去重调用点都不许 panic。
//
// 缺陷形态：noticeEventDuplicated 判了 a.dedup == nil，而 handleGroupMessage /
// handleC2CMessage 直接 a.dedup.Seen(...) —— 手搓 &QQOfficialAdapter{} 或构造失败的
// adapter 走这两条路就是 nil 解引用 panic。
func TestNilDedupDoesNotPanic(t *testing.T) {
	// 连 adapter 本身都没有的情形（纯 nil 接收者）也要安全。
	if (&QQOfficialAdapter{}).noticeEventDuplicated(EventGroupMemberRemove, "GROUP_MEMBER_REMOVE:x") {
		t.Fatal("dedup 表不存在时不可能判定为重复")
	}

	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)
	a.dedup = nil

	// 通知路径。
	if a.noticeEventDuplicated(EventGroupMemberRemove, "GROUP_MEMBER_REMOVE:x") {
		t.Fatal("dedup 表为 nil 时通知事件不该被判成重复")
	}

	var mu sync.Mutex
	groups, c2c := 0, 0
	a.OnGroupMessage(func(*adapter.GroupMessageEvent) { mu.Lock(); groups++; mu.Unlock() })
	a.OnPrivateMessage(func(*adapter.PrivateMessageEvent) { mu.Lock(); c2c++; mu.Unlock() })

	a.onDispatchEvent(EventGroupMessageCreate, "", json.RawMessage(groupAtPayload))
	a.onDispatchEvent(EventC2CMessageCreate, "", json.RawMessage(hdC2CFrame))

	mu.Lock()
	defer mu.Unlock()
	if groups != 1 || c2c != 1 {
		t.Fatalf("dedup 为 nil 时消息必须照常派发（只跳过幂等），实际 群=%d 单聊=%d", groups, c2c)
	}
}

// TestInfoEventParseFailureNotNotified：MESSAGE_AUDIT 解析失败要 return，不能照常发 notify。
//
// 缺陷形态：unmarshalEvent 返回 false 后代码直接落到下面那条 emit，等于「解析失败也照报」，
// 而 handleReady / handleGroupAdd / handleInteraction / handleJoinRequest 一律 return。
func TestInfoEventParseFailureNotNotified(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)

	var mu sync.Mutex
	var got []string
	a.OnNoticeEvent(func(ev *adapter.NoticeEvent) {
		mu.Lock()
		got = append(got, ev.NoticeType+"/"+ev.SubType)
		mu.Unlock()
	})

	// message_audit 不是对象 → json.Unmarshal 报错 → 必须丢弃。
	a.onDispatchEvent(EventMessageAuditPass, "MESSAGE_AUDIT_PASS:bad-1",
		json.RawMessage(`{"message_audit":"这不是对象"}`))
	mu.Lock()
	n := len(got)
	mu.Unlock()
	if n != 0 {
		t.Fatalf("解析失败的消息审核事件不该再发 notify，实际发了 %d 条：%v", n, got)
	}

	// 正常帧与非 message_audit 的 info 事件必须照旧送达。
	a.onDispatchEvent(EventMessageAuditPass, "MESSAGE_AUDIT_PASS:ok-1",
		json.RawMessage(`{"message_audit":{"audit_id":"A1","message_id":"M1"}}`))
	a.onDispatchEvent(EventC2CMsgReceive, "C2C_MSG_RECEIVE:ok-2", json.RawMessage(`{}`))

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 {
		t.Fatalf("正常的审核帧与 info 事件都必须派发，实际 %d 条：%v", len(got), got)
	}
}

// TestFetchGroupInfoAsyncQueuesSecondGroup：两个群几乎同时入群，第二个群也要拿到群信息。
//
// 缺陷形态：infoGate 是单槽 CAS，失败的那一次直接 return —— 无日志、无补跑入口，
// 后一个群的群名/人数永远为空。
func TestFetchGroupInfoAsyncQueuesSecondGroup(t *testing.T) {
	p := newGroupInfoPlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)

	// 真实链路：进群事件里 groups.ensure 会触发一次后台取群信息，两帧几乎同时到。
	a.groups.ensure(hdGroupA)
	a.groups.ensure(hdGroupB)

	waitUntil(t, 5*time.Second, "两个群都要拿到群名（第二个被闸门挡住时必须补跑）", func() bool {
		ra, okA := a.groups.getByOpenID(hdGroupA)
		rb, okB := a.groups.getByOpenID(hdGroupB)
		return okA && okB && ra.Name == "好群" && rb.Name == "好群"
	})

	if n := p.callsFor(hdGroupB); n == 0 {
		t.Fatalf("后一个群的 /info 请求根本没发出去（被单槽闸门静默丢掉）；假平台收到：%s", p.dump())
	}
}

// TestGetGroupInfoReportsFailure：取不到群信息必须报错，且错误里说清群号与原因。
//
// 缺陷形态：fetchGroupInfo 只 Warn 就 return，GetGroupInfo 拿空群名当成功返回 nil error，
// 与 GetStrangerInfo / GetGroupMemberInfo「查不到就报错」不一致。
func TestGetGroupInfoReportsFailure(t *testing.T) {
	p := newGroupInfoPlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)

	// 只测同步路径：摘掉 onNew，别让背景取信息搅乱断言（它是另一条路，见上一个用例）。
	a.groups.onNew = nil

	// 权限错误（实测 11253）：这个群永远拿不到群信息。
	badID := a.groups.ensure(cooldownBadGroup)
	if _, err := a.GetGroupInfo(badID); err == nil {
		t.Fatal("群信息取不到时必须返回 error（上层现在拿到空群名当成功）")
	} else {
		msg := err.Error()
		if !strings.Contains(msg, cooldownBadGroup) {
			t.Fatalf("错误文案要能看出是哪个群（带上 group_openid）：%q", msg)
		}
		if !strings.Contains(msg, "11253") && !strings.Contains(msg, "无权限") {
			t.Fatalf("错误文案要带上失败原因（权限错误 11253）：%q", msg)
		}
	}

	// 拿得到的时候照旧返回群名、不报错。
	goodID := a.groups.ensure(cooldownGoodGroup)
	info, err := a.GetGroupInfo(goodID)
	if err != nil {
		t.Fatalf("群信息取得到时不该报错：%v", err)
	}
	if info == nil || info.Name != "好群" {
		t.Fatalf("群名应为 %q，实际 %+v", "好群", info)
	}
}

// TestNoticeFamilyDedupCoversFriendInteractionJoinRequest：剩下三类通知/请求事件按帧 id 幂等。
//
// 缺陷形态：onDispatchEvent 只给 GROUP_ADD/DEL_ROBOT、GROUP_MEMBER_ADD/REMOVE 过了
// noticeEventDuplicated，FRIEND_ADD/FRIEND_DEL、INTERACTION_CREATE、GROUP_JOIN_REQUEST
// 没有幂等 —— 断线重连重投时上层会重复清理好友、重复派发交互与入群申请。
func TestNoticeFamilyDedupCoversFriendInteractionJoinRequest(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)

	var mu sync.Mutex
	notices, requests := 0, 0
	a.OnNoticeEvent(func(*adapter.NoticeEvent) { mu.Lock(); notices++; mu.Unlock() })
	a.OnRequestEvent(func(*adapter.RequestEvent) { mu.Lock(); requests++; mu.Unlock() })

	friendFrame := json.RawMessage(`{"openid":"` + testOtherOpen + `","timestamp":1794482058}`)
	friendID := "FRIEND_ADD:8f7d1a2b-1111-2222-3333-444455556666"
	a.onDispatchEvent(EventFriendAdd, friendID, friendFrame)
	a.onDispatchEvent(EventFriendAdd, friendID, friendFrame)

	interactionFrame := json.RawMessage(`{"id":"INT_1","type":11,"group_openid":"` + testGroupOpen + `",` +
		`"user_openid":"` + testOtherOpen + `","timestamp":1794482058,` +
		`"data":{"type":11,"resolved":{"button_id":"BTN_1"}}}`)
	interactionID := "INTERACTION_CREATE:2c3d4e5f-1111-2222-3333-444455556666"
	a.onDispatchEvent(EventInteractionCreate, interactionID, interactionFrame)
	a.onDispatchEvent(EventInteractionCreate, interactionID, interactionFrame)

	joinFrame := json.RawMessage(`{"group_openid":"` + testGroupOpen + `","join_request_id":"JR_1",` +
		`"member_openid":"` + testOwnerOpen + `","username":"申请人","apply_at":1794482058}`)
	joinID := "GROUP_JOIN_REQUEST:9a8b7c6d-1111-2222-3333-444455556666"
	a.onDispatchEvent(EventGroupJoinRequest, joinID, joinFrame)
	a.onDispatchEvent(EventGroupJoinRequest, joinID, joinFrame)

	mu.Lock()
	defer mu.Unlock()
	if notices != 2 {
		t.Fatalf("同一个好友帧 + 同一个交互帧各只该派发一次（共 2 次），实际 %d 次", notices)
	}
	if requests != 1 {
		t.Fatalf("同一个人群申请帧只该派发一次，实际 %d 次", requests)
	}
}

// TestInvalidBase64MediaErrorKeepsReason：base64 解码失败的原因要进最终错误文案。
//
// 缺陷形态：newMediaFromSource 把解码错误丢在 else 分支里（只把 path 置空），
// 上层最后只看到「媒体内容为空」，原因（不是合法 base64）彻底消失。
func TestInvalidBase64MediaErrorKeepsReason(t *testing.T) {
	p := newFakeQQPlatform(t)
	a := newSendTestAdapter(t, p.server.URL)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	msg := []adapter.MessageSegment{
		{Type: "image", Data: map[string]interface{}{"file": "base64://这不是合法的base64!!", "name": "bad.png"}},
	}
	_, err := a.SendGroupMessage(groupID, msg)
	if err == nil {
		t.Fatal("非法 base64 媒体必须报错，不能当成功")
	}
	text := err.Error()
	if !strings.Contains(text, "base64") {
		t.Fatalf("错误文案里必须留下 base64 解码失败这个原因，实际：%q", text)
	}
	if !strings.Contains(text, "bad.png") {
		t.Fatalf("错误文案里要能看出是哪一段媒体（文件名），实际：%q", text)
	}
}
