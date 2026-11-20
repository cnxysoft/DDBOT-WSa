package qqofficial

// 40034105「主动消息失败, 无权限」与启动期 allow_proactive_msg 告警的单测。
//
// 实测（2026-11-12，一手接口返回）：
//
//	bot_state: {"member_openid":"3DB0…","allow_proactive_msg":false,"recv_msg_setting":"","member_role":"member"}
//	POST 主动纯文本   -> {"message":"主动消息失败, 无权限","code":40034105,"err_code":40034105}
//	POST 主动 markdown -> 同上
//
// 也就是说这枚码是**权限**（群主没开「允许机器人主动发送消息」），不是配额闸门：
// 重试一万次都不会成功，只会把发送队列拖慢、日志刷屏。所以这里钉住三件事：
//  1. 一次请求就失败返回，且错误里带可操作指引；
//  2. 告警按群限流（同群 1 次/小时，不同群各一条）；
//  3. 40034100 超频控那枚仍然照旧退避重试（别把配额那条一起改坏）。

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// proactiveDeniedBody 是实测的无权限响应体（HTTP 200，业务码落在 body 里）。
const proactiveDeniedBody = `{"message":"主动消息失败, 无权限","code":40034105,"err_code":40034105}`

// proactiveHintMarker 是「去群里开开关」那段指引里最独一无二的一句：
// 日志与 error 文本都靠它定位，别用它去匹配别的文案。
const proactiveHintMarker = "允许机器人主动发送消息"

// resetProactiveDenyWarns 清掉进程级日志限流表，免得用例之间互相压掉告警。
func resetProactiveDenyWarns(t *testing.T) {
	t.Helper()
	proactiveDenyWarns.reset()
	t.Cleanup(proactiveDenyWarns.reset)
}

// 无主动消息权限 -> 只发 1 次请求就失败，不许退避重试；错误里要能直接指导操作。
func TestQQOfficialProactiveDeniedFailsWithoutRetry(t *testing.T) {
	resetProactiveDenyWarns(t)
	fs := newFakeSendServer(t, []fakeSendReply{
		{status: http.StatusOK, body: proactiveDeniedBody},
		{status: http.StatusOK, body: `{"id":"msg-ok"}`}, // 万一真重试了，它会「成功」
	})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	_, err := c.Send(context.Background(), sendTestGroup(), SendOptions{Content: "B站开播了"})
	if err == nil {
		t.Fatal("没有主动消息权限时应当失败，不许假装成功")
	}
	if got := fs.hitCount(); got != 1 {
		t.Fatalf("40034105 是权限问题，重试无解：假平台收到 %d 次请求，期望 1（只发一次）", got)
	}
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("交回的错误必须还能取出 *APIError（上层按码判定要用），实际：%v", err)
	}
	if apiErr.Code != ErrCodeProactiveNoPermission {
		t.Fatalf("错误里应保留平台原始业务码 %d，实际 code=%d", ErrCodeProactiveNoPermission, apiErr.Code)
	}
	for _, want := range []string{proactiveHintMarker, "机器人权限设置", "订阅推送"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息要能直接指导操作，应包含 %q，实际：%v", want, err)
		}
	}
}

// 同一群连着两次 40034105 -> 告警只打一次；不同群各打一次（按群限流，1 次/小时）。
func TestQQOfficialProactiveDeniedWarnsOncePerGroup(t *testing.T) {
	resetProactiveDenyWarns(t)
	fs := newFakeSendServer(t, []fakeSendReply{
		{status: http.StatusOK, body: proactiveDeniedBody},
	})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))
	buf := captureLogs(t)

	first := Target{Kind: TargetGroup, OpenID: "group-deny-A"}
	second := Target{Kind: TargetGroup, OpenID: "group-deny-B"}

	for i := 0; i < 2; i++ {
		if _, err := c.Send(context.Background(), first, SendOptions{Content: "推送"}); err == nil {
			t.Fatal("没有主动消息权限时应当失败")
		}
	}
	if n := strings.Count(buf.String(), proactiveHintMarker); n != 1 {
		t.Fatalf("同一群连续两次 40034105 只该打一条告警（同群 1 次/小时），实际 %d 条：%s", n, buf.String())
	}

	if _, err := c.Send(context.Background(), second, SendOptions{Content: "推送"}); err == nil {
		t.Fatal("没有主动消息权限时应当失败")
	}
	logs := buf.String()
	if n := strings.Count(logs, proactiveHintMarker); n != 2 {
		t.Fatalf("另一个群要各自打一条告警，累计应是 2 条，实际 %d 条：%s", n, logs)
	}
	if !strings.Contains(logs, "group-deny-B") {
		t.Errorf("告警要点名是哪个群，实际日志：%s", logs)
	}
	if !strings.Contains(logs, "level=warning") {
		t.Errorf("无权限告警应是 Warn 级，实际日志：%s", logs)
	}
}

