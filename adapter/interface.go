package adapter

import (
	"time"

	"github.com/Mrs4s/MiraiGo/client"
)

type MessageType string

const (
	MessageTypePrivate MessageType = "private"
	MessageTypeGroup   MessageType = "group"
)

type PostType string

const (
	PostTypeMessage     PostType = "message"
	PostTypeMetaEvent   PostType = "meta_event"
	PostTypeNotice      PostType = "notice"
	PostTypeRequest     PostType = "request"
	PostTypeMessageSent PostType = "message_sent"
)

type AdapterConfig struct {
	Mode    string
	WSMode  string
	WSAddr  string
	Token   string
	Timeout time.Duration
}

type GroupMessageEvent struct {
	MessageID  int64
	GroupID    int64
	UserID     int64
	RawMessage string
	Message    []MessageSegment
	Time       int64
	SelfID     int64
}

type PrivateMessageEvent struct {
	MessageID  int64
	UserID     int64
	RawMessage string
	Message    []MessageSegment
	Time       int64
	SelfID     int64
	TargetID   int64
}

type MessageSegment struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

type MetaEvent struct {
	MetaEventType string
	Time          int64
	SelfID        int64
	Status        map[string]interface{}
	Interval      int64
}

type NoticeEvent struct {
	NoticeType   string
	Time         int64
	SelfID       int64
	GroupID      int64
	UserID       int64
	OperatorID   int64
	Duration     int32
	SubType      string
	Title        string
	MessageID    int64
	File         client.GroupFile
	EmojiId      string
	EmojiCount   int
	OperatorNick string
	Times        int
}

type RequestEvent struct {
	RequestType string
	Time        int64
	SelfID      int64
	GroupID     int64
	UserID      int64
	Comment     string
	Flag        string
	SubType     string
}

type MemberPermission int

const (
	Member        MemberPermission = 3
	Administrator MemberPermission = 2
	Owner         MemberPermission = 1
)

type GroupMemberInfo struct {
	Group           *GroupInfo
	Uin             int64
	Nickname        string
	CardName        string
	JoinTime        int64
	LastSentTime    int64
	LastSpeakTime   int64
	SpecialTitle    string
	ShutUpTimestamp int64
	Permission      MemberPermission
	Level           int
	Gender          byte

	GroupID         int64
	UserID          int64
	Card            string
	Sex             string
	Age             int
	Area            string
	Level_          interface{}
	QQLevel         int16
	TitleExpireTime int64
	Unfriendly      bool
	CardChangeable  bool
	IsRobot         bool
	Role            string
	Title           string
}

func (g *GroupMemberInfo) DisplayName() string {
	if g.CardName != "" {
		return g.CardName
	}
	return g.Nickname
}

func (g *GroupMemberInfo) IsAdminOrOwner() bool {
	return g.Permission == Administrator || g.Permission == Owner
}

func (g *GroupMemberInfo) Poke() {
	// Poke is handled by the messenger/adapter
}

type GroupInfo struct {
	Uin             int64
	Code            int64
	Name            string
	MemberCount     int
	MaxMemberCount  int
	GroupCreateTime int64
	GroupLevel      int
	Members         []*GroupMemberInfo
	Client          interface{}

	GroupID          int64
	GroupName        string
	MaxMemberCount_  int
	GroupCreateTime_ int64
	GroupLevel_      int
}

func (g *GroupInfo) FindMember(uin int64) *GroupMemberInfo {
	for _, m := range g.Members {
		if m.Uin == uin {
			return m
		}
	}
	return nil
}

func (g *GroupInfo) Quit() error {
	// Implemented by messenger
	return nil
}

type FriendInfo struct {
	Uin      int64
	Nickname string
	Remark   string
	FaceId   int
	Client   interface{}

	UserID int64
	Sex    string
	Level  int
}

func (f *FriendInfo) Poke() {
	// Poke is handled by the messenger/adapter
}

