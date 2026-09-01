package bot

import (
	"fmt"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	ob11 "github.com/cnxysoft/DDBOT-WSa/adapter/onebot-v11"
	"github.com/cnxysoft/DDBOT-WSa/adapter/satori"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

// reconnectStop 用于通知重连监控协程退出
var reconnectStop chan struct{}

type Bot struct {
	*adapter.Messenger

	start atomic.Bool

	// Uin 由上线 goroutine 写入、各模块并发读取，必须原子访问
	Uin      atomic.Int64
	Online   atomic.Bool
	Nickname string
	Age      uint16
	Gender   uint16

	GroupMessageRecalledEvent         *EventHandle[*adapter.GroupMessageRecalledEvent]
	GroupMessageEvent                 *EventHandle[*adapter.GroupMessage]
	GroupMuteEvent                    *EventHandle[*adapter.GroupMuteEvent]
	PrivateMessageEvent               *EventHandle[*adapter.PrivateMessage]
	FriendMessageRecalledEvent        *EventHandle[*adapter.FriendMessageRecalledEvent]
	DisconnectedEvent                 *EventHandle[*adapter.ClientDisconnectedEvent]
	SelfGroupMessageEvent             *EventHandle[*adapter.GroupMessage]
	SelfPrivateMessageEvent           *EventHandle[*adapter.PrivateMessage]
	GroupMemberJoinEvent              *EventHandle[*adapter.MemberJoinGroupEvent]
	GroupMemberLeaveEvent             *EventHandle[*adapter.MemberLeaveGroupEvent]
	GroupInvitedEvent                 *EventHandle[*adapter.GroupInvitedRequest]
	NewFriendRequestEvent             *EventHandle[*adapter.NewFriendRequest]
	NewFriendEvent                    *EventHandle[*adapter.NewFriendEvent]
	GroupJoinEvent                    *EventHandle[*adapter.GroupInfo]
	GroupLeaveEvent                   *EventHandle[*adapter.GroupLeaveEvent]
	GroupNotifyEvent                  *EventHandle[adapter.NotifyEvent]
	FriendNotifyEvent                 *EventHandle[adapter.NotifyEvent]
	MemberCardUpdatedEvent            *EventHandle[*adapter.MemberCardUpdatedEvent]
	GroupNameUpdatedEvent             *EventHandle[*adapter.GroupNameUpdatedEvent]
	MemberSpecialTitleUpdatedEvent    *EventHandle[*adapter.MemberSpecialTitleUpdatedEvent]
	GroupMemberPermissionChangedEvent *EventHandle[*adapter.MemberPermissionChangedEvent]
	GroupEssenceChangedEvent          *EventHandle[*adapter.GroupDigestEvent]
	GroupDisbandEvent                 *EventHandle[*adapter.GroupDisbandEvent]
	GroupUploadNotifyEvent            *EventHandle[*adapter.GroupUploadNotifyEvent]
	GroupNotifyNotifyEvent            *EventHandle[adapter.NotifyEvent]
	TempMessageEvent                  *EventHandle[interface{}]
	BotOnlineEvent                    *EventHandle[*adapter.BotOnlineEvent]
	BotOfflineEvent                   *EventHandle[*adapter.BotOfflineEvent]
	BotSendFailedEvent                *EventHandle[*adapter.BotSendFailedEvent]
	GroupMsgEmojiLikeEvent            *EventHandle[*adapter.GroupMsgEmojiLikeEvent]
	ProfileLikeEvent                  *EventHandle[*adapter.ProfileLikeEvent]
	PokeRecallEvent                   *EventHandle[*adapter.PokeRecallEvent]
}

func (bot *Bot) GetUin() int64 {
	if bot.Messenger != nil {
		return bot.Messenger.GetUin()
	}
	return bot.Uin.Load()
}

func (bot *Bot) FindGroup(code int64) *adapter.GroupInfo {
	if bot.Messenger != nil {
		return bot.Messenger.FindGroup(code)
	}
	return nil
}

func (bot *Bot) FindGroupByUin(uin int64) *adapter.GroupInfo {
	if bot.Messenger != nil {
		return bot.Messenger.FindGroupByUin(uin)
	}
	return nil
}

func (bot *Bot) FindFriend(uin int64) *adapter.FriendInfo {
	if bot.Messenger != nil {
		return bot.Messenger.FindFriend(uin)
	}
	return nil
}

func (bot *Bot) ReloadGroupList() error {
	if bot.Messenger != nil {
		// 不再镜像 GroupList：统一从 Messenger 读取，避免绕过 groupMu 产生数据竞争
		return bot.Messenger.ReloadGroupList()
	}
	return fmt.Errorf("messenger not initialized")
}

func (bot *Bot) ReloadFriendList() error {
	if bot.Messenger != nil {
		return bot.Messenger.ReloadFriendList()
	}
	return fmt.Errorf("messenger not initialized")
}

func (bot *Bot) GetGroupMembers(group *adapter.GroupInfo) ([]*adapter.GroupMemberInfo, error) {
	if bot.Messenger != nil {
		return bot.Messenger.GetGroupMembers(group)
	}
	return nil, fmt.Errorf("messenger not initialized")
}

func (bot *Bot) GetGroupMembersByID(groupID int64) ([]*adapter.GroupMemberInfo, error) {
	if bot.Messenger != nil {
		return bot.Messenger.GetGroupMembersByID(groupID)
	}
	return nil, fmt.Errorf("messenger not initialized")
}

func (bot *Bot) SendGroupMessage(groupCode int64, m interface{}, newstr string) adapter.SendResp {
	if bot.Messenger != nil {
		sendingMsg, ok := m.(*adapter.SendingMessage)
		if !ok {
			return adapter.SendResp{
				RetMSG: &adapter.GroupMessage{ID: -1},
				Error:  fmt.Errorf("invalid message type"),
			}
		}
		return bot.Messenger.SendGroupMessage(groupCode, sendingMsg, newstr)
	}
	return adapter.SendResp{
		RetMSG: &adapter.GroupMessage{ID: -1},
		Error:  fmt.Errorf("messenger not initialized"),
	}
}

func (bot *Bot) SendPrivateMessage(target int64, m interface{}, newstr string) adapter.PrivateSendResp {
	if bot.Messenger != nil {
		sendingMsg, ok := m.(*adapter.SendingMessage)
		if !ok {
			return adapter.PrivateSendResp{
				RetMSG: &adapter.PrivateMessage{ID: -1},
				Error:  fmt.Errorf("invalid message type"),
			}
		}
		return bot.Messenger.SendPrivateMessage(target, sendingMsg, newstr)
	}
	return adapter.PrivateSendResp{
		RetMSG: &adapter.PrivateMessage{ID: -1},
		Error:  fmt.Errorf("messenger not initialized"),
	}
}

func (bot *Bot) SendGroupForwardMessage(groupCode int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	if bot.Messenger != nil {
		return bot.Messenger.SendGroupForwardMessage(groupCode, nodes, options)
	}
	return -1, "", fmt.Errorf("messenger not initialized")
}

func (bot *Bot) SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	if bot.Messenger != nil {
		return bot.Messenger.SendPrivateForwardMessage(userID, nodes, options)
	}
	return -1, "", fmt.Errorf("messenger not initialized")
}

