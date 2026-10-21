package twitter

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
)

// twitterIncidentKind 区分告警类型：只有登录态失效才是 Cookie 问题，
// 网络/代理/响应解码类故障必须用另一套文案，否则会把真故障误报成 Cookie 失效。
type twitterIncidentKind int32

const (
	twitterIncidentAuth  twitterIncidentKind = iota // 登录态失效（Cookie 无效、被撤销、账号受限）
	twitterIncidentFetch                            // 抓取持续失败（网络、代理、接口或响应解码异常）
)

func (k twitterIncidentKind) label() string {
	if k == twitterIncidentAuth {
		return "登录态失效"
	}
	return "抓取持续失败"
}

// twitterLoginAlertState 记录Cookie失效告警的发送状态，采用与bilibili登录失效预警相同的
// generation机制：同一代失效期间每个管理员只收到一条告警，恢复后generation递增，
// 再次失效时重新通知，避免轮询期间向管理员重复轰炸。
type twitterLoginAlertStatus struct {
	mu         sync.Mutex
	generation uint64
	invalid    bool
	kind       twitterIncidentKind
	sentAdmins map[int64]struct{}
}

func newTwitterLoginAlertState() *twitterLoginAlertStatus {
	return &twitterLoginAlertStatus{
		sentAdmins: make(map[int64]struct{}),
	}
}

func (s *twitterLoginAlertStatus) markInvalid(kind twitterIncidentKind) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invalid = true
	s.kind = kind
	return s.generation
}

func (s *twitterLoginAlertStatus) shouldSend(generation uint64, qq int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.generation {
		return false
	}
	_, sent := s.sentAdmins[qq]
	return !sent
}

func (s *twitterLoginAlertStatus) markSent(generation uint64, qq int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation == s.generation {
		s.sentAdmins[qq] = struct{}{}
	}
}

func (s *twitterLoginAlertStatus) markRecovered() (twitterIncidentKind, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.invalid {
		return 0, false
	}
	kind := s.kind
	s.invalid = false
	s.generation++
	s.sentAdmins = make(map[int64]struct{})
	return kind, true
}

func (s *twitterLoginAlertStatus) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.invalid = false
	s.sentAdmins = make(map[int64]struct{})
}

var (
	twitterLoginAlertState   = newTwitterLoginAlertState()
	twitterLoginAlertSending atomic.Bool

	listTwitterLoginAlertAdmins = func() []int64 {
		return permission.NewStateManager().ListAdmin()
	}
	twitterLoginAlertBotReady = func() bool {
		return bot.Instance != nil && bot.Instance.Messenger != nil && bot.Instance.Adapter != nil &&
			bot.Instance.Messenger.Online.Load() && bot.Instance.Adapter.IsConnected()
	}
	sendTwitterLoginAlertToAdmin = sendTwitterAlertPrivateMessage
)

// notifyTwitterIncident 发送一次告警；同一代失效期间每个管理员只收到一条。
func notifyTwitterIncident(kind twitterIncidentKind, reason string) {
	generation := twitterLoginAlertState.markInvalid(kind)

	if !twitterLoginAlertSending.CompareAndSwap(false, true) {
		return
	}
	defer twitterLoginAlertSending.Store(false)

	admins := listTwitterLoginAlertAdmins()
	if len(admins) == 0 {
		logger.Warn("未配置Bot管理员，无法发送Twitter预警")
		return
	}
	if !twitterLoginAlertBotReady() {
		logger.Warn("Bot未在线，无法发送Twitter预警")
		return
	}

	for _, qq := range admins {
		if !twitterLoginAlertState.shouldSend(generation, qq) {
			continue
		}
		if !sendTwitterLoginAlertToAdmin(qq, newTwitterIncidentAlertMessage(kind, reason)) {
			continue
		}
		twitterLoginAlertState.markSent(generation, qq)
	}
}

func notifyTwitterIncidentRecovered(downtime time.Duration) {
	kind, ok := twitterLoginAlertState.markRecovered()
	if !ok {
		return
	}
	logger.Infof("Twitter已恢复（%s），重置预警状态", kind.label())

	if !twitterLoginAlertBotReady() {
		logger.Warn("Bot未在线，无法发送Twitter恢复通知")
		return
	}
	for _, qq := range listTwitterLoginAlertAdmins() {
		sendTwitterLoginAlertToAdmin(qq, newTwitterRecoveredMessage(kind, downtime))
	}
}

func sendTwitterAlertPrivateMessage(qq int64, msg *mmsg.MSG) bool {
	combined := msg.ToCombineMessage(mmsg.NewPrivateTarget(qq))
	summary := msgstringer.AdapterMsgToString(combined.Elements)
	result := bot.Instance.SendPrivateMessage(qq, combined, summary)
	if result.Error != nil || result.RetMSG == nil || result.RetMSG.ID == -1 {
		logger.WithField("QQ", qq).Error("发送Twitter预警消息失败")
		return false
	}
	logger.WithField("QQ", qq).Info("已发送Twitter预警消息")
	return true
}

func newTwitterIncidentAlertMessage(kind twitterIncidentKind, reason string) *mmsg.MSG {
	if kind == twitterIncidentAuth {
		return mmsg.NewText(fmt.Sprintf(
			"[Twitter Cookie失效预警]\n"+
				"检测到Twitter Cookie失效：%s\n"+
				"Twitter订阅推送已暂停，正在自动重试恢复，恢复后会再次私聊通知。\n"+
				"如长时间未恢复，请更新application.yaml中的twitter.auth_token和twitter.ct0后重启进程。", reason))
	}
	return mmsg.NewText(fmt.Sprintf(
		"[Twitter抓取异常]\n"+
			"检测到Twitter持续抓取失败（不是Cookie问题）：%s\n"+
			"常见原因是代理不通、接口变动或响应格式变化。推送已暂停并自动重试，恢复后会再次私聊通知。", reason))
}

func newTwitterRecoveredMessage(kind twitterIncidentKind, downtime time.Duration) *mmsg.MSG {
	title := "[Twitter Cookie已恢复]"
	if kind == twitterIncidentFetch {
		title = "[Twitter抓取已恢复]"
	}
	return mmsg.NewText(fmt.Sprintf(
		"%s\n本次中断时长约 %s，Twitter订阅推送已恢复。", title, downtime))
}
