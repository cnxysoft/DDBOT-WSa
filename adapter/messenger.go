package adapter

import (
	"errors"
	"fmt"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/utils/qqlog"
	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type BotEventDispatcher interface {
	DispatchGroupMessage(msg *GroupMessage)
	DispatchPrivateMessage(msg *PrivateMessage)
	DispatchGroupRecall(event *GroupMessageRecalledEvent)
	DispatchFriendRecall(event *FriendMessageRecalledEvent)
	DispatchGroupMute(event *GroupMuteEvent)
	DispatchDisconnected(event *ClientDisconnectedEvent)
	DispatchGroupMemberJoin(event *MemberJoinGroupEvent)
	DispatchGroupMemberLeave(event *MemberLeaveGroupEvent)
	DispatchGroupJoin(event *GroupInfo)
	DispatchGroupLeave(event *GroupLeaveEvent)
	DispatchGroupMemberPermissionChanged(event *MemberPermissionChangedEvent)
	DispatchMemberCardUpdated(event *MemberCardUpdatedEvent)
	DispatchMemberSpecialTitleUpdated(event *MemberSpecialTitleUpdatedEvent)
	DispatchGroupUploadNotify(event *GroupUploadNotifyEvent)
	DispatchGroupNotify(event NotifyEvent)
	DispatchFriendNotify(event NotifyEvent)
	DispatchGroupNameUpdated(event *GroupNameUpdatedEvent)
	DispatchGroupEssenceChanged(event *GroupDigestEvent)
	DispatchGroupDisband(event *GroupDisbandEvent)
	DispatchNewFriendRequest(event *NewFriendRequest)
	DispatchNewFriend(event *NewFriendEvent)
	DispatchUserJoinGroupRequest(event *UserJoinGroupRequest)
	DispatchGroupInvitedRequest(event *GroupInvitedRequest)
	DispatchBotOnline(event *BotOnlineEvent)
	DispatchBotOffline(event *BotOfflineEvent)
	DispatchGroupMsgEmojiLike(event *GroupMsgEmojiLikeEvent)
	DispatchProfileLike(event *ProfileLikeEvent)
	DispatchPokeRecall(event *PokeRecallEvent)
}

var messengerLogger = logrus.WithField("module", "messenger")

const (
	// 消息分片限制
	MaxTextLength = 4500 // 文本最大长度
	MaxImageCount = 20   // 图片最大数量

	// 离线消息队列上限
	offlineQueueMaxSize = 100
)

var offlineQueueRetryDelay = 5 * time.Second

type SendResp struct {
	RetMSG *GroupMessage
	Error  error
	// Queued 表示消息已进入离线队列等待重发（尚未真正投递）
	Queued bool
}

// GroupSendStatus 表示群消息发送的最终状态
type GroupSendStatus int

const (
	// GroupSendSent 已发送成功（RetMSG.ID >= 0）
	GroupSendSent GroupSendStatus = iota
	// GroupSendQueued 已进入离线队列，稍后重发（尚未真正投递）
	GroupSendQueued
	// GroupSendNotSent 未发送：写入前失败且未入离线队列，可安全重试
	GroupSendNotSent
	// GroupSendUnknown 发送结果未知：请求可能已到达 OneBot，重试可能造成重复
	GroupSendUnknown
	// GroupSendRejected 被 OneBot 明确拒绝，重试无意义
	GroupSendRejected
)

// Status 根据 Error 与 Queued 计算明确的群消息发送状态
func (r SendResp) Status() GroupSendStatus {
	switch {
	case r.Queued:
		return GroupSendQueued
	case r.Error == nil:
		return GroupSendSent
	case errors.Is(r.Error, ErrRequestNotSent):
		return GroupSendNotSent
	case errors.Is(r.Error, ErrRequestRejected):
		return GroupSendRejected
	default:
		return GroupSendUnknown
	}
}

// PrivateSendStatus 表示私聊消息发送的最终状态
type PrivateSendStatus int

const (
	// PrivateSendSent 已发送成功（RetMSG.ID >= 0）
	PrivateSendSent PrivateSendStatus = iota
	// PrivateSendQueued 已进入离线队列，稍后重发（尚未真正投递）
	PrivateSendQueued
	// PrivateSendNotSent 未发送：写入前失败且未入离线队列，可安全重试
	PrivateSendNotSent
	// PrivateSendUnknown 发送结果未知：请求可能已到达 OneBot，重试可能造成重复
	PrivateSendUnknown
	// PrivateSendRejected 被 OneBot 明确拒绝，重试无意义
	PrivateSendRejected
)

// PrivateSendResp 私聊消息发送结果
// 与 SendResp 对齐，显式携带 Error 供调用方区分发送状态，
// 避免调用方仅凭 ID/连接状态推断结果造成误判。
type PrivateSendResp struct {
	RetMSG *PrivateMessage
	Error  error
	// Queued 表示消息已进入离线队列等待重发（尚未真正投递）
	Queued bool
}

// Status 根据 Error 与 Queued 计算明确的发送状态
func (r PrivateSendResp) Status() PrivateSendStatus {
	switch {
	case r.Queued:
		return PrivateSendQueued
	case r.Error == nil:
		return PrivateSendSent
	case errors.Is(r.Error, ErrRequestNotSent):
		return PrivateSendNotSent
	case errors.Is(r.Error, ErrRequestRejected):
		return PrivateSendRejected
	default:
		return PrivateSendUnknown
	}
}

// offlineQueueMsg 离线消息结构
// TargetType: "group" 表示群消息, "private" 表示私聊消息
type offlineQueueMsg struct {
	TargetId   int64
	TargetType string
	Message    *SendingMessage
	NewStr     string
	CreatedAt  time.Time
}

type Messenger struct {
	Adapter Adapter

	// Uin 会被 lifecycle 事件 goroutine 写入、其他 goroutine 频繁读取，必须原子访问
	Uin    atomic.Int64
	Online atomic.Bool

	GroupList  []*GroupInfo
	FriendList []*FriendInfo
	groupMu    sync.RWMutex
	friendMu   sync.RWMutex

	stopChan chan struct{}
	wg       sync.WaitGroup

	eventDispatcher BotEventDispatcher

	// 消息统计
	groupMsgCount    atomic.Int64
	privateMsgCount  atomic.Int64
	groupSendCount   atomic.Int64
	privateSendCount atomic.Int64

	// 离线消息队列
	offlineQueue          []offlineQueueMsg
	offlineQueueMu        sync.Mutex
	offlineQueueFlushMu   sync.Mutex
	offlineFlushScheduled atomic.Bool

	// listReloadRetryActive 标记列表重载重试协程是否已启动，防止重复启动
	listReloadRetryActive atomic.Bool

	// listLoaded 标记好友/群/群成员列表是否已完成首次加载
	listLoaded atomic.Bool
}

// 列表加载重试参数
var (
	listReloadRetryInterval = 15 * time.Second
	listReloadMaxRetries    = 10
)

func NewMessenger(adapter Adapter) *Messenger {
	m := &Messenger{
		Adapter:    adapter,
		stopChan:   make(chan struct{}),
		GroupList:  make([]*GroupInfo, 0),
		FriendList: make([]*FriendInfo, 0),
	}

	m.registerEventHandlers()

	// 启动统计汇总定时器
	go m.summaryTicker()

	return m
}

// summaryTicker 每分钟输出一次消息统计汇总
func (m *Messenger) summaryTicker() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !qqlog.Enabled {
				// qq-logs 未启用，输出统计到主日志
				messengerLogger.Infof("消息统计: 收群消息 %d, 收私聊 %d, 发群消息 %d, 发私聊 %d",
					m.groupMsgCount.Load(), m.privateMsgCount.Load(),
					m.groupSendCount.Load(), m.privateSendCount.Load())
			}
		case <-m.stopChan:
			return
		}
	}
}

func (m *Messenger) SetBotEventDispatcher(dispatcher BotEventDispatcher) {
	m.eventDispatcher = dispatcher
}

