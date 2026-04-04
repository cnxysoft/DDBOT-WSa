package adapter

import (
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/utils/qqlog"
	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

type BotEventDispatcher interface {
	// Existing methods
	DispatchGroupMessage(msg *message.GroupMessage)
	DispatchPrivateMessage(msg *message.PrivateMessage)
	DispatchGroupRecall(event *client.GroupMessageRecalledEvent)
	DispatchFriendRecall(event *client.FriendMessageRecalledEvent)
	DispatchGroupMute(event *client.GroupMuteEvent)
	DispatchDisconnected(event *client.ClientDisconnectedEvent)

	// New methods - Notice events
	DispatchGroupMemberJoin(event *client.MemberJoinGroupEvent)
	DispatchGroupMemberLeave(event *client.MemberLeaveGroupEvent)
	DispatchGroupJoin(event *client.GroupInfo)
	DispatchGroupLeave(event *client.GroupLeaveEvent)
	DispatchGroupMemberPermissionChanged(event *client.MemberPermissionChangedEvent)
	DispatchMemberCardUpdated(event *client.MemberCardUpdatedEvent)
	DispatchMemberSpecialTitleUpdated(event *client.MemberSpecialTitleUpdatedEvent)
	DispatchGroupUploadNotify(event *client.GroupUploadNotifyEvent)
	DispatchGroupNotify(event client.INotifyEvent)
	DispatchFriendNotify(event client.INotifyEvent)
	DispatchGroupNameUpdated(event *client.GroupNameUpdatedEvent)
	DispatchGroupEssenceChanged(event *client.GroupDigestEvent)
	DispatchGroupDisband(event *client.GroupDisbandEvent)

	// New methods - Request events
	DispatchNewFriendRequest(event *client.NewFriendRequest)
	DispatchNewFriend(event *client.NewFriendEvent)
	DispatchUserJoinGroupRequest(event *client.UserJoinGroupRequest)
	DispatchGroupInvitedRequest(event *client.GroupInvitedRequest)

	// New methods - Bot events
	DispatchBotOnline(event *client.BotOnlineEvent)
	DispatchBotOffline(event *client.BotOfflineEvent)
	DispatchGroupMsgEmojiLike(event *client.GroupMsgEmojiLikeEvent)
	DispatchProfileLike(event *client.ProfileLikeEvent)
	DispatchPokeRecall(event *client.PokeRecallEvent)
}

var messengerLogger = logrus.WithField("module", "messenger")

type SendResp struct {
	RetMSG *message.GroupMessage
	Error  error
}

type Messenger struct {
	Adapter Adapter

	Uin    int64
	Online atomic.Bool

	GroupList  []*GroupInfo
	FriendList []*FriendInfo
	groupMu    sync.RWMutex
	friendMu   sync.RWMutex

	stopChan chan struct{}
	wg       sync.WaitGroup

	eventDispatcher BotEventDispatcher

	// 消息统计
	groupMsgCount    atomic.Int64
	privateMsgCount  atomic.Int64
	groupSendCount   atomic.Int64
	privateSendCount atomic.Int64
}

func NewMessenger(adapter Adapter) *Messenger {
	m := &Messenger{
		Adapter:    adapter,
		stopChan:   make(chan struct{}),
		GroupList:  make([]*GroupInfo, 0),
		FriendList: make([]*FriendInfo, 0),
	}

	m.registerEventHandlers()

	// 启动统计汇总定时器
	go m.summaryTicker()

	return m
}

// summaryTicker 每分钟输出一次消息统计汇总
func (m *Messenger) summaryTicker() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !qqlog.Enabled {
				// qq-logs 未启用，输出统计到主日志
				messengerLogger.Infof("消息统计: 收群消息 %d, 收私聊 %d, 发群消息 %d, 发私聊 %d",
					m.groupMsgCount.Load(), m.privateMsgCount.Load(),
					m.groupSendCount.Load(), m.privateSendCount.Load())
			}
		case <-m.stopChan:
			return
		}
	}
}

func (m *Messenger) SetBotEventDispatcher(dispatcher BotEventDispatcher) {
	m.eventDispatcher = dispatcher
}

