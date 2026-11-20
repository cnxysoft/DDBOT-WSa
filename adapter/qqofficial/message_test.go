package qqofficial

// message.go 发送路径的单测：重点是「网关层失败（5xx / 裸 429）要真的退避重发」，
// 以及「四个降级 case 的优先级不许被重试分支截胡」。
//
// 全部走 httptest 起的假平台（回环地址），不联网、不碰真机；退避时间压到 1ms，不靠 sleep 等结果。
// 符号统一带 send 前缀，避免与 media_test.go / dedup_test.go / idmap_test.go / event_test.go 撞名。

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeSendReply 是假平台第 N 次请求的回应（请求次数超出脚本长度时一直用最后一个）。
type fakeSendReply struct {
	status int
	body   string
}

// fakeSendServer 是一个按脚本回应的假平台，顺便把每次请求记下来。
type fakeSendServer struct {
	*httptest.Server

	mu     sync.Mutex
	hits   int
	paths  []string
	bodies []sendMessageRequest
}

func newFakeSendServer(t *testing.T, replies []fakeSendReply) *fakeSendServer {
	// @全体 冷却是进程级账本：-count=N 同进程连跑时不清会串到下一轮。
	resetAtAllCooldowns()
	t.Helper()
	if len(replies) == 0 {
		t.Fatal("fakeSendServer 至少要有一个回应")
	}
	fs := &fakeSendServer{}
	fs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req sendMessageRequest
		_ = json.Unmarshal(raw, &req)

		fs.mu.Lock()
		idx := fs.hits
		fs.hits++
		fs.paths = append(fs.paths, r.URL.Path)
		fs.bodies = append(fs.bodies, req)
		fs.mu.Unlock()

		reply := replies[len(replies)-1]
		if idx < len(replies) {
			reply = replies[idx]
		}
		w.Header().Set("Content-Type", ContentTypeJSON)
		w.WriteHeader(reply.status)
		_, _ = w.Write([]byte(reply.body))
	}))
	t.Cleanup(fs.Server.Close)
	return fs
}

func (f *fakeSendServer) hitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits
}

func (f *fakeSendServer) bodyAt(i int) sendMessageRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[i]
}

// sendRetryTestPolicy 是测试用的发送预算：退避压到 1ms，让重发次数可数。
//
// TransportAttempts 沿用既有语义（一次 Send 的总尝试次数上限，含首发）：
// 给 4 就代表最多 4 次请求，给 2 就代表最多 2 次。
func sendRetryTestPolicy(transportAttempts int) sendPolicy {
	return sendPolicy{
		MaxAttempts:       10,
		TransportAttempts: transportAttempts,
		TransportBackoff:  time.Millisecond,
		QuotaAttempts:     2,
		QuotaBackoffBase:  time.Millisecond,
		QuotaBackoffMax:   2 * time.Millisecond,
	}
}

// newSendTestClient 拼一个打到假平台的 Client：token 直接给缓存好的，不走 token 接口。
func newSendTestClient(t *testing.T, ts *httptest.Server, policy sendPolicy) *Client {
	t.Helper()
	tm := &TokenManager{
		appID:      "test-app",
		appSecret:  "test-secret",
		httpClient: &http.Client{Timeout: 2 * time.Second},
		baseURL:    ts.URL,
		token:      "test-access-token",
		expiresAt:  time.Now().Add(time.Hour),
		ahead:      time.Minute,
	}
	c := NewClient(tm)
	c.baseURL = ts.URL
	c.httpClient = &http.Client{Timeout: 2 * time.Second}
	c.policy = policy
	return c
}

// sendTestGroup 是测试用的群目标。
func sendTestGroup() Target { return Target{Kind: TargetGroup, OpenID: "group-openid-1"} }

// TestSendRetriesGatewayInternalError：网关 5xx（body 里没有业务码）必须按退避重试，
// 不能一条消息就这么丢在发送路径上。
//
// 500 -> 502 -> 200：期望真的按退避重发，第 3 次成功，而不是第 1 次就返回错误。
func TestSendRetriesGatewayInternalError(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{
		{status: http.StatusInternalServerError, body: "{\"message\":\"internal server error\"}"},
		{status: http.StatusBadGateway, body: "{\"message\":\"bad gateway\"}"},
		{status: http.StatusOK, body: "{\"id\":\"msg-ok\",\"timestamp\":\"2026-11-11T16:00:00+08:00\"}"},
	})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	res, err := c.Send(context.Background(), sendTestGroup(), SendOptions{Content: "开播啦"})
	if err != nil {
		t.Fatalf("网关 5xx 之后原样重发应当能成功，实际还是失败：%v（假平台收到 %d 次请求）", err, fs.hitCount())
	}
	if got := fs.hitCount(); got != 3 {
		t.Fatalf("网关 5xx 的重发次数不对：假平台收到 %d 次请求，期望 3（两次失败 + 一次成功）", got)
	}
	if res.MsgID != "msg-ok" {
		t.Fatalf("成功响应的 msg id 没取回来：%q", res.MsgID)
	}
	if res.Attempts != 3 {
		t.Fatalf("SendResult.Attempts = %d，期望 3", res.Attempts)
	}
	if got := fs.bodyAt(2).Content; got != "开播啦" {
		t.Fatalf("重发的内容变了：%q", got)
	}
}