func (bot *Bot) GetGroupInfo(groupCode int64) (*adapter.GroupInfo, error) {
	if bot.Messenger != nil {
		return bot.Messenger.GetGroupInfo(groupCode)
	}
	return nil, fmt.Errorf("messenger not initialized")
}

func (bot *Bot) GetStrangerInfo(uin int64) (map[string]interface{}, error) {
	if bot.Messenger != nil {
		return bot.Messenger.GetStrangerInfo(uin)
	}
	return nil, fmt.Errorf("messenger not initialized")
}

func (bot *Bot) DownloadFile(url, base64, name string, headers []string) (string, error) {
	if bot.Messenger != nil {
		return bot.Messenger.DownloadFile(url, base64, name, headers)
	}
	return "", fmt.Errorf("messenger not initialized")
}

func (bot *Bot) GetFileUrl(groupCode int64, fileId string) string {
	if bot.Messenger != nil {
		return bot.Messenger.GetFileUrl(groupCode, fileId)
	}
	return ""
}

func (bot *Bot) GetMsg(msgId int32) (*adapter.GetMsgResult, error) {
	if bot.Messenger != nil {
		return bot.Messenger.GetMsg(msgId)
	}
	return nil, fmt.Errorf("messenger not initialized")
}

func (bot *Bot) GetMsgOrg(msgId int32) (interface{}, error) {
	if bot.Messenger != nil {
		return bot.Messenger.GetMsgOrg(msgId)
	}
	return nil, fmt.Errorf("messenger not initialized")
}

