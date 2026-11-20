package qqofficial

// 本文件钉住两件「启动期」的硬判定：
//
//  1. 状态文件里记的 app_id 与当前配置不一致 → 拒绝启动（不是打条 Warn 就算）；
//  2. 启动时成员表为空的群 → 用一条 Warn 说清「官方通道只认识在群里发过言的人」。
//
// 依据：app_id 判定这条，旧实现只在读盘时 Warn 一句，而 writeLocked 每次写盘都会把
// app_id 改写成新值 —— 那条线索只活到第一次刷盘（现在 2 秒就刷一次），之后就查不到了。
// 换机器人还接着用旧文件，等于让新机器人继承一份「别人的 openid 映射 + 别人的群」，
// 上层按内部 ID 判权限，会把这些群的权限发给错的群。
//
// 成员表这条同样是**暂时性**的：官方群成员列表接口（/v2/groups/{openid}/members）
// 实测返回 11253，成员只能靠群事件自学；哪天腾讯放开该接口，这段提示应删掉、
// 改成真实接口拉取（连同 GetGroupMemberList 的降级逻辑一起）。

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

const (
	// groupCOpen / groupDOpen 是「成员表为空」用例里另外两个群的 openid。
	groupCOpen = "04A2B3C4D5E6F708192A3B4C5D6E7F83"
	groupDOpen = "1A2B3C4D5E6F708192A3B4C5D6E7F801"
)

// appIDMismatchErr 造一个「文件里记的是别的 appID」的状态文件。
func writeMismatchedStateFile(t *testing.T, path string, storedAppID string) {
	t.Helper()
	writeStateFileAt(t, path, storedAppID,
		map[string]int64{composeIDKey(storedAppID, testGroupOpen): 1},
		[]GroupRecord{{OpenID: testGroupOpen, Name: "旧机器人的群", InfoKnown: true}})
}

// CheckAppID：不一致要报错（带两个 appID），一致或为空要放行。
func TestJSONStoreCheckAppID(t *testing.T) {
	t.Run("不一致", func(t *testing.T) {
		const oldAppID = "1900000009"
		path := t.TempDir() + "/state.json"
		writeMismatchedStateFile(t, path, oldAppID)

		err := newStateStore(t, path, testAppID).CheckAppID()
		if err == nil {
			t.Fatal("状态文件里的 app_id 与当前配置不一致时必须报错（否则新机器人继承旧映射）")
		}
		for _, want := range []string{oldAppID, testAppID, path} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("错误信息应点出 %q，实际：%v", want, err)
			}
		}
	})

	t.Run("一致", func(t *testing.T) {
		path := t.TempDir() + "/state.json"
		writeStateFileAt(t, path, testAppID, map[string]int64{composeIDKey(testAppID, testGroupOpen): 3}, nil)
		if err := newStateStore(t, path, testAppID).CheckAppID(); err != nil {
			t.Errorf("app_id 一致时不该报错，实际 %v", err)
		}
	})

	t.Run("为空", func(t *testing.T) {
		path := t.TempDir() + "/state.json"
		writeStateFileAt(t, path, "", map[string]int64{composeIDKey(testAppID, testGroupOpen): 3}, nil)
		if err := newStateStore(t, path, testAppID).CheckAppID(); err != nil {
			t.Errorf("app_id 为空（老文件/手写文件）时必须照常放行，实际 %v", err)
		}
	})

	t.Run("文件不存在", func(t *testing.T) {
		path := t.TempDir() + "/state.json"
		if err := newStateStore(t, path, testAppID).CheckAppID(); err != nil {
			t.Errorf("首次运行没有状态文件，不该报错，实际 %v", err)
		}
	})
}

// 状态文件 app_id 与配置不一致：Start 必须直接失败（拒绝启动），且错误信息可操作。
func TestAdapterStartRefusesWhenStateAppIDDiffers(t *testing.T) {
	const oldAppID = "1900000009"
	path := t.TempDir() + "/state.json"
	writeMismatchedStateFile(t, path, oldAppID)
	setQQOfficialConfig(t, map[string]string{
		"state-file": path,
		"app-id":     testAppID,
		"app-secret": "test-secret",
	})

	a, ok := NewQQOfficialAdapter(&adapter.AdapterConfig{Mode: string(adapter.AdapterTypeQQOfficial)}).(*QQOfficialAdapter)
	if !ok || a == nil {
		t.Fatal("NewQQOfficialAdapter 必须返回 *QQOfficialAdapter")
	}
	err := a.Start()
	if err == nil {
		t.Fatal("状态文件里的 app_id 与当前配置不一致时，启动必须失败（不能带着旧机器人的映射跑起来）")
	}
	msg := err.Error()
	for _, want := range []string{oldAppID, testAppID, path} {
		if !strings.Contains(msg, want) {
			t.Errorf("拒绝启动的错误信息应点出 %q，实际：%s", want, msg)
		}
	}
	// 必须告诉用户怎么办：换机器人就先归档或删除该状态文件
	if !strings.Contains(msg, "归档") && !strings.Contains(msg, "删除") {
		t.Errorf("错误信息应给出处理办法（换机器人就先归档或删除该状态文件），实际：%s", msg)
	}
	if a.started.Load() {
		t.Error("拒绝启动后 started 必须复位，否则后续 Start 会被当成「已经启动过」")
	}
}