type Adapter interface {
	Start() error
	Stop() error

	SendApi(action string, params map[string]interface{}) (interface{}, error)
	SendGroupMessage(groupID int64, message interface{}) (int32, error)
	SendPrivateMessage(userID int64, message interface{}) (int32, error)

	GetSelfID() int64
	GetAdapterName() string
	IsConnected() bool

	OnGroupMessage(handler func(*GroupMessageEvent))
	OnPrivateMessage(handler func(*PrivateMessageEvent))
	OnMetaEvent(handler func(*MetaEvent))
	OnNoticeEvent(handler func(*NoticeEvent))
	OnRequestEvent(handler func(*RequestEvent))

	GetGroupList() ([]*GroupInfo, error)
	GetGroupMemberList(groupID int64) ([]*GroupMemberInfo, error)
	GetFriendList() ([]*FriendInfo, error)
	GetStrangerInfo(userID int64) (map[string]interface{}, error)
	GetGroupInfo(groupID int64) (*GroupInfo, error)

	GetGroupMemberInfo(groupID, userID int64) (*GroupMemberInfo, error)

	DownloadFile(url, base64, name string, headers []string) (string, error)
	GetFileUrl(groupCode int64, fileId string) string
	GetMsg(msgId int32) (interface{}, error)
	RecallMsg(msgId int32) error

	GroupPoke(groupCode, target int64) error
	FriendPoke(target int64) error
	SetGroupBan(groupCode, memberUin int64, duration int64) error
	SetGroupWholeBan(groupCode int64, enable bool) error
	KickGroupMember(groupCode int64, memberUin int64, rejectAddRequest bool) error
	SetGroupLeave(groupCode int64, isDismiss bool) error
	SetGroupAdmin(groupCode, memberUin int64, enable bool) error
	EditGroupCard(groupCode, memberUin int64, card string) error
	EditGroupTitle(groupCode, memberUin int64, title string) error
}

type EventCallback func(interface{})

type EventDispatcher struct {
	handlers map[string][]EventCallback
}

func NewEventDispatcher() *EventDispatcher {
	return &EventDispatcher{
		handlers: make(map[string][]EventCallback),
	}
}

func (ed *EventDispatcher) Register(eventType string, callback EventCallback) {
	ed.handlers[eventType] = append(ed.handlers[eventType], callback)
}

func (ed *EventDispatcher) Dispatch(eventType string, event interface{}) {
	if callbacks, exists := ed.handlers[eventType]; exists {
		for _, callback := range callbacks {
			callback(event)
		}
	}
}

type SendResponse struct {
	MessageID int32
	Error     error
}

func ParseMessageSegments(msg interface{}) []MessageSegment {
	var segments []MessageSegment

	switch m := msg.(type) {
	case string:
		segments = append(segments, MessageSegment{
			Type: "text",
			Data: map[string]interface{}{"text": m},
		})
	case []MessageSegment:
		segments = m
	case []interface{}:
		for _, item := range m {
			if seg, ok := item.(MessageSegment); ok {
				segments = append(segments, seg)
			} else if segMap, ok := item.(map[string]interface{}); ok {
				segType, _ := segMap["type"].(string)
				segData, _ := segMap["data"].(map[string]interface{})
				segments = append(segments, MessageSegment{
					Type: segType,
					Data: segData,
				})
			}
		}
	}

	return segments
}

func BuildMessageParams(message interface{}) map[string]interface{} {
	params := make(map[string]interface{})

	switch msg := message.(type) {
	case string:
		params["message"] = msg
	case []MessageSegment:
		params["message"] = msg
	case []interface{}:
		params["message"] = msg
	default:
		params["message"] = msg
	}

	return params
}

type BotCaller interface {
	FindFriend(uin int64) *FriendInfo
	FindGroup(code int64) *GroupInfo
	GetGroupList() []*GroupInfo
	GetFriendList() []*FriendInfo
	GetUin() int64
	DownloadFile(url, base64, name string, headers []string) (string, error)
	GetFileUrl(groupCode int64, fileId string) string
	GetMsg(msgId int32) (interface{}, error)
	RecallMsg(msgId int32) error
	SendApi(api string, params map[string]interface{}) (interface{}, error)
	GroupPoke(groupCode, target int64) error
	FriendPoke(target int64) error
}
