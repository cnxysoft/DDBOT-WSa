package onebot

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("adapter", "onebot-v11")

// URI 消息超时时间（NapCat/LLOneBot 需要先下载 URI 文件再上传）
// 使用与 wsclient 一致的最大值，确保足够长
const uriMessageTimeout = 30 * time.Minute

type OneBotAdapter struct {
	config   *adapter.AdapterConfig
	wsClient *adapter.WSClient
	selfID   int64
	stopChan chan struct{}

	groupMessageHandlers   []func(*adapter.GroupMessageEvent)
	privateMessageHandlers []func(*adapter.PrivateMessageEvent)
	metaEventHandlers      []func(*adapter.MetaEvent)
	noticeEventHandlers    []func(*adapter.NoticeEvent)
	requestEventHandlers   []func(*adapter.RequestEvent)

	handlersMu sync.RWMutex
}

// containsURI 检测消息参数是否包含 http/https/file URI
// NapCat/LLOneBot 收到包含 URI 的消息后需要先下载文件再上传，
// 这个过程可能比较慢，需要给更长的超时时间
func containsURI(params map[string]interface{}) bool {
	msg, ok := params["message"]
	if !ok {
		return false
	}

	switch m := msg.(type) {
	case string:
		// CQ码字符串可能包含 URI，但检测复杂，保守处理
		return false
	case []interface{}:
		for _, seg := range m {
			segment, ok := seg.(map[string]interface{})
			if !ok {
				continue
			}
			segType, _ := segment["type"].(string)
			data, _ := segment["data"].(map[string]interface{})

			switch segType {
			case "image", "video", "record", "file":
				// 检查 url 或 file 字段是否包含 URI
				if uri := getSegmentURI(data); uri != "" {
					if isRemoteURI(uri) {
						return true
					}
				}
			}
		}
	}
	return false
}

// getSegmentURI 从 segment data 中获取 URI
func getSegmentURI(data map[string]interface{}) string {
	// 优先检查 url 字段
	if url, ok := data["url"].(string); ok && url != "" {
		return url
	}
	// 然后检查 file 字段
	if file, ok := data["file"].(string); ok && file != "" {
		return file
	}
	return ""
}

// isRemoteURI 判断是否为远程 URI（需要下载）
func isRemoteURI(uri string) bool {
	return strings.HasPrefix(uri, "http://") ||
		strings.HasPrefix(uri, "https://") ||
		strings.HasPrefix(uri, "file://")
}

// containsMedia 检测消息参数是否包含媒体消息（图片/语音/视频/文件）。
// 媒体消息即使不带远程 URI（如 base64/file 形式），OneBot 端转码上传也可能较慢，
// 与包含 URI 一样需要给更长的超时时间。
func containsMedia(params map[string]interface{}) bool {
	msg, ok := params["message"]
	if !ok {
		return false
	}

	switch m := msg.(type) {
	case string:
		// CQ 码字符串形式：检查是否包含媒体 CQ 码
		return containsCQMedia(m)
	case []interface{}:
		for _, seg := range m {
			segment, ok := seg.(map[string]interface{})
			if !ok {
				continue
			}
			segType, _ := segment["type"].(string)
			switch segType {
			case "image", "record", "video", "file":
				return true
			}
		}
	}
	return false
}

// containsCQMedia 检测 CQ 码字符串是否包含媒体 CQ 码。
func containsCQMedia(raw string) bool {
	return strings.Contains(raw, "[CQ:image,") ||
		strings.Contains(raw, "[CQ:record,") ||
		strings.Contains(raw, "[CQ:video,") ||
		strings.Contains(raw, "[CQ:file,")
}

// calcSendTimeout 根据消息内容计算合适的超时时间
// 如果消息包含 URI 或媒体内容，使用更长的超时（因为 NapCat/LLOneBot 需要先下载或转码）
func (a *OneBotAdapter) calcSendTimeout(action string, params map[string]interface{}) time.Duration {
	// 只有发送消息的动作才需要特殊处理
	switch action {
	case "send_msg", "send_group_msg", "send_private_msg",
		"send_group_forward_msg", "send_private_forward_msg":
		if containsURI(params) || containsMedia(params) {
			return uriMessageTimeout
		}
	}
	return a.config.Timeout
}