// 状态文件 app_id 为空（老文件/手写文件）：照常放行，构造期不记录致命错误。
func TestAdapterAllowsEmptyStateAppID(t *testing.T) {
	path := t.TempDir() + "/state.json"
	writeStateFileAt(t, path, "", map[string]int64{composeIDKey(testAppID, testGroupOpen): 5}, nil)
	setQQOfficialConfig(t, map[string]string{
		"state-file": path,
		"app-id":     testAppID,
		"app-secret": "test-secret",
	})

	a, ok := NewQQOfficialAdapter(&adapter.AdapterConfig{Mode: string(adapter.AdapterTypeQQOfficial)}).(*QQOfficialAdapter)
	if !ok || a == nil {
		t.Fatal("NewQQOfficialAdapter 必须返回 *QQOfficialAdapter")
	}
	if a.startErr != nil {
		t.Errorf("app_id 为空时必须照常放行，实际记下了致命错误：%v", a.startErr)
	}
	if got := a.ids.ToInt64(testAppID, testGroupOpen); got != 5 {
		t.Errorf("app_id 为空的老文件里的 ID 应继续沿用（5），实际 %d", got)
	}
}

// 启动提示：成员表为空的群要被喊出来，每个群只喊一次，别刷屏。
func TestEmptyMemberTablesWarnOncePerGroup(t *testing.T) {
	a := newTestAdapter(t)
	gA := a.groups.ensure(testGroupOpen)
	gC := a.groups.ensure(groupCOpen)
	if gA == 0 || gC == 0 {
		t.Fatal("测试前置失败：群应已登记进 groupRegistry")
	}

	buf := captureLogs(t)
	a.warnEmptyMemberTables()
	a.warnEmptyMemberTables()

	const marker = "只认识在群里发过言的人"
	logs := buf.String()
	if n := strings.Count(logs, marker); n != 1 {
		t.Fatalf("空成员表的提示应只喊一次（每个群一次、合并成一条），实际 %d 次：%s", n, logs)
	}
	for _, want := range []string{
		fmt.Sprintf("内部群号 %d", gA), // 点名是哪些群
		fmt.Sprintf("内部群号 %d", gC),
		"11253", // 依据：官方成员接口实测不可用
		"管理员",   // 会被误判的能力
		"@全体",   // 同上
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("启动提示应说明 %q，实际日志：%s", want, logs)
		}
	}
	if !strings.Contains(logs, "level=warning") {
		t.Errorf("这条提示应是 Warn 级，实际日志：%s", logs)
	}

	// 群里有人发言之后（该群成员表不再为空），不该再为它喊；新出现的空群才喊
	before := len(logs)
	a.groups.learnMember(testGroupOpen, wsAuthor{MemberOpenID: testOwnerOpen, Username: "群主", MemberRole: "owner"}, a.ids, testAppID)
	a.groups.learnMember(groupCOpen, wsAuthor{MemberOpenID: testOwnerOpen, Username: "群主", MemberRole: "owner"}, a.ids, testAppID)
	a.warnEmptyMemberTables()
	if got := len(buf.String()); got != before {
		t.Errorf("成员表已经有人的群不该再喊，实际又打了 %d 字节日志：%s", got-before, buf.String()[before:])
	}

	gD := a.groups.ensure(groupDOpen)
	a.warnEmptyMemberTables()
	delta := buf.String()[before:]
	if strings.Count(delta, marker) != 1 {
		t.Fatalf("新加入的空群应再喊一次，实际日志：%s", delta)
	}
	if !strings.Contains(delta, fmt.Sprintf("内部群号 %d", gD)) {
		t.Errorf("新出现的空群 %d 应被点名，实际日志：%s", gD, delta)
	}
	if strings.Contains(delta, fmt.Sprintf("内部群号 %d", gA)) {
		t.Errorf("已经不再空的群 %d 不该再被点名，实际日志：%s", gA, delta)
	}
}

// 并发安全：启动期的 warnEmptyMemberTables（判成员表空不空）与首个群事件的
// learnMember（往同一张 map 里写）会撞在同一条路径上，读长度必须在同一把锁内取。
// 这条用例不额外断言行为，它的价值在 -race 下跑：
//
//	CGO_ENABLED=1 go test -race -run TestEmptyMemberTablesWarnRaceFree ./qqofficial/
//
// 出锁以后再 len(成员表) 的话，这里会直接报 map 的读写竞争。
func TestEmptyMemberTablesWarnRaceFree(t *testing.T) {
	a := newTestAdapter(t)
	a.groups.ensure(testGroupOpen)
	a.groups.ensure(groupCOpen)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			a.warnEmptyMemberTables()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			a.groups.learnMember(testGroupOpen, wsAuthor{
				MemberOpenID: fmt.Sprintf("%024d", i),
				Username:     "并发成员",
				MemberRole:   "member",
			}, a.ids, testAppID)
		}
	}()
	wg.Wait()
}