func (m *Messenger) registerEventHandlers() {
	m.Adapter.OnGroupMessage(func(event *GroupMessageEvent) {
		m.handleGroupMessage(event)
	})

	m.Adapter.OnPrivateMessage(func(event *PrivateMessageEvent) {
		m.handlePrivateMessage(event)
	})

	m.Adapter.OnMetaEvent(func(event *MetaEvent) {
		if event.MetaEventType == "lifecycle" {
			m.Uin.Store(event.SelfID)
			wasOnline := m.Online.Swap(true)
			messengerLogger.Infof("Bot online: %d", m.Uin.Load())
			// 重连（lifecycle 事件）时同样刷新离线队列，避免心跳未翻转导致缓存消息滞留
			if !wasOnline && getOfflineQueueEnable() {
				go m.flushOfflineQueue()
			}
			// Lifecycle事件触发时立即刷新好友、群组、群员信息
			go func() {
				if err := m.RefreshList(); err != nil {
					messengerLogger.WithError(err).Error("refresh list failed")
				}
			}()
		} else if event.MetaEventType == "heartbeat" {
			if status, ok := event.Status["online"].(bool); ok {
				wasOnline := m.Online.Load()
				m.Online.Store(status)
				if !wasOnline && status {
					messengerLogger.Info("Bot online")
					if getOfflineQueueEnable() {
						go m.flushOfflineQueue()
					}
				} else if wasOnline && !status {
					messengerLogger.Warn("Bot offline")
				}
			}
		}
	})

	m.Adapter.OnNoticeEvent(func(event *NoticeEvent) {
		m.handleNoticeEvent(event)
	})

	m.Adapter.OnRequestEvent(func(event *RequestEvent) {
		m.handleRequestEvent(event)
	})
}

func (m *Messenger) Start() error {
	return m.Adapter.Start()
}

func (m *Messenger) Stop() error {
	close(m.stopChan)
	m.wg.Wait()
	return m.Adapter.Stop()
}

func (m *Messenger) GetUin() int64 {
	return m.Uin.Load()
}

func (m *Messenger) GetSelfID() int64 {
	return m.Adapter.GetSelfID()
}

func (m *Messenger) SendGroupMessage(groupCode int64, msg *SendingMessage, newstr string) SendResp {
	// 检查离线队列条件（账号在线且 WS 已连接）
	if getOfflineQueueEnable() && !m.isConnected() {
		messengerLogger.Warnf("BOT已离线，已开启离线缓存，将暂存消息: %s", sliceMessage(newstr))
		m.saveOfflineMsg(newOfflineQueueMsg(groupCode, "group", msg, newstr))
		m.scheduleOfflineQueueFlush(offlineQueueRetryDelay)
		return SendResp{RetMSG: &GroupMessage{ID: -1}, Queued: true}
	}

	// 获取群名称
	groupName := "未知群聊"
	if group := m.FindGroup(groupCode); group != nil {
		groupName = group.Name
	}

	// 记录发送日志
	if qqlog.Logger != nil {
		qqlog.Logger.Infof("发送 群消息 给 %s(%d): %s", groupName, groupCode, newstr)
	}

	// 构建消息分片
	chunks := m.buildMessageChunks(msg)

	// 空分片时返回明确错误，避免返回零值 SendResp{RetMSG:nil} 导致调用方解引用 panic
	if len(chunks) == 0 {
		messengerLogger.Warnf("Send group message: 消息无可构建分片，跳过发送 (group=%d)", groupCode)
		return SendResp{RetMSG: &GroupMessage{ID: -1}, Error: errors.New("no message chunks to send")}
	}

	var lastResult SendResp
	for i, chunk := range chunks {
		chunkMsg := &SendingMessage{Elements: parseChunkToElements(chunk)}
		messages := m.buildMessageSegments(chunkMsg)

		// 分片之间添加延迟，避免发送过快
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		msgID, err := m.Adapter.SendGroupMessage(groupCode, messages)
		m.groupSendCount.Add(1)
		if err != nil {
			messengerLogger.Errorf("Send group message failed (chunk %d/%d): %v", i+1, len(chunks), err)
			if errors.Is(err, ErrRequestResultUnknown) {
				messengerLogger.Warnf("群消息发送结果未知，跳过自动重试以避免重复消息 (chunk %d/%d)", i+1, len(chunks))
			} else if errors.Is(err, ErrRequestRejected) {
				messengerLogger.Warnf("群消息被OneBot明确拒绝，不再自动重试 (chunk %d/%d)", i+1, len(chunks))
			} else if errors.Is(err, ErrRequestNotSent) && getOfflineQueueEnable() {
				m.queueUnsentChunks(groupCode, "group", chunks, i, newstr)
				return SendResp{
					RetMSG: &GroupMessage{ID: -1},
					Error:  err,
					Queued: true,
				}
			} else if !errors.Is(err, ErrRequestNotSent) {
				messengerLogger.Warnf("群消息发送错误未明确标记为写入前失败，不自动重试 (chunk %d/%d)", i+1, len(chunks))
			}
			return SendResp{
				RetMSG: &GroupMessage{ID: -1},
				Error:  err,
			}
		} else {
			lastResult = SendResp{
				RetMSG: &GroupMessage{
					ID:        int64(msgID),
					GroupCode: groupCode,
					Sender: &SenderInfo{
						UserID: m.Uin.Load(),
						Uin:    m.Uin.Load(),
					},
					Elements: chunkMsg.Elements,
				},
				Error: nil,
			}
		}
	}

	return lastResult
}

func (m *Messenger) SendPrivateMessage(target int64, msg *SendingMessage, newstr string) PrivateSendResp {
	// 检查离线队列条件（账号在线且 WS 已连接）
	if getOfflineQueueEnable() && !m.isConnected() {
		messengerLogger.Warnf("BOT已离线，已开启离线缓存，将暂存私聊消息: %s", sliceMessage(newstr))
		m.saveOfflineMsg(newOfflineQueueMsg(target, "private", msg, newstr))
		m.scheduleOfflineQueueFlush(offlineQueueRetryDelay)
		return PrivateSendResp{RetMSG: &PrivateMessage{ID: -1}, Queued: true}
	}

	// 获取好友昵称
	nickname := "未知用户"
	if friend := m.FindFriend(target); friend != nil {
		nickname = friend.Nickname
	}

	// 记录发送日志
	if qqlog.Logger != nil {
		qqlog.Logger.Infof("发送 私聊消息 给 %s(%d): %s", nickname, target, newstr)
	}

	// 构建消息分片
	chunks := m.buildMessageChunks(msg)

	// 空分片时返回明确错误，避免发送无内容的假成功结果
	if len(chunks) == 0 {
		messengerLogger.Warnf("Send private message: 消息无可构建分片，跳过发送 (target=%d)", target)
		return PrivateSendResp{
			RetMSG: &PrivateMessage{ID: -1, UserID: target, Self: m.Uin.Load(), Elements: msg.Elements},
			Error:  errors.New("no message chunks to send"),
		}
	}

	var lastMsgID int32 = -1
	for i, chunk := range chunks {
		// 构建新的 SendingMessage
		chunkMsg := &SendingMessage{Elements: parseChunkToElements(chunk)}
		messages := m.buildMessageSegments(chunkMsg)

		// 分片之间添加延迟，避免发送过快
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		msgID, err := m.Adapter.SendPrivateMessage(target, messages)
		m.privateSendCount.Add(1)
		if err != nil {
			messengerLogger.Errorf("Send private message failed (chunk %d/%d): %v", i+1, len(chunks), err)
			if errors.Is(err, ErrRequestResultUnknown) {
				messengerLogger.Warnf("私聊消息发送结果未知，跳过自动重试以避免重复消息 (chunk %d/%d)", i+1, len(chunks))
			} else if errors.Is(err, ErrRequestRejected) {
				messengerLogger.Warnf("私聊消息被OneBot明确拒绝，不再自动重试 (chunk %d/%d)", i+1, len(chunks))
			} else if errors.Is(err, ErrRequestNotSent) && getOfflineQueueEnable() {
				m.queueUnsentChunks(target, "private", chunks, i, newstr)
				return PrivateSendResp{
					RetMSG: &PrivateMessage{ID: -1, UserID: target, Self: m.Uin.Load(), Elements: msg.Elements},
					Error:  err,
					Queued: true,
				}
			} else if !errors.Is(err, ErrRequestNotSent) {
				messengerLogger.Warnf("私聊消息发送错误未明确标记为写入前失败，不自动重试 (chunk %d/%d)", i+1, len(chunks))
			}
			return PrivateSendResp{
				RetMSG: &PrivateMessage{ID: -1, UserID: target, Self: m.Uin.Load(), Elements: msg.Elements},
				Error:  err,
			}
		} else {
			lastMsgID = msgID
		}
	}

	return PrivateSendResp{
		RetMSG: &PrivateMessage{
			ID:     int64(lastMsgID),
			UserID: target,
			Self:   m.Uin.Load(),
			Sender: &SenderInfo{
				UserID: m.Uin.Load(),
				Uin:    m.Uin.Load(),
			},
			Elements: msg.Elements,
		},
	}
}

