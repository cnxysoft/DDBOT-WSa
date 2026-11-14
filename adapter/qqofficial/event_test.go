package qqofficial

// adapter/event 层的单测：用**实测事件体**（真实网关推送）钉住解析契约。
//
// 为什么必须补这块：GROUP_AT_MESSAGE_CREATE 只有真人 @ 才会推过来，端到端里触发不了
// （机器人之间 @ 只会产生 GROUP_MESSAGE_CREATE），所以 AT 路径的正确性靠这里守。

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

const (
	testAppID     = "1900000000"
	testGroupOpen = "03A2B3C4D5E6F708192A3B4C5D6E7F82"
	testOwnerOpen = "05A2B3C4D5E6F708192A3B4C5D6E7F84"
	testOtherOpen = "06A2B3C4D5E6F708192A3B4C5D6E7F85"
	testSelfID    = int64(1234567890123456789)
)

// newTestAdapter 建一个不连网关、状态文件落在临时目录的 adapter。
func newTestAdapter(t *testing.T) *QQOfficialAdapter {
	t.Helper()
	config.GlobalConfig.Set(ConfigKeyPrefix+"."+ConfigKeyStateFile, filepath.Join(t.TempDir(), "state.json"))
	config.GlobalConfig.Set(ConfigKeyPrefix+"."+ConfigKeyAppID, testAppID)
	config.GlobalConfig.Set(ConfigKeyPrefix+"."+ConfigKeyAppSecret, "test-secret")

	a, ok := NewQQOfficialAdapter(&adapter.AdapterConfig{Mode: string(adapter.AdapterTypeQQOfficial)}).(*QQOfficialAdapter)
	if !ok || a == nil {
		t.Fatal("NewQQOfficialAdapter 必须返回 *QQOfficialAdapter")
	}
	// 故意不预置 SelfID：它必须由 setSelfOpenID 从 bot_state 的成员 openid 经 IDMap 定出来，
	// 预置成官方 user.id 正是「@ 了反而不理」的坑（GetSelfID 也已经不再去打 /users/@me）。
	return a
}

// selfOpenInGroup 是实测里机器人在测试群自己的 member_openid（个人机器人视角）。
const selfOpenInGroup = "01A2B3C4D5E6F708192A3B4C5D6E7F80"

// 实测的 GROUP_AT_MESSAGE_CREATE：content 去掉 @ 前缀后残留一个前导空格。
const groupAtPayload = `{
  "author": {"bot": false, "id": "05A2B3C4D5E6F708192A3B4C5D6E7F84",
             "member_openid": "05A2B3C4D5E6F708192A3B4C5D6E7F84",
             "member_role": "owner", "union_openid": "", "username": "kizunerwe"},
  "content": " hi",
  "group_id": "03A2B3C4D5E6F708192A3B4C5D6E7F82",
  "group_openid": "03A2B3C4D5E6F708192A3B4C5D6E7F82",
  "id": "ROBOT1.0_pJwMR5A4R2bYVXiPJsy482uKDNZgsr.Wi5z0PuSJHkVZ",
  "message_scene": {"ext": ["msg_idx=REFIDX_TESTREFIDX", "auth_token=TESTTOKEN"], "source": "default"},
  "message_type": 0,
  "timestamp": "2026-11-11T16:32:27+08:00"
}`

func TestQQOfficialGroupAtEventParsing(t *testing.T) {
	a := newTestAdapter(t)
	a.setSelfOpenID(selfOpenInGroup)

	var mu sync.Mutex
	var got []*adapter.GroupMessageEvent
	a.OnGroupMessage(func(ev *adapter.GroupMessageEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	})

	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload))

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("应收到 1 个群消息事件，实际 %d", len(got))
	}
	ev := got[0]
	if want := a.ids.ToInt64(testAppID, testGroupOpen); ev.GroupID != want {
		t.Errorf("群号应映射为 %d，实际 %d", want, ev.GroupID)
	}
	if want := a.ids.ToInt64(testAppID, testOwnerOpen); ev.UserID != want {
		t.Errorf("用户号应映射为 %d，实际 %d", want, ev.UserID)
	}
	if ev.RawMessage != "hi" {
		t.Errorf("群 @ 消息必须去掉残留的前导空格，实际 %q", ev.RawMessage)
	}
	if ev.SelfID != a.GetSelfID() || ev.SelfID <= 0 {
		t.Errorf("事件 SelfID 应与 GetSelfID() 一致且为正，实际 ev=%d get=%d", ev.SelfID, a.GetSelfID())
	}
	if ev.SelfID == testSelfID {
		t.Errorf("SelfID 绝不能是官方 user.id（%d）", testSelfID)
	}
	if ev.MessageID == 0 {
		t.Error("事件必须带上内部 message id（撤回/取消息要用）")
	}
	// AT 事件：平台把 @ 前缀剥掉了，实现要补一个指向自己的 at 段
	if len(ev.Message) < 2 || ev.Message[0].Type != "at" {
		t.Fatalf("AT 事件应首段为自己的 at 段再接正文，实际 %+v", ev.Message)
	}
	if got, _ := ev.Message[0].Data["qq"].(float64); int64(got) != a.GetSelfID() {
		t.Errorf("补的 at 段 target 应等于 SelfID %d，实际 %d", a.GetSelfID(), int64(got))
	}
	if ev.Message[1].Type != "text" || ev.Message[1].Data["text"] != "hi" {
		t.Fatalf("at 段之后应是正文 text，实际 %+v", ev.Message[1])
	}
}

// 硬约束：同一个 msg_id 重复推送必须整条丢弃。
func TestQQOfficialDedupDropsRepeatEvent(t *testing.T) {
	a := newTestAdapter(t)

	count := 0
	a.OnGroupMessage(func(*adapter.GroupMessageEvent) { count++ })

	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload))
	first := a.msgs.len()
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload))
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload))

	if count != 1 {
		t.Errorf("重复推送必须被丢弃，handler 调用次数应为 1，实际 %d", count)
	}
	if a.msgs.len() != first {
		t.Errorf("被去重的事件不应登记消息，实际 %d -> %d", first, a.msgs.len())
	}
}