func (m *Messenger) registerEventHandlers() {
	m.Adapter.OnGroupMessage(func(event *GroupMessageEvent) {
		m.handleGroupMessage(event)
	})

	m.Adapter.OnPrivateMessage(func(event *PrivateMessageEvent) {
		m.handlePrivateMessage(event)
	})

	m.Adapter.OnMetaEvent(func(event *MetaEvent) {
		if event.MetaEventType == "lifecycle" {
			m.Uin = event.SelfID
			m.Online.Store(true)
			messengerLogger.Infof("Bot online: %d", m.Uin)
			// Lifecycle事件触发时立即刷新好友、群组、群员信息
			go func() {
				if err := m.RefreshList(); err != nil {
					messengerLogger.WithError(err).Error("refresh list failed")
				}
			}()
		} else if event.MetaEventType == "heartbeat" {
			if status, ok := event.Status["online"].(bool); ok {
				wasOnline := m.Online.Load()
				m.Online.Store(status)
				if !wasOnline && status {
					messengerLogger.Info("Bot online")
				} else if wasOnline && !status {
					messengerLogger.Warn("Bot offline")
				}
			}
		}
	})

	m.Adapter.OnNoticeEvent(func(event *NoticeEvent) {
		m.handleNoticeEvent(event)
	})

	m.Adapter.OnRequestEvent(func(event *RequestEvent) {
		m.handleRequestEvent(event)
	})
}

func (m *Messenger) Start() error {
	return m.Adapter.Start()
}

func (m *Messenger) Stop() error {
	close(m.stopChan)
	m.wg.Wait()
	return m.Adapter.Stop()
}

func (m *Messenger) GetUin() int64 {
	return m.Uin
}

func (m *Messenger) GetSelfID() int64 {
	return m.Adapter.GetSelfID()
}

func (m *Messenger) SendGroupMessage(groupCode int64, msg *message.SendingMessage, newstr string) SendResp {
	messages := m.buildMessageSegments(msg)

	// 获取群名称
	groupName := "未知群聊"
	if group := m.FindGroup(groupCode); group != nil {
		groupName = group.Name
	}

	// 记录发送日志
	if qqlog.Logger != nil {
		qqlog.Logger.Infof("发送 群消息 给 %s(%d): %s", groupName, groupCode, newstr)
	}

	msgID, err := m.Adapter.SendGroupMessage(groupCode, messages)
	m.groupSendCount.Add(1)
	if err != nil {
		messengerLogger.Errorf("Send group message failed: %v", err)
		return SendResp{
			RetMSG: &message.GroupMessage{Id: -1},
			Error:  err,
		}
	}

	return SendResp{
		RetMSG: &message.GroupMessage{
			Id:        msgID,
			GroupCode: groupCode,
			Sender: &message.Sender{
				Uin: m.Uin,
			},
			Elements: msg.Elements,
		},
		Error: nil,
	}
}

func (m *Messenger) SendPrivateMessage(target int64, msg *message.SendingMessage, newstr string) *message.PrivateMessage {
	messages := m.buildMessageSegments(msg)

	// 获取好友昵称
	nickname := "未知用户"
	if friend := m.FindFriend(target); friend != nil {
		nickname = friend.Nickname
	}

	// 记录发送日志
	if qqlog.Logger != nil {
		qqlog.Logger.Infof("发送 私聊消息 给 %s(%d): %s", nickname, target, newstr)
	}

	msgID, err := m.Adapter.SendPrivateMessage(target, messages)
	m.privateSendCount.Add(1)
	if err != nil {
		messengerLogger.Errorf("Send private message failed: %v", err)
		return &message.PrivateMessage{Id: -1}
	}

	return &message.PrivateMessage{
		Id:     msgID,
		Target: target,
		Self:   m.Uin,
		Sender: &message.Sender{
			Uin: m.Uin,
		},
		Elements: msg.Elements,
	}
}

