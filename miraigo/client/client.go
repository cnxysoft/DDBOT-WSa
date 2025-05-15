package client

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/netip"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/pkg/errors"

	"github.com/RomiChan/syncx"
	"github.com/gorilla/websocket"

	"github.com/Mrs4s/MiraiGo/binary"
	"github.com/Mrs4s/MiraiGo/client/internal/auth"
	"github.com/Mrs4s/MiraiGo/client/internal/highway"
	"github.com/Mrs4s/MiraiGo/client/internal/network"
	"github.com/Mrs4s/MiraiGo/client/internal/oicq"
	"github.com/Mrs4s/MiraiGo/client/pb/msg"
	"github.com/Mrs4s/MiraiGo/message"
	"github.com/Mrs4s/MiraiGo/utils"
	"github.com/Sora233/MiraiGo-Template/config"
)

type QQClient struct {
	Uin         int64
	PasswordMd5 [16]byte

	stat Statistics
	once sync.Once

	// option
	AllowSlider        bool
	UseFragmentMessage bool

	// account info
	Online        atomic.Bool
	Nickname      string
	Age           uint16
	Gender        uint16
	FriendList    []*FriendInfo
	GroupList     []*GroupInfo
	OnlineClients []*OtherClientInfo
	QiDian        *QiDianAccountInfo
	GuildService  *GuildService

	// protocol public field
	SequenceId  atomic.Int32
	SessionId   []byte
	TCP         *network.TCPClient // todo: combine other protocol state into one struct
	ConnectTime time.Time

	transport *network.Transport
	oicq      *oicq.Codec
	logger    Logger

	// internal state
	handlers        syncx.Map[uint16, *handlerInfo]
	waiters         syncx.Map[string, func(any, error)]
	initServerOnce  sync.Once
	servers         []netip.AddrPort
	currServerIndex int
	retryTimes      int
	alive           bool

	// session info
	qwebSeq        atomic.Int64
	sig            *auth.SigInfo
	highwaySession *highway.Session
	// pwdFlag        bool
	// timeDiff       int64

	// address
	// otherSrvAddrs   []string
	// fileStorageInfo *jce.FileStoragePushFSSvcList

	// event handles
	eventHandlers                     eventHandlers
	PrivateMessageEvent               EventHandle[*message.PrivateMessage]
	TempMessageEvent                  EventHandle[*TempMessageEvent]
	GroupMessageEvent                 EventHandle[*message.GroupMessage]
	SelfPrivateMessageEvent           EventHandle[*message.PrivateMessage]
	SelfGroupMessageEvent             EventHandle[*message.GroupMessage]
	GroupMuteEvent                    EventHandle[*GroupMuteEvent]
	GroupMessageRecalledEvent         EventHandle[*GroupMessageRecalledEvent]
	FriendMessageRecalledEvent        EventHandle[*FriendMessageRecalledEvent]
	GroupJoinEvent                    EventHandle[*GroupInfo]
	GroupLeaveEvent                   EventHandle[*GroupLeaveEvent]
	GroupMemberJoinEvent              EventHandle[*MemberJoinGroupEvent]
	GroupMemberLeaveEvent             EventHandle[*MemberLeaveGroupEvent]
	MemberCardUpdatedEvent            EventHandle[*MemberCardUpdatedEvent]
	GroupNameUpdatedEvent             EventHandle[*GroupNameUpdatedEvent]
	GroupMemberPermissionChangedEvent EventHandle[*MemberPermissionChangedEvent]
	GroupInvitedEvent                 EventHandle[*GroupInvitedRequest]
	UserWantJoinGroupEvent            EventHandle[*UserJoinGroupRequest]
	NewFriendEvent                    EventHandle[*NewFriendEvent]
	NewFriendRequestEvent             EventHandle[*NewFriendRequest]
	DisconnectedEvent                 EventHandle[*ClientDisconnectedEvent]
	GroupNotifyEvent                  EventHandle[INotifyEvent]
	FriendNotifyEvent                 EventHandle[INotifyEvent]
	MemberSpecialTitleUpdatedEvent    EventHandle[*MemberSpecialTitleUpdatedEvent]
	GroupDigestEvent                  EventHandle[*GroupDigestEvent]
	OtherClientStatusChangedEvent     EventHandle[*OtherClientStatusChangedEvent]
	OfflineFileEvent                  EventHandle[*OfflineFileEvent]
	GroupDisbandEvent                 EventHandle[*GroupDisbandEvent]
	DeleteFriendEvent                 EventHandle[*DeleteFriendEvent]
	GroupUploadNotifyEvent            EventHandle[*GroupUploadNotifyEvent]

	// message state
	msgSvcCache            *utils.Cache[unit]
	lastC2CMsgTime         int64
	transCache             *utils.Cache[unit]
	groupSysMsgCache       *GroupSystemMessages
	msgBuilders            syncx.Map[int32, *messageBuilder]
	onlinePushCache        *utils.Cache[unit]
	heartbeatEnabled       bool
	requestPacketRequestID atomic.Int32
	groupSeq               atomic.Int32
	friendSeq              atomic.Int32
	highwayApplyUpSeq      atomic.Int32

	groupListLock sync.Mutex
	//增加的字段
	ws                 *websocket.Conn
	wsWriteLock        sync.Mutex
	responseCh         map[string]chan *T
	disconnectChan     chan bool
	currentEcho        string
	limiterMessageSend *RateLimiter
}

// 新增
type GroupData struct {
	GroupCreateTime int64  `json:"group_create_time"`
	GroupID         int64  `json:"group_id"`
	GroupLevel      int    `json:"group_level"`
	GroupName       string `json:"group_name"`
	MaxMemberCount  int    `json:"max_member_count"`
	MemberCount     int    `json:"member_count"`
	// ... and other fields ...
}

// 新增
type ResponseGroupsData struct {
	Data    []GroupData `json:"data"`
	Message string      `json:"message"`
	Retcode int         `json:"retcode"`
	Status  string      `json:"status"`
	Echo    string      `json:"echo"`
}

// 新增
type ResponseGroupData struct {
	Data    GroupData `json:"data"`
	Message string    `json:"message"`
	Retcode int       `json:"retcode"`
	Status  string    `json:"status"`
	Echo    string    `json:"echo"`
}

// 好友信息请求返回
type ResponseFriendList struct {
	Status  string       `json:"status"`
	Retcode int          `json:"retcode"`
	Message string       `json:"message"`
	Data    []FriendData `json:"data"`
	Wording string       `json:"wording"`
}

// 好友信息
type FriendData struct {
	UserID   int64  `json:"user_id"`
	NickName string `json:"nickname"`
	Remark   string `json:"remark"`
	Sex      string `json:"sex"`
	Level    int    `json:"level"`
}

// 群成员信息
type MemberData struct {
	GroupID         int64       `json:"group_id"`
	UserID          int64       `json:"user_id"`
	Nickname        string      `json:"nickname"`
	Card            string      `json:"card"`
	Sex             string      `json:"sex"`
	Age             int         `json:"age"`
	Area            string      `json:"area"`
	Level           interface{} `json:"level"`
	QQLevel         int16       `json:"qq_level"`
	JoinTime        int64       `json:"join_time"`
	LastSentTime    int64       `json:"last_sent_time"`
	TitleExpireTime int64       `json:"title_expire_time"`
	Unfriendly      bool        `json:"unfriendly"`
	CardChangeable  bool        `json:"card_changeable"`
	IsRobot         bool        `json:"is_robot"`
	ShutUpTimestamp int64       `json:"shut_up_timestamp"`
	Role            string      `json:"role"`
	Title           string      `json:"title"`
}

// 群成员请求返回
type ResponseGroupMemberData struct {
	Status  string       `json:"status"`
	Retcode int          `json:"retcode"`
	Message string       `json:"message"`
	Wording string       `json:"wording"`
	Data    []MemberData `json:"data"`
}

// 新增
type BasicMessage struct {
	Echo string `json:"echo"`
	// Data json.RawMessage `json:"data"`
}

// 发送消息返回
type ResponseSendMessage struct {
	Status  string `json:"status"`
	Retcode int    `json:"retcode"`
	Data    struct {
		MessageID int32 `json:"message_id"`
	}
	Message string `json:"message"`
	Wording string `json:"wording"`
}

// set指令返回
type ResponseSetGroupLeave struct {
	Status  string `json:"status"`
	Retcode int    `json:"retcode"`
	Message string `json:"message"`
	Wording string `json:"wording"`
	Echo    string `json:"echo"`
}

// 发送消息返回
type SendResp struct {
	RetMSG *message.GroupMessage
	Error  error
}

// 查陌生人返回
type ResponseGetStrangerInfo struct {
	Status  string       `json:"status"`
	Retcode int          `json:"retcode"`
	Data    StrangerInfo `json:"data"`
	Message string       `json:"message"`
	Wording string       `json:"wording"`
	Echo    string       `json:"echo"`
}

type StrangerInfo struct {
	UID        string `json:"uid"`
	QID        string `json:"qid"`
	Uin        int64  `json:"user_id"`
	Nick       string `json:"nickname"`
	Remark     string `json:"remark"`
	LongNick   string `json:"long_nick"`
	Sex        string `json:"sex"`
	RegTime    int64  `json:"reg_time"`
	Age        int    `json:"age"`
	QQLevel    int    `json:"qqLevel"`
	Level      int    `json:"level"`
	Status     int    `json:"status"`
	IsVip      bool   `json:"is_vip"`
	IsYearsVip bool   `json:"is_years_vip"`
	VipLevel   int    `json:"vip_level"`
	LoginDays  int    `json:"login_days"`
}

// 卡片消息
type CardMessage struct {
	App    string `json:"app"`
	Config struct {
		AutoSize any    `json:"autosize"` // string or int
		Width    int    `json:"width"`
		Height   int    `json:"height"`
		Forward  any    `json:"forward"` // bool or int
		CTime    int64  `json:"ctime"`
		Round    int    `json:"round"`
		Token    string `json:"token"`
		Type     string `json:"type"`
	} `json:"config"`
	// Extra struct {
	// 	AppType int   `json:"app_type"`
	// 	AppId   int64 `json:"appid"`
	// 	MsgSeq  int64 `json:"msg_seq"`
	// 	Uin     int64 `json:"uin"`
	// } `json:"extra"`
	Meta any `json:"meta"` // {
	// News struct {
	// 	Action         string `json:"action"`
	// 	AndroidPkgName string `json:"android_pkg_name"`
	// 	AppType        int    `json:"app_type"`
	// 	AppId          int64  `json:"appid"`
	// 	CTime          int64  `json:"ctime"`
	// 	Desc           string `json:"desc"`
	// 	JumpUrl        string `json:"jumpUrl"`
	// 	PreView        string `json:"preview"`
	// 	TagIcon        string `json:"tagIcon"`
	// 	SourceIcon     string `json:"source_icon"`
	// 	SourceUrl      string `json:"source_url"`
	// 	Tag            string `json:"tag"`
	// 	Title          string `json:"title"`
	// 	Uin            int64  `json:"uin"`
	// } `json:"news"`
	// Detail_1 struct {
	// 	AppId   string `json:"appid"`
	// 	AppType int    `json:"app_type"`
	// 	Title   string `json:"title"`
	// 	Desc    string `json:"desc"`
	// 	Icon    string `json:"icon"`
	// 	PreView string `json:"preview"`
	// 	Url     string `json:"url"`
	// 	Scene   int    `json:"scene"`
	// 	Host    struct {
	// 		Uin  int64  `json:"uin"`
	// 		Nick string `json:"nick"`
	// 	} `json:"host"`
	// } `json:"detail_1"`
	// Music struct {
	// 	Action         string `json:"action"`
	// 	AndroidPkgName string `json:"android_pkg_name"`
	// 	AppType        int    `json:"app_type"`
	// 	AppId          int64  `json:"appid"`
	// 	CTime          int64  `json:"ctime"`
	// 	Desc           string `json:"desc"`
	// 	JumpUrl        string `json:"jumpUrl"`
	// 	MusicUrl       string `json:"musicUrl"`
	// 	PreView        string `json:"preview"`
	// 	SourceMsgId    string `json:"source_msg_id"`
	// 	SourceIcon     string `json:"source_icon"`
	// 	SourceUrl      string `json:"source_url"`
	// 	Tag            string `json:"tag"`
	// 	Title          string `json:"title"`
	// 	Uin            int64  `json:"uin"`
	// } `json:"music"`
	// } `json:"meta"`
	Prompt string `json:"prompt"`
	Ver    string `json:"ver"`
	View   string `json:"view"`
}

