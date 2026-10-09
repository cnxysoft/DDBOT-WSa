package weibo

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/Sora233/MiraiGo-Template/config"
	localdb "github.com/cnxysoft/DDBOT-WSa/lsp/buntdb"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	localutils "github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/cnxysoft/DDBOT-WSa/utils/msgstringer"
	"github.com/tidwall/buntdb"
)

var c *Concern

func init() {
	c = NewConcern(concern.GetNotifyChan())
	concern.RegisterConcern(c)

	// 配置文件在 Run 阶段才读取，等框架完成初始化后按 cookieMode 分流 Cookie 维护链路
	go func() {
		for bot.Instance == nil {
			time.Sleep(time.Second)
		}
		switch {
		case cfg.IsWeiboAPIMode():
			// API 模式不需要 Cookie
		case isWeiboCookieCloudMode():
			// CookieCloud 刷新器已由 (*Concern).Start 用其加载的 cfg 启动
			// （成品 Start.gowrap1 concern.go:90），此处不再重复启动。
			logger.Info("CookieCloud 模式由CookieCloud刷新器维护，不启动本地微博Cookie心跳")
		case isGuestMode():
			// 访客模式的临时 Cookie 由订阅刷新链路维护
		case cfg.GetWeiboAutoRefresh():
			startCookieHealthMonitor()
		default:
			logger.Info("weibo.autorefresh 未启用，不启动微博登录 Cookie 心跳")
		}
	}()

	// 每 5 分钟检查 Cookie 健康状态，异常时向所有订阅群发送告警
	go func() {
		var lastAlertSent bool
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			// API 模式不需要检查 Cookie
			if cfg.IsWeiboAPIMode() {
				if lastAlertSent {
					sendCookieAlertToAllGroups(true)
					lastAlertSent = false
				}
				continue
			}

			healthy := cookieHealthy.Load()
			if healthy {
				if lastAlertSent {
					// 从故障中恢复，发送恢复通知
					sendCookieAlertToAllGroups(true)
					lastAlertSent = false
				}
				continue
			}
			// Cookie 不健康：尝试刷新
			refreshed := ForceFreshCookie()
			if refreshed {
				if lastAlertSent {
					// 刷新成功且有此前发过告警，发送恢复通知
					sendCookieAlertToAllGroups(true)
				}
				lastAlertSent = false
			} else if !lastAlertSent {
				// 刷新失败且尚未发过告警，发送告警通知
				// 检查是否启用了 Cookie 告警通知
				if cfg.GetWeiboCookieAlertEnabled() {
					sendCookieAlertToAllGroups(false)
					lastAlertSent = true
				}
			}
		}
	}()
}

// startCookieHealthMonitor 启动微博登录 Cookie 心跳：按 loginRefreshInterval 定时刷新登录 Cookie，
// 捕获服务端真实 Set-Cookie 轮换，并以 getSpaConfig 验证登录状态。
func startCookieHealthMonitor() {
	logger.Info("微博登录 Cookie 心跳与轮换捕获已启动")
	// 成品证据：(*Concern).Start(concern.go:48) 与 StartSubAutoRefresh(sub_autorefresh.go:19)
	// 都调用 cfg.GetWeiboLoginRefreshInterval()（默认30m，钳制 [5m,24h]）。
	interval := cfg.GetWeiboLoginRefreshInterval()
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-c.authStop:
				logger.Info("微博登录 Cookie 心跳已停止")
				return
			case <-ticker.C:
				weiboLoginCookieHeartbeat()
			}
		}
	}()
}

// weiboLoginCookieHeartbeat 执行一次登录 Cookie 心跳，登录态失效时上报并等待管理员扫码确认。
func weiboLoginCookieHeartbeat() {
	refreshMu.Lock()
	defer refreshMu.Unlock()

	p, _ := loadLoginProtocol()
	ctx, cancel := weiboProtocolContext()
	defer cancel()
	if err := p.refreshLoginCookies(ctx); err != nil {
		if err.Error() == "微博登录状态检查未通过" {
			reportWeiboCookieFailure("微博登录 Cookie 心跳验证失败，需要重新扫码确认")
			return
		}
		logger.WithField("error", err).Error("微博登录 Cookie 心跳刷新失败")
		return
	}
	if !activateCookieJar(cookieModeLogin, p.jar, p.userAgent) {
		reportWeiboCookieFailure("微博登录 Cookie 心跳验证失败，需要重新扫码确认")
		return
	}
	loginJarHolder.Store(p.jar)
	cookieHealthy.Store(true)
	consecutiveCookieFails.Store(0)
}