func (bot *Bot) RecallMsg(msgId int32) error {
	if bot.Messenger != nil {
		return bot.Messenger.RecallMsg(msgId)
	}
	return fmt.Errorf("messenger not initialized")
}

func (bot *Bot) SendApi(api string, params map[string]interface{}) (interface{}, error) {
	if bot.Messenger != nil {
		return bot.Messenger.SendApi(api, params)
	}
	return nil, fmt.Errorf("messenger not initialized")
}

func (bot *Bot) GetGroupList() []*adapter.GroupInfo {
	if bot.Messenger != nil {
		// 返回加锁快照副本，调用方可安全遍历（原实现直接共享切片，存在数据竞争）
		return bot.Messenger.GetGroupListSnapshot()
	}
	return nil
}

func (bot *Bot) GetFriendList() []*adapter.FriendInfo {
	if bot.Messenger != nil {
		return bot.Messenger.GetFriendListSnapshot()
	}
	return nil
}

// Instance Bot 实例
var Instance *Bot

var logger = logrus.WithField("bot", "internal")

func init() {
	// Set up adapter factory to avoid circular imports
	adapter.NewAdapterFactory = func(adapterType adapter.AdapterType, cfg *adapter.AdapterConfig) adapter.Adapter {
		switch adapterType {
		case adapter.AdapterTypeSatori:
			return satori.NewSatoriAdapter(cfg)
		case adapter.AdapterTypeOneBotV11:
			fallthrough
		default:
			return ob11.NewOneBotAdapter(cfg)
		}
	}
}