func (m *Messenger) buildMessageSegments(msg *message.SendingMessage) []MessageSegment {
	var segments []MessageSegment

	for _, elem := range msg.Elements {
		switch e := elem.(type) {
		case *message.TextElement:
			segments = append(segments, MessageSegment{
				Type: "text",
				Data: map[string]interface{}{"text": e.Content},
			})
		case *message.AtElement:
			qq := "all"
			if e.Target != 0 {
				qq = fmt.Sprintf("%d", e.Target)
			}
			segments = append(segments, MessageSegment{
				Type: "at",
				Data: map[string]interface{}{"qq": qq},
			})
		case *message.FaceElement:
			segments = append(segments, MessageSegment{
				Type: "face",
				Data: map[string]interface{}{"id": e.Index},
			})
		case *message.GroupImageElement:
			segments = append(segments, MessageSegment{
				Type: "image",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.Url,
				},
			})
		case *message.FriendImageElement:
			segments = append(segments, MessageSegment{
				Type: "image",
				Data: map[string]interface{}{
					"file": e.Url,
				},
			})
		case *message.VoiceElement:
			segments = append(segments, MessageSegment{
				Type: "record",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.Url,
				},
			})
		case *message.ReplyElement:
			segments = append(segments, MessageSegment{
				Type: "reply",
				Data: map[string]interface{}{"id": e.ReplySeq},
			})
		case *message.ForwardElement:
			segments = append(segments, MessageSegment{
				Type: "forward",
				Data: map[string]interface{}{"id": e.ResId},
			})
		case *message.LightAppElement:
			segments = append(segments, MessageSegment{
				Type: "json",
				Data: map[string]interface{}{"data": e.Content},
			})
		case *message.GroupFileElement:
			segments = append(segments, MessageSegment{
				Type: "file",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.Url,
				},
			})
		case *message.FriendFileElement:
			segments = append(segments, MessageSegment{
				Type: "file",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.Url,
				},
			})
		case *message.ImageElement:
			segments = append(segments, MessageSegment{
				Type: "image",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.File,
				},
			})
		case *message.VideoElement:
			segments = append(segments, MessageSegment{
				Type: "video",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.File,
				},
			})
		case *message.ShortVideoElement:
			segments = append(segments, MessageSegment{
				Type: "video",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.Url,
				},
			})
		case *message.RecordElement:
			segments = append(segments, MessageSegment{
				Type: "record",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.File,
				},
			})
		case *message.FileElement:
			segments = append(segments, MessageSegment{
				Type: "file",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.File,
				},
			})
		}
	}

	return segments
}

func (m *Messenger) FindGroup(code int64) *GroupInfo {
	m.groupMu.RLock()
	defer m.groupMu.RUnlock()

	for _, g := range m.GroupList {
		if g.Code == code {
			return g
		}
	}
	return nil
}

func (m *Messenger) FindGroupByUin(uin int64) *GroupInfo {
	m.groupMu.RLock()
	defer m.groupMu.RUnlock()

	for _, g := range m.GroupList {
		if g.Uin == uin {
			return g
		}
	}
	return nil
}

func (m *Messenger) FindFriend(uin int64) *FriendInfo {
	if uin == m.Uin {
		return &FriendInfo{
			Uin:      uin,
			Nickname: "Bot",
		}
	}

	m.friendMu.RLock()
	defer m.friendMu.RUnlock()

	for _, f := range m.FriendList {
		if f.Uin == uin {
			return f
		}
	}
	return nil
}

func (m *Messenger) ReloadGroupList() error {
	groups, err := m.Adapter.GetGroupList()
	if err != nil {
		return err
	}

	m.groupMu.Lock()
	defer m.groupMu.Unlock()

	m.GroupList = make([]*GroupInfo, 0, len(groups))
	for _, g := range groups {
		m.GroupList = append(m.GroupList, &GroupInfo{
			Uin:             g.GroupID,
			Code:            g.GroupID,
			Name:            g.GroupName,
			MemberCount:     g.MemberCount,
			MaxMemberCount:  g.MaxMemberCount,
			GroupCreateTime: g.GroupCreateTime,
			GroupLevel:      g.GroupLevel,
			Members:         make([]*GroupMemberInfo, 0),
			Client:          m,
		})
	}

	messengerLogger.Infof("Reloaded %d groups", len(m.GroupList))
	return nil
}

func (m *Messenger) ReloadFriendList() error {
	friends, err := m.Adapter.GetFriendList()
	if err != nil {
		return err
	}

	m.friendMu.Lock()
	defer m.friendMu.Unlock()

	m.FriendList = make([]*FriendInfo, 0, len(friends))
	for _, f := range friends {
		m.FriendList = append(m.FriendList, &FriendInfo{
			Uin:      f.UserID,
			Nickname: f.Nickname,
			Remark:   f.Remark,
			Client:   m,
		})
	}

	messengerLogger.Infof("Reloaded %d friends", len(m.FriendList))
	return nil
}

func (m *Messenger) GetGroupMembers(group *GroupInfo) ([]*GroupMemberInfo, error) {
	return m.GetGroupMembersByID(group.Code)
}

