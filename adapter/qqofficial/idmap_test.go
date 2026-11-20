package qqofficial

// idmap 的单测。重点覆盖硬约束：
//  1. key 必须带 appID（实测 per-bot openid）；
//  2. 重启后从 Persister 恢复，ID 必须不变；
//  3. 并发分配必须原子（-race 下跑）。
//
// 全部使用内存假 Persister，不在 adapter module 引入任何存储依赖。

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

// fakePersister 是内存 Persister。把同一份 data 交给新的 IDMap，
// 就等价于「进程重启后重新加载持久化数据」。
type fakePersister struct {
	mu      sync.Mutex
	data    map[string]int64
	loadErr error
	saveErr error
	loads   int
	saves   int
}

func newFakePersister() *fakePersister {
	return &fakePersister{data: make(map[string]int64)}
}

func (f *fakePersister) Load() (map[string]int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loads++
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	out := make(map[string]int64, len(f.data))
	for k, v := range f.data {
		out[k] = v
	}
	return out, nil
}

// Save 是快照语义：整体替换，不是增量合并。
func (f *fakePersister) Save(m map[string]int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	if f.saveErr != nil {
		return f.saveErr
	}
	cp := make(map[string]int64, len(m))
	for k, v := range m {
		cp[k] = v
	}
	f.data = cp
	return nil
}

func (f *fakePersister) snapshot() map[string]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int64, len(f.data))
	for k, v := range f.data {
		out[k] = v
	}
	return out
}

func (f *fakePersister) counters() (loads, saves int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.loads, f.saves
}

func TestIDMapRoundTripAndAutoIncrement(t *testing.T) {
	m := NewIDMap(newFakePersister())

	a := m.ToInt64("1001", "AAA")
	b := m.ToInt64("1001", "BBB")
	if a != 1 || b != 2 {
		t.Fatalf("ID 必须自增且从 1 开始，得到 a=%d b=%d", a, b)
	}

	// 同一个 key 反复问必须同一个 ID，且不推进计数器
	for i := 0; i < 5; i++ {
		if got := m.ToInt64("1001", "AAA"); got != a {
			t.Fatalf("同一 (appID, openid) 必须稳定，第 %d 次得到 %d，期望 %d", i, got, a)
		}
	}
	if c := m.ToInt64("1001", "CCC"); c != 3 {
		t.Fatalf("重复查询不应消耗 ID，新 key 应拿到 3，得到 %d", c)
	}

	openid, ok := m.ToOpenID("1001", a)
	if !ok || openid != "AAA" {
		t.Fatalf("反查失败：ok=%v openid=%q", ok, openid)
	}
	if _, ok := m.ToOpenID("1001", 999); ok {
		t.Error("未知 ID 必须返回 false")
	}
}

// 硬约束 1：openid 是 per-bot 的，key 必须带 appID。
func TestIDMapAppIDScoped(t *testing.T) {
	p := newFakePersister()
	m := NewIDMap(p)

	// 实测数据：同一个群在两个机器人下 openid 完全不同。
	personalGroup := m.ToInt64("1900000000", "03A2B3C4D5E6F708192A3B4C5D6E7F82")
	enterpriseGroup := m.ToInt64("1900000001", "04A2B3C4D5E6F708192A3B4C5D6E7F83")
	if personalGroup == enterpriseGroup {
		t.Fatal("不同 appID 下的 openid 必须是不同内部 ID")
	}

	// 同一个 openid 字符串在不同 appID 下是两个不同身份。
	a := m.ToInt64("1900000000", "SAME-OPENID")
	b := m.ToInt64("1900000001", "SAME-OPENID")
	if a == b {
		t.Fatal("同一个 openid 在不同 appID 下必须分开分配")
	}

	// 反查只认自己的 appID：拿 A 的 ID 问 B 必须 false，
	// 否则就是把 A 的 openid 张冠李戴给 B（实测会 11255 资源不存在）。
	if got, ok := m.ToOpenID("1900000001", personalGroup); ok {
		t.Errorf("不能拿 A 机器人的 ID 反查出 B 的 openid，却返回了 %q", got)
	}
	if got, ok := m.ToOpenID("1900000000", personalGroup); !ok || got != "03A2B3C4D5E6F708192A3B4C5D6E7F82" {
		t.Errorf("本 appID 反查失败：ok=%v openid=%q", ok, got)
	}

	// 落盘后 key 里必须带 appID，且能被解析回来。
	if err := m.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	snap := p.snapshot()
	if len(snap) != 4 {
		t.Fatalf("应持久化 4 条映射，实际 %d：%v", len(snap), snap)
	}
	for k := range snap {
		if !strings.Contains(k, "1900000000") && !strings.Contains(k, "1900000001") {
			t.Errorf("持久化 key 必须包含 appID，实际 %q", k)
		}
		appID, openid, ok := splitIDKey(k)
		if !ok || appID == "" || openid == "" {
			t.Errorf("持久化 key 必须能切回 (appID, openid)，实际 %q", k)
		}
	}
}

