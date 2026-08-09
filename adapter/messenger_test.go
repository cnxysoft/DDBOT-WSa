package adapter

import (
	"errors"
	"fmt"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/stretchr/testify/assert"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockAdapter implements Adapter for testing.
type mockAdapter struct {
	groupList       []*GroupInfo
	groupMemberList map[int64][]*GroupMemberInfo
	strangerInfo    map[int64]map[string]interface{}
}

func newMockAdapter() *mockAdapter {
	return &mockAdapter{
		groupMemberList: make(map[int64][]*GroupMemberInfo),
		strangerInfo:    make(map[int64]map[string]interface{}),
	}
}

func (m *mockAdapter) Start() error                                     { return nil }
func (m *mockAdapter) Stop() error                                      { return nil }
func (m *mockAdapter) GetSelfID() int64                                 { return 1143469507 }
func (m *mockAdapter) GetAdapterName() string                           { return "mock" }
func (m *mockAdapter) IsConnected() bool                                { return true }
func (m *mockAdapter) GetFileUrl(groupCode int64, fileId string) string { return "" }
func (m *mockAdapter) DownloadFile(url, base64, name string, headers []string) (string, error) {
	return "", nil
}
func (m *mockAdapter) GetMsg(msgId int32) (*GetMsgResult, error)                    { return nil, nil }
func (m *mockAdapter) GetMsgOrg(msgId int32) (interface{}, error)                   { return nil, nil }
func (m *mockAdapter) RecallMsg(msgId int32) error                                  { return nil }
func (m *mockAdapter) GroupPoke(groupCode, target int64) error                      { return nil }
func (m *mockAdapter) FriendPoke(target int64) error                                { return nil }
func (m *mockAdapter) SetGroupBan(groupCode, memberUin int64, duration int64) error { return nil }
func (m *mockAdapter) SetGroupWholeBan(groupCode int64, enable bool) error          { return nil }
func (m *mockAdapter) KickGroupMember(groupCode, memberUin int64, rejectAddRequest bool) error {
	return nil
}
func (m *mockAdapter) SetGroupLeave(groupCode int64, isDismiss bool) error               { return nil }
func (m *mockAdapter) SetGroupAdmin(groupCode, memberUin int64, enable bool) error       { return nil }
func (m *mockAdapter) EditGroupCard(groupCode, memberUin int64, card string) error       { return nil }
func (m *mockAdapter) EditGroupTitle(groupCode, memberUin int64, title string) error     { return nil }
func (m *mockAdapter) SetGroupAddRequest(flag string, approve bool, reason string) error { return nil }
func (m *mockAdapter) SetFriendAddRequest(flag string, approve bool) error               { return nil }

func (m *mockAdapter) SendApi(action string, params map[string]interface{}) (interface{}, error) {
	return nil, nil
}
func (m *mockAdapter) SendGroupMessage(groupID int64, message interface{}) (int32, error) {
	return 1, nil
}
func (m *mockAdapter) SendPrivateMessage(userID int64, message interface{}) (int32, error) {
	return 1, nil
}

type retryMockAdapter struct {
	*mockAdapter
	mu             sync.Mutex
	groupErrors    []error
	groupSendCount int
	connected      bool
}

func newRetryMockAdapter(groupErrors ...error) *retryMockAdapter {
	return &retryMockAdapter{
		mockAdapter: newMockAdapter(),
		groupErrors: groupErrors,
		connected:   true,
	}
}

func (m *retryMockAdapter) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

func (m *retryMockAdapter) setConnected(connected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = connected
}

func (m *retryMockAdapter) SendGroupMessage(groupID int64, message interface{}) (int32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.groupSendCount++
	if len(m.groupErrors) > 0 {
		err := m.groupErrors[0]
		m.groupErrors = m.groupErrors[1:]
		if err != nil {
			return 0, err
		}
	}
	return int32(m.groupSendCount), nil
}

func (m *retryMockAdapter) sendCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.groupSendCount
}
func (m *mockAdapter) SendGroupForwardMessage(groupID int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error) {
	return 1, "", nil
}
func (m *mockAdapter) SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error) {
	return 1, "", nil
}
func (m *mockAdapter) GetGroupList() ([]*GroupInfo, error) { return m.groupList, nil }
func (m *mockAdapter) GetGroupMemberList(groupID int64) ([]*GroupMemberInfo, error) {
	return m.groupMemberList[groupID], nil
}
func (m *mockAdapter) GetFriendList() ([]*FriendInfo, error) { return nil, nil }
func (m *mockAdapter) GetStrangerInfo(userID int64) (map[string]interface{}, error) {
	return m.strangerInfo[userID], nil
}
func (m *mockAdapter) GetGroupInfo(groupID int64) (*GroupInfo, error) { return nil, nil }
func (m *mockAdapter) GetGroupMemberInfo(groupID, userID int64) (*GroupMemberInfo, error) {
	members := m.groupMemberList[groupID]
	for _, mb := range members {
		if mb.Uin == userID {
			return mb, nil
		}
	}
	return nil, nil
}
func (m *mockAdapter) OnGroupMessage(handler func(*GroupMessageEvent))     {}
func (m *mockAdapter) OnPrivateMessage(handler func(*PrivateMessageEvent)) {}
func (m *mockAdapter) OnMetaEvent(handler func(*MetaEvent))                {}
func (m *mockAdapter) OnNoticeEvent(handler func(*NoticeEvent))            {}
func (m *mockAdapter) OnRequestEvent(handler func(*RequestEvent))          {}

// mockDispatcher implements BotEventDispatcher for testing.
type mockDispatcher struct {
	mu     sync.Mutex
	events []string
}

func newMockDispatcher() *mockDispatcher {
	return &mockDispatcher{}
}