// 40034100 超频控仍然是配额闸门：照旧退避重试，别被这次改动一起改坏。
func TestQQOfficialProactiveOverQuotaStillBacksOff(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{
		{status: http.StatusTooManyRequests, body: `{"message":"主动消息发送超过频控限制","code":40034100,"err_code":40034100}`},
	})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(5))

	if _, err := c.Send(context.Background(), sendTestGroup(), SendOptions{Content: "hi"}); err == nil {
		t.Fatal("一直撞频控时应当报错")
	}
	// 首发 + QuotaAttempts(2) 次退避 = 3
	if got := fs.hitCount(); got != 3 {
		t.Fatalf("40034100 应继续走配额退避：假平台收到 %d 次请求，期望 3（首发 + 2 次退避）", got)
	}
}

// 限流表本身：同 key 一小时内只放行一次，跨小时放行，不同 key 互不影响。
func TestQQOfficialProactiveDenyWarnThrottle(t *testing.T) {
	th := newWarnThrottle(time.Hour)
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.Local)

	if !th.allow("k", now) {
		t.Fatal("第一次必须放行")
	}
	if th.allow("k", now.Add(30*time.Minute)) {
		t.Fatal("一小时内同一个群不该再打第二条")
	}
	if th.allow("k", now.Add(59*time.Minute)) {
		t.Fatal("59 分钟仍然在限流窗口内")
	}
	if !th.allow("k", now.Add(61*time.Minute)) {
		t.Fatal("超过一小时后应重新放行（群主可能刚开了开关又关掉）")
	}
	if !th.allow("other", now) {
		t.Fatal("不同群各自独立限流")
	}
}

// allow_proactive_msg=false：启动解析时打一条 Warn（每个群只喊一次），带上群名与群号。
func TestQQOfficialSelfResolveWarnsWhenProactiveDenied(t *testing.T) {
	body := `{"member_openid":"` + selfOpenInGroup + `","member_role":"admin","allow_proactive_msg":false,"recv_msg_setting":""}`
	p := newFakeBotStatePlatform(t, http.StatusOK, body)
	a := botStateAdapter(t, p)
	a.groups.recordInfo(testGroupOpen, "订阅测试群", 0, 0)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	buf := captureLogs(t)
	for i := 0; i < 2; i++ {
		if ok := a.resolveSelfFromGroup(testGroupOpen); !ok {
			t.Fatalf("第 %d 次：bot_state 返回正常时 resolveSelfFromGroup 必须为 true", i+1)
		}
	}
	logs := buf.String()

	if n := strings.Count(logs, proactiveHintMarker); n != 1 {
		t.Fatalf("无主动消息权限的告警每个群只该喊一次，实际 %d 次：%s", n, logs)
	}
	if !strings.Contains(logs, "level=warning") {
		t.Errorf("这条应是 Warn 级，实际日志：%s", logs)
	}
	for _, want := range []string{"订阅测试群", fmt.Sprintf("%d", groupID), "订阅推送"} {
		if !strings.Contains(logs, want) {
			t.Errorf("告警要把群名/群号与后果说清，应包含 %q，实际日志：%s", want, logs)
		}
	}
}

// 群名还不知道时：用内部群号兜底，别打一条没头没尾的告警。
func TestQQOfficialSelfResolveWarnFallsBackToGroupID(t *testing.T) {
	body := `{"member_openid":"` + selfOpenInGroup + `","member_role":"member","allow_proactive_msg":false,"recv_msg_setting":""}`
	p := newFakeBotStatePlatform(t, http.StatusOK, body)
	a := botStateAdapter(t, p)
	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	buf := captureLogs(t)
	a.resolveSelfFromGroup(testGroupOpen)
	if !strings.Contains(buf.String(), fmt.Sprintf("内部群号 %d", groupID)) {
		t.Errorf("群名拿不到时要用内部群号兜底，实际日志：%s", buf.String())
	}
}

// 有权限时不喊；recv_msg_setting 空串（新群实测就是空串）本身不算「没权限」，不许误报。
func TestQQOfficialSelfResolveQuietWhenProactiveAllowed(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "recv_msg_setting=all",
			body: `{"member_openid":"` + selfOpenInGroup + `","member_role":"admin","allow_proactive_msg":true,"recv_msg_setting":"all"}`,
		},
		{
			name: "recv_msg_setting 空串",
			body: `{"member_openid":"` + selfOpenInGroup + `","member_role":"admin","allow_proactive_msg":true,"recv_msg_setting":""}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newFakeBotStatePlatform(t, http.StatusOK, tc.body)
			a := botStateAdapter(t, p)
			buf := captureLogs(t)
			if ok := a.resolveSelfFromGroup(testGroupOpen); !ok {
				t.Fatal("bot_state 返回正常时 resolveSelfFromGroup 必须为 true")
			}
			if strings.Contains(buf.String(), proactiveHintMarker) {
				t.Errorf("有主动消息权限时不该喊无权限告警，实际日志：%s", buf.String())
			}
		})
	}
}
