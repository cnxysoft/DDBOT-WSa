package weibo

import (
	"context"
	"errors"
	"sync"
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
	subAutoRefreshMu     sync.Mutex
	subAutoRefreshCtx    context.Context
	subAutoRefreshCancel context.CancelFunc
)

// StartSubAutoRefresh 启动 SUB 自动刷新监控（仅 login 模式有效）
func StartSubAutoRefresh() {
	reconcileSubAutoRefresh(cfg.GetWeiboMode() == "login" && cfg.GetWeiboAutoRefresh() && cfg.GetWeiboCookieRefreshAPI() != "")
}

func reconcileSubAutoRefresh(enabled bool) {
	subAutoRefreshMu.Lock()
	defer subAutoRefreshMu.Unlock()
	if !enabled {
		if subAutoRefreshCancel != nil {
			subAutoRefreshCancel()
			subAutoRefreshCancel, subAutoRefreshCtx = nil, nil
		}
		return
	}
	if subAutoRefreshCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	subAutoRefreshCtx, subAutoRefreshCancel = ctx, cancel
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				refreshMu.Lock()
				if ctx.Err() == nil && cfg.GetWeiboMode() == "login" && cfg.GetWeiboAutoRefresh() {
					if err := refreshSubFromAPIUnlocked(); err != nil {
						logger.Errorf("SUB 自动刷新失败: %v", err)
					}
				}
				refreshMu.Unlock()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// StopSubAutoRefresh stops only its own generation; a later start has a distinct context.
func StopSubAutoRefresh() {
	reconcileSubAutoRefresh(false)
}

// refreshSubFromAPI 从 API 获取新的 SUB 并更新配置
func refreshSubFromAPI() error {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	return refreshSubFromAPIUnlocked()
}

func refreshSubFromAPIUnlocked() error {
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

	// 保存所有 cookie，保留完整的会话状态
	opt := []requests.Option{
		requests.CookieOption("SUB", newSub),
	}
	for _, cookie := range cookies {
		if cookie.Name != "SUB" {
			opt = append(opt, requests.CookieOption(cookie.Name, cookie.Value))
		}
	}
	storeCookieOptions(opt)

	// 同步更新运行时 SUB，否则后续每小时定时刷新仍会使用旧 SUB，导致自动刷新失效
	setRuntimeSUB(newSub)

	logger.Infof("SUB 已成功更新到内存，共加载 %d 个 cookie", len(opt))
	return nil
}

// maskSub 隐藏 SUB 中间部分用于日志显示
func maskSub(sub string) string {
	if len(sub) <= 20 {
		return "***"
	}
	return sub[:10] + "..." + sub[len(sub)-10:]
}