// queueUnsentChunks 缓存发送失败的当前分片及尚未尝试的分片。
// 已经成功发送的前序分片不会再次入队。
func (m *Messenger) queueUnsentChunks(targetID int64, targetType string, chunks [][]MessageSegment, start int, newstr string) {
	for _, chunk := range chunks[start:] {
		chunkMsg := &SendingMessage{Elements: parseChunkToElements(chunk)}
		m.saveOfflineMsg(newOfflineQueueMsg(targetID, targetType, chunkMsg, newstr))
	}
	m.scheduleOfflineQueueFlush(offlineQueueRetryDelay)
}

func (m *Messenger) SendGroupForwardMessage(groupCode int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error) {
	if m.Adapter == nil {
		return -1, "", fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.SendGroupForwardMessage(groupCode, nodes, options)
}

func (m *Messenger) SendPrivateForwardMessage(userID int64, nodes []map[string]interface{}, options *ForwardOptions) (int32, string, error) {
	if m.Adapter == nil {
		return -1, "", fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.SendPrivateForwardMessage(userID, nodes, options)
}

func (m *Messenger) buildMessageSegments(msg *SendingMessage) []MessageSegment {
	var segments []MessageSegment

	for _, elem := range msg.Elements {
		switch e := elem.(type) {
		case *TextSegment:
			segments = append(segments, MessageSegment{
				Type: "text",
				Data: map[string]interface{}{"text": e.Content},
			})
		case *AtSegment:
			qq := "all"
			if e.Target != 0 {
				qq = fmt.Sprintf("%d", e.Target)
			}
			segments = append(segments, MessageSegment{
				Type: "at",
				Data: map[string]interface{}{"qq": qq},
			})
		case *FaceSegment:
			segments = append(segments, MessageSegment{
				Type: "face",
				Data: map[string]interface{}{"id": e.Index},
			})
		case *ImageSegment:
			file := e.File
			if file == "" {
				file = e.Url
			}
			segments = append(segments, MessageSegment{
				Type: "image",
				Data: map[string]interface{}{
					"file": file,
					"url":  e.Url,
				},
			})
		case *VoiceSegment:
			segments = append(segments, MessageSegment{
				Type: "record",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.Url,
				},
			})
		case *ReplySegment:
			segments = append(segments, MessageSegment{
				Type: "reply",
				Data: map[string]interface{}{"id": e.ReplySeq},
			})
		case *ForwardSegment:
			segments = append(segments, MessageSegment{
				Type: "forward",
				Data: map[string]interface{}{"id": e.ResId},
			})
		case *JsonSegment:
			segments = append(segments, MessageSegment{
				Type: "json",
				Data: map[string]interface{}{"data": e.Content},
			})
		case *FileSegment:
			segments = append(segments, MessageSegment{
				Type: "file",
				Data: map[string]interface{}{
					"name": e.Name,
					"id":   e.Id,
					"url":  e.Url,
					"file": e.Path,
				},
			})
		case *VideoSegment:
			segments = append(segments, MessageSegment{
				Type: "video",
				Data: map[string]interface{}{
					"name": e.Name,
					"file": e.Url,
				},
			})
		}
	}

	return segments
}

// isSingleElement 判断是否为独立发送类型（必须单独发送，不能与其他元素混合）
func isSingleElement(segment MessageSegment) bool {
	switch segment.Type {
	case "video", "file", "record", "forward":
		return true
	}
	return false
}

// 元素类型估计长度常量（用于分片计算）
const (
	// estimateAtLength at元素估计长度（实际为display字符串长度）
	estimateAtLength = 10
	// estimateReplyLength reply元素估计长度
	estimateReplyLength = 10
	// estimateFaceLength face元素估计长度
	estimateFaceLength = 5
)

// calculateTextLength 计算分片中文本的估计长度（at/reply 算作文本）
func calculateTextLength(segments []MessageSegment) int {
	length := 0
	for _, seg := range segments {
		switch seg.Type {
		case "text":
			if text, ok := seg.Data["text"].(string); ok {
				length += utf8.RuneCountInString(text)
			}
		case "at":
			length += estimateAtLength
		case "reply":
			length += estimateReplyLength
		case "face":
			length += estimateFaceLength
		}
	}
	return length
}

// countImages 计算分片中图片的数量
func countImages(segments []MessageSegment) int {
	count := 0
	for _, seg := range segments {
		if seg.Type == "image" {
			count++
		}
	}
	return count
}

type textSplitPart struct {
	Text string
	Hard bool
}

// splitTextSmartWithLimit 智能拆分文本，限制第一段长度不超过 limit
// 在 limit 范围内查找最佳切分点：优先\n，其次标点，最后硬切
func splitTextSmartWithLimit(text string, limit int) textSplitPart {
	if limit <= 0 {
		return textSplitPart{Hard: true}
	}

	runes := []rune(text)
	textLen := len(runes)

	if textLen <= limit {
		return textSplitPart{Text: text}
	}

	// 标点符号列表（中日英）
	punctList := []string{
		"。", "！", "？", "，", "、", "；", "：", "——", "…",
		".", "!", "?", ",", ";", ":", "-", "…",
	}

	const newlineThreshold = 100

	searchEnd := limit
	if searchEnd > textLen {
		searchEnd = textLen
	}

	newlinePos := -1
	for i := 0; i < searchEnd; i++ {
		if runes[i] == '\n' {
			newlinePos = i
		}
	}

	cutPos := -1
	if newlinePos > 0 && (limit-newlinePos) <= newlineThreshold {
		cutPos = newlinePos
	} else {
		punctPos := -1
		for _, punct := range punctList {
			punctRunes := []rune(punct)
			plen := len(punctRunes)
			for i := 0; i <= searchEnd-plen; i++ {
				found := true
				for j := range punctRunes {
					if runes[i+j] != punctRunes[j] {
						found = false
						break
					}
				}
				if found {
					pos := i + plen
					if pos <= limit && pos > punctPos {
						punctPos = pos
					}
				}
			}
		}

		if newlinePos > 0 && punctPos > 0 {
			if limit-punctPos <= limit-newlinePos {
				cutPos = punctPos
			} else {
				cutPos = newlinePos
			}
		} else if punctPos > 0 {
			cutPos = punctPos
		} else if newlinePos > 0 {
			cutPos = newlinePos
		}
	}

	if cutPos > 0 {
		return textSplitPart{Text: strings.TrimRight(string(runes[:cutPos]), "\n")}
	}

	return textSplitPart{Text: string(runes[:limit]), Hard: true}
}

// buildMessageChunks 将消息拆分为多个分片，每个分片符合发送限制
// 限制规则：
// 1. 文本长度不超过 MaxTextLength (4500)
// 2. 图片不超过 MaxImageCount (20)
// 3. 独立类型(video/file/record/forward)必须单独发送
// 4. 可组合类型尽量组合，直到超过限制才分片
func (m *Messenger) buildMessageChunks(msg *SendingMessage) [][]MessageSegment {
	segments := m.buildMessageSegments(msg)

	chunks := make([][]MessageSegment, 0, 10)
	var currentChunk []MessageSegment
	currentTextLen := 0
	currentImageCount := 0

	flush := func() {
		if len(currentChunk) > 0 {
			chunks = append(chunks, currentChunk)
			currentChunk = nil
			currentTextLen = 0
			currentImageCount = 0
		}
	}

	for _, seg := range segments {
		if isSingleElement(seg) {
			flush()
			chunks = append(chunks, []MessageSegment{seg})
			continue
		}

		switch seg.Type {
		case "text":
			text := getString(seg.Data["text"])
			textLen := utf8.RuneCountInString(text)

			for textLen > 0 {
				available := MaxTextLength - currentTextLen

				if available <= 0 {
					flush()
					continue
				}

				if textLen <= available {
					// 文本可以直接放入
					currentChunk = append(currentChunk, MessageSegment{
						Type: "text",
						Data: map[string]interface{}{"text": text},
					})
					currentTextLen += textLen
					break
				} else {
					// 文本需要切分，使用智能切片
					split := splitTextSmartWithLimit(text, available)
					part := split.Text
					if part == "" {
						flush()
						continue
					}

					currentChunk = append(currentChunk, MessageSegment{
						Type: "text",
						Data: map[string]interface{}{"text": part},
					})
					currentTextLen += utf8.RuneCountInString(part)
					flush()

					remainderRunes := []rune(text)[utf8.RuneCountInString(part):]
					remainder := string(remainderRunes)
					if !split.Hard {
						remainder = strings.TrimLeft(remainder, "\n")
					}
					text = remainder
					textLen = utf8.RuneCountInString(text)
				}
			}

		case "image":
			if currentImageCount >= MaxImageCount {
				flush()
			}
			currentChunk = append(currentChunk, seg)
			currentImageCount++

		case "at", "reply", "face":
			estLen := estimateAtLength
			if currentTextLen+estLen > MaxTextLength {
				flush()
			}
			currentChunk = append(currentChunk, seg)
			currentTextLen += estLen
		}
	}

	flush()
	return chunks
}

// parseChunkToElements 将 MessageSegment 分片转换回 adapter message element 数组
func parseChunkToElements(chunk []MessageSegment) []IMessageElement {
	var elements []IMessageElement

	for _, seg := range chunk {
		switch seg.Type {
		case "text":
			if text, ok := seg.Data["text"].(string); ok {
				elements = append(elements, &TextSegment{Content: text})
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
				} else {
					messengerLogger.Warnf("parse at target failed: %v, treating as @everyone", err)
					target = 0
				}
			}
			elements = append(elements, &AtSegment{Target: target})
		case "face":
			var faceId int64
			if id, ok := seg.Data["id"].(float64); ok {
				faceId = int64(id)
			} else if id, ok := seg.Data["id"].(string); ok {
				if parsedId, err := strconv.ParseInt(id, 10, 64); err == nil {
					faceId = parsedId
				} else {
					messengerLogger.Warnf("parse face id failed: %v, using 0", err)
				}
			}
			elements = append(elements, &FaceSegment{Index: int32(faceId)})
		case "image":
			elements = append(elements, &ImageSegment{
				File: getString(seg.Data["file"]),
				Url:  getString(seg.Data["url"]),
			})
		case "record":
			elements = append(elements, &VoiceSegment{
				Url:  getString(seg.Data["file"]),
				Name: getString(seg.Data["name"]),
			})
		case "reply":
			var replySeq int64
			id := getString(seg.Data["id"])
			if parsedId, err := strconv.ParseInt(id, 10, 64); err == nil {
				replySeq = parsedId
			} else {
				messengerLogger.Warnf("parse reply seq failed: %v, using 0", err)
			}
			elements = append(elements, &ReplySegment{ReplySeq: int32(replySeq)})
		case "json":
			if data, ok := seg.Data["data"].(string); ok {
				elements = append(elements, &JsonSegment{Content: data})
			}
		case "forward":
			if id, ok := seg.Data["id"].(string); ok {
				elements = append(elements, &ForwardSegment{ResId: id})
			}
		case "file":
			elements = append(elements, &FileSegment{
				Name: getString(seg.Data["name"]),
				Id:   getString(seg.Data["id"]),
				Url:  getString(seg.Data["url"]),
				Path: getString(seg.Data["file"]),
			})
		case "video":
			elements = append(elements, &VideoSegment{
				Name: getString(seg.Data["name"]),
				Url:  getString(seg.Data["file"]),
			})
		}
	}

	return elements
}

