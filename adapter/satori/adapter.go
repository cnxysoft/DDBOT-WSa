package satori

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("adapter", "satori")

const (
	opEvent    = 0
	opPing     = 1
	opPong     = 2
	opIdentify = 3
	opReady    = 4
	opMeta     = 5
)

var (
	tagPattern  = regexp.MustCompile(`<([a-zA-Z0-9:-]+)([^>]*)/?>`)
	attrPattern = regexp.MustCompile(`([a-zA-Z0-9:_-]+)="([^"]*)"`)
)

type SatoriAdapter struct {
	config     *adapter.AdapterConfig
	httpClient *http.Client
	wsConn     *websocket.Conn
	stopChan   chan struct{}
	readyCh    chan struct{}
	pingDone   chan struct{}

	mu                     sync.RWMutex
	connected              bool
	selfID                 int64
	platform               string
	loginUserID            string
	lastSequence           int64
	proxyURLs              []string
	idMap                  map[int64]string
	directChannelMap       map[int64]string
	messageChannelMap      map[int32]string
	msgIDMap               map[int32]string
	groupChannelMap        map[int64]string
	groupMessageHandlers   []func(*adapter.GroupMessageEvent)
	privateMessageHandlers []func(*adapter.PrivateMessageEvent)
	metaEventHandlers      []func(*adapter.MetaEvent)
	noticeEventHandlers    []func(*adapter.NoticeEvent)
	requestEventHandlers   []func(*adapter.RequestEvent)
}

type wsPayload struct {
	Op   int             `json:"op"`
	Body json.RawMessage `json:"body,omitempty"`
}

type identifyBody struct {
	Token string `json:"token,omitempty"`
	SN    int64  `json:"sn,omitempty"`
}

type readyBody struct {
	Logins    []loginResource `json:"logins"`
	ProxyURLs []string        `json:"proxy_urls"`
}

type metaBody struct {
	ProxyURLs []string `json:"proxy_urls"`
}

type eventBody struct {
	SN        int64            `json:"sn"`
	Type      string           `json:"type"`
	Timestamp int64            `json:"timestamp"`
	Login     *loginResource   `json:"login"`
	Channel   *channelResource `json:"channel"`
	Guild     *guildResource   `json:"guild"`
	Member    *memberResource  `json:"member"`
	Message   *messageResource `json:"message"`
	Operator  *userResource    `json:"operator"`
	User      *userResource    `json:"user"`
}

type loginResource struct {
	SN       int64         `json:"sn"`
	Platform string        `json:"platform"`
	User     *userResource `json:"user"`
	Status   int           `json:"status"`
	Adapter  string        `json:"adapter"`
	Features []string      `json:"features"`
}

type userResource struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Nick   string `json:"nick"`
	Avatar string `json:"avatar"`
	IsBot  bool   `json:"is_bot"`
}

type channelResource struct {
	ID   string `json:"id"`
	Type int    `json:"type"`
	Name string `json:"name"`
}

type guildResource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Avatar      string `json:"avatar"`
	MemberCount int    `json:"member_count"`
}

type memberResource struct {
	User     *userResource `json:"user"`
	Nick     string        `json:"nick"`
	Avatar   string        `json:"avatar"`
	JoinedAt int64         `json:"joined_at"`
	Name     string        `json:"name"`
}

type messageResource struct {
	ID        string           `json:"id"`
	Content   string           `json:"content"`
	CreatedAt int64            `json:"created_at"`
	UpdatedAt int64            `json:"updated_at"`
	User      *userResource    `json:"user"`
	Member    *memberResource  `json:"member"`
	Channel   *channelResource `json:"channel"`
	Guild     *guildResource   `json:"guild"`
}

func NewSatoriAdapter(cfg *adapter.AdapterConfig) *SatoriAdapter {
	return &SatoriAdapter{
		config:            cfg,
		httpClient:        &http.Client{Timeout: cfg.Timeout},
		stopChan:          make(chan struct{}),
		readyCh:           make(chan struct{}, 1),
		pingDone:          make(chan struct{}),
		idMap:             make(map[int64]string),
		directChannelMap:  make(map[int64]string),
		messageChannelMap: make(map[int32]string),
		msgIDMap:          make(map[int32]string),
		groupChannelMap:   make(map[int64]string),
	}
}

func (a *SatoriAdapter) Start() error {
	wsURL, err := a.eventURL()
	if err != nil {
		return err
	}

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial satori websocket: %w", err)
	}

	a.mu.Lock()
	a.wsConn = conn
	a.mu.Unlock()

	go a.readLoop()
	if err := a.identify(); err != nil {
		conn.Close()
		return err
	}

	select {
	case <-a.readyCh:
	case <-time.After(a.config.Timeout):
		conn.Close()
		return fmt.Errorf("wait satori ready timeout")
	}

	go a.pingLoop()
	logger.Infof("Satori adapter started: platform=%s self=%d", a.platform, a.selfID)
	return nil
}

func (a *SatoriAdapter) Stop() error {
	select {
	case <-a.stopChan:
	default:
		close(a.stopChan)
	}
	if a.wsConn != nil {
		_ = a.wsConn.Close()
	}
	select {
	case <-a.pingDone:
	case <-time.After(time.Second):
	}
	a.mu.Lock()
	a.connected = false
	a.mu.Unlock()
	return nil
}

