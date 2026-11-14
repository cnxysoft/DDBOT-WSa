package qqofficial

// ws.go 网关连接的单测：假网关（httptest + gorilla upgrade）按脚本回帧。
//
// 覆盖两条：
//  1. 致命关闭码（4914 机器人下架 / 4915 封禁）之后 Connected() 必须清零，
//     不能留下脏的 true（脏 true 会让上层永久认为在线，连离线事件都不发）；
//  2. op=7（服务端要求重连）之后 session 必须保留，下一次要 Resume 而不是 Identify，
//     否则断线期间的事件（含发给机器人的命令）不重放。
//
// 符号统一带 wsTest / fakeGateway 前缀，避免与同包其它 _test.go 撞名。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeGateway 是一个按脚本运行的假网关：/gateway 给地址，/ws 每来一条连接跑一次脚本。
type fakeGateway struct {
	*httptest.Server

	mu       sync.Mutex
	conns    int
	firstRaw [][]byte
	firstFrm []wsPayload

	script func(index int, conn *websocket.Conn)
}

func newFakeGateway(t *testing.T, script func(index int, conn *websocket.Conn)) *fakeGateway {
	t.Helper()
	fg := &fakeGateway{script: script}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	mux := http.NewServeMux()
	mux.HandleFunc(PathGateway, func(w http.ResponseWriter, r *http.Request) {
		// /gateway 只认 scheme，不校验域名（真机实测域名和文档就不一样）
		wsURL := "ws" + strings.TrimPrefix(fg.Server.URL, "http") + "/ws"
		w.Header().Set("Content-Type", ContentTypeJSON)
		_, _ = w.Write([]byte("{\"url\":\"" + wsURL + "\"}"))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		fg.mu.Lock()
		fg.conns++
		index := fg.conns
		fg.mu.Unlock()

		fg.script(index, conn)
	})
	fg.Server = httptest.NewServer(mux)
	t.Cleanup(fg.Server.Close)
	return fg
}

func (f *fakeGateway) connCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns
}

// recordFirst 记下一条连接上收到的第一帧（握手帧：Identify 或 Resume）。
func (f *fakeGateway) recordFirst(raw []byte, p *wsPayload) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.firstRaw = append(f.firstRaw, raw)
	f.firstFrm = append(f.firstFrm, *p)
}

func (f *fakeGateway) firstCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.firstFrm)
}

func (f *fakeGateway) firstAt(i int) ([]byte, wsPayload) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.firstRaw[i], f.firstFrm[i]
}

// ---------- 假网关侧的小工具（都在服务端 goroutine 里跑，不碰 testing.T） ----------

func wsTestWrite(conn *websocket.Conn, v any) {
	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_ = conn.WriteJSON(v)
}

func wsTestRead(conn *websocket.Conn) ([]byte, *wsPayload, error) {
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return nil, nil, err
	}
	var p wsPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return raw, nil, err
	}
	return raw, &p, nil
}

func wsTestHello(conn *websocket.Conn) {
	wsTestWrite(conn, map[string]any{"op": OpHello, "d": map[string]any{"heartbeat_interval": 5000}})
}

func wsTestReady(conn *websocket.Conn, sessionID string) {
	wsTestWrite(conn, map[string]any{
		"op": OpDispatch, "s": 1, "t": EventReady,
		"d": map[string]any{
			"session_id": sessionID,
			"shard":      []int{0, 1},
			"user":       map[string]any{"id": "10001", "username": "test-bot", "bot": true},
		},
	})
}

// newGatewayForTest 拼一个指向假网关的 Gateway：token 直接给缓存好的，不走 token 接口。
func newGatewayForTest(t *testing.T, fg *fakeGateway, h EventHandler) *Gateway {
	t.Helper()
	tm := &TokenManager{
		appID:      "test-app",
		appSecret:  "test-secret",
		httpClient: fg.Client(),
		baseURL:    fg.Server.URL,
		token:      "test-access-token",
		expiresAt:  time.Now().Add(time.Hour),
		ahead:      time.Minute,
	}
	gw := NewGateway(tm, h)
	gw.cfg.APIBaseURL = fg.Server.URL
	gw.cfg.StartTimeout = 5 * time.Second
	gw.httpClient = fg.Client()
	return gw
}

// wsTestWaitFor 轮询等待条件成立；超时就报 what。
func wsTestWaitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("轮询等待超时（%s）：%s", timeout, what)
}