func NewOneBotAdapter(cfg *adapter.AdapterConfig) *OneBotAdapter {
	return &OneBotAdapter{
		config:   cfg,
		stopChan: make(chan struct{}),
		selfID:   0,
	}
}

func (a *OneBotAdapter) Start() error {
	wsMode := a.config.WSMode
	if wsMode == "" {
		wsMode = adapter.WSModeServer
	}

	wsClient := adapter.NewWSClient(
		"onebot-v11",
		wsMode,
		a.config.WSAddr,
		adapter.WithWSToken(a.config.Token),
		adapter.WithWSMessageHandler(a.handleMessage),
	)

	if err := wsClient.Start(); err != nil {
		return fmt.Errorf("failed to start ws client: %v", err)
	}

	a.wsClient = wsClient
	logger.Infof("OneBot v11 adapter started in %s mode", wsMode)
	return nil
}

func (a *OneBotAdapter) Stop() error {
	close(a.stopChan)
	if a.wsClient != nil {
		return a.wsClient.Stop()
	}
	return nil
}

func (a *OneBotAdapter) GetAdapterName() string {
	return "onebot-v11"
}

func (a *OneBotAdapter) GetSelfID() int64 {
	return a.selfID
}

func (a *OneBotAdapter) IsConnected() bool {
	if a.wsClient != nil {
		return a.wsClient.IsConnected()
	}
	return false
}

func (a *OneBotAdapter) SendApi(action string, params map[string]interface{}) (interface{}, error) {
	if a.wsClient == nil {
		return nil, fmt.Errorf("ws client not initialized")
	}

	timeout := a.calcSendTimeout(action, params)
	resp, err := a.wsClient.SendAndWait(action, params, timeout)
	if err != nil {
		logger.Warnf("SendApi error: %v", err)
		return nil, err
	}

	return resp.Data, nil
}

func (a *OneBotAdapter) SendGroupMessage(groupID int64, message interface{}) (int32, error) {
	params := adapter.BuildMessageParams(message)
	params["group_id"] = groupID

	data, err := a.SendApi("send_group_msg", params)
	if err != nil {
		return 0, err
	}

	if dataMap, ok := data.(map[string]interface{}); ok {
		if msgID, ok := dataMap["message_id"].(float64); ok {
			return int32(msgID), nil
		}
	}

	return 0, nil
}

func (a *OneBotAdapter) SendPrivateMessage(userID int64, message interface{}) (int32, error) {
	params := adapter.BuildMessageParams(message)
	params["user_id"] = userID

	data, err := a.SendApi("send_private_msg", params)
	if err != nil {
		return 0, err
	}

	if dataMap, ok := data.(map[string]interface{}); ok {
		if msgID, ok := dataMap["message_id"].(float64); ok {
			return int32(msgID), nil
		}
	}

	return 0, nil
}

func (a *OneBotAdapter) SendGroupForwardMessage(groupCode int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	params := map[string]interface{}{
		"group_id": groupCode,
		"messages": nodes,
	}

	// 添加顶层参数 (prompt, source, summary, news)
	if options != nil {
		if options.Prompt != "" {
			params["prompt"] = options.Prompt
		}
		if options.Source != "" {
			params["source"] = options.Source
		}
		if options.Summary != "" {
			params["summary"] = options.Summary
		}
		if len(options.News) > 0 {
			news := make([]map[string]string, 0, len(options.News))
			for _, n := range options.News {
				news = append(news, map[string]string{"text": n})
			}
			params["news"] = news
		}
	}

	data, err := a.SendApi("send_group_forward_msg", params)
	if err != nil {
		return 0, "", err
	}

	if dataMap, ok := data.(map[string]interface{}); ok {
		if msgID, ok := dataMap["message_id"].(float64); ok {
			var forwardID string
			// 优先使用 forward_id (LLOneBot)
			if fid, ok := dataMap["forward_id"].(string); ok {
				forwardID = fid
			} else if resid, ok := dataMap["res_id"].(string); ok {
				// fallback 到 res_id (NapCatQQ)
				forwardID = resid
			}
			return int32(msgID), forwardID, nil
		}
	}

	return 0, "", nil
}