func (a *SatoriAdapter) SendApi(action string, params map[string]interface{}) (interface{}, error) {
	endpoint, err := a.apiURL(action)
	if err != nil {
		return nil, err
	}
	if params == nil {
		params = map[string]interface{}{}
	}
	body, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	a.applyHeaders(req)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("satori api %s: status=%d body=%s", action, resp.StatusCode, string(respBody))
	}
	if len(bytes.TrimSpace(respBody)) == 0 {
		return nil, nil
	}
	var result interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (a *SatoriAdapter) SendGroupMessage(groupID int64, message interface{}) (int32, error) {
	channelID := a.resolveGroupChannelID(groupID)
	if channelID == "" {
		return 0, fmt.Errorf("satori group channel not found: %d", groupID)
	}
	segments := adapter.ParseMessageSegments(message)
	content := a.renderMessageContent(segments)
	if len(content) > 200 {
		logger.Debugf("Satori SendGroupMessage to %d: %s...[truncated %d chars]", groupID, content[:200], len(content)-200)
	} else {
		logger.Debugf("Satori SendGroupMessage to %d: %s", groupID, content)
	}
	data, err := a.SendApi("message.create", map[string]interface{}{
		"channel_id": channelID,
		"content":    content,
	})
	if err != nil {
		logger.Warnf("Satori SendGroupMessage error: %v", err)
		return 0, err
	}
	msgID := extractStringField(data, "id")
	if msgID == "" {
		return 0, nil
	}
	parsed := a.rememberID(msgID)
	a.mu.Lock()
	a.messageChannelMap[int32(parsed)] = channelID
	a.mu.Unlock()
	return int32(parsed), nil
}

func (a *SatoriAdapter) SendPrivateMessage(userID int64, message interface{}) (int32, error) {
	channelID, err := a.resolveDirectChannelID(userID)
	if err != nil {
		logger.Warnf("Satori SendPrivateMessage resolve channel error: %v", err)
		return 0, err
	}
	segments := adapter.ParseMessageSegments(message)
	content := a.renderMessageContent(segments)
	if len(content) > 200 {
		logger.Debugf("Satori SendPrivateMessage to %d: %s...[truncated %d chars]", userID, content[:200], len(content)-200)
	} else {
		logger.Debugf("Satori SendPrivateMessage to %d: %s", userID, content)
	}
	data, err := a.SendApi("message.create", map[string]interface{}{
		"channel_id": channelID,
		"content":    content,
	})
	if err != nil {
		logger.Warnf("Satori SendPrivateMessage error: %v", err)
		return 0, err
	}
	msgID := extractStringField(data, "id")
	if msgID == "" {
		return 0, nil
	}
	parsed := a.rememberID(msgID)
	a.mu.Lock()
	a.messageChannelMap[int32(parsed)] = channelID
	a.mu.Unlock()
	return int32(parsed), nil
}

func (a *SatoriAdapter) GetSelfID() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.selfID
}

func (a *SatoriAdapter) GetAdapterName() string {
	return "satori"
}

func (a *SatoriAdapter) IsConnected() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.connected
}

