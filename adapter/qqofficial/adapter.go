package qqofficial

// 本文件实现 adapter.Adapter 的全部 35 个方法。
//
// 分档原则（报告里有逐方法表格）：
//   - **真实现**：连接/事件/发送/撤回/取消息/下载文件/群列表/群信息/禁言/踢人 —— 官方有对应接口，
//     或者能靠事件自建（官方**没有**「列出我加入的群」接口，群清单只能靠 GROUP_ADD_ROBOT /
//     GROUP_DEL_ROBOT 事件 + 落盘维护）；
//   - **降级**：群成员表/好友表 —— 官方接口拿不到（成员接口实测 11253），只能用事件里的
//     author.member_role / author.username 拼一份「见过的成员」，返回空表也**必须**给 nil error；
//   - **明确不支持**：合并转发、拍一拍、全员禁言、改名片/头衔、群成员详情查询 —— 返回固定错误，
//     绝不返回 nil/零值假装成功。
//
// 两条容易踩死启动流程的硬约束（都有仓库侧证据）：
//   1. lsp/module.go:1020-1050 会死等 Messenger.IsConnected() 与 IsListLoaded()，而
//      IsListLoaded 只在 GetFriendList + GetGroupList + 每个群的 GetGroupMemberList **全部无错**
//      之后才置位（adapter/messenger.go:1192-1252）。所以这三个方法在任何情况下都不许返回 error，
//      官方接口拿不到就给空表 + nil。
//   2. 发送要「被动优先」，而且有**两条**被动通道：
//      - msg_id：官方被动回复的老路（群 5 分钟/5 次、单聊 60 分钟/4 次），而 adapter 的
//        SendGroupMessage 签名里没有 msg_id，所以这里给每个目标记一个「最近一条可被动回复的
//        消息」锚点，发送时自动带上；窗口/次数由 message.go 的账本兜底转主动。
//      - event_id：事件**帧顶层**的 id（实测 GROUP_ADD_ROBOT 有），在 allow_proactive_msg=false
//        的群里是唯一还能发话的通道。目前只有进群自我介绍用它（tip 与长文案都由
//        SendJoinIntro 在**同一次调用**里发，顺序才能保证），普通发送路径**不碰**它；
//        有效期与次数上限未实测，所以不套用上面那套账本。