// 硬约束 2：进程重启后从 Persister 重新 Load，同一个 (appID, openid) 还是同一个 ID。
func TestIDMapRestartStability(t *testing.T) {
	p := newFakePersister()

	m1 := NewIDMap(p)
	g1 := m1.ToInt64("app1", "group-openid-1")
	g2 := m1.ToInt64("app1", "group-openid-2")
	u1 := m1.ToInt64("app2", "user-openid-1")
	if err := m1.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// 模拟重启：同一份持久化数据，全新的 IDMap。
	m2 := NewIDMap(p)
	cases := []struct {
		appID, openid string
		want          int64
	}{
		{"app1", "group-openid-1", g1},
		{"app1", "group-openid-2", g2},
		{"app2", "user-openid-1", u1},
	}
	for _, c := range cases {
		if got := m2.ToInt64(c.appID, c.openid); got != c.want {
			t.Errorf("重启后 (%s,%s) 的 ID 应为 %d，实际 %d", c.appID, c.openid, c.want, got)
		}
		got, ok := m2.ToOpenID(c.appID, c.want)
		if !ok || got != c.openid {
			t.Errorf("重启后反查 (%s,%d) 应为 %q，实际 ok=%v %q", c.appID, c.want, c.openid, ok, got)
		}
	}

	// 重启后新分配的 ID 不能撞上任何旧 ID。
	used := map[int64]bool{g1: true, g2: true, u1: true}
	for i := 0; i < 5; i++ {
		id := m2.ToInt64("app1", fmt.Sprintf("new-openid-%d", i))
		if used[id] {
			t.Fatalf("新分配的 ID %d 撞上已有 ID", id)
		}
		used[id] = true
	}

	// 第二轮重启仍然稳定。
	if err := m2.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	m3 := NewIDMap(p)
	if got := m3.ToInt64("app1", "new-openid-4"); got != m2.ToInt64("app1", "new-openid-4") {
		t.Errorf("二次重启后 ID 漂移：%d", got)
	}
}

// Flush 必须真的调用 Persister.Save，并且快照覆盖全部映射。
func TestIDMapFlushCallsSaveWithFullSnapshot(t *testing.T) {
	p := newFakePersister()
	m := NewIDMap(p)

	m.ToInt64("app", "x")
	m.ToInt64("app", "y")
	if _, saves := p.counters(); saves != 0 {
		t.Fatalf("分配阶段不应写盘，save 次数 %d", saves)
	}
	if err := m.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if _, saves := p.counters(); saves != 1 {
		t.Fatalf("Flush 应调用一次 Save，实际 %d", saves)
	}
	if snap := p.snapshot(); len(snap) != 2 {
		t.Fatalf("快照应含 2 条，实际 %v", snap)
	}
	// 再 Flush 一次仍是幂等的完整快照。
	m.ToInt64("app", "z")
	if err := m.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if snap := p.snapshot(); len(snap) != 3 {
		t.Fatalf("快照应含 3 条，实际 %v", snap)
	}
}