func (d *mockDispatcher) DispatchGroupMessage(msg *GroupMessage)     {}
func (d *mockDispatcher) DispatchPrivateMessage(msg *PrivateMessage) {}
func (d *mockDispatcher) DispatchGroupRecall(event *GroupMessageRecalledEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_recall")
}
func (d *mockDispatcher) DispatchFriendRecall(event *FriendMessageRecalledEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "friend_recall")
}
func (d *mockDispatcher) DispatchGroupMute(event *GroupMuteEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_ban")
}
func (d *mockDispatcher) DispatchDisconnected(event *ClientDisconnectedEvent) {}
func (d *mockDispatcher) DispatchGroupMemberJoin(event *MemberJoinGroupEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_increase")
}
func (d *mockDispatcher) DispatchGroupMemberLeave(event *MemberLeaveGroupEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_decrease")
}
func (d *mockDispatcher) DispatchGroupJoin(event *GroupInfo)        {}
func (d *mockDispatcher) DispatchGroupLeave(event *GroupLeaveEvent) {}
func (d *mockDispatcher) DispatchGroupMemberPermissionChanged(event *MemberPermissionChangedEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_admin")
}
func (d *mockDispatcher) DispatchMemberCardUpdated(event *MemberCardUpdatedEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_card")
}
func (d *mockDispatcher) DispatchMemberSpecialTitleUpdated(event *MemberSpecialTitleUpdatedEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "notify_title")
}
func (d *mockDispatcher) DispatchGroupUploadNotify(event *GroupUploadNotifyEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_upload")
}
func (d *mockDispatcher) DispatchGroupNotify(event NotifyEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "notify_poke")
}
func (d *mockDispatcher) DispatchFriendNotify(event NotifyEvent)                {}
func (d *mockDispatcher) DispatchGroupNameUpdated(event *GroupNameUpdatedEvent) {}
func (d *mockDispatcher) DispatchGroupEssenceChanged(event *GroupDigestEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "essence")
}
func (d *mockDispatcher) DispatchGroupDisband(event *GroupDisbandEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_dismiss")
}
func (d *mockDispatcher) DispatchNewFriendRequest(event *NewFriendRequest) {}
func (d *mockDispatcher) DispatchNewFriend(event *NewFriendEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "friend_add")
}
func (d *mockDispatcher) DispatchUserJoinGroupRequest(event *UserJoinGroupRequest) {}
func (d *mockDispatcher) DispatchGroupInvitedRequest(event *GroupInvitedRequest)   {}
func (d *mockDispatcher) DispatchBotOnline(event *BotOnlineEvent)                  {}
func (d *mockDispatcher) DispatchBotOffline(event *BotOfflineEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "bot_offline")
}
func (d *mockDispatcher) DispatchGroupMsgEmojiLike(event *GroupMsgEmojiLikeEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_msg_emoji_like")
}
func (d *mockDispatcher) DispatchProfileLike(event *ProfileLikeEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "profile_like")
}
func (d *mockDispatcher) DispatchPokeRecall(event *PokeRecallEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "poke_recall")
}

func (d *mockDispatcher) getEvents() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string{}, d.events...)
}

// setupTestMessenger creates a Messenger with a mock adapter and dispatcher,
// pre-populated with one group containing three members.
func setupTestMessenger(t *testing.T) (*Messenger, *mockAdapter, *mockDispatcher) {
	t.Helper()
	adapter := newMockAdapter()
	messenger := NewMessenger(adapter)
	dispatcher := newMockDispatcher()
	messenger.SetBotEventDispatcher(dispatcher)

	// Set bot offline for testing consistency
	messenger.Online.Store(false)

	group := &GroupInfo{
		Uin:  545402644,
		Code: 545402644,
		Name: "TestGroup",
		Members: []*GroupMemberInfo{
			{Uin: 1001, Nickname: "Alice", CardName: "Alice Card"},
			{Uin: 1002, Nickname: "Bob", CardName: "Bob Card"},
			{Uin: 785829865, Nickname: "KickedUser", CardName: "KickedUser Card"},
		},
	}
	messenger.GroupList = append(messenger.GroupList, group)
	adapter.groupList = append(adapter.groupList, group)
	adapter.groupMemberList[545402644] = group.Members

	return messenger, adapter, dispatcher
}

// TestMessengerHandleNoticeEvent_GroupDecrease tests that group_decrease does not deadlock.
// This was previously deadlocking because RemoveGroupMember called groupMu.Lock() and then
// FindGroupByUin (which tries to acquire groupMu.RLock()), violating RWMutex semantics
// (same goroutine cannot acquire RLock while holding Lock).
func TestMessengerHandleNoticeEvent_GroupDecrease(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	assert.Len(t, m.GroupList[0].Members, 3)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "group_decrease",
			SubType:    "kick",
			GroupID:    545402644,
			UserID:     785829865,
			OperatorID: 3127124559,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
		// Success - no deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on group_decrease")
	}

	assert.Len(t, m.GroupList[0].Members, 2)
	found := false
	for _, m := range m.GroupList[0].Members {
		if m.Uin == 785829865 {
			found = true
			break
		}
	}
	assert.False(t, found, "kicked user should be removed from group members")

	events := dispatcher.getEvents()
	assert.Contains(t, events, "group_decrease")
}