// TestSendRetriesBareGateway429：不带业务码的裸 429（网关自己限流）同样要重试，
// 不能因为找不到业务码就走进死路。带业务码 100001 的那种归配额分支管，见下面那条测试。
func TestSendRetriesBareGateway429(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{
		{status: http.StatusTooManyRequests, body: "{\"message\":\"too many requests\"}"},
		{status: http.StatusOK, body: "{\"id\":\"msg-ok\"}"},
	})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	if _, err := c.Send(context.Background(), sendTestGroup(), SendOptions{Content: "hi"}); err != nil {
		t.Fatalf("裸 429 退避后应当重发成功，实际失败：%v", err)
	}
	if got := fs.hitCount(); got != 2 {
		t.Fatalf("裸 429 的重发次数不对：假平台收到 %d 次请求，期望 2", got)
	}
}

// TestSendGatewayRetryIsBounded 重发必须有上限：网关一直 500 时按 TransportAttempts 收手，
// 不许无限重发（发送路径背后是群消息，无限重发就是无限重复发言）。
func TestSendGatewayRetryIsBounded(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{
		{status: http.StatusInternalServerError, body: "{\"message\":\"boom\"}"},
	})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(2))

	_, err := c.Send(context.Background(), sendTestGroup(), SendOptions{Content: "hi"})
	if err == nil {
		t.Fatal("网关一直 500 时应当报错，不该假装成功")
	}
	if got := fs.hitCount(); got != 2 {
		t.Fatalf("重发上限没生效：假平台收到 %d 次请求，期望 2（= TransportAttempts）", got)
	}
	apiErr, ok := AsAPIError(err)
	if !ok {
		t.Fatalf("重试用尽后应当把最后一枚 *APIError 原样交出来，实际：%v", err)
	}
	if apiErr.HTTPStatus != http.StatusInternalServerError {
		t.Fatalf("交出的错误状态码 = %d，期望 500", apiErr.HTTPStatus)
	}
}

// TestSendDoesNotRetryNonRetryableError 反向断言：真不可重试的 400 一次都不许多发
// （哪怕假平台下一次就回 200，也不能再试）。
func TestSendDoesNotRetryNonRetryableError(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{
		{status: http.StatusBadRequest, body: "{\"message\":\"bad request\"}"},
		{status: http.StatusOK, body: "{\"id\":\"msg-ok\"}"},
	})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	if _, err := c.Send(context.Background(), sendTestGroup(), SendOptions{Content: "hi"}); err == nil {
		t.Fatal("HTTP 400 应当直接失败")
	}
	if got := fs.hitCount(); got != 1 {
		t.Fatalf("HTTP 400 不可重试，却发了 %d 次请求", got)
	}
}

// TestSendQuotaGateKeepsItsOwnBudget 守护降级 case 的优先级：带业务码 100001 的 429
// 必须仍然走原来的配额退避分支，不能被新的网关重发截胡（否则配额预算会被偷偷放大一轮）。
//
// 网关重发预算故意给大（5），配额预算只有 2：期望总请求数 = 1 + 2 = 3。
func TestSendQuotaGateKeepsItsOwnBudget(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{
		{status: http.StatusTooManyRequests, body: "{\"code\":100001,\"message\":\"频率过高\"}"},
	})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(5))

	if _, err := c.Send(context.Background(), sendTestGroup(), SendOptions{Content: "hi"}); err == nil {
		t.Fatal("一直撞配额闸门时应当报错")
	}
	if got := fs.hitCount(); got != 3 {
		t.Fatalf("配额闸门应当只花自己的预算：假平台收到 %d 次请求，期望 3（首发 + 2 次配额退避）", got)
	}
}

// TestSendDegradeWinsOverGatewayRetry 守护优先级：markdown 被拒的降级必须排在重发之前，
// 哪怕这次响应同时带着 5xx（平台把业务码和坏网关搅在一起时不能乱序）。
//
// 断言第二条请求的 msg_type 已经变成纯文本（0）——这只可能由降级 case 造成，
// 原样重发会保留 msg_type=2。
func TestSendDegradeWinsOverGatewayRetry(t *testing.T) {
	fs := newFakeSendServer(t, []fakeSendReply{
		{status: http.StatusInternalServerError, body: "{\"code\":40034011,\"message\":\"无效的 markdown 内容\"}"},
		{status: http.StatusOK, body: "{\"id\":\"msg-ok\"}"},
	})
	c := newSendTestClient(t, fs.Server, sendRetryTestPolicy(4))

	res, err := c.Send(context.Background(), sendTestGroup(), SendOptions{Markdown: "# 标题\n正文"})
	if err != nil {
		t.Fatalf("markdown 被拒应当降级成纯文本重发：%v", err)
	}
	if !res.Degraded {
		t.Fatal("应当标记为发生了 markdown -> 纯文本降级")
	}
	if got := fs.hitCount(); got != 2 {
		t.Fatalf("降级链只该多一次重发：假平台收到 %d 次请求，期望 2", got)
	}
	if got := fs.bodyAt(1).MsgType; got != MsgTypeText {
		t.Fatalf("降级后第二条请求的 msg_type = %d，期望 %d（0=纯文本）", got, MsgTypeText)
	}
}