func (m *Messenger) GetGroupMembersByID(groupID int64) ([]*GroupMemberInfo, error) {
	members, err := m.Adapter.GetGroupMemberList(groupID)
	if err != nil {
		return nil, err
	}

	result := make([]*GroupMemberInfo, 0, len(members))
	for _, mb := range members {
		perm := Member
		switch mb.Role {
		case "owner":
			perm = Owner
		case "admin":
			perm = Administrator
		}

		result = append(result, &GroupMemberInfo{
			Group:           m.FindGroupByUin(mb.GroupID),
			Uin:             mb.UserID,
			Nickname:        mb.Nickname,
			CardName:        mb.Card,
			JoinTime:        mb.JoinTime,
			LastSpeakTime:   mb.LastSentTime,
			SpecialTitle:    mb.Title,
			ShutUpTimestamp: mb.ShutUpTimestamp,
			Permission:      perm,
		})
	}

	group := m.FindGroupByUin(groupID)
	if group != nil {
		m.groupMu.Lock()
		group.Members = result
		m.groupMu.Unlock()
	}

	return result, nil
}

func (m *Messenger) GetStrangerInfo(uin int64) (map[string]interface{}, error) {
	return m.Adapter.GetStrangerInfo(uin)
}

// AddGroupMember adds a member to the group cache after receiving a group_increase event
func (m *Messenger) AddGroupMember(groupID, userID int64) error {
	members, err := m.Adapter.GetGroupMemberList(groupID)
	if err != nil {
		return err
	}
	for _, mb := range members {
		if mb.UserID == userID {
			perm := Member
			switch mb.Role {
			case "owner":
				perm = Owner
			case "admin":
				perm = Administrator
			}
			// Look up group BEFORE acquiring lock to avoid deadlock
			group := m.FindGroupByUin(groupID)
			if group == nil {
				return fmt.Errorf("group %d not found", groupID)
			}
			newMember := &GroupMemberInfo{
				Group:           group,
				Uin:             mb.UserID,
				Nickname:        mb.Nickname,
				CardName:        mb.Card,
				JoinTime:        mb.JoinTime,
				LastSpeakTime:   mb.LastSentTime,
				SpecialTitle:    mb.Title,
				ShutUpTimestamp: mb.ShutUpTimestamp,
				Permission:      perm,
			}
			m.groupMu.Lock()
			defer m.groupMu.Unlock()
			// Check if member already exists
			for _, existing := range group.Members {
				if existing.Uin == userID {
					*existing = *newMember
					return nil
				}
			}
			group.Members = append(group.Members, newMember)
			messengerLogger.Debugf("AddGroupMember cache updated: group=%d member=%d", groupID, userID)
			return nil
		}
	}
	return fmt.Errorf("member %d not found in group %d", userID, groupID)
}

// RemoveGroupMember removes a member from the group cache after receiving a group_decrease event
func (m *Messenger) RemoveGroupMember(groupID, userID int64) {
	// 先查找 group（不持有锁），避免在持有 groupMu.Lock() 的情况下调用需要 RLock 的 FindGroupByUin
	group := m.FindGroupByUin(groupID)
	if group == nil {
		return
	}
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	for i, member := range group.Members {
		if member.Uin == userID {
			group.Members = append(group.Members[:i], group.Members[i+1:]...)
			messengerLogger.Debugf("RemoveGroupMember cache updated: group=%d member=%d", groupID, userID)
			return
		}
	}
}

// UpdateGroupMember updates a member's info in the cache
func (m *Messenger) UpdateGroupMember(groupID, userID int64, updateFunc func(*GroupMemberInfo)) {
	group := m.FindGroupByUin(groupID)
	if group == nil {
		return
	}
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	for _, member := range group.Members {
		if member.Uin == userID {
			updateFunc(member)
			messengerLogger.Debugf("UpdateGroupMember cache updated: group=%d member=%d", groupID, userID)
			return
		}
	}
}

