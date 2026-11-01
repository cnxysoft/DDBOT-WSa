package lsp

import (
	"fmt"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
)

// PushEntry 描述一次订阅推送的处理结果。
// 通过 eventbus（topic: push_record）广播，由 admin 包收集进内存环形缓冲，
// 再经 REST/SSE 提供给 WebUI「推送记录」页。lsp 不直接依赖 admin（admin 反向依赖 lsp）。
type PushEntry struct {
	Time      time.Time `json:"time"`
	Site      string    `json:"site"`
	Type      string    `json:"type"`
	GroupCode int64     `json:"groupCode"`
	Target    string    `json:"target"`
	Summary   string    `json:"summary"`
	Status    string    `json:"status"`
}

// 推送状态
const (
	PushStatusSent    = "sent"    // 已发送
	PushStatusFailed  = "failed"  // 发送失败
	PushStatusSkipped = "skipped" // 被跳过（如平台停用）
)

// publishPush 非阻塞广播一条推送记录；无订阅者时为空操作。
func publishPush(e PushEntry) {
	e.Time = time.Now()
	eventbus.BusObj.Publish("push_record", e)
}

// formatPushTarget 提取订阅对象标识。
func formatPushTarget(inotify concern.Notify) string {
	return fmt.Sprintf("%v", inotify.GetUid())
}

// summarizePushMsg 生成推送消息的文本摘要（截断，供推送记录页展示）。
func summarizePushMsg(m *mmsg.MSG) string {
	if m == nil {
		return ""
	}
	s := msgstringer.AdapterMsgToString(m.Elements())
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

// pushStatusFromMsgs 根据发送结果判定状态：空结果或首条 ID 为 -1 视为失败。
func pushStatusFromMsgs(msgs []*adapter.GroupMessage) string {
	if len(msgs) == 0 || msgs[0].ID == -1 {
		return PushStatusFailed
	}
	return PushStatusSent
}
