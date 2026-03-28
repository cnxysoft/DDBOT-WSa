package bot

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	ob11 "github.com/cnxysoft/DDBOT-WSa/adapter/onebot-v11"
	"github.com/cnxysoft/DDBOT-WSa/adapter/satori"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"
	"gopkg.ilharper.com/x/isatty"
)

var reloginLock = new(sync.Mutex)

const sessionToken = "session.token"

type Bot struct {
	*adapter.Messenger

	start    bool
	isQRCode bool

	Uin        int64
	Online     atomic.Bool
	FriendList []*adapter.FriendInfo
	GroupList  []*adapter.GroupInfo
	Nickname   string
	Age        uint16
	Gender     uint16

	groupListLock  sync.Mutex
	friendListLock sync.Mutex

	QQClient                          *client.QQClient
	GroupMessageRecalledEvent         *client.EventHandle[*client.GroupMessageRecalledEvent]
	GroupMessageEvent                 *client.EventHandle[*message.GroupMessage]
	GroupMuteEvent                    *client.EventHandle[*client.GroupMuteEvent]
	PrivateMessageEvent               *client.EventHandle[*message.PrivateMessage]
	FriendMessageRecalledEvent        *client.EventHandle[*client.FriendMessageRecalledEvent]
	DisconnectedEvent                 *client.EventHandle[*client.ClientDisconnectedEvent]
	SelfGroupMessageEvent             *client.EventHandle[*message.GroupMessage]
	SelfPrivateMessageEvent           *client.EventHandle[*message.PrivateMessage]
	GroupMemberJoinEvent              *client.EventHandle[*client.MemberJoinGroupEvent]
	GroupMemberLeaveEvent             *client.EventHandle[*client.MemberLeaveGroupEvent]
	GroupInvitedEvent                 *client.EventHandle[*client.GroupInvitedRequest]
	NewFriendRequestEvent             *client.EventHandle[*client.NewFriendRequest]
	NewFriendEvent                    *client.EventHandle[*client.NewFriendEvent]
	GroupJoinEvent                    *client.EventHandle[*client.GroupInfo]
	GroupLeaveEvent                   *client.EventHandle[*client.GroupLeaveEvent]
	GroupNotifyEvent                  *client.EventHandle[client.INotifyEvent]
	FriendNotifyEvent                 *client.EventHandle[client.INotifyEvent]
	MemberCardUpdatedEvent            *client.EventHandle[*client.MemberCardUpdatedEvent]
	GroupNameUpdatedEvent             *client.EventHandle[*client.GroupNameUpdatedEvent]
	MemberSpecialTitleUpdatedEvent    *client.EventHandle[*client.MemberSpecialTitleUpdatedEvent]
	GroupMemberPermissionChangedEvent *client.EventHandle[*client.MemberPermissionChangedEvent]
	GroupEssenceChangedEvent          *client.EventHandle[*client.GroupDigestEvent]
	GroupDisbandEvent                 *client.EventHandle[*client.GroupDisbandEvent]
	GroupUploadNotifyEvent            *client.EventHandle[*client.GroupUploadNotifyEvent]
	GroupNotifyNotifyEvent            *client.EventHandle[client.INotifyEvent]
	TempMessageEvent                  *client.EventHandle[*client.TempMessageEvent]
	BotOnlineEvent                    *client.EventHandle[*client.BotOnlineEvent]
	BotOfflineEvent                   *client.EventHandle[*client.BotOfflineEvent]
	BotSendFailedEvent                *client.EventHandle[*client.BotSendFailedEvent]
	GroupMsgEmojiLikeEvent            *client.EventHandle[*client.GroupMsgEmojiLikeEvent]
	ProfileLikeEvent                  *client.EventHandle[*client.ProfileLikeEvent]
	PokeRecallEvent                   *client.EventHandle[*client.PokeRecallEvent]
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
		sendingMsg, ok := m.(*message.SendingMessage)
		if !ok {
			return adapter.SendResp{
				RetMSG: &message.GroupMessage{Id: -1},
				Error:  fmt.Errorf("invalid message type"),
			}
		}
		return bot.Messenger.SendGroupMessage(groupCode, sendingMsg, newstr)
	}
	return adapter.SendResp{
		RetMSG: &message.GroupMessage{Id: -1},
		Error:  fmt.Errorf("messenger not initialized"),
	}
}