// RefreshMemberInfo fetches fresh member info from API and updates cache
func (m *Messenger) RefreshMemberInfo(groupID, userID int64) error {
	members, err := m.Adapter.GetGroupMemberList(groupID)
	if err != nil {
		return err
	}
	for _, mb := range members {
		if mb.UserID == userID {
			perm := Member
			switch mb.Role {
			case "owner":
				perm = Owner
			case "admin":
				perm = Administrator
			}
			group := m.FindGroupByUin(groupID)
			if group == nil {
				return fmt.Errorf("group %d not found", groupID)
			}
			m.groupMu.Lock()
			defer m.groupMu.Unlock()
			for _, member := range group.Members {
				if member.Uin == userID {
					member.Nickname = mb.Nickname
					member.CardName = mb.Card
					member.SpecialTitle = mb.Title
					member.Permission = perm
					messengerLogger.Debugf("RefreshMemberInfo cache updated: group=%d member=%d", groupID, userID)
					return nil
				}
			}
			return fmt.Errorf("member %d not found in group %d", userID, groupID)
		}
	}
	return fmt.Errorf("member %d not found in group %d response", userID, groupID)
}

func (m *Messenger) GetGroupInfo(groupCode int64) (*GroupInfo, error) {
	info, err := m.Adapter.GetGroupInfo(groupCode)
	if err != nil {
		return nil, err
	}

	return &GroupInfo{
		Uin:             info.GroupID,
		Code:            info.GroupID,
		Name:            info.GroupName,
		MemberCount:     info.MemberCount,
		MaxMemberCount:  info.MaxMemberCount,
		GroupCreateTime: info.GroupCreateTime,
		GroupLevel:      info.GroupLevel,
		Client:          m,
	}, nil
}

func (m *Messenger) RefreshList() error {
	if err := m.ReloadFriendList(); err != nil {
		messengerLogger.WithError(err).Error("unable to load friends list")
	}
	messengerLogger.Infof("已加载 %d 个好友", len(m.FriendList))

	if err := m.ReloadGroupList(); err != nil {
		messengerLogger.WithError(err).Error("unable to load groups list")
	}
	messengerLogger.Infof("已加载 %d 个群组", len(m.GroupList))

	for _, group := range m.GroupList {
		members, err := m.GetGroupMembersByID(group.Code)
		if err != nil {
			messengerLogger.WithError(err).Errorf("unable to load group members for %d", group.Code)
			continue
		}
		messengerLogger.Debugf("群[%d]加载成员[%d]个", group.Code, len(members))
	}

	messengerLogger.Infof("已加载 %d 个群成员", len(m.GroupList))

	return nil
}