func freshCookieOpt(sub string) {
	// API 模式不需要刷新 Cookie，直接从外部 API 获取数据
	if cfg.IsWeiboAPIMode() {
		return
	}

	var cookies []*http.Cookie
	var err error

	localutils.Retry(3, time.Second, func() bool {
		if isGuestMode() {
			cookies, err = FreshCookieGuest()
		} else {
			cookies, err = FreshCookieLogin()
		}
		return err == nil
	})

	if err != nil {
		logger.Errorf("FreshCookie error %v", err)
		return
	}

	// 保留所有 Cookie
	opt := []requests.Option{}

	// 确定要使用的 SUB 值
	var useSub string
	if sub != "" {
		useSub = sub
		logger.Infof("使用传入的 SUB 参数：%s...", sub[:min(20, len(sub))])
	} else if configuredSub := GetSettingCookie(); configuredSub != "" {
		useSub = configuredSub
		logger.Infof("使用配置中的 SUB")
	}

	// 构建 Cookie 选项
	for _, cookie := range cookies {
		if cookie.Name == "SUB" && useSub != "" {
			// 使用指定的 SUB 替代
			opt = append(opt, requests.CookieOption("SUB", useSub))
		} else {
			opt = append(opt, requests.CookieOption(cookie.Name, cookie.Value))
		}
	}

	// 如果没有找到 SUB 但需要使用指定的值，手动添加
	if useSub != "" {
		hasSub := false
		for _, cookie := range cookies {
			if cookie.Name == "SUB" {
				hasSub = true
				break
			}
		}
		if !hasSub {
			opt = append(opt, requests.CookieOption("SUB", useSub))
		}
	} else {
		// 记录获取到的 SUB
		for _, cookie := range cookies {
			if cookie.Name == "SUB" {
				logger.Infof("使用获取到的 SUB：%s...", cookie.Value[:min(20, len(cookie.Value))])
				break
			}
		}
	}

	visitorCookiesOpt.Store(opt)
	if isGuestMode() {
		logger.Infof("微博 Guest Cookie 已加载，共 %d 个", len(opt))
	} else {
		logger.Infof("微博 Login Cookie 已加载，共 %d 个", len(opt))
	}
}

func GetSettingCookie() string {
	return config.GlobalConfig.GetString("weibo.sub")
}

func GetQRLoginEnable() bool {
	return config.GlobalConfig.GetBool("weibo.qrlogin")
}

// getBotAdmins 从 buntdb Permission 索引查询所有 bot 管理员 QQ
func getBotAdmins() []int64 {
	var admins []int64
	_ = localdb.RCoverTx(func(tx *buntdb.Tx) error {
		tx.Ascend("Permission", func(key, value string) bool {
			splits := strings.Split(key, ":")
			if len(splits) != 3 || splits[2] != "Admin" {
				return true
			}
			i, err := strconv.ParseInt(splits[1], 0, 64)
			if err != nil {
				logger.WithField("Key", key).Errorf("解析 PermissionKey 失败 %v", err)
			} else {
				admins = append(admins, i)
			}
			return true
		})
		return nil
	})
	return admins
}

// broadcastAlertToAllTargets 广播告警到所有目标（管理员私聊 + 已启用告警的群）
func broadcastAlertToAllTargets(sendPrivate func(qq int64), sendGroup func(groupCode int64)) {
	admins := getBotAdmins()
	for _, qq := range admins {
		sendPrivate(qq)
	}
	alertGroups := getEnabledAlertGroups()
	for _, groupCode := range alertGroups {
		sendGroup(groupCode)
	}
	if len(admins) == 0 && len(alertGroups) == 0 {
		logger.Debug("无 bot 管理员且无启用告警的群，跳过告警通知")
	}
}

