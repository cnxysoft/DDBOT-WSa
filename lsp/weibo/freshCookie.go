package weibo

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"go.uber.org/atomic"
)

// 微博 Cookie 模式
const (
	cookieModeGuest       = "guest"
	cookieModeLogin       = "login"
	cookieModeAPI         = "api"
	cookieModeCookieCloud = "cookiecloud"
)

var (
	// visitorJarHolder 当前生效的访客 Cookie Jar
	visitorJarHolder atomic.Value
	// loginJarHolder 当前生效的登录 Cookie Jar
	loginJarHolder atomic.Value
	// freshCookiePauseUntil 暂停截止时间戳（Unix），0表示未暂停
	freshCookiePauseUntil atomic.Int64
)

// cookiesForMode 返回指定模式当前应使用的 Cookie 选项；api 模式返回 nil
func cookiesForMode(mode string) []requests.Option {
	switch mode {
	case cookieModeAPI:
		return nil
	case cookieModeCookieCloud:
		if jar, ok := cloudJarHolder.Load().(*persistentCookieJar); ok && jar != nil {
			return jarCookieOptions(jar)
		}
		return CookieOption()
	case cookieModeLogin:
		if jar, ok := loginJarHolder.Load().(*persistentCookieJar); ok && jar != nil {
			return jarCookieOptions(jar)
		}
		return CookieOption()
	case cookieModeGuest:
		if jar, ok := visitorJarHolder.Load().(*persistentCookieJar); ok && jar != nil {
			return jarCookieOptions(jar)
		}
		return CookieOption()
	default:
		return CookieOption()
	}
}

// activateCookieJar 将指定模式的 Jar 发布到全局请求选项，并同步 UA。
// 成品调用图证据：activateCookieJar(freshCookie.go:44) 直接调用 cookiesForMode(freshCookie.go:18)，
// 且自身不读配置；mode 由各调用方按所处链路传入。
// [推断] mode 形参为按调用图反推（成品无 config 取值调用，只能是调用方传入的常量）。
func activateCookieJar(mode string, jar *persistentCookieJar, ua string) bool {
	if jar == nil {
		return false
	}
	// 先按模式登记 Jar，cookiesForMode 会从对应 holder 取 Cookie 选项。
	switch mode {
	case cookieModeCookieCloud:
		cloudJarHolder.Store(jar)
	case cookieModeLogin:
		loginJarHolder.Store(jar)
	case cookieModeGuest:
		visitorJarHolder.Store(jar)
	}
	opt := cookiesForMode(mode)
	if len(opt) == 0 {
		return false
	}
	visitorCookiesOpt.Store(opt)
	if ua != "" {
		visitorUA.Store(ua)
	}
	logger.Infof("微博 Cookie 已加载到内存，共 %d 个", len(opt))
	return true
}

// jarCookieOptions 将 Jar 中微博相关域的 Cookie 转为请求选项（按名称去重）
func jarCookieOptions(jar *persistentCookieJar) []requests.Option {
	if jar == nil {
		return nil
	}
	seen := make(map[string]bool)
	var opt []requests.Option
	for _, raw := range []string{
		"https://weibo.com/",
		"https://m.weibo.cn/",
		"https://passport.weibo.com/",
		"https://login.sina.com.cn/",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			continue
		}
		for _, cookie := range jar.Cookies(u) {
			if cookie == nil || cookie.Name == "" || cookie.Value == "" || seen[cookie.Name] {
				continue
			}
			seen[cookie.Name] = true
			opt = append(opt, requests.CookieOption(cookie.Name, cookie.Value))
		}
	}
	return opt
}

// extractCookieValue 从请求选项中提取指定 Cookie 的值
func extractCookieValue(opts []requests.Option, name string) string {
	return requests.ExtractCookieOption(opts, name)
}