// TestMessengerHandleNoticeEvent_GroupIncrease tests that group_increase does not deadlock.
func TestMessengerHandleNoticeEvent_GroupIncrease(t *testing.T) {
	m, adapter, dispatcher := setupTestMessenger(t)

	adapter.groupMemberList[545402644] = []*GroupMemberInfo{
		{Uin: 1001, Nickname: "Alice"},
		{Uin: 1002, Nickname: "Bob"},
		{Uin: 1003, Nickname: "Charlie", Card: "Charlie Card", Role: "member"},
	}
	adapter.strangerInfo[1003] = map[string]interface{}{"nickname": "Charlie"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "group_increase",
			SubType:    "approve",
			GroupID:    545402644,
			UserID:     1003,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on group_increase")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "group_increase")
}

// TestMessengerHandleNoticeEvent_GroupBan tests that group_ban does not deadlock.
func TestMessengerHandleNoticeEvent_GroupBan(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "group_ban",
			SubType:    "ban",
			GroupID:    545402644,
			UserID:     1001,
			OperatorID: 1002,
			Duration:   600,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on group_ban")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "group_ban")
}

// TestMessengerHandleNoticeEvent_GroupAdmin tests that group_admin does not deadlock.
func TestMessengerHandleNoticeEvent_GroupAdmin(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "group_admin",
			SubType:    "set",
			GroupID:    545402644,
			UserID:     1001,
			OperatorID: 1002,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on group_admin")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "group_admin")
}

// TestMessengerHandleNoticeEvent_GroupCard tests that group_card does not deadlock.
func TestMessengerHandleNoticeEvent_GroupCard(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "group_card",
			SubType:    "update",
			GroupID:    545402644,
			UserID:     1001,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on group_card")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "group_card")
}

// TestMessengerHandleNoticeEvent_FriendAdd tests that friend_add does not deadlock.
func TestMessengerHandleNoticeEvent_FriendAdd(t *testing.T) {
	m, adapter, dispatcher := setupTestMessenger(t)
	adapter.strangerInfo[9999] = map[string]interface{}{"nickname": "NewFriend"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "friend_add",
			UserID:     9999,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on friend_add")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "friend_add")
}

// TestMessengerHandleNoticeEvent_FriendRecall tests that friend_recall does not deadlock.
func TestMessengerHandleNoticeEvent_FriendRecall(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "friend_recall",
			UserID:     1001,
			MessageID:  12345,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on friend_recall")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "friend_recall")
}

// TestMessengerHandleNoticeEvent_GroupRecall tests that group_recall does not deadlock.
func TestMessengerHandleNoticeEvent_GroupRecall(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "group_recall",
			GroupID:    545402644,
			UserID:     1001,
			OperatorID: 1002,
			MessageID:  12345,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on group_recall")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "group_recall")
}

// TestMessengerHandleNoticeEvent_Essence tests that essence does not deadlock.
func TestMessengerHandleNoticeEvent_Essence(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "essence",
			SubType:    "add",
			GroupID:    545402644,
			UserID:     1001,
			OperatorID: 1002,
			MessageID:  12345,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on essence")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "essence")
}

// TestMessengerHandleNoticeEvent_GroupUpload tests that group_upload does not deadlock.
func TestMessengerHandleNoticeEvent_GroupUpload(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "group_upload",
			GroupID:    545402644,
			UserID:     1001,
			File:       GroupFile{},
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on group_upload")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "group_upload")
}

// TestMessengerHandleNoticeEvent_BotOffline tests that bot_offline does not deadlock.
func TestMessengerHandleNoticeEvent_BotOffline(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "bot_offline",
			SelfID:     1143469507,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on bot_offline")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "bot_offline")
}

// TestMessengerHandleNoticeEvent_GroupDismiss tests that group_dismiss does not deadlock.
func TestMessengerHandleNoticeEvent_GroupDismiss(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "group_dismiss",
			GroupID:    545402644,
			UserID:     1001,
			OperatorID: 1002,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on group_dismiss")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "group_dismiss")
}

// TestMessengerHandleNoticeEvent_GroupMsgEmojiLike tests that group_msg_emoji_like does not deadlock.
func TestMessengerHandleNoticeEvent_GroupMsgEmojiLike(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "group_msg_emoji_like",
			SubType:    "add",
			GroupID:    545402644,
			UserID:     1001,
			MessageID:  12345,
			EmojiId:    "笑脸",
			EmojiCount: 1,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on group_msg_emoji_like")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "group_msg_emoji_like")
}

// TestMessengerHandleNoticeEvent_NotifyPoke tests that notify.poke does not deadlock.
func TestMessengerHandleNoticeEvent_NotifyPoke(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "notify",
			SubType:    "poke",
			GroupID:    545402644,
			UserID:     1001,
			OperatorID: 1002,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on notify.poke")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "notify_poke")
}

// TestMessengerHandleNoticeEvent_NotifyTitle tests that notify.title does not deadlock.
func TestMessengerHandleNoticeEvent_NotifyTitle(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "notify",
			SubType:    "title",
			GroupID:    545402644,
			UserID:     1001,
			Title:      "VIP",
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on notify.title")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "notify_title")
}

// TestMessengerHandleNoticeEvent_ProfileLike tests that notify.profile_like does not deadlock.
func TestMessengerHandleNoticeEvent_ProfileLike(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType:   "notify",
			SubType:      "profile_like",
			OperatorID:   1001,
			OperatorNick: "Alice",
			Times:        3,
			Time:         time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on notify.profile_like")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "profile_like")
}

// TestMessengerHandleNoticeEvent_PokeRecall tests that notify.poke_recall does not deadlock.
func TestMessengerHandleNoticeEvent_PokeRecall(t *testing.T) {
	m, _, dispatcher := setupTestMessenger(t)

	done := make(chan struct{})
	go func() {
		defer close(done)
		m.handleNoticeEvent(&NoticeEvent{
			NoticeType: "notify",
			SubType:    "poke_recall",
			GroupID:    545402644,
			UserID:     1001,
			OperatorID: 1002,
			Time:       time.Now().Unix(),
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("handleNoticeEvent deadlocked on notify.poke_recall")
	}

	events := dispatcher.getEvents()
	assert.Contains(t, events, "poke_recall")
}

// TestMessengerHandleNoticeEvent_Concurrent verifies that concurrent calls to handleNoticeEvent
// with various notice types do not cause deadlock.
func TestMessengerHandleNoticeEvent_Concurrent(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	const iterations = 200
	noticeTypes := []string{
		"group_decrease", "group_increase", "group_ban", "group_admin",
		"group_card", "group_recall", "essence", "group_upload",
		"bot_offline", "group_dismiss", "group_msg_emoji_like",
		"friend_recall",
	}

	var wg sync.WaitGroup
	for _, noticeType := range noticeTypes {
		wg.Add(1)
		go func(nt string) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				event := &NoticeEvent{
					NoticeType: nt,
					GroupID:    545402644,
					UserID:     1001,
					OperatorID: 1002,
					Time:       time.Now().Unix(),
				}
				if nt == "group_decrease" {
					event.UserID = 785829865
				}
				m.handleNoticeEvent(event)
			}
		}(noticeType)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent handleNoticeEvent deadlocked")
	}
}

// TestMessenger_RemoveGroupMember_ConcurrentWithFindGroupByUin tests the specific deadlock
// scenario where RemoveGroupMember (with Lock) is called concurrently with FindGroupByUin (with RLock).
func TestMessenger_RemoveGroupMember_ConcurrentWithFindGroupByUin(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	const iterations = 1000
	var wg sync.WaitGroup
	wg.Add(2)

	// Continuously call FindGroupByUin (acquires RLock)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = m.FindGroupByUin(545402644)
		}
	}()

	// Continuously call RemoveGroupMember (acquires Lock then RLock internally)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			m.RemoveGroupMember(545402644, 785829865)
			// Re-add the member for next iteration
			m.GroupList[0].Members = append(m.GroupList[0].Members, &GroupMemberInfo{Uin: 785829865, Nickname: "KickedUser"})
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - completed without deadlock
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent RemoveGroupMember and FindGroupByUin deadlocked")
	}
}

// TestMessenger_UpdateGroupMember_NoDeadlock verifies that UpdateGroupMember does not deadlock
// when called concurrently with RemoveGroupMember.
func TestMessenger_UpdateGroupMember_NoDeadlock(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	const iterations = 1000
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			m.UpdateGroupMember(545402644, 1001, func(member *GroupMemberInfo) {
				member.Permission = Administrator
			})
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			m.RemoveGroupMember(545402644, 785829865)
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - completed without deadlock
	case <-time.After(10 * time.Second):
		t.Fatal("UpdateGroupMember and RemoveGroupMember deadlocked")
	}
}

