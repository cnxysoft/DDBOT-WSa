package weibo

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
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

			// 检查 SUB 是否有效（仅 Login 模式）
			subValid := true
			subNetErr := false
			if !isGuestMode() {
				sub := GetSettingCookie()
				if sub == "" {
					// 内存中也没有 SUB
					opts := CookieOption()
					sub = extractCookieValue(opts, "SUB")
				}
				if sub != "" {
					subValid, subNetErr = isSUBValidDetailed()
				}
			}

			healthy := cookieHealthy.Load()
			if healthy && subValid {
				if lastAlertSent {
					// 从故障中恢复，发送恢复通知
					sendCookieAlertToAllGroups(true)
					lastAlertSent = false
				}
				continue
			}
			// Cookie 不健康或 SUB 失效：尝试刷新
			refreshed := ForceFreshCookie()
			if refreshed {
				// 刷新成功后重新验证 SUB，避免使用刷新前的过期判断
				if !isGuestMode() {
					subValid, subNetErr = isSUBValidDetailed()
				}
			} else if !isGuestMode() {
				// 刷新失败后也重新验证，区分网络错误和明确的鉴权失效
				_, subNetErr = isSUBValidDetailed()
			}
			if refreshed && subValid {
				if lastAlertSent {
					// 刷新成功且 SUB 有效，发送恢复通知
					sendCookieAlertToAllGroups(true)
				}
				lastAlertSent = false
			} else {
				// 网络错误时 SUB 状态未知，不发送告警，等待下一轮再检查
				if subNetErr {
					logger.Debug("Cookie/SUB 检测遇到网络错误，跳过本次告警")
					continue
				}
				// 刷新失败或 SUB 明确失效：始终尝试发送告警。
				// 已成功的目标由各自的 2 小时去重 key 控制，失败的管理员/群会持续重试，
				// 避免仅有一个目标发送成功就把全局状态置为已发送而停止其余目标的重试。
				if cfg.GetWeiboCookieAlertEnabled() && sendCookieAlertToAllGroups(false) {
					lastAlertSent = true
				}
			}
		}
	}()
}

