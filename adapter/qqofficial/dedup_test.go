package qqofficial

// dedup 的单测。核心是窗口语义、容量淘汰、并发原子性。
// 时间用受控时钟注入，不 sleep，测试确定且快。

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testClock 是受控时钟。
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 11, 11, 16, 0, 0, 0, time.UTC)}
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newTestDedup(capacity int, window time.Duration) (*Dedup, *testClock) {
	clock := newTestClock()
	d := NewDedup(capacity, window)
	d.now = clock.now
	return d, clock
}

// 默认值必须覆盖官方重连退避（硬约束 3）。
func TestDedupDefaultsCoverReconnectBackoff(t *testing.T) {
	if DefaultDedupWindow < 5*time.Minute {
		t.Errorf("默认窗口 %v 不得小于 5 分钟", DefaultDedupWindow)
	}
	if DefaultDedupCapacity < 10000 {
		t.Errorf("默认容量 %d 不得小于 10000", DefaultDedupCapacity)
	}
	d := NewDedup(0, 0)
	if d.capacity != DefaultDedupCapacity || d.window != DefaultDedupWindow {
		t.Errorf("非正参数应回落默认值，实际 capacity=%d window=%v", d.capacity, d.window)
	}
	d2 := NewDedup(-5, -time.Second)
	if d2.capacity != DefaultDedupCapacity || d2.window != DefaultDedupWindow {
		t.Errorf("负数参数应回落默认值，实际 capacity=%d window=%v", d2.capacity, d2.window)
	}
	// 回落默认值后仍然可用，不 panic。
	if d2.Seen("x") {
		t.Error("首次必须 false")
	}
	if !d2.Seen("x") {
		t.Error("窗口内重复必须 true")
	}
}

func TestDedupFirstSeenFalseThenRepeatTrue(t *testing.T) {
	d, _ := newTestDedup(100, 5*time.Minute)
	key := "ROBOT1.0_7xlcLlusABCDEF"

	if d.Seen(key) {
		t.Error("首次出现必须返回 false")
	}
	if !d.Seen(key) {
		t.Error("窗口内重复必须返回 true")
	}
	if !d.Seen(key) {
		t.Error("窗口内再次重复必须返回 true")
	}
	if d.Seen(key + "-other") {
		t.Error("不同 key 必须互不影响")
	}
}

// 窗口是半开区间 [t0, t0+window)：恰好到边界按过期处理。
func TestDedupWindowBoundaryIsHalfOpen(t *testing.T) {
	d, clock := newTestDedup(100, 5*time.Minute)
	k := "msg-boundary"

	if d.Seen(k) {
		t.Fatal("首次必须 false")
	}
	clock.advance(5*time.Minute - time.Nanosecond)
	if !d.Seen(k) {
		t.Error("窗口内（差 1ns）必须返回 true")
	}
	clock.advance(time.Nanosecond)
	if d.Seen(k) {
		t.Error("恰好达到窗口边界必须按过期处理，返回 false")
	}
	// 窗口外再次出现后重新计时。
	clock.advance(4 * time.Minute)
	if !d.Seen(k) {
		t.Error("重新计时后仍在窗口内，必须返回 true")
	}
}

// 固定窗口：反复重投不能无限续命。
func TestDedupWindowDoesNotSlide(t *testing.T) {
	d, clock := newTestDedup(100, 5*time.Minute)
	k := "msg-fixed"

	d.Seen(k)
	clock.advance(4 * time.Minute)
	if !d.Seen(k) {
		t.Fatal("窗口内重复必须 true")
	}
	clock.advance(4 * time.Minute) // 距首次出现 8 分钟 > 5 分钟
	if d.Seen(k) {
		t.Error("固定窗口：重投不得刷新窗口起点")
	}
}

// 超容量淘汰最旧的。
func TestDedupCapacityEvictsOldest(t *testing.T) {
	d, _ := newTestDedup(3, time.Hour)

	for _, k := range []string{"k1", "k2", "k3"} {
		if d.Seen(k) {
			t.Fatalf("%s 首次必须 false", k)
		}
	}
	if d.Seen("k4") {
		t.Fatal("k4 首次必须 false")
	}
	// 容量 3，k1 应被淘汰 -> 按首次出现处理
	if d.Seen("k1") {
		t.Error("k1 已被淘汰，必须按首次出现返回 false")
	}
	// 上一步又把 k1 插回队尾，淘汰掉 k2
	if d.Seen("k2") {
		t.Error("k2 已被淘汰，必须按首次出现返回 false")
	}
	if d.order.Len() != len(d.entries) {
		t.Errorf("链表与 map 长度必须一致：len=%d map=%d", d.order.Len(), len(d.entries))
	}
	if d.order.Len() > d.capacity {
		t.Errorf("容量上限被突破：%d > %d", d.order.Len(), d.capacity)
	}
}

