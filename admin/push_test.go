package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/tidwall/buntdb"
)

// newPushTestServer 构造推送记录测试服务器。
func newPushTestServer() *httptest.Server {
	s := &Server{token: "t"}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/pushes", s.handlePushes)
	return httptest.NewServer(mux)
}

func resetPushRing() {
	globalPushRing = newPushRing()
}

func TestPushRing(t *testing.T) {
	resetPushRing()
	// 追加与快照顺序（最旧在前）
	for i := 0; i < 5; i++ {
		globalPushRing.Append(lsp.PushEntry{Site: "bilibili", GroupCode: int64(100 + i), Status: lsp.PushStatusSent})
	}
	snap := globalPushRing.Snapshot()
	if len(snap) != 5 || snap[0].GroupCode != 100 || snap[4].GroupCode != 104 {
		t.Fatalf("快照顺序错误 len=%d first=%v last=%v", len(snap), snap[0].GroupCode, snap[4].GroupCode)
	}
	// 环形覆盖：容量 500，写入 520 条后只剩最新 500 条
	for i := 0; i < 520; i++ {
		globalPushRing.Append(lsp.PushEntry{GroupCode: int64(i), Status: lsp.PushStatusSent})
	}
	snap = globalPushRing.Snapshot()
	if len(snap) != 500 {
		t.Fatalf("环形容量错误 len=%d", len(snap))
	}
	if snap[0].GroupCode != 20 {
		t.Fatalf("最旧应为 20，got %v", snap[0].GroupCode)
	}
}

