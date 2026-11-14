package qqofficial

// message.go event_id 兜底的单测。
//
// 契约只剩一条、而且必须写死在测试里：event_id 被平台拒之后，摘掉它重发**就是一次
// 主动发送**——群里有主动权限才发得出去，没权限的群会白打一次请求拿到 40034105。
// 这不是「还能被动」，日志与应用层文档必须这么说（adapter 层旧注释写的「绝不回退到主动」
// 与事实相反，事实以本文件为准）。
//
// 符号统一带 evFallback 前缀，复用 message_test.go 的 newSendTestClient / sendRetryTestPolicy
// 与 persist_test.go 的 captureLogs。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// evFallbackBody 是 event_id 被平台判失效时的响应体（HTTP 200，业务码在 body 里）。
// 这里复用实测过的 40034005（回复的消息已过期）：event_id 的失效码本身没实测，
// 代码就是拿 msg_id 那批码做保守兜底的，所以测试也照那条路走。
const evFallbackBody = `{"message":"回复消息 msg_id 已过期","code":40034005,"err_code":40034005}`

// evFallbackPlatform 是只处理发消息的假平台：带 event_id 的一律回「已过期」，
// 不带锚点的按 denyUnanchored 决定是「无主动权限」还是正常成功。
type evFallbackPlatform struct {
	*httptest.Server

	denyUnanchored bool

	mu    sync.Mutex
	sends []sendMessageRequest
}

func newEvFallbackPlatform(t *testing.T, denyUnanchored bool) *evFallbackPlatform {
	t.Helper()
	p := &evFallbackPlatform{denyUnanchored: denyUnanchored}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", ContentTypeJSON)
		if !strings.HasSuffix(r.URL.Path, "/messages") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		var req sendMessageRequest
		_ = json.Unmarshal(raw, &req)
		p.mu.Lock()
		p.sends = append(p.sends, req)
		p.mu.Unlock()

		switch {
		case req.EventID != "":
			_, _ = w.Write([]byte(evFallbackBody))
		case p.denyUnanchored:
			_, _ = w.Write([]byte(eventIDProactiveDeniedBody))
		default:
			_, _ = w.Write([]byte(`{"id":"MSG_EV_FALLBACK_1","timestamp":"2026-11-12T19:30:00+08:00","ext_info":{"ref_idx":"REFIDX_EV_FALLBACK_1"}}`))
		}
	}))
	t.Cleanup(p.Server.Close)
	return p
}

func (p *evFallbackPlatform) sent() []sendMessageRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]sendMessageRequest(nil), p.sends...)
}

// TestSendEventIDRejectedBecomesProactiveAndSaysSoInLog：event_id 被拒 → 摘掉重发（主动），
// 群里**有**主动权限时能发出去，日志必须明说「这次将成为主动消息」+ 没权限群的代价 40034105。
func TestSendEventIDRejectedBecomesProactiveAndSaysSoInLog(t *testing.T) {
	buf := captureLogs(t)
	p := newEvFallbackPlatform(t, false)
	c := newSendTestClient(t, p.Server, sendRetryTestPolicy(2))

	res, err := c.Send(context.Background(), sendTestGroup(), SendOptions{
		Content: "hello",
		EventID: "GROUP_ADD_ROBOT:ev-fallback",
	})
	if err != nil {
		t.Fatalf("event_id 被拒后按普通发送重发应当成功：%v", err)
	}
	sends := p.sent()
	if len(sends) != 2 {
		t.Fatalf("期望 2 次请求（带 event_id → 摘掉重发），实际 %d 次", len(sends))
	}
	if sends[0].EventID == "" {
		t.Fatalf("第一次请求必须带 event_id，实际 %+v", sends[0])
	}
	if sends[1].EventID != "" {
		t.Fatalf("第二次请求必须已经摘掉 event_id，实际 %+v", sends[1])
	}
	if sends[1].MsgID != "" || sends[1].MsgSeq != 0 {
		t.Fatalf("摘掉 event_id 之后不该冒出 msg_id/msg_seq（那是另一条账本），实际 %+v", sends[1])
	}
	if res.EventPassive || res.Passive {
		t.Fatalf("这条最终是主动消息，EventPassive/Passive 都应为 false，实际 %+v", *res)
	}

	logs := buf.String()
	if !strings.Contains(logs, "主动消息") {
		t.Fatalf("摘掉 event_id 之后就是主动发送，日志必须说清「这次将成为主动消息」：%s", logs)
	}
	if !strings.Contains(logs, "40034105") {
		t.Fatalf("日志必须写明没主动权限的群会白打一次请求（40034105）：%s", logs)
	}
}

// TestSendEventIDRejectedInNoProactiveGroupCostsOneWastedRequest：
// denyUnanchored 的群（实测 allow_proactive_msg=false）里，event_id 被拒之后那次重发
// 注定拿 40034105 —— 这就是「白打一次请求」的具体代价。错误必须原样交回（不许吞），
// 日志必须事先说清它变成了主动消息。
func TestSendEventIDRejectedInNoProactiveGroupCostsOneWastedRequest(t *testing.T) {
	buf := captureLogs(t)
	p := newEvFallbackPlatform(t, true)
	c := newSendTestClient(t, p.Server, sendRetryTestPolicy(2))

	_, err := c.Send(context.Background(), sendTestGroup(), SendOptions{
		Content: "hello",
		EventID: "GROUP_ADD_ROBOT:ev-fallback-2",
	})
	if err == nil {
		t.Fatal("没有主动权限的群里，摘掉 event_id 重发应当拿到 40034105，实际成功了")
	}
	apiErr, ok := AsAPIError(err)
	if !ok || apiErr.Code != ErrCodeProactiveNoPermission {
		t.Fatalf("错误应当是「主动消息无权限」（40034105），实际 %v", err)
	}
	sends := p.sent()
	if len(sends) != 2 {
		t.Fatalf("期望 2 次请求（event_id 被拒 + 白打一次的主动重发），实际 %d 次", len(sends))
	}
	if sends[1].EventID != "" || sends[1].MsgID != "" {
		t.Fatalf("第二次请求应当无锚点（纯主动），实际 %+v", sends[1])
	}
	logs := buf.String()
	if !strings.Contains(logs, "主动消息") {
		t.Fatalf("日志必须说清那次重发是主动消息（否则没人知道请求白打了）：%s", logs)
	}
}