func (m *Messenger) FindGroup(code int64) *GroupInfo {
	m.groupMu.RLock()
	defer m.groupMu.RUnlock()

	for _, g := range m.GroupList {
		if g.Code == code {
			return g
		}
	}
	return nil
}

func (m *Messenger) FindGroupByUin(uin int64) *GroupInfo {
	m.groupMu.RLock()
	defer m.groupMu.RUnlock()

	for _, g := range m.GroupList {
		if g.Uin == uin {
			return g
		}
	}
	return nil
}

// FindGroupByUinLocked assumes the caller holds the lock
func (m *Messenger) FindGroupByUinLocked(uin int64) *GroupInfo {
	for _, g := range m.GroupList {
		if g.Uin == uin {
			return g
		}
	}
	return nil
}

func (m *Messenger) FindFriend(uin int64) *FriendInfo {
	if uin == m.Uin.Load() {
		return &FriendInfo{
			Uin:      uin,
			Nickname: "Bot",
		}
	}

	m.friendMu.RLock()
	defer m.friendMu.RUnlock()

	for _, f := range m.FriendList {
		if f.Uin == uin {
			return f
		}
	}
	return nil
}

func (m *Messenger) ReloadGroupList() error {
	groups, err := m.Adapter.GetGroupList()
	if err != nil {
		return err
	}

	m.groupMu.Lock()
	defer m.groupMu.Unlock()

	m.GroupList = make([]*GroupInfo, 0, len(groups))
	for _, g := range groups {
		m.GroupList = append(m.GroupList, &GroupInfo{
			Uin:             g.GroupID,
			Code:            g.GroupID,
			Name:            g.GroupName,
			MemberCount:     g.MemberCount,
			MaxMemberCount:  g.MaxMemberCount,
			GroupCreateTime: g.GroupCreateTime,
			GroupLevel:      g.GroupLevel,
			Members:         make([]*GroupMemberInfo, 0),
			Client:          m,
		})
	}

	messengerLogger.Infof("Reloaded %d groups", len(m.GroupList))
	return nil
}

func (m *Messenger) ReloadFriendList() error {
	friends, err := m.Adapter.GetFriendList()
	if err != nil {
		return err
	}

	m.friendMu.Lock()
	defer m.friendMu.Unlock()

	m.FriendList = make([]*FriendInfo, 0, len(friends))
	for _, f := range friends {
		m.FriendList = append(m.FriendList, &FriendInfo{
			Uin:      f.UserID,
			Nickname: f.Nickname,
			Remark:   f.Remark,
			Client:   m,
		})
	}

	messengerLogger.Infof("Reloaded %d friends", len(m.FriendList))
	return nil
}

func (m *Messenger) GetGroupMembers(group *GroupInfo) ([]*GroupMemberInfo, error) {
	return m.GetGroupMembersByID(group.Code)
}

