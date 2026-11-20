package qqofficial

// bot_state 自身身份回归：机器人自己必须进群成员表。
//
// 断掉的表现：lsp/notify.go:102-107 的 @全体判定要 CheckGroupAdministrator(群, 机器人自己UIN)，
// 读的是 messenger 的群成员缓存；而 qqofficial 的成员表只收「事件里出现过的人」，
// 机器人自己从不出现在事件里 ⇒ 恒为空 ⇒ @全体被静默跳过。
//
// 这里用假平台（httptest，回环地址，不联网、不碰真机）钉住 bot_state -> 成员表这一跳。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

// selfNickname 是机器人自己登记进成员表时用的昵称（空昵称会让 {{ memberList }} 出现无名条目）。
const selfNickname = "机器人"

// fakeBotStatePlatform 是只认「换 token」和「bot_state」两条路径的假平台。
//
// bodies 按群 openid 覆盖 bot_state 的返回：真机上同一个机器人在各群的
// member_openid 是**同一个值**（实测 per-bot），但 member_role 与主动消息权限
// 可以逐群不同，用例需要按群造。
type fakeBotStatePlatform struct {
	server *httptest.Server

	mu          sync.Mutex
	body        string
	bodies      map[string]string
	status      int
	botStateHit int
}

func newFakeBotStatePlatform(t *testing.T, status int, body string) *fakeBotStatePlatform {
	t.Helper()
	p := &fakeBotStatePlatform{body: body, status: status, bodies: make(map[string]string)}
	p.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", ContentTypeJSON)
		switch {
		case strings.HasSuffix(r.URL.Path, PathGetAppAccessToken):
			_, _ = w.Write([]byte(`{"access_token":"test-token","expires_in":7200}`))
		case strings.HasSuffix(r.URL.Path, "/bot_state"):
			p.mu.Lock()
			p.botStateHit++
			body := p.body
			for groupOpenID, override := range p.bodies {
				if strings.Contains(r.URL.Path, groupOpenID) {
					body = override
					break
				}
			}
			status := p.status
			p.mu.Unlock()
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		default:
			// 其余路径（info / users/@me 之类）给空对象，别让后台任务报错刷屏。
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	t.Cleanup(p.server.Close)
	return p
}

// setGroupBody 给指定群单独指定 bot_state 返回（不指定就回落到默认 body）。
func (p *fakeBotStatePlatform) setGroupBody(groupOpenID, body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.bodies[groupOpenID] = body
}

func (p *fakeBotStatePlatform) hits() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.botStateHit
}

// newBotStateAdapter 起一个把 API 打到假平台的 adapter（不登记任何群）。
func newBotStateAdapter(t *testing.T, p *fakeBotStatePlatform) *QQOfficialAdapter {
	t.Helper()
	key := ConfigKeyPrefix + "." + ConfigKeyAPIBaseURL
	config.GlobalConfig.Set(key, p.server.URL)
	t.Cleanup(func() { config.GlobalConfig.Set(key, "") })
	return newTestAdapter(t)
}

// botStateAdapter 起 adapter 并把测试群登记进群清单（learnMember 需要有这个落点）。
//
// 这里直接走 registry 的 ensure，而不派发 GROUP_ADD_ROBOT：那条事件会顺手起一个
// 后台的 resolveSelfFromGroup goroutine，跟测试自己的调用抢 bot_state，断言就不稳了。
// ensure 正是两条生产路径（拉群事件 / 群消息）共用的登记入口。
func botStateAdapter(t *testing.T, p *fakeBotStatePlatform) *QQOfficialAdapter {
	t.Helper()
	a := newBotStateAdapter(t, p)
	if id := a.groups.ensure(testGroupOpen); id == 0 {
		t.Fatalf("测试前置失败：群 %s 应已登记进 groupRegistry", testGroupOpen)
	}
	return a
}

// findSelf 在 GetGroupMemberList 的返回里找机器人自己。
//
// 判据是 a.GetSelfID()（= 上层 messenger.Uin / utils.GetBot().GetUin() 用的那个值）：
// 上层判定「机器人是不是管理员」就是拿它去成员表里查。而 member_openid 实测是
// per-bot（跨群相同），所以按它推导出来的 id 天然就等于 SelfID，两边同一个数。
func findSelf(t *testing.T, a *QQOfficialAdapter, groupID int64) *adapter.GroupMemberInfo {
	t.Helper()
	members, err := a.GetGroupMemberList(groupID)
	if err != nil {
		t.Fatalf("GetGroupMemberList 不许返回 error，实际 %v", err)
	}
	selfID := a.GetSelfID()
	if selfID == 0 {
		return nil
	}
	for _, m := range members {
		if m.UserID == selfID || m.Uin == selfID {
			return m
		}
	}
	return nil
}