type CardMessageMeta struct {
	Title string `json:"title"`
	Desc  string `json:"desc"`
	Tag   string `json:"tag"`
}

// 返回结构
type T struct {
	Status  string `json:"status"`
	RetCode int    `json:"retcode"`
	Message string `json:"message"`
	Wording string `json:"wording"`
	Data    any    `json:"data"`
}

// Window represents a fixed-window
type Window interface {
	// Start returns the start boundary
	Start() time.Time

	// Count returns the accumulated count
	Count() int64

	// AddCount increments the accumulated count by n
	AddCount(n int64)

	// Reset sets the state of the window with the given settings
	Reset(s time.Time, c int64)
}

type NewWindow func() Window

type LocalWindow struct {
	start int64
	count int64
}

type RateLimiter struct {
	size  time.Duration
	limit int64

	mu sync.Mutex

	curr Window
	prev Window
}

type QiDianAccountInfo struct {
	MasterUin  int64
	ExtName    string
	CreateTime int64

	bigDataReqAddrs   []string
	bigDataReqSession *bigDataSessionInfo
}

type handlerInfo struct {
	fun     func(i any, err error)
	dynamic bool
	params  network.RequestParams
}

func NewLocalWindow() *LocalWindow {
	return &LocalWindow{}
}

func (w *LocalWindow) Start() time.Time {
	return time.Unix(0, w.start)
}

func (w *LocalWindow) Count() int64 {
	return w.count
}

func (w *LocalWindow) AddCount(n int64) {
	w.count += n
}

func (w *LocalWindow) Reset(s time.Time, c int64) {
	w.start = s.UnixNano()
	w.count = c
}

func NewRateLimiter(size time.Duration, limit int64, newWindow NewWindow) *RateLimiter {
	currWin := newWindow()

	// The previous window is static (i.e. no add changes will happen within it),
	// so we always create it as an instance of LocalWindow
	prevWin := NewLocalWindow()

	return &RateLimiter{
		size:  size,
		limit: limit,
		curr:  currWin,
		prev:  prevWin,
	}

}

// Size returns the time duration of one window size
func (rl *RateLimiter) Size() time.Duration {
	return rl.size
}

// Limit returns the maximum events permitted to happen during one window size
func (rl *RateLimiter) Limit() int64 {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.limit
}

func (rl *RateLimiter) SetLimit(limit int64) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.limit = limit
}

// shorthand for GrantN(time.Now(), 1)
func (rl *RateLimiter) Grant() bool {
	return rl.GrantN(time.Now(), 1)
}

// reports whether n events may happen at time now
func (rl *RateLimiter) GrantN(now time.Time, n int64) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	rl.advance(now)

	elapsed := now.Sub(rl.curr.Start())
	weight := float64(rl.size-elapsed) / float64(rl.size)
	count := int64(weight*float64(rl.prev.Count())) + rl.curr.Count()

	if count+n > rl.limit {
		return false
	}

	rl.curr.AddCount(n)
	return true
}

// advance updates the current/previous windows resulting from the passage of time
func (rl *RateLimiter) advance(now time.Time) {
	// Calculate the start boundary of the expected current-window.
	newCurrStart := now.Truncate(rl.size)

	diffSize := newCurrStart.Sub(rl.curr.Start()) / rl.size
	if diffSize >= 1 {
		// The current-window is at least one-window-size behind the expected one.
		newPrevCount := int64(0)
		if diffSize == 1 {
			// The new previous-window will overlap with the old current-window,
			// so it inherits the count.
			newPrevCount = rl.curr.Count()
		}

		rl.prev.Reset(newCurrStart.Add(-rl.size), newPrevCount)
		// The new current-window always has zero count.
		rl.curr.Reset(newCurrStart, 0)
	}
}

func (h *handlerInfo) getParams() network.RequestParams {
	if h == nil {
		return nil
	}
	return h.params
}

var decoders = map[string]func(*QQClient, *network.Packet) (any, error){
	"wtlogin.login":                                decodeLoginResponse,
	"wtlogin.exchange_emp":                         decodeExchangeEmpResponse,
	"wtlogin.trans_emp":                            decodeTransEmpResponse,
	"StatSvc.register":                             decodeClientRegisterResponse,
	"StatSvc.ReqMSFOffline":                        decodeMSFOfflinePacket,
	"MessageSvc.PushNotify":                        decodeSvcNotify,
	"OnlinePush.ReqPush":                           decodeOnlinePushReqPacket,
	"OnlinePush.PbPushTransMsg":                    decodeOnlinePushTransPacket,
	"OnlinePush.SidTicketExpired":                  decodeSidExpiredPacket,
	"ConfigPushSvc.PushReq":                        decodePushReqPacket,
	"MessageSvc.PbGetMsg":                          decodeMessageSvcPacket,
	"MessageSvc.PushForceOffline":                  decodeForceOfflinePacket,
	"PbMessageSvc.PbMsgWithDraw":                   decodeMsgWithDrawResponse,
	"friendlist.getFriendGroupList":                decodeFriendGroupListResponse,
	"friendlist.delFriend":                         decodeFriendDeleteResponse,
	"friendlist.GetTroopListReqV2":                 decodeGroupListResponse,
	"friendlist.GetTroopMemberListReq":             decodeGroupMemberListResponse,
	"group_member_card.get_group_member_card_info": decodeGroupMemberInfoResponse,
	"LongConn.OffPicUp":                            decodeOffPicUpResponse,
	"ProfileService.Pb.ReqSystemMsgNew.Group":      decodeSystemMsgGroupPacket,
	"ProfileService.Pb.ReqSystemMsgNew.Friend":     decodeSystemMsgFriendPacket,
	"OidbSvc.0xd79":                                decodeWordSegmentation,
	"OidbSvc.0x990":                                decodeTranslateResponse,
	"SummaryCard.ReqSummaryCard":                   decodeSummaryCardResponse,
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type SendMsg struct {
	GroupCode  int64
	Message    *message.SendingMessage
	NewStr     string
	ResultChan chan SendResp
}

var sendMessageQueue = make(chan SendMsg, 128)
var messageQueue = make(chan []byte, 128)
var md5Int64Mapping = make(map[int64]string)
var md5Int64MappingLock sync.Mutex

type DynamicInt64 int64

type MessageContent struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data"`
}

type WebSocketMessage struct {
	PostType      string       `json:"post_type"`
	MetaEventType string       `json:"meta_event_type"`
	NoticeType    string       `json:"notice_type"`
	OperatorId    DynamicInt64 `json:"operator_id"`
	Status        struct {
		Online bool `json:"online"`
		Good   bool `json:"good"`
	} `json:"status"`
	RequestType string       `json:"request_type"`
	Comment     string       `json:"comment"`
	Flag        string       `json:"flag"`
	Interval    int          `json:"interval"`
	MessageType string       `json:"message_type"`
	Time        DynamicInt64 `json:"time"`
	SelfID      DynamicInt64 `json:"self_id"`
	SubType     string       `json:"sub_type"`
	Sender      struct {
		Age      int          `json:"age"`
		Card     string       `json:"card"`
		Nickname string       `json:"nickname"`
		Role     string       `json:"role"`
		UserID   DynamicInt64 `json:"user_id"`
	} `json:"sender"`
	UserID         DynamicInt64 `json:"user_id"`
	MessageID      DynamicInt64 `json:"message_id"`
	RealId         DynamicInt64 `json:"real_id"`
	GroupID        DynamicInt64 `json:"group_id"`
	MessageContent interface{}  `json:"message"`
	MessageSeq     DynamicInt64 `json:"message_seq"`
	RawMessage     any          `json:"raw_message"` // struct or string
	TargetID       DynamicInt64 `json:"target_id"`
	SenderID       DynamicInt64 `json:"sender_id"`
	CardOld        string       `json:"card_old"`
	CardNew        string       `json:"card_new"`
	Duration       int32        `json:"duration"`
	Title          string       `json:"title"`
	Times          int32        `json:"times"`
	Echo           string       `json:"echo,omitempty"`
	File           GroupFile    `json:"file"`
}

func (d *DynamicInt64) UnmarshalJSON(b []byte) error {
	var n int64
	var s string

	// 首先尝试解析为字符串
	if err := json.Unmarshal(b, &s); err == nil {
		// 如果解析成功，再尝试将字符串解析为int64
		n, err = strconv.ParseInt(s, 10, 64)
		if err != nil { // 如果是非数字字符串
			// 进行 md5 处理
			hashed := md5.Sum([]byte(s))
			hexString := hex.EncodeToString(hashed[:])

			// 去除字母并取前15位
			numericPart := strings.Map(func(r rune) rune {
				if unicode.IsDigit(r) {
					return r
				}
				return -1
			}, hexString)

			if len(numericPart) < 15 {
				return fmt.Errorf("哈希过短: %s", numericPart)
			}

			n, err = strconv.ParseInt(numericPart[:15], 10, 64)
			if err != nil {
				return err
			}

			// 更新映射，但限制锁的范围
			md5Int64MappingLock.Lock()
			md5Int64Mapping[n] = s
			md5Int64MappingLock.Unlock()
		}
	} else {
		// 如果字符串解析失败，再尝试解析为int64
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
	}

	*d = DynamicInt64(n)
	return nil
}

func (d DynamicInt64) ToInt64() int64 {
	return int64(d)
}

func (d DynamicInt64) ToString() string {
	return strconv.FormatInt(int64(d), 10)
}

func originalStringFromInt64(n int64) (string, bool) {
	md5Int64MappingLock.Lock()
	defer md5Int64MappingLock.Unlock()

	originalString, exists := md5Int64Mapping[n]
	return originalString, exists
}

// 假设 wsmsg.Message 是一个字符串，包含了形如 [CQ:at=xxxx] 的数据
func extractAtElements(str string) []*message.AtElement {
	re := regexp.MustCompile(`\[CQ:at=(\d+)\]`) // 正则表达式匹配[CQ:at=xxxx]其中xxxx是数字
	matches := re.FindAllStringSubmatch(str, -1)

	var elements []*message.AtElement
	for _, match := range matches {
		if len(match) == 2 {
			target, err := strconv.ParseInt(match[1], 10, 64) // 转化xxxx为int64
			if err != nil {
				continue // 如果转化失败，跳过此次循环
			}
			elements = append(elements, &message.AtElement{
				Target: target,
				// 如果有 Display 和 SubType 的信息，也可以在这里赋值
			})
		}
	}
	return elements
}