// mockRequestDispatcher embeds mockDispatcher and overrides only the request
// dispatch methods so we can capture and verify the events.
type mockRequestDispatcher struct {
	*mockDispatcher
	newFriendRequest *NewFriendRequest
	groupInvited     *GroupInvitedRequest
	userJoinGroup    *UserJoinGroupRequest
}

func newMockRequestDispatcher() *mockRequestDispatcher {
	return &mockRequestDispatcher{mockDispatcher: newMockDispatcher()}
}

func (d *mockRequestDispatcher) DispatchNewFriendRequest(event *NewFriendRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.newFriendRequest = event
}
func (d *mockRequestDispatcher) DispatchGroupInvitedRequest(event *GroupInvitedRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.groupInvited = event
}
func (d *mockRequestDispatcher) DispatchUserJoinGroupRequest(event *UserJoinGroupRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.userJoinGroup = event
}

func (d *mockRequestDispatcher) getFriendRequest() *NewFriendRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.newFriendRequest
}
func (d *mockRequestDispatcher) getGroupInvited() *GroupInvitedRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.groupInvited
}
func (d *mockRequestDispatcher) getUserJoinGroup() *UserJoinGroupRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.userJoinGroup
}

// TestMessengerHandleRequestEvent_NewFriendRequest verifies flag is correctly
// stored in the dispatched NewFriendRequest event.
func TestMessengerHandleRequestEvent_NewFriendRequest(t *testing.T) {
	m, _, _ := setupTestMessenger(t)
	dispatcher := newMockRequestDispatcher()
	m.SetBotEventDispatcher(dispatcher)

	m.handleRequestEvent(&RequestEvent{
		RequestType: "friend",
		Time:        1234567890,
		SelfID:      1143469507,
		UserID:      123456,
		Comment:     "hello",
		Flag:        "test_friend_flag_abc123",
		SubType:     "",
	})

	req := dispatcher.getFriendRequest()
	assert.NotNil(t, req, "NewFriendRequest should be dispatched")
	assert.Equal(t, "test_friend_flag_abc123", req.Flag, "flag should match event.Flag")
	assert.Equal(t, int64(123456), req.RequesterUin, "RequesterUin should match UserID")
	assert.Equal(t, "hello", req.Message, "Message should match Comment")
}

// TestMessengerHandleRequestEvent_GroupInvited verifies flag is correctly
// stored in the dispatched GroupInvitedRequest event.
func TestMessengerHandleRequestEvent_GroupInvited(t *testing.T) {
	m, _, _ := setupTestMessenger(t)
	dispatcher := newMockRequestDispatcher()
	m.SetBotEventDispatcher(dispatcher)

	m.handleRequestEvent(&RequestEvent{
		RequestType: "group",
		Time:        1234567890,
		SelfID:      1143469507,
		GroupID:     545402644,
		UserID:      123456,
		Comment:     "",
		Flag:        "test_group_invite_flag_xyz789",
		SubType:     "invite",
	})

	req := dispatcher.getGroupInvited()
	assert.NotNil(t, req, "GroupInvitedRequest should be dispatched")
	assert.Equal(t, "test_group_invite_flag_xyz789", req.Flag, "flag should match event.Flag")
}

func TestOfflineQueue_RetriesFailedGroupMessage(t *testing.T) {
	oldEnable := config.GlobalConfig.GetBool("bot.offlineQueue.enable")
	oldDelay := offlineQueueRetryDelay
	config.GlobalConfig.Set("bot.offlineQueue.enable", true)
	offlineQueueRetryDelay = 10 * time.Millisecond
	defer func() {
		config.GlobalConfig.Set("bot.offlineQueue.enable", oldEnable)
		offlineQueueRetryDelay = oldDelay
	}()

	mock := newRetryMockAdapter(fmt.Errorf("%w: not connected", ErrRequestNotSent))
	messenger := NewMessenger(mock)
	defer messenger.Stop()
	messenger.Online.Store(true)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "retry me"})
	resp := messenger.SendGroupMessage(545402644, msg, "retry me")

	assert.ErrorIs(t, resp.Error, ErrRequestNotSent)
	assert.Equal(t, int64(-1), resp.RetMSG.ID)
	assert.Eventually(t, func() bool {
		return mock.sendCount() == 2 && len(messenger.loadOfflineMsgs()) == 0
	}, time.Second, 10*time.Millisecond)
}

func TestOfflineQueue_DoesNotRetryTimedOutGroupMessage(t *testing.T) {
	oldEnable := config.GlobalConfig.GetBool("bot.offlineQueue.enable")
	oldDelay := offlineQueueRetryDelay
	config.GlobalConfig.Set("bot.offlineQueue.enable", true)
	offlineQueueRetryDelay = 10 * time.Millisecond
	defer func() {
		config.GlobalConfig.Set("bot.offlineQueue.enable", oldEnable)
		offlineQueueRetryDelay = oldDelay
	}()

	mock := newRetryMockAdapter(ErrRequestTimeout)
	messenger := NewMessenger(mock)
	defer messenger.Stop()
	messenger.Online.Store(true)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "unknown result"})
	resp := messenger.SendGroupMessage(545402644, msg, "unknown result")

	assert.ErrorIs(t, resp.Error, ErrRequestTimeout)
	assert.Empty(t, messenger.loadOfflineMsgs())
	time.Sleep(3 * offlineQueueRetryDelay)
	assert.Equal(t, 1, mock.sendCount())
}

func TestOfflineQueue_DoesNotRetryUnknownResultAfterDisconnect(t *testing.T) {
	oldEnable := config.GlobalConfig.GetBool("bot.offlineQueue.enable")
	oldDelay := offlineQueueRetryDelay
	config.GlobalConfig.Set("bot.offlineQueue.enable", true)
	offlineQueueRetryDelay = 10 * time.Millisecond
	defer func() {
		config.GlobalConfig.Set("bot.offlineQueue.enable", oldEnable)
		offlineQueueRetryDelay = oldDelay
	}()

	mock := newRetryMockAdapter(fmt.Errorf("%w: connection closed while waiting for echo", ErrRequestResultUnknown))
	messenger := NewMessenger(mock)
	defer messenger.Stop()
	messenger.Online.Store(true)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "unknown after disconnect"})
	resp := messenger.SendGroupMessage(545402644, msg, "unknown after disconnect")

	assert.ErrorIs(t, resp.Error, ErrRequestResultUnknown)
	assert.Empty(t, messenger.loadOfflineMsgs())
	time.Sleep(3 * offlineQueueRetryDelay)
	assert.Equal(t, 1, mock.sendCount())
}

