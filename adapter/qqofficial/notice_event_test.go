package qqofficial

// 通知类事件（进群/退群/成员进出）的单测，钉住两件事：
//
//  1. GROUP_DEL_ROBOT 的 NoticeEvent.SubType 必须是 kick_me。
//     adapter/messenger.go:1507 只认 SubType == "kick_me" 或 OperatorID == SelfID 才走
//     「机器人自己被移出」分支；我们填的 OperatorID 是**操作者**（踢人的人），
//     所以留空 SubType 永远落进「普通成员退群」——lsp 不清理该群订阅，
//     而 adapter 已经把群从清单删掉，之后每条推送都报「未知群号」。
//
//  2. 通知类事件必须用帧顶层的事件 id 做幂等。平台断线重连会重投，
//     否则进群自我介绍会发两遍、退群事件重复派发。

import (
	"encoding/json"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

// noticeFrameID 是实测帧顶层 id 的形态（形如 EVENT_TYPE:uuid）。
const noticeFrameID = "GROUP_ADD_ROBOT:1fbfcd00-71bc-4971-b47f-66a2b7bc7794"

func groupDelFrame(openid string) string {
	return `{"group_openid":"` + openid + `","op_member_openid":"` + testOwnerOpen + `","timestamp":1794482058}`
}

func groupMemberOpFrame(openid, memberOpenID string) string {
	return `{"group_openid":"` + openid + `","member_openid":"` + memberOpenID +
		`","op_member_openid":"` + testOwnerOpen + `","timestamp":1794482058}`
}

// TestGroupDelNoticeSubTypeIsKickMe：GROUP_DEL_ROBOT 必须被上层认成「机器人自己被移出」。
func TestGroupDelNoticeSubTypeIsKickMe(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)

	var got []*adapter.NoticeEvent
	a.OnNoticeEvent(func(ev *adapter.NoticeEvent) { got = append(got, ev) })

	a.onDispatchEvent(EventGroupAddRobot, noticeFrameID, json.RawMessage(eventIDGroupAddFrame(testGroupOpen)))
	a.onDispatchEvent(EventGroupDelRobot, "GROUP_DEL_ROBOT:9a0f3c55-1111-2222-3333-444455556666",
		json.RawMessage(groupDelFrame(testGroupOpen)))

	if len(got) != 2 {
		t.Fatalf("应收到进群 + 退群两条通知，实际 %d 条", len(got))
	}
	del := got[1]
	if del.NoticeType != "group_decrease" {
		t.Fatalf("退群通知类型应为 group_decrease，实际 %q", del.NoticeType)
	}
	if del.SubType != "kick_me" {
		t.Fatalf("GROUP_DEL_ROBOT 的 SubType 必须是 kick_me（adapter/messenger.go:1507 只认 kick_me 或 "+
			"OperatorID==SelfID；我们把 OperatorID 填成了踢人的人，SubType 留空就会永远落进"+
			"「普通成员退群」，lsp 不清理订阅），实际 %q", del.SubType)
	}
}

// TestGroupAddNoticeDedupByFrameID：同一个进群帧重投只能派发一次。
func TestGroupAddNoticeDedupByFrameID(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)

	count := 0
	a.OnNoticeEvent(func(ev *adapter.NoticeEvent) {
		if ev.NoticeType == "group_increase" {
			count++
		}
	})

	frame := json.RawMessage(eventIDGroupAddFrame(testGroupOpen))
	a.onDispatchEvent(EventGroupAddRobot, noticeFrameID, frame)
	a.onDispatchEvent(EventGroupAddRobot, noticeFrameID, frame)

	if count != 1 {
		t.Fatalf("同一个进群帧（同一顶层事件 id）重投只应派发一次，实际 %d 次"+
			"（重连后会给同一个群发两遍自我介绍）", count)
	}
}

// TestGroupMemberOpDedupByFrameID：成员进出事件同样按帧 id 去重。
func TestGroupMemberOpDedupByFrameID(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)

	count := 0
	a.OnNoticeEvent(func(ev *adapter.NoticeEvent) {
		if ev.NoticeType == "group_decrease" {
			count++
		}
	})

	frame := json.RawMessage(groupMemberOpFrame(testGroupOpen, testOtherOpen))
	id := "GROUP_MEMBER_REMOVE:23c8b0d1-aaaa-bbbb-cccc-000011112222"
	a.onDispatchEvent(EventGroupMemberRemove, id, frame)
	a.onDispatchEvent(EventGroupMemberRemove, id, frame)

	if count != 1 {
		t.Fatalf("同一个成员移出帧重投只应派发一次，实际 %d 次", count)
	}
}

// TestGroupMemberOpWithoutFrameIDStillDelivered：id 为空时按现有策略放行（不静默丢弃）。
//
// 空 id 不是「重复」的证据：丢掉真正的进出群事件比重复派发一次更糟。
func TestGroupMemberOpWithoutFrameIDStillDelivered(t *testing.T) {
	p := newEventIDFakePlatform(t)
	a := newSendTestAdapter(t, p.Server.URL)

	count := 0
	a.OnNoticeEvent(func(ev *adapter.NoticeEvent) {
		if ev.NoticeType == "group_decrease" {
			count++
		}
	})

	frame := json.RawMessage(groupMemberOpFrame(testGroupOpen, testOtherOpen))
	a.onDispatchEvent(EventGroupMemberRemove, "", frame)
	a.onDispatchEvent(EventGroupMemberRemove, "", frame)

	if count != 2 {
		t.Fatalf("没有帧 id 时不该按「重复」丢弃（现有消息路径的策略是同一条：id 为空就跳过去重），"+
			"两次都该派发，实际 %d 次", count)
	}
}
