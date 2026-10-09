package weibo

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/authprompt"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cookiecloudclient"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"go.uber.org/atomic"
)

// CookieCloud 模式：从用户部署的 CookieCloud 服务同步完整微博登录 Cookie。
const (
	cookieCloudDefaultInterval = 10 * time.Minute
	cookieCloudMinInterval     = time.Minute
	cookieCloudDefaultTimeout  = 15 * time.Second
	cookieCloudCryptoAuto      = "auto"
)

// weiboCookieCloudConfig CookieCloud 拉取配置
type weiboCookieCloudConfig struct {
	Client   cookiecloudclient.Config
	Interval time.Duration
}

var (
	// cloudJarHolder 最近一次从 CookieCloud 拉取并激活的 Cookie Jar
	cloudJarHolder atomic.Value
	// cloudRefreshMu 防止 CookieCloud 拉取重入
	cloudRefreshMu atomic.Bool
	// cookieCloudStartOnce 保证客户端启动日志只打一次
	cookieCloudStartOnce sync.Once
	// cookieCloudFirstFetch 标记是否为启动后的首次拉取
	cookieCloudFirstFetch = atomic.NewBool(true)
)

// loadWeiboCookieCloudConfig 读取 weibo.cookieCloud 配置段。
// 成品 loadWeiboCookieCloudConfig(cookiecloud.go:30, 0xc4ad80) 不在函数内做
// endpoint/uuid 空值短路，而是组装完 Client 后调用 Config.Validate
// （0xc4af60 = 入口+0x1e0），返回 (cloudCfg, error)。
func loadWeiboCookieCloudConfig() (weiboCookieCloudConfig, error) {
	cloudCfg := weiboCookieCloudConfig{}
	cloud := config.GlobalConfig.GetStringMapString("weibo.cookieCloud")
	// 成品证据：loadWeiboCookieCloudConfig(cookiecloud.go:30) 调用
	// cfg.GetWeiboCookieCloudEndpoint/UUID/CryptoType（0xae80c0/0xae8100/0xae8140）；
	// Password/Timeout/Interval 仍直接读 viper（与成品一致）。
	endpoint := cfg.GetWeiboCookieCloudEndpoint()
	uuid := cfg.GetWeiboCookieCloudUUID()
	// 成品 loadWeiboCookieCloudConfig（cookiecloud.go:30）把 Label 作为 Config 最后一个
	// 栈传参（0xc4af47 lea 0x10b0bef len 0x11 = "weibo.cookieCloud"）随 Client 交给
	// Validate，错误文案即 "weibo.cookieCloud.uuid 未配置" 等。
	cloudCfg.Client = cookiecloudclient.Config{
		Endpoint:   endpoint,
		UUID:       uuid,
		Password:   cloud["password"],
		CryptoType: cfg.GetWeiboCookieCloudCryptoType(),
		Timeout:    config.GlobalConfig.GetDuration("weibo.cookieCloud.timeout"),
		Label:      "weibo.cookieCloud",
	}
	if cloudCfg.Client.CryptoType == "" {
		cloudCfg.Client.CryptoType = cookieCloudCryptoAuto
	}
	if cloudCfg.Client.Timeout <= 0 {
		cloudCfg.Client.Timeout = cookieCloudDefaultTimeout
	}
	cloudCfg.Interval = config.GlobalConfig.GetDuration("weibo.cookieCloud.interval")
	if cloudCfg.Interval <= 0 {
		cloudCfg.Interval = cookieCloudDefaultInterval
	}
	// 程序强制最小 1m，避免高频拉取触发服务端限制
	if cloudCfg.Interval < cookieCloudMinInterval {
		cloudCfg.Interval = cookieCloudMinInterval
	}
	if err := cloudCfg.Client.Validate(); err != nil {
		return cloudCfg, err
	}
	return cloudCfg, nil
}

// isWeiboCookieCloudMode 是否启用 CookieCloud 模式（weibo.cookieMode: cookiecloud）
func isWeiboCookieCloudMode() bool {
	mode := strings.TrimSpace(config.GlobalConfig.GetString("weibo.cookieMode"))
	return strings.EqualFold(mode, cookieModeCookieCloud)
}

