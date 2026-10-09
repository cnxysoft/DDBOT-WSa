package adapter

import (
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
)

type SendResp struct {
	RetMSG *GroupMessage
	Error  error
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

	// retrySleep 允许注入重试等待实现（测试中替换为立即返回，避免真实休眠）
	retrySleep func(time.Duration)

	Uin    int64
	Online atomic.Bool

	GroupList  []*GroupInfo
	FriendList []*FriendInfo
	groupMu    sync.RWMutex
	friendMu   sync.RWMutex

	// groupReloadMu 串行化实时群列表刷新，避免并发刷新互相覆盖成员缓存
	groupReloadMu sync.Mutex
	// provisionalGroupSync 记录正在异步补齐资料的群（群号为键），避免重复调度
	provisionalGroupSync sync.Map

	stopChan chan struct{}
	wg       sync.WaitGroup

	eventDispatcher BotEventDispatcher

	// 消息统计
	groupMsgCount    atomic.Int64
	privateMsgCount  atomic.Int64
	groupSendCount   atomic.Int64
	privateSendCount atomic.Int64

	// 离线消息队列
	offlineQueue   []offlineQueueMsg
	offlineQueueMu sync.Mutex
}

func NewMessenger(adapter Adapter) *Messenger {
	m := &Messenger{
		Adapter:    adapter,
		retrySleep: time.Sleep,
		stopChan:   make(chan struct{}),
		GroupList:  make([]*GroupInfo, 0),
		FriendList: make([]*FriendInfo, 0),
	}

	m.registerEventHandlers()

	// 启动时输出图片发送方式配置，便于排查 OneBot 端兼容性问题
	logConfiguredImageSendMode()

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
			m.Uin = event.SelfID
			m.Online.Store(true)
			messengerLogger.Infof("Bot online: %d", m.Uin)
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
	return m.Uin
}

func (m *Messenger) GetSelfID() int64 {
	return m.Adapter.GetSelfID()
}

func (m *Messenger) SendGroupMessage(groupCode int64, msg *SendingMessage, newstr string) SendResp {
	// 检查离线队列条件
	if getOfflineQueueEnable() && !m.Online.Load() {
		messengerLogger.Warnf("BOT已离线，已开启离线缓存，将暂存消息: %s", sliceMessage(newstr))
		m.saveOfflineMsg(offlineQueueMsg{
			TargetId:   groupCode,
			TargetType: "group",
			Message:    msg,
			NewStr:     newstr,
			CreatedAt:  time.Now(),
		})
		return SendResp{RetMSG: &GroupMessage{ID: -1}, Error: nil}
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

	var lastResult SendResp
	for i, chunk := range chunks {
		// 分片之间添加延迟，避免发送过快
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}
		chunkMsg := &SendingMessage{Elements: parseChunkToElements(chunk)}
		// 单个分片带图片降级重发，避免图片失败导致整条通知丢失
		// 成品证据：SendGroupMessage @0x895a23 传 rdi=i+1（0x8959fb inc %rdi）、
		// rsi=len(chunks)（0x8957c5 存 buildMessageChunks 的 len 到 0x78(%rsp)），
		// 即分片序号与分片总数；旧重建误传了 newstr。
		lastResult = m.sendGroupChunkWithImageFallback(groupCode, chunkMsg, i+1, len(chunks))
		if lastResult.Error != nil {
			// 成品证据：SendGroupMessage @0x895620 +0x4c0 Error「Send group message failed (chunk %d/%d): %v」
			messengerLogger.Errorf("Send group message failed (chunk %d/%d): %v", i+1, len(chunks), lastResult.Error)
		}
	}

	return lastResult
}

