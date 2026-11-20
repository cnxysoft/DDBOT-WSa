package qqofficial

// ws.go 处理「平台协议演进」的两条契约：
//
//  1. op=9（INVALID_SESSION）里的 d **解析不出来**时，不许把它当成「平台说不可 resume」。
//     读不懂是「我们读不懂」，不是「平台说不能恢复」。原来 `_ = json.Unmarshal(...)` 把解析错误
//     一咽，resumable 保持 false，于是 clearSession() 把一段仍可恢复的会话直接作废——断线期间的
//     事件（含发给机器人的命令）全部丢掉。判据与 op=7 那段同源：能 resume 就别丢，
//     真不可恢复时平台会用 4006/4007 明说。
//
//  2. 不认识的网关 op 必须 Warn，并带上 op 号与载荷长度。平台新增 op 是常态（op=7/9 就是这么来的），
//     Debug 级等于在故障现场一片沉默。载荷正文不许进日志：里面可能有 openid、消息内容，
//     长度足够定位「有没有载荷、大概多大」。
//
// 复用 ws_test.go 的 fakeGateway / wsTest* 工具；本文件符号统一带 wsOp 前缀避免撞名。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// wsOpResumeSession 断言第二条连接的第一帧是 Resume，并返回它带的 session_id / seq。
func wsOpResumeSession(t *testing.T, fg *fakeGateway) (string, int64) {
	t.Helper()
	if fg.firstCount() < 1 {
		t.Fatal("还没有记录到重连后的握手帧（第二条连接没建立？）")
	}
	raw, second := fg.firstAt(0)
	if second.Op != OpResume {
		t.Fatalf("应当保留 session 走 Resume（op=6），实际重连第一帧 op=%d：%s", second.Op, string(raw))
	}
	var d resumeData
	if err := json.Unmarshal(second.Data, &d); err != nil {
		t.Fatalf("解析 Resume 的 d 失败：%v（%s）", err, string(raw))
	}
	return d.SessionID, d.Seq
}

