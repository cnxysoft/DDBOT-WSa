package adapter

type GroupMuteEvent struct {
	GroupCode   int64
	OperatorUin int64
	TargetUin   int64
	Time        int32
}

type GroupMessageRecalledEvent struct {
	GroupCode   int64
	OperatorUin int64
	AuthorUin   int64
	MessageId   int32
	Time        int32
}

type FriendMessageRecalledEvent struct {
	FriendUin int64
	MessageId int32
	Time      int64
}

type ClientDisconnectedEvent struct {
	Message string
}

type GroupLeaveEvent struct {
	Group    *GroupInfo
	Operator *GroupMemberInfo
}

type MemberJoinGroupEvent struct {
	Group  *GroupInfo
	Member *GroupMemberInfo
}

type MemberLeaveGroupEvent struct {
	Group    *GroupInfo
	Member   *GroupMemberInfo
	Operator *GroupMemberInfo
}

type MemberPermissionChangedEvent struct {
	Group         *GroupInfo
	Member        *GroupMemberInfo
	OldPermission MemberPermission
	NewPermission MemberPermission
}

type MemberCardUpdatedEvent struct {
	Group   *GroupInfo
	OldCard string
	Member  *GroupMemberInfo
}

type GroupNameUpdatedEvent struct {
	Group       *GroupInfo
	OldName     string
	NewName     string
	OperatorUin int64
}

type MemberSpecialTitleUpdatedEvent struct {
	GroupCode int64
	Uin       int64
	NewTitle  string
}

type GroupUploadNotifyEvent struct {
	GroupCode int64
	Sender    int64
	File      GroupFile
}

type NotifyEvent interface {
	From() int64
	Content() string
}

type GroupPokeNotifyEvent struct {
	GroupCode int64
	Sender    int64
	Receiver  int64
}

func (e *GroupPokeNotifyEvent) From() int64 {
	return e.Sender
}

func (e *GroupPokeNotifyEvent) Content() string {
	return "群内戳一戳"
}

type FriendPokeNotifyEvent struct {
	Sender   int64
	Receiver int64
}

func (e *FriendPokeNotifyEvent) From() int64 {
	return e.Sender
}

func (e *FriendPokeNotifyEvent) Content() string {
	return "好友戳一戳"
}

type GroupDigestEvent struct {
	GroupCode int64
}

type GroupDisbandEvent struct {
	Group    *GroupInfo
	Time     int64
	Operator *GroupMemberInfo
}

type NewFriendRequest struct {
	RequestId     int64
	Message       string
	RequesterUin  int64
	RequesterNick string
	Flag          string
}

type NewFriendEvent struct {
	Friend *FriendInfo
}

type UserJoinGroupRequest struct {
	RequestId     int64  `json:"request_id"`
	Message       string `json:"message"`
	RequesterUin  int64  `json:"requester_uin"`
	RequesterNick string `json:"requester_nick"`
	GroupCode     int64  `json:"group_id"`
	GroupName     string `json:"group_name"`
	ActionUinNick string `json:"action_uin_nick"`
	ActionUin     int64  `json:"action_uin"`
	Flag          string `json:"flag"`
	Checked       bool   `json:"checked"`
	Actor         int64  `json:"actor"`
	Suspicious    bool   `json:"suspicious"`
}

type GroupInvitedRequest struct {
	RequestId   int64  `json:"request_id"`
	InvitorUin  int64  `json:"invitor_uin"`
	InvitorNick string `json:"invitor_nick"`
	GroupCode   int64  `json:"group_id"`
	GroupName   string `json:"group_name"`
	Flag        string `json:"flag"`
	Checked     bool   `json:"checked"`
	Actor       int64  `json:"actor"`
}

type BotOnlineEvent struct{}

type BotOfflineEvent struct{}

type BotSendFailedEvent struct {
	Message    string
	TargetUin  int64
	TargetType int
	Times      int
}

type GroupMsgEmojiLikeEvent struct {
	GroupCode  int64
	UserId     int64
	MessageId  int64
	EmojiId    string
	EmojiCount int
	IsAdd      bool
}

type ProfileLikeEvent struct {
	OperatorId   int64
	OperatorNick string
	Times        int
}

type PokeRecallEvent struct {
	GroupCode int64
	Sender    int64
	Receiver  int64
}