func (a *OneBotAdapter) SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *adapter.ForwardOptions) (int32, string, error) {
	params := map[string]interface{}{
		"user_id":  userID,
		"messages": nodes,
	}

	// 添加顶层参数 (prompt, source, summary, news)
	if options != nil {
		if options.Prompt != "" {
			params["prompt"] = options.Prompt
		}
		if options.Source != "" {
			params["source"] = options.Source
		}
		if options.Summary != "" {
			params["summary"] = options.Summary
		}
		if len(options.News) > 0 {
			news := make([]map[string]string, 0, len(options.News))
			for _, n := range options.News {
				news = append(news, map[string]string{"text": n})
			}
			params["news"] = news
		}
	}

	data, err := a.SendApi("send_private_forward_msg", params)
	if err != nil {
		return 0, "", err
	}

	if dataMap, ok := data.(map[string]interface{}); ok {
		if msgID, ok := dataMap["message_id"].(float64); ok {
			var forwardID string
			// 优先使用 forward_id (LLOneBot)
			if fid, ok := dataMap["forward_id"].(string); ok {
				forwardID = fid
			} else if resid, ok := dataMap["res_id"].(string); ok {
				// fallback 到 res_id (NapCatQQ)
				forwardID = resid
			}
			return int32(msgID), forwardID, nil
		}
	}

	return 0, "", nil
}

func (a *OneBotAdapter) OnGroupMessage(handler func(*adapter.GroupMessageEvent)) {
	a.handlersMu.Lock()
	defer a.handlersMu.Unlock()
	a.groupMessageHandlers = append(a.groupMessageHandlers, handler)
}

func (a *OneBotAdapter) OnPrivateMessage(handler func(*adapter.PrivateMessageEvent)) {
	a.handlersMu.Lock()
	defer a.handlersMu.Unlock()
	a.privateMessageHandlers = append(a.privateMessageHandlers, handler)
}

func (a *OneBotAdapter) OnMetaEvent(handler func(*adapter.MetaEvent)) {
	a.handlersMu.Lock()
	defer a.handlersMu.Unlock()
	a.metaEventHandlers = append(a.metaEventHandlers, handler)
}

func (a *OneBotAdapter) OnNoticeEvent(handler func(*adapter.NoticeEvent)) {
	a.handlersMu.Lock()
	defer a.handlersMu.Unlock()
	a.noticeEventHandlers = append(a.noticeEventHandlers, handler)
}

func (a *OneBotAdapter) OnRequestEvent(handler func(*adapter.RequestEvent)) {
	a.handlersMu.Lock()
	defer a.handlersMu.Unlock()
	a.requestEventHandlers = append(a.requestEventHandlers, handler)
}

func (a *OneBotAdapter) handleMessage(data []byte) {
	var msg map[string]interface{}
	if err := json.Unmarshal(data, &msg); err != nil {
		logger.Errorf("Failed to parse message: %v", err)
		return
	}

	postType, _ := msg["post_type"].(string)

	// Debug: print echo value for API responses
	rawEcho := msg["echo"]
	logger.Debugf("handleMessage: post_type=%s, echo=%v (type=%T), has_status=%v",
		postType, rawEcho, rawEcho, msg["status"] != nil)

	switch postType {
	case "message":
		a.handleMessageEvent(msg)
	case "meta_event":
		a.handleMetaEvent(msg)
	case "notice":
		a.handleNoticeEvent(msg)
	case "request":
		a.handleRequestEvent(msg)
	default:
		logger.Tracef("Unknown post_type: %s", postType)
	}
}

func (a *OneBotAdapter) handleMessageEvent(msg map[string]interface{}) {
	messageType, _ := msg["message_type"].(string)

	if messageType == "group" {
		event := a.parseGroupMessageEvent(msg)
		a.handlersMu.RLock()
		handlers := a.groupMessageHandlers
		a.handlersMu.RUnlock()

		for _, handler := range handlers {
			handler(event)
		}
	} else if messageType == "private" {
		event := a.parsePrivateMessageEvent(msg)
		a.handlersMu.RLock()
		handlers := a.privateMessageHandlers
		a.handlersMu.RUnlock()

		for _, handler := range handlers {
			handler(event)
		}
	}
}

