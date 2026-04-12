package adapter

import (
	"strconv"
	"time"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/Mrs4s/MiraiGo/message"
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
	GetMsg(msgId int32) (*GetMsgResult, error)
	GetMsgOrg(msgId int32) (interface{}, error)
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

	// SendGroupForwardMessage 发送群合并转发消息
	// nodes 格式: []map[string]interface{}{{type: "node", data: map[string]interface{}{...}}}
	// options 顶层参数: prompt, source, summary, news (可选)
	SendGroupForwardMessage(groupCode int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error)
	// SendPrivateForwardMessage 发送私聊合并转发消息
	SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error)
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

type GetMsgResult struct {
	MessageID  int64
	GroupID    int64
	UserID     int64
	RawMessage string
	Elements   []message.IMessageElement
	Time       int64
	Sender     *SenderInfo
}

type SenderInfo struct {
	UserID   int64
	Nickname string
	Card     string
	Role     string
}

// ForwardOptions 合并转发消息的顶层参数
// 对应 onebot-v11 send_group_forward_msg API 的顶层字段
type ForwardOptions struct {
	Prompt  string   // 转发消息外显标题 (LLOneBot/NapCatQQ 支持)
	Source  string   // 转发来源 (LLOneBot/NapCatQQ 支持)
	Summary string   // 转发摘要 (LLOneBot/NapCatQQ 支持)
	News    []string // 转发预览文本列表 (LLOneBot/NapCatQQ 支持)
}

// ConvertToMessageElements converts []MessageSegment to []message.IMessageElement
func ConvertToMessageElements(segments []MessageSegment) []message.IMessageElement {
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
	GetMsg(msgId int32) (*GetMsgResult, error)
	GetMsgOrg(msgId int32) (interface{}, error)
	RecallMsg(msgId int32) error
	SendApi(api string, params map[string]interface{}) (interface{}, error)
	GroupPoke(groupCode, target int64) error
	FriendPoke(target int64) error
	// SendGroupForwardMessage 发送群合并转发消息
	// nodes 格式: []map[string]interface{}{{type: "node", data: map[string]interface{}{id: "msgId"}}}
	// options 顶层参数: prompt, source, summary, news (可选)
	SendGroupForwardMessage(groupCode int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error)
	// SendPrivateForwardMessage 发送私聊合并转发消息
	SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error)
}
