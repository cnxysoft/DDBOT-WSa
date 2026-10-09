package bot

import (
	"fmt"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	ob11 "github.com/cnxysoft/DDBOT-WSa/adapter/onebot-v11"
	"github.com/cnxysoft/DDBOT-WSa/adapter/satori"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

type Bot struct {
	*adapter.Messenger

	start bool

	Uin        int64
	Online     atomic.Bool
	FriendList []*adapter.FriendInfo
	GroupList  []*adapter.GroupInfo
	Nickname   string
	Age        uint16
	Gender     uint16

	groupListLock  sync.Mutex
	friendListLock sync.Mutex

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
	UserJoinGroupRequestEvent         *EventHandle[*adapter.UserJoinGroupRequest]
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
	return bot.Uin
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

// EnsureGroup 确保群信息在缓存中，并在缓存命中时刷新自身群成员、派发 GroupJoinEvent。
//
// 成品 bot.(*Bot).EnsureGroup @0xb7dee0（bot/bot.go:91，320B）逐条对应：
//   - 0xb7def2: bot.Messenger == nil -> 0xb7df94 以 25 字节串 "messenger not initialized"
//     走 fmt.Errorf（0x10bcd78，callees 含 fmt.errorf/runtime.newobject）；
//   - 0xb7df0b: 调 adapter.(*Messenger).EnsureGroupCached(code)（@0x89bbc0，三值
//     group/exist/err）；0xb7df10 test %rcx,%rcx；jne 0xb7df86 将 err 原样透传；
//   - 0xb7df15 test %bl,%bl；je 0xb7df7c：exist==false 直接返回 group；
//   - exist==true 时内联 bot.GetUin()（0xb7df1e-0xb7df2d：Messenger+0x18 / bot+0x10），
//     0xb7df36 test %rdi,%rdi；je 跳过（守卫为 selfUin != 0）；
//   - 0xb7df46 call 0x89d1a0 = adapter.(*Messenger).RefreshGroupMember(code, selfUin)；
//   - 0xb7df55 mov 0xe8(%rdx) = bot.GroupJoinEvent（与 DispatchGroupJoin @0xb80a40 读取的
//     同一偏移），非 nil 时经 EventHandle.Dispatch（0xb7df72 -> 0xb81e80，字典 0x113d4a0）派发；
//   - 0xb7df7c 返回 (group, nil)（rax=group, rbx=err.type, rcx=err.data，即二值签名）。
func (bot *Bot) EnsureGroup(code int64) (*adapter.GroupInfo, error) {
	if bot.Messenger == nil {
		return nil, fmt.Errorf("messenger not initialized")
	}
	group, exist, err := bot.Messenger.EnsureGroupCached(code)
	if err != nil {
		return nil, err
	}
	if exist {
		if selfUin := bot.GetUin(); selfUin != 0 {
			// 成品 0xb7df46 直接调 adapter.(*Messenger).RefreshGroupMember，未经过 bot 包装。
			bot.Messenger.RefreshGroupMember(code, selfUin)
		}
		if bot.GroupJoinEvent != nil {
			bot.GroupJoinEvent.Dispatch(group)
		}
	}
	return group, nil
}

// EnsureGroupMember 确保指定群成员在缓存中。
//
// 成品 bot.(*Bot).EnsureGroupMember @0xb7e020（bot/bot.go:108，192B）：
// Messenger==nil 以同一 25 字节串走 fmt.Errorf；否则 0xb7e08f 尾调
// adapter.(*Messenger).EnsureGroupMember @0x89d060，无其它逻辑。
func (bot *Bot) EnsureGroupMember(code int64, uin int64) (*adapter.GroupMemberInfo, error) {
	if bot.Messenger == nil {
		return nil, fmt.Errorf("messenger not initialized")
	}
	return bot.Messenger.EnsureGroupMember(code, uin)
}

// RefreshGroupMember 强制从 OneBot 拉取成员资料并刷新缓存。
//
// 成品 bot.(*Bot).RefreshGroupMember @0xb7e0e0（bot/bot.go:116，192B）：
// Messenger==nil 以同一 25 字节串走 fmt.Errorf；否则 0xb7e14f 尾调
// adapter.(*Messenger).RefreshGroupMember @0x89d1a0。
func (bot *Bot) RefreshGroupMember(code int64, uin int64) (*adapter.GroupMemberInfo, error) {
	if bot.Messenger == nil {
		return nil, fmt.Errorf("messenger not initialized")
	}
	return bot.Messenger.RefreshGroupMember(code, uin)
}

func (bot *Bot) FindFriend(uin int64) *adapter.FriendInfo {
	if bot.Messenger != nil {
		return bot.Messenger.FindFriend(uin)
	}
	return nil
}

func (bot *Bot) ReloadGroupList() error {
	if bot.Messenger != nil {
		err := bot.Messenger.ReloadGroupList()
		if err != nil {
			return err
		}
		bot.GroupList = bot.Messenger.GroupList
		return nil
	}
	return fmt.Errorf("messenger not initialized")
}

func (bot *Bot) ReloadFriendList() error {
	if bot.Messenger != nil {
		err := bot.Messenger.ReloadFriendList()
		if err != nil {
			return err
		}
		bot.FriendList = bot.Messenger.FriendList
		return nil
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

func (bot *Bot) SendPrivateMessage(target int64, m interface{}, newstr string) *adapter.PrivateMessage {
	if bot.Messenger != nil {
		sendingMsg, ok := m.(*adapter.SendingMessage)
		if !ok {
			return &adapter.PrivateMessage{ID: -1}
		}
		return bot.Messenger.SendPrivateMessage(target, sendingMsg, newstr)
	}
	return &adapter.PrivateMessage{ID: -1}
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
		return bot.Messenger.GroupList
	}
	return nil
}

func (bot *Bot) GetFriendList() []*adapter.FriendInfo {
	if bot.Messenger != nil {
		return bot.Messenger.FriendList
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
		start:                             false,
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
		UserJoinGroupRequestEvent:         &EventHandle[*adapter.UserJoinGroupRequest]{},
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
				Instance.Uin = messenger.GetSelfID()
				botOnline()
				break
			}
			time.Sleep(time.Second)
		}
	}()

	logger.Infof("%s adapter initialized", adapterType)
}

func botOnline() {
	logger.Infof("Bot online: %d", Instance.Uin)
	Instance.Online.Store(true)
}

func refreshList() {
	err := Instance.ReloadFriendList()
	if err != nil {
		logger.WithError(err).Error("unable to load friends list")
	}
	logger.Infof("load %d friends", len(Instance.FriendList))

	err = Instance.ReloadGroupList()
	if err != nil {
		logger.WithError(err).Error("unable to load groups list")
	}
	logger.Infof("load %d groups", len(Instance.GroupList))

	for _, group := range Instance.GroupList {
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
	if Instance.start {
		return
	}

	Instance.start = true

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
	wg := sync.WaitGroup{}
	for _, mi := range modules {
		wg.Add(1)
		mi.Instance.Stop(Instance, &wg)
	}
	wg.Wait()
	logger.Info("stopped")
	modules = make(map[string]ModuleInfo)

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

// DispatchUserJoinGroupRequest 直接派发 UserJoinGroupRequestEvent。
//
// 成品 bot.(*Bot).DispatchUserJoinGroupRequest @0xb80f60（bot/bot.go:594，96B）：
//
//	0xb80f6e mov 0xd0(%rax),%rax  = bot.UserJoinGroupRequestEvent，与 Init 中
//	        0xb7f3a0-0xb7f3b3 mov %rax,0xd0(%rcx) 写入的是同一 8B 槽位；
//	0xb80f75 test %rax,%rax; je 0xb80f89 为 nil 守卫；
//	0xb80f7d lea 0x113d5c0,%rbx   = EventHandle[*adapter.UserJoinGroupRequest] 类型字典；
//	0xb80f84 call 0xb81e80        = EventHandle[go.shape.*uint8].Dispatch（events.go:22），
//	        参数为原始 event 指针（%rbx -> %rcx）。
//
// 全程无 GroupJoinEvent 合成路径。
func (bot *Bot) DispatchUserJoinGroupRequest(event *adapter.UserJoinGroupRequest) {
	if bot.UserJoinGroupRequestEvent != nil {
		bot.UserJoinGroupRequestEvent.Dispatch(event)
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
