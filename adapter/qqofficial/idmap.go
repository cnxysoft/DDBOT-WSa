package qqofficial

// 本文件：QQ 官方 openid <-> DDBOT 内部 int64 ID 的可逆映射。
//
// 为什么必须有映射表（而不是哈希）：
//   - DDBOT 内部群号/用户号是 int64（见 adapter/interface.go 的 GroupMessageEvent.GroupID 等），
//     而 QQ 官方只给字符串 openid；
//   - 撤回与引用回复要用**原始 msg_id / ref_idx 字符串**，哈希不可逆，映射必须能反查。
//
// 为什么 key 必须带 appID（实测结论，优先于官方文档）：
// openid 是 per-bot 的——同一个群，个人机器人看到 0F6E696A…，企业机器人看到
// B97B93A3…；拿 A 的 openid 去查 B 会返回 11255「请求的资源不存在」。
// 因此映射 key 一律是 (appID, openid)；ToOpenID 也只在 appID 匹配时返回结果，
// 避免换机器人后旧映射张冠李戴。
//
// 分配策略：单调自增的**全局** int64 计数器，从 1 开始，不回收、不复用。
// 全局（而不是 per-appID）是为了让「同一个 ID 不会分给两个不同 openid」这条
// 约束在内存里就能自证：rev 表是 id -> (appID, openid) 的一对一映射。

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// idKeySep 是持久化 key 里 appID 与 openid 的分隔符：ASCII US，不可打印。
// 正常 appID（数字）与 openid（十六进制串）都不会包含它，切分取**第一个**
// 分隔符，因此即使 openid 里出现该字节也能正确还原。
//
// 持久化编码：appID + idKeySep + openid（持久化 map 的 key 就是这个编码）。
const idKeySep = "\x1f"

// maxAllocID 是内部 int64 ID 的上界（math.MaxInt64，内联以免多引一个包）。
const maxAllocID int64 = 1<<63 - 1

var (
	// ErrNoPersister 表示 NewIDMap 拿到 nil Persister：映射仍可纯内存使用，
	// 但 Flush 无处落盘。
	ErrNoPersister = errors.New("qqofficial: IDMap 未注入 Persister")

	// ErrLoadFailed 表示初始化读取持久化数据失败。此时映射降级为
	// 「内存可用 / 存储只读」：Flush 直接返回该错误，并且**不**调用
	// Persister.Save —— 用一份不完整的映射覆盖旧数据会丢掉已有 ID，
	// 直接破坏「稳定分配」这条硬约束。
	ErrLoadFailed = errors.New("qqofficial: IDMap 持久化数据读取失败")
)

// Persister 是注入给 IDMap 的持久化后端。
//
// 本包不关心它怎么存（buntdb / JSON 文件 / 远端都行），adapter module 因此
// 不需要引入任何存储依赖；真实实现在调用方装配。
//
// Load 返回的 map：key 是本包的持久化编码（appID + idKeySep + openid），
// value 是分配给该 openid 的内部 int64 ID。
// Save 收到的是**完整快照**，实现应整体替换，而不是增量合并。
//
// 两者都可能被并发调用，实现自身需要保证安全。
type Persister interface {
	Load() (map[string]int64, error)
	Save(map[string]int64) error
}

// IDMap 是 (appID, openid) <-> int64 的双向映射，并发安全。
//
// 零值不可用，请用 NewIDMap 构造。
type IDMap struct {
	p Persister

	mu sync.RWMutex
	// fwd: 持久化编码 key -> id
	fwd map[string]int64
	// rev: id -> 持久化编码 key（全局一对一，保证 ID 不复用）
	rev map[int64]string
	// next 是下一个待分配的 ID。restore 之后它一定大于所有已加载的 ID，
	// 所以新分配的 ID 不会撞上持久化里的旧 ID。
	next int64

	// gen 每次分配自增；saved 记录最后一次成功 Save 覆盖到的 gen。
	// 用代数而不是 bool 是为了避免「快照生成后又有新分配，Save 成功后
	// 误把 dirty 清掉」这类竞态。
	gen   uint64
	saved uint64

	// loadErr 只在 NewIDMap 里写一次，之后只读：非 nil 表示存储只读降级。
	loadErr error
}

// NewIDMap 构造映射表，并立刻从 Persister 恢复既有映射。
//
// 签名里没有 error，所以 Load 失败不 panic、也不返回半成品：失败时映射以空表
// 起步（本次运行的分配照常可用），并把错误记下来供 Flush 报出（见 ErrLoadFailed）。
func NewIDMap(p Persister) *IDMap {
	m := &IDMap{
		p:    p,
		fwd:  make(map[string]int64),
		rev:  make(map[int64]string),
		next: 1,
	}
	if p == nil {
		m.loadErr = ErrNoPersister
		return m
	}
	raw, err := p.Load()
	if err != nil {
		m.loadErr = fmt.Errorf("%w: %v", ErrLoadFailed, err)
		return m
	}
	m.restore(raw)
	return m
}