// wsOpRecordAndHold 先按协议发 Hello，再记下这条连接的握手帧（Identify / Resume），
// 然后挂着等测试收尾（对端关闭即返回）。
//
// 必须先发 Hello：客户端的握手帧要等 op=10 Hello 到了才发，少了这一步就会卡在等 Hello。
func wsOpRecordAndHold(fg *fakeGateway, conn *websocket.Conn) {
	wsTestHello(conn)
	raw, p, err := wsTestRead(conn)
	if err != nil {
		return
	}
	if p != nil {
		fg.recordFirst(raw, p)
	}
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

// op=9 的 d 是对象（不是 bool）——「解析失败」必须按**可能可 resume** 处理：
// 保留 session，下一次连上照常带 session_id / seq 发 Resume。
//
// 未修前：解析错误被 `_ = ` 吞掉 → resumable=false → clearSession()，重连第一帧变成 Identify，
// 断线期间的事件全丢。
func TestGatewayInvalidSessionUnparsableDKeepsSession(t *testing.T) {
	buf := captureLogs(t)

	var fg *fakeGateway
	fg = newFakeGateway(t, func(index int, conn *websocket.Conn) {
		switch index {
		case 1:
			wsTestHello(conn)
			if _, _, err := wsTestRead(conn); err != nil { // Identify
				return
			}
			wsTestReady(conn, "sess-op9-unknown-d")
			// d 不是 bool，而是对象：平台换形状时最可能出现的样子。
			wsTestWrite(conn, map[string]any{
				"op": OpInvalidSession,
				"d":  map[string]any{"can_resume": "maybe"},
			})
			time.Sleep(200 * time.Millisecond)
		default:
			wsOpRecordAndHold(fg, conn)
		}
	})

	gw := newGatewayForTest(t, fg, nil)
	// Stop 可重复调用：这里挂一个兜底，断言提前 Fatalf 时也不会留下重连协程去污染后续用例的日志。
	defer func() { _ = gw.Stop() }()
	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	wsTestWaitFor(t, 10*time.Second, "op=9（d 解析失败）之后迟迟没有重连",
		func() bool { return fg.firstCount() >= 1 })

	sessionID, seq := wsOpResumeSession(t, fg)
	if sessionID != "sess-op9-unknown-d" {
		t.Fatalf("Resume 带的 session_id = %q，期望 sess-op9-unknown-d（说明 d 解析失败被当成不可 resume，session 被清了）", sessionID)
	}
	if seq != 1 {
		t.Fatalf("Resume 带的 seq = %d，期望 1（READY 那一帧的 s）", seq)
	}

	// Stop 会等后台读/心跳协程退出，再读日志就不会和它们抢 buffer。
	if err := gw.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	logs := buf.String()
	if !strings.Contains(logs, "op=9") {
		t.Errorf("日志里应当点到是 op=9 出的问题：%s", logs)
	}
	if !strings.Contains(logs, "保留 session") {
		t.Errorf("日志要明说「按保守方向保留 session」（否则运维看不出这是有意为之）：%s", logs)
	}
	if !strings.Contains(logs, "level=warning") {
		t.Errorf("这条必须是 Warn 级（「读不懂 d」是异常情况，不是日常调试信息）：%s", logs)
	}
}

// 反向守卫：d 明确是 false 时，平台就是在说「不可 resume」，必须清 session、下次 Identify。
func TestGatewayInvalidSessionNotResumableClearsSession(t *testing.T) {
	buf := captureLogs(t)

	var fg *fakeGateway
	fg = newFakeGateway(t, func(index int, conn *websocket.Conn) {
		switch index {
		case 1:
			wsTestHello(conn)
			if _, _, err := wsTestRead(conn); err != nil {
				return
			}
			wsTestReady(conn, "sess-op9-no-resume")
			wsTestWrite(conn, map[string]any{"op": OpInvalidSession, "d": false})
			time.Sleep(200 * time.Millisecond)
		default:
			wsOpRecordAndHold(fg, conn)
		}
	})

	gw := newGatewayForTest(t, fg, nil)
	// Stop 可重复调用：这里挂一个兜底，断言提前 Fatalf 时也不会留下重连协程去污染后续用例的日志。
	defer func() { _ = gw.Stop() }()
	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	wsTestWaitFor(t, 10*time.Second, "op=9（d=false）之后迟迟没有重连",
		func() bool { return fg.firstCount() >= 1 })

	raw, second := fg.firstAt(0)
	if second.Op != OpIdentify {
		t.Fatalf("d=false 表示不可 resume，重连第一帧应当是 Identify（op=2），实际 op=%d：%s",
			second.Op, string(raw))
	}
	if gw.SessionID() != "" {
		t.Errorf("d=false 之后 session 应当被清掉，实际还留着 %q", gw.SessionID())
	}

	if err := gw.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if logs := buf.String(); strings.Contains(logs, "保留 session") {
		t.Errorf("d=false 是明确表态，不该打「保留 session」那条保守告警：%s", logs)
	}
}

// 未识别的 op 必须 Warn：带 op 号 + 载荷长度，不带载荷正文。
func TestGatewayUnknownOpWarnsWithOpAndPayloadLength(t *testing.T) {
	buf := captureLogs(t)

	const secret = "payload-secret-must-not-be-logged"
	payload, err := json.Marshal(map[string]any{"content": secret})
	if err != nil {
		t.Fatalf("构造载荷失败：%v", err)
	}

	fg := newFakeGateway(t, func(index int, conn *websocket.Conn) {
		if index != 1 {
			return
		}
		wsTestHello(conn)
		if _, _, err := wsTestRead(conn); err != nil {
			return
		}
		wsTestReady(conn, "sess-unknown-op")
		wsTestWrite(conn, map[string]any{"op": 99, "s": 2, "d": json.RawMessage(payload)})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	gw := newGatewayForTest(t, fg, nil)
	// Stop 可重复调用：这里挂一个兜底，断言提前 Fatalf 时也不会留下重连协程去污染后续用例的日志。
	defer func() { _ = gw.Stop() }()
	if err := gw.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// 给读循环一点时间把 op=99 处理掉；Stop 之后再读日志，避免和后台协程抢 buffer。
	time.Sleep(300 * time.Millisecond)
	if err := gw.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "op=99") {
		t.Errorf("未识别的 op 必须留下 op 号，实际日志：%s", logs)
	}
	wantLen := fmt.Sprintf("%d 字节", len(payload))
	if !strings.Contains(logs, wantLen) {
		t.Errorf("未识别的 op 必须带上载荷长度（%s），实际日志：%s", wantLen, logs)
	}
	if !strings.Contains(logs, "level=warning") {
		t.Errorf("未识别的 op 应当是 Warn 级：Debug 级等于平台新增 op 时没人会知道：%s", logs)
	}
	if strings.Contains(logs, secret) {
		t.Errorf("载荷正文不许进日志（可能含 openid / 消息内容）：%s", logs)
	}
}