// 全量群消息：content 里的 <@openid> 要拆成 at 段，且能被反查成内部 id。
func TestQQOfficialFullMessageSplitsMentions(t *testing.T) {
	a := newTestAdapter(t)

	var ev *adapter.GroupMessageEvent
	a.OnGroupMessage(func(e *adapter.GroupMessageEvent) { ev = e })

	payload := strings.Replace(groupAtPayload, `" hi"`, `"<@`+testOtherOpen+`> hi"`, 1)
	a.onDispatch(EventGroupMessageCreate, json.RawMessage(payload))

	if ev == nil {
		t.Fatal("全量群消息也必须上报")
	}
	if len(ev.Message) != 2 {
		t.Fatalf("应拆成 at + text 两段，实际 %+v", ev.Message)
	}
	if ev.Message[0].Type != "at" {
		t.Fatalf("第一段应为 at，实际 %q", ev.Message[0].Type)
	}
	if want := a.ids.ToInt64(testAppID, testOtherOpen); ev.Message[0].Data["qq"] != float64(want) {
		t.Errorf("at 段目标应为内部 id %d，实际 %v", want, ev.Message[0].Data["qq"])
	}
	if ev.Message[1].Data["text"] != " hi" {
		t.Errorf("at 之后的文本应原样保留，实际 %v", ev.Message[1].Data["text"])
	}
	if !strings.Contains(ev.RawMessage, "<@") {
		t.Errorf("RawMessage 应保留原始文本形态，实际 %q", ev.RawMessage)
	}
}

// 官方没有群列表接口：群清单只能靠事件自建，且必须能喂给启动流程。
func TestQQOfficialGroupListBuiltFromEvents(t *testing.T) {
	a := newTestAdapter(t)

	groups, err := a.GetGroupList()
	if err != nil {
		t.Fatalf("GetGroupList 在空清单时也必须返回 nil error（启动流程会死等），实际 %v", err)
	}
	if len(groups) != 0 {
		t.Fatalf("初始群清单应为空，实际 %d", len(groups))
	}

	add := `{"group_openid":"` + testGroupOpen + `","op_member_openid":"` + testOwnerOpen + `","timestamp":1794385951}`
	a.onDispatch(EventGroupAddRobot, json.RawMessage(add))

	groups, err = a.GetGroupList()
	if err != nil || len(groups) != 1 {
		t.Fatalf("GROUP_ADD_ROBOT 后应有 1 个群，实际 %d（err=%v）", len(groups), err)
	}
	want := a.ids.ToInt64(testAppID, testGroupOpen)
	if groups[0].GroupID != want || groups[0].Code != want || groups[0].Uin != want {
		t.Errorf("群号字段都应填内部 id %d，实际 GroupID=%d Code=%d Uin=%d",
			want, groups[0].GroupID, groups[0].Code, groups[0].Uin)
	}

	// 群成员表拿不到（官方 11253）：必须给空表 + nil error，否则 messenger 的
	// reloadLists 会整体失败、启动流程卡死（adapter/messenger.go:1192-1252）。
	members, err := a.GetGroupMemberList(want)
	if err != nil {
		t.Fatalf("GetGroupMemberList 不许返回 error，实际 %v", err)
	}
	if len(members) != 0 {
		t.Errorf("还没从事件学到成员，应为空表，实际 %d", len(members))
	}

	// 说话过的成员会被学到，并带上事件里的 role。
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload))
	members, err = a.GetGroupMemberList(want)
	if err != nil {
		t.Fatalf("GetGroupMemberList: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("应学到 1 个发言过的成员，实际 %d", len(members))
	}
	if members[0].Nickname != "kizunerwe" || members[0].Permission != adapter.Owner {
		t.Errorf("成员应从事件带上昵称与角色，实际 %+v", members[0])
	}

	// 机器人被移出群后要从清单里消失
	del := `{"group_openid":"` + testGroupOpen + `","op_member_openid":"` + testOwnerOpen + `","timestamp":1794386000}`
	a.onDispatch(EventGroupDelRobot, json.RawMessage(del))
	if groups, _ = a.GetGroupList(); len(groups) != 0 {
		t.Errorf("GROUP_DEL_ROBOT 后群清单应清空，实际 %d", len(groups))
	}
}

// withContent 把实测事件体的 content 换成指定内容（用 json.Marshal 转义，避免手写坑）。
func withContent(t *testing.T, payload, content string) string {
	t.Helper()
	oldContent, err := json.Marshal(" hi")
	if err != nil {
		t.Fatalf("marshal 旧 content: %v", err)
	}
	newContent, err := json.Marshal(content)
	if err != nil {
		t.Fatalf("marshal content: %v", err)
	}
	if !strings.Contains(payload, string(oldContent)) {
		t.Fatalf("事件体里找不到 content 字段：%s", payload)
	}
	return strings.Replace(payload, string(oldContent), string(newContent), 1)
}