func TestOfflineQueue_DoesNotRetryRejectedGroupMessage(t *testing.T) {
	oldEnable := config.GlobalConfig.GetBool("bot.offlineQueue.enable")
	oldDelay := offlineQueueRetryDelay
	config.GlobalConfig.Set("bot.offlineQueue.enable", true)
	offlineQueueRetryDelay = 10 * time.Millisecond
	defer func() {
		config.GlobalConfig.Set("bot.offlineQueue.enable", oldEnable)
		offlineQueueRetryDelay = oldDelay
	}()

	mock := newRetryMockAdapter(fmt.Errorf("%w: retcode=1200 message=bot muted", ErrRequestRejected))
	messenger := NewMessenger(mock)
	defer messenger.Stop()
	messenger.Online.Store(true)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "do not retry"})
	resp := messenger.SendGroupMessage(545402644, msg, "do not retry")

	assert.ErrorIs(t, resp.Error, ErrRequestRejected)
	assert.Empty(t, messenger.loadOfflineMsgs())
	time.Sleep(3 * offlineQueueRetryDelay)
	assert.Equal(t, 1, mock.sendCount())
}

func TestOfflineQueue_DoesNotRetryUnclassifiedAdapterError(t *testing.T) {
	oldEnable := config.GlobalConfig.GetBool("bot.offlineQueue.enable")
	oldDelay := offlineQueueRetryDelay
	config.GlobalConfig.Set("bot.offlineQueue.enable", true)
	offlineQueueRetryDelay = 10 * time.Millisecond
	defer func() {
		config.GlobalConfig.Set("bot.offlineQueue.enable", oldEnable)
		offlineQueueRetryDelay = oldDelay
	}()

	mock := newRetryMockAdapter(errors.New("satori request failed"))
	messenger := NewMessenger(mock)
	defer messenger.Stop()
	messenger.Online.Store(true)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "unclassified error"})
	resp := messenger.SendGroupMessage(545402644, msg, "unclassified error")

	assert.Error(t, resp.Error)
	assert.Empty(t, messenger.loadOfflineMsgs())
	time.Sleep(3 * offlineQueueRetryDelay)
	assert.Equal(t, 1, mock.sendCount())
}

func TestOfflineQueue_FlushesAfterReconnectWithoutLifecycleEvent(t *testing.T) {
	oldEnable := config.GlobalConfig.GetBool("bot.offlineQueue.enable")
	oldDelay := offlineQueueRetryDelay
	config.GlobalConfig.Set("bot.offlineQueue.enable", true)
	offlineQueueRetryDelay = 10 * time.Millisecond
	defer func() {
		config.GlobalConfig.Set("bot.offlineQueue.enable", oldEnable)
		offlineQueueRetryDelay = oldDelay
	}()

	mock := newRetryMockAdapter()
	mock.setConnected(false)
	messenger := NewMessenger(mock)
	defer messenger.Stop()
	// 模拟只有WebSocket断线，未收到新的lifecycle事件，Messenger的Online仍为true。
	messenger.Online.Store(true)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "send after reconnect"})
	resp := messenger.SendGroupMessage(545402644, msg, "send after reconnect")
	assert.NoError(t, resp.Error)
	assert.Len(t, messenger.loadOfflineMsgs(), 1)

	// 先让一次定时刷新在断线状态下触发，确认它不会永久退出。
	time.Sleep(3 * offlineQueueRetryDelay)
	assert.Equal(t, 0, mock.sendCount())
	assert.Len(t, messenger.loadOfflineMsgs(), 1)

	mock.setConnected(true)
	assert.Eventually(t, func() bool {
		return mock.sendCount() == 1 && len(messenger.loadOfflineMsgs()) == 0
	}, time.Second, 10*time.Millisecond)
}

func TestOfflineQueue_FlushDropsTimedOutMessages(t *testing.T) {
	oldEnable := config.GlobalConfig.GetBool("bot.offlineQueue.enable")
	oldDelay := offlineQueueRetryDelay
	config.GlobalConfig.Set("bot.offlineQueue.enable", true)
	offlineQueueRetryDelay = 10 * time.Millisecond
	defer func() {
		config.GlobalConfig.Set("bot.offlineQueue.enable", oldEnable)
		offlineQueueRetryDelay = oldDelay
	}()

	mock := newRetryMockAdapter(fmt.Errorf("%w: not connected", ErrRequestNotSent), ErrRequestTimeout)
	messenger := NewMessenger(mock)
	defer messenger.Stop()
	messenger.Online.Store(true)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "keep me"})
	messenger.SendGroupMessage(545402644, msg, "keep me")
	assert.Len(t, messenger.loadOfflineMsgs(), 1)

	messenger.flushOfflineQueue()
	assert.Empty(t, messenger.loadOfflineMsgs())
	assert.Equal(t, 2, mock.sendCount())
	time.Sleep(3 * offlineQueueRetryDelay)
	assert.Equal(t, 2, mock.sendCount())
}

func TestOfflineQueue_FlushDropsUnclassifiedAdapterError(t *testing.T) {
	oldEnable := config.GlobalConfig.GetBool("bot.offlineQueue.enable")
	oldDelay := offlineQueueRetryDelay
	config.GlobalConfig.Set("bot.offlineQueue.enable", true)
	offlineQueueRetryDelay = 10 * time.Millisecond
	defer func() {
		config.GlobalConfig.Set("bot.offlineQueue.enable", oldEnable)
		offlineQueueRetryDelay = oldDelay
	}()

	mock := newRetryMockAdapter(errors.New("satori response lost"))
	messenger := NewMessenger(mock)
	defer messenger.Stop()
	messenger.Online.Store(true)
	messenger.saveOfflineMsg(newOfflineQueueMsg(545402644, "group", &SendingMessage{}, "queued"))

	messenger.flushOfflineQueue()

	assert.Empty(t, messenger.loadOfflineMsgs())
	time.Sleep(3 * offlineQueueRetryDelay)
	assert.Equal(t, 1, mock.sendCount())
}

func TestOfflineQueue_MaxSizeDropsOnlyOldest(t *testing.T) {
	messenger, _, _ := setupTestMessenger(t)
	defer messenger.Stop()

	for i := 0; i < offlineQueueMaxSize+1; i++ {
		messenger.saveOfflineMsg(offlineQueueMsg{
			TargetId:   int64(i),
			TargetType: "group",
			Message:    &SendingMessage{},
			CreatedAt:  time.Now(),
		})
	}

	msgs := messenger.loadOfflineMsgs()
	assert.Len(t, msgs, offlineQueueMaxSize)
	assert.Equal(t, int64(1), msgs[0].TargetId)
	assert.Equal(t, int64(offlineQueueMaxSize), msgs[len(msgs)-1].TargetId)
}