// sendCookieAlertToAllGroups 发送 Cookie 告警/恢复通知
// 默认私聊发送给管理员，同时发送给已启用告警的群（通过 /config weibo_alert true 开启）
func sendCookieAlertToAllGroups(isRecovery bool) {
	if !cfg.GetWeiboCookieAlertEnabled() {
		logger.Debug("微博 Cookie 告警通知已禁用（weibo.disableCookieAlert=true）")
		return
	}
	broadcastAlertToAllTargets(
		func(qq int64) { sendPrivateAlert(qq, isRecovery) },
		func(groupCode int64) { sendGroupAlert(groupCode, isRecovery) },
	)
}

// sendPrivateAlert 私聊发送 Cookie 告警
// 使用 -qq 作为 key，避免与群号冲突（群号为正数）
func sendPrivateAlert(qq int64, isRecovery bool) {
	alertKey := c.StateManager.CookieAlertKey(-qq)
	if !isRecovery {
		err := c.StateManager.Set(alertKey, "",
			localdb.SetExpireOpt(time.Hour*2), localdb.SetNoOverWriteOpt())
		if err != nil {
			logger.WithField("QQ", qq).Debug("微博 Cookie 告警已在 2 小时内发送过，跳过")
			return
		}
	} else {
		_, _ = c.StateManager.Delete(alertKey)
	}

	notify := NewCookieAlertNotify(0, isRecovery)
	m := notify.ToMessage()
	sm := m.ToCombineMessage(mmsg.NewPrivateTarget(qq))
	summary := msgstringer.AdapterMsgToString(sm.Elements)

	if bot.Instance == nil || !bot.Instance.Online.Load() {
		logger.WithField("QQ", qq).Warn("Bot 未在线，无法发送私聊告警")
		return
	}
	bot.Instance.SendPrivateMessage(qq, sm, summary)
	logger.WithField("QQ", qq).WithField("IsRecovery", isRecovery).Info("已发送微博 Cookie 告警私聊")
}

// sendGroupAlert 群发 Cookie 告警
func sendGroupAlert(groupCode int64, isRecovery bool) {
	alertKey := c.StateManager.CookieAlertKey(groupCode)
	if !isRecovery {
		err := c.StateManager.Set(alertKey, "",
			localdb.SetExpireOpt(time.Hour*2), localdb.SetNoOverWriteOpt())
		if err != nil {
			logger.WithField("GroupCode", groupCode).
				Debug("微博 Cookie 告警已在 2 小时内发送过，跳过")
			return
		}
	} else {
		_, _ = c.StateManager.Delete(alertKey)
	}
	notify := NewCookieAlertNotify(groupCode, isRecovery)
	concern.GetNotifyChan() <- notify
	logger.WithField("GroupCode", groupCode).
		WithField("IsRecovery", isRecovery).
		Info("已发送微博 Cookie 告警通知")
}

// NotifySUBExpired 发送 SUB 过期告警通知
// 默认私聊发送给管理员，同时发送给已启用告警的群
func NotifySUBExpired() {
	if !cfg.GetWeiboCookieAlertEnabled() {
		logger.Debug("微博告警通知已禁用（weibo.disableCookieAlert=true）")
		return
	}
	broadcastAlertToAllTargets(
		sendSUBExpiredAlert,
		sendSUBExpiredGroupAlert,
	)
}

// getEnabledAlertGroups 从数据库读取所有已启用告警的群列表
func getEnabledAlertGroups() []int64 {
	val, err := localdb.Get(localdb.Key("WeiboAlertGroups"))
	if err != nil || val == "" {
		return nil
	}
	var groups []int64
	for _, s := range strings.Split(val, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		groupCode, err := strconv.ParseInt(s, 10, 64)
		if err == nil && groupCode > 0 {
			groups = append(groups, groupCode)
		}
	}
	return groups
}

// enableAlertGroup 启用指定群的 Cookie 告警通知
func enableAlertGroup(groupCode int64) error {
	return localdb.RWCover(func() error {
		groups := getEnabledAlertGroups()
		for _, g := range groups {
			if g == groupCode {
				return nil // 已启用
			}
		}
		groups = append(groups, groupCode)
		strs := make([]string, len(groups))
		for i, g := range groups {
			strs[i] = strconv.FormatInt(g, 10)
		}
		return localdb.Set(localdb.Key("WeiboAlertGroups"), strings.Join(strs, ","))
	})
}