// 回归：@ 自身必须映射成 AtTarget == SelfID。
//
// lsp/parser/parser.go:99-103 的判定是 AtTarget == utils.GetBot().GetUin()，而 GetUin 来自
// lifecycle 里的 SelfID。早先自身 openid 走 IDMap 现分号、SelfID 却是官方 user.id，
// 两个命名空间不相交 → 实测「@ 了机器人反而不执行命令，没 @ 的纯文本照跑」。
func TestQQOfficialSelfMentionEqualsSelfID(t *testing.T) {
	a := newTestAdapter(t)

	const selfOpen = "01A2B3C4D5E6F708192A3B4C5D6E7F80" // 实测：个人机器人在测试群的 member_openid
	a.setSelfOpenID(selfOpen)

	self := a.GetSelfID()
	if self <= 0 {
		t.Fatal("确定自身 openid 后 SelfID 必须是 IDMap 命名空间里的正数")
	}
	if self == testSelfID {
		t.Fatalf("SelfID 不能还是官方 user.id（%d）", testSelfID)
	}

	var ev *adapter.GroupMessageEvent
	a.OnGroupMessage(func(e *adapter.GroupMessageEvent) { ev = e })

	// 全量消息形态：content 里 @机器人 + @别人（都带 openid 标记）
	payload := withContent(t, groupAtPayload, "<@"+selfOpen+"> <@"+testOtherOpen+"> /ping")
	a.onDispatch(EventGroupMessageCreate, json.RawMessage(payload))

	if ev == nil {
		t.Fatal("应收到群消息事件")
	}
	var selfAt, otherAt int64
	ats := 0
	for _, s := range ev.Message {
		if s.Type != "at" {
			continue
		}
		ats++
		target, _ := s.Data["qq"].(float64)
		if openid, _ := s.Data["openid"].(string); openid == selfOpen {
			selfAt = int64(target)
		} else {
			otherAt = int64(target)
		}
	}
	if ats != 2 {
		t.Fatalf("应拆出 2 个 at 段，实际 %d（%+v）", ats, ev.Message)
	}
	if selfAt != self {
		t.Errorf("@ 自己的 target 必须等于 SelfID：得到 %d，期望 %d", selfAt, self)
	}
	if otherAt == 0 || otherAt == self {
		t.Errorf("被别人应映射成另一个 id，实际 %d（SelfID=%d）", otherAt, self)
	}
}

// 多群：机器人在每个群的 member_openid 可能不同，但都必须映射到同一个 SelfID，
// 否则「另一个群里 @ 它」又会掉进同一个坑。
func TestQQOfficialSelfAliasAcrossGroups(t *testing.T) {
	a := newTestAdapter(t)
	a.setSelfOpenID("SELF-OPENID-GROUP-A")
	self := a.GetSelfID()

	a.setSelfOpenID("SELF-OPENID-GROUP-B") // 第二个群的自身 openid
	if got := a.GetSelfID(); got != self {
		t.Fatalf("自身 openid 增加时 SelfID 不能漂移：%d -> %d", self, got)
	}

	var ev *adapter.GroupMessageEvent
	a.OnGroupMessage(func(e *adapter.GroupMessageEvent) { ev = e })
	a.onDispatch(EventGroupMessageCreate, json.RawMessage(
		withContent(t, groupAtPayload, "<@SELF-OPENID-GROUP-B> /ping")))

	if ev == nil || len(ev.Message) == 0 || ev.Message[0].Type != "at" {
		t.Fatalf("首段应为 at，实际 %+v", ev)
	}
	if got, _ := ev.Message[0].Data["qq"].(float64); int64(got) != self {
		t.Errorf("B 群的自身 openid 也必须落到 SelfID %d，实际 %d", self, int64(got))
	}
}

// AT 事件：平台把 @机器人 前缀从 content 里剥掉，实现要补一个指向自己的 at 段，
// 让上层拿到的 AtTarget 明确等于 SelfID。
func TestQQOfficialAtEventPrependsSelfSegment(t *testing.T) {
	a := newTestAdapter(t)
	a.setSelfOpenID("SELF-OPENID-GROUP-A")
	self := a.GetSelfID()

	var ev *adapter.GroupMessageEvent
	a.OnGroupMessage(func(e *adapter.GroupMessageEvent) { ev = e })
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload))

	if ev == nil || len(ev.Message) == 0 {
		t.Fatal("应收到事件")
	}
	if ev.Message[0].Type != "at" {
		t.Fatalf("AT 事件的首个元素应是自己的 at 段，实际 %+v", ev.Message)
	}
	if got, _ := ev.Message[0].Data["qq"].(float64); int64(got) != self {
		t.Errorf("补的 at 段 target 应为 SelfID %d，实际 %d", self, int64(got))
	}

	// 还不知道自己是谁时不许硬加（加了 target 是错的，会让 @ 判定失败）
	b := newTestAdapter(t)
	var ev2 *adapter.GroupMessageEvent
	b.OnGroupMessage(func(e *adapter.GroupMessageEvent) { ev2 = e })
	b.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload))
	if ev2 == nil {
		t.Fatal("应收到事件")
	}
	for _, s := range ev2.Message {
		if s.Type == "at" {
			t.Errorf("SelfID 未知时不该补 at 段，实际 %+v", ev2.Message)
		}
	}
}

// countingSink 记录 adapter 发来的成员缓存失效通知。
type countingSink struct {
	mu     sync.Mutex
	groups []int64
}

func (c *countingSink) InvalidateGroupMembers(groupID int64) {
	c.mu.Lock()
	c.groups = append(c.groups, groupID)
	c.mu.Unlock()
}

func (c *countingSink) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.groups)
}

// withField 替换实测事件体里的某个字符串（用于造「第二条消息」：换个 msg_id）。
func withField(t *testing.T, payload, oldVal, newVal string) string {
	t.Helper()
	if !strings.Contains(payload, oldVal) {
		t.Fatalf("事件体里找不到 %q", oldVal)
	}
	return strings.Replace(payload, oldVal, newVal, 1)
}