// refreshWeiboCookieFromCookieCloud 从 CookieCloud 拉取并激活微博登录 Cookie。
// 成品证据：refreshWeiboCookieFromCookieCloud(cookiecloud.go:49, 0xc4b0a0) 经由栈参数
// 接收 weiboCookieCloudConfig（调用点 0xc4f3e6 把 80 字节 cfg 拷到 %rsp），
// 函数内不调用 loadWeiboCookieCloudConfig(0xc4ad80)。
func refreshWeiboCookieFromCookieCloud(cloudCfg weiboCookieCloudConfig) error {
	st := time.Now()
	defer func() {
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", time.Since(st))
	}()
	if !cloudRefreshMu.CompareAndSwap(false, true) {
		return nil
	}
	defer cloudRefreshMu.Store(false)

	cookies, err := cookiecloudclient.FetchCookies(cloudCfg.Client, nil)
	if err != nil {
		return err
	}
	jar, loggedIn, err := buildWeiboCookieCloudJar(cookies)
	if err != nil {
		return err
	}
	if !loggedIn {
		logger.Warn("微博 CookieCloud Cookie未登录，继续使用上一份Cookie")
		return nil
	}
	previous := currentWeiboCookieFingerprint()
	// 成品证据：refreshWeiboCookieFromCookieCloud(cookiecloud.go:49) 调用
	// cfg.GetWeiboProtocolTimeout() 构造 context；预热/验证失败均回退上一份 Cookie，
	// 只有两步都通过才 activateCookieJar 发布新 Jar。
	if p := newWeiboProtocol(jar); p != nil {
		ctx, cancel := weiboProtocolContext()
		body, err := p.spaConfig(ctx)
		if err != nil {
			cancel()
			return fmt.Errorf("微博 CookieCloud Cookie预热失败，继续使用上一份Cookie: %w", err)
		}
		if !spaConfigLoggedIn(body) {
			cancel()
			return fmt.Errorf("微博 CookieCloud Cookie验证失败，继续使用上一份Cookie: %w", errors.New("weibo cookiecloud spa config not logged in"))
		}
		cancel()
		logger.Info("已从 CookieCloud 获取并验证微博Cookie")
	}
	activateCookieJar(cookieModeCookieCloud, jar, "")
	cloudJarHolder.Store(jar)
	if current := currentWeiboCookieFingerprint(); previous != "" && current == previous {
		logger.Debug("CookieCloud 微博Cookie没有变化")
	}
	authprompt.ObserveCredential("weibo", currentWeiboCookieFingerprint())
	return nil
}

// buildWeiboCookieCloudJar 将 CookieCloud 下载的 Cookie 转换为微博 Cookie Jar
// 返回值依次为 Jar、是否包含登录会话（SUB）
func buildWeiboCookieCloudJar(cookies map[string][]cookiecloudclient.Cookie) (*persistentCookieJar, bool, error) {
	jar := newPersistentCookieJar()
	loggedIn := false
	for site, list := range cookies {
		for _, c := range list {
			if c.Name == "" || c.Value == "" {
				continue
			}
			host := strings.TrimPrefix(strings.TrimSpace(c.Domain), ".")
			if host == "" {
				host = strings.TrimPrefix(strings.TrimSpace(site), ".")
			}
			if host == "" || !isWeiboLoginDomain(host) {
				continue
			}
			path := c.Path
			if path == "" {
				path = "/"
			}
			u := &url.URL{Scheme: "https", Host: host, Path: path}
			cookie := &http.Cookie{
				Name:     c.Name,
				Value:    c.Value,
				Domain:   host,
				Path:     path,
				Secure:   c.Secure,
				HttpOnly: c.HTTPOnly,
			}
			if c.ExpirationDate > 0 {
				cookie.Expires = time.Unix(int64(c.ExpirationDate), 0)
			}
			jar.SetCookies(u, []*http.Cookie{cookie})
			if c.Name == "SUB" {
				loggedIn = true
			}
		}
	}
	// 成品 buildWeiboCookieCloudJar（cookiecloud.go:90）两条 errors.New：
	// 0x10e2500 len 0x38 "CookieCloud 中未找到 weibo.com/sina.com.cn 的Cookie"（快照为空）；
	// 0x10ed660 len 0x52 "CookieCloud 的微博Cookie中没有 SUB，请先在浏览器登录微博并同步"
	// （cookieValueForURL(jar,"https://weibo.com/","SUB") 取不到 SUB / 未登录）。
	if len(jar.snapshot()) == 0 {
		return nil, false, errors.New("CookieCloud 中未找到 weibo.com/sina.com.cn 的Cookie")
	}
	if !loggedIn {
		return nil, false, errors.New("CookieCloud 的微博Cookie中没有 SUB，请先在浏览器登录微博并同步")
	}
	return jar, loggedIn, nil
}

