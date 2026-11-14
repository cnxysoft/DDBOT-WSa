package qqofficial

// ws.go 生命周期相关的单测（启动完成之后的连接状态）。
//
// 两块契约：
//  1. 启动完成之后发生的致命关闭（4914 下架 / 4915 封禁）不会重连，上层必须有一条
//     可查询的状态能把它和「普通断线、后台正在重连」区分开——只打 Warn 不算上报；
//  2. 4008（发送过快）是**限速**，不是会话作废：session 必须留着，下次连上走 Resume，
//     断线期间的事件才会补发。
//
// 符号统一带 wsLife 前缀，复用 ws_test.go 的 fakeGateway / wsTest* 工具，避免撞名。

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestGatewayFatalCloseAfterReadySurfacesFatalError 复现 P0：READY 之后网关以 4914
// （机器人下架）关闭，run() 收工不再重连，而 Start 早就返回了——initErr 通道没人读。
// 于是上层只看到一条 Warn，Connected() 虽然变 false，但和「正在重连」长得一模一样。
//
// 期望：FatalError() 能查到这条致命关闭（含关闭码与「不会再自动恢复」的理由），
// 并且日志里明说不再重连、要人工处理。
func TestGatewayFatalCloseAfterReadySurfacesFatalError(t *testing.T) {
	buf := captureLogs(t)

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
		wsTestReady(conn, "sess-fatal-err")
		<-allowClose
		_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		_ = conn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(WSCloseBotOffline, "bot offline"))
		time.Sleep(300 * time.Millisecond)
	})

	gw := newGatewayForTest(t, fg, nil)
	defer func() { _ = gw.Stop() }()

	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := gw.FatalError(); err != nil {
		t.Fatalf("启动成功之后、还没出事时 FatalError() 应当是 nil，实际 %v", err)
	}
	close(allowClose)

	wsLifeWaitFor(t, 3*time.Second, "4914 之后 FatalError() 仍然是 nil（致命关闭没被记下来）",
		func() bool { return gw.FatalError() != nil })

	ferr := gw.FatalError()
	var fe *fatalWSError
	if !errors.As(ferr, &fe) {
		t.Fatalf("FatalError() 应当是 *fatalWSError（带关闭码），实际 %T：%v", ferr, ferr)
	}
	if fe.Code != WSCloseBotOffline {
		t.Fatalf("FatalError() 的关闭码 = %d，期望 %d", fe.Code, WSCloseBotOffline)
	}
	if !strings.Contains(ferr.Error(), "4914") {
		t.Fatalf("FatalError() 的文案里应当带上关闭码 4914：%v", ferr)
	}
	if !gw.Connected() {
		// 正常：致命关闭后连接必须已经清掉
	} else {
		t.Fatal("4914 之后 Connected() 仍然是 true（脏 true）")
	}

	logs := buf.String()
	if !strings.Contains(logs, "不再重连") {
		t.Fatalf("4914 的日志里应当明说不再自动重连（只剩 Warn 等于没上报）：%s", logs)
	}
}

// TestGatewayRateLimitedCloseKeepsSessionAndResumes 复现 P2：4008（发送过快）是限速，
// 不是会话作废。原先把 4008 塞进 clearSession 那批码里，结果一次限速就把 session_id/seq
// 清掉，下次连上只能 Identify，断线期间的事件（含发给机器人的命令）全部丢失。
//
// 期望：4008 之后重连仍带原 session_id / seq 发 Resume（op=6）。
func TestGatewayRateLimitedCloseKeepsSessionAndResumes(t *testing.T) {
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
			wsTestReady(conn, "sess-ratelimited")
			_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(WSCloseRateLimited, "rate limited"))
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

	wsLifeWaitFor(t, 10*time.Second, "4008 之后迟迟没有第二条连接（可能不再重连）",
		func() bool { return fg.firstCount() >= 2 })

	raw, second := fg.firstAt(1)
	if second.Op != OpResume {
		t.Fatalf("4008（限速）之后应当保留 session 走 Resume（op=6），实际第二条连接的第一帧 op=%d：%s",
			second.Op, string(raw))
	}
	var d resumeData
	if err := json.Unmarshal(second.Data, &d); err != nil {
		t.Fatalf("解析 Resume 的 d 失败：%v（%s）", err, string(raw))
	}
	if d.SessionID != "sess-ratelimited" {
		t.Fatalf("Resume 带的 session_id = %q，期望 sess-ratelimited（说明 4008 把 session 清掉了）", d.SessionID)
	}
	if d.Seq != 1 {
		t.Fatalf("Resume 带的 seq = %d，期望 1（READY 那一帧的 s）", d.Seq)
	}
	if gw.FatalError() != nil {
		t.Fatalf("4008 是限速、不是致命关闭，FatalError() 应为 nil，实际 %v", gw.FatalError())
	}
}

// wsLifeWaitFor 轮询等待条件成立；超时就报 what。
func wsLifeWaitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
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