// 回归：adapter 从事件里学到的成员必须能进 messenger 的成员缓存。
//
// 断掉的表现（实测）：lsp/permission/stateManager.go:199-218 的 CheckGroupAdministrator →
// FindMember 命中不了 → 群主执行 /watch 被判成「no permission / nil member info」。
func TestQQOfficialLearnedMemberRefreshesMessengerCache(t *testing.T) {
	a := newTestAdapter(t)
	// NewMessenger 会把 messenger 自己注入给 adapter（因为它实现了 MemberListSinkSetter）
	m := adapter.NewMessenger(a)

	groupID := a.ids.ToInt64(testAppID, testGroupOpen)
	userID := a.ids.ToInt64(testAppID, testOwnerOpen)

	// 群先进 messenger 的群列表（成员缓存挂在群条目上；正常由 ReloadGroupList 从
	// adapter 的 GetGroupList 灌进来）
	m.GroupList = append(m.GroupList, &adapter.GroupInfo{
		Uin: groupID, Code: groupID, Name: "测试群",
		Members: make([]*adapter.GroupMemberInfo, 0), Client: m,
	})

	// 群主发言（实测事件里 author.member_role = owner）
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload))

	group := m.FindGroup(groupID)
	if group == nil {
		t.Fatal("群应在 messenger 群列表里")
	}
	member := group.FindMember(userID)
	if member == nil {
		t.Fatal("学到的成员必须同步进 messenger 成员缓存（缓存同步这一跳断了）")
	}
	if member.Permission != adapter.Owner {
		t.Fatalf("群主应判定为 Owner，实际 permission=%v", member.Permission)
	}
	if !member.IsAdminOrOwner() {
		t.Fatal("IsAdminOrOwner 必须为真，否则群命令会被权限拦掉")
	}
	if member.Nickname != "kizunerwe" {
		t.Errorf("昵称应一并同步，实际 %q", member.Nickname)
	}
}

// testSecondOpen 造第二个成员用的作者 openid（形状与真实 openid 一致）。
const testSecondOpen = "AAAA1111BBBB2222CCCC3333DDDD"

// member2Payload 基于实测事件体造一个「另一个成员」的事件（换作者、昵称、角色、msg_id）。
// 只替换不带引号的子串，免得在源码里堆转义。
func member2Payload(t *testing.T, role, msgID string) string {
	t.Helper()
	p := strings.ReplaceAll(groupAtPayload, testOwnerOpen, testSecondOpen)
	p = strings.Replace(p, "kizunerwe", "seconduser", 1)
	p = strings.Replace(p, "owner", role, 1)
	p = strings.Replace(p, "ROBOT1.0_pJwMR5A4R2bYVXiPJsy482uKDNZgsr.Wi5z0PuSJHkVZ", msgID, 1)
	if !strings.Contains(p, testSecondOpen) || !strings.Contains(p, msgID) {
		t.Fatalf("造第二个成员的事件体失败：%s", p)
	}
	return p
}

// 回归：**非首个**成员的首条消息也必须在派发事件前同步刷成员缓存。
//
// 修之前判定按「群」（该群刷过就不再同步），于是群里第 2、3… 个成员各自的第一条命令
// 必然撞在「缓存里还没有他」上 —— 实测就是群主第二条消息才通过、第一条被 nil member info 拒。
func TestQQOfficialNonFirstMemberSyncIsInline(t *testing.T) {
	a := newTestAdapter(t)
	sink := &countingSink{}
	a.SetMemberListSink(sink)

	// 第 1 个成员
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload))
	if got := sink.count(); got != 1 {
		t.Fatalf("首个成员应同步刷一次，实际通知 %d 次", got)
	}

	// 第 2 个成员（另一个 openid）：同样必须**立刻**同步刷，不能等防抖
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(member2Payload(t, "member", "ROBOT1.0_SECOND_MEMBER_MSG")))
	if got := sink.count(); got != 2 {
		t.Fatalf("非首个成员的首条消息也必须同步刷一次，实际通知 %d 次", got)
	}

	// 同一个人再发言、人没变角色也没变 → 不再同步刷（这条路交给防抖；本测试没起 worker，所以计数不变）
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(member2Payload(t, "member", "ROBOT1.0_SECOND_MEMBER_MSG_2")))
	if got := sink.count(); got != 2 {
		t.Fatalf("已知成员且无变化时不该再同步刷，实际通知 %d 次", got)
	}

	// 角色变化（member -> admin）同样要同步刷，否则刚升管理要等下一次发言才生效
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(member2Payload(t, "admin", "ROBOT1.0_SECOND_MEMBER_MSG_3")))
	if got := sink.count(); got != 3 {
		t.Fatalf("角色变化必须同步刷一次，实际通知 %d 次", got)
	}
}

// 第二个成员的权限必须在**同一条消息**内可见（权限判定读的就是这份缓存）。
func TestQQOfficialSecondMemberPermissionVisibleInSameMessage(t *testing.T) {
	a := newTestAdapter(t)
	m := adapter.NewMessenger(a)

	groupID := a.ids.ToInt64(testAppID, testGroupOpen)
	m.GroupList = append(m.GroupList, &adapter.GroupInfo{
		Uin: groupID, Code: groupID, Name: "测试群",
		Members: make([]*adapter.GroupMemberInfo, 0), Client: m,
	})

	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload)) // 第 1 个：群主
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(
		member2Payload(t, "member", "ROBOT1.0_SECOND_MEMBER_PERM"))) // 第 2 个：普通成员的首条消息

	group := m.FindGroup(groupID)
	if group == nil {
		t.Fatal("群应在 messenger 群列表里")
	}
	owner := group.FindMember(a.ids.ToInt64(testAppID, testOwnerOpen))
	if owner == nil || !owner.IsAdminOrOwner() {
		t.Fatalf("群主必须在缓存里且为 owner/admin，实际 %+v", owner)
	}
	second := group.FindMember(a.ids.ToInt64(testAppID, testSecondOpen))
	if second == nil {
		t.Fatal("第二个成员必须在同一条消息的处理内就进缓存（不能等防抖）")
	}
	if second.IsAdminOrOwner() {
		t.Error("普通成员不该被判成管理员")
	}

	// 角色变化立刻可见（member -> admin）
	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(
		member2Payload(t, "admin", "ROBOT1.0_SECOND_MEMBER_PERM_2")))
	got := m.FindGroup(groupID).FindMember(a.ids.ToInt64(testAppID, testSecondOpen))
	if got == nil || got.Permission != adapter.Administrator {
		t.Fatalf("角色变化应立刻在缓存里生效（期望 Administrator），实际 %+v", got)
	}
}