// 并发分配：同一对 key 只允许一个 ID，不同 key 不允许共用 ID。
func TestIDMapConcurrentAtomicAllocation(t *testing.T) {
	p := newFakePersister()
	m := NewIDMap(p)

	const (
		slotCount = 32
		workers   = 16
		rounds    = 200
	)
	slots := make([]string, slotCount)
	for i := range slots {
		// 交替 appID，让「同一 openid 不同 appID」也在并发路径上跑一遍
		appID := "app-A"
		if i%2 == 1 {
			appID = "app-B"
		}
		slots[i] = composeIDKey(appID, fmt.Sprintf("openid-%02d", i))
	}

	var (
		mu       sync.Mutex
		observed = make(map[string]int64, slotCount)
		conflict int
	)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			local := make(map[string]int64, slotCount)
			for i := 0; i < rounds; i++ {
				k := slots[(w*7+i)%slotCount]
				appID, openid, _ := splitIDKey(k)
				id := m.ToInt64(appID, openid)
				if prev, ok := local[k]; ok && prev != id {
					t.Errorf("并发下同一个 key 拿到两个 ID：%d vs %d", prev, id)
				}
				local[k] = id
			}
			mu.Lock()
			for k, id := range local {
				if prev, ok := observed[k]; ok && prev != id {
					conflict++
				}
				observed[k] = id
			}
			mu.Unlock()
		}(w)
	}
	wg.Wait()

	if conflict != 0 {
		t.Fatalf("并发分配出现 %d 次不一致", conflict)
	}
	if len(observed) != slotCount {
		t.Fatalf("应观察到 %d 个 key，实际 %d", slotCount, len(observed))
	}
	// ID 全局唯一：不能把同一个 ID 分给两个不同 openid。
	seen := make(map[int64]string, slotCount)
	for k, id := range observed {
		if other, ok := seen[id]; ok {
			t.Fatalf("ID %d 被分给了 %q 和 %q", id, other, k)
		}
		seen[id] = k
	}
	// 反查与分配一致。
	for k, id := range observed {
		appID, openid, _ := splitIDKey(k)
		got, ok := m.ToOpenID(appID, id)
		if !ok || got != openid {
			t.Fatalf("反查 (%s,%d) 应为 %q，实际 ok=%v %q", appID, id, openid, ok, got)
		}
	}
	// 快照必须包含全部并发分配的结果（不能漏写）。
	if err := m.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	snap := p.snapshot()
	if len(snap) != slotCount {
		t.Fatalf("并发分配后快照应含 %d 条，实际 %d", slotCount, len(snap))
	}
	for k, id := range observed {
		if snap[k] != id {
			t.Fatalf("快照里 %q 的 ID 为 %d，内存里是 %d", k, snap[k], id)
		}
	}
}

// 空值 / 异常输入：不 panic，不占用 ID，不污染持久化。
func TestIDMapInvalidInputNoAllocation(t *testing.T) {
	p := newFakePersister()
	m := NewIDMap(p)

	if got := m.ToInt64("", "openid"); got != 0 {
		t.Errorf("空 appID 应返回 0，实际 %d", got)
	}
	if got := m.ToInt64("app", ""); got != 0 {
		t.Errorf("空 openid 应返回 0，实际 %d", got)
	}
	if got := m.ToInt64("", ""); got != 0 {
		t.Errorf("双空应返回 0，实际 %d", got)
	}
	if _, ok := m.ToOpenID("", 1); ok {
		t.Error("空 appID 反查必须 false")
	}
	if _, ok := m.ToOpenID("app", 0); ok {
		t.Error("ID=0 反查必须 false")
	}
	if _, ok := m.ToOpenID("app", -1); ok {
		t.Error("负数 ID 反查必须 false")
	}
	if _, ok := m.ToOpenID("app", maxAllocID); ok {
		t.Error("未分配的上界 ID 反查必须 false")
	}

	// 非法输入没有消耗 ID：第一个合法输入仍然是 1。
	if got := m.ToInt64("app", "real-openid"); got != 1 {
		t.Errorf("非法输入不应占用 ID，实际拿到 %d", got)
	}
	if err := m.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if snap := p.snapshot(); len(snap) != 1 {
		t.Errorf("非法输入不应写进持久化，实际 %v", snap)
	}
}