func (a *OneBotAdapter) parseGroupMessageEvent(msg map[string]interface{}) *adapter.GroupMessageEvent {
	event := &adapter.GroupMessageEvent{
		GroupID:    getInt64(msg["group_id"]),
		UserID:     getInt64(msg["user_id"]),
		MessageID:  getInt64(msg["message_id"]),
		RawMessage: getString(msg["raw_message"]),
		Time:       getInt64(msg["time"]),
		SelfID:     getInt64(msg["self_id"]),
	}

	// sender 对象还原发送者资料。成品证据：onebot-v11.(*OneBotAdapter).parseGroupMessageEvent
	// @0xb63340，依次 mapaccess "sender"（+0x244），再取其中的 user_id（缺省回退 event.UserID）、
	// nickname、card、role，写入新 SenderInfo 并挂到 event+0x50。
	if sender, ok := msg["sender"].(map[string]interface{}); ok {
		senderID := getInt64(sender["user_id"])
		if senderID == 0 {
			senderID = event.UserID
		}
		event.Sender = &adapter.SenderInfo{
			UserID:   senderID,
			Uin:      senderID,
			Nickname: getString(sender["nickname"]),
			Card:     getString(sender["card"]),
			Role:     getString(sender["role"]),
		}
	}

	if msg["message"] != nil {
		event.Message = adapter.ParseMessageSegments(msg["message"])
	} else if msg["raw_message"] != nil {
		// 回退到 raw_message 解析
		event.Message = adapter.ParseMessageSegments(getString(msg["raw_message"]))
	}

	return event
}

func (a *OneBotAdapter) parsePrivateMessageEvent(msg map[string]interface{}) *adapter.PrivateMessageEvent {
	event := &adapter.PrivateMessageEvent{
		UserID:     getInt64(msg["user_id"]),
		MessageID:  getInt64(msg["message_id"]),
		RawMessage: getString(msg["raw_message"]),
		Time:       getInt64(msg["time"]),
		SelfID:     getInt64(msg["self_id"]),
		TargetID:   getInt64(msg["target_id"]),
	}

	if msg["message"] != nil {
		event.Message = adapter.ParseMessageSegments(msg["message"])
	} else if msg["raw_message"] != nil {
		// 回退到 raw_message 解析
		event.Message = adapter.ParseMessageSegments(getString(msg["raw_message"]))
	}

	return event
}

func (a *OneBotAdapter) handleMetaEvent(msg map[string]interface{}) {
	event := &adapter.MetaEvent{
		MetaEventType: getString(msg["meta_event_type"]),
		Time:          getInt64(msg["time"]),
		SelfID:        getInt64(msg["self_id"]),
		Interval:      getInt64(msg["interval"]),
	}

	if status, ok := msg["status"].(map[string]interface{}); ok {
		event.Status = status
	}

	a.handlersMu.RLock()
	handlers := a.metaEventHandlers
	a.handlersMu.RUnlock()

	for _, handler := range handlers {
		handler(event)
	}

	// 处理生命周期事件时更新 selfID
	if event.MetaEventType == "lifecycle" {
		a.selfID = event.SelfID
	}
}

func (a *OneBotAdapter) handleNoticeEvent(msg map[string]interface{}) {
	event := &adapter.NoticeEvent{
		NoticeType: getString(msg["notice_type"]),
		Time:       getInt64(msg["time"]),
		SelfID:     getInt64(msg["self_id"]),
		GroupID:    getInt64(msg["group_id"]),
		UserID:     getInt64(msg["user_id"]),
		OperatorID: getInt64(msg["operator_id"]),
		// 成品 @0xb63e00 依次读 target_id(+0x18b)/sender_id(+0x1c2) 写入 NoticeEvent+0x38/+0x40。
		TargetID:     getInt64(msg["target_id"]),
		SenderID:     getInt64(msg["sender_id"]),
		Duration:     int32(getInt64(msg["duration"])),
		SubType:      getString(msg["sub_type"]),
		Title:        getString(msg["title"]),
		MessageID:    getInt64(msg["message_id"]),
		File:         getFile(msg["file"]),
		OperatorNick: getString(msg["operator_nick"]),
		Times:        getInt(msg["times"]),
		CardNew:      getString(msg["card_new"]),
		CardOld:      getString(msg["card_old"]),
		// 成品 @0xb63e00 在 +0x273 读 is_add 键（0x109f622，len=6），0xb645b2 写入 event+0x180。
		IsAdd: getBool(msg["is_add"]),
	}

	if likes, ok := msg["likes"].([]interface{}); ok && len(likes) > 0 {
		if like, ok := likes[0].(map[string]interface{}); ok {
			event.EmojiId = getString(like["emoji_id"])
			event.EmojiCount = getInt(like["count"])
		}
	} else {
		event.EmojiId = getString(msg["emoji_id"])
		event.EmojiCount = getInt(msg["emoji_count"])
	}

	a.handlersMu.RLock()
	handlers := a.noticeEventHandlers
	a.handlersMu.RUnlock()

	for _, handler := range handlers {
		handler(event)
	}
}