// TestOfflineQueue_SaveAndLoad tests saving and loading offline messages.
func TestOfflineQueue_SaveAndLoad(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	msg1 := offlineQueueMsg{
		TargetId:   123456,
		TargetType: "group",
		Message:    &SendingMessage{},
		NewStr:     "test message 1",
		CreatedAt:  time.Now(),
	}
	msg2 := offlineQueueMsg{
		TargetId:   789012,
		TargetType: "group",
		Message:    &SendingMessage{},
		NewStr:     "test message 2",
		CreatedAt:  time.Now(),
	}

	m.saveOfflineMsg(msg1)
	m.saveOfflineMsg(msg2)

	msgs := m.loadOfflineMsgs()
	assert.Len(t, msgs, 2)
	assert.Equal(t, int64(123456), msgs[0].TargetId)
	assert.Equal(t, "group", msgs[0].TargetType)
	assert.Equal(t, "test message 1", msgs[0].NewStr)

	m.clearOfflineMsgs()
	msgs = m.loadOfflineMsgs()
	assert.Len(t, msgs, 0)
}

// TestOfflineQueue_PrivateMessage tests private message offline queue.
func TestOfflineQueue_PrivateMessage(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	groupMsg := offlineQueueMsg{
		TargetId:   111111,
		TargetType: "group",
		Message:    &SendingMessage{},
		NewStr:     "group test message",
		CreatedAt:  time.Now(),
	}
	privateMsg := offlineQueueMsg{
		TargetId:   222222,
		TargetType: "private",
		Message:    &SendingMessage{},
		NewStr:     "private test message",
		CreatedAt:  time.Now(),
	}

	m.saveOfflineMsg(groupMsg)
	m.saveOfflineMsg(privateMsg)

	msgs := m.loadOfflineMsgs()
	assert.Len(t, msgs, 2)

	// Verify group message
	assert.Equal(t, int64(111111), msgs[0].TargetId)
	assert.Equal(t, "group", msgs[0].TargetType)
	assert.Equal(t, "group test message", msgs[0].NewStr)

	// Verify private message
	assert.Equal(t, int64(222222), msgs[1].TargetId)
	assert.Equal(t, "private", msgs[1].TargetType)
	assert.Equal(t, "private test message", msgs[1].NewStr)

	m.clearOfflineMsgs()
	msgs = m.loadOfflineMsgs()
	assert.Len(t, msgs, 0)
}

// TestOfflineQueue_Expiration tests queue behavior with expired messages.
// Note: flushOfflineQueue requires config to be enabled, so we test
// the queue operations directly without relying on config.
func TestOfflineQueue_Expiration(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	oldMsg := offlineQueueMsg{
		TargetId:   123456,
		TargetType: "group",
		Message:    &SendingMessage{},
		NewStr:     "old message",
		CreatedAt:  time.Now().Add(-2 * time.Hour),
	}
	m.saveOfflineMsg(oldMsg)

	recentMsg := offlineQueueMsg{
		TargetId:   789012,
		TargetType: "group",
		Message:    &SendingMessage{},
		NewStr:     "recent message",
		CreatedAt:  time.Now(),
	}
	m.saveOfflineMsg(recentMsg)

	// Verify both messages are in queue
	msgs := m.loadOfflineMsgs()
	assert.Len(t, msgs, 2)

	// Test message age check logic (without flush)
	now := time.Now()
	expire := 30 * time.Minute // default expire
	for _, msg := range msgs {
		if now.Sub(msg.CreatedAt) > expire {
			messengerLogger.Infof("过期消息: %v", msg.NewStr)
		}
	}

	// Clear for next test
	m.clearOfflineMsgs()
}

// TestOfflineQueue_QueueOperations tests queue operations.
func TestOfflineQueue_QueueOperations(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	// Test direct queue operations
	msg := offlineQueueMsg{
		TargetId:   123456,
		TargetType: "group",
		Message:    &SendingMessage{},
		NewStr:     "direct test",
		CreatedAt:  time.Now(),
	}

	// Save
	m.saveOfflineMsg(msg)

	// Load
	msgs := m.loadOfflineMsgs()
	assert.Len(t, msgs, 1)
	assert.Equal(t, int64(123456), msgs[0].TargetId)
	assert.Equal(t, "group", msgs[0].TargetType)

	// Clear
	m.clearOfflineMsgs()
	msgs = m.loadOfflineMsgs()
	assert.Len(t, msgs, 0)
}

// TestOfflineQueue_ConcurrentAccess tests that concurrent queue operations are safe.
func TestOfflineQueue_ConcurrentAccess(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	const iterations = 100
	var wg sync.WaitGroup
	wg.Add(3)

	// Concurrent saves
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			m.saveOfflineMsg(offlineQueueMsg{
				TargetId:   int64(i),
				TargetType: "group",
				Message:    &SendingMessage{},
				NewStr:     "test",
				CreatedAt:  time.Now(),
			})
		}
	}()

	// Concurrent loads
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			_ = m.loadOfflineMsgs()
		}
	}()

	// Concurrent clears
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			m.clearOfflineMsgs()
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success - no deadlock
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent queue operations deadlocked")
	}
}

// TestOfflineQueue_SliceMessage tests sliceMessage helper.
func TestOfflineQueue_SliceMessage(t *testing.T) {
	short := "hello"
	assert.Equal(t, short, sliceMessage(short))

	long := strings.Repeat("a", 100)
	sliced := sliceMessage(long)
	assert.Equal(t, 78, len(sliced))
	assert.True(t, strings.HasSuffix(sliced, "..."))
}

// TestMessengerHandleRequestEvent_UserJoinGroup verifies flag is correctly
// stored in the dispatched UserJoinGroupRequest event.
func TestMessengerHandleRequestEvent_UserJoinGroup(t *testing.T) {
	m, _, _ := setupTestMessenger(t)
	dispatcher := newMockRequestDispatcher()
	m.SetBotEventDispatcher(dispatcher)

	m.handleRequestEvent(&RequestEvent{
		RequestType: "group",
		Time:        1234567890,
		SelfID:      1143469507,
		GroupID:     545402644,
		UserID:      123456,
		Comment:     "please let me in",
		Flag:        "test_join_group_flag_uvw456",
		SubType:     "add",
	})

	req := dispatcher.getUserJoinGroup()
	assert.NotNil(t, req, "UserJoinGroupRequest should be dispatched")
	assert.Equal(t, "test_join_group_flag_uvw456", req.Flag, "flag should match event.Flag")
}