// TestGatewayFatalCloseClearsConnected：会话 READY 之后网关以 4914（机器人下架）关闭，
// Connected() 必须变 false。
//
// 容易踩的写法：致命分支直接 failInit+return，跳过 run() 里唯一的 connected.Store(false)，
// 于是上层拿到的「在线」永久为真。
func TestGatewayFatalCloseClearsConnected(t *testing.T) {
	// 关闭帧必须等测试确认完 Connected() 再发：早一步发就会和读线程抢跑，
	// READY 与「连接已断」两个状态谁先落地是不确定的（实测 100 次挂 5 次）。
	allowClose := make(chan struct{})
	defer func() {
		select {
		case <-allowClose:
		default:
			close(allowClose)
		}
	}()
	fg := newFakeGateway(t, func(index int, conn *websocket.Conn) {
		wsTestHello(conn)
		if _, _, err := wsTestRead(conn); err != nil { // Identify
			return
		}
		wsTestReady(conn, "sess-fatal")
		<-allowClose
		_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(WSCloseBotOffline, "bot offline"))
		// 让对端把关闭帧读完再收摊
		time.Sleep(300 * time.Millisecond)
	})

	gw := newGatewayForTest(t, fg, nil)
	defer func() { _ = gw.Stop() }()

	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !gw.Connected() {
		t.Fatal("READY 之后 Connected() 应当为 true")
	}
	close(allowClose) // 断言过了，现在才允许服务器发关闭帧

	wsTestWaitFor(t, 3*time.Second, "致命关闭码 4914 之后 Connected() 仍然为 true（脏 true）",
		func() bool { return !gw.Connected() })

	if got := fg.connCount(); got != 1 {
		t.Fatalf("致命关闭码之后不该再重连：连接数 = %d，期望 1", got)
	}
}

// TestGatewayOpReconnectKeepsSessionAndResumes：op=7 只是「服务端要求重连」，
// 不是「会话作废」——保留 session 才能走 Resume，断线期间的事件才会补发。
//
// 平台侧依据（三家独立实现一致）：koishi adapter-qq ws.ts:80-84（op=7 只清心跳定时器、
// 不清 _sessionId）与 ws.ts:112-120（它用 4000 关闭，不在「>4000 且非 4008/4009 就清会话」
// 那一条里，所以 session 留着，下次 HELLO 走 RESUME）；QQ 官方 SDK
// gateway-connection.ts:251-254（RECONNECT 不清 sessionId，只有 INVALID_SESSION 且
// canResume=false 才清）；MaiBot-QQ-Adapter plugin.py:569-575（op=7 只关连接，
// 仅 op=9 且 d 不为 true 才重置会话）。
func TestGatewayOpReconnectKeepsSessionAndResumes(t *testing.T) {
	// fg 在闭包里被引用，所以先声明再赋值（:= 的右边拿不到左边那个名字）
	var fg *fakeGateway
	fg = newFakeGateway(t, func(index int, conn *websocket.Conn) {
		switch index {
		case 1:
			wsTestHello(conn)
			raw, p, err := wsTestRead(conn)
			if err != nil {
				return
			}
			if p != nil {
				fg.recordFirst(raw, p)
			}
			wsTestReady(conn, "sess-reconnect")
			wsTestWrite(conn, map[string]any{"op": OpReconnect})
			time.Sleep(200 * time.Millisecond)
		case 2:
			wsTestHello(conn)
			raw, p, err := wsTestRead(conn)
			if err != nil {
				return
			}
			if p != nil {
				fg.recordFirst(raw, p)
			}
			wsTestWrite(conn, map[string]any{"op": OpDispatch, "s": 2, "t": EventResumed, "d": map[string]any{}})
			// 挂着等测试收尾：对端一关连接这里就返回
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}
	})

	gw := newGatewayForTest(t, fg, nil)
	defer func() { _ = gw.Stop() }()

	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	wsTestWaitFor(t, 10*time.Second, "op=7 之后迟迟没有第二条连接（可能没有重连）",
		func() bool { return fg.firstCount() >= 2 })

	if _, first := fg.firstAt(0); first.Op != OpIdentify {
		t.Fatalf("第一条连接的握手应当是 Identify（op=2），实际 op=%d", first.Op)
	}

	raw, second := fg.firstAt(1)
	if second.Op != OpResume {
		t.Fatalf("op=7 之后应当保留 session 走 Resume（op=6），实际第二条连接的第一帧 op=%d：%s",
			second.Op, string(raw))
	}
	var d resumeData
	if err := json.Unmarshal(second.Data, &d); err != nil {
		t.Fatalf("解析 Resume 的 d 失败：%v（%s）", err, string(raw))
	}
	if d.SessionID != "sess-reconnect" {
		t.Fatalf("Resume 带的 session_id = %q，期望 sess-reconnect（说明 op=7 把 session 清掉了）", d.SessionID)
	}
	if d.Seq != 1 {
		t.Fatalf("Resume 带的 seq = %d，期望 1（READY 那一帧的 s）", d.Seq)
	}
}
