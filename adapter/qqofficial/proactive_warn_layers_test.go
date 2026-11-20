package qqofficial

// 「这个群没开主动消息权限」有两条告警，分工不同、都要留，而且叠起来不许刷屏。
//
//   - adapter.go 的 warnProactiveDeniedOnce（**启动期**，读 bot_state 的 allow_proactive_msg）：
//     每群**永久一次**，讲前提「这个群发不出主动消息、去哪儿开」；
//   - message.go 的 warnProactiveNoPermission（**发送期**，真撞到 40034105）：按 appid+openid
//     **每小时最多一条**，讲这一次失败的现场（原始 code/err_code/message）与同样的操作指引。
//
// 本用例把两层放在一起跑，钉住上限：首轮合计 2 条（各 1），同轮再撞都不再喊；
// 一小时后只有发送期那条重新放行 1 条，启动期那条仍然安静。
// 谁哪天「顺手删掉重复的那一头」或者把限流改坏，这里会红。

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProactiveDenyWarnLayersDoNotFlood(t *testing.T) {
	resetProactiveDenyWarns(t)
	buf := captureLogs(t)

	// 启动期那层：一个没有主动消息权限的群，喊两遍也只出 1 条。
	body := `{"member_openid":"` + selfOpenInGroup + `","member_role":"member","allow_proactive_msg":false,"recv_msg_setting":""}`
	a := botStateAdapter(t, newFakeBotStatePlatform(t, http.StatusOK, body))
	for i := 0; i < 2; i++ {
		a.warnProactiveDeniedOnce(testGroupOpen, false)
	}

	// 发送期那层：同一个目标连撞两遍 40034105，同样只出 1 条。
	fs := newFakeSendServer(t, []fakeSendReply{{status: http.StatusOK, body: proactiveDeniedBody}})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))
	target := Target{Kind: TargetGroup, OpenID: testGroupOpen}
	apiErr := &APIError{
		HTTPStatus: http.StatusOK,
		Code:       ErrCodeProactiveNoPermission,
		ErrCode:    ErrCodeProactiveNoPermission,
		Message:    "主动消息失败, 无权限",
	}
	now := time.Now()
	for i := 0; i < 2; i++ {
		c.warnProactiveNoPermission(target, apiErr, now)
	}

	if n := strings.Count(buf.String(), proactiveHintMarker); n != 2 {
		t.Fatalf("两层各一条、合计 2 条（既不该是 4 条重复刷屏，也不该把哪一头吞掉）：实际 %d 条：%s",
			n, buf.String())
	}

	// 一小时后：发送期那条按小时窗口重新放行，启动期那条仍然不喊。
	c.warnProactiveNoPermission(target, apiErr, now.Add(61*time.Minute))
	a.warnProactiveDeniedOnce(testGroupOpen, false)
	if n := strings.Count(buf.String(), proactiveHintMarker); n != 3 {
		t.Fatalf("一小时窗口过后只该由发送期那条再放行 1 条（累计 3 条）：实际 %d 条：%s",
			n, buf.String())
	}
}