// currentWeiboCookieFingerprint 当前生效 Cookie 的指纹，用于检测轮换
func currentWeiboCookieFingerprint() string {
	for _, holder := range []*atomic.Value{&cloudJarHolder, &loginJarHolder, &visitorJarHolder} {
		if jar, ok := holder.Load().(*persistentCookieJar); ok && jar != nil {
			return jar.fingerprint()
		}
	}
	return ""
}

// reportWeiboCookieFailure 上报微博 Cookie 失效：记录日志、触发管理员扫码确认并广播告警
func reportWeiboCookieFailure(reason string) {
	logger.Errorf("微博 Cookie 失效: %s", reason)
	if !cfg.GetWeiboCookieAlertEnabled() {
		logger.Debug("微博 Cookie 告警通知已禁用")
		return
	}
	// 成品调用图证据：reportWeiboCookieFailure(cookiecloud.go:154) 的 GenerateQR 闭包
	// reportWeiboCookieFailure.func1(cookiecloud.go:163) 直接调用 RunQRLogin(qrlogin.go:30)。
	authprompt.ReportFailure(authprompt.Failure{
		Site:        "weibo",
		DisplayName: "微博",
		Mode:        cfg.GetWeiboCookieMode(),
		Fingerprint: currentWeiboCookieFingerprint(),
		Reason:      reason,
		AllowQR:     cfg.GetWeiboQRLoginEnabled(),
		GenerateQR: func() error {
			_, err := RunQRLogin(QRLoginOption{OutputDir: ".", AutoOpen: cfg.GetWeiboQRAutoOpen()})
			return err
		},
	})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("发送微博 Cookie 告警 panic: %v", r)
			}
		}()
		broadcastAlertToAllTargets(
			func(qq int64) { sendPrivateAlert(qq, false) },
			func(groupCode int64) { sendGroupAlert(groupCode, false) },
		)
	}()
}

// hasLocalLoginCookie 本地是否存有可用的登录 Cookie 备份
func hasLocalLoginCookie() bool {
	_, loginPath := currentWeiboStatePaths()
	state, err := loadCookieState(loginPath)
	if err != nil || state == nil {
		return false
	}
	return len(state.Cookies) > 0
}

// refreshWeiboCookieCloud 启动 CookieCloud 定时刷新：立即拉取一次，随后按配置间隔自续
// 非阻塞，由模块启动流程调用。
// 成品证据：(*Concern).refreshWeiboCookieCloud(cookiecloud.go:175, 0xc4c6e0) 的
// weiboCookieCloudConfig 由栈参数传入（Interval 读自 cfg+0x48，用于 time.NewTicker），
// 函数内不调用 loadWeiboCookieCloudConfig；调用方为 Start.gowrap1(concern.go:90)。
func (c *Concern) refreshWeiboCookieCloud(cloudCfg weiboCookieCloudConfig) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("微博 CookieCloud 刷新异常: %v", r)
		}
	}()
	cookieCloudStartOnce.Do(func() {
		logger.Info("微博 CookieCloud 客户端已启动，将立即获取并定时刷新Cookie")
	})
	first := cookieCloudFirstFetch.CompareAndSwap(true, false)
	if err := refreshWeiboCookieFromCookieCloud(cloudCfg); err != nil {
		if first {
			// 首次获取失败：区分本地是否留有登录 Cookie 备份
			if hasLocalLoginCookie() {
				logger.Warnf("CookieCloud 首次获取微博Cookie失败，暂时使用本地登录状态并在后台重试: %v", err)
			} else {
				logger.Warnf("CookieCloud 首次获取微博Cookie失败且本地备用Cookie不可用，将后台继续重试: %v", err)
			}
		} else {
			logger.Warn("CookieCloud 定时获取微博Cookie失败，继续使用上一份Cookie")
		}
	}
	time.AfterFunc(cloudCfg.Interval, func() { c.refreshWeiboCookieCloud(cloudCfg) })
}