func (m *Messenger) handleNoticeEvent(event *NoticeEvent) {
	if m.eventDispatcher == nil {
		return
	}

	switch event.NoticeType {
	case "group_ban":
		m.eventDispatcher.DispatchGroupMute(&client.GroupMuteEvent{
			GroupCode:   event.GroupID,
			OperatorUin: event.OperatorID,
			TargetUin:   event.UserID,
			Time:        event.Duration,
		})
	case "group_increase":
		if err := m.AddGroupMember(event.GroupID, event.UserID); err != nil {
			messengerLogger.WithError(err).Warnf("AddGroupMember failed for %d/%d", event.GroupID, event.UserID)
		}
		m.eventDispatcher.DispatchGroupMemberJoin(&client.MemberJoinGroupEvent{
			Group: &client.GroupInfo{
				Uin:  event.GroupID,
				Code: event.GroupID,
			},
			Member: &client.GroupMemberInfo{
				Uin: event.UserID,
			},
		})
	case "group_decrease":
		m.RemoveGroupMember(event.GroupID, event.UserID)
		m.eventDispatcher.DispatchGroupMemberLeave(&client.MemberLeaveGroupEvent{
			Group: &client.GroupInfo{
				Uin:  event.GroupID,
				Code: event.GroupID,
			},
			Member: &client.GroupMemberInfo{
				Uin: event.UserID,
			},
			Operator: &client.GroupMemberInfo{
				Uin: event.OperatorID,
			},
		})
	case "group_admin":
		var newPerm MemberPermission
		if event.SubType == "set" {
			newPerm = Administrator
		} else {
			newPerm = Member
		}
		m.UpdateGroupMember(event.GroupID, event.UserID, func(member *GroupMemberInfo) {
			member.Permission = newPerm
		})
		if event.SubType == "set" {
			m.eventDispatcher.DispatchGroupMemberPermissionChanged(&client.MemberPermissionChangedEvent{
				Group: &client.GroupInfo{
					Uin:  event.GroupID,
					Code: event.GroupID,
				},
				Member: &client.GroupMemberInfo{
					Uin:        event.UserID,
					Permission: client.Administrator,
				},
				OldPermission: client.Member,
				NewPermission: client.Administrator,
			})
		} else {
			m.eventDispatcher.DispatchGroupMemberPermissionChanged(&client.MemberPermissionChangedEvent{
				Group: &client.GroupInfo{
					Uin:  event.GroupID,
					Code: event.GroupID,
				},
				Member: &client.GroupMemberInfo{
					Uin:        event.UserID,
					Permission: client.Member,
				},
				OldPermission: client.Administrator,
				NewPermission: client.Member,
			})
		}
	case "group_card":
		if err := m.RefreshMemberInfo(event.GroupID, event.UserID); err != nil {
			messengerLogger.WithError(err).Warnf("RefreshMemberInfo failed for %d/%d", event.GroupID, event.UserID)
		}
		m.eventDispatcher.DispatchMemberCardUpdated(&client.MemberCardUpdatedEvent{
			Group:   &client.GroupInfo{Uin: event.GroupID, Code: event.GroupID},
			Member:  &client.GroupMemberInfo{Uin: event.UserID},
			OldCard: "",
		})
	case "friend_add":
		nickname := "陌生人"
		if info, err := m.Adapter.GetStrangerInfo(event.UserID); err == nil {
			if name, ok := info["nickname"].(string); ok {
				nickname = name
			}
		}
		m.eventDispatcher.DispatchNewFriend(&client.NewFriendEvent{
			Friend: &client.FriendInfo{
				Uin:      event.UserID,
				Nickname: nickname,
			},
		})
	case "friend_recall":
		m.eventDispatcher.DispatchFriendRecall(&client.FriendMessageRecalledEvent{
			FriendUin: event.UserID,
			MessageId: int32(event.MessageID),
			Time:      event.Time,
		})
	case "notify":
		switch event.SubType {
		case "poke":
			m.eventDispatcher.DispatchGroupNotify(&client.GroupPokeNotifyEvent{
				GroupCode: event.GroupID,
				Sender:    event.UserID,
				Receiver:  event.OperatorID,
			})
		case "title":
			m.eventDispatcher.DispatchMemberSpecialTitleUpdated(&client.MemberSpecialTitleUpdatedEvent{
				GroupCode: event.GroupID,
				Uin:       event.UserID,
				NewTitle:  event.Title,
			})
		case "profile_like":
			m.eventDispatcher.DispatchProfileLike(&client.ProfileLikeEvent{
				OperatorId:   event.OperatorID,
				OperatorNick: event.OperatorNick,
				Times:        event.Times,
			})
		case "poke_recall":
			m.eventDispatcher.DispatchPokeRecall(&client.PokeRecallEvent{
				GroupCode: event.GroupID,
				Sender:    event.UserID,
				Receiver:  event.OperatorID,
			})
		}
	case "group_recall":
		m.eventDispatcher.DispatchGroupRecall(&client.GroupMessageRecalledEvent{
			GroupCode:   event.GroupID,
			OperatorUin: event.OperatorID,
			AuthorUin:   event.UserID,
		})
	case "essence":
		m.eventDispatcher.DispatchGroupEssenceChanged(&client.GroupDigestEvent{
			GroupCode: event.GroupID,
		})
	case "group_upload":
		m.eventDispatcher.DispatchGroupUploadNotify(&client.GroupUploadNotifyEvent{
			GroupCode: event.GroupID,
			Sender:    event.UserID,
			File:      event.File,
		})
	case "bot_offline":
		m.eventDispatcher.DispatchBotOffline(&client.BotOfflineEvent{})
	case "group_dismiss":
		m.eventDispatcher.DispatchGroupDisband(&client.GroupDisbandEvent{
			Group: &client.GroupInfo{
				Uin:  event.GroupID,
				Code: event.GroupID,
			},
			Operator: &client.GroupMemberInfo{
				Uin: event.UserID,
			},
		})
	case "group_msg_emoji_like":
		m.eventDispatcher.DispatchGroupMsgEmojiLike(&client.GroupMsgEmojiLikeEvent{
			GroupCode:  event.GroupID,
			UserId:     event.UserID,
			MessageId:  event.MessageID,
			EmojiId:    event.EmojiId,
			EmojiCount: event.EmojiCount,
			IsAdd:      event.SubType == "add",
		})
	}
}

