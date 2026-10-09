package adapter

import (
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// imageSendRetryBaseDelay 是图片发送确定失败后每次重试前的基准等待间隔。
	// 成品证据：sendGroupChunkWithImageFallback @0x8a4580 的重试循环在 +0x410
	// "imul $0x2faf0800,%rcx,%rax"（rcx = 尝试计数）计算等待时长，
	// 0x2faf0800 = 800000000 ns = 800ms，即第 n 次重试等待 n×800ms。
	imageSendRetryBaseDelay = 800 * time.Millisecond
	// imageSendMaxRetryAttempts 是图片发送确定失败后的最大重试次数。
	// 成品证据：+0x180 "cmp $0x2,%rax" / +0x184 "jg 0x8a4e67"（转移除图片降级）；
	// 尝试计数从 1 开始，达到 3 才降级，即最多重试 2 次。
	imageSendMaxRetryAttempts = 2
)

// sendGroupChunkWithImageFallback 发送单个群消息分片。
// 图片准备失败或图片发送确定失败时：按 n×800ms 退避重试最多两次，
// 仍失败则移除图片元素，把本分片降级为文字/@内容重发，
// 保证订阅通知不因图片问题整体丢失。
//
// 第 3/4 参数是当前分片序号与分片总数：成品调用点 SendGroupMessage
// @0x895a23 传入 rdi=i+1（0x8959fb inc %rdi）、rsi=0x78(%rsp)=len(chunks)
// （0x8957c0 call buildMessageChunks 后 0x8957c5 mov %rbx,0x78(%rsp)）；
// 被调函数在 0x8a45a5/0x8a45b5 把这两个寄存器溢出到 0xb70/0xb78，
// 字段 chunk/chunk_total 即分别取 convT64(0xb70)/convT64(0xb78)。
func (m *Messenger) sendGroupChunkWithImageFallback(groupCode int64, msg *SendingMessage, chunk int, chunkTotal int) SendResp {
	// 成品证据：sendGroupChunkWithImageFallback.func1 @0x8a5980 依次调用
	// prepareImageElements(+0x4f) 与 buildOutgoingMessageSegments(+0xa0)：
	// 图片准备、段转换与发送都在闭包内完成，闭包被调用三次
	// （首次 0x8a460d、重试 0x8a4bcd、降级 0x8a540b）。
	// +0x54 "test %rsi,%rsi" / +0x57 "je 0x8a59fb"：prepareImageElements
	// 返回 error 时 +0x59 写入 msgID=-1、+0x61/+0x66 写入 error 后直接 ret，
	// 不执行 buildOutgoingMessageSegments、不调用 SendGroupMessage，
	// 也不递增 groupSendCount（lock xadd 在 +0xd2）。
	// cleanup（第 4 寄存器 rdi）只在准备成功后登记，由闭包在发送结束后执行
	// （+0xf3 清标志、+0xf8 "mov (%rdx),%rcx; call *%rcx"）。
	// 闭包不持有 msg，因此 msg.Elements 始终保持原始（未准备）内容，
	// 重试会对原图重新执行 prepareImageElements。
	sendWithImagePrepare := func(elements []IMessageElement) (int32, error) {
		prepared, cleanup, prepErr := m.prepareImageElements(elements)
		if prepErr != nil {
			return -1, prepErr
		}
		if cleanup != nil {
			defer cleanup()
		}
		segments := m.buildOutgoingMessageSegments(&SendingMessage{Elements: prepared})
		msgID, err := m.Adapter.SendGroupMessage(groupCode, segments)
		m.groupSendCount.Add(1)
		return msgID, err
	}

	success := func(msgID int32, elements []IMessageElement) SendResp {
		return SendResp{
			RetMSG: &GroupMessage{
				ID:        int64(msgID),
				GroupCode: groupCode,
				Sender: &SenderInfo{
					UserID: m.Uin,
					Uin:    m.Uin,
				},
				Elements: elements,
			},
			Error: nil,
		}
	}
	failed := func(err error) SendResp {
		return SendResp{RetMSG: &GroupMessage{ID: -1, GroupCode: groupCode}, Error: err}
	}

	msgID, err := sendWithImagePrepare(msg.Elements)
	// 成品证据：首次发送的 error 在 0x8a4610/0x8a461c 存入 0x80/0xd8，
	// 重试循环每次更新的是 0xa8/0x3b0；降级 Warn 的字段 original_error
	// 取 0x80（itab 的 +8 作 interface{} 类型字）与 0xd8（data）。
	originalErr := err
	if err == nil {
		return success(msgID, msg.Elements)
	}

	// 非图片原因的失败（超时、断连等）不做降级，原样返回。
	// 成品证据：+0xc8 call countImageElements @0x8a5da0 后 "test %rax,%rax;
	// sete %dl"，计数为 0 即无图可降级，返回失败；否则 +0xf1 call
	// isDefiniteImageSendError @0x8a5ae0 判非图片错误同样返回失败。
	if countImageElements(msg.Elements) == 0 || !isDefiniteImageSendError(err) {
		return failed(err)
	}

	// 图片确定失败（含 prepareImageElements 的「图片准备失败」）：
	// 成品证据：+0x5cc 载入 0x11282c0 Warn「群图片消息发送失败，准备重新提交原消息」，
	// 每次重试前都记录并等待 n×800ms，+0x64d call *%rsi 重新执行 func1。
	for attempt := 1; attempt <= imageSendMaxRetryAttempts; attempt++ {
		delay := time.Duration(attempt) * imageSendRetryBaseDelay
		// 成品证据（Warn 0x11282c0，level=3 @0x8a4b5b）：fields map 在
		// 0x8a4700-0x8a49c8 由 6 次 mapassign_faststr 构造，随后
		// 0x8a4a17 第一次 WithFields、0x8a4b38 第二次 WithFields（key="error"，
		// 全局 0x1b91b60/0x1b91b68，值取 0xa8(itab+8)/0x3b0(data) 即 err）、
		// 0x8a4b70 Entry.Log。key(地址,长度)→value：
		//   group_code(0x10a742b,10)   = convT64(0xb60) = groupCode
		//   chunk(0x109abc2,5)         = convT64(0xb70) = chunk
		//   chunk_total(0x10a9229,11)  = convT64(0xb78) = chunkTotal
		//   attempt(0x10a31d0,7)       = convT64(0xa0)  = attempt
		//   max_attempts(0x10aa8cc,12) = 静态 2 @0x1121270 = imageSendMaxRetryAttempts
		//   retry_after(0x10a8b12,11)  = convT64(attempt*0x2faf0800) = delay
		messengerLogger.WithFields(logrus.Fields{
			"group_code":   groupCode,
			"chunk":        chunk,
			"chunk_total":  chunkTotal,
			"attempt":      attempt,
			"max_attempts": imageSendMaxRetryAttempts,
			"retry_after":  delay,
		}).WithError(err).Warn("群图片消息发送失败，准备重新提交原消息")
		if m.retrySleep != nil {
			m.retrySleep(delay)
		} else {
			time.Sleep(delay)
		}
		msgID, err = sendWithImagePrepare(msg.Elements)
		if err == nil {
			// 成品证据：+0x894 Info「群图片消息重试成功」（0x11282d0，
			// level=4 @0x8a4e23）。fields map 在 0x8a4c30-0x8a4daf 构造：
			//   group_code(0x10a742b,10) = convT64(0xb60)
			//   chunk(0x109abc2,5)       = convT64(0xb70)
			//   attempt(0x10a31d0,7)     = convT64(0xa0)
			// 0x8a4e00 WithFields、0x8a4e38 Entry.Log。
			messengerLogger.WithFields(logrus.Fields{
				"group_code": groupCode,
				"chunk":      chunk,
				"attempt":    attempt,
			}).Info("群图片消息重试成功")
			return success(msgID, msg.Elements)
		}
		// 成品证据：+0x66e call isDefiniteImageSendError 后
		// 0x8a4bf5 "jne 0x8a46cc"（尝试计数 +1 继续循环）；
		// 非图片错误则 0x8a4bfb 直接返回失败。
		if !isDefiniteImageSendError(err) {
			return failed(err)
		}
	}

	// 两次重试后仍为图片错误：移除图片降级，避免整条通知丢失。
	// 成品证据：+0x900 call removeImageElements @0x8a5e60，
	// +0x920 call hasVisibleFallbackContent @0x8a6020；
	// 无可见内容（纯图片消息）时追加一条文字降级提醒（见下），保证不丢通知。
	// removeImageElements 返回 4 个寄存器 rax/rbx/rcx=剩余切片，rdi=被移除
	// 图片数量；调用点 0x8a4e97 把 rdi 存入 0x48(%rsp)，供 removed_images 使用。
	remaining, removedImages := removeImageElements(msg.Elements)
	if !hasVisibleFallbackContent(remaining) {
		// 成品证据：+0xec5 分支（hasVisibleFallbackContent 返回 false）在 +0xee1
		// lea 0x10e3d2a,%rcx、+0xed1 movq $0x3a,0x8(%rax)，即
		// runtime.newobject(adapter.TextSegment @0xf75980) 后写入 Content：
		// ptr=0x10e3d2a、len=0x3a=58；该 58 字节 rodata 含前导空格
		// " 图片发送失败，本条通知已降级为文字提醒"。追加后继续走下方 Warn
		// 与降级发送，避免纯图片消息移除图片后无内容可发。
		remaining = append(remaining, &TextSegment{Content: " 图片发送失败，本条通知已降级为文字提醒"})
	}
	// 成品证据：+0xe3d Warn「群图片消息连续失败，正在移除图片并降级发送文字/@内容」
	// （0x11282e0，level=3 @0x8a53cc），+0xe8b call *%rsi（func1 第三个调用点
	// 0x8a540b）用 remaining 重新准备并发送。fields map 在 0x8a4f74-0x8a5274：
	//   group_code(0x10a742b,10)     = convT64(0xb60)
	//   chunk(0x109abc2,5)           = convT64(0xb70)
	//   chunk_total(0x10a9229,11)    = convT64(0xb78)
	//   removed_images(0x10ad2f3,14) = convT64(0x48) = removeImageElements 的 rdi
	//   retry_count(0x10a9234,11)    = 静态 2 @0x1121270 = imageSendMaxRetryAttempts
	//   original_error(0x10ad301,14) = 0x80(itab+8)/0xd8(data) = 首次发送的 err
	// 随后 0x8a53a9 第二次 WithFields（key="error"，值取 0xa8/0x3b0 = 当前 err）。
	messengerLogger.WithFields(logrus.Fields{
		"group_code":     groupCode,
		"chunk":          chunk,
		"chunk_total":    chunkTotal,
		"removed_images": removedImages,
		"retry_count":    imageSendMaxRetryAttempts,
		"original_error": originalErr,
	}).WithError(err).Warn("群图片消息连续失败，正在移除图片并降级发送文字/@内容")

	msgID, err = sendWithImagePrepare(remaining)
	if err != nil {
		// 成品证据：+0x113c Error「群消息移除图片后仍发送失败」（0x11282f0，
		// level=2 @0x8a56cb）。fields map @0x8a5499-0x8a5544：
		//   group_code(0x10a742b,10) = convT64(0xb60)
		//   chunk(0x109abc2,5)       = convT64(0xb70)
		// 0x8a5587 WithFields、0x8a56a8 第二次 WithFields（"error" = 0x68/0xc8 = err）。
		messengerLogger.WithFields(logrus.Fields{
			"group_code": groupCode,
			"chunk":      chunk,
		}).WithError(err).Error("群消息移除图片后仍发送失败")
		return failed(err)
	}
	// 成品证据：+0x136d Info「群消息已移除异常图片并降级发送成功」（0x1128300，
	// level=4 @0x8a58fc）。fields map @0x8a5711-0x8a58c6：
	//   group_code(0x10a742b,10)     = convT64(0xb60)
	//   chunk(0x109abc2,5)           = convT64(0xb70)
	//   removed_images(0x10ad2f3,14) = convT64(0x48)
	// 0x8a58d9 WithFields、0x8a5911 Entry.Log。
	messengerLogger.WithFields(logrus.Fields{
		"group_code":     groupCode,
		"chunk":          chunk,
		"removed_images": removedImages,
	}).Info("群消息已移除异常图片并降级发送成功")
	return success(msgID, remaining)
}

