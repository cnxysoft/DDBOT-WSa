package bilibili

import (
	"strings"
	"sync/atomic"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/permission"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
)

var (
	bilibiliLoginAlertSent atomic.Bool
	sendBilibiliLoginAlert = sendBilibiliLoginExpiredAlertToAdmins
)

func isBilibiliLoginInvalidResponse(code int32, message string) bool {
	return (code == -101 || code == 4100000) &&
		strings.Contains(message, "未登录")
}

func notifyBilibiliLoginExpired() {
	if !bilibiliLoginAlertSent.CompareAndSwap(false, true) {
		return
	}
	if !sendBilibiliLoginAlert() {
		bilibiliLoginAlertSent.Store(false)
	}
}

func resetBilibiliLoginAlert() {
	bilibiliLoginAlertSent.Store(false)
}

func sendBilibiliLoginExpiredAlertToAdmins() bool {
	if bot.Instance == nil || !bot.Instance.Online.Load() {
		logger.Warn("Bot未在线，无法发送B站登录失效预警")
		return false
	}

	admins := permission.NewStateManager().ListAdmin()
	if len(admins) == 0 {
		logger.Warn("未配置Bot管理员，无法发送B站登录失效预警")
		return false
	}

	sent := false
	for _, qq := range admins {
		msg := newBilibiliLoginExpiredAlertMessage().ToCombineMessage(mmsg.NewPrivateTarget(qq))
		summary := msgstringer.AdapterMsgToString(msg.Elements)
		result := bot.Instance.SendPrivateMessage(qq, msg, summary)
		if result == nil || result.ID == -1 {
			logger.WithField("QQ", qq).Error("发送B站登录失效预警失败")
			continue
		}
		sent = true
		logger.WithField("QQ", qq).Info("已发送B站登录失效预警")
	}
	return sent
}

func newBilibiliLoginExpiredAlertMessage() *mmsg.MSG {
	return mmsg.NewText("[B站登录失效预警]\n" +
		"检测到B站账号未登录，动态和直播订阅推送已停止。\n" +
		"请更新application.yaml中的bilibili.SESSDATA和bilibili.bili_jct后重启；" +
		"或开启bilibili.QRLogin、清空这两项并重启，通过二维码重新登录。")
}