// openid 里恰好含分隔符字节时也必须能往返（切分取第一个分隔符）。
func TestIDMapOpenIDContainingSeparator(t *testing.T) {
	m := NewIDMap(newFakePersister())
	weird := "weird" + idKeySep + "openid"
	id := m.ToInt64("app", weird)
	if id == 0 {
		t.Fatal("合法输入不应返回 0")
	}
	got, ok := m.ToOpenID("app", id)
	if !ok || got != weird {
		t.Fatalf("含分隔符的 openid 必须能往返，实际 ok=%v %q", ok, got)
	}
	// 200 字符量级的长 openid（msg_id 同一量级）也要能存能查。
	long := "ROBOT1.0_" + strings.Repeat("AbCdE+", 30)
	lid := m.ToInt64("app", long)
	if got, ok := m.ToOpenID("app", lid); !ok || got != long {
		t.Fatalf("长 openid 必须能往返，实际 ok=%v len=%d", ok, len(got))
	}
}

func TestIDMapNilPersister(t *testing.T) {
	m := NewIDMap(nil) // 不允许 panic
	if got := m.ToInt64("app", "x"); got != 1 {
		t.Errorf("无 Persister 时映射仍应内存可用，实际 %d", got)
	}
	if got, ok := m.ToOpenID("app", 1); !ok || got != "x" {
		t.Errorf("无 Persister 时反查应可用，实际 ok=%v %q", ok, got)
	}
	if err := m.Flush(); !errors.Is(err, ErrNoPersister) {
		t.Errorf("Flush 应报 ErrNoPersister，实际 %v", err)
	}
}

// Load 失败：内存可用，但绝不允许写回（否则会用不完整的映射覆盖旧数据）。
func TestIDMapLoadErrorDegradesToReadOnly(t *testing.T) {
	p := newFakePersister()
	p.data = map[string]int64{composeIDKey("app", "old"): 42}
	p.loadErr = errors.New("buntdb: io error")

	m := NewIDMap(p)
	if got := m.ToInt64("app", "new"); got != 1 {
		t.Errorf("Load 失败后本次运行的分配仍应可用，实际 %d", got)
	}

	err := m.Flush()
	if !errors.Is(err, ErrLoadFailed) {
		t.Errorf("Flush 应报 ErrLoadFailed，实际 %v", err)
	}
	if _, saves := p.counters(); saves != 0 {
		t.Errorf("Load 失败后绝不允许调用 Save，实际 %d 次", saves)
	}
	if snap := p.snapshot(); len(snap) != 1 || snap[composeIDKey("app", "old")] != 42 {
		t.Errorf("旧数据必须原封不动，实际 %v", snap)
	}
}

// Save 失败：错误必须冒泡，且不能丢掉「待落盘」状态，下次 Flush 要重试成功。
func TestIDMapSaveErrorRetriesOnNextFlush(t *testing.T) {
	p := newFakePersister()
	m := NewIDMap(p)
	m.ToInt64("app", "x")

	p.saveErr = errors.New("disk full")
	if err := m.Flush(); err == nil {
		t.Error("Save 失败必须返回错误")
	}
	if snap := p.snapshot(); len(snap) != 0 {
		t.Errorf("失败的 Save 不应写入数据，实际 %v", snap)
	}

	p.saveErr = nil
	if err := m.Flush(); err != nil {
		t.Fatalf("重试 Flush 应成功，实际 %v", err)
	}
	if snap := p.snapshot(); len(snap) != 1 {
		t.Fatalf("重试后应落盘 1 条，实际 %v", snap)
	}

	// 落盘后重启，ID 必须还是原来那个。
	m2 := NewIDMap(p)
	if got := m2.ToInt64("app", "x"); got != 1 {
		t.Errorf("重启后 ID 应为 1，实际 %d", got)
	}
}