func parseEcho(echo string) (string, string) {
	parts := strings.SplitN(echo, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return echo, ""
}

func (c *QQClient) handleConnection(ws *websocket.Conn) {
	//处理ws消息并控制重连
	defer ws.Close()
	for {
		_, p, err := ws.ReadMessage()
		if err != nil {
			logger.Error(err)
			return
		}
		// 储存收到的消息
		messageQueue <- p
	}
}

func (c *QQClient) processMessage() {
	for {
		select {
		case p := <-messageQueue:
			go c.handleResponse(p)
		}
	}
}

func (c *QQClient) handleResponse(p []byte) {
	go logger.Tracef("Received message: %s", string(p))
	//初步解析
	var basicMsg BasicMessage
	err := json.Unmarshal(p, &basicMsg)
	if err != nil {
		//log.Println("Failed to parse basic message:", err)
		logger.Errorf("Failed to parse basic message: %v", err)
		return
	}
	if basicMsg.Echo != "" {
		respCh, isResponse := c.responseCh[basicMsg.Echo]
		if !isResponse {
			logger.Warnf("No response channel for echo: %s", basicMsg.Echo)
			return
		}
		var resp T
		if err = json.Unmarshal(p, &resp); err != nil {
			logger.Errorf("Failed to unmarshal get message response: %v", err)
			return
		}
		respCh <- &resp
	} else {
		// 解析消息
		var wsmsg WebSocketMessage
		err = json.Unmarshal(p, &wsmsg)
		if err != nil {
			//log.Println("Failed to parse message:", err)
			logger.Errorf("Failed to parse message: %v", err)
			return
		}
		// 存储 echo
		// c.currentEcho = wsmsg.Echo
		c.handleMessage(wsmsg)
	}
}

func (c *QQClient) createGroupMessage(wsmsg WebSocketMessage) *message.GroupMessage {
	return &message.GroupMessage{
		Id:        int32(wsmsg.MessageID),
		GroupCode: wsmsg.GroupID.ToInt64(),
		GroupName: "",
		Sender: &message.Sender{
			Uin:      wsmsg.Sender.UserID.ToInt64(),
			Nickname: wsmsg.Sender.Nickname,
			CardName: wsmsg.Sender.Card,
			IsFriend: false,
		},
		Time:           int32(wsmsg.Time),
		OriginalObject: nil,
	}
}

func (c *QQClient) createPrivateMessage(wsmsg WebSocketMessage) *message.PrivateMessage {
	return &message.PrivateMessage{
		Id:         int32(wsmsg.MessageID),
		InternalId: 0,
		Self:       c.Uin,
		Target:     wsmsg.Sender.UserID.ToInt64(),
		Time:       int32(wsmsg.Time),
		Sender: &message.Sender{
			Uin:      wsmsg.Sender.UserID.ToInt64(),
			Nickname: wsmsg.Sender.Nickname,
			CardName: "", // Private message might not have a Card
			IsFriend: wsmsg.SubType == "friend",
		},
	}
}

func (c *QQClient) handleGroupAdminNotice(wsmsg WebSocketMessage) (bool, error) {
	needSync := false
	var err error
	group := c.FindGroup(wsmsg.GroupID.ToInt64())
	member := group.FindMember(wsmsg.UserID.ToInt64())
	if member != nil {
		var permission MemberPermission
		if wsmsg.SubType == "set" {
			permission = Administrator
		} else if wsmsg.SubType == "unset" {
			permission = Member
		}
		// 群名片更新入库
		for _, m := range group.Members {
			if m.Uin == member.Uin {
				m.Permission = permission
				break
			}
		}
		c.GroupMemberPermissionChangedEvent.dispatch(c, &MemberPermissionChangedEvent{
			Group:         group,
			Member:        member,
			OldPermission: member.Permission,
			NewPermission: permission,
		})
	} else {
		needSync = true
		err = fmt.Errorf("member not found: %v", wsmsg.UserID)
	}
	return needSync, err
}

func (c *QQClient) handleGroupIncreaseNotice(wsmsg WebSocketMessage) (bool, error) {
	needSync := false
	var err error
	// 根据是否与自身有关来选择性刷新群列表
	if wsmsg.UserID.ToInt64() == c.Uin {
		needSync = true
		ret, err := c.GetGroupInfo(wsmsg.GroupID.ToInt64())
		if err == nil {
			skip := false
			for i, group := range c.GroupList {
				if group.Code == wsmsg.GroupID.ToInt64() {
					c.GroupList[i] = ret
					skip = true
					logger.Debugf("Group %d updated", wsmsg.GroupID.ToInt64())
					break
				}
			}
			if !skip {
				logger.Debugf("Group %d not found in GroupList, adding it", wsmsg.GroupID.ToInt64())
				c.GroupList = append(c.GroupList, ret)
			}
		} else {
			err = fmt.Errorf("failed to get group info: %v", wsmsg.GroupID)
		}
	} else {
		member, err := c.GetGroupMemberInfo(wsmsg.GroupID.ToInt64(), wsmsg.UserID.ToInt64())
		if err == nil && member.Group != nil {
			// 将新成员加入群聊
			member.Group.Members = append(member.Group.Members, member)
			// 事件入库
			c.GroupMemberJoinEvent.dispatch(c, &MemberJoinGroupEvent{
				Group:  member.Group,
				Member: member,
			})
		} else {
			needSync = true
			err = fmt.Errorf("member not found: %v", wsmsg.UserID)
		}
	}
	return needSync, err
}

func (c *QQClient) handleGroupDecreaseNotice(wsmsg WebSocketMessage) (bool, error) {
	needSync := false
	var err error
	if wsmsg.SubType == "kick_me" {
		group := c.FindGroup(wsmsg.GroupID.ToInt64())
		operator := group.FindMember(wsmsg.OperatorId.ToInt64())
		if operator != nil {
			for i, group := range c.GroupList {
				if group.Code == wsmsg.GroupID.ToInt64() {
					c.GroupList = append(c.GroupList[:i], c.GroupList[i+1:]...)
					logger.Debugf("Group %d removed from GroupList", wsmsg.GroupID.ToInt64())
					break
				}
			}
			c.GroupLeaveEvent.dispatch(c, &GroupLeaveEvent{
				Group:    group,
				Operator: operator,
			})
		} else {
			err = fmt.Errorf("operator not found: %v", wsmsg.OperatorId)
		}
	} else {
		member := c.FindGroup(wsmsg.GroupID.ToInt64()).FindMember(wsmsg.UserID.ToInt64())
		if member != nil {
			operator := member
			if wsmsg.OperatorId != wsmsg.UserID && wsmsg.OperatorId != 0 {
				operator = c.FindGroup(wsmsg.GroupID.ToInt64()).FindMember(wsmsg.OperatorId.ToInt64())
			}
			if operator != nil {
				// 将成员从群聊删除
				for i, m := range member.Group.Members {
					if m.Uin == member.Uin {
						member.Group.Members = append(member.Group.Members[:i], member.Group.Members[i+1:]...)
						break
					}
				}
				// 事件入库
				c.GroupMemberLeaveEvent.dispatch(c, &MemberLeaveGroupEvent{
					Group:    member.Group,
					Member:   member,
					Operator: operator,
				})
			} else {
				needSync = true
				err = fmt.Errorf("operator not found: %v", wsmsg.OperatorId)
			}
		} else {
			needSync = true
			err = fmt.Errorf("member not found: %v", wsmsg.UserID)
		}
	}
	return needSync, err
}

func (c *QQClient) handleFriendAddNotice(wsmsg WebSocketMessage) (bool, error) {
	needSync := false
	var err error
	friend, err := c.GetStrangerInfo(wsmsg.UserID.ToInt64())
	if err == nil {
		Friend := &FriendInfo{
			Uin:      wsmsg.UserID.ToInt64(),
			Nickname: friend.Nick,
			Remark:   friend.Remark,
			FaceId:   0,
		}
		c.FriendList = append(c.FriendList, Friend)
		c.NewFriendEvent.dispatch(c, &NewFriendEvent{Friend})
	} else {
		needSync = true
		err = fmt.Errorf("failed to get friend info: %v", err)
	}
	return needSync, err
}

func (c *QQClient) handleGroupCardNotice(wsmsg WebSocketMessage) (bool, error) {
	needSync := false
	var err error
	member, err := c.GetGroupMemberInfo(wsmsg.GroupID.ToInt64(), wsmsg.UserID.ToInt64())
	if err == nil && member.Group != nil {
		// 群成员名片更新
		for _, m := range member.Group.Members {
			if m.Uin == member.Uin {
				m.CardName = member.CardName
				break
			}
		}
		c.MemberCardUpdatedEvent.dispatch(c, &MemberCardUpdatedEvent{
			Group:   member.Group,
			OldCard: wsmsg.CardOld,
			Member:  member,
		})
	} else {
		needSync = true
		err = fmt.Errorf("member not found: %v", wsmsg.UserID)
	}
	return needSync, err
}

func (c *QQClient) handleGroupBanNotice(wsmsg WebSocketMessage) (bool, error) {
	needSync := false
	skip := false
	var err error
	var group *GroupInfo
	var member *GroupMemberInfo
	if wsmsg.UserID != 0 {
		group = c.FindGroup(wsmsg.GroupID.ToInt64())
		if group != nil {
			member = group.FindMember(wsmsg.UserID.ToInt64())
		} else {
			needSync = true
			err = fmt.Errorf("Failed to find group: %d", wsmsg.GroupID.ToInt64())
		}
	} else {
		group = c.FindGroup(wsmsg.GroupID.ToInt64())
		if group != nil {
			member = group.FindMember(c.Uin)
			if member != nil {
				if member.Permission == Member {
					wsmsg.UserID = DynamicInt64(c.Uin)
				} else {
					skip = true
				}
			}
		} else {
			needSync = true
			err = fmt.Errorf("Failed to find group: %d", wsmsg.GroupID.ToInt64())
		}
	}
	if member != nil {
		if !skip {
			if wsmsg.Duration > 0 {
				member.ShutUpTimestamp = time.Now().Add(time.Second * time.Duration(wsmsg.Duration)).Unix()
			} else {
				member.ShutUpTimestamp = 0
			}
			if wsmsg.Duration == -1 {
				wsmsg.Duration = 268435455
			}
			c.GroupMuteEvent.dispatch(c, &GroupMuteEvent{
				GroupCode:   wsmsg.GroupID.ToInt64(),
				OperatorUin: wsmsg.OperatorId.ToInt64(),
				TargetUin:   wsmsg.UserID.ToInt64(),
				Time:        wsmsg.Duration,
			})
		}
	} else {
		needSync = true
		err = fmt.Errorf("member not found: %v", wsmsg.UserID)
	}
	return needSync, err
}

func (c *QQClient) handleGroupRecallNotice(wsmsg WebSocketMessage) (bool, error) {
	isGroupMsg := wsmsg.MessageType != "private"
	if isGroupMsg {
		c.GroupMessageRecalledEvent.dispatch(c, &GroupMessageRecalledEvent{
			GroupCode:   wsmsg.GroupID.ToInt64(),
			OperatorUin: wsmsg.OperatorId.ToInt64(),
			AuthorUin:   wsmsg.UserID.ToInt64(),
			MessageId:   int32(wsmsg.MessageID),
			Time:        int32(wsmsg.Time),
		})
	} else {
		c.FriendMessageRecalledEvent.dispatch(c, &FriendMessageRecalledEvent{
			FriendUin: wsmsg.UserID.ToInt64(),
			MessageId: int32(wsmsg.MessageID),
			Time:      wsmsg.Time.ToInt64(),
		})
	}
	return false, nil
}

func (c *QQClient) handleNotifyNotice(wsmsg WebSocketMessage) (bool, error) {
	if wsmsg.SubType == "poke" {
		if wsmsg.GroupID != 0 {
			c.GroupNotifyEvent.dispatch(c, &GroupPokeNotifyEvent{
				GroupCode: wsmsg.GroupID.ToInt64(),
				Sender:    wsmsg.UserID.ToInt64(),
				Receiver:  wsmsg.TargetID.ToInt64(),
			})
		} else {
			c.FriendNotifyEvent.dispatch(c, &FriendPokeNotifyEvent{
				Sender:   wsmsg.UserID.ToInt64(),
				Receiver: wsmsg.TargetID.ToInt64(),
			})
		}
	} else if wsmsg.SubType == "title" {
		c.MemberSpecialTitleUpdatedEvent.dispatch(c, &MemberSpecialTitleUpdatedEvent{
			GroupCode: wsmsg.GroupID.ToInt64(),
			Uin:       wsmsg.UserID.ToInt64(),
			NewTitle:  wsmsg.Title,
		})
	} else if wsmsg.SubType == "profile_like" {
		logger.Infof("收到来自用户(%d)的%d次资料卡点赞", wsmsg.OperatorId, wsmsg.Times)
	}
	return false, nil
}

func (c *QQClient) handleRequestEvent(wsmsg WebSocketMessage) {
	const StrangerInfoErr = "Failed to get stranger info: %v"
	switch wsmsg.RequestType {
	case "friend":
		logger.Infof("收到 好友请求 事件: %s: %s", wsmsg.RequestType, wsmsg.SubType)
		friendRequest := NewFriendRequest{
			RequestId:     time.Now().UnixNano() / 1e6,
			Message:       wsmsg.Comment,
			RequesterUin:  wsmsg.UserID.ToInt64(),
			RequesterNick: "陌生人",
			Flag:          wsmsg.Flag,
			client:        c,
		}
		info, err := c.GetStrangerInfo(friendRequest.RequesterUin)
		if err == nil {
			friendRequest.RequesterNick = info.Nick
		} else {
			logger.Warnf(StrangerInfoErr, err)
		}
		c.NewFriendRequestEvent.dispatch(c, &friendRequest)
	case "group":
		if wsmsg.SubType == "add" {
			logger.Infof("收到 入群申请 事件: %s: %s", wsmsg.RequestType, wsmsg.SubType)
			groupRequest := UserJoinGroupRequest{
				RequestId:     time.Now().UnixNano() / 1e6,
				Message:       wsmsg.Comment,
				RequesterUin:  wsmsg.UserID.ToInt64(),
				RequesterNick: "陌生人",
				GroupCode:     wsmsg.GroupID.ToInt64(),
				GroupName:     "未知",
				Flag:          wsmsg.Flag,
				client:        c,
			}
			user, err := c.GetStrangerInfo(groupRequest.RequesterUin)
			if err == nil {
				groupRequest.RequesterNick = user.Nick
			} else {
				logger.Warnf(StrangerInfoErr, err)
			}
			groupInfo := c.FindGroupByUin(groupRequest.GroupCode)
			if groupInfo != nil {
				groupRequest.GroupName = groupInfo.Name
			}
			c.UserWantJoinGroupEvent.dispatch(c, &groupRequest)
		} else if wsmsg.SubType == "invite" {
			logger.Infof("收到 邀请入群 事件: %s: %s", wsmsg.RequestType, wsmsg.SubType)
			groupRequest := GroupInvitedRequest{
				RequestId:   time.Now().UnixNano() / 1e6,
				InvitorUin:  wsmsg.UserID.ToInt64(),
				InvitorNick: "陌生人",
				GroupCode:   wsmsg.GroupID.ToInt64(),
				GroupName:   "未知",
				Flag:        wsmsg.Flag,
				client:      c,
			}
			user, err := c.GetStrangerInfo(groupRequest.InvitorUin)
			if err == nil {
				groupRequest.InvitorNick = user.Nick
			} else {
				logger.Warnf(StrangerInfoErr, err)
			}
			groupInfo := c.FindGroupByUin(groupRequest.GroupCode)
			if groupInfo != nil {
				groupRequest.GroupName = groupInfo.Name
			}
			c.GroupInvitedEvent.dispatch(c, &groupRequest)
		}
	default:
		logger.Warnf("未知 请求事件 类型: %s", wsmsg.RequestType)
	}
}

func (c *QQClient) handleMetaEvent(wsmsg WebSocketMessage) {
	switch wsmsg.MetaEventType {
	case "lifecycle":
		c.Uin = int64(wsmsg.SelfID)
		c.Online.Store(true)
		c.alive = true
	case "heartbeat":
		c.Online.Store(wsmsg.Status.Online)
		c.alive = wsmsg.Status.Good
	default:
		logger.Warnf("未知 元事件 类型: %s", wsmsg.MetaEventType)
	}
}

func (c *QQClient) handleGroupEssence(wsmsg WebSocketMessage) (bool, error) {
	if wsmsg.SubType == "add" {
		//c.GroupEssenceMsgAddEvent.dispatch(c, &GroupEssenceMsgAddEvent{
		//	GroupCode: wsmsg.GroupID.ToInt64(),
		//	Sender:    wsmsg.UserID.ToInt64(),
		//	MessageId: int32(wsmsg.MessageID),
		//})
	} else if wsmsg.SubType == "delete" {
		//c.GroupEssenceMsgDelEvent.dispatch()
	}
	return false, nil
}

func (c *QQClient) handleGroupUploadNotice(wsmsg WebSocketMessage) (bool, error) {
	file := GroupFile{
		GroupCode: wsmsg.GroupID.ToInt64(),
		FileId:    wsmsg.File.AltFildId,
		FileName:  wsmsg.File.AltFileName,
		FileSize:  wsmsg.File.AltFileSize,
		BusId:     wsmsg.File.BusId,
	}
	if file.FileId != "" {
		file.FileUrl = c.GetFileUrl(file.GroupCode, file.FileId)
	}
	c.GroupUploadNotifyEvent.dispatch(c, &GroupUploadNotifyEvent{
		GroupCode: wsmsg.GroupID.ToInt64(),
		Sender:    wsmsg.UserID.ToInt64(),
		File:      file,
	})
	return false, nil
}

func (c *QQClient) GetMsg(msgId int32) (interface{}, error) {
	var resp WebSocketMessage
	data, err := c.SendApi("get_msg", map[string]any{"message_id": msgId})
	if err != nil {
		return nil, err
	}
	t, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(t, &resp); err != nil {
		return nil, err
	}
	if resp.MessageType == "group" {
		wMsg := c.createGroupMessage(resp)
		gMsg := c.ChatMsgHandler(resp, wMsg).(*message.GroupMessage)
		return gMsg, nil
	} else {
		wMsg := c.createPrivateMessage(resp)
		pMsg := c.ChatMsgHandler(resp, wMsg).(*message.PrivateMessage)
		return pMsg, nil
	}
}

func (c *QQClient) GetFileUrl(groupCode int64, fileId string) string {
	data, err := c.SendApi("get_group_file_url", map[string]any{
		"group_id": groupCode,
		"file_id":  fileId,
	})
	if err != nil {
		logger.Errorf("获取群文件链接失败: %v", err)
		return ""
	}
	t, err := json.Marshal(data)
	if err != nil {
		logger.Errorf("解析群文件链接失败: %v", err)
	}
	type Resp struct {
		Url string `json:"url"`
	}
	var resp Resp
	err = json.Unmarshal(t, &resp)
	if err != nil {
		logger.Errorf("解析群文件链接失败: %v", err)
	}
	return resp.Url
}

func (c *QQClient) handleNoticeEvent(wsmsg WebSocketMessage) {
	wsNoticeHeaders := map[string]func(wsmsg WebSocketMessage) (bool, error){
		"group_admin":    c.handleGroupAdminNotice,
		"group_increase": c.handleGroupIncreaseNotice,
		"group_decrease": c.handleGroupDecreaseNotice,
		"friend_add":     c.handleFriendAddNotice,
		"group_card":     c.handleGroupCardNotice,
		"group_ban":      c.handleGroupBanNotice,
		"notify":         c.handleNotifyNotice,
		"group_recall":   c.handleGroupRecallNotice,
		"essence":        c.handleGroupEssence,
		"group_upload":   c.handleGroupUploadNotice,
	}
	needSync := false
	var err error
	if handler, exists := wsNoticeHeaders[wsmsg.NoticeType]; exists {
		logger.Infof("收到 通知事件 消息：%s: %s", wsmsg.NoticeType, wsmsg.SubType)
		needSync, err = handler(wsmsg)
		if err != nil {
			logger.Warnf(err.Error())
		}
	} else {
		logger.Warnf("未知 通知事件 类型: %s", wsmsg.NoticeType)
	}
	if needSync {
		if wsmsg.NoticeType == "friend_add" {
			c.ReloadFriendList()
		}
		if g := c.FindGroup(wsmsg.GroupID.ToInt64()); g != nil {
			c.SyncGroupMembers(wsmsg.GroupID)
		}
	}
}

func (c *QQClient) handleMessage(wsmsg WebSocketMessage) {
	switch wsmsg.MessageType {
	case "group":
		wsMsg := c.createGroupMessage(wsmsg)
		gMsg := c.ChatMsgHandler(wsmsg, wsMsg).(*message.GroupMessage)
		c.waitDataAndDispatch(gMsg)
	case "private":
		wsMsg := c.createPrivateMessage(wsmsg)
		if wsmsg.PostType == "message_sent" {
			wsMsg.Target = int64(wsmsg.TargetID)
		}
		pMsg := c.ChatMsgHandler(wsmsg, wsMsg).(*message.PrivateMessage)
		selfIDStr := strconv.FormatInt(int64(wsmsg.SelfID), 10)
		if selfIDStr == strconv.FormatInt(int64(wsmsg.Sender.UserID), 10) {
			c.SelfPrivateMessageEvent.dispatch(c, pMsg)
		} else {
			c.PrivateMessageEvent.dispatch(c, pMsg)
		}
		c.OutputReceivingMessage(pMsg)
	default:
		switch wsmsg.PostType {
		case "meta_event":
			c.handleMetaEvent(wsmsg)
		case "notice":
			c.handleNoticeEvent(wsmsg)
		case "request":
			c.handleRequestEvent(wsmsg)
		default:
			logger.Warnf("未知 上报 类型: %s", wsmsg.NoticeType)
		}
	}
}

func handleMsgContent(MessageContent string, elements []message.IMessageElement) {
	// 替换字符串中的"\/"为"/"
	MessageContent = strings.Replace(MessageContent, "\\/", "/", -1)
	// 使用extractAtElements函数从wsmsg.Message中提取At元素
	atElements := extractAtElements(MessageContent)
	// 将提取的At元素和文本元素都添加到g.Elements
	elements = append(elements, &message.TextElement{Content: MessageContent})
	for _, elem := range atElements {
		elements = append(elements, elem)
	}
}

type miraiMessage interface {
	GetSender() *message.Sender
	GetTime() int32
	GetElements() []message.IMessageElement
}

// 包装 GroupMessage
type GroupMessageWrapper struct {
	*message.GroupMessage
}

func (g *GroupMessageWrapper) GetSender() *message.Sender {
	return g.Sender
}

func (g *GroupMessageWrapper) GetTime() int32 {
	return g.Time
}

func (g *GroupMessageWrapper) GetElements() []message.IMessageElement {
	return g.Elements
}

// 包装 PrivateMessage
type PrivateMessageWrapper struct {
	*message.PrivateMessage
}

func (p *PrivateMessageWrapper) GetSender() *message.Sender {
	return p.Sender
}

func (p *PrivateMessageWrapper) GetTime() int32 {
	return p.Time
}

func (p *PrivateMessageWrapper) GetElements() []message.IMessageElement {
	return p.Elements
}

// 统一处理函数
func createReplyElement(msg miraiMessage, replySeq int, groupID int64) *message.ReplyElement {
	return &message.ReplyElement{
		ReplySeq: int32(replySeq),
		Sender:   msg.GetSender().Uin,
		GroupID:  groupID,
		Time:     msg.GetTime(),
		Elements: msg.GetElements(),
	}
}

func parseTextElement(contentMap map[string]interface{}, elements *[]message.IMessageElement) {
	text, ok := contentMap["data"].(map[string]interface{})["text"].(string)
	if ok {
		// 替换字符串中的"\/"为"/"
		text = strings.Replace(text, "\\/", "/", -1)
		*elements = append(*elements, &message.TextElement{Content: text})
	}
}

func parseAtElement(contentMap map[string]interface{}, elements *[]message.IMessageElement, miraiMsg any, isGroupMsg bool) {
	if data, ok := contentMap["data"].(map[string]interface{}); ok {
		var qq int64
		var senderName string
		if qqData, ok := data["qq"].(string); ok {
			if qqData != "all" {
				var err error
				qq, err = strconv.ParseInt(qqData, 10, 64)
				if err != nil {
					logger.Errorf("Failed to parse qq: %v", err)
					return
				}
			} else {
				qq = 0
			}
		} else if qqData, ok := data["qq"].(int64); ok {
			qq = qqData
		}
		if isGroupMsg {
			senderName = miraiMsg.(*message.GroupMessage).Sender.DisplayName()
		} else {
			senderName = miraiMsg.(*message.PrivateMessage).Sender.DisplayName()
		}
		*elements = append(*elements, &message.AtElement{Target: qq, Display: senderName})
	}
}

func parseFaceElement(contentMap map[string]interface{}, elements *[]message.IMessageElement) {
	faceID := 0
	switch contentMap["data"].(map[string]interface{})["id"].(type) {
	case string:
		faceID, _ = strconv.Atoi(contentMap["data"].(map[string]interface{})["id"].(string))
	case float64:
		faceID = int(contentMap["data"].(map[string]interface{})["id"].(float64))
	}
	*elements = append(*elements, &message.FaceElement{Index: int32(faceID)})
}

func parseMFaceElement(contentMap map[string]interface{}, elements *[]message.IMessageElement) {
	if mface, ok := contentMap["data"].(map[string]interface{}); ok {
		parseTabId := func(rawId any) int32 {
			switch v := rawId.(type) {
			case string:
				if id, err := strconv.Atoi(v); err == nil {
					return int32(id)
				} else {
					return 0
				}
			case float64:
				return int32(v)
			default:
				return 0
			}
		}
		var tabId int32
		if rawTabId, exists := mface["emoji_package_id"]; exists {
			tabId = parseTabId(rawTabId)
		}
		element := &message.MarketFaceElement{
			Name:       "",
			FaceId:     []byte(mface["emoji_id"].(string)),
			TabId:      tabId,
			MediaType:  2,
			EncryptKey: []byte(mface["key"].(string)),
		}
		if summary, ok := mface["summary"].(string); ok {
			element.Name = summary
		}
		*elements = append(*elements, element)
	}
}

func parseImageElement(contentMap map[string]interface{}, elements *[]message.IMessageElement, isGroupMsg bool) {
	image, ok := contentMap["data"].(map[string]interface{})
	if ok {
		size := 0
		if tmp, ok := image["file_size"].(string); ok {
			size, _ = strconv.Atoi(tmp)
		}
		if contentMap["subType"] == nil {
			if isGroupMsg {
				element := &message.GroupImageElement{
					Size: int32(size),
					Name: image["file"].(string),
					Url:  image["url"].(string),
				}
				*elements = append(*elements, element)
			} else {
				element := &message.FriendImageElement{
					Size: int32(size),
					Url:  image["url"].(string),
				}
				*elements = append(*elements, element)
			}
		} else {
			if contentMap["subType"].(float64) == 1.0 {
				element := &message.MarketFaceElement{
					Name:       "[图片表情]",
					FaceId:     []byte(image["file"].(string)),
					SubType:    3,
					TabId:      0,
					MediaType:  2,
					EncryptKey: []byte("0"),
				}
				*elements = append(*elements, element)
			} else {
				if isGroupMsg {
					element := &message.GroupImageElement{
						Size: int32(size),
						Url:  image["url"].(string),
					}
					*elements = append(*elements, element)
				} else {
					element := &message.FriendImageElement{
						Size: int32(size),
						Url:  image["url"].(string),
					}
					*elements = append(*elements, element)
				}
			}
		}
	}
}

func parseReplyElement(contentMap map[string]interface{}, elements *[]message.IMessageElement, miraiMsg any, isGroupMsg bool) {
	replySeq := 0
	if replyID, ok := contentMap["data"].(map[string]interface{})["id"].(string); ok {
		replySeq, _ = strconv.Atoi(replyID)
	} else if replyID, ok := contentMap["data"].(map[string]interface{})["id"].(float64); ok {
		replySeq = int(replyID)
	}
	var Msg miraiMessage
	GroupCode := int64(0)
	if isGroupMsg {
		GroupCode = miraiMsg.(*message.GroupMessage).GroupCode
		Msg = &GroupMessageWrapper{miraiMsg.(*message.GroupMessage)}
	} else {
		Msg = &PrivateMessageWrapper{miraiMsg.(*message.PrivateMessage)}
	}
	element := createReplyElement(Msg, replySeq, GroupCode)
	*elements = append(*elements, element)
}

func parseRecordElement(contentMap map[string]interface{}, elements *[]message.IMessageElement) {
	record, ok := contentMap["data"].(map[string]interface{})
	if ok {
		fileSize := 0
		filePath := ""
		if size, ok := record["file_size"].(string); ok {
			fileSize, _ = strconv.Atoi(size)
		}
		if path, ok := record["path"].(string); ok {
			filePath = path
		}
		element := &message.VoiceElement{
			Name: record["file"].(string),
			Url:  filePath,
			Size: int32(fileSize),
		}
		*elements = append(*elements, element)
	}
}

func parseJsonContent(meta map[string]interface{}, elements *[]message.IMessageElement) {
	var nowNum int
	needDec, ok := true, false
	title, text := "", "[卡片]["
	var metaData map[string]interface{}
	var metaMap = CardMessageMeta{
		Title: "未知",
		Desc:  "未知",
		Tag:   "未知",
	}
	metaArr := [6]string{"news", "music", "detail_1", "contact", "video", "detail"}
	for i, v := range metaArr {
		if metaData, ok = meta[v].(map[string]interface{}); ok {
			nowNum = i
			break
		}
	}
	switch nowNum {
	case 0:
	case 1:
	case 2:
		if host, ok := metaData["host"].(map[string]interface{}); ok {
			metaMap.Tag = host["nick"].(string)
		}
	case 3:
	case 4:
		needDec = false
		metaMap.Desc = metaData["title"].(string)
		metaMap.Tag = "视频"
		if title, ok = metaData["nickname"].(string); ok {
			metaMap.Title = title
		}
	case 5:
		if channel, ok := metaData["channel_info"].(map[string]interface{}); ok {
			needDec = false
			feedTitle := "未知"
			if feedTitle, ok = metaData["feed"].(map[string]interface{})["title"].(map[string]interface{})["contents"].([]interface{})[0].(map[string]interface{})["text_content"].(map[string]interface{})["text"].(string); !ok {
				logger.Warnf("Failed to parse feed title")
			}
			metaMap.Title = channel["channel_name"].(string)
			metaMap.Desc = feedTitle
			metaMap.Tag = "频道"
		}
	default:
		logger.Warnf("Unknown meta type: %v", meta)
	}
	if needDec {
		b, _ := json.Marshal(metaData)
		_ = json.Unmarshal(b, &metaMap)
	}
	text += metaMap.Tag + "][" + metaMap.Title + "][" + metaMap.Desc + "]"
	*elements = append(*elements, &message.LightAppElement{Content: text})
}

func parseJsonElement(contentMap map[string]interface{}, elements *[]message.IMessageElement) {
	if card, ok := contentMap["data"].(map[string]interface{}); ok {
		switch card["data"].(type) {
		case string:
			var j CardMessage
			err := json.Unmarshal([]byte(card["data"].(string)), &j)
			if err != nil {
				logger.Errorf("Failed to parse card message: %v", err)
				return
			}
			if meta, ok := j.Meta.(map[string]interface{}); ok {
				parseJsonContent(meta, elements)
			}
		default:
			logger.Errorf("Unknown card message type: %v", card)
		}
	}
}

func parseFileElement(contentMap map[string]interface{}, elements *[]message.IMessageElement, isGroupMsg bool) {
	file, ok := contentMap["data"].(map[string]interface{})
	if ok {
		var fileSize int64
		switch file["file_size"].(type) {
		case string:
			fileSize, _ = strconv.ParseInt(file["file_size"].(string), 10, 64)
		case int64:
			fileSize = file["file_size"].(int64)
		}
		if isGroupMsg {
			*elements = append(*elements, &message.GroupFileElement{
				Name: file["file"].(string),
				Size: fileSize,
				Id:   file["file_id"].(string),
			})
		} else {
			*elements = append(*elements, &message.FriendFileElement{
				Name: file["file"].(string),
				Size: fileSize,
				Path: file["path"].(string),
			})
		}
	}
}

func parseForwardElement(contentMap map[string]interface{}, elements *[]message.IMessageElement) {
	forward, ok := contentMap["data"].(map[string]interface{})
	if ok {
		text := "[合并转发]" // + forward["id"].(string)
		element := &message.ForwardElement{
			Content: text,
			ResId:   forward["id"].(string),
		}
		*elements = append(*elements, element)
	}
}

func parseVideoElement(contentMap map[string]interface{}, elements *[]message.IMessageElement) {
	video, ok := contentMap["data"].(map[string]interface{})
	if ok {
		fileSize := 0
		var fileId []byte
		if video["file_size"] != nil {
			fileSize, _ = strconv.Atoi(video["file_size"].(string))
		}
		if video["file_id"] != nil {
			fileId = []byte(video["file_id"].(string))
		}
		element := &message.ShortVideoElement{
			Name: video["file"].(string),
			Uuid: fileId,
			Size: int32(fileSize),
		}
		if video["url"] != nil {
			element.Url = video["url"].(string)
		}
		*elements = append(*elements, element)
	}
}

func parseMarkdownElement(elements *[]message.IMessageElement) {
	text := "[Markdown]"
	*elements = append(*elements, &message.TextElement{Content: text})
}

func handleMixMsg(contentArray []interface{}, miraiMsg any, isGroupMsg bool) []message.IMessageElement {
	var elements []message.IMessageElement
	for _, contentInterface := range contentArray {
		contentMap, ok := contentInterface.(map[string]interface{})
		if !ok {
			continue
		}
		contentType, ok := contentMap["type"].(string)
		if !ok {
			continue
		}
		switch contentType {
		case "text":
			parseTextElement(contentMap, &elements)
		case "at":
			parseAtElement(contentMap, &elements, miraiMsg, isGroupMsg)
		case "face":
			parseFaceElement(contentMap, &elements)
		case "mface":
			parseMFaceElement(contentMap, &elements)
		case "image":
			parseImageElement(contentMap, &elements, isGroupMsg)
		case "reply":
			parseReplyElement(contentMap, &elements, miraiMsg, isGroupMsg)
		case "record":
			parseRecordElement(contentMap, &elements)
		case "json":
			parseJsonElement(contentMap, &elements)
		case "file":
			parseFileElement(contentMap, &elements, isGroupMsg)
		case "forward":
			parseForwardElement(contentMap, &elements)
		case "video":
			parseVideoElement(contentMap, &elements)
		case "markdown":
			parseMarkdownElement(&elements)
		default:
			logger.Warnf("未知 内容 类型: %s", contentType)
		}
	}
	return elements
}

func (c *QQClient) ChatMsgHandler(wsmsg WebSocketMessage, miraiMsg any) any {
	isGroupMsg := wsmsg.MessageType != "private"
	var elements []message.IMessageElement
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("Recovered from panic in handleMixMsg: %v. \nStack trace:%s", r, debug.Stack())
		}
	}()
	switch wsmsg.MessageContent.(type) {
	case string:
		handleMsgContent(wsmsg.MessageContent.(string), elements)
	case []interface{}:
		elements = handleMixMsg(wsmsg.MessageContent.([]interface{}), miraiMsg, isGroupMsg)
	default:
		logger.Warnf("未知 消息内容 类型: %v", wsmsg.MessageContent)
		return nil
	}

	if isGroupMsg {
		gMsg := miraiMsg.(*message.GroupMessage)
		gMsg.Elements = elements
		return gMsg
	} else {
		pMsg := miraiMsg.(*message.PrivateMessage)
		pMsg.Elements = elements
		return pMsg
	}
}