func (a *SatoriAdapter) OnGroupMessage(handler func(*adapter.GroupMessageEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.groupMessageHandlers = append(a.groupMessageHandlers, handler)
}

func (a *SatoriAdapter) OnPrivateMessage(handler func(*adapter.PrivateMessageEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.privateMessageHandlers = append(a.privateMessageHandlers, handler)
}

func (a *SatoriAdapter) OnMetaEvent(handler func(*adapter.MetaEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.metaEventHandlers = append(a.metaEventHandlers, handler)
}

func (a *SatoriAdapter) OnNoticeEvent(handler func(*adapter.NoticeEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.noticeEventHandlers = append(a.noticeEventHandlers, handler)
}

func (a *SatoriAdapter) OnRequestEvent(handler func(*adapter.RequestEvent)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requestEventHandlers = append(a.requestEventHandlers, handler)
}

func (a *SatoriAdapter) GetGroupList() ([]*adapter.GroupInfo, error) {
	data, err := a.SendApi("guild.list", nil)
	if err != nil {
		return nil, err
	}
	items := asList(data)
	result := make([]*adapter.GroupInfo, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		rawID := extractString(m["id"])
		parsedID := a.rememberID(rawID)
		result = append(result, &adapter.GroupInfo{
			Uin:         parsedID,
			Code:        parsedID,
			GroupID:     parsedID,
			Name:        extractString(m["name"]),
			GroupName:   extractString(m["name"]),
			MemberCount: extractInt(m["member_count"]),
		})
	}
	return result, nil
}

func (a *SatoriAdapter) GetGroupMemberList(groupID int64) ([]*adapter.GroupMemberInfo, error) {
	data, err := a.SendApi("guild.member.list", map[string]interface{}{"guild_id": a.rawID(groupID)})
	if err != nil {
		return nil, err
	}
	items := asList(data)
	result := make([]*adapter.GroupMemberInfo, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		userMap, _ := m["user"].(map[string]interface{})
		rawID := extractString(userMap["id"])
		result = append(result, &adapter.GroupMemberInfo{
			GroupID:  groupID,
			Uin:      a.rememberID(rawID),
			UserID:   a.rememberID(rawID),
			Nickname: extractString(userMap["name"]),
			CardName: extractString(m["nick"]),
		})
	}
	return result, nil
}

func (a *SatoriAdapter) GetFriendList() ([]*adapter.FriendInfo, error) {
	data, err := a.SendApi("friend.list", nil)
	if err != nil {
		return nil, err
	}
	items := asList(data)
	result := make([]*adapter.FriendInfo, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		rawID := extractString(m["id"])
		parsed := a.rememberID(rawID)
		result = append(result, &adapter.FriendInfo{
			Uin:      parsed,
			UserID:   parsed,
			Nickname: extractString(m["name"]),
			Remark:   extractString(m["nick"]),
		})
	}
	return result, nil
}

func (a *SatoriAdapter) GetStrangerInfo(userID int64) (map[string]interface{}, error) {
	data, err := a.SendApi("user.get", map[string]interface{}{"user_id": a.rawID(userID)})
	if err != nil {
		return nil, err
	}
	if m, ok := data.(map[string]interface{}); ok {
		return m, nil
	}
	return nil, nil
}

func (a *SatoriAdapter) GetGroupInfo(groupID int64) (*adapter.GroupInfo, error) {
	data, err := a.SendApi("guild.get", map[string]interface{}{"guild_id": a.rawID(groupID)})
	if err != nil {
		return nil, err
	}
	m, ok := data.(map[string]interface{})
	if !ok {
		return nil, nil
	}
	return &adapter.GroupInfo{
		Uin:         groupID,
		Code:        groupID,
		GroupID:     groupID,
		Name:        extractString(m["name"]),
		GroupName:   extractString(m["name"]),
		MemberCount: extractInt(m["member_count"]),
	}, nil
}

func (a *SatoriAdapter) GetGroupMemberInfo(groupID, userID int64) (*adapter.GroupMemberInfo, error) {
	data, err := a.SendApi("guild.member.get", map[string]interface{}{
		"guild_id": a.rawID(groupID),
		"user_id":  a.rawID(userID),
	})
	if err != nil {
		return nil, err
	}
	m, ok := data.(map[string]interface{})
	if !ok {
		return nil, nil
	}
	userMap, _ := m["user"].(map[string]interface{})
	return &adapter.GroupMemberInfo{
		GroupID:  groupID,
		Uin:      userID,
		UserID:   userID,
		Nickname: extractString(userMap["name"]),
		CardName: extractString(m["nick"]),
	}, nil
}

func (a *SatoriAdapter) DownloadFile(rawURL, base64Data, name string, headers []string) (string, error) {
	if base64Data != "" {
		return writeBase64Temp(base64Data, name)
	}
	if rawURL == "" {
		return "", nil
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	for _, header := range headers {
		parts := strings.SplitN(header, ":", 2)
		if len(parts) == 2 {
			req.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
		}
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("download file status=%d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", fileNameOrDefault(name))
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

func (a *SatoriAdapter) GetFileUrl(groupCode int64, fileId string) string {
	if fileId != "" && strings.Contains(fileId, "://") {
		return fileId
	}
	return ""
}

func (a *SatoriAdapter) GetMsgOrg(msgId int32) (interface{}, error) {
	a.mu.RLock()
	channelID := a.messageChannelMap[msgId]
	originalMsgID := a.msgIDMap[msgId]
	a.mu.RUnlock()
	if channelID == "" {
		return nil, fmt.Errorf("channel id not found for message %d", msgId)
	}
	if originalMsgID == "" {
		return nil, fmt.Errorf("original message id not found for message %d", msgId)
	}
	return a.SendApi("message.get", map[string]interface{}{
		"channel_id": channelID,
		"message_id": originalMsgID,
	})
}

func (a *SatoriAdapter) GetMsg(msgId int32) (*adapter.GetMsgResult, error) {
	a.mu.RLock()
	channelID := a.messageChannelMap[msgId]
	originalMsgID := a.msgIDMap[msgId]
	a.mu.RUnlock()
	if channelID == "" {
		return nil, fmt.Errorf("channel id not found for message %d", msgId)
	}
	if originalMsgID == "" {
		return nil, fmt.Errorf("original message id not found for message %d", msgId)
	}
	data, err := a.SendApi("message.get", map[string]interface{}{
		"channel_id": channelID,
		"message_id": originalMsgID,
	})
	if err != nil {
		return nil, err
	}

	msgMap, ok := data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid message data format")
	}

	result := &adapter.GetMsgResult{}

	// 解析 message ID
	if id, ok := msgMap["id"].(float64); ok {
		result.MessageID = int64(id)
	} else if idStr, ok := msgMap["id"].(string); ok {
		if parsed, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			result.MessageID = parsed
		}
	}

	// 解析 content 作为 raw_message 和消息元素
	if content, ok := msgMap["content"].(string); ok {
		result.RawMessage = content
		segments := parseSatoriContentWithMap(content, a.msgIDMap, nil, "")
		result.Elements = adapter.ConvertToMessageElements(segments)
	}

	// 解析 created_at 作为时间戳
	if ts, ok := msgMap["created_at"].(float64); ok {
		result.Time = int64(ts) / 1000 // Satori返回的是毫秒
	}

	// 解析 channel 信息
	if channel, ok := msgMap["channel"].(map[string]interface{}); ok {
		// channel.id 就是 channelID，可以用作 group_id
		if chID, ok := channel["id"].(string); ok {
			if parsed, err := strconv.ParseInt(chID, 10, 64); err == nil {
				result.GroupID = parsed
			}
		}
	}

	// 解析 guild 信息作为 group_id
	if guild, ok := msgMap["guild"].(map[string]interface{}); ok {
		if guildID, ok := guild["id"].(string); ok {
			if parsed, err := strconv.ParseInt(guildID, 10, 64); err == nil {
				result.GroupID = parsed
			}
		}
	}

	// 解析 user 信息作为 sender
	if user, ok := msgMap["user"].(map[string]interface{}); ok {
		sender := &adapter.SenderInfo{}
		if userID, ok := user["id"].(string); ok {
			if parsed, err := strconv.ParseInt(userID, 10, 64); err == nil {
				sender.UserID = parsed
			}
		}
		if nickname, ok := user["name"].(string); ok {
			sender.Nickname = nickname
		}
		result.Sender = sender
	}

	// 解析 member.nick 作为群成员昵称（会覆盖上面的 nickname）
	if member, ok := msgMap["member"].(map[string]interface{}); ok {
		if result.Sender == nil {
			result.Sender = &adapter.SenderInfo{}
		}
		if nick, ok := member["nick"].(string); ok {
			result.Sender.Nickname = nick
		}
		// member 也可能包含 user 信息
		if memberUser, ok := member["user"].(map[string]interface{}); ok {
			if result.Sender.UserID == 0 {
				if userID, ok := memberUser["id"].(string); ok {
					if parsed, err := strconv.ParseInt(userID, 10, 64); err == nil {
						result.Sender.UserID = parsed
						result.UserID = parsed
					}
				}
			}
			if result.Sender.Nickname == "" {
				if nickname, ok := memberUser["name"].(string); ok {
					result.Sender.Nickname = nickname
				}
			}
		}
	}

	return result, nil
}

func (a *SatoriAdapter) RecallMsg(msgId int32) error {
	a.mu.RLock()
	channelID := a.messageChannelMap[msgId]
	originalMsgID := a.msgIDMap[msgId]
	a.mu.RUnlock()
	if channelID == "" {
		return fmt.Errorf("channel id not found for message %d", msgId)
	}
	if originalMsgID == "" {
		return fmt.Errorf("original message id not found for message %d", msgId)
	}
	_, err := a.SendApi("message.delete", map[string]interface{}{
		"channel_id": channelID,
		"message_id": originalMsgID,
	})
	return err
}

func (a *SatoriAdapter) GroupPoke(groupCode, target int64) error {
	return fmt.Errorf("satori adapter: GroupPoke not implemented")
}

func (a *SatoriAdapter) FriendPoke(target int64) error {
	return fmt.Errorf("satori adapter: FriendPoke not implemented")
}

func (a *SatoriAdapter) SetGroupBan(groupCode, memberUin int64, duration int64) error {
	guildID := a.rawID(groupCode)
	userID := a.rawID(memberUin)
	_, err := a.SendApi("guild.member.mute", map[string]interface{}{
		"guild_id": guildID,
		"user_id":  userID,
		"duration": duration * 1000,
	})
	return err
}

func (a *SatoriAdapter) SetGroupWholeBan(groupCode int64, enable bool) error {
	channelID := a.groupChannelMap[groupCode]
	if channelID == "" {
		return fmt.Errorf("channel id not found for group %d", groupCode)
	}
	duration := int64(0)
	if enable {
		duration = -1
	}
	_, err := a.SendApi("channel.mute", map[string]interface{}{
		"channel_id": channelID,
		"duration":   duration,
	})
	return err
}

func (a *SatoriAdapter) KickGroupMember(groupCode int64, memberUin int64, rejectAddRequest bool) error {
	guildID := a.rawID(groupCode)
	userID := a.rawID(memberUin)
	_, err := a.SendApi("guild.member.kick", map[string]interface{}{
		"guild_id":  guildID,
		"user_id":   userID,
		"permanent": rejectAddRequest,
	})
	return err
}

func (a *SatoriAdapter) SetGroupLeave(groupCode int64, isDismiss bool) error {
	return fmt.Errorf("satori adapter: SetGroupLeave not implemented")
}

func (a *SatoriAdapter) SetGroupAdmin(groupCode, memberUin int64, enable bool) error {
	guildID := a.rawID(groupCode)
	userID := a.rawID(memberUin)
	roleID := "3"
	if enable {
		roleID = "2"
	}
	_, err := a.SendApi("guild.member.role.set", map[string]interface{}{
		"guild_id": guildID,
		"user_id":  userID,
		"role_id":  roleID,
	})
	return err
}

func (a *SatoriAdapter) EditGroupCard(groupCode, memberUin int64, card string) error {
	return fmt.Errorf("satori adapter: EditGroupCard not implemented")
}

func (a *SatoriAdapter) EditGroupTitle(groupCode, memberUin int64, title string) error {
	return fmt.Errorf("satori adapter: EditGroupTitle not implemented")
}

func (a *SatoriAdapter) identify() error {
	payload := wsPayload{Op: opIdentify}
	body, err := json.Marshal(identifyBody{Token: a.config.Token, SN: a.lastSequence})
	if err != nil {
		return err
	}
	payload.Body = body
	return a.wsConn.WriteJSON(payload)
}

func (a *SatoriAdapter) readLoop() {
	for {
		select {
		case <-a.stopChan:
			return
		default:
		}
		_, data, err := a.wsConn.ReadMessage()
		if err != nil {
			a.mu.Lock()
			a.connected = false
			a.mu.Unlock()
			return
		}
		if err := a.handleWSPayload(data); err != nil {
			logger.Warnf("handle satori payload: %v", err)
		}
	}
}

func (a *SatoriAdapter) pingLoop() {
	defer close(a.pingDone)
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopChan:
			return
		case <-ticker.C:
			_ = a.wsConn.WriteJSON(wsPayload{Op: opPing})
		}
	}
}

func (a *SatoriAdapter) handleWSPayload(data []byte) error {
	var payload wsPayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}
	switch payload.Op {
	case opReady:
		var body readyBody
		if err := json.Unmarshal(payload.Body, &body); err != nil {
			return err
		}
		a.handleReady(body)
	case opMeta:
		var body metaBody
		if err := json.Unmarshal(payload.Body, &body); err != nil {
			return err
		}
		a.mu.Lock()
		a.proxyURLs = append([]string(nil), body.ProxyURLs...)
		a.mu.Unlock()
	case opEvent:
		var body eventBody
		if err := json.Unmarshal(payload.Body, &body); err != nil {
			return err
		}
		a.handleEvent(body)
	case opPong:
		a.dispatchMeta(&adapter.MetaEvent{
			MetaEventType: string(adapter.PostTypeMetaEvent),
			Status:        map[string]interface{}{"online": true},
			SelfID:        a.GetSelfID(),
			Time:          time.Now().UnixMilli(),
		})
	}
	return nil
}

func (a *SatoriAdapter) handleReady(body readyBody) {
	a.mu.Lock()
	a.proxyURLs = append([]string(nil), body.ProxyURLs...)
	a.connected = true
	for _, login := range body.Logins {
		if login.User == nil || login.Platform == "" {
			continue
		}
		a.platform = login.Platform
		a.loginUserID = login.User.ID
		a.selfID = a.rememberID(login.User.ID)
		break
	}
	a.mu.Unlock()
	select {
	case a.readyCh <- struct{}{}:
	default:
	}
	a.dispatchMeta(&adapter.MetaEvent{
		MetaEventType: "lifecycle",
		Time:          time.Now().UnixMilli(),
		SelfID:        a.GetSelfID(),
		Status:        map[string]interface{}{"online": true},
	})
}

func (a *SatoriAdapter) handleEvent(event eventBody) {
	a.mu.Lock()
	a.lastSequence = event.SN
	if event.Login != nil && event.Login.User != nil {
		a.platform = event.Login.Platform
		a.loginUserID = event.Login.User.ID
		a.selfID = a.rememberID(event.Login.User.ID)
	}
	a.mu.Unlock()

	switch event.Type {
	case "message-created", "message-updated", "send":
		a.dispatchMessage(event)
	case "message-deleted":
		a.dispatchRecall(event)
	case "guild-member-added", "guild-member-removed", "guild-member-updated", "guild-added", "guild-removed", "friend-added":
		a.dispatchNotice(event)
	case "guild-member-request", "friend-request", "guild-request":
		a.dispatchRequest(event)
	case "login-added", "login-updated":
		a.dispatchMeta(&adapter.MetaEvent{MetaEventType: "lifecycle", Time: event.Timestamp, SelfID: a.GetSelfID(), Status: map[string]interface{}{"online": true}})
	case "login-removed":
		a.dispatchMeta(&adapter.MetaEvent{MetaEventType: "heartbeat", Time: event.Timestamp, SelfID: a.GetSelfID(), Status: map[string]interface{}{"online": false}})
	default:
		logger.Tracef("Satori unhandled event type: %s", event.Type)
	}
}

func (a *SatoriAdapter) dispatchMessage(event eventBody) {
	if event.Message == nil {
		return
	}
	channelID := pickChannelID(event)
	messageID := a.rememberID(event.Message.ID)
	internalMsgID := int32(messageID)
	a.mu.Lock()
	a.messageChannelMap[internalMsgID] = channelID
	a.msgIDMap[internalMsgID] = event.Message.ID
	if event.Guild != nil {
		guildID := a.rememberID(event.Guild.ID)
		if channelID != "" {
			a.groupChannelMap[guildID] = channelID
		}
	}
	a.mu.Unlock()
	segments := parseSatoriContentWithMap(event.Message.Content, a.msgIDMap, a.messageChannelMap, channelID)
	if event.Guild != nil {
		guildID := a.rememberID(event.Guild.ID)
		userID := rememberEventUserID(a, event)
		groupEvent := &adapter.GroupMessageEvent{
			MessageID:  int64(int32(messageID)),
			GroupID:    guildID,
			UserID:     userID,
			RawMessage: event.Message.Content,
			Message:    segments,
			Time:       normalizeTimestamp(event.Timestamp, event.Message.CreatedAt),
			SelfID:     a.GetSelfID(),
		}
		a.mu.RLock()
		handlers := append([]func(*adapter.GroupMessageEvent){}, a.groupMessageHandlers...)
		a.mu.RUnlock()
		for _, handler := range handlers {
			handler(groupEvent)
		}
		return
	}
	userID := rememberEventUserID(a, event)
	privateEvent := &adapter.PrivateMessageEvent{
		MessageID:  int64(int32(messageID)),
		UserID:     userID,
		RawMessage: event.Message.Content,
		Message:    segments,
		Time:       normalizeTimestamp(event.Timestamp, event.Message.CreatedAt),
		SelfID:     a.GetSelfID(),
		TargetID:   a.GetSelfID(),
	}
	if channelID != "" {
		a.mu.Lock()
		a.directChannelMap[userID] = channelID
		a.mu.Unlock()
	}
	a.mu.RLock()
	handlers := append([]func(*adapter.PrivateMessageEvent){}, a.privateMessageHandlers...)
	a.mu.RUnlock()
	for _, handler := range handlers {
		handler(privateEvent)
	}
}

func (a *SatoriAdapter) dispatchRecall(event eventBody) {
	noticeType := "group_recall"
	if event.Guild == nil {
		noticeType = "friend_recall"
	}
	logger.Debugf("Satori dispatchRecall: type=%s guild=%s user=%s operator=%s", noticeType, event.Guild, event.User, event.Operator)
	a.dispatchNoticeEvent(&adapter.NoticeEvent{
		NoticeType: noticeType,
		Time:       event.Timestamp,
		SelfID:     a.GetSelfID(),
		GroupID:    rememberGuildID(a, event),
		UserID:     rememberEventUserID(a, event),
		OperatorID: rememberOperatorID(a, event),
	})
}

func (a *SatoriAdapter) dispatchNotice(event eventBody) {
	var noticeType string
	switch event.Type {
	case "guild-member-added", "friend-added":
		noticeType = "group_increase"
	case "guild-member-removed", "guild-removed":
		noticeType = "group_decrease"
	case "guild-member-updated":
		noticeType = "group_card"
	case "guild-added":
		noticeType = "group_increase"
	default:
		logger.Tracef("Satori dispatchNotice unhandled type: %s", event.Type)
		return
	}
	logger.Debugf("Satori dispatchNotice: mapped type=%s event.Type=%s", noticeType, event.Type)
	a.dispatchNoticeEvent(&adapter.NoticeEvent{
		NoticeType: noticeType,
		Time:       event.Timestamp,
		SelfID:     a.GetSelfID(),
		GroupID:    rememberGuildID(a, event),
		UserID:     rememberEventUserID(a, event),
		OperatorID: rememberOperatorID(a, event),
	})
}

func (a *SatoriAdapter) dispatchRequest(event eventBody) {
	requestType := "group"
	subType := "add"
	if event.Type == "friend-request" {
		requestType = "friend"
		subType = ""
	} else if event.Type == "guild-request" {
		subType = "invite"
	}
	logger.Debugf("Satori dispatchRequest: type=%s subtype=%s user=%s group=%s", requestType, subType, event.User, event.Guild)
	a.dispatchRequestEvent(&adapter.RequestEvent{
		RequestType: requestType,
		Time:        event.Timestamp,
		SelfID:      a.GetSelfID(),
		GroupID:     rememberGuildID(a, event),
		UserID:      rememberEventUserID(a, event),
		Comment:     "",
		Flag:        extractString(event.Message.ID),
		SubType:     subType,
	})
}

func (a *SatoriAdapter) dispatchMeta(event *adapter.MetaEvent) {
	a.mu.RLock()
	handlers := append([]func(*adapter.MetaEvent){}, a.metaEventHandlers...)
	a.mu.RUnlock()
	for _, handler := range handlers {
		handler(event)
	}
}

func (a *SatoriAdapter) dispatchNoticeEvent(event *adapter.NoticeEvent) {
	logger.Debugf("Satori dispatchNoticeEvent: type=%s group=%d user=%d operator=%d", event.NoticeType, event.GroupID, event.UserID, event.OperatorID)
	a.mu.RLock()
	handlers := append([]func(*adapter.NoticeEvent){}, a.noticeEventHandlers...)
	a.mu.RUnlock()
	logger.Debugf("Satori notice handlers count: %d", len(handlers))
	for _, handler := range handlers {
		handler(event)
	}
}

func (a *SatoriAdapter) dispatchRequestEvent(event *adapter.RequestEvent) {
	logger.Debugf("Satori dispatchRequestEvent: type=%s group=%d user=%d", event.RequestType, event.GroupID, event.UserID)
	a.mu.RLock()
	handlers := append([]func(*adapter.RequestEvent){}, a.requestEventHandlers...)
	a.mu.RUnlock()
	logger.Debugf("Satori request handlers count: %d", len(handlers))
	for _, handler := range handlers {
		handler(event)
	}
}

func (a *SatoriAdapter) eventURL() (string, error) {
	parsed, err := normalizeURL(a.config.WSAddr)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "http" {
		parsed.Scheme = "ws"
	} else if parsed.Scheme == "https" {
		parsed.Scheme = "wss"
	}
	parsed.Path = strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), "/v1/events")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/v1")
	parsed.Path = parsed.Path + "/v1/events"
	parsed.RawQuery = ""
	return parsed.String(), nil
}