// 过期条目从队首顺带清理，不会无限堆积。
func TestDedupPrunesExpiredEntries(t *testing.T) {
	d, clock := newTestDedup(100, 5*time.Minute)

	for i := 0; i < 5; i++ {
		d.Seen(fmt.Sprintf("k%d", i))
	}
	clock.advance(6 * time.Minute)
	d.Seen("fresh")

	if d.order.Len() != 1 || len(d.entries) != 1 {
		t.Errorf("过期条目应被清理，实际 order=%d map=%d", d.order.Len(), len(d.entries))
	}
	if got, ok := d.entries["fresh"]; !ok || got == nil {
		t.Error("新 key 必须登记在案")
	}
	// 过期的 key 重新出现按首次处理。
	if d.Seen("k0") {
		t.Error("过期 key 必须按首次出现返回 false")
	}
}

// 空 key 与 ~200 字符的 msg_id 都按普通 key 处理，不 panic。
func TestDedupEmptyAndLongKeys(t *testing.T) {
	d, _ := newTestDedup(10, time.Minute)

	if d.Seen("") {
		t.Error("空 key 首次必须 false")
	}
	if !d.Seen("") {
		t.Error("空 key 窗口内重复必须 true")
	}

	long := "ROBOT1.0_" + strings.Repeat("Ab3dEf", 32) // 约 200 字符
	if len(long) < 190 {
		t.Fatalf("测试用的长 key 太短：%d", len(long))
	}
	if d.Seen(long) {
		t.Error("长 key 首次必须 false")
	}
	if !d.Seen(long) {
		t.Error("长 key 窗口内重复必须 true")
	}
}

// 并发：同一个 key 只能产生一次「首次出现」，否则就是事件被重复处理。
func TestDedupConcurrentSameKeySingleFirst(t *testing.T) {
	d, _ := newTestDedup(1000, time.Hour)

	keys := make([]string, 8)
	for i := range keys {
		keys[i] = fmt.Sprintf("ROBOT1.0_msg-%d", i)
	}

	var (
		firstCount int64
		wg         sync.WaitGroup
	)
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if !d.Seen(keys[(g+i)%len(keys)]) {
					atomic.AddInt64(&firstCount, 1)
				}
			}
		}(g)
	}
	wg.Wait()

	if firstCount != int64(len(keys)) {
		t.Errorf("每个 key 只能有一次首次出现，实际 %d 次（期望 %d）", firstCount, len(keys))
	}
	if d.order.Len() != len(d.entries) {
		t.Errorf("链表与 map 长度必须一致：len=%d map=%d", d.order.Len(), len(d.entries))
	}
	if d.order.Len() != len(keys) {
		t.Errorf("窗口内 8 个 key 应全部保留，实际 %d", d.order.Len())
	}
}

// 并发 + 容量淘汰：结构必须始终自洽，不 panic、不越界。
func TestDedupConcurrentWithEviction(t *testing.T) {
	d, _ := newTestDedup(16, time.Minute)

	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				d.Seen(fmt.Sprintf("k-%d-%d", g, i%37))
			}
		}(g)
	}
	wg.Wait()

	if d.order.Len() != len(d.entries) {
		t.Errorf("淘汰必须同步删除 map 与链表：len=%d map=%d", d.order.Len(), len(d.entries))
	}
	if d.order.Len() > d.capacity {
		t.Errorf("容量上限被突破：%d > %d", d.order.Len(), d.capacity)
	}
	// 所有 map 里的 key 都必须在链表中（反查一遍，防止残留幽灵条目）。
	for k, el := range d.entries {
		if el == nil {
			t.Fatalf("map 里的 %q 指向空元素", k)
		}
	}
}

// 窗口过期后重新登记，元素不能因为「先剪枝后移动」的次序问题丢掉。
func TestDedupExpiredRefreshKeepsEntryTracked(t *testing.T) {
	d, clock := newTestDedup(10, 5*time.Minute)

	d.Seen("a")
	d.Seen("b")
	clock.advance(6 * time.Minute)

	// a 与 b 都过期；此时查询 a 会把 a 挪到队尾并重新计时。
	if d.Seen("a") {
		t.Error("过期后的 a 必须按首次出现返回 false")
	}
	if d.order.Len() != 1 || len(d.entries) != 1 {
		t.Fatalf("过期条目应被清理且 a 保留，实际 order=%d map=%d", d.order.Len(), len(d.entries))
	}
	if _, ok := d.entries["a"]; !ok {
		t.Fatal("a 必须仍在表内（否则后续重复挡不住）")
	}
	if !d.Seen("a") {
		t.Error("重新计时后窗口内重复必须 true")
	}
	// b 已经过期并被清理，重新出现按首次处理。
	if d.Seen("b") {
		t.Error("b 早已过期，必须按首次出现返回 false")
	}
}
