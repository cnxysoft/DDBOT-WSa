package weibo

import (
	"context"
	"fmt"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

var (
	// cookieRefreshCtx 用于控制 Cookie 刷新协程的生命周期
	cookieRefreshCtx    context.Context
	cookieRefreshCancel context.CancelFunc
)

// StartCookieRefreshMonitor 启动 Cookie 有效性监控和自动刷新
func StartCookieRefreshMonitor(sub string) {
	// API 模式才启动监控
	if !cfg.IsWeiboAPIMode() {
		logger.Debug("非 API 模式，不启动 Cookie 自动刷新监控")
		return
	}

	apiURL := cfg.GetWeiboCookieRefreshAPI()
	if apiURL == "" {
		logger.Warn("微博 Cookie 刷新 API 地址未配置，自动刷新功能无法启动")
		return
	}

	// 如果已有上下文，先取消
	if cookieRefreshCancel != nil {
		cookieRefreshCancel()
	}

	cookieRefreshCtx, cookieRefreshCancel = context.WithCancel(context.Background())

	go func() {
		// 初始延迟，等待系统启动完成
		select {
		case <-time.After(5 * time.Second):
		case <-cookieRefreshCtx.Done():
			return
		}

		// 默认每 30 分钟检测一次
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				logger.Debug("开始检测微博 Cookie 有效性")
				if !isCookieValid(sub) {
					logger.Info("检测到微博 Cookie 已失效，尝试从 API 刷新")
					refreshCookieWithAPI(sub)
				} else {
					logger.Debug("微博 Cookie 仍然有效")
				}
			case <-cookieRefreshCtx.Done():
				logger.Info("微博 Cookie 监控已停止")
				return
			}
		}
	}()

	logger.Infof("微博 Cookie 自动刷新监控已启动，API: %s", apiURL)
}

// StopCookieRefreshMonitor 停止 Cookie 监控
func StopCookieRefreshMonitor() {
	if cookieRefreshCancel != nil {
		cookieRefreshCancel()
		cookieRefreshCancel = nil
		cookieRefreshCtx = nil
	}
}

// isCookieValid 检测当前 Cookie 是否有效
func isCookieValid(sub string) bool {
	if sub == "" || isGuestMode() {
		return true // Guest 模式或无 SUB 时不检测
	}

	// 通过访问一个轻量级 API 来验证 Cookie 有效性
	testUid := int64(5462373877) // 使用固定的测试用户
	profileResp, err := ApiContainerGetIndexProfile(testUid)
	if err != nil {
		logger.Debugf("Cookie 有效性检测 - API 请求失败：%v", err)
		return false
	}

	if profileResp.GetOk() != 1 {
		logger.Debugf("Cookie 有效性检测 - 返回错误码：%v", profileResp.GetOk())
		return false
	}

	return true
}

// refreshCookieWithAPI 使用 API 刷新 Cookie
func refreshCookieWithAPI(sub string) {
	cookies, err := FreshCookieFromAPI()
	if err != nil {
		logger.Errorf("从 API 刷新 Cookie 失败：%v", err)
		return
	}

	if len(cookies) == 0 {
		logger.Error("从 API 获取的 Cookie 为空")
		return
	}

	// 应用新的 Cookie
	var opt []requests.Option
	for _, cookie := range cookies {
		// 保留用户配置的 SUB 值
		if cookie.Name == "SUB" && sub != "" {
			cookie.Value = sub
			logger.Debug("使用配置中的 SUB 值")
		}
		opt = append(opt, requests.HttpCookieOption(cookie))
	}

	visitorCookiesOpt.Store(opt)
	logger.Info("微博 Cookie 已成功从 API 刷新")

	// 刷新后验证 Cookie 是否有效
	time.Sleep(1 * time.Second)
	if isCookieValid(sub) {
		logger.Info("Cookie 刷新验证成功")
	} else {
		logger.Warn("Cookie 刷新后验证失败，可能需要检查 API 返回的 Cookie 是否正确")
	}
}

// ManualRefreshCookie 手动触发 Cookie 刷新（供命令调用）
func ManualRefreshCookie(sub string) error {
	if !cfg.IsWeiboAPIMode() {
		return fmt.Errorf("非 API 模式，无法手动刷新 Cookie")
	}

	apiURL := cfg.GetWeiboCookieRefreshAPI()
	if apiURL == "" {
		return fmt.Errorf("微博 Cookie 刷新 API 地址未配置")
	}

	logger.Info("手动触发微博 Cookie 刷新")
	refreshCookieWithAPI(sub)
	return nil
}