// isDefiniteImageSendError 判断发送错误是否明确由图片发送引起（而非网络/超时等原因）。
// 成品证据：adapter.isDefiniteImageSendError @0x8a5ae0/send_retry.go:94
//   - err.Error() 先 strings.ToLower；
//   - 含「图片准备失败」直接判定为图片问题（先于超时排除）；
//   - 含 timeout / deadline exceeded / context deadline 判定非图片问题；
//   - 再逐个匹配 12 个关键字，命中任一即图片问题。
func isDefiniteImageSendError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	if strings.Contains(lower, "图片准备失败") {
		return true
	}
	if strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "context deadline") {
		return false
	}
	for _, keyword := range []string{
		"文件异常",
		"大小为 0",
		"ebusy",
		"resource busy",
		"copyfile",
		"enoent",
		"no such file",
		"file not found",
		"图片异常",
		"图片下载失败",
		"image download",
		"invalid image",
	} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// countImageElements 统计元素中的图片数量。
func countImageElements(elements []IMessageElement) int {
	count := 0
	for _, el := range elements {
		if _, ok := el.(*ImageSegment); ok {
			count++
		}
	}
	return count
}

// removeImageElements 返回去除图片元素后的副本（原切片不变）与被移除的图片数量。
// 成品证据：@0x8a5e60 循环内命中 *ImageSegment 时 0x8a5f45 "inc %rcx" 累加，
// 返回前 0x8a5fc5-0x8a5fd1 把第 4 寄存器 rdi 设为该计数；调用方 0x8a4e97
// 取 rdi 作为 removed_images。
func removeImageElements(elements []IMessageElement) ([]IMessageElement, int) {
	remaining := make([]IMessageElement, 0, len(elements))
	removed := 0
	for _, el := range elements {
		if _, ok := el.(*ImageSegment); ok {
			removed++
			continue
		}
		remaining = append(remaining, el)
	}
	return remaining, removed
}

// hasVisibleFallbackContent 判断降级后的元素是否还有可见内容（文字/@/表情）。
func hasVisibleFallbackContent(elements []IMessageElement) bool {
	for _, el := range elements {
		switch e := el.(type) {
		case *TextSegment:
			if strings.TrimSpace(e.Content) != "" {
				return true
			}
		case *AtSegment:
			return true
		case *FaceSegment:
			return true
		}
	}
	return false
}
