package qqofficial

// 本文件：事件重复推送去重。
//
// 为什么需要：QQ 官方在断线重连/服务端重投时会重复投递同一个事件（实测同一
// AppID 的多个 WS 连接都会收到同一批消息，见 10-live-verification.md 验证 8），
// DDBOT 侧必须自己挡住重复，否则会重复回复、重复推送。
//
// 去重 key 用**原始 msg_id 字符串**（形如 ROBOT1.0_xxx，实测可到 ~200 字符），
// 不做哈希：一是哈希没必要，二是 msg_id 本身还要留给撤回/引用用。
//
// 窗口语义（固定窗口，从首次出现算起）：
//   - 首次出现的 key -> Seen 返回 false；
//   - 距首次出现不足 window 再出现 -> 返回 true（重复）；
//   - 距首次出现已达 window（半开区间 [t0, t0+window)，边界按过期算）
//     -> 返回 false，并按一次新的首次出现重新计时。
//
// 之所以用固定窗口而不是滑动窗口：msg_id 是一次性事件的标识，重投只会发生在
// 有限的重连退避窗口内；固定窗口让「多久之后可以再收到同一个 key」可预测，
// 不会因为反复重投把一条记录无限续命。
//
// 淘汰策略：容量上限 + 最旧优先（FIFO，按首次出现时间）。
// 过期条目在每次 Seen 时从队首顺带清理，因此容量压力通常轮不到淘汰。

import (
	"container/list"
	"sync"
	"time"
)

const (
	// DefaultDedupCapacity 是容量上限的默认值。超容量时淘汰最旧的记录。
	DefaultDedupCapacity = 10000
	// DefaultDedupWindow 是去重窗口的默认值，要能覆盖官方断线重连的退避周期。
	DefaultDedupWindow = 5 * time.Minute
)

// Dedup 是并发安全的固定窗口去重器，零值不可用，请用 NewDedup 构造。
type Dedup struct {
	capacity int
	window   time.Duration

	// now 可在测试里替换成受控时钟；只在持有 mu 时读取。
	now func() time.Time

	mu      sync.Mutex
	order   *list.List // 元素为 *dedupEntry，按 at 升序（队首最旧）
	entries map[string]*list.Element
}

// dedupEntry 是队列元素，也是 map 的值。
type dedupEntry struct {
	key string
	at  time.Time // 当前这一轮窗口的起点（首次出现时间）
}

// NewDedup 构造去重器。capacity / window 非正时回落到默认值
// （DefaultDedupCapacity / DefaultDedupWindow），不会 panic。
func NewDedup(capacity int, window time.Duration) *Dedup {
	if capacity <= 0 {
		capacity = DefaultDedupCapacity
	}
	if window <= 0 {
		window = DefaultDedupWindow
	}
	return &Dedup{
		capacity: capacity,
		window:   window,
		now:      time.Now,
		order:    list.New(),
		entries:  make(map[string]*list.Element),
	}
}

// Seen 判断 key 是否在窗口内重复出现。
//
// 首次出现返回 false，窗口内的重复返回 true；窗口外再次出现按首次处理，
// 返回 false 并重新计时。
//
// key 一律按普通字符串处理（空串、200 字符的 msg_id 都合法，不会 panic）。
// 判定与登记在同一把锁内完成，并发调用下同一个 key 只会有一个 false。
func (d *Dedup) Seen(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.seenLocked(key)
}

func (d *Dedup) seenLocked(key string) bool {
	now := d.now()

	if el, ok := d.entries[key]; ok {
		e := el.Value.(*dedupEntry)
		if now.Sub(e.at) < d.window {
			return true // 窗口内重复：不刷新计时，也不改变淘汰顺序
		}
		// 窗口已过：当作一次全新的首次出现。
		// 必须先刷新并挪到队尾，再清队首过期条目——顺序反了会把这条刚
		// 刷新的自己当成过期项剪掉，元素被移出链表而 map 里还留着引用。
		e.at = now
		d.order.MoveToBack(el)
		d.pruneFrontLocked(now)
		d.enforceCapacityLocked()
		return false
	}

	d.pruneFrontLocked(now)
	e := &dedupEntry{key: key, at: now}
	d.entries[key] = d.order.PushBack(e)
	d.enforceCapacityLocked()
	return false
}

// pruneFrontLocked 从队首摘掉所有已过期条目。order 按 at 升序排列，
// 所以队首就是最旧的一条；遇到未过期的即可停止。
func (d *Dedup) pruneFrontLocked(now time.Time) {
	for {
		front := d.order.Front()
		if front == nil {
			return
		}
		e := front.Value.(*dedupEntry)
		if now.Sub(e.at) < d.window {
			return
		}
		d.order.Remove(front)
		delete(d.entries, e.key)
	}
}

// enforceCapacityLocked 淘汰最旧的条目直到不超过容量上限。
func (d *Dedup) enforceCapacityLocked() {
	for d.order.Len() > d.capacity {
		front := d.order.Front()
		if front == nil {
			return
		}
		d.order.Remove(front)
		delete(d.entries, front.Value.(*dedupEntry).key)
	}
}
