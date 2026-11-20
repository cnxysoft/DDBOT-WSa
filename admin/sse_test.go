package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetGlobalLogRing() { globalLogRing = newLogRing() }

// TestLogRing_AppendAndSnapshot 验证顺序写入与读取，以及容量满后最旧条目被覆盖。
func TestLogRing_AppendAndSnapshot(t *testing.T) {
	l := newLogRing()
	assert.Empty(t, l.Snapshot())

	for i := 0; i < logRingCapacity+5; i++ {
		l.Append(LogInfo{Time: fmt.Sprintf("t%d", i), Level: "info", Message: fmt.Sprintf("m%d", i)})
	}

	snap := l.Snapshot()
	assert.Len(t, snap, logRingCapacity)
	// 最旧的 5 条已被覆盖，快照从 m5 开始且顺序递增
	assert.Equal(t, "m5", snap[0].Message)
	assert.Equal(t, fmt.Sprintf("m%d", logRingCapacity+4), snap[len(snap)-1].Message)
}

// TestLogRing_Subscribe 验证订阅者收到增量推送，且慢消费者不会阻塞 Append。
func TestLogRing_Subscribe(t *testing.T) {
	l := newLogRing()
	sub := l.Subscribe()

	l.Append(LogInfo{Message: "hello"})
	select {
	case e := <-sub.ch:
		assert.Equal(t, "hello", e.Message)
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive the appended log")
	}

	// 慢消费者：订阅后从不读取，写入远超 channel 容量的日志，Append 不应阻塞
	slow := l.Subscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			l.Append(LogInfo{Message: "spam"})
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Append blocked on a slow consumer")
	}
	_ = slow
}

// TestAdminLogHook_Fire 验证 logrus Entry 到 LogInfo 的字段转换。
func TestAdminLogHook_Fire(t *testing.T) {
	resetGlobalLogRing()
	h := &adminLogHook{}
	entry := &logrus.Entry{
		Time:    time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC),
		Level:   logrus.WarnLevel,
		Message: "cookie 即将过期",
	}
	err := h.Fire(entry)
	assert.NoError(t, err)

	snap := globalLogRing.Snapshot()
	require.Len(t, snap, 1)
	assert.Equal(t, "warning", snap[0].Level)
	assert.Equal(t, "cookie 即将过期", snap[0].Message)
	assert.Contains(t, snap[0].Time, "2026-09-24")
}

// TestHandleLogsStream 验证 SSE 流：先回放存量，再推送增量，客户端断开后返回。
func TestHandleLogsStream(t *testing.T) {
	resetGlobalLogRing()
	globalLogRing.Append(LogInfo{Time: "t0", Level: "info", Message: "replay-me"})

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/logs/stream", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	s := &Server{}

	go func() {
		time.Sleep(100 * time.Millisecond)
		globalLogRing.Append(LogInfo{Time: "t1", Level: "warning", Message: "live-me"})
	}()

	s.handleLogsStream(w, r)

	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	body := w.Body.String()
	assert.Contains(t, body, "event: log")
	assert.Contains(t, body, "replay-me") // 存量回放
	assert.Contains(t, body, "live-me")   // 增量推送
}