func (c *QQClient) FindMemberByUin(groupCode int64, uin int64) (*GroupMemberInfo, error) {
	group := c.FindGroupByUin(groupCode)
	if group == nil {
		return nil, errors.New("group not found")
	}
	for _, member := range group.Members {
		if member.Uin == uin {
			return member, nil
		}
	}
	return nil, errors.New("member not found")
}

func (c *QQClient) GetGroupMemberInfo(groupCode int64, memberUin int64) (*GroupMemberInfo, error) {
	params := map[string]any{
		"group_id": groupCode,
		"user_id":  memberUin,
		"no_cache": false,
	}
	data, err := c.SendApi("get_group_member_info", params)
	if err != nil {
		return nil, err
	}
	bytes, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var member MemberData
	var per MemberPermission
	var sex byte
	if err = json.Unmarshal(bytes, &member); err != nil {
		return nil, err
	}
	switch member.Role {
	case "owner":
		per = Owner
	case "admin":
		per = Administrator
	case "member":
		per = Member
	}
	switch member.Sex {
	case "male":
		sex = 2
	case "female":
		sex = 1
	case "unknown":
		sex = 0
	}
	ret := GroupMemberInfo{
		Group:           c.FindGroupByUin(groupCode),
		Uin:             member.UserID,
		Nickname:        member.Nickname,
		CardName:        member.Card,
		JoinTime:        member.JoinTime,
		LastSpeakTime:   member.LastSentTime,
		SpecialTitle:    member.Title,
		ShutUpTimestamp: member.ShutUpTimestamp,
		Permission:      per,
		Level:           uint16(member.QQLevel),
		Gender:          sex,
	}
	return &ret, nil
}