import (
	"container/list"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

const (
	// AdapterName 是注册到 DDBOT 的 adapter 名。
	AdapterName = "qqofficial"

	// DefaultMsgRegistryCapacity 是「消息 id 登记表」的容量上限。
	// 官方 msg_id 是长字符串（ROBOT1.0_…），而 DDBOT 接口层用 int32，
	// 所以要用一张表做双向映射；表满了淘汰最旧的，被淘汰的消息只是没法再撤回/查详情。
	DefaultMsgRegistryCapacity = 4096

	// heartbeatInterval 是向上层汇报在线状态的心跳间隔。
	heartbeatInterval = 30 * time.Second
	// stateFlushInterval 是状态文件的定期落盘间隔（新映射/新群都会等这个周期）。
	//
	// 2 秒（原来是 60 秒）。这个文件里放着 (appID, openid) -> 内部 ID 的映射，而内部 ID
	// 是 lsp 侧群权限与订阅的唯一凭据：进程被 kill -9 / OOM / 断电打断时，只写过内存的
	// 映射就没了，下次启动按文件重新分配，这些群会拿到与 .lsp.db 里记的不一样的编号，
	// 旧权限与订阅于是静默指向别的群。所以这个间隔实际上限定了「崩溃最多丢多久的新分配」，
	// 60 秒意味着一段正常聊天之后被打断，今天新认识的群与用户全部要重排。
	//
	// 为什么 2 秒不心疼：Flush 在没有新分配时直接返回（见 idmap.go 的 Flush），空闲 tick
	// 只是一次加锁比较，不碰磁盘；真写盘的频率由「新分配的推进速度」决定，而新分配只在
	// 遇到新的群/用户时发生。2 秒是实测体感足够短、又不需要为每个事件单独写盘的取值：
	// 再短只是让 tick 更密，写盘次数不会变多，日志与调度开销反而是纯亏。
	stateFlushInterval = 2 * time.Second
	// groupInfoCooldown 是「群信息接口无权限」之后的冷却期：实测 /info 对个人机器人可用，
	// 但文档把它标成 11253 白名单，所以拿到权限错误就退避，别死磕。
	groupInfoCooldown = 30 * time.Minute
	// downloadTimeout 是 DownloadFile 的超时。
	downloadTimeout = 5 * time.Minute
	// maxDownloadBytes 是 DownloadFile 的体积上限。
	maxDownloadBytes = 200 << 20
	// stateFlushLogInterval 是「同一类落盘失败」日志的最小间隔。
	//
	// 为什么需要：stateLoop 每 2 秒跑一次，而 IDMap 一旦因状态文件读取失败进入只读降级，
	// Flush 就是**永久**失败 —— 不设间隔就是每 2 秒一条 Warn，一天四万多条，
	// 真出事时真正有用的日志全被它挤没了。限流**不吞失败**：首条立即打，之后每分钟
	// 最多一条，并且这一条里带上期间被压掉的条数。
	stateFlushLogInterval = time.Minute
	// downloadDirName 是下载文件落地的子目录名。
	downloadDirName = "ddbot-qqofficial"
	// botNameRetryAttempts 是启动时拉 GET /users/@me 的总尝试次数（含第一次）。
	botNameRetryAttempts = 3
	// botNameRetryBackoff 是两次尝试之间的间隔。只影响模板里的 bot_name，
	// 失败就留空——绝不拖住启动，也绝不 panic。
	botNameRetryBackoff = 2 * time.Second
)

// ErrUnsupported 表示该能力官方开放平台没有对应接口（不是「没实现」）。
var ErrUnsupported = errors.New("qqofficial: 官方开放平台未提供该能力")

func errUnsupported(method string) error {
	return fmt.Errorf("%w：%s", ErrUnsupported, method)
}

// ---------------------------------------------------------------- 消息登记表

// msgRecord 是一条消息的本地记录。
//
// 存在的理由：DDBOT 接口层的 message id 是 int32，而官方 msg_id 是长字符串
// （实测 ROBOT1.0_… 约 200 字符）——撤回、查详情、被动回复都要那个原始字符串，
// 哈希不可逆，所以只能登记映射。
type msgRecord struct {
	ID       int32
	RawID    string // 原始 msg_id（被动回复/撤回用）
	RefIdx   string // 引用索引（REFIDX_ 形态，用来引用这条消息）
	Kind     TargetKind
	OpenID   string // 群 openid 或用户 openid
	GroupID  int64
	UserID   int64
	Content  string
	Username string
	Outgoing bool
	Passive  bool
	Time     int64
}

type msgRegistry struct {
	mu       sync.Mutex
	byID     map[int32]*msgRecord
	byRaw    map[string]*msgRecord
	byRef    map[string]*msgRecord
	order    *list.List
	next     int32
	capacity int
}

func newMsgRegistry(capacity int) *msgRegistry {
	if capacity <= 0 {
		capacity = DefaultMsgRegistryCapacity
	}
	return &msgRegistry{
		byID:     make(map[int32]*msgRecord),
		byRaw:    make(map[string]*msgRecord),
		byRef:    make(map[string]*msgRecord),
		order:    list.New(),
		capacity: capacity,
	}
}

// register 登记一条消息并返回它的内部 int32 id。同一个 RawID 重复登记返回同一个 id。
func (r *msgRegistry) register(rec *msgRecord) int32 {
	if r == nil || rec == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if rec.RawID != "" {
		if existing, ok := r.byRaw[rec.RawID]; ok {
			// 同一条消息（比如自己发的消息又以全量事件回来）只保留一个 id
			if existing.RefIdx == "" {
				existing.RefIdx = rec.RefIdx
			}
			if rec.Content != "" {
				existing.Content = rec.Content
			}
			return existing.ID
		}
	}

	// 找一个没被占用的 id（正常情况一次就中；极端情况下有界重试）
	for attempts := 0; attempts < 1024; attempts++ {
		r.next++
		if r.next <= 0 {
			r.next = 1
		}
		if _, used := r.byID[r.next]; !used {
			break
		}
	}
	if _, used := r.byID[r.next]; used {
		logger.Errorf("%s: 消息登记表 id 分配失败，放弃登记", ConfigKeyPrefix)
		return 0
	}

	rec.ID = r.next
	r.byID[rec.ID] = rec
	r.order.PushBack(rec)
	if rec.RawID != "" {
		r.byRaw[rec.RawID] = rec
	}
	if rec.RefIdx != "" {
		r.byRef[rec.RefIdx] = rec
	}
	r.evictLocked()
	return rec.ID
}

func (r *msgRegistry) evictLocked() {
	for r.order.Len() > r.capacity {
		front := r.order.Front()
		if front == nil {
			return
		}
		rec := front.Value.(*msgRecord)
		r.order.Remove(front)
		delete(r.byID, rec.ID)
		if rec.RawID != "" {
			if cur, ok := r.byRaw[rec.RawID]; ok && cur == rec {
				delete(r.byRaw, rec.RawID)
			}
		}
		if rec.RefIdx != "" {
			if cur, ok := r.byRef[rec.RefIdx]; ok && cur == rec {
				delete(r.byRef, rec.RefIdx)
			}
		}
	}
}

func (r *msgRegistry) get(id int32) (*msgRecord, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.byID[id]
	return rec, ok
}

func (r *msgRegistry) byRefIdx(ref string) (*msgRecord, bool) {
	if r == nil || ref == "" {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.byRef[ref]
	return rec, ok
}

func (r *msgRegistry) len() int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.order.Len()
}

// ---------------------------------------------------------------- 成员缓存同步

// 成员缓存同步的节奏参数。
const (
	// memberSyncDebounce 是防抖窗口：同一群在这个窗口内的多次变化只通知一次。
	memberSyncDebounce = 2 * time.Second
	// memberSyncMinGap 是同一群两次通知之间的最小间隔。
	memberSyncMinGap = 5 * time.Second
	// memberSyncMaxPerMinute 是全局每分钟的通知上限，超了就推迟一轮。
	memberSyncMaxPerMinute = 12
)

// memberSyncNotifier 把「学到新成员/角色变化」合并成少量「成员缓存失效」通知。
//
// 防抖：从第一次变化起 memberSyncDebounce 后才通知，期间的新变化**不会**把时间往后推；
// 限流：同一群两次通知至少隔 memberSyncMinGap，全局每分钟最多 memberSyncMaxPerMinute 次，
// 超过就推迟一轮并打日志。
type memberSyncNotifier struct {
	debounce     time.Duration
	minGap       time.Duration
	maxPerMinute int

	mu      sync.Mutex
	due     map[int64]time.Time
	last    map[int64]time.Time
	hist    []time.Time
	wake    chan struct{}
	sink    func(int64)
	limited bool
}

func newMemberSyncNotifier(sink func(int64), debounce, minGap time.Duration, maxPerMinute int) *memberSyncNotifier {
	if debounce <= 0 {
		debounce = time.Second
	}
	if minGap < 0 {
		minGap = 0
	}
	if maxPerMinute <= 0 {
		maxPerMinute = 1
	}
	return &memberSyncNotifier{
		debounce:     debounce,
		minGap:       minGap,
		maxPerMinute: maxPerMinute,
		due:          make(map[int64]time.Time),
		last:         make(map[int64]time.Time),
		wake:         make(chan struct{}, 1),
		sink:         sink,
	}
}

// mark 记下某群需要同步；可重复调用，会被合并。
func (n *memberSyncNotifier) mark(groupID int64) {
	if n == nil || groupID == 0 {
		return
	}
	now := time.Now()
	n.mu.Lock()
	at, ok := n.due[groupID]
	if !ok {
		at = now.Add(n.debounce)
	}
	if last, ok := n.last[groupID]; ok {
		if earliest := last.Add(n.minGap); earliest.After(at) {
			at = earliest
		}
	}
	n.due[groupID] = at
	n.mu.Unlock()
	select {
	case n.wake <- struct{}{}:
	default:
	}
}

// run 是通知循环，随 adapter 的 ctx 退出。
func (n *memberSyncNotifier) run(ctx context.Context) {
	if n == nil {
		return
	}
	for {
		now := time.Now()
		n.mu.Lock()
		for len(n.hist) > 0 && now.Sub(n.hist[0]) > time.Minute {
			n.hist = n.hist[1:]
		}
		if len(n.hist) < n.maxPerMinute {
			n.limited = false
		}
		var due []int64
		next := time.Time{}
		for g, at := range n.due {
			if at.After(now) {
				if next.IsZero() || at.Before(next) {
					next = at
				}
				continue
			}
			if len(n.hist)+len(due) >= n.maxPerMinute {
				// 超频：把这一群推迟一轮，别把上层的刷新刷爆
				n.due[g] = now.Add(n.minGap)
				if !n.limited {
					n.limited = true
					logger.Warnf("%s: 成员缓存同步达到每分钟 %d 次上限，已推迟后续通知",
						ConfigKeyPrefix, n.maxPerMinute)
				}
				if next.IsZero() || n.due[g].Before(next) {
					next = n.due[g]
				}
				continue
			}
			due = append(due, g)
			n.hist = append(n.hist, now)
			n.last[g] = now
			delete(n.due, g)
		}
		n.mu.Unlock()

		for _, g := range due {
			if n.sink != nil {
				n.sink(g)
			}
		}

		wait := 5 * time.Second
		if !next.IsZero() {
			if d := time.Until(next); d > 0 && d < wait {
				wait = d
			}
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-n.wake:
		case <-timer.C:
		}
		timer.Stop()
	}
}

// SetMemberListSink 接收 messenger 的成员表失效通道（实现 adapter.MemberListSinkSetter）。
//
// 只有「靠事件自学成员」的 adapter 需要它；OneBot/satori 有真实成员接口，不实现本方法，
// NewMessenger 也不会注入，因此它们的行为完全不变。
func (a *QQOfficialAdapter) SetMemberListSink(sink adapter.MemberListSink) {
	if a == nil {
		return
	}
	a.memberSinkMu.Lock()
	a.memberSink = sink
	a.memberSinkMu.Unlock()
}

func (a *QQOfficialAdapter) notifyMemberListStale(groupID int64) {
	if a == nil || groupID == 0 {
		return
	}
	a.memberSinkMu.Lock()
	sink := a.memberSink
	a.memberSinkMu.Unlock()
	if sink == nil {
		return
	}
	sink.InvalidateGroupMembers(groupID)
}

// syncGroupMember 决定这次「学到东西」要不要立刻同步上层成员缓存。
//
// 判定**按人**（不是按群）：权限判定（lsp/permission/stateManager.go:199-218）读的是
// messenger 的 group.Members 缓存，而命令就在这条消息的处理流程里执行 —— 只要发起者本人
// 还不在已同步快照里（或他的角色变了），就必须**同步**刷一次，否则：
//   - 群里第 1 个发言的人能过，第 2、3… 个人各自的第一条命令都会被 nil member info 拒掉（实测）；
//   - 刚被升成管理员的人要等下一次发言才生效。
//
// 同步刷新是纯内存操作（adapter 的成员表在内存里，不走网络），微秒级，不会拖慢事件处理。
//
// 只是昵称变了（人、角色都没动）的情况不走这条路：交给防抖 + 限流，别占用命令路径。
func (a *QQOfficialAdapter) syncGroupMember(groupID, memberID int64, role string) {
	if a == nil || groupID == 0 || memberID == 0 {
		return
	}
	a.memberSinkMu.Lock()
	synced := a.memberSynced[groupID]
	if synced == nil {
		synced = make(map[int64]string, 4)
		a.memberSynced[groupID] = synced
	}
	prev, seen := synced[memberID]
	if !seen || prev != role {
		synced[memberID] = role
		a.memberSinkMu.Unlock()
		a.notifyMemberListStale(groupID)
		return
	}
	a.memberSinkMu.Unlock()

	if a.memberSync != nil {
		a.memberSync.mark(groupID)
	}
}

// ---------------------------------------------------------------- 群清单/成员

type memberRecord struct {
	OpenID   string
	ID       int64
	Nickname string
	Role     string
	LastSeen int64
}

type groupRecord struct {
	OpenID         string
	ID             int64
	Name           string
	MemberCount    int
	MaxMemberCount int
	AddedAt        int64
	InfoKnown      bool
	members        map[string]*memberRecord

	// memberCount 是快照时刻的成员表长度，只由 snapshot() 在锁内填。
	// 出锁之后要判「这张成员表空不空」只能用这个数：直接 len(members) 会和
	// learnMember 的写入撞上（map 是引用类型，浅拷贝只复制了表头）。
	memberCount int
}

// groupRegistry 是「机器人所在群」的自建清单。
//
// 官方没有「列出我加入的群」接口（接口索引里就没有），所以只能：
//   - GROUP_ADD_ROBOT / GROUP_DEL_ROBOT 事件维护增删；
//   - 收到群消息时把群也学进来（历史群在接入后第一次有人说话就能认出来）；
//   - 落盘，重启后仍然认得（不落盘的话 lsp 启动会等到群列表为空也算加载完）。
type groupRegistry struct {
	mu       sync.RWMutex
	byID     map[int64]*groupRecord
	byOpenID map[string]*groupRecord
	ids      *IDMap
	appID    string
	store    *JSONStore
	dirty    bool
	onNew    func(openid string)
}

func newGroupRegistry(ids *IDMap, appID string, store *JSONStore, onNew func(string)) *groupRegistry {
	return &groupRegistry{
		byID:     make(map[int64]*groupRecord),
		byOpenID: make(map[string]*groupRecord),
		ids:      ids,
		appID:    appID,
		store:    store,
		onNew:    onNew,
	}
}

// ensure 返回群的内部 id，顺带把新群登记进来（新群会触发一次背景取群信息）。
func (g *groupRegistry) ensure(openid string) int64 {
	if g == nil || openid == "" {
		return 0
	}
	g.mu.Lock()
	if rec, ok := g.byOpenID[openid]; ok {
		id := rec.ID
		g.mu.Unlock()
		return id
	}
	var id int64
	if g.ids != nil {
		id = g.ids.ToInt64(g.appID, openid)
	}
	rec := &groupRecord{
		OpenID:  openid,
		ID:      id,
		AddedAt: time.Now().Unix(),
		members: make(map[string]*memberRecord),
	}
	g.byOpenID[openid] = rec
	g.byID[id] = rec
	g.dirty = true
	g.mu.Unlock()

	if g.onNew != nil {
		g.onNew(openid)
	}
	return id
}

func (g *groupRegistry) remove(openid string) {
	if g == nil || openid == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.byOpenID[openid]
	if !ok {
		return
	}
	delete(g.byOpenID, openid)
	delete(g.byID, rec.ID)
	g.dirty = true
}

func (g *groupRegistry) get(groupID int64) (*groupRecord, bool) {
	if g == nil {
		return nil, false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	rec, ok := g.byID[groupID]
	if !ok {
		return nil, false
	}
	cp := *rec
	return &cp, true
}

func (g *groupRegistry) getByOpenID(openid string) (*groupRecord, bool) {
	if g == nil {
		return nil, false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	rec, ok := g.byOpenID[openid]
	if !ok {
		return nil, false
	}
	cp := *rec
	return &cp, true
}

func (g *groupRegistry) snapshot() []groupRecord {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]groupRecord, 0, len(g.byID))
	for _, rec := range g.byID {
		cp := *rec
		// members 是 map，浅拷贝只复制表头：长度必须在锁内取，出锁后再 len() 就是数据竞争。
		cp.memberCount = len(rec.members)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// recordInfo 记下从 /v2/groups/{openid}/info 取到的群信息。
func (g *groupRegistry) recordInfo(openid, name string, memberCount, maxMemberCount int) {
	if g == nil || openid == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.byOpenID[openid]
	if !ok {
		return
	}
	if name != "" {
		rec.Name = name
	}
	if memberCount > 0 {
		rec.MemberCount = memberCount
	}
	if maxMemberCount > 0 {
		rec.MaxMemberCount = maxMemberCount
	}
	rec.InfoKnown = true
	g.dirty = true
}

// learnMember 从事件里学一个群成员（官方群成员接口实测 11253，只能用事件拼）。
//
// 返回 (成员内部 id, 角色, 是否有新增或角色变化)。第三个值给上层判定「要不要立刻同步
// 上层缓存」：新增成员与角色变化都要（否则刚升管理员的人得等下一次发言才生效）。
// 昵称变化不计（不影响权限，也免得群里聊天刷屏时反复刷新）。
func (g *groupRegistry) learnMember(groupOpenID string, author wsAuthor, ids *IDMap, appID string) (int64, string, bool) {
	if g == nil || groupOpenID == "" {
		return 0, "", false
	}
	ident := author.identity()
	if ident == "" {
		return 0, "", false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	rec, ok := g.byOpenID[groupOpenID]
	if !ok {
		return 0, "", false
	}
	if rec.members == nil {
		rec.members = make(map[string]*memberRecord)
	}
	var id int64
	if ids != nil {
		id = ids.ToInt64(appID, ident)
	}
	changed := false
	m := rec.members[ident]
	if m == nil {
		m = &memberRecord{OpenID: ident}
		rec.members[ident] = m
		changed = true
	}
	if m.ID != id {
		m.ID = id
		changed = true
	}
	if author.Username != "" {
		m.Nickname = author.Username
	}
	if author.MemberRole != "" && m.Role != author.MemberRole {
		m.Role = author.MemberRole
		changed = true
	}
	m.LastSeen = time.Now().Unix()
	return m.ID, m.Role, changed
}

func (g *groupRegistry) members(groupID int64) []*memberRecord {
	if g == nil {
		return nil
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	rec, ok := g.byID[groupID]
	if !ok {
		return nil
	}
	out := make([]*memberRecord, 0, len(rec.members))
	for _, m := range rec.members {
		cp := *m
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (g *groupRegistry) member(groupID, userID int64) (*memberRecord, bool) {
	if g == nil {
		return nil, false
	}
	g.mu.RLock()
	defer g.mu.RUnlock()
	rec, ok := g.byID[groupID]
	if !ok {
		return nil, false
	}
	for _, m := range rec.members {
		if m.ID == userID {
			cp := *m
			return &cp, true
		}
	}
	return nil, false
}

// load 从状态文件恢复群清单（顺带把这些群的内部 id 固定下来）。
func (g *groupRegistry) load() error {
	if g == nil || g.store == nil {
		return nil
	}
	recs, err := g.store.LoadGroups()
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, r := range recs {
		if r.OpenID == "" {
			continue
		}
		var id int64
		if g.ids != nil {
			id = g.ids.ToInt64(g.appID, r.OpenID)
		}
		g.byOpenID[r.OpenID] = &groupRecord{
			OpenID:         r.OpenID,
			ID:             id,
			Name:           r.Name,
			MemberCount:    r.MemberCount,
			MaxMemberCount: r.MaxMemberCount,
			AddedAt:        r.AddedAt,
			InfoKnown:      r.InfoKnown,
			members:        make(map[string]*memberRecord),
		}
		g.byID[id] = g.byOpenID[r.OpenID]
	}
	return nil
}

// persist 把群清单写回状态文件（没变化就跳过）。
func (g *groupRegistry) persist() error {
	if g == nil || g.store == nil {
		return nil
	}
	g.mu.Lock()
	if !g.dirty {
		g.mu.Unlock()
		return nil
	}
	recs := make([]GroupRecord, 0, len(g.byID))
	ids := make([]int64, 0, len(g.byID))
	for id := range g.byID {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		rec := g.byID[id]
		recs = append(recs, GroupRecord{
			OpenID:         rec.OpenID,
			Name:           rec.Name,
			MemberCount:    rec.MemberCount,
			MaxMemberCount: rec.MaxMemberCount,
			AddedAt:        rec.AddedAt,
			InfoKnown:      rec.InfoKnown,
		})
	}
	g.dirty = false
	g.mu.Unlock()

	if err := g.store.SaveGroups(recs); err != nil {
		g.mu.Lock()
		g.dirty = true
		g.mu.Unlock()
		return err
	}
	return nil
}

// ---------------------------------------------------------------- 好友表（内存）

type friendRecord struct {
	OpenID   string
	ID       int64
	Nickname string
	LastSeen int64
}

// friendRegistry 是「见过的单聊用户」清单。
//
// 官方同样没有好友列表接口，只能靠 C2C_MESSAGE_CREATE / FRIEND_ADD 事件学；
// 不落盘（成员/好友这类高频变化的表落盘收益低、文件易膨胀）。
type friendRegistry struct {
	mu    sync.RWMutex
	byID  map[int64]*friendRecord
	ids   *IDMap
	appID string
}

func newFriendRegistry(ids *IDMap, appID string) *friendRegistry {
	return &friendRegistry{byID: make(map[int64]*friendRecord), ids: ids, appID: appID}
}

func (f *friendRegistry) ensure(openid, nickname string) int64 {
	if f == nil || openid == "" || f.ids == nil {
		return 0
	}
	id := f.ids.ToInt64(f.appID, openid)
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := f.byID[id]
	if rec == nil {
		rec = &friendRecord{OpenID: openid, ID: id}
		f.byID[id] = rec
	}
	if nickname != "" {
		rec.Nickname = nickname
	}
	rec.LastSeen = time.Now().Unix()
	return id
}

func (f *friendRegistry) remove(id int64) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.byID, id)
}

func (f *friendRegistry) snapshot() []*friendRecord {
	if f == nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	out := make([]*friendRecord, 0, len(f.byID))
	for _, rec := range f.byID {
		cp := *rec
		out = append(out, &cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (f *friendRegistry) get(id int64) (*friendRecord, bool) {
	if f == nil {
		return nil, false
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	rec, ok := f.byID[id]
	if !ok {
		return nil, false
	}
	cp := *rec
	return &cp, true
}

// ---------------------------------------------------------------- 被动回复锚点

// passiveAnchor 是「最近一条可以被动回复的消息」。
type passiveAnchor struct {
	OpenID string
	MsgID  string
	At     time.Time
}

// passiveStore 按目标记录被动回复锚点。
//
// 为什么要它：adapter 的 SendGroupMessage/SendPrivateMessage 签名里没有 msg_id，
// 而官方被动回复必须带事件的 msg_id。事件一到就记锚点，发送时若还在窗口内就带上，
// 窗口/次数用尽由 message.go 的账本自动摘掉转主动。
type passiveStore struct {
	mu sync.Mutex
	m  map[string]passiveAnchor
}

func newPassiveStore() *passiveStore {
	return &passiveStore{m: make(map[string]passiveAnchor)}
}

func passiveKey(kind TargetKind, id int64) string {
	return strconv.FormatUint(uint64(kind), 10) + ":" + strconv.FormatInt(id, 10)
}

func (p *passiveStore) set(kind TargetKind, id int64, openID, msgID string) {
	if p == nil || id == 0 || msgID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[passiveKey(kind, id)] = passiveAnchor{OpenID: openID, MsgID: msgID, At: time.Now()}
}

func (p *passiveStore) clear(kind TargetKind, id int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.m, passiveKey(kind, id))
}

// get 返回窗口内的锚点；超过窗口返回 false（宁可主动发，也不拿过期 msg_id 去试）。
func (p *passiveStore) get(kind TargetKind, id int64) (passiveAnchor, bool) {
	if p == nil {
		return passiveAnchor{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	anchor, ok := p.m[passiveKey(kind, id)]
	if !ok {
		return passiveAnchor{}, false
	}
	ttl := PassiveTTLUser
	if kind == TargetGroup {
		ttl = PassiveTTLGroup
	}
	if ttl <= 0 || time.Since(anchor.At) >= ttl {
		delete(p.m, passiveKey(kind, id))
		return passiveAnchor{}, false
	}
	return anchor, true
}

// ---------------------------------------------------------------- Adapter

// QQOfficialAdapter 是 QQ 官方机器人（开放平台 v2）的 DDBOT adapter。
type QQOfficialAdapter struct {
	cfg      *Config
	tm       *TokenManager
	client   *Client
	uploader *Uploader
	gateway  *Gateway
	ids      *IDMap
	dedup    *Dedup
	store    *JSONStore
	msgs     *msgRegistry
	groups   *groupRegistry
	friends  *friendRegistry
	passive  *passiveStore
	// introAnchors 是「一次性进群锚点」：进群帧顶层 id（event_id）按内部群号存 TTL 时间，
	// **只有 SendJoinIntro 会取**。普通发送路径一律不读不写，见 introAnchorStore 的注释。
	introAnchors *introAnchorStore
	parser       *eventParser

	downloadClient *http.Client
	// downloadLimit 是 DownloadFile 的字节上限，默认 maxDownloadBytes（200MB）。
	// 单列成字段只为了让用例能在「不实际传 200MB」的前提下验证截断检测，不是给用户配的旋钮。
	downloadLimit int64

	handlerMu        sync.RWMutex
	onGroupMessage   func(*adapter.GroupMessageEvent)
	onPrivateMessage func(*adapter.PrivateMessageEvent)
	onMetaEvent      func(*adapter.MetaEvent)
	onNoticeEvent    func(*adapter.NoticeEvent)
	onRequestEvent   func(*adapter.RequestEvent)

	// selfID 是机器人自己的**内部 id**（IDMap 命名空间）。
	// 注意：绝不能塞官方的 user.id（READY 里那个 1234567890123456789）——
	// lsp/parser/parser.go:99-103 的 @ 判定是 AtTarget == GetUin()，而 AtTarget 是
	// 事件里 at 段的 target（IDMap 给的小整数），两个命名空间永不相交 → @ 了反而不理。
	selfID atomic.Int64
	// selfMu 保护下面三张「自身 openid」相关的表。
	selfMu sync.Mutex
	// selfAliases 是所有确认属于机器人的 openid。openid 与 member_openid 实测都是
	// per-bot（同一机器人在不同群拿到的是同一个 member_openid，见 learnSelfMember），
	// 这里仍按「可能多个」存：换机器人 hook、平台后续调整时不必再改结构。
	selfAliases map[string]struct{}
	// selfResolved 记录已经从 bot_state 拿到过自身 openid 的群。
	selfResolved map[string]bool
	// selfAttempted 是取自身 openid 的节流表（失败后别死磕）。
	selfAttempted map[string]time.Time

	// infoGate 是「同一时刻只跑一个群信息任务」的闸门。注意它只是闸门，**队列在下面**：
	// CAS 失败不代表这件事被丢掉。
	infoGate atomic.Bool
	// infoPendingMu 同时保护 infoPending **和闸门的释放**：队列判空与 infoGate.Store(false)
	// 必须在同一把锁里完成，否则「刚判空就进来一条新请求」会让那条请求永远没人处理。
	infoPendingMu sync.Mutex
	// infoPending 是等闸门的群（group_openid 集合）。
	//
	// 以前闸门被占用就直接 return：两个群几乎同时入群时，后一个群的群名/人数永远取不到，
	// 日志里一个字都没有。现在占用期间进来的请求排队，持有闸门的任务跑完当场补跑，
	// 不会丢；同一时刻仍然只有一个任务在打接口（保留原来「别刚加群就猛打」的本意）。
	infoPending map[string]struct{}
	// infoBlockedMu 保护 infoBlocked。
	infoBlockedMu sync.Mutex
	// infoBlocked 记录「哪个群（group_openid）的群信息接口撞过权限错误」以及冷却截止时间。
	//
	// 必须**按群**记（原来是全局单值）：一个群没权限不代表别的群没权限。全局闸门时，
	// 谁先撞上 11253 就把所有群一起挡 30 分钟，新加的群连群名都拿不到，群名/人数一直为空。
	// 冷却到期的记录在读取时就地删掉，表长最多是「冷却期内撞过权限错误的群数」。
	infoBlocked map[string]time.Time

	// memberSink 是 messenger 注入的成员表失效通道（见 adapter.MemberListSinkSetter）。
	memberSinkMu sync.Mutex
	memberSink   adapter.MemberListSink
	// memberSynced 记录「已经同步给上层缓存的成员及其角色」：groupID -> memberID -> role。
	//
	// 判定必须**按人**而不是按群：判定「是否 @ 了我」要的是发起者本人能不能在自己那条消息
	// 的处理流程里被权限系统看到。按群的话，群里第 2、3… 个成员的首条命令必然撞在
	// 「缓存里还没有他」上 —— 实测就是这样：群主第二条消息才通过，第一条被 nil member info 拒掉。
	memberSynced map[int64]map[int64]string
	memberSync   *memberSyncNotifier

	// startErr 是构造期发现、但签名里没法直接返回的致命错误（NewQQOfficialAdapter
	// 的签名由调用方冻结，不能改成返回 (Adapter, error)）。目前唯一的来源是
	// 「状态文件里的 app_id 与当前配置不一致」；Start 一进来就把它交出去，让
	// bot.Init 的 Fatalf 把它变成「拒绝启动」。
	startErr error

	// stateLogLimit 给「定期落盘反复失败」的日志限流（见 failureLogLimiter 与 stateLoop）。
	stateLogLimit *failureLogLimiter

	// emptyMembersWarned 记录「已经为哪个群喊过成员表为空」，避免每次启动/每次检查
	// 都刷一遍同一条提示（见 warnEmptyMemberTables）。受 memberSinkMu 保护。
	emptyMembersWarned map[int64]bool
	// proactiveWarned 记录「已经为哪个群（按 group_openid）喊过没有主动消息权限」，
	// 每个群只喊一次（见 warnProactiveDeniedOnce）。受 memberSinkMu 保护。
	proactiveWarned map[string]bool

	started atomic.Bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// botName 是机器人自己的名字（GET /users/@me 的 username），填模板
	// trigger.group.bot_joined.tmpl 的 {{ bot_name }}。启动后由后台 goroutine 写入，
	// 事件回调里并发读，所以用 atomic.Value；没拉到就是空串（模板自己兜底）。
	botName atomic.Value // string
	// botNameRetryBackoff 是拉名字失败后的重试间隔（测试里调成 0，不干等）。
	botNameRetryBackoff time.Duration
}

// 编译期检查：必须完整实现 adapter.Adapter 的全部 35 个方法。
var _ adapter.Adapter = (*QQOfficialAdapter)(nil)

// NewQQOfficialAdapter 建一个官方 adapter。
//
// 注意 cfg（adapter.AdapterConfig）是 OneBot 形状的（mode/ws 地址/token），这里的
// 凭据与运行参数一律读 qqofficial.* 配置段（config.go 的 GetConfig）；cfg 里只有
// Timeout 能顺手用上。签名由调用方（bot/bot/bot.go 的工厂）冻结，不能改。
func NewQQOfficialAdapter(cfg *adapter.AdapterConfig) adapter.Adapter {
	qc := GetConfig()
	if cfg != nil && cfg.Timeout > 0 && qc.HTTPTimeout <= 0 {
		qc.HTTPTimeout = cfg.Timeout
	}

	a := &QQOfficialAdapter{
		cfg:                 qc,
		msgs:                newMsgRegistry(DefaultMsgRegistryCapacity),
		passive:             newPassiveStore(),
		introAnchors:        newIntroAnchorStore(introAnchorTTL),
		downloadClient:      &http.Client{Timeout: downloadTimeout},
		downloadLimit:       maxDownloadBytes,
		selfAliases:         make(map[string]struct{}),
		selfResolved:        make(map[string]bool),
		selfAttempted:       make(map[string]time.Time),
		memberSynced:        make(map[int64]map[int64]string),
		botNameRetryBackoff: botNameRetryBackoff,
		emptyMembersWarned:  make(map[int64]bool),
		proactiveWarned:     make(map[string]bool),
		infoBlocked:         make(map[string]time.Time),
		stateLogLimit:       newFailureLogLimiter(stateFlushLogInterval),
	}
	a.tm = NewTokenManager(qc.AppID, qc.AppSecret)
	a.client = NewClient(a.tm)
	a.uploader = NewUploader(a.client)

	// 默认文件名带 appId（换机器人天然是一个干净文件）。只有在用默认路径时才做
	// 一次性迁移；用户显式配了 qqofficial.state-file 就完全按他的路径走。
	statePath := StateFilePath(qc.AppID)
	if !StateFilePathConfigured() {
		if moved, err := MigrateLegacyStateFile(LegacyStateFile, statePath, qc.AppID); err != nil {
			logger.Warnf("%s: 旧状态文件 %s 没有迁移到 %s（%v）。本次运行按新文件从空状态开始，"+
				"旧文件里的内部 ID 不会带过来；请按上面的原因确认后自行改名或删除该文件",
				ConfigKeyPrefix, LegacyStateFile, statePath, err)
		} else if moved {
			logger.Infof("%s: 状态文件已迁移到带 appId 的新名字：%s → %s（内部 ID 与群清单原样保留，"+
				"旧文件改名成 %s%s；换机器人请用新文件，旧文件可以归档了）",
				ConfigKeyPrefix, LegacyStateFile, statePath, LegacyStateFile, stateFileMigratedSuffix)
		}
	}

	if st, err := NewJSONStore(statePath, qc.AppID); err != nil {
		logger.Warnf("%s: 状态文件不可用（%v），ID 映射与群清单将只存在于内存", ConfigKeyPrefix, err)
	} else {
		a.store = st
		// app_id 不一致必须拒绝启动，而构造签名没有 error 可返回：先记下来，由 Start 交出。
		// 为什么不再是「只 Warn 一句」：写盘会把 app_id 改写成当前值，2 秒后就查不到了，
		// 详见 persist.go 的 CheckAppID 注释。
		if err := st.CheckAppID(); err != nil {
			logger.Errorf("%s: 状态文件检查未通过，本次启动将被拒绝：%v", ConfigKeyPrefix, err)
			a.startErr = err
		}
	}

	if a.store != nil {
		a.ids = NewIDMap(a.store)
	} else {
		a.ids = NewIDMap(nil)
	}
	a.dedup = NewDedup(DefaultDedupCapacity, DedupWindow)
	a.friends = newFriendRegistry(a.ids, qc.AppID)
	a.groups = newGroupRegistry(a.ids, qc.AppID, a.store, a.fetchGroupInfoAsync)
	if err := a.groups.load(); err != nil {
		logger.Warnf("%s: 读取群清单失败（%v），本次运行群列表从空开始", ConfigKeyPrefix, err)
	}
	a.parser = &eventParser{appID: qc.AppID, ids: a.ids, msgs: a.msgs, self: a.GetSelfID, isSelf: a.isSelfOpenID}
	a.memberSync = newMemberSyncNotifier(a.notifyMemberListStale, memberSyncDebounce, memberSyncMinGap, memberSyncMaxPerMinute)
	a.gateway = NewGateway(a.tm, EventHandlerFunc(a.onDispatchEvent))

	logger.Infof("%s: adapter 已装配（appID=%s，状态文件=%s，已恢复 %d 个群）",
		ConfigKeyPrefix, qc.AppID, a.store.Path(), len(a.groups.snapshot()))
	return a
}

// ------------------------------------------------------------------ 生命周期

// Start 连接官方网关，阻塞到首个 READY（或失败）。
func (a *QQOfficialAdapter) Start() error {
	if a.started.Swap(true) {
		return fmt.Errorf("%s: adapter 已经启动过", ConfigKeyPrefix)
	}
	// 构造期就判定不能跑的（状态文件 app_id 与当前配置不符）：这里直接拒绝启动。
	if a.startErr != nil {
		a.started.Store(false)
		return fmt.Errorf("%s: 拒绝启动：%w", ConfigKeyPrefix, a.startErr)
	}
	if err := a.cfg.Validate(); err != nil {
		a.started.Store(false)
		return err
	}
	// 成员表为空的群先说清一句：官方通道拿不到群成员列表，启动瞬间它一定是空的。
	a.warnEmptyMemberTables()

	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	a.cancel = cancel

	// 机器人自己的名字（GET /users/@me 的 username）丢后台拉：它只影响模板里的
	// bot_name，**不许挡网关连接**。失败由 loadBotName 自己兜底（有限重试 → 留空）。
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.loadBotName(ctx)
	}()

	// 先把「机器人自己的 openid」解出来：SelfID 与事件里 at 段的 target 必须同源，
	// 否则 @ 判定恒假。群清单已经落过盘（状态文件里存了 group_openid），所以这里
	// 不用等用户发消息就能拿到 —— 取第一个群的 bot_state 同步解析（决定 SelfID），
	// 其余群丢后台补别名（member_openid 实测是 per-bot，各群值相同，这里只是顺手记全）。
	a.bootstrapSelfOpenID()

	if err := a.gateway.Start(ctx); err != nil {
		cancel()
		a.started.Store(false)
		return err
	}

	a.wg.Add(3)
	go a.heartbeatLoop(ctx)
	go a.stateLoop(ctx)
	if a.memberSync != nil {
		go func() {
			defer a.wg.Done()
			a.memberSync.run(ctx)
		}()
	} else {
		a.wg.Done()
	}

	// 这里必须主动报一次 lifecycle（实测踩过的坑）：
	// adapter/messenger.go:243-257 靠 lifecycle MetaEvent 置 Online/Uin，而 lsp 的启动流程
	// （lsp/module.go:1020-1040）死等 Messenger.IsConnected() = Online && Adapter.IsConnected()。
	// 但 ws.go 的 READY 分支只在自己内部处理（onReady），**不会 dispatch 给 EventHandler**，
	// 所以「网关收到 READY」到「上层认为机器人在线」之间没有任何自动通路——不补这一枪，
	// 启动就会一直卡在「等待 bot 上线」。
	a.emitLifecycle()

	logger.Infof("%s: 网关已就绪（self_id=%d session=%s）", ConfigKeyPrefix, a.GetSelfID(), a.gateway.SessionID())
	return nil
}

// Stop 断开网关并把映射与群清单落盘。可重复调用。
func (a *QQOfficialAdapter) Stop() error {
	if !a.started.Swap(false) {
		return nil
	}
	if a.cancel != nil {
		a.cancel()
	}
	var errs []error
	if a.gateway != nil {
		if err := a.gateway.Stop(); err != nil {
			errs = append(errs, err)
		}
	}
	a.wg.Wait()
	if a.ids != nil {
		if err := a.ids.Flush(); err != nil {
			errs = append(errs, fmt.Errorf("落盘 ID 映射失败：%w", err))
		}
	}
	if a.groups != nil {
		if err := a.groups.persist(); err != nil {
			errs = append(errs, fmt.Errorf("落盘群清单失败：%w", err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		logger.Errorf("%s: Stop 收尾有错误：%v", ConfigKeyPrefix, err)
		return err
	}
	return nil
}

// heartbeatLoop 把连接状态按心跳 MetaEvent 报给上层（上层据此翻 Online 标志）。
func (a *QQOfficialAdapter) heartbeatLoop(ctx context.Context) {
	defer a.wg.Done()
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	online := true
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cur := a.IsConnected()
			if cur == online {
				continue
			}
			online = cur
			if cur {
				// 重连回来：再报一次 lifecycle（上层会翻转 Online 并刷新列表）
				a.emitLifecycle()
			} else {
				a.reportOffline()
			}
		}
	}
}

// reportOffline 在上层状态从「在线」翻成「离线」时调用一次。
//
// 光报 emitMetaEvent(false) 不够：普通断线和「网关已经永久放弃」在上层看起来一模一样。
// 网关以 4914（机器人下架）/ 4915（封禁）关闭时，run() 直接收工——不重连、不再有心跳，
// 而 Start 早就返回了，initErr 通道没人读。没有这一句，进程会静默躺在那里，
// 上层一直以为「正在重连，等一等就好」，实际永远回不来。
//
// 判据是 Gateway.FatalError()：非 nil = 永久离线，需要人工处理（确认没被下架/封禁）后重启；
// nil = 普通断线，后台仍在按退避重连，不该惊动用户。
func (a *QQOfficialAdapter) reportOffline() {
	if a.gateway != nil {
		if fatal := a.gateway.FatalError(); fatal != nil {
			logger.Errorf("%s: 网关永久离线，后台不再重连，机器人不会自己恢复；"+
				"确认机器人没被下架/封禁后重启 adapter：%v", ConfigKeyPrefix, fatal)
		}
	}
	a.emitMetaEvent(false)
}

// stateLoop 定期把新分配的 ID 映射与群清单写盘。
//
// 落盘失败**每条都会报**，只是同类失败做了日志限流（见 failureLogLimiter）：
// IDMap 进只读降级之后 Flush 是永久失败，不设限就是每 2 秒一条 Warn、一天四万多条。
func (a *QQOfficialAdapter) stateLoop(ctx context.Context) {
	defer a.wg.Done()
	ticker := time.NewTicker(stateFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.flushStateOnce()
		}
	}
}

// 落盘日志的限流 key：ID 映射与群清单各算一类失败，互不影响。
const (
	stateFlushLogKeyIDMap  = "idmap"
	stateFlushLogKeyGroups = "groups"
)

// flushStateOnce 是一次「把 ID 映射与群清单落盘」的完整动作。
func (a *QQOfficialAdapter) flushStateOnce() {
	if a.ids != nil {
		if err := a.ids.Flush(); err != nil {
			a.reportStateFlushFailure(stateFlushLogKeyIDMap, "定期落盘 ID 映射失败", err)
		} else {
			a.reportStateFlushRecovered(stateFlushLogKeyIDMap, "定期落盘 ID 映射")
		}
	}
	if a.groups != nil {
		if err := a.groups.persist(); err != nil {
			a.reportStateFlushFailure(stateFlushLogKeyGroups, "定期落盘群清单失败", err)
		} else {
			a.reportStateFlushRecovered(stateFlushLogKeyGroups, "定期落盘群清单")
		}
	}
}

// reportStateFlushFailure 报一次落盘失败，同类失败按 stateFlushLogInterval 限流。
//
// 限流不是把失败藏起来：被压掉的次数会写进下一条日志，所以日志里始终能看出
// 「这个错误还在持续，已经又失败了 N 次」。
func (a *QQOfficialAdapter) reportStateFlushFailure(key, what string, err error) {
	if a.stateLogLimit == nil {
		logger.Warnf("%s: %s：%v", ConfigKeyPrefix, what, err)
		return
	}
	ok, dropped := a.stateLogLimit.allow(key)
	if !ok {
		return
	}
	if dropped > 0 {
		logger.Warnf("%s: %s：%v（限流期间同类失败又被压掉 %d 条；不是不再失败，只是不每 %s 打一遍）",
			ConfigKeyPrefix, what, err, dropped, stateFlushLogInterval)
		return
	}
	logger.Warnf("%s: %s：%v", ConfigKeyPrefix, what, err)
}

// reportStateFlushRecovered 在落盘恢复正常时补一条 Info（只在之前失败过时打）。
func (a *QQOfficialAdapter) reportStateFlushRecovered(key, what string) {
	if a.stateLogLimit == nil {
		return
	}
	if had, dropped := a.stateLogLimit.reset(key); had {
		logger.Infof("%s: %s 已恢复正常（之前一直在失败，限流期间共压掉 %d 条）", ConfigKeyPrefix, what, dropped)
	}
}

// failureLogLimiter 给「同一类失败反复发生」的日志限流。
//
// 语义（不许把失败吞掉）：
//   - 每个 key 的第一条**立即**打，不受间隔限制；
//   - 之后同一 key 每 interval 最多一条，这一条里带上「期间被压掉几条」；
//   - 恢复只报一次；**恢复之后不清「上次打日志的时刻」**。
//
// 最后这条是踩出来的：原来 reset 会把 last 一起删掉，于是「失败→成功→失败→成功」
// 这种抖动每次都当「首条」立即打、每次成功再补一条恢复 Info，2 秒一对，一天八万多行，
// 比不做限流还糟。保留 last 之后，抖动在一个 interval 内最多打一轮。
type failureLogLimiter struct {
	interval time.Duration
	// now 可在测试里替换成受控时钟；只在持有 mu 时读取。
	now func() time.Time

	mu      sync.Mutex
	last    map[string]time.Time
	dropped map[string]int
	// failing 表示这个 key 当前处于「已经打过失败日志、还没报恢复」的状态。
	failing map[string]bool
}

func newFailureLogLimiter(interval time.Duration) *failureLogLimiter {
	if interval <= 0 {
		interval = stateFlushLogInterval
	}
	return &failureLogLimiter{
		interval: interval,
		now:      time.Now,
		last:     make(map[string]time.Time),
		dropped:  make(map[string]int),
		failing:  make(map[string]bool),
	}
}

// allow 判断这次是否该打日志；该打时返回自上次以来被压掉的条数。
func (l *failureLogLimiter) allow(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	// 判据只看「距上次**打日志**多久」，不看中间成功过几次：
	// 抖动（失败→成功→失败）如果按「恢复后重新算首条」，每次都会立刻打，比不限流还糟。
	// 首次失败没有 last，天然立即放行。
	if last, ok := l.last[key]; ok && now.Sub(last) < l.interval {
		l.dropped[key]++
		return false, 0
	}
	dropped := l.dropped[key]
	delete(l.dropped, key)
	l.last[key] = now
	l.failing[key] = true
	return true, dropped
}

// reset 在同类操作恢复之后调用，返回（之前是否在报失败, 期间被压掉的条数）。
//
// **故意不清 last**：清了就会让抖动重新按「首条」打。last 留着，下次失败是否立刻可见
// 由 interval 决定，与成功次数无关。
func (l *failureLogLimiter) reset(key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	had := l.failing[key]
	dropped := l.dropped[key]
	delete(l.failing, key)
	delete(l.dropped, key)
	return had, dropped
}

// apiCtx 给单次 HTTP 调用配一个带超时的 ctx。
func (a *QQOfficialAdapter) apiCtx() (context.Context, context.CancelFunc) {
	base := a.ctx
	if base == nil {
		base = context.Background()
	}
	timeout := a.cfg.HTTPTimeout
	if timeout <= 0 {
		timeout = HTTPTimeout
	}
	return context.WithTimeout(base, timeout)
}

// ------------------------------------------------------------------ 事件分发

// onDispatch 是网关回调的**兼容入口**（不带事件 id）：等价于 onDispatchEvent(eventType, "", data)。
//
// 留着它是因为上层的大量用例直接调它；网关走的是下面那个带 id 的入口。
func (a *QQOfficialAdapter) onDispatch(eventType string, data json.RawMessage) {
	a.onDispatchEvent(eventType, "", data)
}

// onDispatchEvent 是网关回调：把官方事件翻成 adapter 事件并交给上层。
//
// id 是 WS 帧**顶层**的 id（实测 GROUP_ADD_ROBOT 有，形如 GROUP_ADD_ROBOT:<uuid>；
// 事件体 d 里**没有**它）。两个用途：进群事件的被动发送锚点（见 handleGroupAdd），
// 以及通知类事件的幂等键（见 noticeEventDuplicated）。
//
// 去重分两处：
//   - **入站消息事件**在各自的 handle 里先用原始 msg_id 过 Dedup.Seen，重复的整条丢弃
//     （官方明说相同 msg_id 可能重复推送；实测同一 AppID 的多个连接都会收到同一批事件）；
//   - **通知/请求类事件**（进群、退群、成员进出、好友增删、按钮交互、入群申请）用帧顶层
//     id 在这里过同一张 Dedup 表。凡是「被重投一次就会让上层多做一件事」的事件都算在
//     这一类里；C2C_MSG_RECEIVE / MESSAGE_AUDIT_* 这类纯通知暂不在此列（见 handleInfoEvent）。
func (a *QQOfficialAdapter) onDispatchEvent(eventType, id string, data json.RawMessage) {
	switch eventType {
	case EventReady, EventResumed:
		a.handleReady(eventType, data)
	case EventGroupAtMessageCreate, EventGroupMessageCreate:
		a.handleGroupMessage(eventType, data)
	case EventC2CMessageCreate:
		a.handleC2CMessage(data)
	case EventGroupAddRobot:
		if a.noticeEventDuplicated(eventType, id) {
			return
		}
		a.handleGroupAdd(data, id)
	case EventGroupDelRobot:
		if a.noticeEventDuplicated(eventType, id) {
			return
		}
		a.handleGroupDel(data)
	case EventGroupMemberAdd, EventGroupMemberRemove:
		if a.noticeEventDuplicated(eventType, id) {
			return
		}
		a.handleGroupMemberOp(eventType, data)
	case EventFriendAdd, EventFriendDel:
		if a.noticeEventDuplicated(eventType, id) {
			return
		}
		a.handleFriendOp(eventType, data)
	case EventInteractionCreate:
		if a.noticeEventDuplicated(eventType, id) {
			return
		}
		a.handleInteraction(data)
	case EventGroupJoinRequest:
		if a.noticeEventDuplicated(eventType, id) {
			return
		}
		a.handleJoinRequest(data)
	case EventC2CMsgReceive, EventC2CMsgReject, EventGroupMsgReceive, EventGroupMsgReject,
		EventMessageAuditPass, EventMessageAuditReject:
		a.handleInfoEvent(eventType, data)
	default:
		logger.Debugf("%s: 未处理的事件类型 %s", ConfigKeyPrefix, eventType)
	}
}

// noticeEventDuplicated 用帧顶层的**事件 id** 给通知类事件做幂等。
//
// 为什么通知类事件比消息更急：断线重连时平台会重投同一批帧。重投的进群事件会重新登记
// 一次性锚点、让 lsp 再派发一次 GroupJoinEvent —— 同一个群的自我介绍就发两遍；
// 退群/成员进出重复派发同理会让上层重复清理、重复刷新成员缓存；好友删除重复派发会让
// 上层重复清理联系人，按钮交互和入群申请重复派发则等于用户点了两次按钮 / 申请了两次。
// 消息事件早就在用 Dedup，这里给**整族**通知与请求事件补上同一套。
//
// 幂等键就是帧顶层 id（形如 GROUP_ADD_ROBOT:<uuid>）。去重表沿用 a.dedup（和 msg_id
// 共用一张表、同一个固定窗口）：两边键的空间不同（ROBOT1.0_… 与 EVENT_TYPE:uuid），
// 不会互相撞；共用一张表也就不会有两份内存表各记一半。
//
// id 为空时**放行**并留一条 Debug，与现有消息路径的策略一致（handleGroupMessage /
// handleC2CMessage 的判据同样是 p.ID != "" 才过 Seen）：空 id 不是「重复」的证据，
// 丢掉一次真的进群/退群事件，比偶尔重复派发一次更难收拾。
func (a *QQOfficialAdapter) noticeEventDuplicated(eventType, id string) bool {
	if id == "" {
		logger.Debugf("%s: 事件 %s 的帧没有顶层 id，无法做幂等，按首次处理放行", ConfigKeyPrefix, eventType)
		return false
	}
	if a.seenBefore(id) {
		logger.Debugf("%s: 重复推送，丢弃事件 %s（帧 id=%s）", ConfigKeyPrefix, eventType, id)
		return true
	}
	return false
}

// seenBefore 是**唯一**的去重入口：问一下这张 Dedup 表里见没见过 key。
//
// 为什么不各处直接 a.dedup.Seen(...)：dedup 是构造期才赋值的字段，手搓
// &QQOfficialAdapter{}（或构造失败的 adapter）走消息/通知路径时它就是 nil，直接调
// Seen 是 nil 解引用 panic。这里统一判一次 nil，nil 的语义是「没有表 ⇒ 判不出重复」，
// 与「表存在但没见过这个 key」一致 —— 宁可重复处理一次，也不让消息路径崩掉。
func (a *QQOfficialAdapter) seenBefore(key string) bool {
	if a == nil || a.dedup == nil || key == "" {
		return false
	}
	return a.dedup.Seen(key)
}

func (a *QQOfficialAdapter) handleReady(eventType string, data json.RawMessage) {
	var p readyPayload
	if !unmarshalEvent(eventType, data, &p) {
		return
	}
	// 注意：READY 里的 user.id 是**官方身份**（实测是一串数字，如 1234567890123456789），
	// 不是 IDMap 命名空间里的内部 id，**绝不能**写进 selfID —— 那正是「@ 了反而不理」的根因
	// （at 段 target 走 IDMap，SelfID 走官方 id，lsp/parser 的等式恒假）。
	// 自身 openid 只能来自 bot_state 的 member_openid，见 resolveSelfFromGroup。
	if p.User.ID != "" {
		logger.Infof("%s: %s 的官方 user.id=%s（仅记录；SelfID 以 bot_state 的 member_openid 为准）",
			ConfigKeyPrefix, eventType, p.User.ID)
	}
	logger.Infof("%s: %s username=%q shard=%v（当前 self_id=%d）",
		ConfigKeyPrefix, eventType, p.User.Username, p.Shard, a.GetSelfID())
	a.emitLifecycle()
}

func (a *QQOfficialAdapter) handleGroupMessage(eventType string, data json.RawMessage) {
	var p wsMessagePayload
	if !unmarshalEvent(eventType, data, &p) {
		return
	}
	groupOpenID := p.groupOpenid()
	if groupOpenID == "" {
		logger.Errorf("%s: 事件 %s 缺少 group_openid，丢弃", ConfigKeyPrefix, eventType)
		return
	}
	if a.seenBefore(p.ID) {
		logger.Debugf("%s: 重复推送，丢弃事件 %s（msg_id=%s）", ConfigKeyPrefix, eventType, p.ID)
		return
	}
	// 机器人自己（以及群里别的机器人）的发言一律丢弃：实测官方会把机器人自己的消息
	// 以 GROUP_MESSAGE_CREATE 推回来，不拦就会再走一遍命令
	// 管线 —— 自己发的文本若以命令前缀开头会被 AtCheck 放行而**自执行**（AtTarget<=0 时
	// 放行，lsp/parser/parser.go:99-104），配了通用消息模板还会自回复循环。
	if reason := a.botAuthoredReason(&p); reason != "" {
		logger.Debugf("%s: 忽略机器人发的消息（依据=%s 事件=%s 作者=%s msg_id=%s）",
			ConfigKeyPrefix, reason, eventType, sendClip(p.Author.identity()), sendClip(p.ID))
		return
	}
	if eventType == EventGroupAtMessageCreate {
		// 实测：@ 前缀被去掉后残留一个前导空格
		p.Content = trimAtPrefix(p.Content)
	}
	// 把引用相关的原始字段一并打出来：排查「回复某条图片 + /倒放」这条链时，
	// 需要确认事件里到底有没有 msg_idx / ref_msg_idx、以及引用能不能在消息表里命中。
	logger.Debugf("%s: 收到群事件 %s（群=%s 作者=%s msg_id=%s msg_idx=%q ref_msg_idx=%q 引用命中=%v 附件=%d 文本=%q）",
		ConfigKeyPrefix, eventType, groupOpenID, p.Author.identity(), sendClip(p.ID),
		sendClip(p.selfRefIdx()), sendClip(p.quotedRefIdx()), a.replyTargetKnown(&p),
		len(p.Attachments), sendClip(p.text()))

	groupID := a.groups.ensure(groupOpenID)
	a.ensureSelfForGroupAsync(groupOpenID)
	if memberID, role, changed := a.groups.learnMember(groupOpenID, p.Author, a.ids, a.cfg.AppID); changed {
		// 新增成员或角色变化：按**人**判定要不要在派发事件前同步刷上层成员缓存，
		// 这样发起者本人在这条消息里的权限判定就能看到他。
		a.syncGroupMember(groupID, memberID, role)
	}
	userID := a.parser.userID(p.Author.identity())

	recID := a.msgs.register(&msgRecord{
		RawID:    p.ID,
		RefIdx:   p.selfRefIdx(),
		Kind:     TargetGroup,
		OpenID:   groupOpenID,
		GroupID:  groupID,
		UserID:   userID,
		Content:  p.text(),
		Username: p.Author.Username,
		Time:     p.timeUnix(),
	})

	// 被动锚点：@ 事件与**全量群消息**都记。
	// 文档只为 GROUP_AT_MESSAGE_CREATE 的 d.id 承诺「5 分钟内可被动回复」，
	// 但 2026-11-11 端到端实测：拿 GROUP_MESSAGE_CREATE 的 d.id 当 msg_id 发送照样走通被动
	// （日志「已发送group消息 … 被动=true」，无降级重发），所以两种都记，全量模式下也保持被动优先。
	// 万一遇到真过期的 d.id，message.go 会按 isPassiveInvalidCode 自动摘掉 msg_id 转主动。
	if p.ID != "" {
		a.passive.set(TargetGroup, groupID, groupOpenID, p.ID)
	}

	segments := a.parser.segments(&p)
	if eventType == EventGroupAtMessageCreate {
		segments = a.ensureSelfAtSegment(segments)
	}

	ev := &adapter.GroupMessageEvent{
		MessageID:  int64(recID),
		GroupID:    groupID,
		UserID:     userID,
		RawMessage: p.text(),
		Message:    segments,
		Time:       p.timeUnix(),
		SelfID:     a.GetSelfID(),
	}
	a.emitGroupMessage(ev)
}

func (a *QQOfficialAdapter) handleC2CMessage(data json.RawMessage) {
	var p wsMessagePayload
	if !unmarshalEvent(EventC2CMessageCreate, data, &p) {
		return
	}
	if a.seenBefore(p.ID) {
		logger.Debugf("%s: 重复推送，丢弃事件 %s（msg_id=%s）", ConfigKeyPrefix, EventC2CMessageCreate, p.ID)
		return
	}
	// 与群消息同一条规则：机器人自己发的消息不进命令管线（单聊侧防御性保留，
	// 目前没有观察到自身回推，但判据与群消息相同，没有理由分叉）。
	if reason := a.botAuthoredReason(&p); reason != "" {
		logger.Debugf("%s: 忽略机器人发的消息（依据=%s 事件=%s 作者=%s msg_id=%s）",
			ConfigKeyPrefix, reason, EventC2CMessageCreate, sendClip(p.Author.identity()), sendClip(p.ID))
		return
	}
	openID := p.Author.identity()
	if openID == "" {
		logger.Errorf("%s: 单聊事件缺少作者 openid，丢弃", ConfigKeyPrefix)
		return
	}
	userID := a.parser.userID(openID)
	a.friends.ensure(openID, p.Author.Username)

	recID := a.msgs.register(&msgRecord{
		RawID:    p.ID,
		RefIdx:   p.selfRefIdx(),
		Kind:     TargetUser,
		OpenID:   openID,
		UserID:   userID,
		Content:  p.text(),
		Username: p.Author.Username,
		Time:     p.timeUnix(),
	})
	if p.ID != "" {
		a.passive.set(TargetUser, userID, openID, p.ID)
	}

	a.emitPrivateMessage(&adapter.PrivateMessageEvent{
		MessageID:  int64(recID),
		UserID:     userID,
		TargetID:   userID,
		RawMessage: p.text(),
		Message:    a.parser.segments(&p),
		Time:       p.timeUnix(),
		SelfID:     a.GetSelfID(),
	})
}

// introAnchorTTL 是一次性进群锚点的存活时间。
//
// 只需要撑到 lsp 收到 GroupJoinEvent、把模板渲染完再调回来，2 分钟绰绰有余；
// 顺带保证一个很早以前的进群帧不会被后面的发送误用。
const introAnchorTTL = 2 * time.Minute

// introAnchor 是一条进群锚点。
type introAnchor struct {
	eventID  string
	expireAt time.Time
}

// introAnchorStore 是「机器人被拉进群」那次事件的一次性锚点表：内部群号 -> 进群帧 event_id。
//
// 为什么需要它：event_id 只有进群帧的**顶层 id** 才有（见 handleGroupAdd），而两条自我介绍
// 都由 lsp 收尾时调回来的 SendJoinIntro 发（模板得先渲染）—— 中间必须有个地方把 event_id
// 交过去。tip 也一起挪到了这次调用里，所以两条共用同一个锚点、顺序天然成立。
//
// 约定（上一版就是因为违反它被推翻的，改动前先看这条）：
//   - 只有 handleGroupAdd 写、只有 SendJoinIntro take；
//   - 普通发送路径（SendGroupMessage / sendTo / sendToWithEvent）**一律不读不写**：
//     读一下就可能把 lsp 要用的锚点吃掉，写一下就更离谱；
//   - take 是一次性的，取不到就是取不到 —— 这一层只返回「没有锚点」，**adapter 自己
//     不会造一条主动消息顶上**。
//
// 但别把最后一条读成「这条链路永远不可能变成主动消息」：adapter 层**不会主动把
// event_id 去掉**，而 message.go 有一层保守兜底 —— 平台把 event_id 判成失效时，它会
// 摘掉 event_id 重发一次（见 message.go 的 Send 里 eventDropped 分支）。**那一次重发
// 就是主动消息**：群里有主动消息权限才发得出去，没主动权限的群白打一次请求并拿到
// 40034105。两句话各管一层，都成立，别按其中一句去改另一层。
type introAnchorStore struct {
	mu   sync.Mutex
	ttl  time.Duration
	now  func() time.Time
	byID map[int64]introAnchor
}

func newIntroAnchorStore(ttl time.Duration) *introAnchorStore {
	return &introAnchorStore{
		ttl:  ttl,
		now:  time.Now,
		byID: make(map[int64]introAnchor),
	}
}

// put 登记（或覆盖）一个群的锚点，并**顺带清掉已经过期的项** —— 表只在进群事件里增长，
// 顺手清一下就不会一直攒着没人取的老条目。
func (s *introAnchorStore) put(groupID int64, eventID string) {
	if s == nil || groupID == 0 || eventID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, anchor := range s.byID {
		if !now.Before(anchor.expireAt) {
			delete(s.byID, id)
		}
	}
	s.byID[groupID] = introAnchor{eventID: eventID, expireAt: now.Add(s.ttl)}
}

// take 一次性取走某个群的锚点：取到就删掉；没进群 / 已用 / 已过期都返回 false。
//
// 取舍（有意为之，别「优化」掉）：锚点是**先取走、后发送**，所以如果接下来的长文案
// 发送失败，这个锚点不会退还回去。理由是退还救不回任何东西、只会更糟：
//   - lsp 对同一次 GroupJoinEvent 不会重试，退回去的锚点没人取，只能躺到 TTL 过期；
//   - 而在这段时间里，任何一次读到它的发送都可能把它用掉（比如用户手动触发一次进群
//     自我介绍），拿一条早就用过的 event_id 去发，结果只是多一条 40054/40034105；
//   - 真正需要知道的是「这条长文案没发出去」，那件事由 SendJoinIntro 返回 error + Error
//     日志说清楚（见它的失败语义），比留一个可能被误用的锚点可靠。
func (s *introAnchorStore) take(groupID int64) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	anchor, ok := s.byID[groupID]
	if !ok {
		return "", false
	}
	delete(s.byID, groupID)
	if !s.now().Before(anchor.expireAt) {
		return "", false
	}
	return anchor.eventID, true
}

// count 返回表里还剩多少条（含已过期但还没被清掉的），只给测试看。
func (s *introAnchorStore) count() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.byID)
}

// SendJoinIntro 机器人被拉进群后的**自我介绍**：先一条短提示，再一条 lsp 渲染好的长文案。
//
// 说清楚语义：这不是「欢迎某人」，是机器人自报家门 —— 告诉这个群它能干什么。
//
// 两条为什么都在**这里**发：tip 以前由 handleGroupAdd 起 goroutine 发，长文案由 lsp 的
// 事件链接回调发，两条是并发链路、不承诺顺序。两条需要按「先短后长」的顺序发出，把 tip 挪进
// 这个方法，顺序就天然成立：同一个调用、同一个 goroutine、同一个 event_id。
//
// 走的是进群事件那条被动通道：event_id 由 handleGroupAdd 按内部群号登记，这里一次性取走。
// 取不到就直接报错、一条都不发：这一层**不会主动把 event_id 去掉**，也不会自己造一条
// 主动消息顶上。
//
// 边界说清楚（别与发送层的兜底混为一谈）：message.go 的保守兜底在平台把 event_id 判成
// 失效时会摘掉它重发一次（见 message.go 的 Send 里 eventDropped 分支），**那一次就成了
// 主动消息**——群里有主动权限才发得出去，没主动权限的群会白打一次请求拿到 40034105。
// 也就是说：「adapter 不回退」和「极端情况下真的会发出一次主动消息」同时成立。
//
// 失败语义（别改成「一条失败就整体失败」）：
//   - tip 失败只记 Error，**不中断**，长文案照发；
//   - 长文案失败 → 整体返回错误（上层据此知道模板那条没出去）；
//   - 两条都没内容（tip 没配 + 模板为空）→ 返回 (0, nil)：什么都没发，但这不算错误。
//
// 锚点在进来时就 take 掉了，失败也**不退还**（理由见 introAnchorStore.take 的注释）：
// 所以「长文案失败」之后重试同一次进群自我介绍会报「没有进群事件锚点」，这是预期行为。
func (a *QQOfficialAdapter) SendJoinIntro(groupCode int64, message interface{}) (int32, error) {
	if groupCode == 0 {
		return 0, fmt.Errorf("%s: 进群自我介绍没有给出群号（内部群号 0）", ConfigKeyPrefix)
	}
	eventID, ok := a.introAnchors.take(groupCode)
	if !ok {
		return 0, fmt.Errorf("%s: 群 %d 没有进群事件锚点（没收到过这个群的进群事件，"+
			"或者锚点已经被取用 / 已经过期）；进群自我介绍只能走进群事件的被动回复，"+
			"没有锚点时一条都不发（adapter 不会自己造一条主动消息顶上）",
			ConfigKeyPrefix, groupCode)
	}

	// 第 1 条：短提示。文案在**发送时**读：用户改完配置重启就生效，不用进 Config 结构体。
	seq := 0
	if tip := getString(ConfigKeyJoinIntroTip); tip != "" {
		seq++
		// 同一个 event_id 连发两条实测都是 http=200，两条共用这一条被动通道。
		if _, err := a.sendToWithEvent(TargetGroup, groupCode, tip, eventID); err != nil {
			// 不中断：一条提示失败不该把后面的长文案也一起丢掉。
			logger.Errorf("%s: 进群自我介绍第 %d 条（短提示 %s.%s）发送失败（内部群号 %d，event_id=%s），继续发下一条：%v",
				ConfigKeyPrefix, seq, ConfigKeyPrefix, ConfigKeyJoinIntroTip, groupCode, sendClip(eventID), err)
		} else {
			logger.Infof("%s: 已发进群自我介绍第 %d 条（短提示 %s.%s，内部群号 %d，event_id=%s）",
				ConfigKeyPrefix, seq, ConfigKeyPrefix, ConfigKeyJoinIntroTip, groupCode, sendClip(eventID))
		}
	} else {
		logger.Debugf("%s: 进群自我介绍没有配短提示（%s.%s 为空），群 %d 只发模板文案",
			ConfigKeyPrefix, ConfigKeyPrefix, ConfigKeyJoinIntroTip, groupCode)
	}

	// 第 2 条：lsp 渲染出来的长文案。没有内容就跳过 —— 只配了 tip 的用户到这里就结束了。
	if joinIntroMessageEmpty(message) {
		if seq == 0 {
			logger.Debugf("%s: 群 %d 的进群自我介绍没有可发内容（tip 没配、模板也为空），一条都不发",
				ConfigKeyPrefix, groupCode)
		}
		return 0, nil
	}
	seq++
	id, err := a.sendToWithEvent(TargetGroup, groupCode, message, eventID)
	if err != nil {
		return 0, err
	}
	logger.Infof("%s: 已发进群自我介绍第 %d 条（模板文案，内部群号 %d，event_id=%s）",
		ConfigKeyPrefix, seq, groupCode, sendClip(eventID))
	return id, nil
}

// joinIntroMessageEmpty 判断 lsp 交过来的模板消息是不是「没有内容」。
//
// lsp 侧渲染为空时传 nil 或 0 元素的 *adapter.SendingMessage 都算空（typed-nil 也算），
// 这两种都要跳过发送，而不是当成错误。
func joinIntroMessageEmpty(message interface{}) bool {
	if message == nil {
		return true
	}
	if sm, ok := message.(*adapter.SendingMessage); ok {
		return sm == nil || len(sm.Elements) == 0
	}
	return false
}

// handleGroupAdd 处理 GROUP_ADD_ROBOT：登记群、存一次性锚点，**自己一条都不发**。
//
// eventID 是帧顶层的 id（事件体 d 里没有）：实测把它当 event_id 发消息，在没有主动
// 消息权限的群里也能成功（同一个群里用 msg_id 主动发是 40034105）。
//
// 这里只登记锚点：tip 与长文案都由 lsp 收尾时调的 SendJoinIntro 一起发，顺序才成立
// （以前 tip 在这里起 goroutine，和 lsp 那条成了并发链路，谁先谁后全靠运气）。
func (a *QQOfficialAdapter) handleGroupAdd(data json.RawMessage, eventID string) {
	var p groupOpPayload
	if !unmarshalEvent(EventGroupAddRobot, data, &p) {
		return
	}
	openid := p.groupOpenid()
	if openid == "" {
		return
	}
	groupID := a.groups.ensure(openid)
	a.ensureSelfForGroupAsync(openid)
	a.fetchGroupInfoAsync(openid)
	// 锚点必须在 emitNoticeEvent **之前**登记：emit 是同步派发，
	// lsp 的 GroupJoinEvent 回调会在这一行里立刻回来调 SendJoinIntro 取锚点。
	// 放后面（曾经就是放后面）会让它每次都取不到，报「没有进群事件锚点」。
	if eventID != "" {
		a.introAnchors.put(groupID, eventID)
	} else {
		logger.Warnf("%s: 群 %s 的进群帧没有顶层 id，进群自我介绍没有 event_id 可带"+
			"（在 allow_proactive_msg=false 的群里发不出去）", ConfigKeyPrefix, sendClip(openid))
	}
	logger.Infof("%s: 机器人被拉进群 %s（内部群号 %d）", ConfigKeyPrefix, openid, groupID)
	a.emitNoticeEvent(&adapter.NoticeEvent{
		NoticeType: "group_increase",
		Time:       p.timeUnix(),
		SelfID:     a.GetSelfID(),
		GroupID:    groupID,
		UserID:     a.GetSelfID(),
		OperatorID: a.parser.userID(p.OpMemberOpenID),
	})
}

func (a *QQOfficialAdapter) handleGroupDel(data json.RawMessage) {
	var p groupOpPayload
	if !unmarshalEvent(EventGroupDelRobot, data, &p) {
		return
	}
	openid := p.groupOpenid()
	if openid == "" {
		return
	}
	var groupID int64
	if rec, ok := a.groups.getByOpenID(openid); ok {
		groupID = rec.ID
	} else {
		// 群不在清单里（状态文件丢了、或这个群从没被登记过）。这时 GroupID 只能是 0，
		// 而上层（messenger）拿到 0 会 FindGroupByUin(0) 取空、什么都不做也不报错——
		// 订阅清理就被静默跳过。这种情况必须让人看见，否则「被踢了但配置还在」无从排查。
		logger.Warnf("%s: 机器人被移出群 %s，但这个群不在本地群清单里（内部群号未知）："+
			"上层无法按群号清理订阅与权限，请人工检查是否有残留配置", ConfigKeyPrefix, sendClip(openid))
	}
	logger.Infof("%s: 机器人被移出群 %s（内部群号 %d），从群清单移除", ConfigKeyPrefix, openid, groupID)
	a.groups.remove(openid)
	a.emitNoticeEvent(&adapter.NoticeEvent{
		NoticeType: "group_decrease",
		// SubType 必须是 kick_me，理由不是「我们确定是被踢的」，而是上层的判据：
		// adapter/messenger.go:1507 只认 SubType == "kick_me" 或 OperatorID == SelfID
		// 才走「机器人自己离开」分支，否则落进「普通成员退群」——那条路只会移除成员、
		// 不清理群与订阅，而 adapter 这边已经把群从清单删掉了，之后每条推送都报「未知群号」。
		// 我们填的 OperatorID 是**操作者**（踢人的人），拿它跟 SelfID 比永远不等。
		// 官方事件里「主动退群」和「被管理员踢」不区分（GROUP_DEL_ROBOT 只有这一个语义：
		// 机器人自己离开这个群），所以两种都按 kick_me 处理是当前唯一合理的映射。
		SubType:    "kick_me",
		Time:       p.timeUnix(),
		SelfID:     a.GetSelfID(),
		GroupID:    groupID,
		UserID:     a.GetSelfID(),
		OperatorID: a.parser.userID(p.OpMemberOpenID),
	})
}

func (a *QQOfficialAdapter) handleGroupMemberOp(eventType string, data json.RawMessage) {
	var p memberOpPayload
	if !unmarshalEvent(eventType, data, &p) {
		return
	}
	openid := p.groupOpenid()
	if openid == "" {
		return
	}
	groupID := a.groups.ensure(openid)
	a.ensureSelfForGroupAsync(openid)
	noticeType := "group_increase"
	if eventType == EventGroupMemberRemove {
		noticeType = "group_decrease"
	}
	now := time.Now().Unix()
	if p.Timestamp > 0 {
		now = p.Timestamp
	}
	a.emitNoticeEvent(&adapter.NoticeEvent{
		NoticeType: noticeType,
		Time:       now,
		SelfID:     a.GetSelfID(),
		GroupID:    groupID,
		UserID:     a.parser.userID(firstNonEmpty(p.MemberOpenID, p.OpMemberOpenID)),
		OperatorID: a.parser.userID(p.OpMemberOpenID),
	})
}

func (a *QQOfficialAdapter) handleFriendOp(eventType string, data json.RawMessage) {
	var p friendOpPayload
	if !unmarshalEvent(eventType, data, &p) {
		return
	}
	if p.OpenID == "" {
		return
	}
	id := a.friends.ensure(p.OpenID, "")
	now := p.Timestamp
	if now <= 0 {
		now = time.Now().Unix()
	}
	noticeType := "friend_add"
	if eventType == EventFriendDel {
		a.friends.remove(id)
		// DDBOT 的 notice 类型里没有「好友删除」，用 friend_remove 明确表达（上层会当未知类型忽略并记日志）
		noticeType = "friend_remove"
	}
	a.emitNoticeEvent(&adapter.NoticeEvent{
		NoticeType: noticeType,
		SubType:    strings.ToLower(eventType),
		Time:       now,
		SelfID:     a.GetSelfID(),
		UserID:     id,
	})
}

// handleInteraction 把按钮回调映射成 NoticeEvent。
//
// DDBOT 没有交互事件类型，这里用 NoticeType = "interaction" 兜住：不丢事件、不 panic。
// 官方要求收到事件后 PUT /interactions/{id} 回应，否则客户端一直 loading ——
// 目前不做回应（DDBOT 没有按钮消息的发送路径），回调只在这里兜成一条事件。
func (a *QQOfficialAdapter) handleInteraction(data json.RawMessage) {
	var p interactionPayload
	if !unmarshalEvent(EventInteractionCreate, data, &p) {
		return
	}
	now := p.Timestamp
	if now <= 0 {
		now = time.Now().Unix()
	}
	var groupID int64
	if openid := firstNonEmpty(p.GroupOpenID, p.GroupID); openid != "" {
		groupID = a.groups.ensure(openid)
	}
	userID := a.parser.userID(firstNonEmpty(p.Data.Resolved.UserID, p.UserOpenID))
	subType := p.Data.Resolved.ButtonID
	if subType == "" {
		subType = p.Data.Resolved.ButtonData
	}
	a.emitNoticeEvent(&adapter.NoticeEvent{
		NoticeType: "interaction",
		SubType:    subType,
		Time:       now,
		SelfID:     a.GetSelfID(),
		GroupID:    groupID,
		UserID:     userID,
		OperatorID: userID,
	})
}

func (a *QQOfficialAdapter) handleJoinRequest(data json.RawMessage) {
	var p joinRequestPayload
	if !unmarshalEvent(EventGroupJoinRequest, data, &p) {
		return
	}
	openid := firstNonEmpty(p.GroupOpenID, p.GroupID)
	var groupID int64
	if openid != "" {
		groupID = a.groups.ensure(openid)
	}
	now := p.ApplyAt
	if now <= 0 {
		now = time.Now().Unix()
	}
	a.emitRequestEvent(&adapter.RequestEvent{
		RequestType: "group",
		SubType:     "add",
		Time:        now,
		SelfID:      a.GetSelfID(),
		GroupID:     groupID,
		UserID:      a.parser.userID(p.MemberOpenID),
		Comment:     p.Username,
		Flag:        p.JoinRequestID,
	})
}

// handleInfoEvent 把「开关通知 / 消息审核」这类事件统一报成 notify。
//
// 解析失败一律 return、不再照发一条 notify：这一层报出去的东西等于「平台确认过这件事」，
// 拿半截数据也发通知就是在编事实。其它 handler（handleReady / handleGroupAdd /
// handleInteraction / handleJoinRequest）都是这个规矩，这里没有理由例外。
func (a *QQOfficialAdapter) handleInfoEvent(eventType string, data json.RawMessage) {
	subType := strings.ToLower(eventType)
	now := time.Now().Unix()
	if strings.HasPrefix(eventType, "MESSAGE_AUDIT") {
		var p auditPayload
		if !unmarshalEvent(eventType, data, &p) {
			// unmarshalEvent 已经把「空 body / 坏 JSON」记成 Warn/Error，这里补一句
			// 说明为什么不再派发，免得有人以为这是又一次静默丢弃。
			logger.Warnf("%s: 消息审核事件 %s 解析失败，不派发 notify（解析原因见上一条日志）",
				ConfigKeyPrefix, eventType)
			return
		}
		// 注意：message_audit.message_id 是原始消息 id，**不是** openid，
		// 绝不能拿去建 ID 映射（那会把一个消息 id 永久占成「用户」）。
		a.emitNoticeEvent(&adapter.NoticeEvent{
			NoticeType: "notify",
			SubType:    subType,
			Time:       now,
			SelfID:     a.GetSelfID(),
		})
		return
	}
	a.emitNoticeEvent(&adapter.NoticeEvent{
		NoticeType: "notify",
		SubType:    subType,
		Time:       now,
		SelfID:     a.GetSelfID(),
	})
}

func (a *QQOfficialAdapter) emitGroupMessage(ev *adapter.GroupMessageEvent) {
	a.handlerMu.RLock()
	h := a.onGroupMessage
	a.handlerMu.RUnlock()
	if h != nil {
		h(ev)
	}
}

func (a *QQOfficialAdapter) emitPrivateMessage(ev *adapter.PrivateMessageEvent) {
	a.handlerMu.RLock()
	h := a.onPrivateMessage
	a.handlerMu.RUnlock()
	if h != nil {
		h(ev)
	}
}

func (a *QQOfficialAdapter) emitNoticeEvent(ev *adapter.NoticeEvent) {
	a.handlerMu.RLock()
	h := a.onNoticeEvent
	a.handlerMu.RUnlock()
	if h != nil {
		h(ev)
	}
}

func (a *QQOfficialAdapter) emitRequestEvent(ev *adapter.RequestEvent) {
	a.handlerMu.RLock()
	h := a.onRequestEvent
	a.handlerMu.RUnlock()
	if h != nil {
		h(ev)
	}
}

func (a *QQOfficialAdapter) emitMetaEvent(online bool) {
	ev := &adapter.MetaEvent{
		MetaEventType: "heartbeat",
		Time:          time.Now().Unix(),
		SelfID:        a.GetSelfID(),
		Status:        map[string]interface{}{"online": online, "good": online},
	}
	a.handlerMu.RLock()
	h := a.onMetaEvent
	a.handlerMu.RUnlock()
	if h != nil {
		h(ev)
	}
}

// replyTargetKnown 只用于日志：本条消息引用的那条消息在不在本地消息表里。
func (a *QQOfficialAdapter) replyTargetKnown(p *wsMessagePayload) bool {
	ref := p.quotedRefIdx()
	if ref == "" || a.msgs == nil {
		return false
	}
	_, ok := a.msgs.byRefIdx(ref)
	return ok
}

// ensureSelfAtSegment 保证 Elements 里有一个指向机器人自己的 at 段。
//
// 用途：GROUP_AT_MESSAGE_CREATE 明确表示「有人 @ 了机器人」，但平台会把 @机器人 的前缀
// 从 content 里剥掉，于是 Elements 里没有对应的 AtSegment、AtTarget 会是 0。补上自己的
// at 段，上层拿到的就是明确且正确的信号（AtTarget == SelfID）。
// 已经有指向自己的 at 段（或有多个 @ 时机器人本身也在其中）就不重复加。
func (a *QQOfficialAdapter) ensureSelfAtSegment(segs []adapter.MessageSegment) []adapter.MessageSegment {
	self := a.GetSelfID()
	if self <= 0 {
		// 还不知道自己是谁：硬加一个错的 target 反而会让 @ 判定失败，
		// 不如不加（上层对 AtTarget <= 0 是放行的）。
		return segs
	}
	for _, s := range segs {
		if s.Type != "at" {
			continue
		}
		if v, ok := s.Data["qq"].(float64); ok && int64(v) == self {
			return segs
		}
	}
	out := make([]adapter.MessageSegment, 0, len(segs)+1)
	out = append(out, adapter.MessageSegment{
		Type: "at",
		Data: map[string]interface{}{"qq": float64(self)},
	})
	return append(out, segs...)
}

// emitLifecycle 报「上线」：上层（adapter/messenger.go:243-257）据此置 Uin/Online 并刷新列表。
func (a *QQOfficialAdapter) emitLifecycle() {
	ev := &adapter.MetaEvent{
		MetaEventType: "lifecycle",
		Time:          time.Now().Unix(),
		SelfID:        a.GetSelfID(),
		Status:        map[string]interface{}{"online": true, "good": true},
	}
	a.handlerMu.RLock()
	h := a.onMetaEvent
	a.handlerMu.RUnlock()
	if h != nil {
		h(ev)
	}
}

// groupInfoFetchDelay 是后台取群信息前的固定等待（进群那一瞬间接口往往还没就绪）。
const groupInfoFetchDelay = 200 * time.Millisecond

// fetchGroupInfoAsync 后台取一次群信息（同时最多一个任务在跑，避免刚加群就猛打接口）。
//
// 闸门被占用时**不丢弃**：把群排进 infoPending，持有闸门的那个任务跑完会当场补跑
// （见 runGroupInfoQueue）。以前直接 return，两个群几乎同时入群时后一个群永远拿不到
// 群名/人数，日志里也看不出来。
func (a *QQOfficialAdapter) fetchGroupInfoAsync(openid string) {
	if openid == "" {
		return
	}
	a.infoPendingMu.Lock()
	if a.infoGate.CompareAndSwap(false, true) {
		a.infoPendingMu.Unlock()
		go a.runGroupInfoQueue(openid)
		return
	}
	if a.infoPending == nil {
		a.infoPending = make(map[string]struct{})
	}
	a.infoPending[openid] = struct{}{}
	a.infoPendingMu.Unlock()
	logger.Debugf("%s: 群 %s 的群信息任务排在第 %d 个（同时只跑一个，前一个结束后补取）",
		ConfigKeyPrefix, sendClip(openid), a.infoPendingLen())
}

// infoPendingLen 只给日志用：数一下还有几个群在排队。
func (a *QQOfficialAdapter) infoPendingLen() int {
	a.infoPendingMu.Lock()
	defer a.infoPendingMu.Unlock()
	return len(a.infoPending)
}

// runGroupInfoQueue 是 infoGate 的**唯一持有者**：先取 first，再把排队等闸门的群取完。
//
// 队列判空与释放闸门在 infoPendingMu 里一起做（见字段注释）：判空之后、释放之前进来的
// 请求会在 CAS 失败后照常入队，而释放之后的请求会自己拿到闸门，两头都不会漏。
func (a *QQOfficialAdapter) runGroupInfoQueue(first string) {
	next := first
	for next != "" {
		time.Sleep(groupInfoFetchDelay)
		// 这里吞掉 error 是有意的：后台任务没有调用方可以接错误，fetchGroupInfo 自己
		// 已经按 Warn/Infof 把「哪个群、为什么没取到」写进日志了。同步路径（GetGroupInfo）
		// 才需要把 error 往上交给用户。
		_ = a.fetchGroupInfo(next)

		a.infoPendingMu.Lock()
		if queued := a.takeInfoPendingLocked(); queued != "" {
			a.infoPendingMu.Unlock()
			next = queued
			continue
		}
		a.infoGate.Store(false)
		a.infoPendingMu.Unlock()
		return
	}
}

// takeInfoPendingLocked 从队列里取一个群（调用方必须持有 infoPendingMu）。
func (a *QQOfficialAdapter) takeInfoPendingLocked() string {
	for k := range a.infoPending {
		delete(a.infoPending, k)
		return k
	}
	return ""
}

// fetchGroupInfo 用 GET /v2/groups/{openid}/info 补群名与人数。
//
// 实测该接口对个人机器人是 200 可用，但文档把它标成 11253 白名单；所以拿到权限类错误
// 就进冷却，期间不再尝试（群名保持为空，不影响收发消息）。
//
// **返回 error 是为了让同步路径能如实报「没取到」**（GetGroupInfo 用它，见那边的注释）。
// 后台路径（runGroupInfoQueue）拿到的 error 直接丢掉 —— 后台没有调用方接它，
// 而「哪个群、为什么没取到」这里已经按 Warn 写进日志，不会静默。
func (a *QQOfficialAdapter) fetchGroupInfo(openid string) error {
	if openid == "" {
		return nil
	}
	if a.groupInfoBlocked(openid) {
		// 冷却期内**不**再打接口，但这不是「成功」：调用方必须知道这次没拿到。
		return fmt.Errorf("%s: 群 %s 的群信息接口还在无权限冷却中（上次撞过 11253 权限错误，"+
			"%s 内不再尝试）", ConfigKeyPrefix, openid, groupInfoCooldown)
	}
	ctx, cancel := a.apiCtx()
	defer cancel()

	var resp struct {
		GroupOpenID     string `json:"group_openid"`
		GroupName       string `json:"group_name"`
		GroupMemberNum  int    `json:"group_member_num"`
		GroupFingerMemo string `json:"group_finger_memo"`
	}
	if err := a.client.GetJSON(ctx, fmt.Sprintf(PathGroupInfo, openid), &resp); err != nil {
		if apiErr, ok := AsAPIError(err); ok && apiErr.IsPermission() {
			a.blockGroupInfo(openid)
			logger.Warnf("%s: 群 %s 的群信息接口无权限（%v），这个群 %s 内不再尝试；它的群名将保持为空（其它群不受影响）",
				ConfigKeyPrefix, openid, err, groupInfoCooldown)
			return fmt.Errorf("%s: 群 %s 的群信息接口无权限（11253 白名单），%s 内不再尝试：%w",
				ConfigKeyPrefix, openid, groupInfoCooldown, err)
		}
		logger.Warnf("%s: 取群 %s 的信息失败：%v", ConfigKeyPrefix, openid, err)
		return fmt.Errorf("%s: 取群 %s 的信息失败：%w", ConfigKeyPrefix, openid, err)
	}
	a.unblockGroupInfo(openid)
	a.groups.recordInfo(openid, resp.GroupName, resp.GroupMemberNum, 0)
	logger.Infof("%s: 群 %s 信息已缓存（name=%q members=%d）", ConfigKeyPrefix, openid, resp.GroupName, resp.GroupMemberNum)
	return nil
}

// groupInfoBlocked 判断这个群的群信息接口是否还在「无权限冷却」里。
// 顺手删掉已经到期的记录：API 恢复（或配置变了）之后不该再被一张老表挡住。
func (a *QQOfficialAdapter) groupInfoBlocked(openid string) bool {
	a.infoBlockedMu.Lock()
	defer a.infoBlockedMu.Unlock()
	until, ok := a.infoBlocked[openid]
	if !ok {
		return false
	}
	if !time.Now().Before(until) {
		delete(a.infoBlocked, openid)
		return false
	}
	return true
}

// blockGroupInfo 只把**这个群**放进冷却，不影响别的群。
func (a *QQOfficialAdapter) blockGroupInfo(openid string) {
	now := time.Now()
	a.infoBlockedMu.Lock()
	defer a.infoBlockedMu.Unlock()
	for id, until := range a.infoBlocked {
		if !now.Before(until) {
			delete(a.infoBlocked, id)
		}
	}
	a.infoBlocked[openid] = now.Add(groupInfoCooldown)
}

// unblockGroupInfo 在某个群取信息成功之后清掉它的冷却记录。
func (a *QQOfficialAdapter) unblockGroupInfo(openid string) {
	a.infoBlockedMu.Lock()
	defer a.infoBlockedMu.Unlock()
	delete(a.infoBlocked, openid)
}

// ------------------------------------------------------------------ 发送

// outbound 是一条待发消息的中间形态。
type outbound struct {
	// plain 是纯文本正文（markdown 被拒时的兜底文案）。
	plain string
	// markdown 非空表示这条消息里有 @ 标记，必须按 markdown 发才能渲染成真 @
	// （实测/资料：纯文本里的 <qqbot-at-user id="…"/> 会原样显示成代码）。
	markdown string
	media    []outboundMedia
	refIdx   string
}

// outboundMedia 是待上传的媒体。
type outboundMedia struct {
	fileType int
	fileName string
	url      string // http(s) 地址，走 URL 上传
	path     string // 本地路径，走二进制直传
	data     []byte // 已解码的字节（base64:// 或本地文件读出来的）
	// sourceErr 记录「来源本身有问题」的原因（目前只有 base64:// 解码失败）。
	//
	// 它必须一路带到最终错误文案里：以前解码错误在 newMediaFromSource 的 else 分支被直接
	// 丢掉，上传侧只剩「媒体内容为空」—— 用户看不出是 base64 写坏了还是文件读空了。
	sourceErr error
}

func (a *QQOfficialAdapter) sendTo(kind TargetKind, id int64, message interface{}) (int32, error) {
	return a.sendToWithEvent(kind, id, message, "")
}

// sendToWithEvent 是 sendTo 的实际实现；eventID 非空表示这次是「回复某个事件帧」的
// 被动发送（目前只有进群自我介绍这么用，见 SendJoinIntro；语义见 message.go 的
// SendOptions.EventID）。
func (a *QQOfficialAdapter) sendToWithEvent(kind TargetKind, id int64, message interface{}, eventID string) (int32, error) {
	if kind != TargetGroup && kind != TargetUser {
		return 0, fmt.Errorf("%s: 发送目标类型没给（%d）", ConfigKeyPrefix, uint8(kind))
	}
	if id == 0 {
		return 0, fmt.Errorf("%s: 发送目标的内部 id 为 0", ConfigKeyPrefix)
	}
	openID, ok := a.ids.ToOpenID(a.cfg.AppID, id)
	if !ok || openID == "" {
		return 0, fmt.Errorf("%s: 未知的%s号 %d（映射表里没有它；映射表由 qqofficial.state-file 恢复，"+
			"状态文件丢失或被清空后旧 id 就认不出来了）", ConfigKeyPrefix, kind, id)
	}

	out, err := a.buildOutbound(message)
	if err != nil {
		return 0, err
	}
	if out.plain == "" && out.markdown == "" && len(out.media) == 0 {
		return 0, fmt.Errorf("%s: 没有可发送的内容（消息为空或全是当前实现不支持的段）", ConfigKeyPrefix)
	}

	target := Target{Kind: kind, OpenID: openID}
	// 两条被动通道互斥：给了 event_id 就不再吃 msg_id 的窗口/次数账本
	// （见 message.go 的 SendOptions.EventID）。event_id 只会由进群自我介绍
	// （SendJoinIntro）显式传进来，普通发送路径（SendGroupMessage 等）一律传空。
	opts := SendOptions{RefIdx: out.refIdx, EventID: eventID}
	if opts.EventID != "" {
		logger.Infof("%s: %s 按事件被动发送（不吃 msg_id 的窗口/次数账本）：event_id=%s",
			ConfigKeyPrefix, target, sendClip(opts.EventID))
	} else if anchor, ok := a.passive.get(kind, id); ok {
		opts.MsgID = anchor.MsgID
	}

	ctx, cancel := a.apiCtx()
	defer cancel()

	var lastID int32
	sentAny := false
	// sentCount 是「已经成功发出去的条数」，部分失败时要写进错误文案。
	sentCount := 0
	// firstErr 是**部分失败**的错误：一条消息被拆成多条发，中间某一条没成功。
	// 以前它从来没被赋值过（if firstErr != nil 是死代码），部分失败只打一条 Error 就
	// 当成功返回，上层以为整条消息发出去了。
	var firstErr error

	// 正文只发一次：曾经这里是先独立发一条正文，下面的富媒体循环又把同一段正文塞进
	// 第一条媒体的 content —— 群里正文重复一次，还多吃一个被动名额。
	//
	// 分流依据（实测出来的请求能力边界）：
	//   - 有 @某个人 标记（out.markdown 非空）：**必须单独成条**发 markdown（msg_type=2）。
	//     群里 @用户 只在 markdown 消息里渲染；富媒体是 msg_type=7，它的 content 是纯文本，
	//     <qqbot-at-user/> 塞进去会原样显示成代码 —— 「并入媒体条」这条路会丢 @ 语义。
	//     注意 out.markdown 为空**不等于**没有 @：@全体 两边都写（见 buildOutbound 的
	//     at 分支），只有它时 markdown 与 plain 相同、out.markdown 为空，走的正是下面
	//     textInline 那条路 —— 这是有意为之：实测纯文本消息认 <qqbot-at-everyone />。
	//   - 纯文本正文 + 有媒体：并入第一条媒体的 content（msg_type=7 的 content 本来就是
	//     附带文本），一次发送只吃一个被动名额（群上限 5 次）。
	//   - 纯文本正文 + 没有媒体：照旧单独发一条。
	textInline := out.markdown == "" && out.plain != "" && len(out.media) > 0
	if !textInline && (out.markdown != "" || out.plain != "") {
		var res *SendResult
		var err error
		if out.markdown != "" {
			fallback := opts
			fallback.Content = out.plain
			res, err = a.client.SendMarkdown(ctx, target, out.markdown, fallback)
		} else {
			res, err = a.client.SendText(ctx, target, out.plain, opts)
		}
		if err != nil {
			return 0, err
		}
		sentAny = true
		sentCount++
		lastID = a.registerSent(res, kind, openID, id, out.plain)
		logger.Infof("%s: 已发送%s消息（msg_id=%s msg_type=%d 被动=%v 事件被动=%v 引用=%v 内部id=%d）",
			ConfigKeyPrefix, kind, res.MsgID, res.MsgType, res.Passive, res.EventPassive, out.refIdx != "", lastID)
	}

	// 富媒体：官方一条消息只有一个 media 字段，多个媒体拆成多条发。
	for i, part := range out.media {
		info, err := a.uploadMedia(ctx, kind, openID, part)
		if err != nil {
			if !sentAny {
				return 0, err
			}
			// 已经有内容发出去过：这时返回的是「部分失败」——不能当成成功（用户会少收一条，
			// 谁也不知道），也不能让上层重发整条（正文就重复了）。错误文案里明确写
			// 「已发出的部分不重发」，由调用方决定只记录还是提示。
			firstErr = partialSendError("上传失败", i, len(out.media), sentCount, err)
			logger.Errorf("%s: %v", ConfigKeyPrefix, firstErr)
			break
		}
		mediaOpts := opts
		if i == 0 && textInline {
			// 只有「正文并入媒体条」这条路才带 content；markdown 那条已经单独发过正文。
			mediaOpts.Content = out.plain
		}
		res, sendErr := a.client.SendMedia(ctx, target, info.FileInfo, mediaOpts)
		if sendErr != nil {
			// 平台说这枚 file_info 已失效（40035003/08/09）就把缓存里那一条精确删掉，
			// 下一次推送重新上传。不接这一行，media.go 的失效清理就只有入口没有调用者，
			// 坏 file_info 会一直用到 24h TTL 到期。删除与日志都由 media.go 负责。
			_ = a.uploader.InvalidateOnSendFailure(info, sendErr)
			if !sentAny {
				return 0, sendErr
			}
			firstErr = partialSendError("发送失败", i, len(out.media), sentCount, sendErr)
			logger.Errorf("%s: %v", ConfigKeyPrefix, firstErr)
			break
		}
		sentAny = true
		sentCount++
		lastID = a.registerSent(res, kind, openID, id, "")
		logger.Infof("%s: 已发送%s富媒体消息（msg_id=%s 被动=%v 事件被动=%v 内部id=%d）",
			ConfigKeyPrefix, kind, res.MsgID, res.Passive, res.EventPassive, lastID)
	}

	if firstErr != nil {
		// 带上 lastID：最后一条成功发出去的消息，调用方要撤回/引用时还能用。
		return lastID, firstErr
	}
	if !sentAny {
		return 0, fmt.Errorf("%s: 消息没有任何一部分发送成功", ConfigKeyPrefix)
	}
	return lastID, nil
}

// partialSendError 造「一条消息拆成多条发、中间某一条没成功」的错误。
//
// phase 是「上传失败」或「发送失败」；index 从 0 起，文案里写成「第 i/total 个媒体」。
// sentCount 是这条消息里已经成功发出去的条数（正文单独成条时也算一条）。
func partialSendError(phase string, index, total, sentCount int, cause error) error {
	return fmt.Errorf("%s: 第 %d/%d 个媒体%s（已成功发出 %d 条；已发出的部分不重发，"+
		"剩下的 %d 个媒体也不再尝试）：%w",
		ConfigKeyPrefix, index+1, total, phase, sentCount, total-(index+1), cause)
}

// registerSent 把一条**自己发出**的消息登记进表，返回内部 id（撤回/引用要用）。
func (a *QQOfficialAdapter) registerSent(res *SendResult, kind TargetKind, openID string, id int64, content string) int32 {
	if res == nil {
		return 0
	}
	now := time.Now().Unix()
	if res.Timestamp != "" {
		if ts, err := time.Parse(time.RFC3339, res.Timestamp); err == nil {
			now = ts.Unix()
		}
	}
	rec := &msgRecord{
		RawID:    res.MsgID,
		RefIdx:   res.RefIdx,
		Kind:     kind,
		OpenID:   openID,
		Content:  content,
		Outgoing: true,
		Passive:  res.Passive,
		Time:     now,
	}
	switch kind {
	case TargetGroup:
		rec.GroupID = id
		rec.UserID = a.GetSelfID()
	case TargetUser:
		rec.UserID = id
	}
	return a.msgs.register(rec)
}

// uploadMedia 上传一个媒体，拿到 file_info。
func (a *QQOfficialAdapter) uploadMedia(ctx context.Context, kind TargetKind, openID string, part outboundMedia) (*MediaInfo, error) {
	if part.url != "" {
		return a.uploader.UploadURL(ctx, kind, openID, part.fileType, part.fileName, part.url)
	}
	data := part.data
	if data == nil && part.path != "" {
		raw, err := os.ReadFile(part.path)
		if err != nil {
			// 错误里必须带上文件名：file:// 前缀已经在上游剥掉了，这里拿到的是真实路径，
			// 报「读不到」时要让人一眼看出是哪一段媒体（多媒体的消息里尤其重要）。
			return nil, fmt.Errorf("%s: 读取本地媒体失败（文件名 %s，本地路径 %s）：%w",
				ConfigKeyPrefix, part.fileName, part.path, err)
		}
		data = raw
	}
	if len(data) == 0 {
		if part.sourceErr != nil {
			// 有明确来源错误就把它原样带出去，「内容为空」只是表象。
			return nil, fmt.Errorf("%s: 媒体内容为空（文件名 %s）：%w", ConfigKeyPrefix, part.fileName, part.sourceErr)
		}
		return nil, fmt.Errorf("%s: 媒体内容为空（文件名 %s，本地路径 %q）", ConfigKeyPrefix, part.fileName, part.path)
	}
	return a.uploader.UploadBytes(ctx, kind, openID, part.fileType, part.fileName, data)
}

// buildOutbound 把 DDBOT 的 message 参数翻成待发形态。
func (a *QQOfficialAdapter) buildOutbound(message interface{}) (*outbound, error) {
	segments, err := toSegments(message)
	if err != nil {
		return nil, err
	}
	out := &outbound{}
	var plain, md strings.Builder

	for _, seg := range segments {
		switch seg.Type {
		case "text":
			text := segString(seg.Data["text"])
			plain.WriteString(text)
			md.WriteString(text)
		case "at":
			// @ 标记写进哪一份正文，按「有没有纯文本渲染的实证」分两路（2026-11-13 修
			// 「带 @全体 的推送被拆成两条」）：
			//
			//   - @全体（<qqbot-at-everyone />）：**plain 与 md 都写**。
			//     实测（2026-11-12）：{"content":"<qqbot-at-everyone /> …","msg_type":0}
			//     发到测试群 http=200，实测确认「确实是全体级艾特」；官方能力表
			//     也写「@全部成员：群聊 文本消息、markdown 消息」。两边写之后 md == plain，
			//     下面就不会产出 out.markdown ⇒ 发送侧 textInline 成立 ⇒ 正文与标记一起
			//     并进第一条媒体（msg_type=7），一次推送只发一条，而不是「文本一条、图片一条」。
			//
			//   - @某个人（<qqbot-at-user id="…" />）：**只写 md**。
			//     官方能力表只写「@用户：群聊 markdown 消息」，我们没有纯文本渲染的一手实证
			//     （参考实现注释记「纯文本里会显示成代码原文」），所以保守维持现状：富媒体条的
			//     content 是纯文本，塞进去只会把标签当正文显示出来。
			target := ""
			if openid, _ := seg.Data["openid"].(string); openid != "" {
				target = openid
			} else {
				switch v := seg.Data["qq"].(type) {
				case string:
					if v == "all" {
						target = "all"
					} else if n, err := strconv.ParseInt(v, 10, 64); err == nil {
						target = a.openIDOf(n)
					}
				case float64:
					target = a.openIDOf(int64(v))
				case int64:
					target = a.openIDOf(v)
				case int32:
					target = a.openIDOf(int64(v))
				case int:
					target = a.openIDOf(int64(v))
				}
			}
			switch {
			case target == "all":
				// @全体 两边都写（理由见本 case 开头的注释）：md 与 plain 相同 ⇒
				// out.markdown 为空 ⇒ 正文并入第一条媒体，一条发完。
				plain.WriteString("<qqbot-at-everyone />")
				md.WriteString("<qqbot-at-everyone />")
			case target != "":
				md.WriteString(`<qqbot-at-user id="` + target + `" />`)
			default:
				logger.Warnf("%s: at 段的目标无法反查成 openid，已忽略这个 @", ConfigKeyPrefix)
			}
		case "reply":
			if ref := a.refIdxOfReplySeq(seg.Data["id"]); ref != "" {
				out.refIdx = ref
			}
		case "image", "record", "video", "file":
			part, ok := mediaFromSegment(seg)
			if !ok {
				logger.Warnf("%s: 媒体段 %q 的来源无法识别，已跳过", ConfigKeyPrefix, seg.Type)
				continue
			}
			out.media = append(out.media, part)
		case "face", "json", "forward":
			logger.Warnf("%s: 段类型 %q 官方接口不支持发送，已忽略", ConfigKeyPrefix, seg.Type)
		default:
			logger.Warnf("%s: 未知段类型 %q，已忽略", ConfigKeyPrefix, seg.Type)
		}
	}

	if md.Len() != 0 && md.String() != plain.String() {
		out.markdown = md.String()
	}
	out.plain = plain.String()
	return out, nil
}

func (a *QQOfficialAdapter) openIDOf(internalID int64) string {
	if internalID == 0 {
		return ""
	}
	openid, _ := a.ids.ToOpenID(a.cfg.AppID, internalID)
	return openid
}

// refIdxOfReplySeq 把 DDBOT 的 reply 段 id（内部 message id）翻成官方的引用索引。
//
// 硬约束：message_reference.message_id **只接受 REFIDX_ 形态**。实测拿 msg_id 去引用
// 接口照样返回 200，但客户端显示「原消息已过期」——所以这里查不到就干脆不引用。
func (a *QQOfficialAdapter) refIdxOfReplySeq(v interface{}) string {
	var id int32
	switch n := v.(type) {
	case float64:
		id = int32(n)
	case int32:
		id = n
	case int64:
		id = int32(n)
	case int:
		id = int32(n)
	case string:
		if parsed, err := strconv.ParseInt(n, 10, 32); err == nil {
			id = int32(parsed)
		}
	}
	if id == 0 {
		return ""
	}
	rec, ok := a.msgs.get(id)
	if !ok || rec.RefIdx == "" {
		logger.Warnf("%s: 要引用的消息 %d 不在登记表里（或没有引用索引），本条不做引用", ConfigKeyPrefix, id)
		return ""
	}
	return rec.RefIdx
}

// mediaFromSegment 从一个媒体段里解析出上传参数。
func mediaFromSegment(seg adapter.MessageSegment) (outboundMedia, bool) {
	source := segString(seg.Data["file"])
	if source == "" {
		source = segString(seg.Data["url"])
	}
	if source == "" {
		return outboundMedia{}, false
	}
	name := segString(seg.Data["name"])
	if name == "" {
		name = segString(seg.Data["filename"])
	}

	switch seg.Type {
	case "image":
		return newMediaFromSource(FileTypeImage, source, name), true
	case "video":
		return newMediaFromSource(FileTypeVideo, source, name), true
	case "record":
		return newMediaFromSource(FileTypeVoice, source, name), true
	default:
		return newMediaFromSource(FileTypeFile, source, name), true
	}
}

// fileURLPrefix 是本地媒体的 URL 形态前缀（lsp/mmsg 产出的就是它）。
const fileURLPrefix = "file://"

// newMediaFromSource 判断来源是 URL、base64 还是本地路径。
//
// file:// 必须单独认（P0）：lsp/mmsg 的 image/video/record/file 元素在 Url 不是 http(s) 时，
// 一律产出 "file://" + 本地路径（image.go:108/132、video.go:70/94、record.go:70/94、
// file.go:90/114），Windows 上还把反斜杠转义成两个。以前这里只认 http(s):// 与 base64://，
// 其余原样塞进 path，uploadMedia 于是拿 os.ReadFile("file:///tmp/a.png") 去读 ——
// 所有本地图片/语音/视频/文件都发不出去。
func newMediaFromSource(fileType int, source, name string) outboundMedia {
	part := outboundMedia{fileType: fileType, fileName: name}

	switch {
	case strings.HasPrefix(source, "http://"), strings.HasPrefix(source, "https://"):
		part.url = source
		if part.fileName == "" {
			part.fileName = fileNameFromURL(source, fileType)
		}
	case strings.HasPrefix(source, "base64://"):
		data, err := decodeBase64(strings.TrimPrefix(source, "base64://"))
		if err != nil {
			// 失败就是失败，但原因不能丢：带进 outboundMedia，由 uploadMedia 拼进最终错误。
			part.sourceErr = fmt.Errorf("base64 媒体解码失败（源长度 %d 字节）：%w", len(source), err)
			break
		}
		part.data = data
		if part.fileName == "" {
			part.fileName = defaultMediaName(fileType)
		}
	case isFileURL(source):
		part.path = localPathFromFileURL(source)
		if part.fileName == "" {
			part.fileName = baseNameAnySep(part.path)
		}
	default:
		part.path = source
		if part.fileName == "" {
			part.fileName = filepath.Base(source)
		}
	}
	if part.fileName == "" {
		part.fileName = defaultMediaName(fileType)
	}
	return part
}

// isFileURL 判断来源是不是 file:// 形态（大小写不敏感：FILE:// 也认）。
func isFileURL(source string) bool {
	return len(source) >= len(fileURLPrefix) && strings.EqualFold(source[:len(fileURLPrefix)], fileURLPrefix)
}

// localPathFromFileURL 把 file:// 形态转回本地路径。处理的三种形态：
//
//	file:///tmp/a.png     -> /tmp/a.png     （空 host 的三斜杠，Unix 常态）
//	file:///C:/tmp/a.png  -> C:/tmp/a.png  （标准 Windows file URL：去掉盘符前的斜杠）
//	file://C:\tmp\a.png   -> C:\tmp\a.png  （lsp 只拼前缀，且把每个反斜杠转义成了两个）
//
// 只剥前缀、**不做** URL 解码：这条链路上产生的是原始路径，凭空 PathUnescape 会把
// 文件名里的 %20 / %2F 弄坏。反斜杠也只有「看起来是 Windows 路径」时才还原，
// 免得把 Unix 上真带反斜杠的文件名改坏。
func localPathFromFileURL(source string) string {
	rest := source[len(fileURLPrefix):]
	if len(rest) >= 3 && rest[0] == '/' && isASCIILetter(rest[1]) && rest[2] == ':' {
		rest = rest[1:]
	}
	if len(rest) >= 2 && isASCIILetter(rest[0]) && rest[1] == ':' {
		rest = strings.ReplaceAll(rest, `\\`, `\`)
	}
	return rest
}

// isASCIILetter 判断是不是 [A-Za-z]（盘符判定用）。
func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// baseNameAnySep 取最后一段路径名，'/' 与 '\' 都当分隔符：
// file:// 剥出来的可能是 Windows 路径，而 filepath.Base 在 Linux 上不认反斜杠。
func baseNameAnySep(p string) string {
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

func decodeBase64(s string) ([]byte, error) {
	if data, err := base64.StdEncoding.DecodeString(s); err == nil {
		return data, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}

func fileNameFromURL(rawURL string, fileType int) string {
	if u, err := url.Parse(rawURL); err == nil {
		if base := filepath.Base(u.Path); base != "." && base != "/" && base != "" {
			return base
		}
	}
	return defaultMediaName(fileType)
}

func defaultMediaName(fileType int) string {
	switch fileType {
	case FileTypeImage:
		return "image.png"
	case FileTypeVideo:
		return "video.mp4"
	case FileTypeVoice:
		return "voice.silk"
	default:
		return "file.bin"
	}
}

// segString 把段数据里的值转成字符串（段数据是 interface{} 的 map，类型不定）。
func segString(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(val, 10)
	case int32:
		return strconv.FormatInt(int64(val), 10)
	case int:
		return strconv.Itoa(val)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", val)
	}
}

// toSegments 把 DDBOT 传进来的 message 参数归一成 []adapter.MessageSegment。
func toSegments(message interface{}) ([]adapter.MessageSegment, error) {
	switch m := message.(type) {
	case nil:
		return nil, fmt.Errorf("%s: message 为 nil", ConfigKeyPrefix)
	case string:
		return []adapter.MessageSegment{{Type: "text", Data: map[string]interface{}{"text": m}}}, nil
	case []adapter.MessageSegment:
		return m, nil
	case adapter.MessageSegment:
		return []adapter.MessageSegment{m}, nil
	case *adapter.SendingMessage:
		if m == nil {
			return nil, fmt.Errorf("%s: SendingMessage 为 nil", ConfigKeyPrefix)
		}
		return elementsToSegments(m.Elements), nil
	case []adapter.IMessageElement:
		return elementsToSegments(m), nil
	case []interface{}:
		out := make([]adapter.MessageSegment, 0, len(m))
		for _, item := range m {
			switch v := item.(type) {
			case adapter.MessageSegment:
				out = append(out, v)
			case *adapter.MessageSegment:
				if v != nil {
					out = append(out, *v)
				}
			case map[string]interface{}:
				segType := segString(v["type"])
				data, _ := v["data"].(map[string]interface{})
				if segType == "" {
					continue
				}
				out = append(out, adapter.MessageSegment{Type: segType, Data: data})
			case adapter.IMessageElement:
				out = append(out, elementsToSegments([]adapter.IMessageElement{v})...)
			default:
				return nil, fmt.Errorf("%s: 不认识的 message 元素类型 %T", ConfigKeyPrefix, item)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: 不支持的 message 类型 %T（支持 string / []MessageSegment / SendingMessage）", ConfigKeyPrefix, message)
	}
}

// elementsToSegments 是 messenger.buildMessageSegments 的逆操作：把元素翻回段。
func elementsToSegments(elements []adapter.IMessageElement) []adapter.MessageSegment {
	out := make([]adapter.MessageSegment, 0, len(elements))
	for _, elem := range elements {
		switch e := elem.(type) {
		case *adapter.TextSegment:
			out = append(out, adapter.MessageSegment{Type: "text", Data: map[string]interface{}{"text": e.Content}})
		case *adapter.AtSegment:
			qq := "all"
			if e.Target != 0 {
				qq = strconv.FormatInt(e.Target, 10)
			}
			out = append(out, adapter.MessageSegment{Type: "at", Data: map[string]interface{}{"qq": qq}})
		case *adapter.ImageSegment:
			source := e.File
			if source == "" {
				source = e.Url
			}
			out = append(out, adapter.MessageSegment{Type: "image", Data: map[string]interface{}{"file": source, "url": e.Url}})
		case *adapter.FaceSegment:
			out = append(out, adapter.MessageSegment{Type: "face", Data: map[string]interface{}{"id": e.Index}})
		case *adapter.ReplySegment:
			out = append(out, adapter.MessageSegment{Type: "reply", Data: map[string]interface{}{"id": e.ReplySeq}})
		case *adapter.VoiceSegment:
			out = append(out, adapter.MessageSegment{Type: "record", Data: map[string]interface{}{"file": e.Url, "name": e.Name}})
		case *adapter.VideoSegment:
			out = append(out, adapter.MessageSegment{Type: "video", Data: map[string]interface{}{"file": e.Url, "name": e.Name}})
		case *adapter.FileSegment:
			source := e.Path
			if source == "" {
				source = e.Url
			}
			out = append(out, adapter.MessageSegment{Type: "file", Data: map[string]interface{}{"file": source, "url": e.Url, "name": e.Name}})
		case *adapter.JsonSegment:
			out = append(out, adapter.MessageSegment{Type: "json", Data: map[string]interface{}{"data": e.Content}})
		case *adapter.ForwardSegment:
			out = append(out, adapter.MessageSegment{Type: "forward", Data: map[string]interface{}{"id": e.ResId}})
		default:
			logger.Warnf("%s: 不认识的元素类型 %T，已忽略", ConfigKeyPrefix, elem)
		}
	}
	return out
}

// ------------------------------------------------------------------ 接口实现

// SendApi 提供一小撮 OneBot 动作的兼容映射（模板里的 sendApi 会用到），其余一律明确报不支持。
func (a *QQOfficialAdapter) SendApi(action string, params map[string]interface{}) (interface{}, error) {
	switch action {
	case "send_group_msg", "send_group_message":
		id, err := paramInt64(params, "group_id")
		if err != nil {
			return nil, err
		}
		msgID, err := a.SendGroupMessage(id, params["message"])
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"status": "ok", "retcode": 0, "message_id": msgID}, nil
	case "send_private_msg", "send_private_message":
		id, err := paramInt64(params, "user_id")
		if err != nil {
			return nil, err
		}
		msgID, err := a.SendPrivateMessage(id, params["message"])
		if err != nil {
			return nil, err
		}
		return map[string]interface{}{"status": "ok", "retcode": 0, "message_id": msgID}, nil
	case "delete_msg":
		id, err := paramInt64(params, "message_id")
		if err != nil {
			return nil, err
		}
		if err := a.RecallMsg(int32(id)); err != nil {
			return nil, err
		}
		return map[string]interface{}{"status": "ok", "retcode": 0}, nil
	default:
		return nil, errUnsupported("SendApi(" + action + ")")
	}
}

func paramInt64(params map[string]interface{}, key string) (int64, error) {
	if params == nil {
		return 0, fmt.Errorf("%s: 缺少参数 %s", ConfigKeyPrefix, key)
	}
	v, ok := params[key]
	if !ok {
		return 0, fmt.Errorf("%s: 缺少参数 %s", ConfigKeyPrefix, key)
	}
	switch n := v.(type) {
	case float64:
		return int64(n), nil
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case int32:
		return int64(n), nil
	case json.Number:
		if parsed, err := n.Int64(); err == nil {
			return parsed, nil
		}
	case string:
		if parsed, err := strconv.ParseInt(n, 10, 64); err == nil {
			return parsed, nil
		}
	}
	return 0, fmt.Errorf("%s: 参数 %s 不是整数（%T）", ConfigKeyPrefix, key, v)
}

// SendGroupMessage 发群消息。message 支持 string / []MessageSegment / *SendingMessage。
func (a *QQOfficialAdapter) SendGroupMessage(groupID int64, message interface{}) (int32, error) {
	return a.sendTo(TargetGroup, groupID, message)
}

// SendPrivateMessage 发单聊消息。
func (a *QQOfficialAdapter) SendPrivateMessage(userID int64, message interface{}) (int32, error) {
	return a.sendTo(TargetUser, userID, message)
}

// GetSelfID 返回机器人自己的**内部 id**（走 IDMap 的那套命名空间）。
//
// 硬约束：必须与事件里 at 段的 target 同源。lsp/parser/parser.go:99-103 判断
// 「这条消息是不是 @ 了我」用的是 AtTarget == utils.GetBot().GetUin()，而 GetUin 就是
// 上层从 lifecycle MetaEvent 里缓存的 SelfID。早先这里返回 READY 里的官方 user.id
// （1234567890123456789），而 at 段 target 是 IDMap 给的小整数 → 等式恒假 →
// 实测现象「@ 了机器人反而不理，没 @ 反而执行」。
//
// 自身 openid 的来源是 GET /v2/groups/{openid}/bot_state 的 member_openid（实测可用）。
// 还没拿到时返回 0，并由 ensureSelfForGroupAsync 后台补；拿到后会补发一次 lifecycle，
// 让 adapter/messenger.go:245 缓存的那个 Uin 跟着更新。
func (a *QQOfficialAdapter) GetSelfID() int64 {
	if a == nil {
		return 0
	}
	return a.selfID.Load()
}

// GetBotName 返回机器人自己的名字（GET /users/@me 的 username）。
//
// 还没拉到、或拉取失败时是空串，调用方（模板）自己兜底。这是**可选扩展能力**
// （和 SendJoinIntro 一样），故意不放进 adapter.Adapter：别的 adapter 不提供名字照常工作。
func (a *QQOfficialAdapter) GetBotName() string {
	if a == nil {
		return ""
	}
	name, _ := a.botName.Load().(string)
	return name
}

// loadBotName 拉一次机器人自己的资料，把 username 记下来给模板用。
//
// 几条约定：
//   - 有限次重试（botNameRetryAttempts），间隔 botNameRetryBackoff；
//   - 空 username 也算失败，绝不用空串覆盖已经存下的值；
//   - 仍然失败就 Warn 一句、留空：只影响模板里的 bot_name，不许影响启动、不许 panic。
//
// Start 里由后台 goroutine 调，所以这里的任何耗时都不挡网关连接。
func (a *QQOfficialAdapter) loadBotName(ctx context.Context) {
	if a == nil || a.client == nil {
		return
	}
	backoff := a.botNameRetryBackoff
	if backoff < 0 {
		backoff = 0
	}

	var lastErr error
	for attempt := 1; attempt <= botNameRetryAttempts; attempt++ {
		name, err := a.client.FetchBotUsername(ctx)
		name = strings.TrimSpace(name)
		if err == nil && name != "" {
			a.botName.Store(name)
			logger.Infof("%s: 已取到机器人自己的名字：%s（GET %s 的 username）",
				ConfigKeyPrefix, name, PathUsersMe)
			return
		}
		if err == nil {
			err = fmt.Errorf("username 为空")
		}
		lastErr = err
		if attempt < botNameRetryAttempts {
			// 可被 ctx 打断：Stop 时不用等满退避。
			if sleepErr := sendSleepCtx(ctx, backoff); sleepErr != nil {
				logger.Debugf("%s: 拉机器人名字被中断：%v", ConfigKeyPrefix, sleepErr)
				return
			}
		}
	}
	logger.Warnf("%s: 拉机器人自己的名字失败（GET %s，试了 %d 次）：%v；模板 {{ bot_name }} 这次留空",
		ConfigKeyPrefix, PathUsersMe, botNameRetryAttempts, lastErr)
}

// isSelfOpenID 判断某个 openid 是不是机器人自己。
func (a *QQOfficialAdapter) isSelfOpenID(openid string) bool {
	if a == nil || openid == "" {
		return false
	}
	a.selfMu.Lock()
	defer a.selfMu.Unlock()
	_, ok := a.selfAliases[openid]
	return ok
}

// botAuthoredReason 判断一条入站消息是不是机器人发的，返回判断依据（空串 = 不是）。
//
// 两条判据都要用，各自覆盖对方的盲区：
//   - 自身 openid 别名表（selfAliases，来自 bot_state 的 member_openid）：最精确，能认出
//     「平台没给 bot 字段」的自身消息；
//   - author.bot：自身 openid 还解析不出来时（启动窗口，或状态文件里一个群都没有）唯一
//     可靠的判据 —— 这个窗口里别名表是空的，只按 openid 比会漏掉自己的发言。
//
// 注意 mentions[].is_you 标记的是「这条消息 @ 了机器人」，不是作者身份，判断「是不是自己
// 发的」不能用它（event.go 里解析了它，但那是给 @ 判定用的）。
func (a *QQOfficialAdapter) botAuthoredReason(p *wsMessagePayload) string {
	if p == nil {
		return ""
	}
	if a.isSelfOpenID(p.Author.identity()) {
		return "作者 openid 命中自身别名表"
	}
	if p.Author.Bot {
		return "author.bot=true"
	}
	return ""
}

// setSelfOpenID 记下一个确认属于机器人的 openid，并在首次确认时定下 selfID。
func (a *QQOfficialAdapter) setSelfOpenID(openid string) {
	if a == nil || openid == "" {
		return
	}
	a.selfMu.Lock()
	if _, ok := a.selfAliases[openid]; ok && a.selfID.Load() > 0 {
		a.selfMu.Unlock()
		return
	}
	a.selfAliases[openid] = struct{}{}
	a.selfMu.Unlock()

	if a.selfID.Load() != 0 || a.ids == nil {
		return
	}
	id := a.ids.ToInt64(a.cfg.AppID, openid)
	if !a.selfID.CompareAndSwap(0, id) {
		return
	}
	logger.Infof("%s: 机器人自身 id 已确定：openid=%s -> 内部 id=%d（与 at 段 target 同命名空间）",
		ConfigKeyPrefix, sendClip(openid), id)
	// Uin 是上层在 lifecycle 事件里一次性缓存的，这里必须补报一次，否则它会一直是旧值。
	a.emitLifecycle()
}

// warnEmptyMemberTables 启动时把「成员表还空着的群」汇总成一条 Warn。
//
// 为什么值得吵这一次：官方群成员接口（/v2/groups/{openid}/members）实测返回 11253，
// 成员表只能靠群事件自学，而启动瞬间它一定是空的。lsp 侧有三处依赖这张表：
//   - 「机器人是不是管理员」（CheckGroupAdministrator → GetBot().GetUin() 要在本群
//     成员表里找到自己）——判定不出来，@全体 就只能退回不带 at 的普通消息；
//   - 群管理命令的权限判定要查发起者在群里的角色；
//   - 成员相关的命令（查人、at 某人）需要成员 openid 与内部 ID 的对应。
//
// 这些在群里第一个人发言之前都可能失败，而用户看到的现象只是「机器人没反应」。
// 所以启动时明说一句，比让人去翻日志找 11253 强。
//
// 每个群只喊一次（emptyMembersWarned）：这条信息重复出现会把真正的告警淹掉。
//
// 暂时性：等腾讯放开上述成员接口，这张表就该改成真实拉取，这段提示连同
// GetGroupMemberList 的降级说明一起删掉；届时「有人发言前判定不出来」这个现象
// 也就不存在了。
func (a *QQOfficialAdapter) warnEmptyMemberTables() {
	if a == nil || a.groups == nil {
		return
	}
	groups := a.groups.snapshot()
	if len(groups) == 0 {
		return
	}
	var empty []string
	a.memberSinkMu.Lock()
	for _, g := range groups {
		if g.memberCount > 0 || a.emptyMembersWarned[g.ID] {
			continue
		}
		a.emptyMembersWarned[g.ID] = true
		empty = append(empty, fmt.Sprintf("%s（内部群号 %d）", sendClip(g.OpenID), g.ID))
	}
	a.memberSinkMu.Unlock()
	if len(empty) == 0 {
		return
	}
	logger.Warnf("%s: %d 个群的成员表还是空的：%s。官方通道拿不到群成员列表（成员接口实测 11253），"+
		"只认识在群里发过言的人 —— 因此「机器人是不是管理员」「群管理命令」「@全体」在有人发言之前"+
		"可能判定不出来，群里有人说话之后会自动补上。",
		ConfigKeyPrefix, len(empty), strings.Join(empty, "、"))
}

// bootstrapSelfOpenID 启动时解析自身 openid：第一个群同步（决定 SelfID），其余后台。
func (a *QQOfficialAdapter) bootstrapSelfOpenID() {
	groups := a.groups.snapshot()
	if len(groups) == 0 {
		logger.Warnf("%s: 状态文件里还没有任何群，暂时无法确定机器人自身 openid；"+
			"收到第一个群事件后会补上 —— 在那之前本群的 @ 命令可能不被识别为「@ 我」", ConfigKeyPrefix)
		return
	}
	for i, g := range groups {
		if i == 0 {
			a.selfMu.Lock()
			a.selfAttempted[g.OpenID] = time.Now()
			a.selfMu.Unlock()
			if a.resolveSelfFromGroup(g.OpenID) {
				a.selfMu.Lock()
				a.selfResolved[g.OpenID] = true
				a.selfMu.Unlock()
			}
			continue
		}
		a.ensureSelfForGroupAsync(g.OpenID)
	}
}

// ensureSelfForGroupAsync 后台为某个群解析自身 openid（每个群最多一次成功尝试，失败 5 分钟后再试）。
func (a *QQOfficialAdapter) ensureSelfForGroupAsync(groupOpenID string) {
	if a == nil || groupOpenID == "" {
		return
	}
	a.selfMu.Lock()
	if a.selfResolved[groupOpenID] {
		a.selfMu.Unlock()
		return
	}
	if last, ok := a.selfAttempted[groupOpenID]; ok && time.Since(last) < 5*time.Minute {
		a.selfMu.Unlock()
		return
	}
	a.selfAttempted[groupOpenID] = time.Now()
	firstTime := a.selfID.Load() == 0
	a.selfMu.Unlock()

	if firstTime {
		logger.Warnf("%s: 还不知道机器人自身的 openid，正在后台通过群 %s 的 bot_state 获取；"+
			"本群首条 @ 命令可能不被识别为「@ 我」", ConfigKeyPrefix, sendClip(groupOpenID))
	}
	go func() {
		if !a.resolveSelfFromGroup(groupOpenID) {
			return
		}
		a.selfMu.Lock()
		a.selfResolved[groupOpenID] = true
		a.selfMu.Unlock()
	}()
}

// resolveSelfFromGroup 用 GET /v2/groups/{openid}/bot_state 拿机器人在该群的 member_openid。
//
// 实测该接口可用（不是文档说的 11253 白名单），返回里还有 allow_proactive_msg /
// recv_msg_setting / member_role —— 顺手记下来，部署时排查主动消息权限有用。
func (a *QQOfficialAdapter) resolveSelfFromGroup(groupOpenID string) bool {
	if groupOpenID == "" {
		return false
	}
	ctx, cancel := a.apiCtx()
	defer cancel()

	var resp struct {
		MemberOpenID      string `json:"member_openid"`
		MemberRole        string `json:"member_role"`
		AllowProactiveMsg bool   `json:"allow_proactive_msg"`
		RecvMsgSetting    string `json:"recv_msg_setting"`
	}
	if err := a.client.GetJSON(ctx, fmt.Sprintf(PathGroupBotState, groupOpenID), &resp); err != nil {
		logger.Warnf("%s: 取群 %s 的机器人状态失败（拿不到自身 openid，@ 判定会不可靠）：%v",
			ConfigKeyPrefix, sendClip(groupOpenID), err)
		return false
	}
	if resp.MemberOpenID == "" {
		logger.Warnf("%s: 群 %s 的 bot_state 没返回 member_openid，无法确定自身 openid", ConfigKeyPrefix, sendClip(groupOpenID))
		return false
	}
	logger.Infof("%s: 群 %s 机器人状态：member_role=%s allow_proactive_msg=%v recv_msg_setting=%q",
		ConfigKeyPrefix, sendClip(groupOpenID), resp.MemberRole, resp.AllowProactiveMsg, resp.RecvMsgSetting)
	// 没有主动消息权限的群，订阅推送会全部发不出去（实测见 warnProactiveDeniedOnce）。
	// 启动期就把这句话喊出来，别等人去翻 40034105 的日志。
	a.warnProactiveDeniedOnce(groupOpenID, resp.AllowProactiveMsg)
	a.setSelfOpenID(resp.MemberOpenID)
	// 顺手把机器人自己写进该群的成员表 —— 这是 @全体能发出去的前提。
	// 上层 lsp/notify.go:102-107 判定「机器人是不是管理员」读的是 messenger 的成员缓存，
	// 而这份缓存由本 adapter 的成员表灌进去；成员表只收事件里出现过的人，
	// 机器人自己永远不会出现在事件里，于是它恒不在表里 ⇒ @全体被静默跳过。
	// bot_state 的 member_openid + member_role 正好补上这一行。
	a.learnSelfMember(groupOpenID, resp.MemberOpenID, resp.MemberRole)
	return true
}

// warnProactiveDeniedOnce 在 bot_state 说「没有主动消息权限」时喊一条能照着做的 Warn。
//
// 判据**只看 allow_proactive_msg**，不看 recv_msg_setting：实测新群的 recv_msg_setting
// 本来就是空串（有权限的群是 "all"），把空串当成「没开权限」会误报一堆新群。
//
// 每个群只喊一次（proactiveWarned）：这是配置问题不是故障，重复出现只会把真正的告警淹掉。
// 但**不放弃这个群**：群主随时可能把开关打开，后续推送照常尝试（只是不重试、不刷屏）。
func (a *QQOfficialAdapter) warnProactiveDeniedOnce(groupOpenID string, allowProactive bool) {
	if a == nil || groupOpenID == "" || allowProactive {
		return
	}
	a.memberSinkMu.Lock()
	if a.proactiveWarned[groupOpenID] {
		a.memberSinkMu.Unlock()
		return
	}
	if a.proactiveWarned == nil {
		a.proactiveWarned = make(map[string]bool)
	}
	a.proactiveWarned[groupOpenID] = true
	a.memberSinkMu.Unlock()

	groupID := int64(0)
	name := ""
	if a.groups != nil {
		if rec, ok := a.groups.getByOpenID(groupOpenID); ok && rec != nil {
			groupID, name = rec.ID, rec.Name
		}
	}
	if groupID == 0 && a.ids != nil && a.cfg != nil {
		groupID = a.ids.ToInt64(a.cfg.AppID, groupOpenID)
	}

	// 群名拿得到就用群名（人去设置里找的是名字），拿不到就用内部群号兜底——
	// 总之这条告警必须点明是哪个群，否则等于没喊。
	var label string
	switch {
	case name != "":
		label = fmt.Sprintf("群「%s」（%s，内部群号 %d）", name, sendClip(groupOpenID), groupID)
	case groupID != 0:
		label = fmt.Sprintf("群 %s（内部群号 %d）", sendClip(groupOpenID), groupID)
	default:
		label = "群 " + sendClip(groupOpenID)
	}
	logger.Warnf("%s: %s 的 allow_proactive_msg=false —— 这个群没开「允许机器人主动发送消息」，"+
		"订阅推送会全部发不出去（平台对主动消息回 code=40034105「主动消息失败, 无权限」）。"+
		"请群主在群设置的「机器人权限设置」里打开它；打开后无需重启，后续推送会照常尝试"+
		"（本适配层只是不再重试、不再刷屏）。",
		ConfigKeyPrefix, label)
}

// selfMemberNickname 是机器人自己写进成员表时的昵称。
//
// 不能留空：空昵称会让 {{ memberList }} 模板里出现一条没有名字的成员。
const selfMemberNickname = "机器人"

// learnSelfMember 把机器人自己登记进某个群的成员表，并在首次写入/角色变化时刷上层缓存。
//
// 三条约定：
//  1. 昵称固定 selfMemberNickname，不许空；
//  2. 角色字符串**原样透传**——admin/owner/member 到 MemberPermission 的映射统一由
//     memberInfoFromRecord 做，这里再映射一遍会漂（那也是「角色被悄悄改掉」的经典来源）；
//  3. 群还没登记进 registry 时只记 Warn 就跳过：这条路径跑在后台解析里，
//     不许 panic，也不许把「群清单里还没有它」升级成 bot_state 的失败。
//
// 成员 id 就按该群的 member_openid 经 IDMap 推导，**不需要**额外指定 SelfID：
// 实测（2026-11-12，个人机 1900000000 / 企业机 1900000001）同一个机器人在任意两个群
// 拿到的 bot_state.member_openid 完全一样 ——
//
//	群 0F6E… -> 01A2B3C4D5E6F708192A3B4C5D6E7F80
//	群 88D0… -> 01A2B3C4D5E6F708192A3B4C5D6E7F80   （同一台个人机，两个群同一个值）
//
// 也就是说 member_openid 是 **per-bot**，不是 per-group。所以每个群各算一遍，结果天然
// 等于 GetSelfID()：上层判定「机器人是不是管理员」（lsp/notify.go 的
// CheckGroupAdministrator → GetBot().GetUin()）用全局 SelfID 去查，在每个群都查得到。
// 早先「多群时按 member_openid 推导会得到不同 id、非首发群的 @全体 会静默失效」的担心
// 建立在「member_openid 是群内身份」这个错误前提上，那一层强制指定 id 的逻辑已撤掉。
//
// SelfID 本身的语义不变（@ 反查、自身发言识别都要它）：它由 setSelfOpenID 从同一枚
// member_openid 推出，与这里的推导同源，两边永远一致。
func (a *QQOfficialAdapter) learnSelfMember(groupOpenID, memberOpenID, memberRole string) {
	if a == nil || a.groups == nil || groupOpenID == "" || memberOpenID == "" {
		return
	}
	rec, ok := a.groups.getByOpenID(groupOpenID)
	if !ok {
		logger.Warnf("%s: 群 %s 还没登记进群清单，机器人自身（member_openid=%s）这次不写进成员表",
			ConfigKeyPrefix, sendClip(groupOpenID), sendClip(memberOpenID))
		return
	}
	groupID := rec.ID
	if groupID == 0 {
		groupID = a.ids.ToInt64(a.cfg.AppID, groupOpenID)
	}
	if groupID == 0 {
		logger.Warnf("%s: 群 %s 取不到内部 id，机器人自身这次不写进成员表",
			ConfigKeyPrefix, sendClip(groupOpenID))
		return
	}

	memberID, role, changed := a.groups.learnMember(groupOpenID, wsAuthor{
		MemberOpenID: memberOpenID,
		Username:     selfMemberNickname,
		MemberRole:   memberRole,
	}, a.ids, a.cfg.AppID)
	if memberID == 0 {
		logger.Warnf("%s: 群 %s 里机器人自身（member_openid=%s）登记后拿不到内部 id，跳过同步",
			ConfigKeyPrefix, sendClip(groupOpenID), sendClip(memberOpenID))
		return
	}
	if !changed {
		return
	}
	logger.Infof("%s: 机器人自身已写进群 %s 的成员表（成员 id=%d role=%q），@全体判定从此能读到它",
		ConfigKeyPrefix, sendClip(groupOpenID), memberID, role)
	a.syncGroupMember(groupID, memberID, role)
}

// GetAdapterName 返回 adapter 名。
func (a *QQOfficialAdapter) GetAdapterName() string { return AdapterName }

// IsConnected 报告网关是否在线。
func (a *QQOfficialAdapter) IsConnected() bool {
	if a == nil || a.gateway == nil {
		return false
	}
	return a.started.Load() && a.gateway.Connected()
}

func (a *QQOfficialAdapter) OnGroupMessage(handler func(*adapter.GroupMessageEvent)) {
	a.handlerMu.Lock()
	a.onGroupMessage = handler
	a.handlerMu.Unlock()
}

func (a *QQOfficialAdapter) OnPrivateMessage(handler func(*adapter.PrivateMessageEvent)) {
	a.handlerMu.Lock()
	a.onPrivateMessage = handler
	a.handlerMu.Unlock()
}

func (a *QQOfficialAdapter) OnMetaEvent(handler func(*adapter.MetaEvent)) {
	a.handlerMu.Lock()
	a.onMetaEvent = handler
	a.handlerMu.Unlock()
}

func (a *QQOfficialAdapter) OnNoticeEvent(handler func(*adapter.NoticeEvent)) {
	a.handlerMu.Lock()
	a.onNoticeEvent = handler
	a.handlerMu.Unlock()
}

func (a *QQOfficialAdapter) OnRequestEvent(handler func(*adapter.RequestEvent)) {
	a.handlerMu.Lock()
	a.onRequestEvent = handler
	a.handlerMu.Unlock()
}

// GetGroupList 返回自建的群清单。
//
// **绝不返回 error**：lsp 启动会死等列表加载完成（adapter/messenger.go:1192-1252），
// 空表也要算加载成功，否则订阅系统永远不启动。
func (a *QQOfficialAdapter) GetGroupList() ([]*adapter.GroupInfo, error) {
	recs := a.groups.snapshot()
	out := make([]*adapter.GroupInfo, 0, len(recs))
	for _, rec := range recs {
		out = append(out, &adapter.GroupInfo{
			Uin:            rec.ID,
			Code:           rec.ID,
			GroupID:        rec.ID,
			Name:           rec.Name,
			GroupName:      rec.Name,
			MemberCount:    rec.MemberCount,
			MaxMemberCount: rec.MaxMemberCount,
		})
	}
	return out, nil
}

// GetGroupMemberList 返回「从事件里见过的」群成员。
//
// 降级说明：官方群成员列表接口（/v2/groups/{openid}/members）实测 11253 拿不到，
// 所以这里只能给事件里出现过的成员（author.username + member_role）。
// 返回 nil error 是硬要求：一旦返回 error，messenger 的列表加载判定就会失败，
// 启动流程会卡住（reloadLists 里成员加载失败即整体失败）。
func (a *QQOfficialAdapter) GetGroupMemberList(groupID int64) ([]*adapter.GroupMemberInfo, error) {
	members := a.groups.members(groupID)
	out := make([]*adapter.GroupMemberInfo, 0, len(members))
	for _, m := range members {
		out = append(out, memberInfoFromRecord(groupID, *m))
	}
	if len(out) == 0 {
		logger.Debugf("%s: 群 %d 的成员表还是空的（官方成员接口不可用，只能靠事件积累）", ConfigKeyPrefix, groupID)
	}
	return out, nil
}

func memberInfoFromRecord(groupID int64, m memberRecord) *adapter.GroupMemberInfo {
	perm := adapter.Member
	switch strings.ToLower(m.Role) {
	case "owner":
		perm = adapter.Owner
	case "admin":
		perm = adapter.Administrator
	}
	return &adapter.GroupMemberInfo{
		GroupID:      groupID,
		UserID:       m.ID,
		Uin:          m.ID,
		Nickname:     m.Nickname,
		Card:         "",
		CardName:     "",
		Role:         m.Role,
		Permission:   perm,
		LastSentTime: m.LastSeen,
	}
}

// GetFriendList 返回「见过的单聊用户」。
//
// 同样**绝不返回 error**（启动流程硬要求）；官方没有好友列表接口，
// 靠 C2C_MESSAGE_CREATE / FRIEND_ADD 事件积累，重启后从空开始。
func (a *QQOfficialAdapter) GetFriendList() ([]*adapter.FriendInfo, error) {
	recs := a.friends.snapshot()
	out := make([]*adapter.FriendInfo, 0, len(recs))
	for _, rec := range recs {
		out = append(out, &adapter.FriendInfo{
			Uin:      rec.ID,
			UserID:   rec.ID,
			Nickname: rec.Nickname,
		})
	}
	return out, nil
}

// GetStrangerInfo 只能给出本地登记过的信息（官方没有按 openid 查用户的接口）。
func (a *QQOfficialAdapter) GetStrangerInfo(userID int64) (map[string]interface{}, error) {
	openid, ok := a.ids.ToOpenID(a.cfg.AppID, userID)
	if !ok || openid == "" {
		return nil, fmt.Errorf("%w：GetStrangerInfo（官方没有按 openid 查用户资料的接口）", ErrUnsupported)
	}
	out := map[string]interface{}{
		"user_id": userID,
		"uin":     userID,
		"openid":  openid,
	}
	if rec, ok := a.friends.get(userID); ok {
		out["nickname"] = rec.Nickname
	}
	return out, nil
}

// GetGroupInfo 返回群信息：本地记录有了就直接用，名字还没取到就现场调一次 /info。
//
// 取不到就**报错**，与 GetStrangerInfo / GetGroupMemberInfo「查不到就说查不到」一致：
// 以前 fetchGroupInfo 只 Warn 就返回，上层拿到空群名当成功，日志里看不出「这次其实没拿到」。
//
// 影响面（改前 grep 过全仓）：adapter.Adapter.GetGroupInfo 的调用方只有
// adapter/messenger.go:1446（机器人自己被拉进群时取群信息），它拿到 error 会 Warn 并
// 用 `&GroupInfo{Uin:..., Code:...}` 兜底继续，**不依赖「拿不到也不报错」**；
// bot.Bot.GetGroupInfo 只是透传，仓内没有其它调用点。所以这里报错是安全的。
func (a *QQOfficialAdapter) GetGroupInfo(groupID int64) (*adapter.GroupInfo, error) {
	rec, ok := a.groups.get(groupID)
	if !ok {
		return nil, fmt.Errorf("%s: 未知群号 %d（不在自建群清单里；官方没有「列出我加入的群」接口，"+
			"只能等机器人被拉进群或收到群消息）", ConfigKeyPrefix, groupID)
	}
	if !rec.InfoKnown {
		err := a.fetchGroupInfo(rec.OpenID)
		if updated, ok := a.groups.get(groupID); ok {
			rec = updated
		}
		if !rec.InfoKnown {
			if err == nil {
				// fetchGroupInfo 成功却没落下信息：只可能是群记录在途中被删了。
				err = fmt.Errorf("接口调用没有报错，但群记录已经不在了（群可能刚被移除）")
			}
			return nil, fmt.Errorf("%s: 取不到群 %d 的信息（group_openid=%s；官方群信息接口可能要 "+
				"11253 白名单）：%w", ConfigKeyPrefix, groupID, rec.OpenID, err)
		}
	}
	return &adapter.GroupInfo{
		Uin:            rec.ID,
		Code:           rec.ID,
		GroupID:        rec.ID,
		Name:           rec.Name,
		GroupName:      rec.Name,
		MemberCount:    rec.MemberCount,
		MaxMemberCount: rec.MaxMemberCount,
		Members:        nil,
	}, nil
}

// GetGroupMemberInfo 只能给出事件里见过的成员。
func (a *QQOfficialAdapter) GetGroupMemberInfo(groupID, userID int64) (*adapter.GroupMemberInfo, error) {
	m, ok := a.groups.member(groupID, userID)
	if !ok {
		return nil, fmt.Errorf("%s: 群 %d 里没有成员 %d 的记录（官方成员查询接口报 11253，"+
			"只有在本群发过言的成员才会被事件学到）", ConfigKeyPrefix, groupID, userID)
	}
	return memberInfoFromRecord(groupID, *m), nil
}

// DownloadFile 下载文件到本地临时目录，返回路径。
//
// url 与 base64 二选一（都为空报错）。headers 是 "Key: Value" 形态的附加头。
func (a *QQOfficialAdapter) DownloadFile(url, base64Data, name string, headers []string) (string, error) {
	var data []byte
	safeName := sanitizeFileName(name)
	switch {
	case url != "":
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return "", fmt.Errorf("%s: 构造下载请求失败：%w", ConfigKeyPrefix, err)
		}
		for _, h := range headers {
			if k, v, ok := strings.Cut(h, ":"); ok {
				req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
			}
		}
		client := a.downloadClient
		if client == nil {
			client = &http.Client{Timeout: downloadTimeout}
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", fmt.Errorf("%s: 下载 %s 失败：%w", ConfigKeyPrefix, url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("%s: 下载 %s 返回 HTTP %d", ConfigKeyPrefix, url, resp.StatusCode)
		}
		limit := a.downloadLimit
		if limit <= 0 {
			limit = maxDownloadBytes
		}
		// 多读 1 个字节：读满 limit 之后还能再读到一个字节，说明响应体比上限长，
		// 这一次下载被截断了。比「只看 Content-Length」严一档：分块传输没有
		// Content-Length 时同样能抓到。
		body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		if err != nil {
			return "", fmt.Errorf("%s: 读下载内容失败：%w", ConfigKeyPrefix, err)
		}
		if int64(len(body)) > limit {
			// 截断绝对不能当成功：上层会把半个文件当成品去解析/上传，错误埋得很深。
			return "", fmt.Errorf("%s: 下载 %s 超过 %d 字节上限（Content-Length=%d），已中止且未写入文件；"+
				"这不是「文件小一点」，而是内容被截断、不完整",
				ConfigKeyPrefix, url, limit, resp.ContentLength)
		}
		data = body
		if safeName == "" {
			safeName = fileNameFromURL(url, FileTypeFile)
		}
	case base64Data != "":
		raw := strings.TrimPrefix(base64Data, "base64://")
		decoded, err := decodeBase64(raw)
		if err != nil {
			return "", fmt.Errorf("%s: base64 内容解码失败：%w", ConfigKeyPrefix, err)
		}
		data = decoded
		if safeName == "" {
			safeName = "file.bin"
		}
	default:
		return "", fmt.Errorf("%s: DownloadFile 需要 url 或 base64 其中之一", ConfigKeyPrefix)
	}

	dir := filepath.Join(os.TempDir(), downloadDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("%s: 创建下载目录 %s 失败：%w", ConfigKeyPrefix, dir, err)
	}
	finalName := fmt.Sprintf("%d-%s", time.Now().UnixNano(), safeName)
	full := filepath.Join(dir, finalName)
	if err := os.WriteFile(full, data, 0o600); err != nil {
		return "", fmt.Errorf("%s: 写下载文件 %s 失败：%w", ConfigKeyPrefix, full, err)
	}
	return full, nil
}

// sanitizeFileName 去掉路径成分，避免 name 里带 ../ 写到目录外。
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, string(os.PathSeparator), "_")
	if name == "." || name == ".." || name == "/" {
		return ""
	}
	return name
}

// GetFileUrl 官方不给文件直链（文件消息只到 file_info 为止），返回空串。
func (a *QQOfficialAdapter) GetFileUrl(groupCode int64, fileId string) string {
	logger.Debugf("%s: GetFileUrl 在官方平台上没有对应能力，返回空", ConfigKeyPrefix)
	return ""
}

// GetMsg 从本地登记表里取消息详情。
//
// 官方没有「按 id 查消息」的接口，所以只能给出本进程发过/收过的消息；查不到就报错，
// 不返回零值假装成功。
func (a *QQOfficialAdapter) GetMsg(msgId int32) (*adapter.GetMsgResult, error) {
	rec, ok := a.msgs.get(msgId)
	if !ok {
		return nil, fmt.Errorf("%s: 消息 %d 不在本地登记表里（官方无按 id 查消息的接口，"+
			"只能查本进程发过或收过、且还在表内（%d 条）的消息）", ConfigKeyPrefix, msgId, a.msgs.len())
	}
	return &adapter.GetMsgResult{
		MessageID:  int64(rec.ID),
		GroupID:    rec.GroupID,
		UserID:     rec.UserID,
		RawMessage: rec.Content,
		Time:       rec.Time,
		Sender: &adapter.SenderInfo{
			UserID:   rec.UserID,
			Uin:      rec.UserID,
			Nickname: rec.Username,
		},
	}, nil
}

// GetMsgOrg 不支持：官方没有原始消息体查询接口。
func (a *QQOfficialAdapter) GetMsgOrg(msgId int32) (interface{}, error) {
	return nil, errUnsupported("GetMsgOrg")
}

// RecallMsg 撤回本机器人发的消息。
//
// 官方限制：只能撤自己发的、且**超过 2 分钟不可撤回**（10 QPS）。
func (a *QQOfficialAdapter) RecallMsg(msgId int32) error {
	rec, ok := a.msgs.get(msgId)
	if !ok {
		return fmt.Errorf("%s: 消息 %d 不在本地登记表里，无法撤回", ConfigKeyPrefix, msgId)
	}
	if !rec.Outgoing {
		return fmt.Errorf("%s: 消息 %d 不是本机器人发的，官方只允许撤回自己发的消息", ConfigKeyPrefix, msgId)
	}
	if rec.RawID == "" {
		return fmt.Errorf("%s: 消息 %d 没有原始 msg_id，无法撤回", ConfigKeyPrefix, msgId)
	}
	if time.Since(time.Unix(rec.Time, 0)) > 2*time.Minute {
		logger.Warnf("%s: 消息 %d 已经超过 2 分钟，官方会拒绝撤回", ConfigKeyPrefix, msgId)
	}
	ctx, cancel := a.apiCtx()
	defer cancel()
	return a.client.DeleteMessage(ctx, Target{Kind: rec.Kind, OpenID: rec.OpenID}, rec.RawID)
}

// GroupPoke 不支持：官方没有拍一拍接口。
func (a *QQOfficialAdapter) GroupPoke(groupCode, target int64) error {
	return errUnsupported("GroupPoke")
}

// FriendPoke 不支持：官方没有拍一拍接口。
func (a *QQOfficialAdapter) FriendPoke(target int64) error {
	return errUnsupported("FriendPoke")
}

// SetGroupBan 禁言/解除禁言群成员。
//
// 官方：POST /v2/groups/{openid}/restrict_chat_setting，机器人需是群管理员，
// 最长 30 天，单次最多 20 人，**只能操作普通成员**（群主/管理员/机器人不行）。
// duration <= 0 表示立即解除禁言。
func (a *QQOfficialAdapter) SetGroupBan(groupCode, memberUin int64, duration int64) error {
	groupOpenID, ok := a.ids.ToOpenID(a.cfg.AppID, groupCode)
	if !ok || groupOpenID == "" {
		return fmt.Errorf("%s: 未知群号 %d，无法禁言", ConfigKeyPrefix, groupCode)
	}
	memberOpenID, ok := a.ids.ToOpenID(a.cfg.AppID, memberUin)
	if !ok || memberOpenID == "" {
		return fmt.Errorf("%s: 未知成员 %d，无法禁言", ConfigKeyPrefix, memberUin)
	}

	item := map[string]interface{}{
		"op":            "add",
		"member_openid": memberOpenID,
	}
	if duration > 0 {
		if maxBan := int64(30 * 24 * time.Hour / time.Second); duration > maxBan {
			duration = maxBan
		}
		item["mute_expire_at"] = time.Now().Add(time.Duration(duration) * time.Second).Format(time.RFC3339)
	} else {
		item["op"] = "del"
		item["mute_expire_at"] = "" // 立即解除
	}

	body := map[string]interface{}{"members": []interface{}{item}}
	ctx, cancel := a.apiCtx()
	defer cancel()
	return a.client.PostJSON(ctx, fmt.Sprintf(PathGroupRestrictChatSetting, groupOpenID), body, nil)
}

// SetGroupWholeBan 不支持：官方 restrict_chat_setting 只能设置**成员级**禁言，
// 全员禁言在查询接口里只是一条只读的 global_rule。
func (a *QQOfficialAdapter) SetGroupWholeBan(groupCode int64, enable bool) error {
	return errUnsupported("SetGroupWholeBan（官方只有成员级禁言，没有全员禁言开关）")
}

// KickGroupMember 移出群成员。
//
// 官方：POST /v2/groups/{openid}/batch_remove_members（30 QPM），实测该接口属四类
// 11253 白名单能力之一——照常实现并调用，拿到权限错误就由上层记录，别在编译期砍掉。
func (a *QQOfficialAdapter) KickGroupMember(groupCode int64, memberUin int64, rejectAddRequest bool) error {
	groupOpenID, ok := a.ids.ToOpenID(a.cfg.AppID, groupCode)
	if !ok || groupOpenID == "" {
		return fmt.Errorf("%s: 未知群号 %d，无法移出成员", ConfigKeyPrefix, groupCode)
	}
	memberOpenID, ok := a.ids.ToOpenID(a.cfg.AppID, memberUin)
	if !ok || memberOpenID == "" {
		return fmt.Errorf("%s: 未知成员 %d，无法移出成员", ConfigKeyPrefix, memberUin)
	}
	if rejectAddRequest {
		logger.Warnf("%s: 官方 batch_remove_members 的请求体里没有「拒绝再次加群」字段（文档未列），该参数被忽略",
			ConfigKeyPrefix)
	}
	body := map[string]interface{}{"member_openids": []string{memberOpenID}}
	ctx, cancel := a.apiCtx()
	defer cancel()
	return a.client.PostJSON(ctx, fmt.Sprintf(PathGroupBatchRemoveMembers, groupOpenID), body, nil)
}

// SetGroupLeave 不支持：官方没有「机器人主动退群」接口。
func (a *QQOfficialAdapter) SetGroupLeave(groupCode int64, isDismiss bool) error {
	return errUnsupported("SetGroupLeave")
}

// SetGroupAdmin 不支持：官方没有设置群管理员的接口。
func (a *QQOfficialAdapter) SetGroupAdmin(groupCode, memberUin int64, enable bool) error {
	return errUnsupported("SetGroupAdmin")
}

// EditGroupCard 不支持：官方没有改群名片的接口。
func (a *QQOfficialAdapter) EditGroupCard(groupCode, memberUin int64, card string) error {
	return errUnsupported("EditGroupCard")
}

// EditGroupTitle 不支持：官方没有改群头衔的接口。
func (a *QQOfficialAdapter) EditGroupTitle(groupCode, memberUin int64, title string) error {
	return errUnsupported("EditGroupTitle")
}

// SupportsForward 明确告诉 lsp：本 adapter **不支持**合并转发。
//
// 官方 v2 发消息只支持 msg_type 0/2/7/8，没有合并转发，SendGroupForwardMessage /
// SendPrivateForwardMessage 只能返回 unsupported。这个方法是给 lsp 的**可选能力接口**
// （interface{ SupportsForward() bool }）用的：lsp 看到 false 就把节点内容合并成一条
// 普通文本消息发出去，而不是让整条推送直接失败。
//
// 刻意不放进 adapter.Adapter：onebot / satori 不实现它，lsp 侧断言不到就按支持处理，
// 行为与以前完全一致。
func (a *QQOfficialAdapter) SupportsForward() bool { return false }

// SendGroupForwardMessage 不支持：官方 v2 发消息只支持 msg_type 0/2/7/8，没有合并转发。
func (a *QQOfficialAdapter) SendGroupForwardMessage(groupCode int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	return 0, "", errUnsupported("SendGroupForwardMessage（官方没有合并转发消息）")
}

// SendPrivateForwardMessage 不支持，理由同上。
func (a *QQOfficialAdapter) SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	return 0, "", errUnsupported("SendPrivateForwardMessage（官方没有合并转发消息）")
}