func (a *SatoriAdapter) apiURL(action string) (string, error) {
	parsed, err := normalizeURL(a.config.WSAddr)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "ws" {
		parsed.Scheme = "http"
	} else if parsed.Scheme == "wss" {
		parsed.Scheme = "https"
	}
	parsed.Path = strings.TrimSuffix(strings.TrimRight(parsed.Path, "/"), "/v1/events")
	parsed.Path = strings.TrimSuffix(parsed.Path, "/v1")
	parsed.Path = parsed.Path + "/v1/" + action
	parsed.RawQuery = ""
	return parsed.String(), nil
}

func (a *SatoriAdapter) applyHeaders(req *http.Request) {
	a.mu.RLock()
	platform := a.platform
	loginUserID := a.loginUserID
	a.mu.RUnlock()
	if a.config.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.config.Token)
	}
	if platform != "" {
		req.Header.Set("Satori-Platform", platform)
	}
	if loginUserID != "" {
		req.Header.Set("Satori-User-ID", loginUserID)
	}
}

func (a *SatoriAdapter) resolveDirectChannelID(userID int64) (string, error) {
	a.mu.RLock()
	if channelID := a.directChannelMap[userID]; channelID != "" {
		a.mu.RUnlock()
		return channelID, nil
	}
	a.mu.RUnlock()

	// Try user.channel.create first
	data, err := a.SendApi("user.channel.create", map[string]interface{}{"user_id": a.rawID(userID)})
	if err == nil {
		channelID := extractStringField(data, "id")
		if channelID != "" {
			a.mu.Lock()
			a.directChannelMap[userID] = channelID
			a.mu.Unlock()
			return channelID, nil
		}
	}

	// Fallback: try channel.create with type=1 (direct message)
	data, err = a.SendApi("channel.create", map[string]interface{}{
		"type":    1,
		"user_id": a.rawID(userID),
	})
	if err != nil {
		return "", fmt.Errorf("satori direct channel create failed for user %d: %v", userID, err)
	}
	channelID := extractStringField(data, "id")
	if channelID == "" {
		return "", fmt.Errorf("satori direct channel not found for user %d", userID)
	}
	a.mu.Lock()
	a.directChannelMap[userID] = channelID
	a.mu.Unlock()
	return channelID, nil
}