func TestPushAPIListAndFilter(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	clearPersistedPushes(t)
	ts := newPushTestServer()
	defer ts.Close()

	base := time.Now()
	persistPushEntry(lsp.PushEntry{Time: base, Site: "bilibili", Status: lsp.PushStatusSent, GroupCode: 1})
	persistPushEntry(lsp.PushEntry{Time: base.Add(time.Second), Site: "weibo", Status: lsp.PushStatusFailed, GroupCode: 2})
	persistPushEntry(lsp.PushEntry{Time: base.Add(2 * time.Second), Site: "weibo", Status: lsp.PushStatusSkipped, GroupCode: 3})

	// 全量倒序
	resp, _ := http.Get(ts.URL + "/api/v1/pushes")
	var body struct {
		Pushes []lsp.PushEntry `json:"pushes"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(body.Pushes) != 3 || body.Pushes[0].GroupCode != 3 {
		t.Fatalf("全量查询错误 status=%d len=%d", resp.StatusCode, len(body.Pushes))
	}
	// site 过滤
	resp, _ = http.Get(ts.URL + "/api/v1/pushes?site=weibo")
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if len(body.Pushes) != 2 {
		t.Fatalf("site 过滤错误 len=%d", len(body.Pushes))
	}
	// status 过滤
	resp, _ = http.Get(ts.URL + "/api/v1/pushes?status=failed")
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if len(body.Pushes) != 1 || body.Pushes[0].Site != "weibo" {
		t.Fatalf("status 过滤错误 len=%d", len(body.Pushes))
	}
	// limit
	resp, _ = http.Get(ts.URL + "/api/v1/pushes?limit=1")
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if len(body.Pushes) != 1 || body.Pushes[0].GroupCode != 3 {
		t.Fatal("limit 错误")
	}
}


// ---- 持久化层测试 ----

func clearPersistedPushes(t *testing.T) {
	err := localdb.RWCoverTx(func(tx *buntdb.Tx) error {
		var keys []string
		if err := tx.AscendKeys(pushRecordPattern, func(key, value string) bool {
			keys = append(keys, key)
			return true
		}); err != nil {
			return err
		}
		for _, k := range keys {
			if _, err := tx.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("清理持久化记录失败 %v", err)
	}
}

func TestPushPersistAndLoad(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	clearPersistedPushes(t)

	// 写入三条不同时间的记录
	base := time.Now()
	for i := 0; i < 3; i++ {
		persistPushEntry(lsp.PushEntry{
			Time:      base.Add(time.Duration(i) * time.Minute),
			Site:      "bilibili",
			GroupCode: int64(100 + i),
			Status:    lsp.PushStatusSent,
		})
	}
	// 倒序读取：最新在前
	got, err := loadPersistedPushes(10)
	if err != nil || len(got) != 3 {
		t.Fatalf("读取持久化失败 err=%v len=%d", err, len(got))
	}
	if got[0].GroupCode != 102 || got[2].GroupCode != 100 {
		t.Fatalf("倒序错误 first=%v last=%v", got[0].GroupCode, got[2].GroupCode)
	}
	// limit
	got, _ = loadPersistedPushes(2)
	if len(got) != 2 || got[0].GroupCode != 102 {
		t.Fatalf("limit 读取错误 %v", got)
	}
}

func TestPushBackfill(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	clearPersistedPushes(t)
	resetPushRing()

	base := time.Now()
	for i := 0; i < 3; i++ {
		persistPushEntry(lsp.PushEntry{Time: base.Add(time.Duration(i) * time.Minute), GroupCode: int64(i), Status: lsp.PushStatusSent})
	}
	backfillPushRing() // db 已就绪，立即成功
	snap := globalPushRing.Snapshot()
	if len(snap) != 3 || snap[0].GroupCode != 0 || snap[2].GroupCode != 2 {
		t.Fatalf("回填顺序错误 len=%d", len(snap))
	}
}

func TestPushAPIFromPersistence(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	clearPersistedPushes(t)
	resetPushRing()
	ts := newPushTestServer()
	defer ts.Close()

	base := time.Now()
	persistPushEntry(lsp.PushEntry{Time: base, Site: "weibo", Status: lsp.PushStatusSent, GroupCode: 1})
	persistPushEntry(lsp.PushEntry{Time: base.Add(time.Second), Site: "douyu", Status: lsp.PushStatusFailed, GroupCode: 2})

	resp, _ := http.Get(ts.URL + "/api/v1/pushes")
	var body struct {
		Pushes []lsp.PushEntry `json:"pushes"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(body.Pushes) != 2 || body.Pushes[0].GroupCode != 2 {
		t.Fatalf("REST 持久层查询错误 status=%d len=%d", resp.StatusCode, len(body.Pushes))
	}
	// site 过滤（走持久层）
	resp, _ = http.Get(ts.URL + "/api/v1/pushes?site=weibo")
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if len(body.Pushes) != 1 || body.Pushes[0].Site != "weibo" {
		t.Fatalf("持久层过滤错误 len=%d", len(body.Pushes))
	}
}

// eventbus → ring 全链路（lsp 发布侧不可直接测，此处验证 admin 收集链路）
func TestPushCollectorViaEventbus(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	clearPersistedPushes(t)
	resetPushRing()
	installPushCollector()
	eventbus.BusObj.Publish("push_record", lsp.PushEntry{Site: "douyu", Status: lsp.PushStatusSent, GroupCode: 9})
	// 发布是非阻塞投递到收集 goroutine，轮询等待 ring 生效（上限 1s）
	for i := 0; i < 100; i++ {
		if len(globalPushRing.Snapshot()) == 1 {
			break
		}
		if i == 99 {
			t.Fatal("eventbus 发布未被收集进 ring")
		}
		time.Sleep(10 * time.Millisecond)
	}
	snap := globalPushRing.Snapshot()
	if snap[0].Site != "douyu" {
		t.Fatalf("收集内容错误 %v", snap[0].Site)
	}
	// 非法类型应被忽略
	eventbus.BusObj.Publish("push_record", "not-an-entry")
}

// TestPushMergeHistory 覆盖 M7 的合并语义：回填与实时消费并发时，
// 记录必须按时间升序、同一条记录去重，且回填期间已在 ring 里的实时事件不丢失。
func TestPushMergeHistory(t *testing.T) {
	resetPushRing()
	live := lsp.PushEntry{Time: time.Now(), Site: "douyu", GroupCode: 1, Status: lsp.PushStatusSent}
	globalPushRing.Append(live)

	older := lsp.PushEntry{Time: live.Time.Add(-time.Minute), Site: "bilibili", GroupCode: 2, Status: lsp.PushStatusFailed}
	newer := lsp.PushEntry{Time: live.Time.Add(time.Minute), Site: "weibo", GroupCode: 3, Status: lsp.PushStatusSkipped}
	// loadPersistedPushes 返回时间倒序；live 的副本模拟「实时事件已落库又被回填读到」
	globalPushRing.MergeHistory([]lsp.PushEntry{newer, live, older})

	snap := globalPushRing.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("合并后应去重为 3 条，got %d: %+v", len(snap), snap)
	}
	if snap[0].GroupCode != 2 || snap[1].GroupCode != 1 || snap[2].GroupCode != 3 {
		t.Fatalf("合并后应为时间升序 2,1,3，got %+v", snap)
	}
}

// TestPushMergeHistoryCapacity 验证合并后仍受环形容量约束，且保留最新记录。
func TestPushMergeHistoryCapacity(t *testing.T) {
	resetPushRing()
	base := time.Now()
	globalPushRing.MergeHistory([]lsp.PushEntry{{Time: base, GroupCode: -1}})

	entries := make([]lsp.PushEntry, 0, pushRingCapacity+50)
	for i := 0; i < pushRingCapacity+50; i++ {
		entries = append(entries, lsp.PushEntry{Time: base.Add(time.Duration(i+1) * time.Millisecond), GroupCode: int64(i)})
	}
	globalPushRing.MergeHistory(entries)

	snap := globalPushRing.Snapshot()
	if len(snap) != pushRingCapacity {
		t.Fatalf("合并后应保留容量上限 %d 条，got %d", pushRingCapacity, len(snap))
	}
	if snap[0].GroupCode != 50 || snap[len(snap)-1].GroupCode != int64(pushRingCapacity+49) {
		t.Fatalf("容量截断错误 first=%d last=%d", snap[0].GroupCode, snap[len(snap)-1].GroupCode)
	}
}