func (c *QQClient) DownloadFile(url, base64, name string, headers []string) (string, error) {
	if url == "" && base64 == "" {
		return "", errors.New("url 或 base64 参数不能为空")
	}
	params := map[string]any{
		"url":    url,
		"base64": base64,
		"name":   name,
	}
	if len(headers) > 0 {
		params["headers"] = headers
	}

	rsp, err := c.SendApi("download_file", params)
	if err != nil {
		return "", fmt.Errorf("API请求失败: %w", err)
	}

	if fileName, ok := rsp.(map[string]any)["file"].(string); ok {
		return fileName, nil
	}

	return "", errors.New("无效的API响应结构")
}

func (c *QQClient) SendApi(api string, params map[string]any, expTime ...float64) (any, error) {
	// 设置超时时间
	var timeout float64
	if len(expTime) > 0 {
		timeout = expTime[0]
	} else {
		timeout = 10
	}
	// 构建请求
	echo := generateEcho(api)
	req := map[string]any{
		"action": api,
		"params": params,
		"echo":   echo,
	}
	// 发送请求
	data, _ := json.Marshal(req)
	ch := make(chan *T)
	c.responseCh[echo] = ch
	c.sendToWebSocketClient(c.ws, data)
	// 等待返回
	select {
	case res := <-ch:
		if res.Status == "ok" {
			return res.Data, nil
		} else {
			return nil, errors.New(res.Message)
		}
	case <-time.After(time.Second * time.Duration(timeout)):
		return nil, errors.New(api + " timeout")
	}
}