func Init() {
	adapterType := adapter.GetAdapterType()

	logger.Infof("Initializing bot with adapter: %s", adapterType)

	adapterCfg := adapter.GetAdapterConfig()
	adapterInstance := adapter.NewAdapter(adapterType, adapterCfg)

	if adapterInstance == nil {
		logger.Fatalf("Failed to create adapter: %s", adapterType)
	}

	messenger := adapter.NewMessenger(adapterInstance)

	Instance = &Bot{
		Messenger:                         messenger,
		GroupMessageRecalledEvent:         &EventHandle[*adapter.GroupMessageRecalledEvent]{},
		GroupMessageEvent:                 &EventHandle[*adapter.GroupMessage]{},
		GroupMuteEvent:                    &EventHandle[*adapter.GroupMuteEvent]{},
		PrivateMessageEvent:               &EventHandle[*adapter.PrivateMessage]{},
		FriendMessageRecalledEvent:        &EventHandle[*adapter.FriendMessageRecalledEvent]{},
		DisconnectedEvent:                 &EventHandle[*adapter.ClientDisconnectedEvent]{},
		SelfGroupMessageEvent:             &EventHandle[*adapter.GroupMessage]{},
		SelfPrivateMessageEvent:           &EventHandle[*adapter.PrivateMessage]{},
		GroupMemberJoinEvent:              &EventHandle[*adapter.MemberJoinGroupEvent]{},
		GroupMemberLeaveEvent:             &EventHandle[*adapter.MemberLeaveGroupEvent]{},
		GroupInvitedEvent:                 &EventHandle[*adapter.GroupInvitedRequest]{},
		NewFriendRequestEvent:             &EventHandle[*adapter.NewFriendRequest]{},
		NewFriendEvent:                    &EventHandle[*adapter.NewFriendEvent]{},
		GroupJoinEvent:                    &EventHandle[*adapter.GroupInfo]{},
		GroupLeaveEvent:                   &EventHandle[*adapter.GroupLeaveEvent]{},
		GroupNotifyEvent:                  &EventHandle[adapter.NotifyEvent]{},
		FriendNotifyEvent:                 &EventHandle[adapter.NotifyEvent]{},
		MemberCardUpdatedEvent:            &EventHandle[*adapter.MemberCardUpdatedEvent]{},
		GroupNameUpdatedEvent:             &EventHandle[*adapter.GroupNameUpdatedEvent]{},
		MemberSpecialTitleUpdatedEvent:    &EventHandle[*adapter.MemberSpecialTitleUpdatedEvent]{},
		GroupMemberPermissionChangedEvent: &EventHandle[*adapter.MemberPermissionChangedEvent]{},
		GroupEssenceChangedEvent:          &EventHandle[*adapter.GroupDigestEvent]{},
		GroupDisbandEvent:                 &EventHandle[*adapter.GroupDisbandEvent]{},
		GroupUploadNotifyEvent:            &EventHandle[*adapter.GroupUploadNotifyEvent]{},
		GroupNotifyNotifyEvent:            &EventHandle[adapter.NotifyEvent]{},
		TempMessageEvent:                  &EventHandle[interface{}]{},
		BotOnlineEvent:                    &EventHandle[*adapter.BotOnlineEvent]{},
		BotOfflineEvent:                   &EventHandle[*adapter.BotOfflineEvent]{},
		BotSendFailedEvent:                &EventHandle[*adapter.BotSendFailedEvent]{},
		GroupMsgEmojiLikeEvent:            &EventHandle[*adapter.GroupMsgEmojiLikeEvent]{},
		ProfileLikeEvent:                  &EventHandle[*adapter.ProfileLikeEvent]{},
		PokeRecallEvent:                   &EventHandle[*adapter.PokeRecallEvent]{},
	}

	messenger.SetBotEventDispatcher(Instance)

	localutils.GetBot().Bot = Instance

	if err := messenger.Start(); err != nil {
		logger.Fatalf("Failed to start %s adapter: %v", adapterType, err)
	}

	// 启动模块服务
	StartService()

	// 等待获取 self ID
	go func() {
		for {
			if messenger.GetSelfID() > 0 {
				Instance.Uin.Store(messenger.GetSelfID())
				botOnline()
				break
			}
			time.Sleep(time.Second)
		}
	}()

	// 监控 WS 重连，重连后重新发布 bot_online 事件
	reconnectStop = make(chan struct{})
	go func() {
		// 等待首次上线（基于实际连接状态，避免心跳缓存 Online 滞后导致误判）
		for !messenger.IsConnected() {
			select {
			case <-reconnectStop:
				return
			case <-time.After(time.Second):
			}
		}
		wasOnline := true
		ticker := time.NewTicker(time.Second * 3)
		defer ticker.Stop()
		for {
			select {
			case <-reconnectStop:
				return
			case <-ticker.C:
				nowOnline := messenger.IsConnected()
				if nowOnline && !wasOnline {
					logger.Info("Bot reconnected, publishing bot_online event")
					eventbus.BusObj.Publish("bot_online", true)
				}
				wasOnline = nowOnline
			}
		}
	}()

	logger.Infof("%s adapter initialized", adapterType)
}

func botOnline() {
	logger.Infof("Bot online: %d", Instance.Uin.Load())
	Instance.Online.Store(true)
	// 发布 bot_online 事件，通知所有订阅模块（weibo/bilibili/acfun 等）
	eventbus.BusObj.Publish("bot_online", true)
}

