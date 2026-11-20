package adapter

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 本文件固化「成员缓存失效通知」的行为契约。
//
// 背景：GroupList 平时只由 ReloadGroupList 从 adapter.GetGroupList() 整体重建，
// 但靠事件自学成员的 adapter（实现 MemberListSinkSetter）会在群还没进列表时就来催刷新，
// 那时没有条目可挂 Members，于是 ensureGroupForMemberRefresh 会补一条占位条目 ——
// 这是那条不变量的唯一例外，见 adapter/messenger.go 里该函数的注释。
// 下面四条用例分别锁住：补一次、不覆盖既有条目、可被 ReloadGroupList 整体替换、同群单飞。

func newMemberRefreshMessenger(t *testing.T) (*Messenger, *mockAdapter) {
	t.Helper()
	mock := newMockAdapter()
	return NewMessenger(mock), mock
}

// waitForMemberRefresh 轮询等待条件成立；超时即判失败，避免用固定 sleep 造成的偶发失败。
func waitForMemberRefresh(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

// 群不在 GroupList 时补一条占位条目；同群重复催不会补出第二条。
func TestEnsureGroupForMemberRefresh_AddsSingleStub(t *testing.T) {
	m, mock := newMemberRefreshMessenger(t)
	mock.groupMemberList[777] = []*GroupMemberInfo{
		{GroupID: 777, UserID: 1001, Uin: 1001, Nickname: "Alice"},
	}

	m.InvalidateGroupMembers(777)

	g := m.FindGroup(777)
	require.NotNil(t, g, "群不在列表里时应当补一条占位条目，否则成员表没有落点")
	assert.Equal(t, int64(777), g.Code)
	assert.Empty(t, g.Name, "占位条目不编造群名，名字留给下一次 ReloadGroupList 从 adapter 取")
	assert.Equal(t, 0, g.MemberCount)
	require.Len(t, g.Members, 1, "刷新后成员表应当来自 adapter.GetGroupMemberList")
	assert.Equal(t, int64(1001), g.Members[0].Uin)

	m.InvalidateGroupMembers(777)
	m.groupMu.RLock()
	assert.Len(t, m.GroupList, 1, "重复催刷新不得补出第二条占位条目")
	m.groupMu.RUnlock()
}

// 群已在列表里：占位逻辑不得覆盖既有条目的任何字段，但成员表要刷成 adapter 的。
func TestEnsureGroupForMemberRefresh_KeepsExistingEntry(t *testing.T) {
	m, mock := newMemberRefreshMessenger(t)
	existing := &GroupInfo{Uin: 1, Code: 1, Name: "RealGroup", MemberCount: 42}
	m.GroupList = append(m.GroupList, existing)
	mock.groupList = append(mock.groupList, existing)
	mock.groupMemberList[1] = []*GroupMemberInfo{
		{GroupID: 1, UserID: 2, Uin: 2, Nickname: "Bob", Role: "owner"},
	}

	m.InvalidateGroupMembers(1)

	require.Len(t, m.GroupList, 1)
	g := m.FindGroup(1)
	require.NotNil(t, g)
	assert.Equal(t, "RealGroup", g.Name, "既有条目的字段不能被占位逻辑改写")
	assert.Equal(t, 42, g.MemberCount)
	require.Len(t, g.Members, 1)
	assert.Equal(t, Owner, g.Members[0].Permission, "role=owner 应当映射成群主权限")
}

// 占位条目是临时的：下一次 ReloadGroupList 用 adapter 的结果整体替换 GroupList。
func TestEnsureGroupForMemberRefresh_StubReplacedByReloadGroupList(t *testing.T) {
	m, mock := newMemberRefreshMessenger(t)

	m.InvalidateGroupMembers(888)
	stub := m.FindGroup(888)
	require.NotNil(t, stub, "应当先补出占位条目")
	assert.Empty(t, stub.Name)

	// 注意 ReloadGroupList 读的是 adapter 侧那套字段：GroupID 才映射成 Uin/Code，
	// GroupName 才映射成 Name（GroupInfo 上两套字段并存，别只填一半）。
	mock.groupList = []*GroupInfo{{
		Uin: 888, Code: 888, GroupID: 888,
		Name: "FromAdapter", GroupName: "FromAdapter", MemberCount: 7,
	}}
	require.NoError(t, m.ReloadGroupList())

	g := m.FindGroup(888)
	require.NotNil(t, g)
	assert.Equal(t, "FromAdapter", g.Name, "ReloadGroupList 会整体替换，占位条目随之消失")
	assert.Equal(t, 7, g.MemberCount)
	m.groupMu.RLock()
	assert.Len(t, m.GroupList, 1)
	m.groupMu.RUnlock()
}

// gatedMemberAdapter 让 GetGroupMemberList 阻塞，用来观察同一群的刷新是否真的单飞。
type gatedMemberAdapter struct {
	*mockAdapter
	release chan struct{}
	mu      sync.Mutex
	calls   int
}

func (g *gatedMemberAdapter) GetGroupMemberList(groupID int64) ([]*GroupMemberInfo, error) {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	<-g.release
	return g.mockAdapter.GetGroupMemberList(groupID)
}

func (g *gatedMemberAdapter) callCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.calls
}

// 同一群的刷新单飞：刷新进行中到来的通知只记 pending 并立刻返回，结束后只补刷一次。
func TestInvalidateGroupMembers_SingleFlightCoalescesPending(t *testing.T) {
	mock := newMockAdapter()
	mock.groupMemberList[1] = []*GroupMemberInfo{{GroupID: 1, UserID: 2, Uin: 2, Nickname: "Bob"}}
	gated := &gatedMemberAdapter{mockAdapter: mock, release: make(chan struct{})}
	m := NewMessenger(gated)

	idle := func() bool {
		m.memberRefreshMu.Lock()
		defer m.memberRefreshMu.Unlock()
		return !m.memberRefreshing[1] && !m.memberRefreshPending[1]
	}

	// 第一个调用卡在 GetGroupMemberList 里，后续 8 个通知应当立刻返回（只记 pending）。
	firstDone := make(chan struct{})
	go func() {
		m.InvalidateGroupMembers(1)
		close(firstDone)
	}()
	waitForMemberRefresh(t, "第一次刷新进入 GetGroupMemberList", func() bool { return gated.callCount() == 1 })

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.InvalidateGroupMembers(1)
		}()
	}
	allReturned := make(chan struct{})
	go func() {
		wg.Wait()
		close(allReturned)
	}()
	select {
	case <-allReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("刷新进行中，后续通知应当只记 pending 立刻返回，不该阻塞等待")
	}
	assert.Equal(t, 1, gated.callCount(), "前一次刷新还没结束，不得并发发起第二次")

	close(gated.release) // 放行

	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("第一次刷新没有结束")
	}
	// 8 个通知合并成一次补刷：总数恰好 2（不是 9），且最终没有残留状态。
	waitForMemberRefresh(t, "补刷完成且状态清空", func() bool { return gated.callCount() == 2 && idle() })
	assert.Equal(t, 2, gated.callCount())

	g := m.FindGroup(1)
	require.NotNil(t, g)
	require.Len(t, g.Members, 1)
}
