package onebot

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/Mrs4s/MiraiGo/client"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("adapter", "onebot-v11")

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

	wsClient.SetMessageHandler(a.handleMessage)

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

	resp, err := a.wsClient.SendAndWait(action, params, a.config.Timeout)
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

	if msg["message"] != nil {
		event.Message = adapter.ParseMessageSegments(msg["message"])
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
		NoticeType:   getString(msg["notice_type"]),
		Time:         getInt64(msg["time"]),
		SelfID:       getInt64(msg["self_id"]),
		GroupID:      getInt64(msg["group_id"]),
		UserID:       getInt64(msg["user_id"]),
		OperatorID:   getInt64(msg["operator_id"]),
		Duration:     int32(getInt64(msg["duration"])),
		SubType:      getString(msg["sub_type"]),
		Title:        getString(msg["title"]),
		MessageID:    getInt64(msg["message_id"]),
		File:         getFile(msg["file"]),
		OperatorNick: getString(msg["operator_nick"]),
		Times:        getInt(msg["times"]),
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
	}

	a.handlersMu.RLock()
	handlers := a.requestEventHandlers
	a.handlersMu.RUnlock()

	for _, handler := range handlers {
		handler(event)
	}
}

func (a *OneBotAdapter) GetGroupList() ([]*adapter.GroupInfo, error) {
	data, err := a.SendApi("get_group_list", nil)
	if err != nil {
		return nil, err
	}

	var groups []*adapter.GroupInfo
	switch d := data.(type) {
	case []interface{}:
		for _, item := range d {
			if groupMap, ok := item.(map[string]interface{}); ok {
				groupID := getInt64(groupMap["group_id"])
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
				}
				groups = append(groups, group)
			}
		}
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
			if memberMap, ok := item.(map[string]interface{}); ok {
				member := &adapter.GroupMemberInfo{
					GroupID:         getInt64(memberMap["group_id"]),
					UserID:          getInt64(memberMap["user_id"]),
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
		}
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
		return m, nil
	}

	return nil, nil
}

func (a *OneBotAdapter) GetGroupInfo(groupID int64) (*adapter.GroupInfo, error) {
	data, err := a.SendApi("get_group_info", map[string]interface{}{
		"group_id": groupID,
	})
	if err != nil {
		return nil, err
	}

	if groupMap, ok := data.(map[string]interface{}); ok {
		groupID := getInt64(groupMap["group_id"])
		return &adapter.GroupInfo{
			Uin:             groupID,
			Code:            groupID,
			GroupID:         groupID,
			Name:            getString(groupMap["group_name"]),
			GroupName:       getString(groupMap["group_name"]),
			MemberCount:     getInt(groupMap["member_count"]),
			MaxMemberCount:  getInt(groupMap["max_member_count"]),
			GroupCreateTime: getInt64(groupMap["group_create_time"]),
			GroupLevel:      getInt(groupMap["group_level"]),
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
		return &adapter.GroupMemberInfo{
			GroupID:         getInt64(memberMap["group_id"]),
			UserID:          getInt64(memberMap["user_id"]),
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

	return nil, nil
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

func (a *OneBotAdapter) GetMsg(msgId int32) (interface{}, error) {
	params := map[string]interface{}{
		"message_id": msgId,
	}

	data, err := a.SendApi("get_msg", params)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (a *OneBotAdapter) RecallMsg(msgId int32) error {
	params := map[string]interface{}{
		"message_id": msgId,
	}

	_, err := a.SendApi("delete_msg", params)
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

func getFile(v interface{}) client.GroupFile {
	var file client.GroupFile
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