func (m *Messenger) GetGroupMembersByID(groupID int64) ([]*GroupMemberInfo, error) {
	members, err := m.Adapter.GetGroupMemberList(groupID)
	if err != nil {
		return nil, err
	}

	result := make([]*GroupMemberInfo, 0, len(members))
	for _, mb := range members {
		perm := Member
		switch mb.Role {
		case "owner":
			perm = Owner
		case "admin":
			perm = Administrator
		}

		result = append(result, &GroupMemberInfo{
			Group:           m.FindGroupByUin(mb.GroupID),
			Uin:             mb.UserID,
			Nickname:        mb.Nickname,
			CardName:        mb.Card,
			JoinTime:        mb.JoinTime,
			LastSpeakTime:   mb.LastSentTime,
			SpecialTitle:    mb.Title,
			ShutUpTimestamp: mb.ShutUpTimestamp,
			Permission:      perm,
		})
	}

	group := m.FindGroupByUin(groupID)
	if group != nil {
		m.groupMu.Lock()
		group.Members = result
		m.groupMu.Unlock()
	}

	return result, nil
}

func (m *Messenger) GetStrangerInfo(uin int64) (map[string]interface{}, error) {
	return m.Adapter.GetStrangerInfo(uin)
}

// AddGroupMember adds a member to the group cache after receiving a group_increase event.
// It calls GetGroupMemberInfo to fetch complete member info before saving.
func (m *Messenger) AddGroupMember(groupID, userID int64) error {
	// Look up group BEFORE acquiring lock to avoid deadlock
	group := m.FindGroupByUin(groupID)
	if group == nil {
		return fmt.Errorf("group %d not found", groupID)
	}

	// Get complete member info from API
	memberInfo, err := m.Adapter.GetGroupMemberInfo(groupID, userID)
	if err != nil {
		return err
	}

	m.groupMu.Lock()
	defer m.groupMu.Unlock()

	// Check if member already exists
	for _, existing := range group.Members {
		if existing.Uin == userID {
			// Member already exists, update it with fresh info
			existing.Nickname = memberInfo.Nickname
			existing.CardName = memberInfo.Card
			switch memberInfo.Role {
			case "owner":
				existing.Permission = Owner
			case "admin":
				existing.Permission = Administrator
			default:
				existing.Permission = Member
			}
			return nil
		}
	}

	// Add new member with full info from API
	perm := Member
	switch memberInfo.Role {
	case "owner":
		perm = Owner
	case "admin":
		perm = Administrator
	}

	newMember := &GroupMemberInfo{
		Group:           group,
		Uin:             memberInfo.UserID,
		Nickname:        memberInfo.Nickname,
		CardName:        memberInfo.Card,
		JoinTime:        memberInfo.JoinTime,
		LastSentTime:    memberInfo.LastSentTime,
		LastSpeakTime:   memberInfo.LastSentTime,
		SpecialTitle:    memberInfo.Title,
		ShutUpTimestamp: memberInfo.ShutUpTimestamp,
		Permission:      perm,
	}
	group.Members = append(group.Members, newMember)
	messengerLogger.Debugf("AddGroupMember cache updated: group=%d member=%d", groupID, userID)
	return nil
}

// RemoveGroupMember removes a member from the group cache after receiving a group_decrease event
func (m *Messenger) RemoveGroupMember(groupID, userID int64) {
	// 先查找 group（不持有锁），避免在持有 groupMu.Lock() 的情况下调用需要 RLock 的 FindGroupByUin
	group := m.FindGroupByUin(groupID)
	if group == nil {
		return
	}
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	for i, member := range group.Members {
		if member.Uin == userID {
			group.Members = append(group.Members[:i], group.Members[i+1:]...)
			messengerLogger.Debugf("RemoveGroupMember cache updated: group=%d member=%d", groupID, userID)
			return
		}
	}
}

// UpdateGroupMember updates a member's info in the cache
func (m *Messenger) UpdateGroupMember(groupID, userID int64, updateFunc func(*GroupMemberInfo)) {
	group := m.FindGroupByUin(groupID)
	if group == nil {
		return
	}
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	for _, member := range group.Members {
		if member.Uin == userID {
			updateFunc(member)
			messengerLogger.Debugf("UpdateGroupMember cache updated: group=%d member=%d", groupID, userID)
			return
		}
	}
}

// RefreshMemberInfo fetches fresh member info from API and updates cache
func (m *Messenger) RefreshMemberInfo(groupID, userID int64) error {
	members, err := m.Adapter.GetGroupMemberList(groupID)
	if err != nil {
		return err
	}
	for _, mb := range members {
		if mb.UserID == userID {
			perm := Member
			switch mb.Role {
			case "owner":
				perm = Owner
			case "admin":
				perm = Administrator
			}
			group := m.FindGroupByUin(groupID)
			if group == nil {
				return fmt.Errorf("group %d not found", groupID)
			}
			m.groupMu.Lock()
			defer m.groupMu.Unlock()
			for _, member := range group.Members {
				if member.Uin == userID {
					member.Nickname = mb.Nickname
					member.CardName = mb.Card
					member.SpecialTitle = mb.Title
					member.Permission = perm
					messengerLogger.Debugf("RefreshMemberInfo cache updated: group=%d member=%d", groupID, userID)
					return nil
				}
			}
			return fmt.Errorf("member %d not found in group %d", userID, groupID)
		}
	}
	return fmt.Errorf("member %d not found in group %d response", userID, groupID)
}

func (m *Messenger) GetGroupInfo(groupCode int64) (*GroupInfo, error) {
	info, err := m.Adapter.GetGroupInfo(groupCode)
	if err != nil {
		return nil, err
	}

	return &GroupInfo{
		Uin:             info.GroupID,
		Code:            info.GroupID,
		Name:            info.GroupName,
		MemberCount:     info.MemberCount,
		MaxMemberCount:  info.MaxMemberCount,
		GroupCreateTime: info.GroupCreateTime,
		GroupLevel:      info.GroupLevel,
		OwnerUin:        info.OwnerUin,
		Client:          m,
	}, nil
}

func (m *Messenger) RefreshList() error {
	err := m.reloadLists()
	if err != nil {
		messengerLogger.WithError(err).Error("列表加载不完整，不标记加载完成，将在后台重试")
		m.startListReloadRetry()
		return err
	}
	m.listLoaded.Store(true)
	return nil
}

// reloadLists 加载好友、群组和群成员列表。
// 任一列表加载失败都返回错误，避免残缺列表被当作加载成功并启动订阅。
func (m *Messenger) reloadLists() error {
	var listErr error

	if err := m.ReloadFriendList(); err != nil {
		messengerLogger.WithError(err).Error("unable to load friends list")
		listErr = err
	} else {
		messengerLogger.Infof("已加载 %d 个好友", len(m.FriendList))
	}

	if err := m.ReloadGroupList(); err != nil {
		messengerLogger.WithError(err).Error("unable to load groups list")
		if listErr == nil {
			listErr = err
		}
	} else {
		messengerLogger.Infof("已加载 %d 个群组", len(m.GroupList))
	}

	// 好友/群列表失败时不再加载成员，避免在残缺列表上继续请求
	if listErr != nil {
		return listErr
	}

	var totalMembers int
	// 在 groupMu 读锁下拷贝群列表快照后再遍历，避免与 ReloadGroupList（整体替换切片）、
	// GetGroupMembersByID（加锁写 Members）并发时产生 data race
	m.groupMu.RLock()
	groupSnapshot := make([]*GroupInfo, len(m.GroupList))
	copy(groupSnapshot, m.GroupList)
	m.groupMu.RUnlock()

	for _, group := range groupSnapshot {
		members, err := m.GetGroupMembersByID(group.Code)
		if err != nil {
			messengerLogger.WithError(err).Errorf("unable to load group members for %d", group.Code)
			if listErr == nil {
				listErr = err
			}
			continue
		}
		totalMembers += len(members)
		messengerLogger.Debugf("群[%d]加载成员[%d]个", group.Code, len(members))
	}
	messengerLogger.Infof("已加载 %d 个群成员", totalMembers)

	return listErr
}