// FreshCookieGuest 刷新访客 Cookie：纯 HTTP 访客流程，成功后激活新 Jar
func FreshCookieGuest() ([]*http.Cookie, error) {
	p := newWeiboProtocol(newPersistentCookieJar())
	// 成品证据：FreshCookieGuest(freshCookie.go:73) 调用 cfg.GetWeiboProtocolTimeout()
	// 构造 context，loadCookieMeta(+0xee) 后以 meta.VisitorTID 调用 createVisitor(+0x1d7)。
	ctx, cancel := weiboProtocolContext()
	defer cancel()
	visitorPath, _ := currentWeiboStatePaths()
	meta, metaErr := loadCookieMeta(filepath.Dir(visitorPath))
	if metaErr != nil {
		return nil, metaErr
	}
	spaOnly, visitorTID, visitorNewTID, _, err := p.createVisitor(ctx, meta.VisitorTID)
	if err != nil {
		return nil, err
	}
	// 成品 disasm 证据（FreshCookieGuest @0xc4d700）：
	//   - +0x840 / +0x8c9 两次内联 firstNonEmpty（非 spaOnly 成功路径；disasm c4df40/c4dfc9）：
	//       meta.VisitorTID    = firstNonEmpty(s1, meta.VisitorTID)
	//       meta.VisitorNewTID = firstNonEmpty(s2, meta.VisitorNewTID)
	//   - +0x3b4 time.Now -> meta.VisitorUpdatedAt；
	//   - +0x3f9 saveCookieState(visitorPath, p.userAgent, p.jar.snapshot())，失败仅 Warnf；
	//   - +0x489 writePrivateJSON（内联 saveCookieMeta(stateDir, meta)），失败仅 Warnf；
	//   - spaOnly 为真时跳过 firstNonEmpty，但仍落盘（0xc4dab4 是两条路的汇合点）。
	if !spaOnly {
		meta.VisitorTID = firstNonEmpty(visitorTID, meta.VisitorTID)
		meta.VisitorNewTID = firstNonEmpty(visitorNewTID, meta.VisitorNewTID)
	}
	meta.VisitorUpdatedAt = time.Now()
	if err := saveCookieState(visitorPath, p.userAgent, p.jar.snapshot()); err != nil {
		logger.Warnf("写入微博访客 Cookie 状态失败: %v", err)
	}
	if err := saveCookieMeta(filepath.Dir(visitorPath), meta); err != nil {
		logger.Warnf("写入微博访客 Cookie 元信息失败: %v", err)
	}
	logger.Debugf("微博访客 Cookie 刷新完成: spaOnly=%v visitorTID=%s visitorNewTID=%s", spaOnly, maskSensitive(visitorTID), maskSensitive(visitorNewTID))
	if !activateCookieJar(cookieModeGuest, p.jar, p.userAgent) {
		return nil, fmt.Errorf("微博 %s Cookie Jar 中没有可用 Cookie", cookieModeGuest)
	}
	visitorJarHolder.Store(p.jar)
	cookieHealthy.Store(true)
	consecutiveCookieFails.Store(0)
	u, _ := url.Parse("https://m.weibo.cn/")
	return p.jar.Cookies(u), nil
}

// loadLoginProtocol 加载登录 Cookie Jar 并构建协议客户端
func loadLoginProtocol() (*weiboProtocol, error) {
	_, loginPath := currentWeiboStatePaths()
	state, err := loadCookieState(loginPath)
	jar := newPersistentCookieJar()
	if err != nil {
		logger.Warnf("微博登录 Cookie 预热失败: %v", err)
	} else if state != nil && len(state.Cookies) > 0 {
		jar.importRecords(state.Cookies)
	}
	jar.setConfiguredSUB()
	p := newWeiboProtocol(jar)
	if state != nil && state.UserAgent != "" {
		p.userAgent = state.UserAgent
	}
	return p, err
}

// FreshCookieLogin 刷新登录 Cookie：心跳验证登录状态并捕获轮换；无登录态时上报等待扫码确认
func FreshCookieLogin() ([]*http.Cookie, error) {
	p, lpErr := loadLoginProtocol()
	if lpErr != nil {
		return nil, fmt.Errorf("微博登录 Cookie 预热失败: %w", lpErr)
	}
	weiboURL, _ := url.Parse("https://weibo.com/")
	if cookieValueForURL(p.jar, weiboURL.String(), "SUB") == "" {
		logger.Warn("未找到已保存的微博登录 Cookie")
		reportWeiboCookieFailure("微博登录Cookie不可用，已私聊管理员询问是否生成二维码；未确认前保持等待")
		if !GetQRLoginEnable() {
			logger.Warn("请启用 weibo.qrlogin，重启后使用微博官方 App 重新扫码确认")
		}
		return nil, fmt.Errorf("微博 %s Cookie Jar 中没有可用 Cookie", cookieModeLogin)
	}
	// 成品证据：FreshCookieLogin(freshCookie.go:126) 调用 cfg.GetWeiboProtocolTimeout()。
	ctx, cancel := weiboProtocolContext()
	defer cancel()
	if err := p.refreshLoginCookies(ctx); err != nil {
		logger.Errorf("微博 Cookie 实时刷新失败: %v", err)
		return nil, err
	}
	if !activateCookieJar(cookieModeLogin, p.jar, p.userAgent) {
		return nil, fmt.Errorf("微博 %s Cookie Jar 中没有可用 Cookie", cookieModeLogin)
	}
	loginJarHolder.Store(p.jar)
	cookieHealthy.Store(true)
	consecutiveCookieFails.Store(0)
	logger.Info("微博账号登录状态已通过 getSpaConfig 实时验证")
	u, _ := url.Parse("https://weibo.com/")
	return p.jar.Cookies(u), nil
}