func (a *SatoriAdapter) resolveGroupChannelID(groupID int64) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if raw := a.groupChannelMap[groupID]; raw != "" {
		return raw
	}
	return a.rawIDLocked(groupID)
}

func (a *SatoriAdapter) rawID(id int64) string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.rawIDLocked(id)
}

func (a *SatoriAdapter) rawIDLocked(id int64) string {
	if raw, ok := a.idMap[id]; ok && raw != "" {
		return raw
	}
	return strconv.FormatInt(id, 10)
}

func (a *SatoriAdapter) rememberID(raw string) int64 {
	if raw == "" {
		return 0
	}
	if value, err := strconv.ParseInt(raw, 10, 64); err == nil {
		a.idMap[value] = raw
		return value
	}
	hashed := hashStringToInt64(raw)
	a.idMap[hashed] = raw
	return hashed
}

func normalizeURL(raw string) (*url.URL, error) {
	if raw == "" {
		raw = "http://127.0.0.1:5500"
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return url.Parse(raw)
}

func parseSatoriContent(content string) []adapter.MessageSegment {
	if content == "" {
		return nil
	}
	segments := make([]adapter.MessageSegment, 0)
	index := 0
	matches := tagPattern.FindAllStringSubmatchIndex(content, -1)
	for _, match := range matches {
		start, end := match[0], match[1]
		if start > index {
			appendTextSegment(&segments, htmlUnescape(content[index:start]))
		}
		tagName := content[match[2]:match[3]]
		attrs := parseAttrs(content[match[4]:match[5]])
		switch tagName {
		case "at":
			data := map[string]interface{}{}
			if attrs["type"] == "all" {
				data["qq"] = "all"
			} else if attrs["id"] != "" {
				data["qq"] = attrs["id"]
			}
			if len(data) > 0 {
				segments = append(segments, adapter.MessageSegment{Type: "at", Data: data})
			}
		case "img", "image":
			url := firstNonEmpty(attrs["src"], attrs["url"])
			if url != "" {
				segments = append(segments, adapter.MessageSegment{Type: "image", Data: map[string]interface{}{"url": url, "file": url}})
			}
		case "audio":
			url := firstNonEmpty(attrs["src"], attrs["url"])
			if url != "" {
				segments = append(segments, adapter.MessageSegment{Type: "record", Data: map[string]interface{}{"url": url, "file": url}})
			}
		case "video":
			url := firstNonEmpty(attrs["src"], attrs["url"])
			if url != "" {
				segments = append(segments, adapter.MessageSegment{Type: "video", Data: map[string]interface{}{"url": url, "file": url}})
			}
		case "file":
			url := firstNonEmpty(attrs["src"], attrs["url"])
			name := firstNonEmpty(attrs["title"], attrs["name"])
			if url != "" || name != "" {
				segments = append(segments, adapter.MessageSegment{Type: "file", Data: map[string]interface{}{"url": url, "name": name}})
			}
		case "quote":
			if id := attrs["id"]; id != "" {
				if numId, err := strconv.ParseInt(id, 10, 64); err == nil {
					segments = append(segments, adapter.MessageSegment{Type: "reply", Data: map[string]interface{}{"id": float64(numId)}})
				}
			}
		}
		index = end
	}
	if index < len(content) {
		appendTextSegment(&segments, htmlUnescape(content[index:]))
	}
	if len(segments) == 0 {
		appendTextSegment(&segments, htmlUnescape(stripTags(content)))
	}
	return segments
}

func parseSatoriContentWithMap(content string, msgIDMap map[int32]string, messageChannelMap map[int32]string, channelID string) []adapter.MessageSegment {
	if content == "" {
		return nil
	}
	segments := make([]adapter.MessageSegment, 0)
	index := 0
	matches := tagPattern.FindAllStringSubmatchIndex(content, -1)
	var quoteEnd int = -1
	for _, match := range matches {
		start, end := match[0], match[1]
		inQuote := quoteEnd >= 0 && start < quoteEnd
		if start > index {
			appendTextSegment(&segments, htmlUnescape(content[index:start]))
		}
		tagName := content[match[2]:match[3]]
		attrs := parseAttrs(content[match[4]:match[5]])
		switch tagName {
		case "quote":
			if id := attrs["id"]; id != "" {
				if numId, err := strconv.ParseInt(id, 10, 64); err == nil {
					localID := int32(numId)
					msgIDMap[localID] = id
					// 同时注册 channelID，这样 GetMsg 时能通过 quote ID 找到 channel
					if messageChannelMap != nil && channelID != "" {
						messageChannelMap[localID] = channelID
					}
					segments = append(segments, adapter.MessageSegment{Type: "reply", Data: map[string]interface{}{"id": float64(localID)}})
				}
			}
			closeIdx := strings.Index(content[end:], "</quote>")
			if closeIdx >= 0 {
				quoteEnd = end + closeIdx + len("</quote>")
				index = quoteEnd
			} else {
				index = end
			}
		case "at":
			data := map[string]interface{}{}
			if attrs["type"] == "all" {
				data["qq"] = "all"
			} else if attrs["id"] != "" {
				data["qq"] = attrs["id"]
			}
			if len(data) > 0 {
				segments = append(segments, adapter.MessageSegment{Type: "at", Data: data})
			}
			if !inQuote {
				index = end
			}
		case "img", "image":
			url := firstNonEmpty(attrs["src"], attrs["url"])
			if url != "" {
				segments = append(segments, adapter.MessageSegment{Type: "image", Data: map[string]interface{}{"url": url, "file": url}})
			}
			if !inQuote {
				index = end
			}
		case "audio":
			url := firstNonEmpty(attrs["src"], attrs["url"])
			if url != "" {
				segments = append(segments, adapter.MessageSegment{Type: "record", Data: map[string]interface{}{"url": url, "file": url}})
			}
			if !inQuote {
				index = end
			}
		case "video":
			url := firstNonEmpty(attrs["src"], attrs["url"])
			if url != "" {
				segments = append(segments, adapter.MessageSegment{Type: "video", Data: map[string]interface{}{"url": url, "file": url}})
			}
			if !inQuote {
				index = end
			}
		case "file":
			url := firstNonEmpty(attrs["src"], attrs["url"])
			name := firstNonEmpty(attrs["title"], attrs["name"])
			if url != "" || name != "" {
				segments = append(segments, adapter.MessageSegment{Type: "file", Data: map[string]interface{}{"url": url, "name": name}})
			}
			if !inQuote {
				index = end
			}
		default:
			if !inQuote {
				index = end
			}
		}
	}
	if index < len(content) {
		appendTextSegment(&segments, htmlUnescape(content[index:]))
	}
	if len(segments) == 0 {
		appendTextSegment(&segments, htmlUnescape(stripTags(content)))
	}
	return segments
}

func (a *SatoriAdapter) renderMessageContent(segments []adapter.MessageSegment) string {
	if len(segments) == 0 {
		return ""
	}
	var builder strings.Builder
	for _, segment := range segments {
		switch segment.Type {
		case "text":
			builder.WriteString(htmlEscape(extractString(segment.Data["text"])))
		case "at":
			qq := extractString(segment.Data["qq"])
			if qq == "all" {
				builder.WriteString(`<at type="all"/>`)
			} else if qq != "" {
				builder.WriteString(`<at id="` + htmlEscape(qq) + `"/>`)
			}
		case "image":
			uri := firstNonEmpty(extractString(segment.Data["url"]), extractString(segment.Data["file"]))
			if uri != "" {
				builder.WriteString(`<img src="` + htmlEscape(uri) + `"/>`)
			}
		case "record":
			uri := firstNonEmpty(extractString(segment.Data["url"]), extractString(segment.Data["file"]))
			if uri != "" {
				builder.WriteString(`<audio src="` + htmlEscape(uri) + `"/>`)
			}
		case "video":
			uri := firstNonEmpty(extractString(segment.Data["url"]), extractString(segment.Data["file"]))
			if uri != "" {
				builder.WriteString(`<video src="` + htmlEscape(uri) + `"/>`)
			}
		case "file":
			uri := firstNonEmpty(extractString(segment.Data["url"]), extractString(segment.Data["file"]))
			name := extractString(segment.Data["name"])
			if uri != "" || name != "" {
				builder.WriteString(`<file`)
				if uri != "" {
					builder.WriteString(` src="` + htmlEscape(uri) + `"`)
				}
				if name != "" {
					builder.WriteString(` title="` + htmlEscape(name) + `"`)
				}
				builder.WriteString(`/>`)
			}
		case "reply":
			if idStr := extractString(segment.Data["id"]); idStr != "" {
				if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
					a.mu.RLock()
					if origID, ok := a.msgIDMap[int32(id)]; ok {
						builder.WriteString(`<quote id="` + htmlEscape(origID) + `"/>`)
					} else {
						builder.WriteString(`<quote id="` + htmlEscape(idStr) + `"/>`)
					}
					a.mu.RUnlock()
				} else {
					builder.WriteString(`<quote id="` + htmlEscape(idStr) + `"/>`)
				}
			}
		}
	}
	return builder.String()
}