func (c *QQClient) OutputReceivingMessage(Msg interface{}) {
	mode := false
	var content []message.IMessageElement
	if msg, ok := Msg.(*message.GroupMessage); ok {
		content = msg.GetElements()
	} else if msg, ok := Msg.(*message.PrivateMessage); ok {
		content = msg.GetElements()
		mode = true
	}
	var tmpText string
	for _, elem := range content {
		if text, ok := elem.(*message.TextElement); ok {
			if len(text.Content) > 75 {
				tmpText += text.Content[:75] + "..."
			} else {
				tmpText += text.Content
			}
		} else if _, ok := elem.(*message.AtElement); ok {
			tmpText += "[艾特]"
		} else if _, ok := elem.(*message.FaceElement); ok {
			tmpText += "[表情]"
		} else if _, ok := elem.(*message.MarketFaceElement); ok {
			tmpText += "[表情]"
		} else if _, ok := elem.(*message.GroupImageElement); ok {
			tmpText += "[图片]"
		} else if _, ok := elem.(*message.FriendImageElement); ok {
			tmpText += "[图片]"
		} else if _, ok := elem.(*message.ReplyElement); ok {
			tmpText += "[引用]"
		} else if _, ok := elem.(*message.VoiceElement); ok {
			tmpText += "[语音]"
		} else if e, ok := elem.(*message.ForwardElement); ok {
			tmpText += e.Content
		} else if e, ok := elem.(*message.LightAppElement); ok {
			tmpText += e.Content
		} else if _, ok := elem.(*message.ShortVideoElement); ok {
			tmpText += "[视频]"
		} else if _, ok := elem.(*message.GroupFileElement); ok {
			tmpText += "[文件]"
		} else if _, ok := elem.(*message.FriendFileElement); ok {
			tmpText += "[文件]"
		}
	}
	if !mode {
		name := Msg.(*message.GroupMessage).Sender.CardName
		if name == "" {
			name = Msg.(*message.GroupMessage).Sender.Nickname
		}
		logger.Infof("收到群 %s(%d) 内 %s(%d) 的消息: %s (%d)", Msg.(*message.GroupMessage).GroupName, Msg.(*message.GroupMessage).GroupCode, name, Msg.(*message.GroupMessage).Sender.Uin, tmpText, Msg.(*message.GroupMessage).Id)
	} else {
		logger.Infof("收到 %s(%d) 的私聊消息: %s (%d)", Msg.(*message.PrivateMessage).Sender.Nickname, Msg.(*message.PrivateMessage).Sender.Uin, tmpText, Msg.(*message.PrivateMessage).Id)
	}
}

func (c *QQClient) waitDataAndDispatch(g *message.GroupMessage) {
	if group := c.FindGroup(g.GroupCode); group != nil {
		c.SetMsgGroupNames(g)
	}
	if friend := c.FindFriend(g.Sender.Uin); friend != nil {
		c.SetFriend(g)
	}
	// 使用 dispatch 方法
	c.GroupMessageEvent.dispatch(c, g)
	c.OutputReceivingMessage(g)
}

func (c *QQClient) SetFriend(g *message.GroupMessage) {
	g.Sender.IsFriend = c.FindFriend(g.Sender.Uin) != nil
}

func (c *QQClient) SetMsgGroupNames(g *message.GroupMessage) {
	group := c.FindGroupByUin(g.GroupCode)
	if group != nil {
		g.GroupName = group.Name
	} else {
		logger.Warnf("Not found group name: %d", g.GroupCode)
	}
}

func (c *QQClient) SyncGroupMembers(groupID DynamicInt64) {
	group := c.FindGroupByUin(groupID.ToInt64())
	if group != nil {
		var err error
		group.Members, err = c.GetGroupMembers(group)
		if err != nil {
			logger.Errorf("Failed to update group members: %v", err)
		}
	} else {
		logger.Warnf("Not found group: %d", groupID.ToInt64())
	}
}

var (
	registerOnce sync.Once
	wsUpgrader   = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
)

func (c *QQClient) StartWebSocketServer() {
	wsMode := config.GlobalConfig.GetString("websocket.mode")
	if wsMode == "" {
		wsMode = "ws-server"
	}

	if wsMode == "ws-server" {
		registerOnce.Do(func() {
			http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
				ws, err := wsUpgrader.Upgrade(w, r, nil)
				if err != nil {
					logger.Error(err)
					return
				}

				// 打印客户端headers
				for name, values := range r.Header {
					for _, value := range values {
						logger.WithField(name, value).Debug()
					}
				}

				c.wsInit(ws, wsMode)
			})

			wsAddr := config.GlobalConfig.GetString("websocket.ws-server")
			if wsAddr == "" {
				wsAddr = "0.0.0.0:15630"
			}

			logger.WithField("force", true).Printf("WebSocket server started on ws://%s/ws", wsAddr)
			go func() {
				logger.Fatal(http.ListenAndServe(wsAddr, nil))
			}()
		})
	} else {
		c.disconnectChan = make(chan bool)
		go c.reverseConn(wsMode)
	}
}

func (c *QQClient) reverseConn(mode string) {
	var err error
	var ws *websocket.Conn
	// 使用反向链接
	wsAddr := config.GlobalConfig.GetString("websocket.ws-reverse")
	if wsAddr == "" {
		wsAddr = "ws://localhost:3001"
	}
	header := http.Header{}
	header.Set("Authorization", "Bearer "+config.GlobalConfig.GetString("websocket.token"))
	logger.WithField("force", true).Printf("WebSocket reverse started on %s", wsAddr)
	for {
		ws, _, err = websocket.DefaultDialer.Dial(wsAddr, header)
		if err != nil {
			logger.Debug("dial:", err)
			time.Sleep(time.Second * 5)
			continue
		}
		c.wsInit(ws, mode)
		<-c.disconnectChan
		c.alive = false
		c.Online.Store(false)
		logger.Debug("Received disconnect signal, attempting to reconnect...")
	}
}

func (c *QQClient) wsInit(ws *websocket.Conn, mode string) {
	logger.Info("有新的ws连接了!!")
	//初始化变量
	c.ws = ws
	c.responseCh = make(map[string]chan *T)
	// 初始化流控
	c.limiterMessageSend = NewRateLimiter(time.Second, 5, func() Window {
		return NewLocalWindow()
	})
	// 刷新信息并启动流控和消息处理
	go c.RefreshList()
	go c.SendLimit()
	go c.processMessage()
	if mode == "ws-server" {
		c.handleConnection(ws)
	} else {
		go func() {
			c.handleConnection(ws)
			c.disconnectChan <- true // 连接断开时发送信号
		}()
	}
}

// RefreshList 刷新联系人
func (c *QQClient) RefreshList() {
	reloadDelay := config.GlobalConfig.GetBool("reloadDelay.enable")
	if reloadDelay {
		//logger.Info("enabled reload delay")
		var Delay time.Duration
		Delay = config.GlobalConfig.GetDuration("reloadDelay.time")
		logger.Infof("延迟加载时间: %ss", strconv.FormatFloat(Delay.Seconds(), 'f', 0, 64))
		time.Sleep(Delay)
	}
	//logger.Info("start reload friends list")
	err := c.ReloadFriendList()
	if err != nil {
		logger.WithError(err).Error("unable to load friends list")
	}
	logger.Infof("已加载 %d 个好友", len(c.FriendList))
	//logger.Info("start reload groups list")
	err = c.ReloadGroupList()
	if err != nil {
		logger.WithError(err).Error("unable to load groups list")
	}
	logger.Infof("已加载 %d 个群组", len(c.GroupList))
	//logger.Info("start reload group members list")
	memberCount, err := c.ReloadGroupMembers()
	if err != nil {
		logger.WithError(err).Error("unable to load group members list")
	} else {
		if memberCount > 0 {
			logger.Infof("已加载 %d 个群成员", memberCount)
		} else {
			logger.Info("群成员加载失败")
		}
	}
}

func (c *QQClient) ReloadGroupMembers() (int, error) {
	var err error
	memberCount := 0
	for _, group := range c.GroupList {
		group.Members = nil
		group.Members, err = c.GetGroupMembers(group)
		memberCount += len(group.Members)
		logger.Debugf("群[%d]加载成员[%d]个\n", group.Code, len(group.Members))
		if err != nil {
			return memberCount, err
		}
	}
	return memberCount, nil
}