func refreshList() {
	err := Instance.ReloadFriendList()
	if err != nil {
		logger.WithError(err).Error("unable to load friends list")
	}
	logger.Infof("load %d friends", len(Instance.GetFriendList()))

	err = Instance.ReloadGroupList()
	if err != nil {
		logger.WithError(err).Error("unable to load groups list")
	}
	groupSnapshot := Instance.GetGroupList()
	logger.Infof("load %d groups", len(groupSnapshot))

	for _, group := range groupSnapshot {
		members, err := Instance.GetGroupMembersByID(group.Code)
		if err != nil {
			logger.WithError(err).Errorf("unable to load group members for %d", group.Code)
			continue
		}
		logger.Debugf("群[%d]加载成员[%d]个", group.Code, len(members))
	}
	logger.Info("load members done.")
}

func RefreshList() {
	refreshList()
}

func StartService() {
	logger.Infof("StartService called, Instance=%p", Instance)
	if Instance.start.Load() {
		return
	}

	Instance.start.Store(true)

	logger.Infof("initializing modules ...")
	for _, mi := range modules {
		mi.Instance.Init()
	}
	for _, mi := range modules {
		mi.Instance.PostInit()
	}
	logger.Info("all modules initialized")

	logger.Info("registering modules serve functions ...")
	logger.Infof("Modules registered: %v", getModuleNames())
	for _, mi := range modules {
		logger.Infof("Calling Serve for module: %s, bot=%p", mi.ID, Instance)
		mi.Instance.Serve(Instance)
	}
	logger.Info("all modules serve functions registered")

	logger.Info("starting modules tasks ...")
	for _, mi := range modules {
		go mi.Instance.Start(Instance)
	}
	logger.Info("tasks running")
}

func Stop() {
	logger.Warn("stopping ...")
	if reconnectStop != nil {
		close(reconnectStop)
	}
	// 与 RegisterModule/GetModule 的 modulesMu 保持一致：先在锁下取快照再停止，
	// 避免模块 Stop 回调内调用 GetModule 时死锁
	modulesMu.RLock()
	moduleSnapshot := make([]ModuleInfo, 0, len(modules))
	for _, mi := range modules {
		moduleSnapshot = append(moduleSnapshot, mi)
	}
	modulesMu.RUnlock()

	wg := sync.WaitGroup{}
	for _, mi := range moduleSnapshot {
		wg.Add(1)
		mi.Instance.Stop(Instance, &wg)
	}
	wg.Wait()

	modulesMu.Lock()
	modules = make(map[string]ModuleInfo)
	modulesMu.Unlock()
	logger.Info("stopped")

	if Instance.Messenger != nil {
		Instance.Messenger.Stop()
	}
}

func getModuleNames() []string {
	var names []string
	for _, mi := range modules {
		names = append(names, string(mi.ID))
	}
	return names
}

func (bot *Bot) DispatchGroupMessage(msg *adapter.GroupMessage) {
	logger.Debugf("DispatchGroupMessage called: group=%d, user=%d, bot=%p, GroupMessageEvent=%p", msg.GroupCode, msg.Sender.UserID, bot, bot.GroupMessageEvent)
	if bot.GroupMessageEvent != nil {
		logger.Debugf("Dispatching to GroupMessageEvent")
		bot.GroupMessageEvent.Dispatch(msg)
	} else {
		logger.Warn("GroupMessageEvent is nil!")
	}
	if bot.SelfGroupMessageEvent != nil && msg.Sender.UserID == bot.GetSelfID() {
		bot.SelfGroupMessageEvent.Dispatch(msg)
	}
}

func (bot *Bot) DispatchPrivateMessage(msg *adapter.PrivateMessage) {
	if bot.PrivateMessageEvent != nil {
		bot.PrivateMessageEvent.Dispatch(msg)
	}
	if bot.SelfPrivateMessageEvent != nil && msg.Sender.UserID == bot.GetSelfID() {
		bot.SelfPrivateMessageEvent.Dispatch(msg)
	}
}