// TryRefreshGuestCookie 尝试刷新访客 Cookie；频率限制暂停期内跳过
func TryRefreshGuestCookie() bool {
	refreshMu.Lock()
	defer refreshMu.Unlock()

	if freshCookiePauseUntil.Load() > time.Now().Unix() {
		logger.Warn("微博访客 Cookie 刷新处于频率限制暂停期")
		return false
	}
	cookies, err := FreshCookieGuest()
	if err != nil {
		logger.Errorf("微博访客临时 Cookie 定时刷新失败: %v", err)
		return false
	}
	if len(cookies) == 0 {
		return false
	}
	return true
}

// ForceFreshCookie 强制刷新 Cookie（按模式分流）；健康时直接返回
func ForceFreshCookie() bool {
	refreshMu.Lock()
	defer refreshMu.Unlock()

	if cookieHealthy.Load() {
		return true
	}

	// API 模式不需要 Cookie
	if cfg.IsWeiboAPIMode() {
		cookieHealthy.Store(true)
		consecutiveCookieFails.Store(0)
		return true
	}

	if isWeiboCookieCloudMode() {
		// 成品证据：ForceFreshCookie(freshCookie.go:184, 0xc4eec0) 的 CookieCloud 分支先
		// 调用 loadWeiboCookieCloudConfig(0xc4ad80, +0x4b1)，再把 80 字节 cfg 拷到 %rsp
		// 调用 refreshWeiboCookieFromCookieCloud(0xc4b0a0, +0x526=0xc4f3e6)；
		// load 失败与 refresh 失败共用同一失败日志。
		cloudCfg, err := loadWeiboCookieCloudConfig()
		if err == nil {
			err = refreshWeiboCookieFromCookieCloud(cloudCfg)
		}
		if err != nil {
			fails := consecutiveCookieFails.Inc()
			cookieHealthy.Store(false)
			logger.WithField("ConsecutiveFails", fails).Errorf("微博 CookieCloud 实时刷新失败: %v", err)
			return false
		}
		cookieHealthy.Store(true)
		consecutiveCookieFails.Store(0)
		return true
	}

	if isGuestMode() {
		if _, err := FreshCookieGuest(); err != nil {
			fails := consecutiveCookieFails.Inc()
			cookieHealthy.Store(false)
			logger.WithField("ConsecutiveFails", fails).Errorf("刷新微博访客 Cookie 失败: %v", err)
			return false
		}
		return true
	}

	if _, err := FreshCookieLogin(); err != nil {
		fails := consecutiveCookieFails.Inc()
		cookieHealthy.Store(false)
		logger.WithField("ConsecutiveFails", fails).Errorf("微博 Cookie 实时刷新失败: %v", err)
		return false
	}
	return true
}

// RefreshSessionCookie 通过登录心跳刷新会话 Cookie（捕获服务端轮换），不影响登录状态
func RefreshSessionCookie() bool {
	refreshMu.Lock()
	defer refreshMu.Unlock()

	p, err := loadLoginProtocol()
	if err != nil {
		return false
	}
	ctx, cancel := weiboProtocolContext()
	defer cancel()
	if err := p.refreshLoginCookies(ctx); err != nil {
		logger.Errorf("微博 Cookie 实时刷新失败: %v", err)
		return false
	}
	if !activateCookieJar(cookieModeLogin, p.jar, p.userAgent) {
		logger.Warnf("微博 %s Cookie Jar 中没有可用 Cookie", cookieModeLogin)
		return false
	}
	loginJarHolder.Store(p.jar)
	cookieHealthy.Store(true)
	logger.Info("微博登录 Cookie 状态实时检查完成")
	return true
}

// PauseRefreshOnRateLimit 频率限制触发后暂停访客 Cookie 刷新
func PauseRefreshOnRateLimit(pauseDuration time.Duration) {
	if pauseDuration <= 0 {
		pauseDuration = time.Minute * 10
	}
	freshCookiePauseUntil.Store(time.Now().Add(pauseDuration).Unix())
	logger.Warnf("微博访客 Cookie 刷新已因频率限制暂停，将在 %v 后恢复", pauseDuration)
}
