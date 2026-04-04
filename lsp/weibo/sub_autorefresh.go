package weibo

import (
	"context"
	"errors"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

var (
	// ErrNoSUBInResponse API 响应中未包含 SUB
	ErrNoSUBInResponse = errors.New("API 响应中未找到 SUB Cookie")
)

var (
	// subAutoRefreshCtx 用于控制 SUB 自动刷新协程的生命周期
	subAutoRefreshCtx    context.Context
	subAutoRefreshCancel context.CancelFunc
)

// StartSubAutoRefresh 启动 SUB 自动刷新监控（仅 login 模式有效）
func StartSubAutoRefresh() {
	// 仅在 login 模式下启用
	mode := cfg.GetWeiboMode()
	if mode != "login" {
		logger.Debug("非 login 模式，不启动 SUB 自动刷新")
		return
	}

	// 检查是否启用自动刷新
	if !cfg.GetWeiboAutoRefresh() {
		logger.Debug("weibo.autorefresh 未启用")
		return
	}

	apiURL := cfg.GetWeiboCookieRefreshAPI()
	if apiURL == "" {
		logger.Warn("微博 Cookie 刷新 API 地址未配置，SUB 自动刷新功能无法启动")
		return
	}

	// 如果已有上下文，先取消
	if subAutoRefreshCancel != nil {
		subAutoRefreshCancel()
	}

	subAutoRefreshCtx, subAutoRefreshCancel = context.WithCancel(context.Background())

	go func() {
		logger.Info("SUB 自动刷新已启动，将每小时从 API 刷新一次")

		// 初始延迟，等待系统启动完成
		select {
		case <-time.After(5 * time.Second):
		case <-subAutoRefreshCtx.Done():
			return
		}

		// 每小时刷新一次
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				logger.Debug("开始自动刷新微博 SUB")
				if err := refreshSubFromAPI(); err != nil {
					logger.Errorf("SUB 自动刷新失败: %v", err)
				}
			case <-subAutoRefreshCtx.Done():
				logger.Info("SUB 自动刷新已停止")
				return
			}
		}
	}()
}

// StopSubAutoRefresh 停止 SUB 自动刷新
func StopSubAutoRefresh() {
	if subAutoRefreshCancel != nil {
		subAutoRefreshCancel()
		subAutoRefreshCancel = nil
	}
}

// refreshSubFromAPI 从 API 获取新的 SUB 并更新配置
func refreshSubFromAPI() error {
	// 从 API 获取 Cookie
	cookies, err := FreshCookieFromAPI()
	if err != nil {
		return err
	}

	// 提取 SUB
	newSub := ExtractSUBFromCookies(cookies)
	if newSub == "" {
		return ErrNoSUBInResponse
	}

	// 获取当前配置的 SUB
	currentSub := GetSettingCookie()

	// 比较是否相同（如果当前为空，也视为需要更新）
	if currentSub != "" && currentSub == newSub {
		logger.Debug("SUB 未变化，无需更新")
		return nil
	}

	// SUB 不同或当前为空，进行替换
	if currentSub == "" {
		logger.Info("当前 SUB 为空，正在从 API 初始化（仅内存使用）...")
	} else {
		logger.Infof("检测到 SUB 变化，正在更新（仅内存使用）...")
		logger.Debugf("旧 SUB: %s...", maskSub(currentSub))
	}
	logger.Debugf("新 SUB: %s...", maskSub(newSub))

	// 更新内存中的 Cookie
	opt := []requests.Option{
		requests.CookieOption("SUB", newSub),
	}
	visitorCookiesOpt.Store(opt)

	logger.Info("SUB 已成功更新到内存")
	return nil
}

// maskSub 隐藏 SUB 中间部分用于日志显示
func maskSub(sub string) string {
	if len(sub) <= 20 {
		return "***"
	}
	return sub[:10] + "..." + sub[len(sub)-10:]
}