// startListReloadRetry 启动列表重载重试协程，避免加载失败后订阅系统永久无法启动
func (m *Messenger) startListReloadRetry() {
	if !m.listReloadRetryActive.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer m.listReloadRetryActive.Store(false)
		for i := 1; i <= listReloadMaxRetries; i++ {
			time.Sleep(listReloadRetryInterval)
			if m.listLoaded.Load() {
				return
			}
			if err := m.reloadLists(); err != nil {
				messengerLogger.WithError(err).Warnf("列表重载重试 %d/%d 失败，保持未加载状态", i, listReloadMaxRetries)
				continue
			}
			m.listLoaded.Store(true)
			messengerLogger.Info("列表重载重试成功，已标记加载完成")
			return
		}
		messengerLogger.Error("列表多次重试仍失败，订阅系统将不会启动，请检查适配器连接后重启")
	}()
}

// GetGroupListSnapshot 返回群列表的加锁快照副本，调用方可安全遍历而不与 ReloadGroupList 竞争
func (m *Messenger) GetGroupListSnapshot() []*GroupInfo {
	m.groupMu.RLock()
	defer m.groupMu.RUnlock()
	out := make([]*GroupInfo, len(m.GroupList))
	copy(out, m.GroupList)
	return out
}

// GetFriendListSnapshot 返回好友列表的加锁快照副本
func (m *Messenger) GetFriendListSnapshot() []*FriendInfo {
	m.friendMu.RLock()
	defer m.friendMu.RUnlock()
	out := make([]*FriendInfo, len(m.FriendList))
	copy(out, m.FriendList)
	return out
}

// IsListLoaded 返回好友/群/群成员列表是否已完成首次加载
func (m *Messenger) IsListLoaded() bool {
	return m.listLoaded.Load()
}

// isConnected 返回当前是否具备实际投递能力：账号在线（心跳缓存）且 WS 已连接。
// 两者缺一不可：心跳 Online 反映账号登录状态，Adapter 连接状态反映底层 socket。
// 任一不可用都视为离线，交由离线队列暂存，避免在账号未在线或 socket 断开时误发。
func (m *Messenger) isConnected() bool {
	return m.Online.Load() && m.Adapter != nil && m.Adapter.IsConnected()
}

// IsConnected 返回当前是否具备投递能力（供外部模块判断发送/重连状态）
func (m *Messenger) IsConnected() bool {
	return m.isConnected()
}

func (m *Messenger) handleNoticeEvent(event *NoticeEvent) {
	if m.eventDispatcher == nil {
		return
	}

	switch event.NoticeType {
	case "group_ban":
		m.eventDispatcher.DispatchGroupMute(&GroupMuteEvent{
			GroupCode:   event.GroupID,
			OperatorUin: event.OperatorID,
			TargetUin:   event.UserID,
			Time:        event.Duration,
		})
	case "group_increase":
		// Check if it's the bot joining the group
		if event.UserID == event.SelfID {
			// Bot joined the group - get full group info and member list
			groupInfo, err := m.GetGroupInfo(event.GroupID)
			if err != nil {
				messengerLogger.WithError(err).Warnf("GetGroupInfo failed for %d", event.GroupID)
				groupInfo = &GroupInfo{Uin: event.GroupID, Code: event.GroupID}
			}
			// Add group to GroupList first (needed for GetGroupMembersByID to set Members)
			m.groupMu.Lock()
			existingGroup := m.FindGroupByUinLocked(event.GroupID)
			if existingGroup == nil {
				m.GroupList = append(m.GroupList, groupInfo)
			} else {
				// Update existing group info
				existingGroup.Name = groupInfo.Name
				existingGroup.MemberCount = groupInfo.MemberCount
				groupInfo = existingGroup
			}
			m.groupMu.Unlock()
			// Fetch and cache all group members
			members, err := m.GetGroupMembersByID(event.GroupID)
			if err != nil {
				messengerLogger.WithError(err).Warnf("GetGroupMembersByID failed for %d", event.GroupID)
			} else {
				messengerLogger.Debugf("Fetched %d members for group %d", len(members), event.GroupID)
			}
			// Build GroupInfo for dispatch
			clientGroupInfo := &GroupInfo{
				Uin:         groupInfo.Uin,
				Code:        groupInfo.Code,
				Name:        groupInfo.Name,
				MemberCount: groupInfo.MemberCount,
				OwnerUin:    groupInfo.OwnerUin,
			}
			// Also set Members for the GroupInfo
			if members != nil {
				clientGroupInfo.Members = make([]*GroupMemberInfo, len(members))
				for i, mb := range members {
					clientGroupInfo.Members[i] = &GroupMemberInfo{
						Group:    clientGroupInfo,
						Uin:      mb.Uin,
						Nickname: mb.Nickname,
					}
				}
			}
			m.eventDispatcher.DispatchGroupJoin(clientGroupInfo)
		} else {
			// Regular member joined
			if err := m.AddGroupMember(event.GroupID, event.UserID); err != nil {
				messengerLogger.WithError(err).Warnf("AddGroupMember failed for %d/%d", event.GroupID, event.UserID)
			}
			m.eventDispatcher.DispatchGroupMemberJoin(&MemberJoinGroupEvent{
				Group: &GroupInfo{
					Uin:  event.GroupID,
					Code: event.GroupID,
				},
				Member: &GroupMemberInfo{
					Uin: event.UserID,
				},
			})
		}
	case "group_decrease":
		// Check if it's the bot being kicked/leaving the group
		if event.SubType == "kick_me" || event.OperatorID == m.Uin.Load() {
			// Bot was kicked or left the group - save group info before removing
			group := m.FindGroupByUin(event.GroupID)
			if group != nil {
				// Save group info for the event
				groupCopy := &GroupInfo{
					Uin:            group.Uin,
					Code:           group.Code,
					Name:           group.Name,
					MemberCount:    group.MemberCount,
					MaxMemberCount: group.MaxMemberCount,
				}
				m.groupMu.Lock()
				for i, g := range m.GroupList {
					if g.Code == event.GroupID {
						m.GroupList = append(m.GroupList[:i], m.GroupList[i+1:]...)
						break
					}
				}
				m.groupMu.Unlock()
				m.eventDispatcher.DispatchGroupLeave(&GroupLeaveEvent{
					Group:    groupCopy,
					Operator: &GroupMemberInfo{Uin: event.OperatorID},
				})
			}
		} else {
			// Regular member left
			m.RemoveGroupMember(event.GroupID, event.UserID)
			m.eventDispatcher.DispatchGroupMemberLeave(&MemberLeaveGroupEvent{
				Group: &GroupInfo{
					Uin:  event.GroupID,
					Code: event.GroupID,
				},
				Member: &GroupMemberInfo{
					Uin: event.UserID,
				},
				Operator: &GroupMemberInfo{
					Uin: event.OperatorID,
				},
			})
		}
	case "group_admin":
		var newPerm MemberPermission
		if event.SubType == "set" {
			newPerm = Administrator
		} else {
			newPerm = Member
		}
		m.UpdateGroupMember(event.GroupID, event.UserID, func(member *GroupMemberInfo) {
			member.Permission = newPerm
		})
		if event.SubType == "set" {
			m.eventDispatcher.DispatchGroupMemberPermissionChanged(&MemberPermissionChangedEvent{
				Group: &GroupInfo{
					Uin:  event.GroupID,
					Code: event.GroupID,
				},
				Member: &GroupMemberInfo{
					Uin:        event.UserID,
					Permission: Administrator,
				},
				OldPermission: Member,
				NewPermission: Administrator,
			})
		} else {
			m.eventDispatcher.DispatchGroupMemberPermissionChanged(&MemberPermissionChangedEvent{
				Group: &GroupInfo{
					Uin:  event.GroupID,
					Code: event.GroupID,
				},
				Member: &GroupMemberInfo{
					Uin:        event.UserID,
					Permission: Member,
				},
				OldPermission: Administrator,
				NewPermission: Member,
			})
		}
	case "group_card":
		m.UpdateGroupMember(event.GroupID, event.UserID, func(member *GroupMemberInfo) {
			member.CardName = event.CardNew
		})
		m.eventDispatcher.DispatchMemberCardUpdated(&MemberCardUpdatedEvent{
			Group:   &GroupInfo{Uin: event.GroupID, Code: event.GroupID},
			Member:  &GroupMemberInfo{Uin: event.UserID},
			OldCard: event.CardOld,
		})
	case "friend_add":
		nickname := "陌生人"
		if info, err := m.GetStrangerInfo(event.UserID); err == nil {
			if name, ok := info["nickname"].(string); ok {
				nickname = name
			}
		}
		m.eventDispatcher.DispatchNewFriend(&NewFriendEvent{
			Friend: &FriendInfo{
				Uin:      event.UserID,
				Nickname: nickname,
			},
		})
	case "friend_recall":
		m.eventDispatcher.DispatchFriendRecall(&FriendMessageRecalledEvent{
			FriendUin: event.UserID,
			MessageId: int32(event.MessageID),
			Time:      event.Time,
		})
	case "notify":
		switch event.SubType {
		case "poke":
			m.eventDispatcher.DispatchGroupNotify(&GroupPokeNotifyEvent{
				GroupCode: event.GroupID,
				Sender:    event.UserID,
				Receiver:  event.OperatorID,
			})
		case "title":
			m.eventDispatcher.DispatchMemberSpecialTitleUpdated(&MemberSpecialTitleUpdatedEvent{
				GroupCode: event.GroupID,
				Uin:       event.UserID,
				NewTitle:  event.Title,
			})
		case "profile_like":
			m.eventDispatcher.DispatchProfileLike(&ProfileLikeEvent{
				OperatorId:   event.OperatorID,
				OperatorNick: event.OperatorNick,
				Times:        event.Times,
			})
		case "poke_recall":
			m.eventDispatcher.DispatchPokeRecall(&PokeRecallEvent{
				GroupCode: event.GroupID,
				Sender:    event.UserID,
				Receiver:  event.OperatorID,
			})
		}
	case "group_recall":
		m.eventDispatcher.DispatchGroupRecall(&GroupMessageRecalledEvent{
			GroupCode:   event.GroupID,
			OperatorUin: event.OperatorID,
			AuthorUin:   event.UserID,
			MessageId:   int32(event.MessageID),
		})
	case "essence":
		m.eventDispatcher.DispatchGroupEssenceChanged(&GroupDigestEvent{
			GroupCode: event.GroupID,
		})
	case "group_upload":
		m.eventDispatcher.DispatchGroupUploadNotify(&GroupUploadNotifyEvent{
			GroupCode: event.GroupID,
			Sender:    event.UserID,
			File:      event.File,
		})
	case "bot_offline":
		m.eventDispatcher.DispatchBotOffline(&BotOfflineEvent{})
	case "group_dismiss":
		m.eventDispatcher.DispatchGroupDisband(&GroupDisbandEvent{
			Group: &GroupInfo{
				Uin:  event.GroupID,
				Code: event.GroupID,
			},
			Operator: &GroupMemberInfo{
				Uin: event.UserID,
			},
		})
	case "group_msg_emoji_like":
		m.eventDispatcher.DispatchGroupMsgEmojiLike(&GroupMsgEmojiLikeEvent{
			GroupCode:  event.GroupID,
			UserId:     event.UserID,
			MessageId:  event.MessageID,
			EmojiId:    event.EmojiId,
			EmojiCount: event.EmojiCount,
			IsAdd:      event.SubType == "add",
		})
	}
}