func (c *QQClient) SendGroupMessage(groupCode int64, m *message.SendingMessage, newstr string) SendResp {
	resultChan := make(chan SendResp)
	sendMsg := SendMsg{
		GroupCode:  groupCode,
		Message:    m,
		NewStr:     newstr,
		ResultChan: resultChan,
	}
	sendMessageQueue <- sendMsg
	return <-resultChan
}

func (c *QQClient) sendToWebSocketClient(ws *websocket.Conn, message []byte) {
	if ws != nil {
		c.wsWriteLock.Lock()
		if err := ws.WriteMessage(websocket.TextMessage, message); err != nil {
			logger.Errorf("Failed to send message to WebSocket client: %v", err)
		}
		c.wsWriteLock.Unlock()
	}
}

// NewClient create new qq client
func NewClient(uin int64, password string) *QQClient {
	return NewClientMd5(uin, md5.Sum([]byte(password)))
}

func NewClientEmpty() *QQClient {
	return NewClientMd5(0, [16]byte{})
}

func NewClientMd5(uin int64, passwordMd5 [16]byte) *QQClient {
	cli := &QQClient{
		Uin:         uin,
		PasswordMd5: passwordMd5,
		AllowSlider: true,
		TCP:         &network.TCPClient{},
		sig: &auth.SigInfo{
			OutPacketSessionID: []byte{0x02, 0xB0, 0x5B, 0x8B},
		},
		msgSvcCache:     utils.NewCache[unit](time.Second * 15),
		transCache:      utils.NewCache[unit](time.Second * 15),
		onlinePushCache: utils.NewCache[unit](time.Second * 15),
		alive:           true,
		highwaySession:  new(highway.Session),
	}

	go cli.StartWebSocketServer() // 异步启动WebSocket服务端

	cli.transport = &network.Transport{Sig: cli.sig}
	cli.oicq = oicq.NewCodec(cli.Uin)
	{ // init atomic values
		cli.SequenceId.Store(int32(rand.Intn(100000)))
		cli.requestPacketRequestID.Store(1921334513)
		cli.groupSeq.Store(int32(rand.Intn(20000)))
		cli.friendSeq.Store(22911)
		cli.highwayApplyUpSeq.Store(77918)
	}
	cli.highwaySession.Uin = strconv.FormatInt(cli.Uin, 10)
	cli.GuildService = &GuildService{c: cli}
	cli.TCP.PlannedDisconnect(cli.plannedDisconnect)
	cli.TCP.UnexpectedDisconnect(cli.unexpectedDisconnect)
	return cli
}

func (c *QQClient) version() *auth.AppVersion {
	return c.transport.Version
}

func (c *QQClient) Device() *DeviceInfo {
	return c.transport.Device
}

func (c *QQClient) UseDevice(info *auth.Device) {
	c.transport.Version = info.Protocol.Version()
	c.transport.Device = info
	c.highwaySession.AppID = int32(c.version().AppId)
	c.sig.Ksid = []byte(fmt.Sprintf("|%s|A8.2.7.27f6ea96", info.IMEI))
}

func (c *QQClient) Release() {
	if c.Online.Load() {
		c.Disconnect()
	}
	c.alive = false
}

// Login send login request
func (c *QQClient) Login() (*LoginResponse, error) {
	if c.Online.Load() {
		return nil, ErrAlreadyOnline
	}
	err := c.connect()
	if err != nil {
		return nil, err
	}
	rsp, err := c.sendAndWait(c.buildLoginPacket())
	if err != nil {
		c.Disconnect()
		return nil, err
	}
	l := rsp.(LoginResponse)
	if l.Success {
		err = c.init(false)
	}
	return &l, err
}

func (c *QQClient) TokenLogin(token []byte) error {
	if c.Online.Load() {
		return ErrAlreadyOnline
	}
	err := c.connect()
	if err != nil {
		return err
	}
	{
		r := binary.NewReader(token)
		c.Uin = r.ReadInt64()
		c.sig.D2 = r.ReadBytesShort()
		c.sig.D2Key = r.ReadBytesShort()
		c.sig.TGT = r.ReadBytesShort()
		c.sig.SrmToken = r.ReadBytesShort()
		c.sig.T133 = r.ReadBytesShort()
		c.sig.EncryptedA1 = r.ReadBytesShort()
		c.oicq.WtSessionTicketKey = r.ReadBytesShort()
		c.sig.OutPacketSessionID = r.ReadBytesShort()
		// SystemDeviceInfo.TgtgtKey = r.ReadBytesShort()
		c.Device().TgtgtKey = r.ReadBytesShort()
	}
	_, err = c.sendAndWait(c.buildRequestChangeSigPacket(true))
	if err != nil {
		return err
	}
	return c.init(true)
}

func (c *QQClient) FetchQRCode() (*QRCodeLoginResponse, error) {
	return c.FetchQRCodeCustomSize(3, 4, 2)
}

func (c *QQClient) FetchQRCodeCustomSize(size, margin, ecLevel uint32) (*QRCodeLoginResponse, error) {
	if c.Online.Load() {
		return nil, ErrAlreadyOnline
	}
	err := c.connect()
	if err != nil {
		return nil, err
	}
	i, err := c.sendAndWait(c.buildQRCodeFetchRequestPacket(size, margin, ecLevel))
	if err != nil {
		return nil, errors.Wrap(err, "fetch qrcode error")
	}
	return i.(*QRCodeLoginResponse), nil
}

func (c *QQClient) QueryQRCodeStatus(sig []byte) (*QRCodeLoginResponse, error) {
	i, err := c.sendAndWait(c.buildQRCodeResultQueryRequestPacket(sig))
	if err != nil {
		return nil, errors.Wrap(err, "query result error")
	}
	return i.(*QRCodeLoginResponse), nil
}

func (c *QQClient) QRCodeLogin(info *QRCodeLoginInfo) (*LoginResponse, error) {
	i, err := c.sendAndWait(c.buildQRCodeLoginPacket(info.tmpPwd, info.tmpNoPicSig, info.tgtQR))
	if err != nil {
		return nil, errors.Wrap(err, "qrcode login error")
	}
	rsp := i.(LoginResponse)
	if rsp.Success {
		err = c.init(false)
	}
	return &rsp, err
}

// SubmitCaptcha send captcha to server
func (c *QQClient) SubmitCaptcha(result string, sign []byte) (*LoginResponse, error) {
	seq, packet := c.buildCaptchaPacket(result, sign)
	rsp, err := c.sendAndWait(seq, packet)
	if err != nil {
		c.Disconnect()
		return nil, err
	}
	l := rsp.(LoginResponse)
	if l.Success {
		err = c.init(false)
	}
	return &l, err
}

func (c *QQClient) SubmitTicket(ticket string) (*LoginResponse, error) {
	seq, packet := c.buildTicketSubmitPacket(ticket)
	rsp, err := c.sendAndWait(seq, packet)
	if err != nil {
		c.Disconnect()
		return nil, err
	}
	l := rsp.(LoginResponse)
	if l.Success {
		err = c.init(false)
	}
	return &l, err
}

func (c *QQClient) SubmitSMS(code string) (*LoginResponse, error) {
	rsp, err := c.sendAndWait(c.buildSMSCodeSubmitPacket(code))
	if err != nil {
		c.Disconnect()
		return nil, err
	}
	l := rsp.(LoginResponse)
	if l.Success {
		err = c.init(false)
	}
	return &l, err
}

func (c *QQClient) RequestSMS() bool {
	rsp, err := c.sendAndWait(c.buildSMSRequestPacket())
	if err != nil {
		c.error("request sms error: %v", err)
		return false
	}
	return rsp.(LoginResponse).Error == SMSNeededError
}

func (c *QQClient) init(tokenLogin bool) error {
	if len(c.sig.G) == 0 {
		c.warning("device lock is disabled. HTTP API may fail.")
	}
	c.highwaySession.Uin = strconv.FormatInt(c.Uin, 10)
	if err := c.registerClient(); err != nil {
		return errors.Wrap(err, "register error")
	}
	if tokenLogin {
		notify := make(chan struct{}, 2)
		d := c.waitPacket("StatSvc.ReqMSFOffline", func(i any, err error) {
			notify <- struct{}{}
		})
		d2 := c.waitPacket("MessageSvc.PushForceOffline", func(i any, err error) {
			notify <- struct{}{}
		})
		select {
		case <-notify:
			d()
			d2()
			return errors.New("token failed")
		case <-time.After(time.Second):
			d()
			d2()
		}
	}
	c.groupSysMsgCache, _ = c.GetGroupSystemMessages()
	if !c.heartbeatEnabled {
		go c.doHeartbeat()
	}
	_ = c.RefreshStatus()
	if c.version().Protocol == auth.QiDian {
		_, _ = c.sendAndWait(c.buildLoginExtraPacket())     // 小登录
		_, _ = c.sendAndWait(c.buildConnKeyRequestPacket()) // big data key 如果等待 config push 的话时间来不及
	}
	seq, pkt := c.buildGetMessageRequestPacket(msg.SyncFlag_START, time.Now().Unix())
	_, _ = c.sendAndWait(seq, pkt, network.RequestParams{"used_reg_proxy": true, "init": true})
	c.syncChannelFirstView()
	return nil
}

func (c *QQClient) GenToken() []byte {
	return binary.NewWriterF(func(w *binary.Writer) {
		w.WriteUInt64(uint64(c.Uin))
		w.WriteBytesShort(c.sig.D2)
		w.WriteBytesShort(c.sig.D2Key)
		w.WriteBytesShort(c.sig.TGT)
		w.WriteBytesShort(c.sig.SrmToken)
		w.WriteBytesShort(c.sig.T133)
		w.WriteBytesShort(c.sig.EncryptedA1)
		w.WriteBytesShort(c.oicq.WtSessionTicketKey)
		w.WriteBytesShort(c.sig.OutPacketSessionID)
		w.WriteBytesShort(c.Device().TgtgtKey)
	})
}

func (c *QQClient) SetOnlineStatus(s UserOnlineStatus) {
	if s < 1000 {
		_, _ = c.sendAndWait(c.buildStatusSetPacket(int32(s), 0))
		return
	}
	_, _ = c.sendAndWait(c.buildStatusSetPacket(11, int32(s)))
}

func (c *QQClient) GetWordSegmentation(text string) ([]string, error) {
	rsp, err := c.sendAndWait(c.buildWordSegmentationPacket([]byte(text)))
	if err != nil {
		return nil, err
	}
	if data, ok := rsp.([][]byte); ok {
		var ret []string
		for _, val := range data {
			ret = append(ret, string(val))
		}
		return ret, nil
	}
	return nil, errors.New("decode error")
}

func (c *QQClient) GetSummaryInfo(target int64) (*SummaryCardInfo, error) {
	rsp, err := c.sendAndWait(c.buildSummaryCardRequestPacket(target))
	if err != nil {
		return nil, err
	}
	return rsp.(*SummaryCardInfo), nil
}

// ReloadFriendList refresh QQClient.FriendList field via GetFriendList()
func (c *QQClient) ReloadFriendList() error {
	rsp, err := c.GetFriendList()
	if err != nil {
		return err
	}
	c.FriendList = rsp
	return nil
}

func (c *QQClient) GetFriendList() ([]*FriendInfo, error) {
	data, err := c.SendApi("get_friend_list", nil)
	if err != nil {
		return nil, err
	}
	t, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	var resp []FriendData
	if err := json.Unmarshal(t, &resp); err != nil {
		return nil, err
	}
	friends := make([]*FriendInfo, len(resp))
	c.debug("GetFriendList: %v", resp)
	for i, friend := range resp {
		friends[i] = &FriendInfo{
			Uin:      friend.UserID,
			Nickname: friend.NickName,
			Remark:   friend.Remark,
			FaceId:   0,
		}
	}
	return friends, nil
}