func parseAttrs(raw string) map[string]string {
	result := map[string]string{}
	for _, match := range attrPattern.FindAllStringSubmatch(raw, -1) {
		result[match[1]] = match[2]
	}
	return result
}

func appendTextSegment(segments *[]adapter.MessageSegment, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	*segments = append(*segments, adapter.MessageSegment{Type: "text", Data: map[string]interface{}{"text": text}})
}

func stripTags(content string) string {
	return tagPattern.ReplaceAllString(content, "")
}

func htmlEscape(input string) string {
	replacer := strings.NewReplacer("&", "&amp;", `<`, "&lt;", `>`, "&gt;", `"`, "&quot;")
	return replacer.Replace(input)
}

func htmlUnescape(input string) string {
	replacer := strings.NewReplacer("&quot;", `"`, "&gt;", `>`, "&lt;", `<`, "&amp;", "&")
	return replacer.Replace(input)
}

func asList(data interface{}) []interface{} {
	switch value := data.(type) {
	case []interface{}:
		return value
	case map[string]interface{}:
		if items, ok := value["data"].([]interface{}); ok {
			return items
		}
	}
	return nil
}

func writeBase64Temp(data, name string) (string, error) {
	if strings.Contains(data, ",") {
		parts := strings.SplitN(data, ",", 2)
		data = parts[1]
	}
	decoded, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", fileNameOrDefault(name))
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := tmp.Write(decoded); err != nil {
		return "", err
	}
	return tmp.Name(), nil
}