func (m *Messenger) handleRequestEvent(event *RequestEvent) {
	if m.eventDispatcher == nil {
		return
	}

	switch event.RequestType {
	case "friend":
		m.eventDispatcher.DispatchNewFriendRequest(&client.NewFriendRequest{
			RequestId:     time.Now().UnixNano() / 1e6,
			Message:       event.Comment,
			RequesterUin:  event.UserID,
			RequesterNick: "陌生人",
			Flag:          event.Flag,
		})
	case "group":
		if event.SubType == "add" {
			m.eventDispatcher.DispatchUserJoinGroupRequest(&client.UserJoinGroupRequest{
				RequestId:     time.Now().UnixNano() / 1e6,
				Message:       event.Comment,
				RequesterUin:  event.UserID,
				RequesterNick: "陌生人",
				GroupCode:     event.GroupID,
				Flag:          event.Flag,
			})
		} else if event.SubType == "invite" {
			m.eventDispatcher.DispatchGroupInvitedRequest(&client.GroupInvitedRequest{
				RequestId:   time.Now().UnixNano() / 1e6,
				InvitorUin:  event.UserID,
				InvitorNick: "陌生人",
				GroupCode:   event.GroupID,
				Flag:        event.Flag,
			})
		}
	}
}

func (m *Messenger) handleGroupMessage(event *GroupMessageEvent) {
	m.groupMsgCount.Add(1)
	messengerLogger.Debugf("handleGroupMessage called: group=%d, user=%d, msgID=%d", event.GroupID, event.UserID, event.MessageID)

	msg := &message.GroupMessage{

		Id:        int32(event.MessageID),
		GroupCode: event.GroupID,
		GroupName: "",
		Sender: &message.Sender{
			Uin:      event.UserID,
			Nickname: "",
			IsFriend: false,
		},
		Time: int32(event.Time),
	}

	elements := m.parseMessageSegments(event.Message)
	msg.Elements = elements

	group := m.FindGroup(event.GroupID)
	if group != nil {
		msg.GroupName = group.Name
		member := group.FindMember(event.UserID)
		if member != nil {
			msg.Sender.Nickname = member.Nickname
			msg.Sender.CardName = member.CardName
		}
	}

	messengerLogger.Debugf("收到群 %d 内 %d 的消息", event.GroupID, event.UserID)

	if m.eventDispatcher != nil {
		messengerLogger.Debugf("Dispatching group message to bot event handlers")
		m.eventDispatcher.DispatchGroupMessage(msg)
	} else {
		messengerLogger.Warnf("eventDispatcher is nil, cannot dispatch message!")
	}
}

func (m *Messenger) handlePrivateMessage(event *PrivateMessageEvent) {
	m.privateMsgCount.Add(1)
	isFriend := m.FindFriend(event.UserID) != nil
	nickname := ""
	if !isFriend {
		if info, err := m.Adapter.GetStrangerInfo(event.UserID); err == nil {
			if name, ok := info["nickname"].(string); ok {
				nickname = name
			}
		}
	}
	msg := &message.PrivateMessage{
		Id:     int32(event.MessageID),
		Target: event.UserID,
		Self:   event.SelfID,
		Sender: &message.Sender{
			Uin:      event.UserID,
			Nickname: nickname,
			IsFriend: isFriend,
		},
		Time: int32(event.Time),
	}

	elements := m.parseMessageSegments(event.Message)
	msg.Elements = elements

	messengerLogger.Debugf("收到 %d 的私聊消息", event.UserID)

	if m.eventDispatcher != nil {
		m.eventDispatcher.DispatchPrivateMessage(msg)
	}
}

