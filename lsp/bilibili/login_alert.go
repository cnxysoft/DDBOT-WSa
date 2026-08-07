package bilibili

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
)

type bilibiliLoginAlertSource uint8

const (
	bilibiliLoginSourceSelf bilibiliLoginAlertSource = iota
	bilibiliLoginSourceDynamic
	bilibiliLoginSourceLive
)

type bilibiliLoginAlertStatus struct {
	mu             sync.Mutex
	generation     uint64
	invalidSources map[bilibiliLoginAlertSource]struct{}
	sentAdmins     map[int64]struct{}
}

func newBilibiliLoginAlertStatus() *bilibiliLoginAlertStatus {
	return &bilibiliLoginAlertStatus{
		invalidSources: make(map[bilibiliLoginAlertSource]struct{}),
		sentAdmins:     make(map[int64]struct{}),
	}
}

func (s *bilibiliLoginAlertStatus) markInvalid(source bilibiliLoginAlertSource) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source == bilibiliLoginSourceSelf {
		s.invalidSources[bilibiliLoginSourceDynamic] = struct{}{}
		s.invalidSources[bilibiliLoginSourceLive] = struct{}{}
	} else {
		s.invalidSources[source] = struct{}{}
	}
	return s.generation
}

func (s *bilibiliLoginAlertStatus) shouldSend(generation uint64, qq int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.generation {
		return false
	}
	_, sent := s.sentAdmins[qq]
	return !sent
}

func (s *bilibiliLoginAlertStatus) markSent(generation uint64, qq int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation == s.generation {
		s.sentAdmins[qq] = struct{}{}
	}
}

func (s *bilibiliLoginAlertStatus) markRecovered(source bilibiliLoginAlertSource) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if source == bilibiliLoginSourceSelf {
		if len(s.invalidSources) == 0 {
			return false
		}
		s.invalidSources = make(map[bilibiliLoginAlertSource]struct{})
	} else {
		if _, invalid := s.invalidSources[source]; !invalid {
			return false
		}
		delete(s.invalidSources, source)
		if len(s.invalidSources) != 0 {
			return false
		}
	}
	s.generation++
	s.sentAdmins = make(map[int64]struct{})
	return true
}

func (s *bilibiliLoginAlertStatus) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.invalidSources = make(map[bilibiliLoginAlertSource]struct{})
	s.sentAdmins = make(map[int64]struct{})
}

var (
	bilibiliLoginAlertState   = newBilibiliLoginAlertStatus()
	bilibiliLoginAlertSending atomic.Bool

	listBilibiliLoginAlertAdmins = func() []int64 {
		return permission.NewStateManager().ListAdmin()
	}
	bilibiliLoginAlertBotReady = func() bool {
		return bot.Instance != nil && bot.Instance.Messenger != nil && bot.Instance.Adapter != nil &&
			bot.Instance.Messenger.Online.Load() && bot.Instance.Adapter.IsConnected()
	}
	sendBilibiliLoginAlertToAdmin = sendBilibiliLoginExpiredAlertToAdmin
)

func isBilibiliLoginInvalidResponse(code int32, message string) bool {
	return (code == -101 || code == 4100000) &&
		strings.Contains(message, "未登录")
}

func notifyBilibiliLoginExpired(source bilibiliLoginAlertSource) {
	generation := bilibiliLoginAlertState.markInvalid(source)
	if !bilibiliLoginAlertSending.CompareAndSwap(false, true) {
		return
	}
	defer bilibiliLoginAlertSending.Store(false)

	admins := listBilibiliLoginAlertAdmins()
	if len(admins) == 0 {
		logger.Warn("未配置Bot管理员，无法发送B站登录失效预警")
		return
	}
	if !bilibiliLoginAlertBotReady() {
		logger.Warn("Bot未在线，无法发送B站登录失效预警")
		return
	}

	for _, qq := range admins {
		if !bilibiliLoginAlertState.shouldSend(generation, qq) {
			continue
		}
		if !sendBilibiliLoginAlertToAdmin(qq) {
			continue
		}
		bilibiliLoginAlertState.markSent(generation, qq)
	}
}

func markBilibiliLoginRecovered(source bilibiliLoginAlertSource) {
	if bilibiliLoginAlertState.markRecovered(source) {
		logger.Info("B站登录接口已恢复，重置登录失效预警状态")
	}
}

func sendBilibiliLoginExpiredAlertToAdmin(qq int64) bool {
	msg := newBilibiliLoginExpiredAlertMessage().ToCombineMessage(mmsg.NewPrivateTarget(qq))
	summary := msgstringer.AdapterMsgToString(msg.Elements)
	result := bot.Instance.SendPrivateMessage(qq, msg, summary)
	if result == nil || result.ID == -1 {
		logger.WithField("QQ", qq).Error("发送B站登录失效预警失败")
		return false
	}
	logger.WithField("QQ", qq).Info("已发送B站登录失效预警")
	return true
}

func newBilibiliLoginExpiredAlertMessage() *mmsg.MSG {
	return mmsg.NewText("[B站登录失效预警]\n" +
		"检测到B站账号未登录，动态和直播订阅推送已停止。\n" +
		"请更新application.yaml中的bilibili.SESSDATA和bilibili.bili_jct后重启；" +
		"或开启bilibili.QRLogin、清空这两项并重启，通过二维码重新登录。")
}
