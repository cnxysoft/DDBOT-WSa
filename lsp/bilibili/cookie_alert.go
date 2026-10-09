package bilibili

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/cnxysoft/DDBOT-WSa/lsp/authprompt"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
)

// Cookie 异常/恢复告警：
// 凭据失效时记录指纹并按指纹去重（同一份Cookie只提醒本次一次），
// 通过 authprompt 私聊管理员（回复“生成 B站”可触发扫码），
// 群通知复用 /config weibo_alert 启用的群；凭据恢复时清理告警状态。
var (
	// bilibiliCookieAlertMu 保护告警指纹状态。
	bilibiliCookieAlertMu sync.Mutex
	// bilibiliCookieAlertedFingerprint 是最近一次已告警的 Cookie 指纹。
	bilibiliCookieAlertedFingerprint string
)

// markBilibiliCookieUnhealthy 标记当前登录凭据失效并发送告警。
// 同一份 Cookie 只提醒本次一次，恢复后指纹清空才允许再次告警。
func markBilibiliCookieUnhealthy(reason string) {
	// 成品调用图证据：markBilibiliCookieUnhealthy(cookie_alert.go:39) 的第一条调用就是
	// tryRefreshBilibiliCookieAfterAuthFailure(cookie_refresh.go:104)。
	// 认证失败后先尝试用 ac_time_value 走官方刷新；刷新成功说明凭据已恢复，不再告警。
	if err := tryRefreshBilibiliCookieAfterAuthFailure(reason); err == nil {
		markBilibiliCookieHealthy()
		return
	}
	fingerprint := bilibiliCookieFingerprint()
	bilibiliCookieAlertMu.Lock()
	alreadyAlerted := fingerprint != "" && fingerprint == bilibiliCookieAlertedFingerprint
	if !alreadyAlerted {
		bilibiliCookieAlertedFingerprint = fingerprint
	}
	bilibiliCookieAlertMu.Unlock()
	if alreadyAlerted {
		logger.WithField("reason", reason).Warn("同一份Cookie只提醒本次一次。")
		return
	}
	logger.WithField("reason", reason).
		Warn("检测到当前登录凭据失效，相关订阅可能无法正常推送")
	logger.Warn("Bot会继续自动重试；登录态彻底失效时需要重新扫码确认")
	sendPendingBilibiliCookieAlert(reason, fingerprint)
}

// markBilibiliCookieHealthy 标记登录凭据已恢复，清理告警指纹。
func markBilibiliCookieHealthy() {
	bilibiliCookieAlertMu.Lock()
	bilibiliCookieAlertedFingerprint = ""
	bilibiliCookieAlertMu.Unlock()
}

// bilibiliCookieFingerprint 计算当前登录凭据的指纹，用于告警去重。
// 无凭据时返回空串。
func bilibiliCookieFingerprint() string {
	verify := getVerify()
	if verify == nil {
		return ""
	}
	material := verify.SESSDATA + "|" + verify.BiliJct
	if verify.DedeUserID != "" {
		material += "|" + verify.DedeUserID
	}
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:16])
}

// sendPendingBilibiliCookieAlert 发送待处理的 Cookie 告警：
// authprompt 私聊提醒管理员（可回复生成二维码），群通知发给 weibo_alert 启用的群。
func sendPendingBilibiliCookieAlert(reason string, fingerprint string) {
	if !cfg.GetBilibiliCookieAlertEnabled() {
		logger.Debug("B站 Cookie 告警通知已禁用")
		return
	}
	authprompt.ReportFailure(authprompt.Failure{
		Site:        Site,
		DisplayName: "B站",
		Mode:        currentBilibiliCookieMode(),
		Fingerprint: fingerprint,
		Reason:      reason,
		AllowQR:     cfg.GetBilibiliQRLoginEnabled(),
		GenerateQR:  runBilibiliQRLoginOnce,
	})
	bilibiliCookieAlertGroups(fingerprint)
}

// bilibiliCookieAlertGroups 向已启用告警的群发送 Cookie 状态异常通知。
// 群列表复用 /config weibo_alert 维护的 WeiboAlertGroups 键，2 小时内不重复发送。
func bilibiliCookieAlertGroups(fingerprint string) {
	for _, groupCode := range enabledAlertGroups() {
		alertKey := localdb.Key("BilibiliCookieAlert", strconv.FormatInt(groupCode, 10))
		err := localdb.Set(alertKey, fingerprint, localdb.SetExpireOpt(2*time.Hour), localdb.SetNoOverWriteOpt())
		if err != nil {
			logger.WithField("GroupCode", groupCode).
				Debug("B站 Cookie 告警已在 2 小时内发送过，跳过")
			continue
		}
		sendBilibiliCookieAlertGroup(groupCode)
	}
}

// sendBilibiliCookieAlertGroup 向单个群发送 Cookie 告警消息。
func sendBilibiliCookieAlertGroup(groupCode int64) {
	if bot.Instance == nil || !bot.Instance.Online.Load() {
		logger.WithField("GroupCode", groupCode).Warn("Bot 未在线，无法发送B站 Cookie 告警")
		return
	}
	m := mmsg.NewMSG()
	m.Textf("[%sCookie告警] %sCookie状态异常", "B站", "B站")
	m.Text("\n检测到当前登录凭据失效，相关订阅可能无法正常推送\n")
	m.Text("Bot会继续自动重试；登录态彻底失效时需要重新扫码确认")
	sm := m.ToCombineMessage(mmsg.NewGroupTarget(groupCode))
	summary := msgstringer.AdapterMsgToString(sm.Elements)
	bot.Instance.SendGroupMessage(groupCode, sm, summary)
	logger.WithField("GroupCode", groupCode).Info("已发送B站 Cookie 告警通知")
}

// enabledAlertGroups 读取 weibo_alert 维护的告警群列表。
func enabledAlertGroups() []int64 {
	val, err := localdb.Get(localdb.Key("WeiboAlertGroups"))
	if err != nil || val == "" {
		return nil
	}
	var groups []int64
	for _, item := range strings.Split(val, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		groupCode, err := strconv.ParseInt(item, 10, 64)
		if err != nil || groupCode <= 0 {
			continue
		}
		groups = append(groups, groupCode)
	}
	return groups
}