func (c *QQClient) GetGroupInfo(groupCode int64) (*GroupInfo, error) {
	data, err := c.SendApi("get_group_info", map[string]any{"group_id": groupCode})
	if err != nil {
		return nil, err
	}
	var resp GroupData
	t, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(t, &resp); err != nil {
		return nil, err
	}
	return &GroupInfo{
		Uin:             resp.GroupID,
		Code:            resp.GroupID,
		Name:            resp.GroupName,
		GroupCreateTime: uint32(resp.GroupCreateTime),
		GroupLevel:      uint32(resp.GroupLevel),
		MemberCount:     uint16(resp.MemberCount),
		MaxMemberCount:  uint16(resp.MaxMemberCount),
	}, nil
}

func (c *QQClient) SendGroupPoke(groupCode, target int64) {
	_, _ = c.sendAndWait(c.buildGroupPokePacket(groupCode, target))
}

func (c *QQClient) SendFriendPoke(target int64) {
	_, _ = c.sendAndWait(c.buildFriendPokePacket(target))
}

func (c *QQClient) ReloadGroupList() error {
	c.groupListLock.Lock()
	defer c.groupListLock.Unlock()
	list, err := c.GetGroupList()
	if err != nil {
		return err
	}
	c.GroupList = list
	return nil
}

func generateEcho(action string) string {
	timestamp := time.Now().UnixNano()
	return fmt.Sprintf("%s:%d", action, timestamp)
}

func (c *QQClient) GetGroupList() ([]*GroupInfo, error) {
	data, err := c.SendApi("get_group_list", nil)
	if err != nil {
		return nil, err
	}
	var resp []GroupData
	t, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(t, &resp); err != nil {
		return nil, err
	}
	groups := make([]*GroupInfo, len(resp))
	for i, group := range resp {
		groups[i] = &GroupInfo{
			Uin:             group.GroupID,
			Code:            group.GroupID,
			Name:            group.GroupName,
			GroupCreateTime: uint32(group.GroupCreateTime),
			GroupLevel:      uint32(group.GroupLevel),
			MemberCount:     uint16(group.MemberCount),
			MaxMemberCount:  uint16(group.MaxMemberCount),
		}
	}
	return groups, nil
}

func (c *QQClient) GetGroupMembers(group *GroupInfo) ([]*GroupMemberInfo, error) {
	return c.getGroupMembers(group)
}

func (c *QQClient) getGroupMembers(group *GroupInfo) ([]*GroupMemberInfo, error) {
	data, err := c.SendApi("get_group_member_list", map[string]any{
		"group_id": group.Uin,
		"no_cache": true,
	})
	if err != nil {
		return nil, err
	}
	var resp []MemberData
	t, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(t, &resp); err != nil {
		return nil, err
	}
	members := make([]*GroupMemberInfo, len(resp))
	for i, member := range resp {
		var permission MemberPermission
		if member.Role == "owner" {
			permission = Owner
		} else if member.Role == "admin" {
			permission = Administrator
		} else if member.Role == "member" {
			permission = Member
		}
		members[i] = &GroupMemberInfo{
			Group:           group,
			Uin:             member.UserID,
			Nickname:        member.Nickname,
			CardName:        member.Card,
			JoinTime:        member.JoinTime,
			LastSpeakTime:   member.LastSentTime,
			SpecialTitle:    member.Title,
			ShutUpTimestamp: member.ShutUpTimestamp,
			Permission:      permission,
		}
	}
	return members, nil
}

func (c *QQClient) GetMemberInfo(groupCode, memberUin int64) (*GroupMemberInfo, error) {
	info, err := c.sendAndWait(c.buildGroupMemberInfoRequestPacket(groupCode, memberUin))
	if err != nil {
		return nil, err
	}
	return info.(*GroupMemberInfo), nil
}

func (c *QQClient) FindFriend(uin int64) *FriendInfo {
	if uin == c.Uin {
		return &FriendInfo{
			Uin:      uin,
			Nickname: c.Nickname,
		}
	}
	for _, t := range c.FriendList {
		f := t
		if f.Uin == uin {
			return f
		}
	}
	return nil
}

func (c *QQClient) DeleteFriend(uin int64) error {
	if c.FindFriend(uin) == nil {
		return errors.New("friend not found")
	}
	_, err := c.sendAndWait(c.buildFriendDeletePacket(uin))
	return errors.Wrap(err, "delete friend error")
}

func (c *QQClient) FindGroupByUin(uin int64) *GroupInfo {
	for _, g := range c.GroupList {
		f := g
		if f.Uin == uin {
			return f
		}
	}
	return nil
}

func (c *QQClient) FindGroup(code int64) *GroupInfo {
	for _, g := range c.GroupList {
		if g.Code == code {
			return g
		}
	}
	return nil
}

func (c *QQClient) SolveGroupJoinRequest(i any, accept, block bool, reason string) {
	if accept {
		block = false
		reason = ""
	}
	subType := ""
	var Req interface{}
	switch req := i.(type) {
	case *UserJoinGroupRequest:
		subType = "add"
		Req = req
	case *GroupInvitedRequest:
		subType = "invite"
		Req = req
	}
	params := map[string]any{
		"sub_type": subType,
		"approve":  accept,
		"reason":   reason,
	}
	if req, ok := Req.(*UserJoinGroupRequest); ok {
		params["flag"] = req.Flag
	} else if req, ok := Req.(*GroupInvitedRequest); ok {
		params["flag"] = req.Flag
	}
	_, _ = c.SendApi("set_group_add_request", params)
}

func (c *QQClient) SolveFriendRequest(req *NewFriendRequest, accept bool) {
	_, _ = c.SendApi("set_friend_add_request", map[string]any{
		"flag":    req.Flag,
		"approve": accept,
	})
}

func (c *QQClient) getSKey() string {
	if c.sig.SKeyExpiredTime < time.Now().Unix() && len(c.sig.G) > 0 {
		c.debug("skey expired. refresh...")
		_, _ = c.sendAndWait(c.buildRequestTgtgtNopicsigPacket())
	}
	return string(c.sig.SKey)
}

func (c *QQClient) getCookies() string {
	return fmt.Sprintf("uin=o%d; skey=%s;", c.Uin, c.getSKey())
}

func (c *QQClient) getCookiesWithDomain(domain string) string {
	cookie := c.getCookies()

	if psKey, ok := c.sig.PsKeyMap[domain]; ok {
		return fmt.Sprintf("%s p_uin=o%d; p_skey=%s;", cookie, c.Uin, psKey)
	} else {
		return cookie
	}
}

func (c *QQClient) getCSRFToken() int {
	accu := 5381
	for _, b := range []byte(c.getSKey()) {
		accu = accu + (accu << 5) + int(b)
	}
	return 2147483647 & accu
}

func (c *QQClient) editMemberCard(groupCode, memberUin int64, card string) {
	_, _ = c.sendAndWait(c.buildEditGroupTagPacket(groupCode, memberUin, card))
}

func (c *QQClient) editMemberSpecialTitle(groupCode, memberUin int64, title string) {
	_, _ = c.sendAndWait(c.buildEditSpecialTitlePacket(groupCode, memberUin, title))
}

func (c *QQClient) setGroupAdmin(groupCode, memberUin int64, flag bool) {
	_, _ = c.sendAndWait(c.buildGroupAdminSetPacket(groupCode, memberUin, flag))
}

func (c *QQClient) updateGroupName(groupCode int64, newName string) {
	_, _ = c.sendAndWait(c.buildGroupNameUpdatePacket(groupCode, newName))
}

func (c *QQClient) groupMuteAll(groupCode int64, mute bool) {
	_, _ = c.sendAndWait(c.buildGroupMuteAllPacket(groupCode, mute))
}

func (c *QQClient) groupMute(groupCode, memberUin int64, time uint32) {
	_, _ = c.sendAndWait(c.buildGroupMutePacket(groupCode, memberUin, time))
}

func (c *QQClient) quitGroup(group *GroupInfo) {
	_, err := c.SendApi("set_group_leave", map[string]any{
		"group_id":   group.Code,
		"is_dismiss": false,
	})
	if err != nil {
		logger.Error(err)
		return
	}
	c.GroupLeaveEvent.dispatch(c, &GroupLeaveEvent{
		Group:    group,
		Operator: nil,
	})
}

func (c *QQClient) KickGroupMembers(groupCode int64, msg string, block bool, memberUins ...int64) {
	_, _ = c.sendAndWait(c.buildGroupKickPacket(groupCode, msg, block, memberUins...))
}

func (g *GroupInfo) removeMember(uin int64) {
	g.Update(func(info *GroupInfo) {
		i := sort.Search(len(info.Members), func(i int) bool {
			return info.Members[i].Uin >= uin
		})
		if i >= len(info.Members) || info.Members[i].Uin != uin { // not found
			return
		}
		info.Members = append(info.Members[:i], info.Members[i+1:]...)
	})
}

func (c *QQClient) setGroupAnonymous(groupCode int64, enable bool) {
	_, _ = c.sendAndWait(c.buildSetGroupAnonymous(groupCode, enable))
}

// UpdateProfile 修改个人资料
func (c *QQClient) UpdateProfile(profile ProfileDetailUpdate) {
	_, _ = c.sendAndWait(c.buildUpdateProfileDetailPacket(profile))
}

func (c *QQClient) SetCustomServer(servers []netip.AddrPort) {
	c.servers = append(servers, c.servers...)
}

func (c *QQClient) registerClient() error {
	_, err := c.sendAndWait(c.buildClientRegisterPacket())
	if err == nil {
		c.Online.Store(true)
	}
	return err
}

func (c *QQClient) nextSeq() uint16 {
	seq := c.SequenceId.Add(1)
	if seq > 1000000 {
		seq = int32(rand.Intn(100000)) + 60000
		c.SequenceId.Store(seq)
	}
	return uint16(seq)
}

func (c *QQClient) nextPacketSeq() int32 {
	return c.requestPacketRequestID.Add(2)
}

func (c *QQClient) nextGroupSeq() int32 {
	return c.groupSeq.Add(2)
}

func (c *QQClient) nextFriendSeq() int32 {
	return c.friendSeq.Add(1)
}

func (c *QQClient) nextQWebSeq() int64 {
	return c.qwebSeq.Add(1)
}

func (c *QQClient) nextHighwayApplySeq() int32 {
	return c.highwayApplyUpSeq.Add(2)
}

func (c *QQClient) doHeartbeat() {
	c.heartbeatEnabled = true
	times := 0
	for c.Online.Load() {
		time.Sleep(time.Second * 30)
		seq := c.nextSeq()
		req := network.Request{
			Type:        network.RequestTypeLogin,
			EncryptType: network.EncryptTypeNoEncrypt,
			SequenceID:  int32(seq),
			Uin:         c.Uin,
			CommandName: "Heartbeat.Alive",
			Body:        EmptyBytes,
		}
		packet := c.transport.PackPacket(&req)
		_, err := c.sendAndWait(seq, packet)
		if errors.Is(err, network.ErrConnectionClosed) {
			continue
		}
		times++
		if times >= 7 {
			_ = c.registerClient()
			times = 0
		}
	}
	c.heartbeatEnabled = false
}

func (c *QQClient) GetStrangerInfo(uid int64) (StrangerInfo, error) {
	data, err := c.SendApi("get_stranger_info", map[string]any{
		"user_id":  uid,
		"no_cache": false,
	})
	if err != nil {
		return StrangerInfo{}, err
	}
	t, err := json.Marshal(data)
	if err != nil {
		return StrangerInfo{}, err
	}
	var resp StrangerInfo
	if err = json.Unmarshal(t, &resp); err != nil {
		return StrangerInfo{}, err
	}
	return resp, nil
}