func (m *Messenger) parseMessageSegments(segments []MessageSegment) []message.IMessageElement {
	var elements []message.IMessageElement

	for _, seg := range segments {
		switch seg.Type {
		case "text":
			if text, ok := seg.Data["text"].(string); ok {
				elements = append(elements, &message.TextElement{Content: text})
			}
		case "at":
			var target int64
			if qq, ok := seg.Data["qq"].(float64); ok {
				target = int64(qq)
			} else if qq, ok := seg.Data["qq"].(string); ok {
				if qq == "all" {
					target = 0
				} else if n, err := strconv.ParseInt(qq, 10, 64); err == nil {
					target = n
				}
			}
			if target != 0 || seg.Data["qq"] == "all" {
				elements = append(elements, &message.AtElement{Target: target})
			}
		case "face":
			var faceId int64
			if id, ok := seg.Data["id"].(float64); ok {
				faceId = int64(id)
			} else if id, ok := seg.Data["id"].(string); ok {
				faceId, _ = strconv.ParseInt(id, 10, 64)
			}
			if faceId != 0 {
				elements = append(elements, &message.FaceElement{Index: int32(faceId)})
			}
		case "image":
			elements = append(elements, &message.GroupImageElement{
				Url:  getString(seg.Data["url"]),
				Name: getString(seg.Data["file"]),
			})
		case "record":
			elements = append(elements, &message.VoiceElement{
				Url:  getString(seg.Data["url"]),
				Name: getString(seg.Data["file"]),
			})
		case "reply":
			var replySeq int64
			if id, ok := seg.Data["id"].(float64); ok {
				replySeq = int64(id)
			} else if id, ok := seg.Data["id"].(string); ok {
				replySeq, _ = strconv.ParseInt(id, 10, 64)
			}
			if replySeq != 0 {
				elements = append(elements, &message.ReplyElement{ReplySeq: int32(replySeq)})
			}
		case "json":
			if data, ok := seg.Data["data"].(string); ok {
				elements = append(elements, &message.LightAppElement{Content: data})
			}
		case "forward":
			if id, ok := seg.Data["id"].(string); ok {
				elements = append(elements, &message.ForwardElement{ResId: id})
			}
		case "file":
			elements = append(elements, &message.GroupFileElement{
				Name: getString(seg.Data["name"]),
				Id:   getString(seg.Data["id"]),
				Url:  getString(seg.Data["url"]),
			})
		}
	}

	return elements
}

func (m *Messenger) SendApi(action string, params map[string]interface{}) (interface{}, error) {
	return m.Adapter.SendApi(action, params)
}

func getString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func parseTextElement(contentMap map[string]interface{}) string {
	if data, ok := contentMap["data"].(map[string]interface{}); ok {
		if text, ok := data["text"].(string); ok {
			return strings.ReplaceAll(text, "\\/", "/")
		}
	}
	return ""
}

func (m *Messenger) GroupPoke(groupCode, target int64) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.GroupPoke(groupCode, target)
}

func (m *Messenger) FriendPoke(target int64) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.FriendPoke(target)
}

func (m *Messenger) SetGroupAddRequest(flag string, subType string, approve bool, reason string) error {
	_, err := m.SendApi("set_group_add_request", map[string]interface{}{
		"flag":     flag,
		"sub_type": subType,
		"approve":  approve,
		"reason":   reason,
	})
	return err
}

func (m *Messenger) SetFriendAddRequest(flag string, approve bool) error {
	_, err := m.SendApi("set_friend_add_request", map[string]interface{}{
		"flag":    flag,
		"approve": approve,
	})
	return err
}

func (m *Messenger) SetGroupAdmin(groupCode, memberUin int64, enable bool) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.SetGroupAdmin(groupCode, memberUin, enable)
}

func (m *Messenger) EditGroupCard(groupCode, memberUin int64, card string) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.EditGroupCard(groupCode, memberUin, card)
}

func (m *Messenger) EditGroupTitle(groupCode, memberUin int64, title string) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.EditGroupTitle(groupCode, memberUin, title)
}

func (m *Messenger) SetGroupWholeBan(groupCode int64, enable bool) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.SetGroupWholeBan(groupCode, enable)
}

func (m *Messenger) SetGroupBan(groupCode, memberUin int64, duration int64) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.SetGroupBan(groupCode, memberUin, duration)
}

func (m *Messenger) SetGroupLeave(groupCode int64, isDismiss bool) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.SetGroupLeave(groupCode, isDismiss)
}

func (m *Messenger) KickGroupMember(groupCode int64, memberUin int64, rejectAddRequest bool) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.KickGroupMember(groupCode, memberUin, rejectAddRequest)
}

func (m *Messenger) GetMsg(messageID int32) (*GetMsgResult, error) {
	if m.Adapter == nil {
		return nil, fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.GetMsg(messageID)
}

func (m *Messenger) GetMsgOrg(messageID int32) (interface{}, error) {
	if m.Adapter == nil {
		return nil, fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.GetMsgOrg(messageID)
}

func (m *Messenger) RecallMsg(messageID int32) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.RecallMsg(messageID)
}

func (m *Messenger) DownloadFile(url, base64, name string, headers []string) (string, error) {
	if m.Adapter == nil {
		return "", fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.DownloadFile(url, base64, name, headers)
}

func (m *Messenger) GetFileUrl(groupCode int64, fileId string) string {
	if m.Adapter == nil {
		return ""
	}
	return m.Adapter.GetFileUrl(groupCode, fileId)
}