// restore 把持久化的 key->id 装进内存，并算出下一个可用 ID。
//
// 对脏数据的处理原则：**确定性 + 不撞 ID**。
//   - 按 key 排序后处理，同一份持久化数据永远得到同一份内存状态；
//   - 非法 key（切不出 appID/openid）直接丢弃，但它的 ID 仍被 next 跳过，
//     不会被新分配的条目标复用；
//   - ID <= 0 或与别人重复的条目重新分配一个全新 ID —— 宁可让这一条不稳定，
//     也不能让两个不同 openid 共用一个 ID（那会让 DDBOT 侧把两个群当成一个）。
func (m *IDMap) restore(raw map[string]int64) {
	if len(raw) == 0 {
		return
	}
	keys := make([]string, 0, len(raw))
	maxSeen := int64(0)
	for k, v := range raw {
		keys = append(keys, k)
		if v > maxSeen {
			maxSeen = v
		}
	}
	sort.Strings(keys)

	next := int64(1)
	if maxSeen >= 1 && maxSeen < maxAllocID {
		next = maxSeen + 1
	}

	claimed := make(map[int64]bool, len(keys))
	// changed 记录「内存里的视图是否已经与磁盘上那份数据不同」：丢弃非法 key、
	// 给重复/越界 ID 重新分配，都会让内存和磁盘分叉，这时必须让下一次 Flush 真的
	// 写一次盘，把清洗结果落回去（否则脏数据会一直留在文件里）。
	changed := false
	for _, k := range keys {
		id := raw[k]
		appID, openid, ok := splitIDKey(k)
		if !ok || appID == "" || openid == "" {
			changed = true
			continue
		}
		if id <= 0 || claimed[id] {
			for claimed[next] {
				next++
				if next <= 0 { // 理论上的溢出保护
					next = 1
				}
			}
			id = next
			next++
			changed = true
		}
		claimed[id] = true
		m.fwd[k] = id
		m.rev[id] = k
	}
	if next >= 1 {
		m.next = next
	}
	if changed {
		m.gen++
	}
}

// ToInt64 返回 (appID, openid) 对应的内部 int64 ID。
//
// 稳定分配：同一个 (appID, openid) 永远得到同一个 ID，跨进程重启也保持
// （重启后从 Persister 恢复，见 restore）。ID 自增，从 1 开始。
//
// appID 或 openid 为空时返回 0，表示「无法映射」：既不为垃圾输入占用 ID，
// 也不让它污染持久化数据。（0 不会被合法分配出去，可作为哨兵值判断。）
//
// 查表与分配在同一把锁内完成（写锁内双检），因此并发调用是原子的，
// 不会给同一对 (appID, openid) 发出两个 ID。
func (m *IDMap) ToInt64(appID, openid string) int64 {
	if appID == "" || openid == "" {
		return 0
	}
	key := composeIDKey(appID, openid)

	m.mu.RLock()
	id, ok := m.fwd[key]
	m.mu.RUnlock()
	if ok {
		return id
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if id, ok = m.fwd[key]; ok { // check-then-act 必须在同一临界区内收口
		return id
	}
	for {
		if m.next <= 0 || m.next > maxAllocID {
			return 0 // ID 空间耗尽（理论上不可达）
		}
		if _, used := m.rev[m.next]; !used {
			break
		}
		m.next++
	}
	id = m.next
	m.next++
	m.fwd[key] = id
	m.rev[id] = key
	m.gen++
	return id
}

// ToOpenID 反查 openid。第二个返回值是「是否找到」。
//
// 只有当 id 确实属于该 appID 时才返回 true：拿 A 机器人的 ID 去问 B 会得到
// false，而不是错误地返回 A 的 openid（那正是实测里 11255 的坑）。
func (m *IDMap) ToOpenID(appID string, id int64) (string, bool) {
	if appID == "" || id <= 0 {
		return "", false
	}
	m.mu.RLock()
	key, ok := m.rev[id]
	m.mu.RUnlock()
	if !ok {
		return "", false
	}
	keyAppID, openid, ok := splitIDKey(key)
	if !ok || keyAppID != appID || openid == "" {
		return "", false
	}
	return openid, true
}

// Flush 把当前映射的完整快照交给 Persister.Save。
//
// 有没有新分配看 gen 与 saved 的代数：gen 没推进（也没有还原时的清洗）就直接返回，
// 不调用 Save —— 反复 Save 同一份快照虽然无害，但在这里就是无谓的磁盘写。
// Save 失败时错误原样返回，映射保持「待落盘」状态，下次 Flush 会重试。
//
// 存储降级（Load 失败或 nil Persister）时直接返回对应错误且不写：
// 见 ErrLoadFailed 的说明。
func (m *IDMap) Flush() error {
	if m.p == nil {
		return ErrNoPersister
	}
	if m.loadErr != nil {
		return m.loadErr
	}

	m.mu.RLock()
	gen := m.gen
	if gen <= m.saved {
		// 上一次落盘之后既没有新分配，也没有还原时的清洗动作（见 restore）：
		// 磁盘上那份就是当前这份，不必重写。
		//
		// 这条短路是「2 秒刷一次盘」的前提（见 adapter.go 的 stateFlushInterval 注释）：
		// 没有它，空闲的 tick 也会走一遍「读盘 + 序列化 + 临时文件 + fsync + 备份轮转」，
		// 双核小机上一天就是四万多次无谓写盘。
		m.mu.RUnlock()
		return nil
	}
	snapshot := make(map[string]int64, len(m.fwd))
	for k, v := range m.fwd {
		snapshot[k] = v
	}
	m.mu.RUnlock()

	if err := m.p.Save(snapshot); err != nil {
		return err
	}

	m.mu.Lock()
	if m.saved < gen {
		m.saved = gen
	}
	m.mu.Unlock()
	return nil
}

// composeIDKey 生成持久化 key。appID 不允许包含 idKeySep（正常是数字）。
func composeIDKey(appID, openid string) string {
	return appID + idKeySep + openid
}

// splitIDKey 按**第一个**分隔符切分持久化 key。
func splitIDKey(key string) (appID, openid string, ok bool) {
	i := strings.Index(key, idKeySep)
	if i < 0 {
		return "", "", false
	}
	return key[:i], key[i+len(idKeySep):], true
}