// TestBuildMessageChunks_PlainText tests that long text messages are split correctly.
func TestBuildMessageChunks_PlainText(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	// Create a text message longer than MaxTextLength
	longText := strings.Repeat("a", 5000)
	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: longText})

	chunks := m.buildMessageChunks(msg)

	// Should be split - 5000 chars should result in at least 2 chunks (4500 + 500)
	assert.GreaterOrEqual(t, len(chunks), 2, "long text should be split into multiple chunks")

	// Verify each chunk's text length is within limits (allow some variance for estimation)
	for i, chunk := range chunks {
		textLen := calculateTextLength(chunk)
		assert.LessOrEqual(t, textLen, MaxTextLength+10, "chunk %d text length should be within limit", i)
	}
}

// TestBuildMessageChunks_MultipleTextSegments tests text splitting across multiple segments.
func TestBuildMessageChunks_MultipleTextSegments(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	// Create multiple text segments that together exceed MaxTextLength
	msg := &SendingMessage{}
	// Add 3 segments of 2000 chars each = 6000 chars total
	for i := 0; i < 3; i++ {
		msg.Append(&TextSegment{Content: strings.Repeat("b", 2000)})
	}

	chunks := m.buildMessageChunks(msg)

	// Should be split
	assert.GreaterOrEqual(t, len(chunks), 2, "multiple text segments should be split")

	// Verify total content is preserved
	var totalTextLen int
	for _, chunk := range chunks {
		totalTextLen += calculateTextLength(chunk)
	}
	assert.Equal(t, 6000, totalTextLen, "total text length should be preserved after splitting")
}

// TestBuildMessageChunks_Images tests that messages with many images are split correctly.
func TestBuildMessageChunks_Images(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	msg := &SendingMessage{}
	// Add 25 images (more than MaxImageCount of 20)
	for i := 0; i < 25; i++ {
		msg.Append(&ImageSegment{
			File: fmt.Sprintf("image_%d.jpg", i),
			Url:  fmt.Sprintf("https://example.com/image_%d.jpg", i),
		})
	}

	chunks := m.buildMessageChunks(msg)

	// Should be split into at least 2 chunks (20 + 5)
	assert.GreaterOrEqual(t, len(chunks), 2, "many images should be split into multiple chunks")

	// Verify image counts per chunk
	for i, chunk := range chunks {
		imgCount := countImages(chunk)
		assert.LessOrEqual(t, imgCount, MaxImageCount, "chunk %d image count should not exceed MaxImageCount", i)
	}
}

// TestBuildMessageChunks_MixedContent tests mixed content (text + images + at).
func TestBuildMessageChunks_MixedContent(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	msg := &SendingMessage{}
	// Add: text (1000 chars) + at + 10 images + text (1000 chars)
	msg.Append(&TextSegment{Content: strings.Repeat("x", 1000)})
	msg.Append(&AtSegment{Target: 1001})
	for i := 0; i < 10; i++ {
		msg.Append(&ImageSegment{
			File: fmt.Sprintf("img_%d.jpg", i),
			Url:  fmt.Sprintf("https://example.com/img_%d.jpg", i),
		})
	}
	msg.Append(&TextSegment{Content: strings.Repeat("y", 1000)})

	chunks := m.buildMessageChunks(msg)

	// Should not need splitting since we're well under limits
	assert.Equal(t, 1, len(chunks), "small mixed content should not be split")

	// Verify content is preserved
	assert.Equal(t, 10, countImages(chunks[0]))
	// 10 images + 3 text segments + 1 at = 14 elements, but images are separate
	assert.Equal(t, 13, len(chunks[0])) // at + 3 text + 10 images = 13 elements
}

// TestBuildMessageChunks_Video tests that video is sent as a separate message.
func TestBuildMessageChunks_Video(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "hello"})
	msg.Append(&VideoSegment{
		Name: "video.mp4",
		Url:  "/path/to/video.mp4",
	})
	msg.Append(&TextSegment{Content: "world"})

	chunks := m.buildMessageChunks(msg)

	// Video should be a separate chunk
	// Expected: [text hello], [video], [text world]
	assert.GreaterOrEqual(t, len(chunks), 2, "video should cause splitting")

	// Find the video chunk
	var videoChunk []MessageSegment
	for _, chunk := range chunks {
		for _, seg := range chunk {
			if seg.Type == "video" {
				videoChunk = chunk
				break
			}
		}
	}
	assert.NotNil(t, videoChunk, "video should be in its own chunk")
	assert.Equal(t, 1, len(videoChunk), "video chunk should contain only video")
}

// TestBuildMessageChunks_File tests that file is sent as a separate message.
func TestBuildMessageChunks_File(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "before"})
	msg.Append(&FileSegment{
		Path: "document.pdf",
		Url:  "https://example.com/document.pdf",
	})
	msg.Append(&TextSegment{Content: "after"})

	chunks := m.buildMessageChunks(msg)

	// File should be a separate chunk
	assert.GreaterOrEqual(t, len(chunks), 2, "file should cause splitting")

	// Find the file chunk
	var fileChunk []MessageSegment
	for _, chunk := range chunks {
		for _, seg := range chunk {
			if seg.Type == "file" {
				fileChunk = chunk
				break
			}
		}
	}
	assert.NotNil(t, fileChunk, "file should be in its own chunk")
	assert.Equal(t, 1, len(fileChunk), "file chunk should contain only file")
}

// TestBuildMessageChunks_Forward tests that forward is sent as a separate message.
func TestBuildMessageChunks_Forward(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "check this"})
	msg.Append(&ForwardSegment{ResId: "forward123"})
	msg.Append(&TextSegment{Content: "done"})

	chunks := m.buildMessageChunks(msg)

	// Forward should be a separate chunk
	assert.GreaterOrEqual(t, len(chunks), 2, "forward should cause splitting")

	// Find the forward chunk
	var forwardChunk []MessageSegment
	for _, chunk := range chunks {
		for _, seg := range chunk {
			if seg.Type == "forward" {
				forwardChunk = chunk
				break
			}
		}
	}
	assert.NotNil(t, forwardChunk, "forward should be in its own chunk")
	assert.Equal(t, 1, len(forwardChunk), "forward chunk should contain only forward")
}