func (m *Messenger) handleRequestEvent(event *RequestEvent) {
	if m.eventDispatcher == nil {
		return
	}

	switch event.RequestType {
	case "friend":
		m.eventDispatcher.DispatchNewFriendRequest(&NewFriendRequest{
			RequestId:     time.Now().UnixNano() / 1e6,
			Message:       event.Comment,
			RequesterUin:  event.UserID,
			RequesterNick: "陌生人",
			Flag:          event.Flag,
		})
	case "group":
		if event.SubType == "add" {
			m.eventDispatcher.DispatchUserJoinGroupRequest(&UserJoinGroupRequest{
				RequestId:     time.Now().UnixNano() / 1e6,
				Message:       event.Comment,
				RequesterUin:  event.UserID,
				RequesterNick: "陌生人",
				GroupCode:     event.GroupID,
				Flag:          event.Flag,
			})
		} else if event.SubType == "invite" {
			m.eventDispatcher.DispatchGroupInvitedRequest(&GroupInvitedRequest{
				RequestId:   time.Now().UnixNano() / 1e6,
				InvitorUin:  event.UserID,
				InvitorNick: "陌生人",
				GroupCode:   event.GroupID,
				Flag:        event.Flag,
			})
		}
	}
}

func (m *Messenger) handleGroupMessage(event *GroupMessageEvent) {
	m.groupMsgCount.Add(1)
	messengerLogger.Debugf("handleGroupMessage called: group=%d, user=%d, msgID=%d", event.GroupID, event.UserID, event.MessageID)

	sender := &SenderInfo{
		UserID: event.UserID,
		Uin:    event.UserID,
	}
	group := m.FindGroup(event.GroupID)
	if group != nil {
		member := group.FindMember(event.UserID)
		if member != nil {
			sender.Nickname = member.Nickname
			sender.Card = member.CardName
		}
	}

	elements := ConvertToMessageElements(event.Message)

	groupName := ""
	if group != nil {
		groupName = group.Name
	}
	msg := &GroupMessage{
		ID:        int64(event.MessageID),
		GroupCode: event.GroupID,
		GroupName: groupName,
		Sender:    sender,
		Time:      event.Time,
		Elements:  elements,
	}

	messengerLogger.Debugf("收到群 %d 内 %d 的消息", event.GroupID, event.UserID)

	if m.eventDispatcher != nil {
		messengerLogger.Debugf("Dispatching group message to bot event handlers")
		m.eventDispatcher.DispatchGroupMessage(msg)
	} else {
		messengerLogger.Warnf("eventDispatcher is nil, cannot dispatch message!")
	}
}

func (m *Messenger) handlePrivateMessage(event *PrivateMessageEvent) {
	m.privateMsgCount.Add(1)
	isFriend := m.FindFriend(event.UserID) != nil
	nickname := ""
	if !isFriend {
		if info, err := m.GetStrangerInfo(event.UserID); err == nil {
			if name, ok := info["nickname"].(string); ok {
				nickname = name
			}
		}
	}

	elements := ConvertToMessageElements(event.Message)
	msg := &PrivateMessage{
		ID:     int64(event.MessageID),
		UserID: event.UserID,
		Self:   event.SelfID,
		Sender: &SenderInfo{
			UserID:   event.UserID,
			Uin:      event.UserID,
			Nickname: nickname,
		},
		Time:     event.Time,
		Elements: elements,
	}

	messengerLogger.Debugf("收到 %d 的私聊消息", event.UserID)

	if m.eventDispatcher != nil {
		m.eventDispatcher.DispatchPrivateMessage(msg)
	}
}

func (m *Messenger) SendApi(action string, params map[string]interface{}) (interface{}, error) {
	return m.Adapter.SendApi(action, params)
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

func (m *Messenger) GroupPoke(groupCode, target int64) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.GroupPoke(groupCode, target)
}

func (m *Messenger) FriendPoke(target int64) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.FriendPoke(target)
}

func (m *Messenger) SetGroupAddRequest(flag string, approve bool, reason string) error {
	_, err := m.SendApi("set_group_add_request", map[string]interface{}{
		"flag":    flag,
		"approve": approve,
		"reason":  reason,
	})
	return err
}

func (m *Messenger) SetFriendAddRequest(flag string, approve bool) error {
	_, err := m.SendApi("set_friend_add_request", map[string]interface{}{
		"flag":    flag,
		"approve": approve,
	})
	return err
}