func (m *Messenger) SendPrivateMessage(target int64, msg *SendingMessage, newstr string) *PrivateMessage {
	// 检查离线队列条件
	if getOfflineQueueEnable() && !m.Online.Load() {
		messengerLogger.Warnf("BOT已离线，已开启离线缓存，将暂存私聊消息: %s", sliceMessage(newstr))
		m.saveOfflineMsg(offlineQueueMsg{
			TargetId:   target,
			TargetType: "private",
			Message:    msg,
			NewStr:     newstr,
			CreatedAt:  time.Now(),
		})
		return &PrivateMessage{ID: -1}
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

	var lastMsgID int32 = -1
	for i, chunk := range chunks {
		// 构建新的 SendingMessage，按配置的图片发送方式准备媒体元素后再转为 OneBot 段。
		// 成品证据：SendPrivateMessage @0x895c60 的 callees 同时含 +0x3c0
		// prepareImageElements 与 +0x4e6 buildOutgoingMessageSegments；
		// 重建原先只走 buildOutgoingMessageSegments，图片准备被包在里面。
		elements, cleanup, prepErr := m.prepareImageElements(parseChunkToElements(chunk))
		if prepErr != nil {
			// 成品证据：SendPrivateMessage @0x895c60 在 +0x3c0 call
			// prepareImageElements 后 0x896025 "test %rsi,%rsi"、0x896028
			// "je 0x896116"；出错时记录 55 字节
			// "Prepare private message images failed (chunk %d/%d): %v"
			// （0x10e1b82），随后 0x896111 jmp 0x895fc1 直接进入循环自增，
			// 跳过本次分片发送；错误路径返回空闭包（无临时文件需清理）。
			messengerLogger.Errorf("Prepare private message images failed (chunk %d/%d): %v", i+1, len(chunks), prepErr)
			continue
		}
		chunkMsg := &SendingMessage{Elements: elements}
		messages := m.buildOutgoingMessageSegments(chunkMsg)

		// 分片之间添加延迟，避免发送过快
		if i > 0 {
			time.Sleep(100 * time.Millisecond)
		}

		msgID, err := m.Adapter.SendPrivateMessage(target, messages)
		m.privateSendCount.Add(1)
		if err != nil {
			messengerLogger.Errorf("Send private message failed (chunk %d/%d): %v", i+1, len(chunks), err)
		} else {
			lastMsgID = msgID
		}
		// 成品证据：SendPrivateMessage @0x8961b3 调 Adapter.SendPrivateMessage 后
		// 立即于 0x8961c6 call *%rsi 执行 prepareImageElements 返回的 cleanup。
		if cleanup != nil {
			cleanup()
		}
	}

	return &PrivateMessage{
		ID:     int64(lastMsgID),
		UserID: target,
		Self:   m.Uin,
		Sender: &SenderInfo{
			UserID: m.Uin,
			Uin:    m.Uin,
		},
		Elements: msg.Elements,
	}
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

// buildOutgoingMessageSegments 把消息转换为 OneBot 段，并删除 image 段冗余的 url 字段。
// 图片准备（prepareImageElements）由调用方在发送前完成。
// 成品证据：adapter.(*Messenger).buildOutgoingMessageSegments @0x897680（192B）的 callees
// 只有 buildMessageSegments 与 runtime.mapdelete_faststr；反汇编遍历段列表，对
// type=="image"（0x8976c6 比较 5 字节 "image"）的段调用 mapdelete(Data, "url")
// （key 常量 0x10987f4 长度 3 = "url"），不含 prepareImageElements。
func (m *Messenger) buildOutgoingMessageSegments(msg *SendingMessage) []MessageSegment {
	segments := m.buildMessageSegments(msg)
	for i := range segments {
		if segments[i].Type == "image" {
			delete(segments[i].Data, "url")
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

	return m.FindGroupByUinLocked(code)
}

func (m *Messenger) FindGroupByUin(uin int64) *GroupInfo {
	m.groupMu.RLock()
	defer m.groupMu.RUnlock()

	return m.FindGroupByUinLocked(uin)
}

// FindGroupByUinLocked assumes the caller holds the lock.
//
// 成品证据（DDBOT-Lite）：
//   - FindGroup @0x899e20 与 FindGroupByUin @0x899fa0 反汇编逐字节同构，都是 RLock
//     后内联同一份循环；
//   - FindGroupByUinLocked @0x89a120（64B，不加锁）循环体：
//     test %rsi,%rsi; je   元素为 nil 跳过
//     test %rbx,%rbx; jle  入参 <=0 跳过
//     cmp %rbx,0x8(%rsi)   GroupInfo.Code
//     cmp %rbx,(%rsi)      GroupInfo.Uin
//     cmp %rbx,0x70(%rsi)  GroupInfo.GroupID
//     recovered/types 的 adapter.GroupInfo{Uin int64; Code int64; ...; GroupID int64}
//     对应 Uin@0x0、Code@0x8、GroupID@0x70。
func (m *Messenger) FindGroupByUinLocked(uin int64) *GroupInfo {
	for _, g := range m.GroupList {
		if g == nil {
			continue
		}
		if uin > 0 && (g.Code == uin || g.Uin == uin || g.GroupID == uin) {
			return g
		}
	}
	return nil
}

func (m *Messenger) FindFriend(uin int64) *FriendInfo {
	if uin == m.Uin {
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
	m.groupReloadMu.Lock()
	defer m.groupReloadMu.Unlock()
	return m.reloadGroupListLocked()
}

// reloadGroupListLocked 实时拉取群列表并重建缓存，调用方需持有 groupReloadMu。
// 已缓存的群保留成员列表，仅刷新群资料，避免刷新期间丢掉刚拉取的群成员。
func (m *Messenger) reloadGroupListLocked() error {
	groups, err := m.Adapter.GetGroupList()
	if err != nil {
		return err
	}

	m.groupMu.Lock()
	defer m.groupMu.Unlock()

	newList := make([]*GroupInfo, 0, len(groups))
	for _, g := range groups {
		if g.GroupID == 0 {
			// 成品证据：+0x61b Warn「get_group_list 返回了缺少群号的记录，已忽略」
			messengerLogger.Warn("get_group_list 返回了缺少群号的记录，已忽略")
			continue
		}
		cached := m.FindGroupByUinLocked(g.GroupID)
		if cached != nil {
			cached.Name = g.GroupName
			cached.MemberCount = g.MemberCount
			cached.MaxMemberCount = g.MaxMemberCount
			cached.GroupCreateTime = g.GroupCreateTime
			cached.GroupLevel = g.GroupLevel
			newList = append(newList, cached)
			continue
		}
		newList = append(newList, &GroupInfo{
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
	m.GroupList = newList

	messengerLogger.Infof("Reloaded %d groups", len(m.GroupList))
	return nil
}

// groupMetadataIncomplete 判断群资料是否缺失关键信息（名称为空或成员数未知）。
func groupMetadataIncomplete(g *GroupInfo) bool {
	return g == nil || g.Name == "" || g.MemberCount <= 0
}

// refreshSingleGroupFromList 从实时群列表补齐单个群的资料，成功返回缓存中的群，失败返回 nil。
func (m *Messenger) refreshSingleGroupFromList(groupID int64) *GroupInfo {
	m.groupReloadMu.Lock()
	defer m.groupReloadMu.Unlock()

	groups, err := m.Adapter.GetGroupList()
	if err != nil {
		messengerLogger.WithError(err).WithField("group", groupID).Warn("实时补查群资料失败")
		return nil
	}

	m.groupMu.Lock()
	defer m.groupMu.Unlock()

	for _, g := range groups {
		if g.GroupID != groupID {
			continue
		}
		cached := m.FindGroupByUinLocked(groupID)
		if cached == nil {
			cached = &GroupInfo{
				Uin:     g.GroupID,
				Code:    g.GroupID,
				Members: make([]*GroupMemberInfo, 0),
				Client:  m,
			}
			m.GroupList = append(m.GroupList, cached)
		}
		cached.Name = g.GroupName
		cached.MemberCount = g.MemberCount
		cached.MaxMemberCount = g.MaxMemberCount
		cached.GroupCreateTime = g.GroupCreateTime
		cached.GroupLevel = g.GroupLevel
		messengerLogger.WithFields(logrus.Fields{
			"group": groupID,
			"name":  g.GroupName,
		}).Info("新群资料已通过实时群列表补齐")
		return cached
	}
	messengerLogger.Warnf("group %d is not present in current OneBot group list", groupID)
	return nil
}

// provisionalGroupRefreshMaxAttempts 是临时群资料异步补齐的最大尝试次数。
// TODO(lite): 成品中的重试参数不可复原，这里取保守值。
const provisionalGroupRefreshMaxAttempts = 3

// provisionalGroupRefreshInterval 是临时群资料异步补齐的重试间隔。
const provisionalGroupRefreshInterval = 2 * time.Second

// scheduleProvisionalGroupRefresh 异步补齐临时群的资料；同一群并发去重，避免重复刷新。
func (m *Messenger) scheduleProvisionalGroupRefresh(groupID int64) {
	if _, loaded := m.provisionalGroupSync.LoadOrStore(groupID, struct{}{}); loaded {
		return
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer m.provisionalGroupSync.Delete(groupID)
		for attempt := 1; attempt <= provisionalGroupRefreshMaxAttempts; attempt++ {
			if m.refreshSingleGroupFromList(groupID) != nil {
				return
			}
			messengerLogger.WithFields(logrus.Fields{
				"group":   groupID,
				"attempt": attempt,
			}).Info("新群资料暂未在实时群列表中出现，将继续重试")
			if attempt == provisionalGroupRefreshMaxAttempts {
				// 成品证据：+0x816 Warn「新群资料在有限重试后仍未同步；收到该群后续消息时会再次尝试」
				messengerLogger.WithField("group", groupID).Warn("新群资料在有限重试后仍未同步；收到该群后续消息时会再次尝试")
				return
			}
			if m.retrySleep != nil {
				m.retrySleep(provisionalGroupRefreshInterval)
			} else {
				time.Sleep(provisionalGroupRefreshInterval)
			}
		}
	}()
}

// EnsureGroupCached 确保群在缓存中；缓存未命中时重载完整群列表后再查。
//
// 成品三值签名 (*GroupInfo, bool, error) 的证据：
//   - adapter.(*Messenger).EnsureGroupCached @0x89bbc0（messenger.go:1028）返回
//     rax=*GroupInfo、rbx=bool、rcx:rdi=error；deferwrap1 @0x89c020 对应第 1043 行的
//     defer Unlock。
//   - 入口 0x89bc00 "test %rbx,%rbx; jg 0x89bcad"：groupID<=0 时走 0x89bc0f 用
//     fmt.errorf 生成 19 字节 "invalid group id %d"，返回 (nil,false,err)。
//   - 先 FindGroupByUin(+0x100)；命中且 groupMetadataIncomplete 时调
//     scheduleProvisionalGroupRefresh(+0x1a8)，返回 (group,false,nil)。
//   - 未命中则锁 m.groupReloadMu（0x89bcde "lock cmpxchg ... 0x88(%rcx)"；同偏移
//     的锁在 ReloadGroupList @0x89a32f 处也出现，故 0x88 即 groupReloadMu）后复检；
//     复检命中同上返回 (group,false,nil)。
//   - 仍未命中才 reloadGroupListLocked(+0x1ef)；err 非 nil 时 0x89bdd3 以 22 字节
//     "refresh group list: %w" 包装返回 (nil,false,err)。
//   - 重载成功后第三次 FindGroupByUin(+0x2b8)：命中返回 (group,true,nil)（0x89bf42
//     "movb $0x1,0x2e(%rsp)"，bool 唯一置位点）；仍为 nil 时 0x89beaf 以 44 字节
//     "group %d is not present in OneBot group list" 返回 (nil,false,err)。
//
// 第二个返回值 [推断] 命名为 reloaded：true 仅表示「调用开始时群不在缓存中，
// 本次通过重载完整群列表才发现」。缓存直接命中的两条路径分别在 0x89bf9e、0x89bd6d
// 写 "movb $0x0,0x2e(%rsp)"，可排除「命中缓存/资料已刷新」等解释；调用方
// bot.(*Bot).EnsureGroup @0xb7df15 "test %bl,%bl" 与 handleNoticeEvent @0x89e460
// 也只在该值为 true 时追加实时成员刷新，与「刚发现、成员尚未加载」一致。
func (m *Messenger) EnsureGroupCached(groupID int64) (*GroupInfo, bool, error) {
	if groupID <= 0 {
		return nil, false, fmt.Errorf("invalid group id %d", groupID)
	}

	if group := m.FindGroupByUin(groupID); group != nil {
		if groupMetadataIncomplete(group) {
			m.scheduleProvisionalGroupRefresh(groupID)
		}
		return group, false, nil
	}

	m.groupReloadMu.Lock()
	defer m.groupReloadMu.Unlock()

	if group := m.FindGroupByUin(groupID); group != nil {
		if groupMetadataIncomplete(group) {
			m.scheduleProvisionalGroupRefresh(groupID)
		}
		return group, false, nil
	}

	if err := m.reloadGroupListLocked(); err != nil {
		return nil, false, fmt.Errorf("refresh group list: %w", err)
	}

	group := m.FindGroupByUin(groupID)
	if group == nil {
		return nil, false, fmt.Errorf("group %d is not present in OneBot group list", groupID)
	}
	return group, true, nil
}

// EnsureGroup 确保群信息在缓存中，缺失或资料不完整时实时拉取；
// 拉取后仍无法取得群资料时返回错误。
// [推断] 成品中该方法在 adapter.Messenger 上无独立 pclntab 符号（疑似被内联进
// bot.(*Bot).EnsureGroup），此处按 EnsureGroupCached 的三值语义包装并透传 error。
func (m *Messenger) EnsureGroup(groupID int64) (*GroupInfo, error) {
	group, _, err := m.EnsureGroupCached(groupID)
	return group, err
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
	// 成品证据：disasm adapter.(*Messenger).GetGroupMembersByID @0x89c480
	// 入口 +0x2d call EnsureGroupCached（三值），+0x32 "test %rcx,%rcx" / "jne 0x89c521"
	// 只处理 error（0x89c521 起 rax=rbx=rcx=0，把入参 error 原样返回，即 "return nil, err"）。
	// 随后 0x89c4b7 "mov %rax,0x88(%rsp)" 直接保存群指针，+0x5a 调用 Adapter.GetGroupMemberList，
	// 直到 0x89c887 取 groupMu.Lock（"add $0x58,%rax" 后 call sync.(*RWMutex).Lock）
	// 并写 Members(0x40)/MembersLoaded(0x58) 为止，全程没有任何对群指针的判空分支——
	// 三值签名下 error==nil 时群必非 nil，故不保留非成品原样的 nil 守卫。
	group, _, err := m.EnsureGroupCached(groupID)
	if err != nil {
		return nil, err
	}

	members, err := m.Adapter.GetGroupMemberList(groupID)
	if err != nil {
		return nil, err
	}

	result := make([]*GroupMemberInfo, 0, len(members))
	for _, mb := range members {
		if mb.UserID == 0 {
			// 成品证据：+0x3c2 Warn「get_group_member_list 返回了缺少用户号的记录，已忽略」
			messengerLogger.Warn("get_group_member_list 返回了缺少用户号的记录，已忽略")
			continue
		}
		perm := Member
		switch mb.Role {
		case "owner":
			perm = Owner
		case "admin":
			perm = Administrator
		}

		result = append(result, &GroupMemberInfo{
			// 成品把入口处 EnsureGroupCached 返回的同一个群指针写入每个成员的 Group 字段
			// （0x89c618 `mov %rdx,(%rax)`，rdx 即入口保存的群指针），不再逐个 FindGroupByUin。
			Group:           group,
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

	m.groupMu.Lock()
	group.Members = result
	// 拿到完整成员列表后置位。证据：成品 disasm 0x89c8e8
	// `movb $0x1,0x58(%rdx)`，0x58 正是 GroupInfo.MembersLoaded（Members 在 0x40），
	// 紧随 Members 写入（0x89c8e4 `mov %rdi,0x40(%rdx)`）之后、
	// Unlock（0x89c8f4 call sync.(*RWMutex).Unlock）之前。
	// 锁块仅由成员循环出口 0x89c563 "jle 0x89c887" 进入，反汇编全程无 group==nil 分支；
	// 三值签名保证 error==nil 时 group 必非 nil，故不再保留非成品原样的判空。
	group.MembersLoaded = true
	m.groupMu.Unlock()

	return result, nil
}

func (m *Messenger) GetStrangerInfo(uin int64) (map[string]interface{}, error) {
	return m.Adapter.GetStrangerInfo(uin)
}

// AddGroupMember 是 RefreshGroupMember 的薄封装，入群事件后刷新成员缓存。
// 成品证据：adapter.(*Messenger).AddGroupMember @0x89d400（96B）唯一 callees 是
// RefreshGroupMember @0x89d1a0；反汇编 0x89d40e call 后 "mov %rbx,%rax; mov %rcx,%rbx"
// 即丢弃返回的 *GroupMemberInfo、只把 error 作为返回值。
func (m *Messenger) AddGroupMember(groupID, userID int64) error {
	_, err := m.RefreshGroupMember(groupID, userID)
	return err
}

// cacheGroupMember 把一份成员资料写入群成员缓存，返回缓存中的成员。
// 签名与行为对照成品 adapter.(*Messenger).cacheGroupMember @0x89c940（1760B）：
//   - 入参 (rax=m, rbx=*GroupInfo, rcx=*GroupMemberInfo)；
//   - rbx==0 -> "group is nil"（0x10aa8b4，12B）；rcx==0 -> "group member info is empty"
//     （0x10be570，26B）；成员 ID 取 memberInfo.UserID(0x80)，为 0 时回退 Uin(0x8)，
//     仍为 0 -> "group member info has no user id"（0x10c7961，32B）；
//   - Permission 由 Role 推导（owner=1/admin=2/其余=3），Role 为空时回退
//     memberInfo.Permission(0x60)；
//   - 命中已缓存成员时逐字段刷新（CardName 先取 memberInfo.Card(0x88)、
//     memberInfo.CardName(0x20) 非空再覆盖；SpecialTitle 同理），JoinTime/LastSentTime/
//     ShutUpTimestamp 仅在非零时覆盖；未命中则 append 新成员。
func (m *Messenger) cacheGroupMember(group *GroupInfo, memberInfo *GroupMemberInfo) (*GroupMemberInfo, error) {
	if group == nil {
		return nil, fmt.Errorf("group is nil")
	}
	if memberInfo == nil {
		return nil, fmt.Errorf("group member info is empty")
	}
	userID := memberInfo.UserID
	if userID == 0 {
		userID = memberInfo.Uin
	}
	if userID == 0 {
		return nil, fmt.Errorf("group member info has no user id")
	}

	perm := Member
	if memberInfo.Role != "" {
		switch memberInfo.Role {
		case "owner":
			perm = Owner
		case "admin":
			perm = Administrator
		}
	} else if memberInfo.Permission != 0 {
		perm = memberInfo.Permission
	}

	m.groupMu.Lock()
	defer m.groupMu.Unlock()

	for _, member := range group.Members {
		if member.Uin == userID {
			member.Group = group
			member.Nickname = memberInfo.Nickname
			member.CardName = memberInfo.Card
			if memberInfo.CardName != "" {
				member.CardName = memberInfo.CardName
			}
			if memberInfo.JoinTime != 0 {
				member.JoinTime = memberInfo.JoinTime
			}
			if memberInfo.LastSentTime != 0 {
				member.LastSentTime = memberInfo.LastSentTime
				member.LastSpeakTime = memberInfo.LastSentTime
			}
			member.SpecialTitle = memberInfo.Title
			if memberInfo.SpecialTitle != "" {
				member.SpecialTitle = memberInfo.SpecialTitle
			}
			if memberInfo.ShutUpTimestamp != 0 {
				member.ShutUpTimestamp = memberInfo.ShutUpTimestamp
			}
			member.Permission = perm
			return member, nil
		}
	}

	member := &GroupMemberInfo{
		Group:           group,
		Uin:             userID,
		Nickname:        memberInfo.Nickname,
		CardName:        memberInfo.Card,
		JoinTime:        memberInfo.JoinTime,
		LastSentTime:    memberInfo.LastSentTime,
		LastSpeakTime:   memberInfo.LastSentTime,
		SpecialTitle:    memberInfo.Title,
		ShutUpTimestamp: memberInfo.ShutUpTimestamp,
		Permission:      perm,
	}
	if memberInfo.CardName != "" {
		member.CardName = memberInfo.CardName
	}
	if memberInfo.SpecialTitle != "" {
		member.SpecialTitle = memberInfo.SpecialTitle
	}
	group.Members = append(group.Members, member)
	return member, nil
}

// EnsureGroupMember 返回群成员缓存；缓存中没有该成员时走 RefreshGroupMember 实时拉取。
// 成品证据：adapter.(*Messenger).EnsureGroupMember @0x89d060（320B）：
//   - 入口 "test %rcx,%rcx; jg" 校验 userID>0，否则 fmt.errorf("invalid user id %d")
//     （0x10b2c3e，18B）返回 (nil, err)；
//   - +0xa0 call EnsureGroupCached @0x89bbc0；error 非空立即返回 (nil, err)；
//   - 成功后在 group.Members(0x40/0x48) 内联扫描 member.Uin(0x8)==userID，
//     命中返回 (member,nil)，未命中 +0x100 tail-call RefreshGroupMember @0x89d1a0。
func (m *Messenger) EnsureGroupMember(groupID, userID int64) (*GroupMemberInfo, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("invalid user id %d", userID)
	}
	group, _, err := m.EnsureGroupCached(groupID)
	if err != nil {
		return nil, err
	}
	for _, member := range group.Members {
		if member.Uin == userID {
			return member, nil
		}
	}
	return m.RefreshGroupMember(groupID, userID)
}

// RefreshGroupMember 强制从 OneBot 拉取成员资料并刷新缓存。
// 成品证据：adapter.(*Messenger).RefreshGroupMember @0x89d1a0（544B）：
//   - 入口校验 userID>0，否则 fmt.errorf("invalid user id %d")；
//   - +0xca call EnsureGroupCached @0x89bbc0，error 非空返回 (nil, err)；
//   - +0x? itab 偏移 0x60 的 Adapter.GetGroupMemberInfo(groupID,userID)（间接调用，
//     cg 只记录 E8 直调故不在 callees 中），error 非空返回 (nil, err)；
//   - +0x120 call cacheGroupMember @0x89c940，error 非空时用 logrus.Logf
//     （2 个字段：group/member）打印 "按需刷新群成员缓存: group=%d member=%d"
//     （0x10db204，47B），并返回 (member, err)。
func (m *Messenger) RefreshGroupMember(groupID, userID int64) (*GroupMemberInfo, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("invalid user id %d", userID)
	}
	group, _, err := m.EnsureGroupCached(groupID)
	if err != nil {
		return nil, err
	}
	memberInfo, err := m.Adapter.GetGroupMemberInfo(groupID, userID)
	if err != nil {
		return nil, err
	}
	member, err := m.cacheGroupMember(group, memberInfo)
	if err != nil {
		messengerLogger.WithFields(logrus.Fields{
			"group":  groupID,
			"member": userID,
		}).Errorf("按需刷新群成员缓存: group=%d member=%d", groupID, userID)
		return member, err
	}
	return member, nil
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

// UpdateGroupMember updates a member's info in the cache; 返回是否命中缓存中的成员。
// 返回值证据：recovered/types/types-closure-x64.tsv:6436
//
//	func(int64, int64, func(*adapter.GroupMemberInfo)) bool；
//	handleNoticeEvent @0x89e220 在 +0xdd0 等处以 "test %al,%al" 判断是否需要在
//	RefreshGroupMember 之后再 Update 一次。
func (m *Messenger) UpdateGroupMember(groupID, userID int64, updateFunc func(*GroupMemberInfo)) bool {
	group := m.FindGroupByUin(groupID)
	if group == nil {
		return false
	}
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	for _, member := range group.Members {
		if member.Uin == userID {
			updateFunc(member)
			messengerLogger.Debugf("UpdateGroupMember cache updated: group=%d member=%d", groupID, userID)
			return true
		}
	}
	return false
}

// RefreshMemberInfo 是 RefreshGroupMember 的薄封装。
// 成品证据：adapter.(*Messenger).RefreshMemberInfo @0x89d9a0（96B）与 AddGroupMember
// 反汇编同构：唯一 callees RefreshGroupMember @0x89d1a0，call 后
// "mov %rbx,%rax; mov %rcx,%rbx" 丢弃成员、只返回 error。
func (m *Messenger) RefreshMemberInfo(groupID, userID int64) error {
	_, err := m.RefreshGroupMember(groupID, userID)
	return err
}

// GetGroupInfo 校验群号并向 Adapter 查询完整群资料。
// 成品证据：adapter.(*Messenger).GetGroupInfo @0x89da00
//   - +0x20 "test %rbx,%rbx; jg"：groupCode<=0 时以 19 字节 "invalid group id %d" 返回；
//   - 调用 Adapter.GetGroupInfo 后 error 非空原样返回（+0xcf）；
//   - +0xe0 "test %rax,%rax; je"：返回 nil 时以 48 字节
//     "get_group_info returned empty data for group %d" 报错；
//   - 群号按 GroupID(0x70) → Code(0x8) → Uin(0x0) 依次回退，全为 0 时以 48 字节
//     "get_group_info returned no group id for group %d" 报错；
//   - 与查询群号不一致时以 59 字节
//     "get_group_info returned mismatched group id %d for query %d" 报错；
//   - 群名取 GroupName(0x78/0x80)，为空回退 Name(0x10/0x18)。
func (m *Messenger) GetGroupInfo(groupCode int64) (*GroupInfo, error) {
	if groupCode <= 0 {
		return nil, fmt.Errorf("invalid group id %d", groupCode)
	}

	info, err := m.Adapter.GetGroupInfo(groupCode)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, fmt.Errorf("get_group_info returned empty data for group %d", groupCode)
	}

	groupID := info.GroupID
	if groupID == 0 {
		groupID = info.Code
	}
	if groupID == 0 {
		groupID = info.Uin
	}
	if groupID == 0 {
		return nil, fmt.Errorf("get_group_info returned no group id for group %d", groupCode)
	}
	if groupID != groupCode {
		return nil, fmt.Errorf("get_group_info returned mismatched group id %d for query %d", groupID, groupCode)
	}

	name := info.GroupName
	if name == "" {
		name = info.Name
	}

	return &GroupInfo{
		Uin:             groupID,
		Code:            groupID,
		GroupID:         groupID,
		Name:            name,
		GroupName:       name,
		MemberCount:     info.MemberCount,
		MaxMemberCount:  info.MaxMemberCount,
		GroupCreateTime: info.GroupCreateTime,
		GroupLevel:      info.GroupLevel,
		OwnerUin:        info.OwnerUin,
		Client:          m,
	}, nil
}

func (m *Messenger) RefreshList() error {
	if err := m.ReloadFriendList(); err != nil {
		messengerLogger.WithError(err).Error("unable to load friends list")
	}
	messengerLogger.Infof("已加载 %d 个好友", len(m.FriendList))

	if err := m.ReloadGroupList(); err != nil {
		messengerLogger.WithError(err).Error("unable to load groups list")
	}
	messengerLogger.Infof("已加载 %d 个群组", len(m.GroupList))

	// 成品证据：adapter.(*Messenger).RefreshList @0x89de00 的 callees 只有
	// ReloadFriendList / ReloadGroupList 与 logrus 日志，不含 GetGroupMembersByID，
	// 结尾以 87 字节串 0x11281e0{ptr=0x10ee7c6,len=0x57} Info：
	// 启动时不逐群拉取成员，成员改由按需查询与事件增量缓存维护。
	messengerLogger.Info("群成员采用按需查询与事件增量缓存，启动时不逐群拉取成员列表")

	return nil
}

func (m *Messenger) handleNoticeEvent(event *NoticeEvent) {
	messengerLogger.Debug("收到 OneBot 群事件")
	if m.eventDispatcher == nil {
		return
	}

	// 入口 effective self id 三级回退。成品证据：handleNoticeEvent @0x89e220
	//   - +0x43 取 event.SelfID（event+0x18），非 0 直接使用；
	//   - 为 0 时 +0x4c 调 m.Adapter.GetSelfID()（接口 itab 槽位 0x80）；
	//   - 仍为 0 时 +0x77 取 m.Uin（m+0x18）。
	// 该值用于 group_decrease（0x89fc91）与 group_increase（0x8a0209）的
	// UserID==selfID 判定，两处都先判 selfID!=0。
	selfID := event.SelfID
	if selfID == 0 {
		selfID = m.Adapter.GetSelfID()
	}
	if selfID == 0 {
		selfID = m.Uin
	}

	// 入口公共前置分支。成品证据：handleNoticeEvent @0x89e220 全函数共 3 次
	// EnsureGroupCached（+0x22c 入口、+0x2310 入群分支、+0x2519 成员增加分支），
	// 入口这次在 switch 之前：notice_type 以 "group_" 开头或为 notify/essence
	// （0x89e2a2..0x89e3e7 的 6/7 字节前缀比较）、GroupID>0（0x89e361 "test %r12,%r12"）、
	// 不等于 group_increase（0x89e40d..0x89e436）且非机器人退群（0x89e440 "test %r11b"）
	// 时才实时补查；失败时以 group_id/notice_type/sub_type 三字段（0x10a48be/0x10a921e/0x10a501e）
	// Debug 66 字节串 0x10e6cc0「群事件对应群不在缓存，实时补查未取得群资料」（0x89e9dc）。
	noticeType := event.NoticeType
	groupNotice := strings.HasPrefix(noticeType, "group_") || noticeType == "notify" || noticeType == "essence"
	// 成品 @0x89e340 的 botLeave 判定：group_decrease 且非 kick_me 时比较
	// effective selfID(rdx) 与 event.UserID(0x28)，且 selfID!=0 才置位；
	// 旧重建误用 event.OperatorID==m.Uin。
	botLeave := noticeType == "group_dismiss" ||
		(noticeType == "group_decrease" && (event.SubType == "kick_me" || (selfID != 0 && event.UserID == selfID)))
	if groupNotice && event.GroupID > 0 && noticeType != "group_increase" && !botLeave {
		group, reloaded, err := m.EnsureGroupCached(event.GroupID)
		if err != nil {
			messengerLogger.WithFields(logrus.Fields{
				"group_id":    event.GroupID,
				"notice_type": event.NoticeType,
				"sub_type":    event.SubType,
			}).Debug("群事件对应群不在缓存，实时补查未取得群资料")
		} else if reloaded {
			// 成品证据：0x89e460 "test %bl,%bl" 为真（reloaded）时走 0x89e468..0x89e6b8：
			// 以 group_id（键 0x10a48be，值 event+0x20）/notice_type（0x10a921e，
			// event+0x0）/sub_type（0x10a501e，event+0x50）三字段 Info 打印
			// 72 字节串 0x1128200 -> 0x10ead86「进群事件可能遗漏，已通过后续群事件恢复群资料缓存」
			// （0x89e648 logger.WithFields、0x89e67a level=4、0x89e68f Entry.Log），
			// 随后 0x89e694 取 m.eventDispatcher(0xd8/0xe0)、0x89e6aa 取 itab+0x58
			// （BotEventDispatcher 第 8 个方法 DispatchGroupJoin），0x89e6b6 call *%rcx 派发。
			messengerLogger.WithFields(logrus.Fields{
				"group_id":    event.GroupID,
				"notice_type": event.NoticeType,
				"sub_type":    event.SubType,
			}).Info("进群事件可能遗漏，已通过后续群事件恢复群资料缓存")
			m.eventDispatcher.DispatchGroupJoin(group)
		}
	}

	switch event.NoticeType {
	case "group_ban":
		// 成品 @0x89f265..0x89f30f：先调 UpdateGroupMember(event.GroupID, event.UserID, closure)，
		// 再派发 GroupMute。闭包 func1 @0x8a1980 把 member+0x58（ShutUpTimestamp）写为
		// time.Now().Add(Duration*time.Second).Unix()（call time.Now @0x4f91a0、
		// read internals、add Duration、add internalToUnix -62135596800），
		// 且 Duration<=0 时显式置 0。旧重建漏掉了这次成员缓存刷新。
		m.UpdateGroupMember(event.GroupID, event.UserID, func(member *GroupMemberInfo) {
			if event.Duration > 0 {
				member.ShutUpTimestamp = time.Now().Add(time.Duration(event.Duration) * time.Second).Unix()
			} else {
				member.ShutUpTimestamp = 0
			}
		})
		m.eventDispatcher.DispatchGroupMute(&GroupMuteEvent{
			GroupCode:   event.GroupID,
			OperatorUin: event.OperatorID,
			TargetUin:   event.UserID,
			Time:        event.Duration,
		})
	case "group_increase":
		// Check if it's the bot joining the group。成品 @0x8a0209：先 test selfID!=0，
		// 再比较 effective selfID 与 event.UserID(0x28)。
		if selfID != 0 && event.UserID == selfID {
			// Bot joined the group.
			// 成品证据：handleNoticeEvent @0x89e220 在 UserID==SelfID 时先以 5 字段
			// （group_id/self_id/user_id/operator_id/sub_type）Info 打印串 0x1128220
			// （0x8a02ad..0x8a0517），随即 EnsureGroupCached(event.GroupID)（0x8a0530）。
			// 失败时 WithError.Warn 串 0x1128230（0x8a064f/0x8a0687），用群号建立临时
			// GroupInfo（0x8a068c：Uin/Code/GroupID=event.GroupID、Client=m），持 groupMu
			// 走 FindGroupByUinLocked 内联查找（0x8a1128：命中不入列、未命中 append），
			// 解锁后 scheduleProvisionalGroupRefresh（0x8a1225）。两条路径汇合到
			// RefreshGroupMember(event.GroupID, event.SelfID)（0x8a0dcd/0x8a0dee），
			// 失败仅 WithError.Debug 串 0x1128240（0x8a0f47）。最后以
			// group_name/member_count 两字段 Info 串 0x1128250（0x8a10f2）并
			// DispatchGroupJoin(group)（itab +0x58，0x8a1119）。
			// 成品该分支不调用 GetGroupInfo / GetGroupMembersByID / ReloadGroupList。
			messengerLogger.WithFields(logrus.Fields{
				"group_id":    event.GroupID,
				"self_id":     event.SelfID,
				"user_id":     event.UserID,
				"operator_id": event.OperatorID,
				"sub_type":    event.SubType,
			}).Info("收到 OneBot 机器人入群事件，开始实时更新群列表")
			group, _, err := m.EnsureGroupCached(event.GroupID)
			if err != nil {
				messengerLogger.WithError(err).Warn("进群后实时群资料接口尚未同步，先使用群号建立缓存")
				group = &GroupInfo{
					Uin:     event.GroupID,
					Code:    event.GroupID,
					GroupID: event.GroupID,
					Client:  m,
				}
				m.groupMu.Lock()
				if m.FindGroupByUinLocked(event.GroupID) == nil {
					m.GroupList = append(m.GroupList, group)
				}
				m.groupMu.Unlock()
				m.scheduleProvisionalGroupRefresh(event.GroupID)
			}
			if _, err := m.RefreshGroupMember(event.GroupID, event.SelfID); err != nil {
				messengerLogger.WithError(err).Debug("按需读取机器人自身群成员资料失败")
			}
			messengerLogger.WithFields(logrus.Fields{
				"group_name":   group.Name,
				"member_count": group.MemberCount,
			}).Info("OneBot 新群已写入群列表缓存")
			m.eventDispatcher.DispatchGroupJoin(group)
		} else {
			// Regular member joined
			// 成品证据：member 分支 0x8a0735 call EnsureGroupCached；error 时
			// WithError.Warnf 串 0x10df6f0（"群成员增加事件对应的群不在缓存中: %d"，
			// 0x8a08d0）；found（0x8a0740 test %bl）为真时以 group_id 字段 Info 串
			// 0x1128260 并 DispatchGroupJoin（itab +0x58，0x8a0a57）；随后
			// AddGroupMember（源调用点 0x8a0a71，成品把 96B 薄封装内联成
			// RefreshGroupMember）失败 Warnf "AddGroupMember failed for %d/%d"
			// （0x10c602d，0x8a0c38）。
			group, found, err := m.EnsureGroupCached(event.GroupID)
			if err != nil {
				messengerLogger.WithError(err).Warnf("群成员增加事件对应的群不在缓存中: %d", event.GroupID)
			} else if found {
				messengerLogger.WithField("group_id", event.GroupID).Info("进群事件可能遗漏，已通过群成员增加事件恢复群资料缓存")
				m.eventDispatcher.DispatchGroupJoin(group)
			}
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
	case "group_dismiss", "group_decrease":
		// Check if it's the bot being kicked/leaving the group
		// 成品证据：入口用 r11 标志把 notice_type=="group_dismiss" 与
		// group_decrease 的 kick_me / UserID==effective selfID 合并为同一「机器人离开该群」分支
		// （0x89fc66..0x89fc95 与入口 0x89e340 同构）。
		if botLeave {
			// 成品证据：Warn/Info 消息 0x1128270（level=4 @0x89ff92），WithFields
			// @0x89ff67：operator_id（0x10a8a15,11）= event+0x30；sub_type
			// （0x10a501e,8）= event+0x50/0x58。
			messengerLogger.WithFields(logrus.Fields{
				"operator_id": event.OperatorID,
				"sub_type":    event.SubType,
			}).Info("收到 OneBot 机器人退群事件，开始移除群列表缓存")
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
				if event.NoticeType == "group_dismiss" {
					// 成品证据：+0x3521 Info「收到 LLBot 群解散事件，已移除群列表缓存」
					messengerLogger.WithField("group_id", event.GroupID).Info("收到 LLBot 群解散事件，已移除群列表缓存")
					m.eventDispatcher.DispatchGroupDisband(&GroupDisbandEvent{
						Group:    groupCopy,
						Operator: &GroupMemberInfo{Uin: event.OperatorID},
					})
				} else {
					// 成品证据：+0x317d Info「OneBot 退群已从群列表缓存移除」
					messengerLogger.WithField("group_id", event.GroupID).Info("OneBot 退群已从群列表缓存移除")
					m.eventDispatcher.DispatchGroupLeave(&GroupLeaveEvent{
						Group:    groupCopy,
						Operator: &GroupMemberInfo{Uin: event.OperatorID},
					})
				}
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
		updatePermission := func(member *GroupMemberInfo) {
			member.Permission = newPerm
		}
		// 成品证据：UpdateGroupMember(+0xdd0) 未命中（al==0）时先 RefreshGroupMember
		// (+0xdf1) 再派发。
		if !m.UpdateGroupMember(event.GroupID, event.UserID, updatePermission) {
			if _, err := m.RefreshGroupMember(event.GroupID, event.UserID); err != nil {
				messengerLogger.WithError(err).Warnf("RefreshGroupMember failed for %d/%d", event.GroupID, event.UserID)
			}
		}
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
		updateCard := func(member *GroupMemberInfo) {
			member.CardName = event.CardNew
		}
		// 成品证据：UpdateGroupMember(+0x1280) 未命中时 RefreshGroupMember(+0x12a1) 再派发。
		if !m.UpdateGroupMember(event.GroupID, event.UserID, updateCard) {
			if _, err := m.RefreshGroupMember(event.GroupID, event.UserID); err != nil {
				messengerLogger.WithError(err).Warnf("RefreshGroupMember failed for %d/%d", event.GroupID, event.UserID)
			}
		}
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
			updateTitle := func(member *GroupMemberInfo) {
				member.SpecialTitle = event.Title
			}
			// 成品证据：UpdateGroupMember(+0xdd0) 未命中时 RefreshGroupMember(+0xdf1)，
			// 之后再次 UpdateGroupMember(+0xe32) 再派发。
			if !m.UpdateGroupMember(event.GroupID, event.UserID, updateTitle) {
				if _, err := m.RefreshGroupMember(event.GroupID, event.UserID); err != nil {
					messengerLogger.WithError(err).Warnf("RefreshGroupMember failed for %d/%d", event.GroupID, event.UserID)
				}
				m.UpdateGroupMember(event.GroupID, event.UserID, updateTitle)
			}
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
	case "group_msg_emoji_like":
		m.eventDispatcher.DispatchGroupMsgEmojiLike(&GroupMsgEmojiLikeEvent{
			GroupCode:  event.GroupID,
			UserId:     event.UserID,
			MessageId:  event.MessageID,
			EmojiId:    event.EmojiId,
			EmojiCount: event.EmojiCount,
			IsAdd:      event.IsAdd, // 成品 @0x8a0d8a 直接取 event+0x180（onebot 由 is_add 键解析）
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
	// 成品证据：handleGroupMessage @0x8a1e40 先用 event.Sender(0x50) 复制填充本地 SenderInfo
	// （UserID/Uin 为 0 时回退 event.UserID），再进入群缓存逻辑。
	if event.Sender != nil {
		*sender = *event.Sender
		if sender.UserID == 0 {
			sender.UserID = event.UserID
		}
		if sender.Uin == 0 {
			sender.Uin = event.UserID
		}
	}

	group := m.FindGroup(event.GroupID)
	if group == nil {
		// 群资料不在缓存：改用 EnsureGroupCached 实时补齐，避免事件遗漏。
		// 成品证据：+0x1e0 call EnsureGroupCached @0x89bbc0；error 非空记录
		// +0x41f Warn「收到未知群消息，但按需同步群资料失败」，补齐成功记录
		// +0x575 Info「群事件可能遗漏，已通过群消息恢复群资料缓存」。
		var err error
		group, _, err = m.EnsureGroupCached(event.GroupID)
		if err != nil {
			messengerLogger.WithError(err).WithField("group", event.GroupID).Warn("收到未知群消息，但按需同步群资料失败")
		} else {
			messengerLogger.WithField("group_id", event.GroupID).Info("群事件可能遗漏，已通过群消息恢复群资料缓存")
		}
	}
	if group != nil {
		// 资料不完整时调度异步补齐。成品证据：+0x5e0 call groupMetadataIncomplete，
		// 返回真时 +0x600 call scheduleProvisionalGroupRefresh。
		if groupMetadataIncomplete(group) {
			m.scheduleProvisionalGroupRefresh(event.GroupID)
		}
		// 用事件自带的发送者资料写成员缓存。成品证据：+0x6d0 call cacheGroupMember
		// @0x89c940，实参为 (m, group, &GroupMemberInfo{GroupID,UserID,Uin,Nickname,Card,Role})，
		// 其中成员资料全部来自 event.Sender（成员资料为空时该调用被跳过）。
		if event.Sender != nil {
			if _, err := m.cacheGroupMember(group, &GroupMemberInfo{
				GroupID:  event.GroupID,
				UserID:   event.Sender.UserID,
				Uin:      event.Sender.Uin,
				Nickname: event.Sender.Nickname,
				Card:     event.Sender.Card,
				Role:     event.Sender.Role,
			}); err != nil {
				messengerLogger.WithError(err).Warn("写入发送者群成员缓存失败")
			}
		}
		// 从群成员缓存回填发送者昵称/名片。成品证据：+0x251d 起内联循环比较
		// member.Uin(0x8)==event.UserID，命中后复制 Nickname/CardName 到 SenderInfo。
		if member := group.FindMember(event.UserID); member != nil {
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
	if len(m.offlineQueue) > 0 && cap(m.offlineQueue) >= 100 {
		messengerLogger.Warnf("离线队列已满(%d)，丢弃最旧消息", cap(m.offlineQueue))
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

func (m *Messenger) clearOfflineMsgs() {
	m.offlineQueueMu.Lock()
	defer m.offlineQueueMu.Unlock()
	m.offlineQueue = make([]offlineQueueMsg, 0, 100)
}

func (m *Messenger) flushOfflineQueue() {
	if !getOfflineQueueEnable() {
		return
	}
	msgs := m.loadOfflineMsgs()
	expire := getOfflineQueueExpire()
	now := time.Now()
	messengerLogger.Infof("BOT已上线，开始重发缓存的 %d 条离线消息", len(msgs))

	for _, msg := range msgs {
		if now.Sub(msg.CreatedAt) <= expire {
			// 成品证据：adapter.(*Messenger).flushOfflineQueue 的 callees 含
			// buildMessageSegments @messenger.go:354，但不含 prepareImageElements /
			// buildOutgoingMessageSegments——离线重发走原始段转换。
			messages := m.buildMessageSegments(msg.Message)
			switch msg.TargetType {
			case "group":
				msgID, err := m.Adapter.SendGroupMessage(msg.TargetId, messages)
				if err != nil {
					messengerLogger.Errorf("重发离线群消息失败: %v", err)
				} else {
					messengerLogger.Debugf("离线群消息重发成功: group=%d, msgID=%d", msg.TargetId, msgID)
				}
			case "private":
				msgID, err := m.Adapter.SendPrivateMessage(msg.TargetId, messages)
				if err != nil {
					messengerLogger.Errorf("重发离线私聊消息失败: %v", err)
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
	m.clearOfflineMsgs()
}

func sliceMessage(str string) string {
	if len(str) > 75 {
		return str[:75] + "..."
	}
	return str
}