func (a *OneBotAdapter) handleRequestEvent(msg map[string]interface{}) {
	event := &adapter.RequestEvent{
		RequestType: getString(msg["request_type"]),
		Time:        getInt64(msg["time"]),
		SelfID:      getInt64(msg["self_id"]),
		GroupID:     getInt64(msg["group_id"]),
		UserID:      getInt64(msg["user_id"]),
		Comment:     getString(msg["comment"]),
		Flag:        getString(msg["flag"]),
		SubType:     getString(msg["sub_type"]),
		// 成品 @0xb64880 在 +0x201 读 invitor_id、+0x239 读 source_group_id（均 getInt64）。
		InvitorID:     getInt64(msg["invitor_id"]),
		SourceGroupID: getInt64(msg["source_group_id"]),
	}

	a.handlersMu.RLock()
	handlers := a.requestEventHandlers
	a.handlersMu.RUnlock()

	for _, handler := range handlers {
		handler(event)
	}
}

func (a *OneBotAdapter) GetGroupList() ([]*adapter.GroupInfo, error) {
	// 成品证据：onebot-v11.(*OneBotAdapter).GetGroupList @0xb64cc0 +0x35（0xb64cf5）
	// mapassign_faststr 键 0x10a48c6「no_cache」（8 字节），值为 bool true
	// （类型 0xee7500、数据指针 0x115cc48），再以 14 字节 API 名 0x10ad86b
	// "get_group_list" 调 SendApi。
	data, err := a.SendApi("get_group_list", map[string]interface{}{"no_cache": true})
	if err != nil {
		return nil, err
	}

	var groups []*adapter.GroupInfo
	switch d := data.(type) {
	case []interface{}:
		for _, item := range d {
			groupMap, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("get_group_list returned invalid item type %T", item)
			}
			groupID := getInt64(groupMap["group_id"])
			if groupID <= 0 {
				return nil, fmt.Errorf("get_group_list returned invalid group_id %d", groupID)
			}
			group := &adapter.GroupInfo{
				Uin:             groupID,
				Code:            groupID,
				GroupID:         groupID,
				Name:            getString(groupMap["group_name"]),
				GroupName:       getString(groupMap["group_name"]),
				MemberCount:     getInt(groupMap["member_count"]),
				MaxMemberCount:  getInt(groupMap["max_member_count"]),
				GroupCreateTime: getInt64(groupMap["group_create_time"]),
				GroupLevel:      getInt(groupMap["group_level"]),
				OwnerUin:        getInt64(groupMap["owner_id"]),
			}
			groups = append(groups, group)
		}
	default:
		return nil, fmt.Errorf("get_group_list returned unexpected response type %T", data)
	}

	return groups, nil
}

func (a *OneBotAdapter) GetGroupMemberList(groupID int64) ([]*adapter.GroupMemberInfo, error) {
	data, err := a.SendApi("get_group_member_list", map[string]interface{}{
		"group_id": groupID,
	})
	if err != nil {
		return nil, err
	}

	var members []*adapter.GroupMemberInfo
	switch d := data.(type) {
	case []interface{}:
		for _, item := range d {
			memberMap, ok := item.(map[string]interface{})
			if !ok {
				return nil, fmt.Errorf("get_group_member_list returned invalid item type %T", item)
			}
			memberGroupID := getInt64(memberMap["group_id"])
			memberUserID := getInt64(memberMap["user_id"])
			if memberGroupID != groupID {
				return nil, fmt.Errorf("get_group_member_list returned invalid identity group_id=%d user_id=%d for group %d", memberGroupID, memberUserID, groupID)
			}
			member := &adapter.GroupMemberInfo{
				GroupID:         memberGroupID,
				UserID:          memberUserID,
				Nickname:        getString(memberMap["nickname"]),
				Card:            getString(memberMap["card"]),
				Sex:             getString(memberMap["sex"]),
				Age:             getInt(memberMap["age"]),
				Area:            getString(memberMap["area"]),
				Level:           getInt(memberMap["level"]),
				QQLevel:         int16(getInt(memberMap["qq_level"])),
				JoinTime:        getInt64(memberMap["join_time"]),
				LastSentTime:    getInt64(memberMap["last_sent_time"]),
				TitleExpireTime: getInt64(memberMap["title_expire_time"]),
				Unfriendly:      getBool(memberMap["unfriendly"]),
				CardChangeable:  getBool(memberMap["card_changeable"]),
				IsRobot:         getBool(memberMap["is_robot"]),
				ShutUpTimestamp: getInt64(memberMap["shut_up_timestamp"]),
				Role:            getString(memberMap["role"]),
				Title:           getString(memberMap["title"]),
			}
			members = append(members, member)
		}
	default:
		return nil, fmt.Errorf("get_group_member_list returned unexpected response type %T", data)
	}

	return members, nil
}

