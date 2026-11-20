package admin

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// logRingCapacity 内存日志环形缓冲的容量（约 1000 条 × 数百字节，常驻内存可忽略）。
const logRingCapacity = 1000

// logSubscriber 表示一个 SSE 订阅者。
// ch 带缓冲；缓冲写满时直接丢弃（慢消费者不阻塞业务 goroutine 写日志）。
type logSubscriber struct {
	ch chan LogInfo
}

// logRing 是线程安全的内存日志环形缓冲，支持快照读取与订阅广播。
type logRing struct {
	mu   sync.Mutex
	buf  []LogInfo
	head int // 下一个写入位置
	size int
	subs map[*logSubscriber]struct{}
}

var globalLogRing = newLogRing()

func newLogRing() *logRing {
	return &logRing{
		buf:  make([]LogInfo, logRingCapacity),
		subs: make(map[*logSubscriber]struct{}),
	}
}

// Append 写入一条日志并广播给所有订阅者（非阻塞）。
func (l *logRing) Append(e LogInfo) {
	l.mu.Lock()
	l.buf[l.head] = e
	l.head = (l.head + 1) % len(l.buf)
	if l.size < len(l.buf) {
		l.size++
	}
	for sub := range l.subs {
		select {
		case sub.ch <- e:
		default: // 慢消费者：丢弃，避免阻塞写日志的 goroutine
		}
	}
	l.mu.Unlock()
}

// Snapshot 返回缓冲内日志的顺序副本（最旧在前）。
func (l *logRing) Snapshot() []LogInfo {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.size == 0 {
		return nil
	}
	out := make([]LogInfo, 0, l.size)
	start := (l.head - l.size + len(l.buf)) % len(l.buf)
	for i := 0; i < l.size; i++ {
		out = append(out, l.buf[(start+i)%len(l.buf)])
	}
	return out
}

// Subscribe 注册一个订阅者，此后 Append 的日志会推送到其 channel。
func (l *logRing) Subscribe() *logSubscriber {
	sub := &logSubscriber{ch: make(chan LogInfo, 64)}
	l.mu.Lock()
	l.subs[sub] = struct{}{}
	l.mu.Unlock()
	return sub
}

// Unsubscribe 注销订阅者。
func (l *logRing) Unsubscribe(sub *logSubscriber) {
	l.mu.Lock()
	delete(l.subs, sub)
	l.mu.Unlock()
}

// adminLogHook 将 logrus 日志桥接到内存环形缓冲，供 admin SSE 推送。
// Fire 在写日志的 goroutine 内同步调用，实现必须快（无 IO、无阻塞）。
type adminLogHook struct{}

func (h *adminLogHook) Levels() []logrus.Level { return logrus.AllLevels }

func (h *adminLogHook) Fire(entry *logrus.Entry) error {
	globalLogRing.Append(LogInfo{
		Time:    entry.Time.Format(time.RFC3339),
		Level:   entry.Level.String(),
		Message: entry.Message,
	})
	return nil
}

// InstallLogHook 安装内存日志 hook，越早调用越能覆盖启动早期日志。
// 由 bot.go 的 SetUpLog 调用（无条件安装，即使 admin 未启用也仅常驻约数百 KB 内存）。
func InstallLogHook() {
	logrus.AddHook(&adminLogHook{})
}

// writeSSEEvent 以 SSE 帧格式写入一条日志事件。
func writeSSEEvent(w http.ResponseWriter, e LogInfo) {
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("event: log\ndata: "))
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n\n"))
}

// handleLogsStream 以 Server-Sent Events 推送实时日志：
// 连接建立后先回放内存缓冲中的存量日志，再持续推送增量。
// 客户端用 fetch 流式读取即可携带 Authorization 头（EventSource 不支持自定义头）。
func (s *Server) handleLogsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // 禁用反向代理缓冲

	// 回放存量
	for _, e := range globalLogRing.Snapshot() {
		writeSSEEvent(w, e)
	}
	flusher.Flush()

	// 增量推送，直到客户端断开
	sub := globalLogRing.Subscribe()
	defer globalLogRing.Unsubscribe(sub)
	for {
		select {
		case e := <-sub.ch:
			writeSSEEvent(w, e)
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// logrusLineRe 匹配本项目日志格式（TextFormatter + ForceQuote）：
// time="..." level="..." msg="..."。msg 为行尾字段，贪婪匹配后经 Unquote 还原转义。
var logrusLineRe = regexp.MustCompile(`^time="(.*)" level="(.*)" msg="(.*)"$`)

// parseLogrusLine 解析一行日志文件内容；解析失败返回 false（调用方按原样处理）。
func parseLogrusLine(line string) (LogInfo, bool) {
	m := logrusLineRe.FindStringSubmatch(line)
	if m == nil {
		return LogInfo{}, false
	}
	msg, err := unquoteLogValue(m[3])
	if err != nil {
		return LogInfo{}, false
	}
	return LogInfo{Time: m[1], Level: m[2], Message: msg}, true
}

// unquoteLogValue 还原 logrus %q 转义的字段值（处理 \" 等转义）。
func unquoteLogValue(s string) (string, error) {
	return strconv.Unquote("\"" + s + "\"")
}