// 脏持久化数据：重复 ID / 非法 ID / 非法 key。
func TestIDMapSanitizesPersistedData(t *testing.T) {
	dirty := map[string]int64{
		composeIDKey("app", "A"):        1,
		composeIDKey("app", "B"):        1, // 与 A 重复
		composeIDKey("app", "C"):        0, // 非法
		composeIDKey("app", "D"):        -3,
		"garbage-key-without-separator": 7, // 非法 key
	}
	newDirty := func() *fakePersister {
		p := newFakePersister()
		for k, v := range dirty {
			p.data[k] = v
		}
		return p
	}

	p := newDirty()
	m := NewIDMap(p)
	a, b, c, dd := m.ToInt64("app", "A"), m.ToInt64("app", "B"), m.ToInt64("app", "C"), m.ToInt64("app", "D")
	ids := map[int64]string{a: "A", b: "B", c: "C", dd: "D"}
	if len(ids) != 4 {
		t.Fatalf("同一份数据里的 4 个 openid 必须拿到 4 个不同 ID，实际 %v", ids)
	}
	for _, s := range []struct {
		name string
		id   int64
	}{{"A", a}, {"B", b}, {"C", c}, {"D", dd}} {
		if s.id <= 0 {
			t.Errorf("%s 拿到非法 ID %d", s.name, s.id)
		}
	}
	if a != 1 {
		t.Errorf("排序后 app/A 应保留原 ID 1，实际 %d", a)
	}
	// 新分配的 ID 必须跳过持久化里出现过的最大值（7），不复用。
	for _, id := range []int64{b, c, dd} {
		if id <= 7 {
			t.Errorf("重新分配的 ID %d 复用了持久化里出现过的 ID 段", id)
		}
	}

	// 确定性：同一份脏数据必须得到同一份结果。
	m2 := NewIDMap(newDirty())
	for _, s := range []struct {
		openid string
		id     int64
	}{{"A", a}, {"B", b}, {"C", c}, {"D", dd}} {
		if got := m2.ToInt64("app", s.openid); got != s.id {
			t.Errorf("脏数据解析必须确定性：%s 得到 %d，期望 %d", s.openid, got, s.id)
		}
	}

	// 清洗后落盘再重启，结果保持稳定。
	if err := m.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if snap := p.snapshot(); len(snap) != 4 {
		t.Fatalf("落盘应只剩 4 条合法映射，实际 %v", snap)
	}
	m3 := NewIDMap(p)
	for _, s := range []struct {
		openid string
		id     int64
	}{{"A", a}, {"B", b}, {"C", c}, {"D", dd}} {
		if got := m3.ToInt64("app", s.openid); got != s.id {
			t.Errorf("清洗后重启 ID 漂移：%s 得到 %d，期望 %d", s.openid, got, s.id)
		}
	}
}

// 持久化 key 的编码必须按第一个分隔符切分，且排序稳定（确定性依赖它）。
func TestIDMapKeyEncoding(t *testing.T) {
	k := composeIDKey("12345", "ABCDEF")
	appID, openid, ok := splitIDKey(k)
	if !ok || appID != "12345" || openid != "ABCDEF" {
		t.Fatalf("编码往返失败：%q -> (%q,%q,%v)", k, appID, openid, ok)
	}
	if _, _, ok := splitIDKey("no-separator"); ok {
		t.Error("没有分隔符的 key 必须返回 ok=false")
	}
	keys := []string{composeIDKey("b", "2"), composeIDKey("a", "1")}
	sort.Strings(keys)
	if keys[0] != composeIDKey("a", "1") {
		t.Errorf("排序结果不符合预期：%v", keys)
	}
}