func (a *OneBotAdapter) GetFriendList() ([]*adapter.FriendInfo, error) {
	data, err := a.SendApi("get_friend_list", nil)
	if err != nil {
		return nil, err
	}

	var friends []*adapter.FriendInfo
	switch d := data.(type) {
	case []interface{}:
		for _, item := range d {
			if friendMap, ok := item.(map[string]interface{}); ok {
				friend := &adapter.FriendInfo{
					UserID:   getInt64(friendMap["user_id"]),
					Nickname: getString(friendMap["nickname"]),
					Remark:   getString(friendMap["remark"]),
					Sex:      getString(friendMap["sex"]),
					Level:    getInt(friendMap["level"]),
				}
				friends = append(friends, friend)
			}
		}
	}

	return friends, nil
}

func (a *OneBotAdapter) GetStrangerInfo(userID int64) (map[string]interface{}, error) {
	data, err := a.SendApi("get_stranger_info", map[string]interface{}{
		"user_id":  userID,
		"no_cache": false,
	})
	if err != nil {
		return nil, err
	}

	if m, ok := data.(map[string]interface{}); ok {
		returnedUserID := getInt64(m["user_id"])
		if returnedUserID != userID {
			return nil, fmt.Errorf("get_stranger_info returned invalid user_id %d for query %d", returnedUserID, userID)
		}
		return m, nil
	}

	return nil, nil
}

func (a *OneBotAdapter) GetGroupInfo(groupID int64) (*adapter.GroupInfo, error) {
	data, err := a.SendApi("get_group_info", map[string]interface{}{
		"group_id": groupID,
		"no_cache": false,
	})
	if err != nil {
		return nil, err
	}

	if groupMap, ok := data.(map[string]interface{}); ok {
		returnedGroupID := getInt64(groupMap["group_id"])
		if returnedGroupID != groupID {
			return nil, fmt.Errorf("get_group_info returned invalid group_id %d for query %d", returnedGroupID, groupID)
		}
		return &adapter.GroupInfo{
			Uin:             returnedGroupID,
			Code:            returnedGroupID,
			GroupID:         returnedGroupID,
			Name:            getString(groupMap["group_name"]),
			GroupName:       getString(groupMap["group_name"]),
			MemberCount:     getInt(groupMap["member_count"]),
			MaxMemberCount:  getInt(groupMap["max_member_count"]),
			GroupCreateTime: getInt64FromMap(groupMap, "group_create_time", "groupCreateTime"),
			GroupLevel:      getIntFromMap(groupMap, "group_level", "groupGrade"),
			OwnerUin:        getInt64FromMap(groupMap, "owner_id", "ownerUin"),
		}, nil
	}

	return nil, nil
}