func (bot *Bot) DispatchGroupRecall(event *adapter.GroupMessageRecalledEvent) {
	if bot.GroupMessageRecalledEvent != nil {
		bot.GroupMessageRecalledEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchFriendRecall(event *adapter.FriendMessageRecalledEvent) {
	if bot.FriendMessageRecalledEvent != nil {
		bot.FriendMessageRecalledEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupMute(event *adapter.GroupMuteEvent) {
	if bot.GroupMuteEvent != nil {
		bot.GroupMuteEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchDisconnected(event *adapter.ClientDisconnectedEvent) {
	if bot.DisconnectedEvent != nil {
		bot.DisconnectedEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupMemberJoin(event *adapter.MemberJoinGroupEvent) {
	if bot.GroupMemberJoinEvent != nil {
		bot.GroupMemberJoinEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupMemberLeave(event *adapter.MemberLeaveGroupEvent) {
	if bot.GroupMemberLeaveEvent != nil {
		bot.GroupMemberLeaveEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupJoin(event *adapter.GroupInfo) {
	if bot.GroupJoinEvent != nil {
		bot.GroupJoinEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupLeave(event *adapter.GroupLeaveEvent) {
	if bot.GroupLeaveEvent != nil {
		bot.GroupLeaveEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupMemberPermissionChanged(event *adapter.MemberPermissionChangedEvent) {
	if bot.GroupMemberPermissionChangedEvent != nil {
		bot.GroupMemberPermissionChangedEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchMemberCardUpdated(event *adapter.MemberCardUpdatedEvent) {
	if bot.MemberCardUpdatedEvent != nil {
		bot.MemberCardUpdatedEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchMemberSpecialTitleUpdated(event *adapter.MemberSpecialTitleUpdatedEvent) {
	if bot.MemberSpecialTitleUpdatedEvent != nil {
		bot.MemberSpecialTitleUpdatedEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupUploadNotify(event *adapter.GroupUploadNotifyEvent) {
	if bot.GroupUploadNotifyEvent != nil {
		bot.GroupUploadNotifyEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupNotify(event adapter.NotifyEvent) {
	if bot.GroupNotifyEvent != nil {
		bot.GroupNotifyEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchFriendNotify(event adapter.NotifyEvent) {
	if bot.FriendNotifyEvent != nil {
		bot.FriendNotifyEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupNameUpdated(event *adapter.GroupNameUpdatedEvent) {
	if bot.GroupNameUpdatedEvent != nil {
		bot.GroupNameUpdatedEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupEssenceChanged(event *adapter.GroupDigestEvent) {
	if bot.GroupEssenceChangedEvent != nil {
		bot.GroupEssenceChangedEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupDisband(event *adapter.GroupDisbandEvent) {
	if bot.GroupDisbandEvent != nil {
		bot.GroupDisbandEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchNewFriendRequest(event *adapter.NewFriendRequest) {
	if bot.NewFriendRequestEvent != nil {
		bot.NewFriendRequestEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchNewFriend(event *adapter.NewFriendEvent) {
	if bot.NewFriendEvent != nil {
		bot.NewFriendEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchUserJoinGroupRequest(event *adapter.UserJoinGroupRequest) {
	if bot.GroupJoinEvent != nil {
		info := &adapter.GroupInfo{
			Uin: event.GroupCode,
		}
		bot.GroupJoinEvent.Dispatch(info)
	}
}

func (bot *Bot) DispatchGroupInvitedRequest(event *adapter.GroupInvitedRequest) {
	if bot.GroupInvitedEvent != nil {
		bot.GroupInvitedEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchBotOnline(event *adapter.BotOnlineEvent) {
	if bot.BotOnlineEvent != nil {
		bot.BotOnlineEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchBotOffline(event *adapter.BotOfflineEvent) {
	if bot.BotOfflineEvent != nil {
		bot.BotOfflineEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchGroupMsgEmojiLike(event *adapter.GroupMsgEmojiLikeEvent) {
	if bot.GroupMsgEmojiLikeEvent != nil {
		bot.GroupMsgEmojiLikeEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchProfileLike(event *adapter.ProfileLikeEvent) {
	if bot.ProfileLikeEvent != nil {
		bot.ProfileLikeEvent.Dispatch(event)
	}
}

func (bot *Bot) DispatchPokeRecall(event *adapter.PokeRecallEvent) {
	if bot.PokeRecallEvent != nil {
		bot.PokeRecallEvent.Dispatch(event)
	}
}