// 群还不在 messenger 群列表时（靠群消息自学的存量群），通知也要能刷新成功。
func TestQQOfficialMemberRefreshAddsMissingGroup(t *testing.T) {
	a := newTestAdapter(t)
	m := adapter.NewMessenger(a)

	groupID := a.ids.ToInt64(testAppID, testGroupOpen)
	userID := a.ids.ToInt64(testAppID, testOwnerOpen)
	// 故意不往 m.GroupList 里加

	a.onDispatch(EventGroupAtMessageCreate, json.RawMessage(groupAtPayload))

	group := m.FindGroup(groupID)
	if group == nil {
		t.Fatal("刷新时应把未知群补进群列表，否则成员缓存没有落点")
	}
	if member := group.FindMember(userID); member == nil || !member.IsAdminOrOwner() {
		t.Fatalf("补的群条目也要能命中群主，实际 %+v", member)
	}
}

// 防抖：同一群连续多次变化只通知一次（别每条群消息都触发一次全量刷新）。
func TestMemberSyncNotifierCoalescesBurst(t *testing.T) {
	var mu sync.Mutex
	calls := map[int64]int{}
	n := newMemberSyncNotifier(func(id int64) {
		mu.Lock()
		calls[id]++
		mu.Unlock()
	}, 30*time.Millisecond, 20*time.Millisecond, 100)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.run(ctx)

	for i := 0; i < 5; i++ { // 模拟同一批抖动
		n.mark(7)
	}
	time.Sleep(250 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if calls[7] != 1 {
		t.Fatalf("同一群的抖动应被合并成 1 次通知，实际 %d", calls[7])
	}
}

// 限流：一分钟窗口内的通知上限不能被突破（超出的会被推迟，不会一次性刷爆上层）。
func TestMemberSyncNotifierRespectsRateLimit(t *testing.T) {
	var mu sync.Mutex
	total := 0
	n := newMemberSyncNotifier(func(id int64) {
		mu.Lock()
		total++
		mu.Unlock()
	}, 10*time.Millisecond, 20*time.Millisecond, 2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.run(ctx)

	for g := int64(1); g <= 6; g++ {
		n.mark(g)
	}
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if total == 0 {
		t.Fatal("上限内至少应通知一次")
	}
	if total > 2 {
		t.Fatalf("每分钟上限是 2，实际通知了 %d 次", total)
	}
}

// 实测形态的图片消息事件（附件是临时鉴权下载链接）。
const groupImagePayload = `{
  "author": {"bot": false, "id": "05A2B3C4D5E6F708192A3B4C5D6E7F84",
             "member_openid": "05A2B3C4D5E6F708192A3B4C5D6E7F84",
             "member_role": "owner", "union_openid": "", "username": "kizunerwe"},
  "content": "",
  "group_id": "03A2B3C4D5E6F708192A3B4C5D6E7F82",
  "group_openid": "03A2B3C4D5E6F708192A3B4C5D6E7F82",
  "id": "ROBOT1.0_IMAGE_MSG_ID",
  "attachments": [{"content_type": "image/png", "filename": "test.png",
                   "height": 100, "width": 100, "size": 12345,
                   "url": "https://multimedia.nt.qq.com.cn/download?appid=1&fileid=abc&rkey=TEMPKEY&spec=0"}],
  "message_scene": {"ext": ["msg_idx=REFIDX_IMAGE_MSG"], "source": "default"},
  "message_type": 0,
  "timestamp": "2026-11-11T19:16:40+08:00"
}`

// imageVariant 改实测图片事件体的附件字段（只替换不带引号的值，免得堆转义）。
func imageVariant(t *testing.T, contentType, fileName, msgID string) string {
	t.Helper()
	p := strings.Replace(groupImagePayload, "image/png", contentType, 1)
	p = strings.Replace(p, "test.png", fileName, 1)
	p = strings.Replace(p, "ROBOT1.0_IMAGE_MSG_ID", msgID, 1)
	return p
}

func firstImageSegment(ev *adapter.GroupMessageEvent) *adapter.MessageSegment {
	for i := range ev.Message {
		if ev.Message[i].Type == "image" {
			return &ev.Message[i]
		}
	}
	return nil
}

// 回归：入站图片附件必须变成图片段（带可下载 URL）。
// 修之前平台没给 content_type 时会被当成普通文件段，/倒放 报「参数错误 - 未找到图片」。
func TestQQOfficialImageAttachmentBecomesImageElement(t *testing.T) {
	a := newTestAdapter(t)
	var ev *adapter.GroupMessageEvent
	a.OnGroupMessage(func(e *adapter.GroupMessageEvent) { ev = e })

	a.onDispatch(EventGroupMessageCreate, json.RawMessage(
		imageVariant(t, "image/png", "test.png", "ROBOT1.0_IMG_1")))
	if ev == nil {
		t.Fatal("应收到群消息事件")
	}

	const wantURL = "https://multimedia.nt.qq.com.cn/download?appid=1&fileid=abc&rkey=TEMPKEY&spec=0"

	seg := firstImageSegment(ev)
	if seg == nil {
		t.Fatalf("附件应翻成 image 段，实际 %+v", ev.Message)
	}
	if got, _ := seg.Data["url"].(string); got != wantURL {
		t.Errorf("图片段必须带上可下载 URL，实际 %q", got)
	}
	if got, _ := seg.Data["file"].(string); got != wantURL {
		t.Errorf("图片段的 file 也应填 URL（DDBOT 对远程图片就是 File=Url 的约定），实际 %q", got)
	}

	// DDBOT 侧：元素列表里要出现 *adapter.ImageSegment —— 就是实测日志里看的那一步
	elems := adapter.ConvertToMessageElements(ev.Message)
	var img *adapter.ImageSegment
	for _, e := range elems {
		if im, ok := e.(*adapter.ImageSegment); ok {
			img = im
			break
		}
	}
	if img == nil {
		t.Fatalf("DDBOT 元素里应出现 *adapter.ImageSegment，实际 %#v", elems)
	}
	if img.Url != wantURL {
		t.Errorf("ImageSegment.Url 应为附件 URL，实际 %q", img.Url)
	}
}

// 平台偶尔不给 content_type：要靠文件名后缀兜底，别退化成文件段。
func TestQQOfficialImageAttachmentFallsBackToFileName(t *testing.T) {
	a := newTestAdapter(t)
	var ev *adapter.GroupMessageEvent
	a.OnGroupMessage(func(e *adapter.GroupMessageEvent) { ev = e })

	a.onDispatch(EventGroupMessageCreate, json.RawMessage(
		imageVariant(t, "", "photo.JPG", "ROBOT1.0_IMG_2")))
	if ev == nil {
		t.Fatal("应收到事件")
	}
	if firstImageSegment(ev) == nil {
		t.Fatalf("content_type 缺失时应按文件名后缀识别成图片，实际 %+v", ev.Message)
	}
}

// 非图片附件：不丢事件，映射有据可依（语音→record、视频→file、其它→file）。
func TestQQOfficialNonImageAttachments(t *testing.T) {
	cases := []struct {
		ct       string
		name     string
		wantType string
		wantElem string
	}{
		{"voice", "a.silk", "record", "voice"},
		{"video/mp4", "a.mp4", "file", "file"}, // ConvertToMessageElements 没有 video 分支
		{"application/octet-stream", "a.bin", "file", "file"},
		{"", "", "file", "file"},
	}
	for i, c := range cases {
		a := newTestAdapter(t)
		var ev *adapter.GroupMessageEvent
		a.OnGroupMessage(func(e *adapter.GroupMessageEvent) { ev = e })
		a.onDispatch(EventGroupMessageCreate, json.RawMessage(
			imageVariant(t, c.ct, c.name, "ROBOT1.0_NONIMG_"+strconv.Itoa(i))))

		if ev == nil {
			t.Fatalf("用例 %d：附件不能把事件弄丢", i)
		}
		if len(ev.Message) == 0 {
			t.Fatalf("用例 %d(ct=%q)：应有段产出", i, c.ct)
		}
		if got := ev.Message[0].Type; got != c.wantType {
			t.Errorf("用例 %d(ct=%q, name=%q)：段类型应为 %q，实际 %q", i, c.ct, c.name, c.wantType, got)
		}
		// DDBOT 侧也不能被静默丢掉
		elems := adapter.ConvertToMessageElements(ev.Message)
		if len(elems) == 0 {
			t.Fatalf("用例 %d：元素被 ConvertToMessageElements 丢了", i)
		}
		switch c.wantElem {
		case "voice":
			if _, ok := elems[0].(*adapter.VoiceSegment); !ok {
				t.Errorf("用例 %d：语音应变成 VoiceSegment，实际 %T", i, elems[0])
			}
		default:
			if _, ok := elems[0].(*adapter.FileSegment); !ok {
				t.Errorf("用例 %d：应变成 FileSegment，实际 %T", i, elems[0])
			}
		}
	}
}

// 回复图片消息：ReplySegment 必须带上**被回复消息的内部 id**。
//
// /倒放 的回复路径就是靠它（lsp/groupCommand.go:971-979 → GetMessageImageUrl(groupCode, replySeq)），
// 而 DDBOT 存图 URL 时用的 key 正是 int32(msg.ID)（lsp/module.go:647）——两边必须是同一套 id，
// 也就是事件里那个 MessageID。
func TestQQOfficialReplySegmentCarriesQuotedMessageID(t *testing.T) {
	a := newTestAdapter(t)
	var events []*adapter.GroupMessageEvent
	a.OnGroupMessage(func(e *adapter.GroupMessageEvent) { events = append(events, e) })

	// 1) 先来一条图片消息（它自己带 msg_idx=REFIDX_IMAGE_MSG，会被登记进消息表）
	a.onDispatch(EventGroupMessageCreate, json.RawMessage(
		imageVariant(t, "image/png", "test.png", "ROBOT1.0_IMG_REPLY_SRC")))
	if len(events) != 1 {
		t.Fatalf("第一条应上报，实际 %d", len(events))
	}
	imageMsgID := events[0].MessageID

	// 2) 再来一条「回复那条图片」的消息：ext 里带 ref_msg_idx=同样的引用索引
	reply := strings.Replace(groupImagePayload, "msg_idx=REFIDX_IMAGE_MSG", "ref_msg_idx=REFIDX_IMAGE_MSG", 1)
	reply = strings.Replace(reply, "ROBOT1.0_IMAGE_MSG_ID", "ROBOT1.0_REPLY_MSG_ID", 1)
	a.onDispatch(EventGroupMessageCreate, json.RawMessage(reply))
	if len(events) != 2 {
		t.Fatalf("第二条应上报，实际 %d", len(events))
	}

	var replySeg *adapter.MessageSegment
	for i := range events[1].Message {
		if events[1].Message[i].Type == "reply" {
			replySeg = &events[1].Message[i]
			break
		}
	}
	if replySeg == nil {
		t.Fatalf("回复消息应产出 reply 段，实际 %+v", events[1].Message)
	}
	got, _ := replySeg.Data["id"].(float64)
	if int64(got) != imageMsgID {
		t.Fatalf("reply 段里的序号必须是被回复消息的内部 id（%d），实际 %d", imageMsgID, int64(got))
	}
	// DDBOT 侧要能还原成 ReplySegment，且 ReplySeq 与存图用的 id 一致
	elems := adapter.ConvertToMessageElements(events[1].Message)
	var rs *adapter.ReplySegment
	for _, e := range elems {
		if r, ok := e.(*adapter.ReplySegment); ok {
			rs = r
			break
		}
	}
	if rs == nil {
		t.Fatalf("元素里应出现 *adapter.ReplySegment，实际 %#v", elems)
	}
	if int64(rs.ReplySeq) != imageMsgID {
		t.Fatalf("ReplySeq(%d) 必须等于被回复消息的 MessageID(%d)，否则 /倒放 取不到图",
			rs.ReplySeq, imageMsgID)
	}
}

// 发送必须走映射表：未知目标要明确报错，不能静默发到别处。
func TestQQOfficialSendRejectsUnknownTarget(t *testing.T) {
	a := newTestAdapter(t)

	if _, err := a.SendGroupMessage(999999, "hi"); err == nil {
		t.Error("未知群号必须返回错误")
	}
	if _, err := a.SendPrivateMessage(0, "hi"); err == nil {
		t.Error("id 为 0 必须返回错误")
	}
	if _, err := a.SendGroupMessage(a.ids.ToInt64(testAppID, testGroupOpen), nil); err == nil {
		t.Error("message 为 nil 必须返回错误")
	}
	if _, err := a.SendGroupMessage(a.ids.ToInt64(testAppID, testGroupOpen), struct{ X int }{1}); err == nil {
		t.Error("不支持的 message 类型必须返回错误")
	}
}

// 「官方没这个能力」的方法必须返回明确错误，绝不能返回 nil 假装成功。
func TestQQOfficialUnsupportedMethodsAreExplicit(t *testing.T) {
	a := newTestAdapter(t)

	cases := []struct {
		name string
		err  error
	}{
		{"SetGroupWholeBan", a.SetGroupWholeBan(1, true)},
		{"SetGroupLeave", a.SetGroupLeave(1, false)},
		{"SetGroupAdmin", a.SetGroupAdmin(1, 2, true)},
		{"EditGroupCard", a.EditGroupCard(1, 2, "x")},
		{"EditGroupTitle", a.EditGroupTitle(1, 2, "x")},
		{"GroupPoke", a.GroupPoke(1, 2)},
		{"FriendPoke", a.FriendPoke(2)},
	}
	for _, c := range cases {
		if c.err == nil {
			t.Errorf("%s 必须返回错误，不能假装成功", c.name)
			continue
		}
		if !errors.Is(c.err, ErrUnsupported) {
			t.Errorf("%s 的错误应可用 errors.Is 判定为 ErrUnsupported，实际 %v", c.name, c.err)
		}
	}
	if _, _, err := a.SendGroupForwardMessage(1, nil, nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("合并转发应报不支持，实际 %v", err)
	}
	if _, err := a.GetMsgOrg(1); !errors.Is(err, ErrUnsupported) {
		t.Errorf("GetMsgOrg 应报不支持，实际 %v", err)
	}
	if _, err := a.SendApi("get_group_list", nil); !errors.Is(err, ErrUnsupported) {
		t.Errorf("未映射的 SendApi 动作应报不支持，实际 %v", err)
	}
	if got := a.GetFileUrl(1, "x"); got != "" {
		t.Errorf("GetFileUrl 官方无能力，应返回空串，实际 %q", got)
	}
	if err := a.RecallMsg(4242); err == nil {
		t.Error("撤回未登记的消息必须报错")
	}
}

// 状态文件：idmap 与群清单两段互不覆盖，重启后 ID 与群清单都还在。
func TestQQOfficialStateFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	store, err := NewJSONStore(path, testAppID)
	if err != nil {
		t.Fatalf("NewJSONStore: %v", err)
	}

	ids := NewIDMap(store)
	gid := ids.ToInt64(testAppID, testGroupOpen)
	uid := ids.ToInt64(testAppID, testOwnerOpen)
	if err := ids.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := store.SaveGroups([]GroupRecord{{OpenID: testGroupOpen, Name: "测试群", MemberCount: 7, InfoKnown: true}}); err != nil {
		t.Fatalf("SaveGroups: %v", err)
	}

	// 写群清单不能把 idmap 冲掉（先 Save 后 SaveGroups 的顺序最容易被写坏）
	again := NewIDMap(store)
	if got := again.ToInt64(testAppID, testGroupOpen); got != gid {
		t.Errorf("重启后群号应稳定为 %d，实际 %d", gid, got)
	}
	if got := again.ToInt64(testAppID, testOwnerOpen); got != uid {
		t.Errorf("重启后用户号应稳定为 %d，实际 %d", uid, got)
	}

	groups, err := store.LoadGroups()
	if err != nil || len(groups) != 1 || groups[0].Name != "测试群" {
		t.Fatalf("群清单应能读回，实际 %+v（err=%v）", groups, err)
	}

	// ① 主文件坏掉、但备份还在 → 必须从备份恢复
	if err := os.WriteFile(path, []byte("{ not json"), 0o600); err != nil {
		t.Fatalf("写坏文件失败：%v", err)
	}
	recovered := NewIDMap(store)
	if got := recovered.ToInt64(testAppID, testGroupOpen); got != gid {
		t.Errorf("主文件损坏后应从备份恢复群号 %d，实际 %d", gid, got)
	}
	if got := recovered.ToInt64(testAppID, testOwnerOpen); got != uid {
		t.Errorf("主文件损坏后应从备份恢复用户号 %d，实际 %d", uid, got)
	}

	// ② 备份也删掉 → 坏文件必须报错（不能当空的，否则会用不完整的映射覆盖旧数据）
	if err := os.Remove(path + ".bak"); err != nil {
		t.Fatalf("删除备份失败：%v", err)
	}
	if _, err := store.Load(); err == nil {
		t.Error("状态文件损坏且无备份时 Load 必须报错")
	}
	mapAfter := NewIDMap(store)
	if err := mapAfter.Flush(); !errors.Is(err, ErrLoadFailed) {
		t.Errorf("Load 失败后 Flush 应报 ErrLoadFailed 且不写盘，实际 %v", err)
	}
	if got, _ := store.LoadGroups(); len(got) != 0 {
		t.Errorf("Load 失败后不应有任何写入，实际 %+v", got)
	}
}

// ---------------- 机器人自身消息过滤 ----------------

// botAuthoredPayload 造一条「机器人自己发的」群消息。
//
// 形态取自实测：机器人自己的发言会以 GROUP_MESSAGE_CREATE 推回来，
// author.bot=true，author 的 openid 就是它在本群的 member_openid。
func botAuthoredPayload(t *testing.T, authorOpen, content, msgID string) string {
	t.Helper()
	p := strings.Replace(groupAtPayload, `"bot": false`, `"bot": true`, 1)
	p = strings.ReplaceAll(p, testOwnerOpen, authorOpen)
	p = strings.Replace(p, "ROBOT1.0_pJwMR5A4R2bYVXiPJsy482uKDNZgsr.Wi5z0PuSJHkVZ", msgID, 1)
	if !strings.Contains(p, `"bot": true`) || !strings.Contains(p, authorOpen) {
		t.Fatalf("造机器人发言的事件体失败：%s", p)
	}
	return withContent(t, p, content)
}

// 机器人自己发的消息必须被丢掉，绝不进命令管线。
//
// 修之前：author.bot 与自身 openid 都不看，机器人自己的发言（哪怕以命令前缀开头）照常
// 派发 —— lsp/parser/parser.go:99-104 的 AtCheck 在 AtTarget<=0 时放行，于是自执行；
// 一旦运维配了 trigger.group.message.tmpl 就是自回复循环。
func TestQQOfficialIgnoresBotAuthoredMessages(t *testing.T) {
	a := newTestAdapter(t)
	a.setSelfOpenID(selfOpenInGroup)

	var mu sync.Mutex
	var gotGroup, gotPrivate int
	a.OnGroupMessage(func(*adapter.GroupMessageEvent) {
		mu.Lock()
		gotGroup++
		mu.Unlock()
	})
	a.OnPrivateMessage(func(*adapter.PrivateMessageEvent) {
		mu.Lock()
		gotPrivate++
		mu.Unlock()
	})
	seen := func() int { mu.Lock(); defer mu.Unlock(); return gotGroup }
	seePrivate := func() int { mu.Lock(); defer mu.Unlock(); return gotPrivate }

	// 1) 机器人自己的发言：author.bot=true，且 openid 命中自身别名表。
	//    故意用命令前缀开头，修之前这就是「自己执行自己的命令」。
	a.onDispatch(EventGroupMessageCreate, json.RawMessage(botAuthoredPayload(
		t, selfOpenInGroup, "/watch 12345", "ROBOT1.0_BOT_SELF_FULL")))
	if n := seen(); n != 0 {
		t.Fatalf("机器人自己发的消息必须被丢弃，实际派发了 %d 个群消息事件", n)
	}

	// 2) author.bot=true 但 openid 还不在别名表里（自身 openid 尚未解析出来的窗口，
	//    或群里的另一个机器人）：同样不许进管线。
	a.onDispatch(EventGroupMessageCreate, json.RawMessage(botAuthoredPayload(
		t, "B0T0000000000000000000000000000", "/watch 999", "ROBOT1.0_OTHER_BOT_FULL")))
	if n := seen(); n != 0 {
		t.Fatalf("author.bot=true 的消息必须被丢弃，实际派发了 %d 个群消息事件", n)
	}

	// 3) 真人消息照常上报（别把正常的过滤掉）。
	a.onDispatch(EventGroupMessageCreate, json.RawMessage(
		member2Payload(t, "member", "ROBOT1.0_HUMAN_AFTER_BOT")))
	if n := seen(); n != 1 {
		t.Fatalf("真人的消息必须照常派发，期望 1 个事件，实际 %d", n)
	}

	// 4) 单聊同理（同一条规则，防御性：官方单聊未观察到自身回推）。
	a.onDispatch(EventC2CMessageCreate, json.RawMessage(botAuthoredPayload(
		t, selfOpenInGroup, "/watch 123", "ROBOT1.0_BOT_SELF_C2C")))
	if n := seePrivate(); n != 0 {
		t.Fatalf("机器人自己发的单聊消息也该丢弃，实际派发 %d 个单聊事件", n)
	}
}

// 只按 author.bot 判会被「自身 openid 已知但平台没给 bot 字段」的形态漏掉，
// 只按自身 openid 判会在启动窗口（还没拿到自身 openid）里漏掉 —— 两条都要在。
func TestQQOfficialSelfOpenIDAloneIsEnoughToDrop(t *testing.T) {
	a := newTestAdapter(t)
	a.setSelfOpenID(selfOpenInGroup)

	var mu sync.Mutex
	got := 0
	a.OnGroupMessage(func(*adapter.GroupMessageEvent) {
		mu.Lock()
		got++
		mu.Unlock()
	})

	// 造一条 author.bot=false 但 openid 是机器人自己的事件。
	// 注意两处都在 author 里：identity() 优先取 member_openid（不是 id），两处都要换。
	p := strings.ReplaceAll(groupAtPayload, testOwnerOpen, selfOpenInGroup)
	p = withContent(t, p, "/watch 777")
	p = strings.Replace(p, "ROBOT1.0_pJwMR5A4R2bYVXiPJsy482uKDNZgsr.Wi5z0PuSJHkVZ", "ROBOT1.0_SELF_NO_BOTFLAG", 1)
	a.onDispatch(EventGroupMessageCreate, json.RawMessage(p))

	mu.Lock()
	defer mu.Unlock()
	if got != 0 {
		t.Fatalf("作者 openid 是机器人自己时必须丢弃（不能只看 author.bot），实际派发 %d 个事件", got)
	}
}