// disableAlertGroup 禁用指定群的 Cookie 告警通知
func disableAlertGroup(groupCode int64) error {
	return localdb.RWCover(func() error {
		groups := getEnabledAlertGroups()
		var filtered []int64
		for _, g := range groups {
			if g != groupCode {
				filtered = append(filtered, g)
			}
		}
		if len(filtered) == 0 {
			_, _ = localdb.Delete(localdb.Key("WeiboAlertGroups"))
			return nil
		}
		strs := make([]string, len(filtered))
		for i, g := range filtered {
			strs[i] = strconv.FormatInt(g, 10)
		}
		return localdb.Set(localdb.Key("WeiboAlertGroups"), strings.Join(strs, ","))
	})
}

// isAlertGroupEnabled 检查指定群是否启用了 Cookie 告警通知
func isAlertGroupEnabled(groupCode int64) bool {
	for _, g := range getEnabledAlertGroups() {
		if g == groupCode {
			return true
		}
	}
	return false
}

// EnableAlertGroup 启用指定群的 Cookie 告警通知（供外部调用）
func EnableAlertGroup(groupCode int64) error {
	return enableAlertGroup(groupCode)
}

// DisableAlertGroup 禁用指定群的 Cookie 告警通知（供外部调用）
func DisableAlertGroup(groupCode int64) error {
	return disableAlertGroup(groupCode)
}

// IsAlertGroupEnabled 检查指定群是否启用了 Cookie 告警通知（供外部调用）
func IsAlertGroupEnabled(groupCode int64) bool {
	return isAlertGroupEnabled(groupCode)
}

// GetBotAdmins 获取所有 bot 管理员 QQ（供外部调用）
func GetBotAdmins() []int64 {
	return getBotAdmins()
}

// GetEnabledAlertGroups 获取所有已启用告警的群列表（供外部调用）
func GetEnabledAlertGroups() []int64 {
	return getEnabledAlertGroups()
}

// trySetAlertDedup 尝试设置告警去重 key，返回 true 表示可以发送告警
func trySetAlertDedup(alertKey string) bool {
	err := c.StateManager.Set(alertKey, "",
		localdb.SetExpireOpt(time.Hour*24), localdb.SetNoOverWriteOpt())
	if err != nil {
		if localdb.IsRollback(err) {
			logger.Debug("SUB 过期告警已在 24 小时内发送过，跳过")
		} else {
			logger.Errorf("设置 SUB 过期告警状态失败: %v", err)
		}
		return false
	}
	return true
}

// sendSUBExpiredAlert 私聊发送 SUB 过期告警
// 使用 -qq 作为 key，避免与群号冲突（群号为正数）
func sendSUBExpiredAlert(qq int64) {
	if !trySetAlertDedup(c.StateManager.SUBExpiredAlertKey(-qq)) {
		return
	}

	notify := NewSUBExpiredNotify(0)
	m := notify.ToMessage()
	sm := m.ToCombineMessage(mmsg.NewPrivateTarget(qq))
	summary := msgstringer.AdapterMsgToString(sm.Elements)

	if bot.Instance == nil || !bot.Instance.Online.Load() {
		logger.WithField("QQ", qq).Warn("Bot 未在线，无法发送私聊告警")
		return
	}
	bot.Instance.SendPrivateMessage(qq, sm, summary)
	logger.WithField("QQ", qq).Info("已发送微博 SUB 过期告警私聊")
}

// sendSUBExpiredGroupAlert 群发 SUB 过期告警
func sendSUBExpiredGroupAlert(groupCode int64) {
	if !trySetAlertDedup(c.StateManager.SUBExpiredAlertKey(groupCode)) {
		return
	}

	notify := NewSUBExpiredNotify(groupCode)
	concern.GetNotifyChan() <- notify
	logger.WithField("GroupCode", groupCode).
		Info("已发送微博 SUB 过期告警通知")
}
