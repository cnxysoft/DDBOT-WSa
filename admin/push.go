package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/buntdb"
)

// 推送记录：lsp 侧在推送消费出口经 eventbus（topic: push_record）广播
// lsp.PushEntry，admin 侧收集进内存环形缓冲（重启即清空，与日志流同一哲学），
// 提供 REST 快照查询 + SSE 实时流。

const pushRingCapacity = 500

// pushRing 与 sse.go 的 logRing 同构（类型不同，独立实现避免改动已验证的日志链路）。
type pushRing struct {
	mu   sync.Mutex
	buf  []lsp.PushEntry
	head int
	size int
	subs map[chan lsp.PushEntry]struct{}
}

var globalPushRing = newPushRing()

func newPushRing() *pushRing {
	return &pushRing{
		buf:  make([]lsp.PushEntry, pushRingCapacity),
		subs: make(map[chan lsp.PushEntry]struct{}),
	}
}

func (p *pushRing) Append(e lsp.PushEntry) {
	p.mu.Lock()
	p.buf[p.head] = e
	p.head = (p.head + 1) % len(p.buf)
	if p.size < len(p.buf) {
		p.size++
	}
	for ch := range p.subs {
		select {
		case ch <- e:
		default: // 慢消费者丢弃
		}
	}
	p.mu.Unlock()
}

func (p *pushRing) Snapshot() []lsp.PushEntry {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.snapshotLocked()
}

// snapshotLocked 返回按时间正序排列的记录副本；调用方必须持有 p.mu。
func (p *pushRing) snapshotLocked() []lsp.PushEntry {
	if p.size == 0 {
		return nil
	}
	out := make([]lsp.PushEntry, 0, p.size)
	start := (p.head - p.size + len(p.buf)) % len(p.buf)
	for i := 0; i < p.size; i++ {
		out = append(out, p.buf[(start+i)%len(p.buf)])
	}
	return out
}

// MergeHistory 把回填的历史记录与 ring 中已有的实时记录合并：按 Time 升序排序，
// 同一条记录（时间+平台+群+状态完全相同）去重，超出容量的部分保留最新的记录。
// 配合「先启动消费循环、回填在独立 goroutine」：即使 db 就绪晚于首次推送，
// 回填期间到达的实时事件也已进入 ring 并落库，不会被回填覆盖或重复插入。
func (p *pushRing) MergeHistory(entries []lsp.PushEntry) {
	p.mu.Lock()
	defer p.mu.Unlock()

	type entryKey struct {
		ns        int64
		site      string
		groupCode int64
		status    string
	}
	seen := make(map[entryKey]struct{}, p.size+len(entries))
	all := make([]lsp.PushEntry, 0, p.size+len(entries))
	appendUnique := func(e lsp.PushEntry) {
		k := entryKey{e.Time.UnixNano(), e.Site, e.GroupCode, e.Status}
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		all = append(all, e)
	}
	for _, e := range p.snapshotLocked() {
		appendUnique(e)
	}
	for _, e := range entries {
		appendUnique(e)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Time.Before(all[j].Time) })
	if len(all) > len(p.buf) {
		all = all[len(all)-len(p.buf):] // 只保留最新的 N 条
	}
	p.head = 0
	p.size = 0
	for _, e := range all {
		p.buf[p.head] = e
		p.head = (p.head + 1) % len(p.buf)
		p.size++
	}
}

func (p *pushRing) Subscribe() chan lsp.PushEntry {
	ch := make(chan lsp.PushEntry, 64)
	p.mu.Lock()
	p.subs[ch] = struct{}{}
	p.mu.Unlock()
	return ch
}

func (p *pushRing) Unsubscribe(ch chan lsp.PushEntry) {
	p.mu.Lock()
	delete(p.subs, ch)
	p.mu.Unlock()
}

// pushCollectorOnce 防止重复启动收集 goroutine（admin.Start 可能被多次调用）。
var pushCollectorOnce sync.Once