func (a *OneBotAdapter) GetGroupMemberInfo(groupID, userID int64) (*adapter.GroupMemberInfo, error) {
	data, err := a.SendApi("get_group_member_info", map[string]interface{}{
		"group_id": groupID,
		"user_id":  userID,
		"no_cache": false,
	})
	if err != nil {
		return nil, err
	}

	if memberMap, ok := data.(map[string]interface{}); ok {
		returnedGroupID := getInt64(memberMap["group_id"])
		returnedUserID := getInt64(memberMap["user_id"])
		if returnedGroupID != groupID || returnedUserID != userID {
			return nil, fmt.Errorf("get_group_member_info returned identity group_id=%d user_id=%d for query group_id=%d user_id=%d", returnedGroupID, returnedUserID, groupID, userID)
		}
		return &adapter.GroupMemberInfo{
			GroupID:         returnedGroupID,
			UserID:          returnedUserID,
			Nickname:        getString(memberMap["nickname"]),
			Card:            getString(memberMap["card"]),
			Sex:             getString(memberMap["sex"]),
			Age:             getInt(memberMap["age"]),
			Area:            getString(memberMap["area"]),
			Level:           getInt(memberMap["level"]),
			QQLevel:         int16(getInt(memberMap["qq_level"])),
			JoinTime:        getInt64(memberMap["join_time"]),
			LastSentTime:    getInt64(memberMap["last_sent_time"]),
			TitleExpireTime: getInt64(memberMap["title_expire_time"]),
			Unfriendly:      getBool(memberMap["unfriendly"]),
			CardChangeable:  getBool(memberMap["card_changeable"]),
			IsRobot:         getBool(memberMap["is_robot"]),
			ShutUpTimestamp: getInt64(memberMap["shut_up_timestamp"]),
			Role:            getString(memberMap["role"]),
			Title:           getString(memberMap["title"]),
		}, nil
	}

	return nil, fmt.Errorf("get_group_member_info returned unexpected response type %T", data)
}

func (a *OneBotAdapter) DownloadFile(url, base64, name string, headers []string) (string, error) {
	params := map[string]interface{}{
		"url":    url,
		"name":   name,
		"base64": base64,
	}
	if len(headers) > 0 {
		params["headers"] = headers
	}

	data, err := a.SendApi("download_file", params)
	if err != nil {
		return "", err
	}

	if dataMap, ok := data.(map[string]interface{}); ok {
		if filePath, ok := dataMap["file"].(string); ok {
			return filePath, nil
		}
	}
	return "", nil
}

func (a *OneBotAdapter) GetFileUrl(groupCode int64, fileId string) string {
	params := map[string]interface{}{
		"group_id": groupCode,
		"file_id":  fileId,
	}

	data, err := a.SendApi("get_group_files_folder_files", params)
	if err != nil {
		return ""
	}

	if dataMap, ok := data.(map[string]interface{}); ok {
		if files, ok := dataMap["files"].([]interface{}); ok && len(files) > 0 {
			if firstFile, ok := files[0].(map[string]interface{}); ok {
				if url, ok := firstFile["url"].(string); ok {
					return url
				}
			}
		}
	}
	return ""
}

func (a *OneBotAdapter) GetMsgOrg(msgId int32) (interface{}, error) {
	params := map[string]interface{}{
		"message_id": msgId,
	}

	data, err := a.SendApi("get_msg", params)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (a *OneBotAdapter) GetMsg(msgId int32) (*adapter.GetMsgResult, error) {
	params := map[string]interface{}{
		"message_id": msgId,
	}

	data, err := a.SendApi("get_msg", params)
	if err != nil {
		return nil, err
	}

	msgMap, ok := data.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid message data format")
	}

	result := &adapter.GetMsgResult{
		MessageID:  getInt64(msgMap["message_id"]),
		GroupID:    getInt64(msgMap["group_id"]),
		UserID:     getInt64(msgMap["user_id"]),
		RawMessage: getString(msgMap["raw_message"]),
		Time:       getInt64(msgMap["time"]),
	}

	if msgMap["message"] != nil {
		segments := adapter.ParseMessageSegments(msgMap["message"])
		result.Elements = adapter.ConvertToMessageElements(segments)
	} else if msgMap["raw_message"] != nil {
		segments := adapter.ParseMessageSegments(getString(msgMap["raw_message"]))
		result.Elements = adapter.ConvertToMessageElements(segments)
	}

	if sender, ok := msgMap["sender"].(map[string]interface{}); ok {
		uid := getInt64(sender["user_id"])
		result.Sender = &adapter.SenderInfo{
			UserID:   uid,
			Uin:      uid,
			Nickname: getString(sender["nickname"]),
			Card:     getString(sender["card"]),
			Role:     getString(sender["role"]),
		}
	}

	return result, nil
}

func (a *OneBotAdapter) RecallMsg(msgId int32) error {
	params := map[string]interface{}{
		"message_id": msgId,
	}

	_, err := a.SendApi("delete_msg", params)
	return err
}

func (a *OneBotAdapter) GroupPoke(groupCode, target int64) error {
	_, err := a.SendApi("group_poke", map[string]interface{}{
		"group_id": groupCode,
		"user_id":  target,
	})
	return err
}