func fileNameOrDefault(name string) string {
	if name == "" {
		return "ddb-*"
	}
	base := filepath.Base(name)
	if base == "." || base == string(filepath.Separator) || base == "" {
		return "ddb-*"
	}
	return base + "-*"
}

func extractString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func extractInt(value interface{}) int {
	return int(extractInt64(value))
}

func extractInt64(value interface{}) int64 {
	switch typed := value.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		n, _ := typed.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(typed, 10, 64)
		return n
	default:
		return 0
	}
}

func extractStringField(data interface{}, field string) string {
	if m, ok := data.(map[string]interface{}); ok {
		return extractString(m[field])
	}
	if arr, ok := data.([]interface{}); ok && len(arr) > 0 {
		if m, ok := arr[0].(map[string]interface{}); ok {
			return extractString(m[field])
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizeTimestamp(primary, fallback int64) int64 {
	if primary > 0 {
		return primary
	}
	return fallback
}

func pickChannelID(event eventBody) string {
	if event.Message != nil && event.Message.Channel != nil && event.Message.Channel.ID != "" {
		return event.Message.Channel.ID
	}
	if event.Channel != nil {
		return event.Channel.ID
	}
	return ""
}

func rememberGuildID(a *SatoriAdapter, event eventBody) int64 {
	if event.Message != nil && event.Message.Guild != nil && event.Message.Guild.ID != "" {
		return a.rememberID(event.Message.Guild.ID)
	}
	if event.Guild != nil && event.Guild.ID != "" {
		return a.rememberID(event.Guild.ID)
	}
	return 0
}

func rememberEventUserID(a *SatoriAdapter, event eventBody) int64 {
	if event.Message != nil && event.Message.User != nil && event.Message.User.ID != "" {
		return a.rememberID(event.Message.User.ID)
	}
	if event.User != nil && event.User.ID != "" {
		return a.rememberID(event.User.ID)
	}
	if event.Member != nil && event.Member.User != nil && event.Member.User.ID != "" {
		return a.rememberID(event.Member.User.ID)
	}
	return 0
}

func rememberOperatorID(a *SatoriAdapter, event eventBody) int64 {
	if event.Operator != nil && event.Operator.ID != "" {
		return a.rememberID(event.Operator.ID)
	}
	return 0
}

func hashStringToInt64(raw string) int64 {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(raw))
	return int64(hasher.Sum64() & 0x7fffffffffffffff)
}

func stableFileName(rawURL string) string {
	sum := md5.Sum([]byte(rawURL))
	return hex.EncodeToString(sum[:])
}