// TestBuildMessageChunks_Voice tests that voice is sent as a separate message.
func TestBuildMessageChunks_Voice(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	msg := &SendingMessage{}
	msg.Append(&TextSegment{Content: "listen"})
	msg.Append(&VoiceSegment{
		Name: "audio.amr",
		Url:  "https://example.com/audio.amr",
	})
	msg.Append(&TextSegment{Content: "end"})

	chunks := m.buildMessageChunks(msg)

	// Voice should be a separate chunk
	assert.GreaterOrEqual(t, len(chunks), 2, "voice should cause splitting")

	// Find the voice chunk
	var voiceChunk []MessageSegment
	for _, chunk := range chunks {
		for _, seg := range chunk {
			if seg.Type == "record" {
				voiceChunk = chunk
				break
			}
		}
	}
	assert.NotNil(t, voiceChunk, "voice should be in its own chunk")
	assert.Equal(t, 1, len(voiceChunk), "voice chunk should contain only voice")
}

// TestBuildMessageChunks_ReplyAlone tests that reply can combine with other mix elements.
func TestBuildMessageChunks_ReplyAlone(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	msg := &SendingMessage{}
	msg.Append(&ReplySegment{ReplySeq: 12345})
	msg.Append(&TextSegment{Content: "this is a reply"})

	chunks := m.buildMessageChunks(msg)

	// Reply + text should be in one chunk
	assert.Equal(t, 1, len(chunks), "reply with text should be in one chunk")
	assert.Equal(t, 2, len(chunks[0]), "chunk should contain reply and text")
}

// TestBuildMessageChunks_AtWithText tests at element combines with text.
func TestBuildMessageChunks_AtWithText(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	msg := &SendingMessage{}
	msg.Append(&AtSegment{Target: 1001})
	msg.Append(&TextSegment{Content: "hello world"})

	chunks := m.buildMessageChunks(msg)

	// At + text should be in one chunk
	assert.Equal(t, 1, len(chunks), "at with text should be in one chunk")
	assert.Equal(t, 2, len(chunks[0]))
}

// TestBuildMessageChunks_ComplexMixed tests complex message with all element types.
func TestBuildMessageChunks_ComplexMixed(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	msg := &SendingMessage{}
	// Add some mix elements - text before video
	msg.Append(&TextSegment{Content: "start "})
	msg.Append(&AtSegment{Target: 1001})
	msg.Append(&TextSegment{Content: " check this "})
	msg.Append(&ReplySegment{ReplySeq: 123})
	msg.Append(&TextSegment{Content: " here's a pic "})
	// Add 5 images
	for i := 0; i < 5; i++ {
		msg.Append(&ImageSegment{
			File: fmt.Sprintf("pic_%d.jpg", i),
			Url:  fmt.Sprintf("https://example.com/pic_%d.jpg", i),
		})
	}
	// Video should be at the end as a single element
	msg.Append(&VideoSegment{
		Name: "video.mp4",
		Url:  "/path/to/video.mp4",
	})

	chunks := m.buildMessageChunks(msg)

	// Should have multiple chunks - the video should force a split
	// Check if any chunk contains video
	foundVideo := false
	for _, chunk := range chunks {
		for _, seg := range chunk {
			if seg.Type == "video" {
				foundVideo = true
				break
			}
		}
	}
	assert.True(t, foundVideo, "should have a chunk containing video, got %d chunks", len(chunks))
}

// TestParseChunkToElements tests that chunks are correctly converted back to message elements.
func TestParseChunkToElements(t *testing.T) {
	m, _, _ := setupTestMessenger(t)

	// Create original message
	origMsg := &SendingMessage{}
	origMsg.Append(&TextSegment{Content: "hello"})
	origMsg.Append(&AtSegment{Target: 1001})
	origMsg.Append(&TextSegment{Content: " world"})

	// Build chunks
	chunks := m.buildMessageChunks(origMsg)
	assert.Equal(t, 1, len(chunks))

	// Convert back to elements
	elements := parseChunkToElements(chunks[0])
	assert.Equal(t, 3, len(elements))

	// Verify elements
	_, ok := elements[0].(*TextSegment)
	assert.True(t, ok)
	_, ok = elements[1].(*AtSegment)
	assert.True(t, ok)
	_, ok = elements[2].(*TextSegment)
	assert.True(t, ok)
}

// TestCalculateTextLength tests text length calculation.
func TestCalculateTextLength(t *testing.T) {
	tests := []struct {
		name     string
		segments []MessageSegment
		expected int
	}{
		{
			name:     "empty",
			segments: []MessageSegment{},
			expected: 0,
		},
		{
			name: "text only",
			segments: []MessageSegment{
				{Type: "text", Data: map[string]interface{}{"text": "hello"}},
			},
			expected: 5,
		},
		{
			name: "text and at",
			segments: []MessageSegment{
				{Type: "text", Data: map[string]interface{}{"text": "hello"}},
				{Type: "at", Data: map[string]interface{}{"qq": "1001"}},
			},
			expected: 15, // 5 + 10 (at estimate)
		},
		{
			name: "with reply",
			segments: []MessageSegment{
				{Type: "reply", Data: map[string]interface{}{"id": "123"}},
				{Type: "text", Data: map[string]interface{}{"text": "reply text"}},
			},
			expected: 20, // 10 + 10
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateTextLength(tt.segments)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCountImages tests image counting.
func TestCountImages(t *testing.T) {
	tests := []struct {
		name     string
		segments []MessageSegment
		expected int
	}{
		{
			name:     "empty",
			segments: []MessageSegment{},
			expected: 0,
		},
		{
			name: "no images",
			segments: []MessageSegment{
				{Type: "text", Data: map[string]interface{}{"text": "hello"}},
				{Type: "at", Data: map[string]interface{}{"qq": "1001"}},
			},
			expected: 0,
		},
		{
			name: "with images",
			segments: []MessageSegment{
				{Type: "text", Data: map[string]interface{}{"text": "check"}},
				{Type: "image", Data: map[string]interface{}{"file": "pic1.jpg"}},
				{Type: "image", Data: map[string]interface{}{"file": "pic2.jpg"}},
				{Type: "image", Data: map[string]interface{}{"file": "pic3.jpg"}},
			},
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := countImages(tt.segments)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsSingleElement tests single element detection.
func TestIsSingleElement(t *testing.T) {
	tests := []struct {
		segmentType string
		expected    bool
	}{
		{"text", false},
		{"at", false},
		{"face", false},
		{"image", false},
		{"reply", false},
		{"video", true},
		{"file", true},
		{"record", true},
		{"forward", true},
		{"json", false},
	}

	for _, tt := range tests {
		t.Run(tt.segmentType, func(t *testing.T) {
			seg := MessageSegment{Type: tt.segmentType}
			result := isSingleElement(seg)
			assert.Equal(t, tt.expected, result)
		})
	}
}