func (m *Messenger) SetGroupAdmin(groupCode, memberUin int64, enable bool) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.SetGroupAdmin(groupCode, memberUin, enable)
}

func (m *Messenger) EditGroupCard(groupCode, memberUin int64, card string) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.EditGroupCard(groupCode, memberUin, card)
}

func (m *Messenger) EditGroupTitle(groupCode, memberUin int64, title string) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.EditGroupTitle(groupCode, memberUin, title)
}

func (m *Messenger) SetGroupWholeBan(groupCode int64, enable bool) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.SetGroupWholeBan(groupCode, enable)
}

func (m *Messenger) SetGroupBan(groupCode, memberUin int64, duration int64) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.SetGroupBan(groupCode, memberUin, duration)
}

func (m *Messenger) SetGroupLeave(groupCode int64, isDismiss bool) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.SetGroupLeave(groupCode, isDismiss)
}

func (m *Messenger) KickGroupMember(groupCode int64, memberUin int64, rejectAddRequest bool) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.KickGroupMember(groupCode, memberUin, rejectAddRequest)
}

func (m *Messenger) GetMsg(messageID int32) (*GetMsgResult, error) {
	if m.Adapter == nil {
		return nil, fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.GetMsg(messageID)
}

func (m *Messenger) GetMsgOrg(messageID int32) (interface{}, error) {
	if m.Adapter == nil {
		return nil, fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.GetMsgOrg(messageID)
}

func (m *Messenger) RecallMsg(messageID int32) error {
	if m.Adapter == nil {
		return fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.RecallMsg(messageID)
}

func (m *Messenger) DownloadFile(url, base64, name string, headers []string) (string, error) {
	if m.Adapter == nil {
		return "", fmt.Errorf("adapter not initialized")
	}
	return m.Adapter.DownloadFile(url, base64, name, headers)
}

func (m *Messenger) GetFileUrl(groupCode int64, fileId string) string {
	if m.Adapter == nil {
		return ""
	}
	return m.Adapter.GetFileUrl(groupCode, fileId)
}

// offlineQueue 相关方法

func newOfflineQueueMsg(targetID int64, targetType string, msg *SendingMessage, newStr string) offlineQueueMsg {
	return offlineQueueMsg{
		TargetId:   targetID,
		TargetType: targetType,
		Message:    msg,
		NewStr:     newStr,
		CreatedAt:  time.Now(),
	}
}

func getOfflineQueueEnable() bool {
	return config.GlobalConfig.GetBool("bot.offlineQueue.enable")
}

func getOfflineQueueExpire() time.Duration {
	timeStr := config.GlobalConfig.GetString("bot.offlineQueue.expire")
	if timeStr == "" {
		return 30 * time.Minute
	}
	t, err := time.ParseDuration(timeStr)
	if err != nil || t <= 0 {
		messengerLogger.Warnf("无效的离线队列过期配置: %s，使用默认值30m", timeStr)
		return 30 * time.Minute
	}
	return t
}

func (m *Messenger) saveOfflineMsg(msg offlineQueueMsg) {
	m.offlineQueueMu.Lock()
	defer m.offlineQueueMu.Unlock()
	if len(m.offlineQueue) >= offlineQueueMaxSize {
		messengerLogger.Warnf("离线队列已满(%d)，丢弃最旧消息", offlineQueueMaxSize)
		m.offlineQueue = m.offlineQueue[1:]
	}
	m.offlineQueue = append(m.offlineQueue, msg)
}

func (m *Messenger) loadOfflineMsgs() []offlineQueueMsg {
	m.offlineQueueMu.Lock()
	defer m.offlineQueueMu.Unlock()
	result := make([]offlineQueueMsg, len(m.offlineQueue))
	copy(result, m.offlineQueue)
	return result
}

func (m *Messenger) takeOfflineMsgs() []offlineQueueMsg {
	m.offlineQueueMu.Lock()
	defer m.offlineQueueMu.Unlock()
	result := m.offlineQueue
	m.offlineQueue = make([]offlineQueueMsg, 0, offlineQueueMaxSize)
	return result
}

func (m *Messenger) clearOfflineMsgs() {
	m.takeOfflineMsgs()
}

func (m *Messenger) scheduleOfflineQueueFlush(delay time.Duration) {
	if !getOfflineQueueEnable() || !m.offlineFlushScheduled.CompareAndSwap(false, true) {
		return
	}
	time.AfterFunc(delay, func() {
		m.offlineFlushScheduled.Store(false)
		select {
		case <-m.stopChan:
			return
		default:
		}
		if m.isConnected() {
			m.flushOfflineQueue()
		} else if len(m.loadOfflineMsgs()) > 0 {
			// 定时器触发时仍断线也要继续等待，避免稍后重连后消息永久滞留。
			m.scheduleOfflineQueueFlush(delay)
		}
	})
}

func (m *Messenger) flushOfflineQueue() {
	if !getOfflineQueueEnable() {
		return
	}
	m.offlineQueueFlushMu.Lock()
	defer m.offlineQueueFlushMu.Unlock()

	if !m.isConnected() {
		return
	}
	msgs := m.takeOfflineMsgs()
	if len(msgs) == 0 {
		return
	}
	expire := getOfflineQueueExpire()
	now := time.Now()
	messengerLogger.Infof("BOT已上线，开始重发缓存的 %d 条离线消息", len(msgs))

	failed := 0
	for _, msg := range msgs {
		if now.Sub(msg.CreatedAt) <= expire {
			messages := m.buildMessageSegments(msg.Message)
			switch msg.TargetType {
			case "group":
				msgID, err := m.Adapter.SendGroupMessage(msg.TargetId, messages)
				if err != nil {
					if errors.Is(err, ErrRequestResultUnknown) {
						messengerLogger.Warnf("重发离线群消息超时，发送结果未知，不再重试: group=%d", msg.TargetId)
					} else if errors.Is(err, ErrRequestRejected) {
						messengerLogger.Warnf("重发离线群消息被OneBot明确拒绝，不再重试: group=%d", msg.TargetId)
					} else if errors.Is(err, ErrRequestNotSent) {
						messengerLogger.Errorf("重发离线群消息失败: %v", err)
						m.saveOfflineMsg(msg)
						failed++
					} else {
						messengerLogger.Warnf("重发离线群消息错误未明确标记为写入前失败，不再重试: group=%d error=%v", msg.TargetId, err)
					}
				} else {
					messengerLogger.Debugf("离线群消息重发成功: group=%d, msgID=%d", msg.TargetId, msgID)
				}
			case "private":
				msgID, err := m.Adapter.SendPrivateMessage(msg.TargetId, messages)
				if err != nil {
					if errors.Is(err, ErrRequestResultUnknown) {
						messengerLogger.Warnf("重发离线私聊消息超时，发送结果未知，不再重试: user=%d", msg.TargetId)
					} else if errors.Is(err, ErrRequestRejected) {
						messengerLogger.Warnf("重发离线私聊消息被OneBot明确拒绝，不再重试: user=%d", msg.TargetId)
					} else if errors.Is(err, ErrRequestNotSent) {
						messengerLogger.Errorf("重发离线私聊消息失败: %v", err)
						m.saveOfflineMsg(msg)
						failed++
					} else {
						messengerLogger.Warnf("重发离线私聊消息错误未明确标记为写入前失败，不再重试: user=%d error=%v", msg.TargetId, err)
					}
				} else {
					messengerLogger.Debugf("离线私聊消息重发成功: user=%d, msgID=%d", msg.TargetId, msgID)
				}
			default:
				messengerLogger.Warnf("未知的离线消息类型: %s", msg.TargetType)
			}
		} else {
			messengerLogger.Infof("丢弃过期离线消息: %s", msg.NewStr)
		}
	}
	if failed > 0 {
		m.scheduleOfflineQueueFlush(offlineQueueRetryDelay)
	}
}

func sliceMessage(str string) string {
	if len(str) > 75 {
		return str[:75] + "..."
	}
	return str
}