func (bot *Bot) SendPrivateMessage(target int64, m interface{}, newstr string) *message.PrivateMessage {
	if bot.Messenger != nil {
		sendingMsg, ok := m.(*message.SendingMessage)
		if !ok {
			return &message.PrivateMessage{Id: -1}
		}
		return bot.Messenger.SendPrivateMessage(target, sendingMsg, newstr)
	}
	return &message.PrivateMessage{Id: -1}
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

func (bot *Bot) GetMsg(msgId int32) (interface{}, error) {
	if bot.Messenger != nil {
		return bot.Messenger.GetMsg(msgId)
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

func (bot *Bot) saveToken() {
	// 无需保存 token，因为使用适配器
}

func (bot *Bot) clearToken() {
	// 无需清理 token
}

func (bot *Bot) getToken() ([]byte, error) {
	// 返回空，因为使用适配器
	return []byte{}, nil
}

func (bot *Bot) ReLogin(e interface{}) error {
	reloginLock.Lock()
	defer reloginLock.Unlock()

	if !bot.Online.Load() {
		logger.Info("Bot offline")
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
		QQClient:                          nil,
		GroupMessageRecalledEvent:         &client.EventHandle[*client.GroupMessageRecalledEvent]{},
		GroupMessageEvent:                 &client.EventHandle[*message.GroupMessage]{},
		GroupMuteEvent:                    &client.EventHandle[*client.GroupMuteEvent]{},
		PrivateMessageEvent:               &client.EventHandle[*message.PrivateMessage]{},
		FriendMessageRecalledEvent:        &client.EventHandle[*client.FriendMessageRecalledEvent]{},
		DisconnectedEvent:                 &client.EventHandle[*client.ClientDisconnectedEvent]{},
		SelfGroupMessageEvent:             &client.EventHandle[*message.GroupMessage]{},
		SelfPrivateMessageEvent:           &client.EventHandle[*message.PrivateMessage]{},
		GroupMemberJoinEvent:              &client.EventHandle[*client.MemberJoinGroupEvent]{},
		GroupMemberLeaveEvent:             &client.EventHandle[*client.MemberLeaveGroupEvent]{},
		GroupInvitedEvent:                 &client.EventHandle[*client.GroupInvitedRequest]{},
		NewFriendRequestEvent:             &client.EventHandle[*client.NewFriendRequest]{},
		NewFriendEvent:                    &client.EventHandle[*client.NewFriendEvent]{},
		GroupJoinEvent:                    &client.EventHandle[*client.GroupInfo]{},
		GroupLeaveEvent:                   &client.EventHandle[*client.GroupLeaveEvent]{},
		GroupNotifyEvent:                  &client.EventHandle[client.INotifyEvent]{},
		FriendNotifyEvent:                 &client.EventHandle[client.INotifyEvent]{},
		MemberCardUpdatedEvent:            &client.EventHandle[*client.MemberCardUpdatedEvent]{},
		GroupNameUpdatedEvent:             &client.EventHandle[*client.GroupNameUpdatedEvent]{},
		MemberSpecialTitleUpdatedEvent:    &client.EventHandle[*client.MemberSpecialTitleUpdatedEvent]{},
		GroupMemberPermissionChangedEvent: &client.EventHandle[*client.MemberPermissionChangedEvent]{},
		GroupEssenceChangedEvent:          &client.EventHandle[*client.GroupDigestEvent]{},
		GroupDisbandEvent:                 &client.EventHandle[*client.GroupDisbandEvent]{},
		GroupUploadNotifyEvent:            &client.EventHandle[*client.GroupUploadNotifyEvent]{},
		GroupNotifyNotifyEvent:            &client.EventHandle[client.INotifyEvent]{},
		TempMessageEvent:                  &client.EventHandle[*client.TempMessageEvent]{},
		BotOnlineEvent:                    &client.EventHandle[*client.BotOnlineEvent]{},
		BotOfflineEvent:                   &client.EventHandle[*client.BotOfflineEvent]{},
		BotSendFailedEvent:                &client.EventHandle[*client.BotSendFailedEvent]{},
		GroupMsgEmojiLikeEvent:            &client.EventHandle[*client.GroupMsgEmojiLikeEvent]{},
		ProfileLikeEvent:                  &client.EventHandle[*client.ProfileLikeEvent]{},
		PokeRecallEvent:                   &client.EventHandle[*client.PokeRecallEvent]{},
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

	// 刷新群和好友列表
	go func() {
		time.Sleep(time.Second * 5)
		refreshList()
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

func Login() {
	// 不需要登录，因为使用适配器
	logger.Info("Adapter mode: no login required")
}

var deviceInfo interface{}

func UseDevice(device []byte) error {
	return nil
}

func GenRandomDevice() {
}

var remoteVersions = map[int]string{
	1: "https://raw.githubusercontent.com/RomiChan/protocol-versions/master/android_phone.json",
	6: "https://raw.githubusercontent.com/RomiChan/protocol-versions/master/android_pad.json",
}

func getRemoteLatestProtocolVersion(protocolType int) ([]byte, error) {
	url, ok := remoteVersions[protocolType]
	if !ok {
		return nil, fmt.Errorf("remote version unavailable")
	}
	resp, err := http.Get(url)
	if err != nil {
		resp, err = http.Get("https://ghproxy.com/" + url)
	}
	if err != nil {
		return nil, err
	}
	return io.ReadAll(resp.Body)
}

func readIfTTY(de string) (str string) {
	if isatty.Isatty(os.Stdin.Fd()) {
		return readLine()
	}
	logger.Warnf("未检测到输入终端，自动选择%s.", de)
	return de
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

type LoginResponse struct {
	Success bool
}

func (bot *Bot) Login() (interface{}, error) {
	return &LoginResponse{Success: true}, nil
}

func (bot *Bot) FetchQRCode() (interface{}, error) {
	return []byte{}, nil
}

func (bot *Bot) FetchQRCodeCustomSize(a, b, c uint32) (interface{}, error) {
	return []byte{}, nil
}

func (bot *Bot) QueryQRCodeStatus([]byte) (interface{}, error) {
	return &LoginResponse{Success: true}, nil
}

func (bot *Bot) QRCodeLogin(interface{}) (interface{}, error) {
	return &LoginResponse{Success: true}, nil
}

func (bot *Bot) SubmitTicket(string) (interface{}, error) {
	return &LoginResponse{Success: true}, nil
}

func (bot *Bot) SubmitCaptcha(string, []byte) (interface{}, error) {
	return &LoginResponse{Success: true}, nil
}

func (bot *Bot) RequestSMS() bool {
	return false
}

func (bot *Bot) SubmitSMS(string) (interface{}, error) {
	return &LoginResponse{Success: true}, nil
}

func (bot *Bot) UseDevice(info interface{}) error {
	return nil
}

func (bot *Bot) Device() interface{} {
	return nil
}

func (bot *Bot) DispatchGroupMessage(msg *message.GroupMessage) {
	logger.Debugf("DispatchGroupMessage called: group=%d, user=%d, bot=%p, GroupMessageEvent=%p", msg.GroupCode, msg.Sender.Uin, bot, bot.GroupMessageEvent)
	if bot.GroupMessageEvent != nil {
		logger.Debugf("Dispatching to GroupMessageEvent")
		bot.GroupMessageEvent.Dispatch(nil, msg)
	} else {
		logger.Warn("GroupMessageEvent is nil!")
	}
	if bot.SelfGroupMessageEvent != nil && msg.Sender.Uin == bot.GetSelfID() {
		bot.SelfGroupMessageEvent.Dispatch(nil, msg)
	}
}

func (bot *Bot) DispatchPrivateMessage(msg *message.PrivateMessage) {
	if bot.PrivateMessageEvent != nil {
		bot.PrivateMessageEvent.Dispatch(nil, msg)
	}
	if bot.SelfPrivateMessageEvent != nil && msg.Sender.Uin == bot.GetSelfID() {
		bot.SelfPrivateMessageEvent.Dispatch(nil, msg)
	}
}

func (bot *Bot) DispatchGroupRecall(event *client.GroupMessageRecalledEvent) {
	if bot.GroupMessageRecalledEvent != nil {
		bot.GroupMessageRecalledEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchFriendRecall(event *client.FriendMessageRecalledEvent) {
	if bot.FriendMessageRecalledEvent != nil {
		bot.FriendMessageRecalledEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupMute(event *client.GroupMuteEvent) {
	if bot.GroupMuteEvent != nil {
		bot.GroupMuteEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchDisconnected(event *client.ClientDisconnectedEvent) {
	if bot.DisconnectedEvent != nil {
		bot.DisconnectedEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupMemberJoin(event *client.MemberJoinGroupEvent) {
	if bot.GroupMemberJoinEvent != nil {
		bot.GroupMemberJoinEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupMemberLeave(event *client.MemberLeaveGroupEvent) {
	if bot.GroupMemberLeaveEvent != nil {
		bot.GroupMemberLeaveEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupJoin(event *client.GroupInfo) {
	if bot.GroupJoinEvent != nil {
		bot.GroupJoinEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupLeave(event *client.GroupLeaveEvent) {
	if bot.GroupLeaveEvent != nil {
		bot.GroupLeaveEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupMemberPermissionChanged(event *client.MemberPermissionChangedEvent) {
	if bot.GroupMemberPermissionChangedEvent != nil {
		bot.GroupMemberPermissionChangedEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchMemberCardUpdated(event *client.MemberCardUpdatedEvent) {
	if bot.MemberCardUpdatedEvent != nil {
		bot.MemberCardUpdatedEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchMemberSpecialTitleUpdated(event *client.MemberSpecialTitleUpdatedEvent) {
	if bot.MemberSpecialTitleUpdatedEvent != nil {
		bot.MemberSpecialTitleUpdatedEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupUploadNotify(event *client.GroupUploadNotifyEvent) {
	if bot.GroupUploadNotifyEvent != nil {
		bot.GroupUploadNotifyEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupNotify(event client.INotifyEvent) {
	if bot.GroupNotifyEvent != nil {
		bot.GroupNotifyEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchFriendNotify(event client.INotifyEvent) {
	if bot.FriendNotifyEvent != nil {
		bot.FriendNotifyEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupNameUpdated(event *client.GroupNameUpdatedEvent) {
	if bot.GroupNameUpdatedEvent != nil {
		bot.GroupNameUpdatedEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupEssenceChanged(event *client.GroupDigestEvent) {
	if bot.GroupEssenceChangedEvent != nil {
		bot.GroupEssenceChangedEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupDisband(event *client.GroupDisbandEvent) {
	if bot.GroupDisbandEvent != nil {
		bot.GroupDisbandEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchNewFriendRequest(event *client.NewFriendRequest) {
	if bot.NewFriendRequestEvent != nil {
		bot.NewFriendRequestEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchNewFriend(event *client.NewFriendEvent) {
	if bot.NewFriendEvent != nil {
		bot.NewFriendEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchUserJoinGroupRequest(event *client.UserJoinGroupRequest) {
	if bot.GroupJoinEvent != nil {
		info := &client.GroupInfo{
			Uin: event.GroupCode,
		}
		bot.GroupJoinEvent.Dispatch(nil, info)
	}
}

func (bot *Bot) DispatchGroupInvitedRequest(event *client.GroupInvitedRequest) {
	if bot.GroupInvitedEvent != nil {
		bot.GroupInvitedEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchBotOnline(event *client.BotOnlineEvent) {
	if bot.BotOnlineEvent != nil {
		bot.BotOnlineEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchBotOffline(event *client.BotOfflineEvent) {
	if bot.BotOfflineEvent != nil {
		bot.BotOfflineEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchGroupMsgEmojiLike(event *client.GroupMsgEmojiLikeEvent) {
	if bot.GroupMsgEmojiLikeEvent != nil {
		bot.GroupMsgEmojiLikeEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchProfileLike(event *client.ProfileLikeEvent) {
	if bot.ProfileLikeEvent != nil {
		bot.ProfileLikeEvent.Dispatch(nil, event)
	}
}

func (bot *Bot) DispatchPokeRecall(event *client.PokeRecallEvent) {
	if bot.PokeRecallEvent != nil {
		bot.PokeRecallEvent.Dispatch(nil, event)
	}
}