// installPushCollector 订阅 eventbus，把推送记录写入环形缓冲（SSE 实时流）并持久化到 buntdb。
// 消费循环立即启动：即使 db 尚未就绪、回填还在重试，回填期间到达的推送事件也会被
// 收集并落库（因此不再依赖「db 就绪早于首次推送」这一顺序假设，启动窗口不再丢事件）。
// 回填放在独立 goroutine，完成后与实时记录去重合并（MergeHistory）。
func installPushCollector() {
	pushCollectorOnce.Do(func() {
		ch := eventbus.BusObj.Subscribe("push_record")
		go func() {
			for entry := range ch {
				if e, ok := entry.(lsp.PushEntry); ok {
					globalPushRing.Append(e)
					persistPushEntry(e)
				}
			}
		}()
		go backfillPushRing()
	})
}

// ---- 持久化（buntdb，.lsp.db）----
// 推送记录落库：key = push:record:<UnixNano 补零>（字典序即时间序），
// 值为 PushEntry JSON，TTL 30 天自动过期，防止无限膨胀。
// REST 历史查询走持久层；ring 仅服务 SSE 实时流。

const pushRecordKeyPrefix = "push:record:"
const pushRecordPattern = pushRecordKeyPrefix + "*"
const pushRecordTTL = 30 * 24 * time.Hour

func pushRecordKey(t time.Time) string {
	return fmt.Sprintf("%s%019d", pushRecordKeyPrefix, t.UnixNano())
}

func persistPushEntry(e lsp.PushEntry) {
	if err := localdb.SetJson(pushRecordKey(e.Time), e, localdb.SetExpireOpt(pushRecordTTL)); err != nil {
		logrus.WithError(err).Debug("push record persist failed") // 持久化失败不影响实时流
	}
}

func loadPersistedPushes(limit int) ([]lsp.PushEntry, error) {
	var out []lsp.PushEntry
	err := localdb.RCoverTx(func(tx *buntdb.Tx) error {
		return tx.DescendKeys(pushRecordPattern, func(key, value string) bool {
			var e lsp.PushEntry
			if json.Unmarshal([]byte(value), &e) == nil {
				out = append(out, e)
			}
			return len(out) < limit
		})
	})
	return out, err
}

// backfillPushRing 重启后把持久层最近记录回填进实时环形缓冲。
// db 可能晚于 admin 启动就绪（模块初始化顺序），重试至成功。
// 回填通过 MergeHistory 与消费循环已在 ring 中的实时记录合并去重，
// 因此与实时消费并发是安全的（消费循环先启动，见 installPushCollector）。
func backfillPushRing() {
	for i := 0; i < 60; i++ { // 最多重试 60 次 × 5s = 5 分钟
		entries, err := loadPersistedPushes(pushRingCapacity)
		if err == nil {
			globalPushRing.MergeHistory(entries)
			if len(entries) > 0 {
				logrus.WithField("count", len(entries)).Info("推送记录已从本地库回填")
			}
			return
		}
		time.Sleep(5 * time.Second)
	}
	logrus.Warn("推送记录回填失败：数据库长时间未就绪，仅实时流可用")
}


// handlePushes GET /api/v1/pushes?limit=&site=&status=
// 返回推送记录（时间倒序，最新在前），可选按平台/状态过滤。查询走持久层（30 天）。
func (s *Server) handlePushes(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= pushRingCapacity {
		limit = v
	}
	site := r.URL.Query().Get("site")
	status := r.URL.Query().Get("status")

	all, err := loadPersistedPushes(pushRingCapacity)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "数据库未就绪")
		return
	}
	out := make([]lsp.PushEntry, 0, limit)
	for _, e := range all { // all 已是倒序
		if site != "" && e.Site != site {
			continue
		}
		if status != "" && e.Status != status {
			continue
		}
		out = append(out, e)
		if len(out) >= limit {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"pushes": out})
}

// handlePushesStream GET /api/v1/pushes/stream
// SSE：先回放缓冲内全部记录（正序），再实时推送新记录。
func (s *Server) handlePushesStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	for _, e := range globalPushRing.Snapshot() {
		writePushSSE(w, e)
	}
	flusher.Flush()

	ch := globalPushRing.Subscribe()
	defer globalPushRing.Unsubscribe(ch)
	heartbeat := time.NewTicker(20 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case e := <-ch:
			writePushSSE(w, e)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func writePushSSE(w http.ResponseWriter, e lsp.PushEntry) {
	data, _ := json.Marshal(e)
	fmt.Fprintf(w, "event: push\ndata: %s\n\n", data)
}