// assertSelfPermission 跑一遍 bot_state -> 成员表，断言机器人自己的权限档位。
func assertSelfPermission(t *testing.T, role string, want adapter.MemberPermission) {
	t.Helper()
	body := `{"member_openid":"` + selfOpenInGroup + `","member_role":"` + role +
		`","allow_proactive_msg":true,"recv_msg_setting":"all"}`
	p := newFakeBotStatePlatform(t, http.StatusOK, body)
	a := botStateAdapter(t, p)
	sink := &countingSink{}
	a.SetMemberListSink(sink)

	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	if ok := a.resolveSelfFromGroup(testGroupOpen); !ok {
		t.Fatalf("bot_state 返回正常时 resolveSelfFromGroup 必须为 true（role=%s）", role)
	}
	if got := p.hits(); got < 1 {
		t.Fatal("成员表这条路没有打 bot_state —— 机器人自己不该能凭空出现")
	}

	self := findSelf(t, a, groupID)
	if self == nil {
		members, _ := a.GetGroupMemberList(groupID)
		t.Fatalf("机器人自己必须进该群成员表（role=%s），实际成员表 %d 人：%+v", role, len(members), members)
	}
	if self.Permission != want {
		t.Errorf("member_role=%q 应映射成 permission=%v，实际 %v", role, want, self.Permission)
	}
	if self.Nickname != selfNickname {
		t.Errorf("机器人自己的昵称必须是 %q（空昵称会让 {{ memberList }} 出现无名条目），实际 %q",
			selfNickname, self.Nickname)
	}
	if self.Role != role {
		t.Errorf("角色字符串必须原样透传（映射交给 memberInfoFromRecord），期望 %q 实际 %q", role, self.Role)
	}
	if got := sink.count(); got == 0 {
		t.Error("机器人自己首次进成员表时，必须调 syncGroupMember 让上层缓存立刻重建")
	}
}

// bot_state 说自己在群里是管理员 → 成员表里就有自己，且权限是 Administrator。
func TestQQOfficialSelfMemberFromBotStateAdmin(t *testing.T) {
	assertSelfPermission(t, "admin", adapter.Administrator)
}

// bot_state 说自己是群主 → Owner。
func TestQQOfficialSelfMemberFromBotStateOwner(t *testing.T) {
	assertSelfPermission(t, "owner", adapter.Owner)
}

// bot_state 说自己是普通成员 → Member。
func TestQQOfficialSelfMemberFromBotStateMember(t *testing.T) {
	assertSelfPermission(t, "member", adapter.Member)
}

// bot_state 打不通时：resolveSelfFromGroup 返回 false、不 panic，成员表里也不许凭空出现机器人。
func TestQQOfficialSelfMemberSkippedWhenBotStateFails(t *testing.T) {
	p := newFakeBotStatePlatform(t, http.StatusInternalServerError, `{"code":10001,"message":"boom"}`)
	a := botStateAdapter(t, p)
	sink := &countingSink{}
	a.SetMemberListSink(sink)

	groupID := a.ids.ToInt64(testAppID, testGroupOpen)

	if ok := a.resolveSelfFromGroup(testGroupOpen); ok {
		t.Fatal("bot_state 失败时 resolveSelfFromGroup 必须返回 false")
	}
	if self := findSelf(t, a, groupID); self != nil {
		t.Fatalf("bot_state 失败时不该凭空登记机器人自己，实际 %+v", self)
	}
	if got := sink.count(); got != 0 {
		t.Errorf("没有登记成功就不该刷上层缓存，实际通知 %d 次", got)
	}
}

// 群还没登记进 registry（learnMember 查不到）时：Warn 后跳过，不 panic、不返回 error。
func TestQQOfficialSelfMemberSkippedWhenGroupUnknown(t *testing.T) {
	body := `{"member_openid":"` + selfOpenInGroup + `","member_role":"admin","allow_proactive_msg":true,"recv_msg_setting":"all"}`
	p := newFakeBotStatePlatform(t, http.StatusOK, body)
	a := newBotStateAdapter(t, p) // 故意不登记群

	if ok := a.resolveSelfFromGroup(testGroupOpen); !ok {
		t.Fatal("bot_state 自己成功时，即使群没登记进 registry 也不该返回 false")
	}
	if _, found := a.groups.getByOpenID(testGroupOpen); found {
		t.Fatal("不该顺手把未知群塞进 registry")
	}
}