func (a *OneBotAdapter) FriendPoke(target int64) error {
	_, err := a.SendApi("friend_poke", map[string]interface{}{
		"user_id": target,
	})
	return err
}

func (a *OneBotAdapter) SetGroupBan(groupCode, memberUin int64, duration int64) error {
	_, err := a.SendApi("set_group_ban", map[string]interface{}{
		"group_id": groupCode,
		"user_id":  memberUin,
		"duration": duration,
	})
	return err
}

func (a *OneBotAdapter) SetGroupWholeBan(groupCode int64, enable bool) error {
	_, err := a.SendApi("set_group_whole_ban", map[string]interface{}{
		"group_id": groupCode,
		"enable":   enable,
	})
	return err
}

func (a *OneBotAdapter) KickGroupMember(groupCode int64, memberUin int64, rejectAddRequest bool) error {
	_, err := a.SendApi("set_group_kick", map[string]interface{}{
		"group_id":           groupCode,
		"user_id":            memberUin,
		"reject_add_request": rejectAddRequest,
	})
	return err
}

func (a *OneBotAdapter) SetGroupLeave(groupCode int64, isDismiss bool) error {
	_, err := a.SendApi("set_group_leave", map[string]interface{}{
		"group_id":   groupCode,
		"is_dismiss": isDismiss,
	})
	return err
}

func (a *OneBotAdapter) SetGroupAddRequest(flag string, approve bool, reason string) error {
	_, err := a.SendApi("set_group_add_request", map[string]interface{}{
		"flag":    flag,
		"approve": approve,
		"reason":  reason,
	})
	return err
}

func (a *OneBotAdapter) SetFriendAddRequest(flag string, approve bool) error {
	_, err := a.SendApi("set_friend_add_request", map[string]interface{}{
		"flag":    flag,
		"approve": approve,
	})
	return err
}

func (a *OneBotAdapter) SetGroupAdmin(groupCode, memberUin int64, enable bool) error {
	_, err := a.SendApi("set_group_admin", map[string]interface{}{
		"group_id": groupCode,
		"user_id":  memberUin,
		"enable":   enable,
	})
	return err
}

func (a *OneBotAdapter) EditGroupCard(groupCode, memberUin int64, card string) error {
	_, err := a.SendApi("set_group_card", map[string]interface{}{
		"group_id": groupCode,
		"user_id":  memberUin,
		"card":     card,
	})
	return err
}

func (a *OneBotAdapter) EditGroupTitle(groupCode, memberUin int64, title string) error {
	_, err := a.SendApi("set_group_special_title", map[string]interface{}{
		"group_id":      groupCode,
		"user_id":       memberUin,
		"special_title": title,
	})
	return err
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

func getInt64(v interface{}) int64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int64(val)
	case string:
		n, _ := strconv.ParseInt(val, 10, 64)
		return n
	case int64:
		return val
	case int:
		return int64(val)
	default:
		return 0
	}
}

func getInt(v interface{}) int {
	return int(getInt64(v))
}

func getInt64FromMap(m map[string]interface{}, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if n := getInt64(v); n != 0 {
				return n
			}
		}
	}
	return 0
}

func getIntFromMap(m map[string]interface{}, keys ...string) int {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if n := getInt(v); n != 0 {
				return n
			}
		}
	}
	return 0
}

func getBool(v interface{}) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case float64:
		return val != 0
	default:
		return false
	}
}

func getMap(v interface{}) map[string]interface{} {
	if m, ok := v.(map[string]interface{}); ok {
		return m
	}
	return nil
}

func getFile(v interface{}) adapter.GroupFile {
	var file adapter.GroupFile
	data, err := json.Marshal(v)
	if err != nil {
		return file
	}
	err = json.Unmarshal(data, &file)
	if err != nil {
		return file
	}
	replaceEmpty := func(primary, alternative string) string {
		if primary == "" {
			return alternative
		}
		return primary
	}
	replaceZero := func(primary, alternative int64) int64 {
		if primary == 0 {
			return alternative
		}
		return primary
	}
	file.FileName = replaceEmpty(file.FileName, file.AltFileName)
	file.FileId = replaceEmpty(file.FileId, file.AltFildId)
	file.FileUrl = replaceEmpty(file.FileUrl, file.AltFileUrl)
	file.FileSize = replaceZero(file.FileSize, file.AltFileSize)
	return file
}