func freshCookieOpt(sub string) error {
	// API 模式不需要刷新 Cookie，直接从外部 API 获取数据
	if cfg.IsWeiboAPIMode() {
		return nil
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
		errMsg := fmt.Errorf("FreshCookie error: %w", err)
		logger.Errorf("%v", errMsg)
		return errMsg
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
	return nil
}

func GetSettingCookie() string {
	return config.GlobalConfig.GetString("weibo.sub")
}

func GetQRLoginEnable() bool {
	return config.GlobalConfig.GetBool("weibo.qrlogin")
}

// isSUBValid 检测当前 SUB 是否有效
// 通过调用一个轻量级 API 来验证 Cookie/SUB 是否仍然可用
func isSUBValid() bool {
	testUid := int64(5462373877)
	profileResp, err := ApiContainerGetIndexProfile(testUid)
	if err != nil {
		logger.Debugf("SUB 有效性检测失败 - Profile API: %v", err)
		return false
	}
	if profileResp.GetOk() != 1 {
		logger.Debugf("SUB 有效性检测失败 - Profile API 返回错误码: %v", profileResp.GetOk())
		return false
	}
	return true
}

// isSUBValidDetailed 检测当前 SUB 是否有效，并区分网络错误和鉴权失效
// 返回值: (valid, networkError)
// - valid=true: SUB 有效
// - valid=false, networkError=true: 网络错误，SUB 状态未知
// - valid=false, networkError=false: 明确的鉴权失效
func isSUBValidDetailed() (bool, bool) {
	testUid := int64(5462373877)
	profileResp, err := ApiContainerGetIndexProfile(testUid)
	if err != nil {
		// 区分明确的鉴权失效与网络层错误：
		// - 鉴权失效（Cookie/SUB 无效返回 HTML、HTTP 4xx、响应解析失败）应触发告警
		// - 仅网络层错误（DNS、超时、连接拒绝等）时 SUB 状态未知，跳过本次告警
		if isAuthFailure(err) {
			logger.Debugf("SUB 有效性检测失败 - Cookie/SUB 鉴权失效: %v", err)
			return false, false
		}
		logger.Debugf("SUB 有效性检测遇到网络错误: %v", err)
		return false, true
	}
	if profileResp.GetOk() != 1 {
		// API 返回了响应但状态码非 1，说明 SUB 鉴权失效
		logger.Debugf("SUB 有效性检测失败 - Profile API 返回错误码: %v", profileResp.GetOk())
		return false, false
	}
	return true, false
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
// 返回是否有至少一个目标成功发送了告警
func broadcastAlertToAllTargets(sendPrivate func(qq int64) bool, sendGroup func(groupCode int64) bool) bool {
	atLeastOneSuccess := false
	admins := getBotAdmins()
	for _, qq := range admins {
		if sendPrivate(qq) {
			atLeastOneSuccess = true
		}
	}
	alertGroups := getEnabledAlertGroups()
	for _, groupCode := range alertGroups {
		if sendGroup(groupCode) {
			atLeastOneSuccess = true
		}
	}
	if len(admins) == 0 && len(alertGroups) == 0 {
		logger.Debug("无 bot 管理员且无启用告警的群，跳过告警通知")
	}
	return atLeastOneSuccess
}

// sendCookieAlertToAllGroups 发送 Cookie 告警/恢复通知
// 默认私聊发送给管理员，同时发送给已启用告警的群（通过 /config weibo_alert true 开启）
// 返回是否有至少一个目标成功发送了告警
func sendCookieAlertToAllGroups(isRecovery bool) bool {
	if !cfg.GetWeiboCookieAlertEnabled() {
		logger.Debug("微博 Cookie 告警通知已禁用（weibo.disableCookieAlert=true）")
		return false
	}
	return broadcastAlertToAllTargets(
		func(qq int64) bool { return sendPrivateAlert(qq, isRecovery) },
		func(groupCode int64) bool { return sendGroupAlert(groupCode, isRecovery) },
	)
}

// canDeliverMessage 检查当前是否可以投递消息（WS 在线或已启用离线队列）
// 同时检查 Messenger.Online（心跳缓存）和 Adapter.IsConnected()（实际连接状态）
func canDeliverMessage() bool {
	if bot.Instance == nil || bot.Instance.Messenger == nil {
		return false
	}
	// 优先检查实际连接状态，避免缓存标志滞后
	if bot.Instance.Messenger.Adapter.IsConnected() {
		return true
	}
	// WS 离线时，检查是否启用了离线队列
	return config.GlobalConfig.GetBool("bot.offlineQueue.enable")
}

// sendPrivateAlert 私聊发送 Cookie 告警
// 使用 -qq 作为 key，避免与群号冲突（群号为正数）
// 返回 true 表示成功发送（或已进入离线队列）
func sendPrivateAlert(qq int64, isRecovery bool) bool {
	alertKey := c.StateManager.CookieAlertKey(-qq)
	if !isRecovery {
		// 先检查去重：如果最近已发过告警，跳过
		if _, err := localdb.Get(alertKey); err == nil {
			logger.WithField("QQ", qq).Debug("微博 Cookie 告警已在 2 小时内发送过，跳过")
			return false
		}
	} else {
		_, _ = c.StateManager.Delete(alertKey)
		clearAlertDedup(c.StateManager.SUBExpiredAlertKey(-qq))
	}

	// 检查是否可以投递消息（WS 在线或离线队列已启用）
	if !canDeliverMessage() {
		logger.WithField("QQ", qq).Warn("Bot WS 未在线且未启用离线队列，无法发送私聊告警")
		return false
	}

	notify := NewCookieAlertNotify(0, isRecovery)
	m := notify.ToMessage()
	sm := m.ToCombineMessage(mmsg.NewPrivateTarget(qq))
	summary := msgstringer.AdapterMsgToString(sm.Elements)

	resp := bot.Instance.SendPrivateMessage(qq, sm, summary)

	// 仅在发送成功或确认进入离线队列后设置告警去重标记。
	// 直接依据底层返回的明确发送状态判断，不再根据发送后的连接状态推断结果，
	// 避免"连接检查通过后、真正写入前断线"（ErrRequestNotSent 且未入队）被误判为已投递。
	delivered := false
	if !isRecovery {
		switch resp.Status() {
		case adapter.PrivateSendSent, adapter.PrivateSendQueued:
			_ = c.StateManager.Set(alertKey, "",
				localdb.SetExpireOpt(time.Hour*2), localdb.SetNoOverWriteOpt())
			delivered = true
		case adapter.PrivateSendNotSent:
			logger.WithField("QQ", qq).Warn("私聊告警未发送（写入前失败且未入队），不设置去重标记以便重试")
		case adapter.PrivateSendUnknown:
			logger.WithField("QQ", qq).Warn("私聊告警发送结果未知，不设置去重标记以便下轮重试")
		case adapter.PrivateSendRejected:
			logger.WithField("QQ", qq).Warn("私聊告警被 OneBot 明确拒绝，不设置去重标记")
		}
	} else {
		delivered = true
	}
	logger.WithField("QQ", qq).WithField("IsRecovery", isRecovery).Info("已发送微博 Cookie 告警私聊")
	return delivered
}

// sendGroupAlert 群发 Cookie 告警
// 返回 true 表示成功发送（或已进入离线队列）
func sendGroupAlert(groupCode int64, isRecovery bool) bool {
	alertKey := c.StateManager.CookieAlertKey(groupCode)
	if !isRecovery {
		// 先检查去重：如果最近已发过告警，跳过
		if _, err := localdb.Get(alertKey); err == nil {
			logger.WithField("GroupCode", groupCode).
				Debug("微博 Cookie 告警已在 2 小时内发送过，跳过")
			return false
		}
	} else {
		_, _ = c.StateManager.Delete(alertKey)
		clearAlertDedup(c.StateManager.SUBExpiredAlertKey(groupCode))
	}

	// 检查是否可以投递消息（WS 在线或离线队列已启用）
	if !canDeliverMessage() {
		logger.WithField("GroupCode", groupCode).
			Warn("Bot WS 未在线且未启用离线队列，无法发送群告警")
		return false
	}

	notify := NewCookieAlertNotify(groupCode, isRecovery)
	m := notify.ToMessage()
	sm := m.ToCombineMessage(mmsg.NewGroupTarget(groupCode))
	summary := msgstringer.AdapterMsgToString(sm.Elements)
	resp := bot.Instance.SendGroupMessage(groupCode, sm, summary)

	// 仅在发送成功或确认进入离线队列后设置告警去重标记
	delivered := resp.Status() == adapter.GroupSendSent || resp.Status() == adapter.GroupSendQueued
	if !isRecovery && delivered {
		_ = c.StateManager.Set(alertKey, "",
			localdb.SetExpireOpt(time.Hour*2), localdb.SetNoOverWriteOpt())
	}
	if !delivered {
		logger.WithField("GroupCode", groupCode).
			WithField("IsRecovery", isRecovery).
			WithField("Status", resp.Status()).
			Errorf("发送微博 Cookie 告警通知到群失败: %v", resp.Error)
	} else {
		logger.WithField("GroupCode", groupCode).
			WithField("IsRecovery", isRecovery).
			Info("已发送微博 Cookie 告警通知到群")
	}
	return delivered
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
// 一小时去重，避免刷屏
func trySetAlertDedup(alertKey string) bool {
	err := c.StateManager.Set(alertKey, "",
		localdb.SetExpireOpt(time.Hour), localdb.SetNoOverWriteOpt())
	if err != nil {
		if localdb.IsRollback(err) {
			logger.Debug("SUB 过期告警已在 1 小时内发送过，跳过")
			return false
		}
		logger.Errorf("设置 SUB 过期告警状态失败: %v", err)
		return false
	}
	return true
}

// clearAlertDedup 清除告警去重 key，用于 Cookie 恢复时重置状态
func clearAlertDedup(alertKey string) {
	c.StateManager.Delete(alertKey)
}

// sendSUBExpiredAlert 私聊发送 SUB 过期告警
// 使用 -qq 作为 key，避免与群号冲突（群号为正数）
// 返回 true 表示成功发送（或已进入离线队列）
func sendSUBExpiredAlert(qq int64) bool {
	if !trySetAlertDedup(c.StateManager.SUBExpiredAlertKey(-qq)) {
		return false
	}

	// 检查是否可以投递消息
	if !canDeliverMessage() {
		logger.WithField("QQ", qq).Warn("Bot WS 未在线且未启用离线队列，无法发送私聊告警")
		// 发送失败，清除去重标记以便下次重试
		clearAlertDedup(c.StateManager.SUBExpiredAlertKey(-qq))
		return false
	}

	notify := NewSUBExpiredNotify(0)
	m := notify.ToMessage()
	sm := m.ToCombineMessage(mmsg.NewPrivateTarget(qq))
	summary := msgstringer.AdapterMsgToString(sm.Elements)

	resp := bot.Instance.SendPrivateMessage(qq, sm, summary)

	// 直接依据底层返回的明确发送状态判断，不再依赖发送后的连接状态推断结果
	if resp.Status() != adapter.PrivateSendSent && resp.Status() != adapter.PrivateSendQueued {
		logger.WithField("QQ", qq).WithField("Status", resp.Status()).
			Warn("SUB 过期告警私聊发送失败，清除去重标记以便重试")
		clearAlertDedup(c.StateManager.SUBExpiredAlertKey(-qq))
		return false
	}
	logger.WithField("QQ", qq).Info("已发送微博 SUB 过期告警私聊")
	return true
}

// sendSUBExpiredGroupAlert 群发 SUB 过期告警
// 返回 true 表示成功发送（或已进入离线队列）
func sendSUBExpiredGroupAlert(groupCode int64) bool {
	if !trySetAlertDedup(c.StateManager.SUBExpiredAlertKey(groupCode)) {
		return false
	}

	// 检查是否可以投递消息
	if !canDeliverMessage() {
		logger.WithField("GroupCode", groupCode).
			Warn("Bot WS 未在线且未启用离线队列，无法发送群告警")
		// 发送失败，清除去重标记以便下次重试
		clearAlertDedup(c.StateManager.SUBExpiredAlertKey(groupCode))
		return false
	}

	notify := NewSUBExpiredNotify(groupCode)
	m := notify.ToMessage()
	sm := m.ToCombineMessage(mmsg.NewGroupTarget(groupCode))
	summary := msgstringer.AdapterMsgToString(sm.Elements)
	resp := bot.Instance.SendGroupMessage(groupCode, sm, summary)

	if resp.Status() != adapter.GroupSendSent && resp.Status() != adapter.GroupSendQueued {
		logger.WithField("GroupCode", groupCode).
			WithField("Status", resp.Status()).
			Errorf("发送微博 SUB 过期告警通知到群失败: %v", resp.Error)
		// 发送失败，清除去重标记以便下次重试
		clearAlertDedup(c.StateManager.SUBExpiredAlertKey(groupCode))
		return false
	} else {
		logger.WithField("GroupCode", groupCode).
			Info("已发送微博 SUB 过期告警通知到群")
		return true
	}
}
