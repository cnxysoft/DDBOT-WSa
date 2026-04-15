package adapter

import (
	"sync"
	"testing"
	"time"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/stretchr/testify/assert"
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

func (m *mockAdapter) Start() error                       { return nil }
func (m *mockAdapter) Stop() error                       { return nil }
func (m *mockAdapter) GetSelfID() int64                   { return 1143469507 }
func (m *mockAdapter) GetAdapterName() string             { return "mock" }
func (m *mockAdapter) IsConnected() bool                 { return true }
func (m *mockAdapter) GetFileUrl(groupCode int64, fileId string) string { return "" }
func (m *mockAdapter) DownloadFile(url, base64, name string, headers []string) (string, error) {
	return "", nil
}
func (m *mockAdapter) GetMsg(msgId int32) (*GetMsgResult, error) { return nil, nil }
func (m *mockAdapter) GetMsgOrg(msgId int32) (interface{}, error) { return nil, nil }
func (m *mockAdapter) RecallMsg(msgId int32) error { return nil }
func (m *mockAdapter) GroupPoke(groupCode, target int64) error { return nil }
func (m *mockAdapter) FriendPoke(target int64) error { return nil }
func (m *mockAdapter) SetGroupBan(groupCode, memberUin int64, duration int64) error { return nil }
func (m *mockAdapter) SetGroupWholeBan(groupCode int64, enable bool) error { return nil }
func (m *mockAdapter) KickGroupMember(groupCode, memberUin int64, rejectAddRequest bool) error { return nil }
func (m *mockAdapter) SetGroupLeave(groupCode int64, isDismiss bool) error { return nil }
func (m *mockAdapter) SetGroupAdmin(groupCode, memberUin int64, enable bool) error { return nil }
func (m *mockAdapter) EditGroupCard(groupCode, memberUin int64, card string) error { return nil }
func (m *mockAdapter) EditGroupTitle(groupCode, memberUin int64, title string) error { return nil }

func (m *mockAdapter) SendApi(action string, params map[string]interface{}) (interface{}, error) {
	return nil, nil
}
func (m *mockAdapter) SendGroupMessage(groupID int64, message interface{}) (int32, error) {
	return 1, nil
}
func (m *mockAdapter) SendPrivateMessage(userID int64, message interface{}) (int32, error) {
	return 1, nil
}
func (m *mockAdapter) SendGroupForwardMessage(groupID int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error) {
	return 1, "", nil
}
func (m *mockAdapter) SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error) {
	return 1, "", nil
}
func (m *mockAdapter) GetGroupList() ([]*GroupInfo, error)       { return m.groupList, nil }
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
func (m *mockAdapter) OnGroupMessage(handler func(*GroupMessageEvent))    {}
func (m *mockAdapter) OnPrivateMessage(handler func(*PrivateMessageEvent)) {}
func (m *mockAdapter) OnMetaEvent(handler func(*MetaEvent))               {}
func (m *mockAdapter) OnNoticeEvent(handler func(*NoticeEvent))           {}
func (m *mockAdapter) OnRequestEvent(handler func(*RequestEvent))         {}

// mockDispatcher implements BotEventDispatcher for testing.
type mockDispatcher struct {
	mu     sync.Mutex
	events []string
}

func newMockDispatcher() *mockDispatcher {
	return &mockDispatcher{}
}

func (d *mockDispatcher) DispatchGroupMessage(msg *message.GroupMessage)                                {}
func (d *mockDispatcher) DispatchPrivateMessage(msg *message.PrivateMessage)                              {}
func (d *mockDispatcher) DispatchGroupRecall(event *client.GroupMessageRecalledEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_recall")
}
func (d *mockDispatcher) DispatchFriendRecall(event *client.FriendMessageRecalledEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "friend_recall")
}
func (d *mockDispatcher) DispatchGroupMute(event *client.GroupMuteEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_ban")
}
func (d *mockDispatcher) DispatchDisconnected(event *client.ClientDisconnectedEvent) {}
func (d *mockDispatcher) DispatchGroupMemberJoin(event *client.MemberJoinGroupEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_increase")
}
func (d *mockDispatcher) DispatchGroupMemberLeave(event *client.MemberLeaveGroupEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_decrease")
}
func (d *mockDispatcher) DispatchGroupJoin(event *client.GroupInfo)                          {}
func (d *mockDispatcher) DispatchGroupLeave(event *client.GroupLeaveEvent)                   {}
func (d *mockDispatcher) DispatchGroupMemberPermissionChanged(event *client.MemberPermissionChangedEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_admin")
}
func (d *mockDispatcher) DispatchMemberCardUpdated(event *client.MemberCardUpdatedEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_card")
}
func (d *mockDispatcher) DispatchMemberSpecialTitleUpdated(event *client.MemberSpecialTitleUpdatedEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "notify_title")
}
func (d *mockDispatcher) DispatchGroupUploadNotify(event *client.GroupUploadNotifyEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_upload")
}
func (d *mockDispatcher) DispatchGroupNotify(event client.INotifyEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "notify_poke")
}
func (d *mockDispatcher) DispatchFriendNotify(event client.INotifyEvent) {}
func (d *mockDispatcher) DispatchGroupNameUpdated(event *client.GroupNameUpdatedEvent) {}
func (d *mockDispatcher) DispatchGroupEssenceChanged(event *client.GroupDigestEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "essence")
}
func (d *mockDispatcher) DispatchGroupDisband(event *client.GroupDisbandEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_dismiss")
}
func (d *mockDispatcher) DispatchNewFriendRequest(event *client.NewFriendRequest)  {}
func (d *mockDispatcher) DispatchNewFriend(event *client.NewFriendEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "friend_add")
}
func (d *mockDispatcher) DispatchUserJoinGroupRequest(event *client.UserJoinGroupRequest) {}
func (d *mockDispatcher) DispatchGroupInvitedRequest(event *client.GroupInvitedRequest)  {}
func (d *mockDispatcher) DispatchBotOnline(event *client.BotOnlineEvent)                 {}
func (d *mockDispatcher) DispatchBotOffline(event *client.BotOfflineEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "bot_offline")
}
func (d *mockDispatcher) DispatchGroupMsgEmojiLike(event *client.GroupMsgEmojiLikeEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "group_msg_emoji_like")
}
func (d *mockDispatcher) DispatchProfileLike(event *client.ProfileLikeEvent) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, "profile_like")
}
func (d *mockDispatcher) DispatchPokeRecall(event *client.PokeRecallEvent) {
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
			File:       client.GroupFile{},
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
			NoticeType:  "group_msg_emoji_like",
			SubType:     "add",
			GroupID:     545402644,
			UserID:      1001,
			MessageID:   12345,
			EmojiId:     "笑脸",
			EmojiCount:  1,
			Time:        time.Now().Unix(),
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
		t.Fatal("UpdateGroupMember and RemoveGroupMember deadlocked")
	}
}
