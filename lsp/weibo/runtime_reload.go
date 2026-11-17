package weibo

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

var transactionalReloadEnabled atomic.Bool

func SetTransactionalReloadEnabled() { transactionalReloadEnabled.Store(true) }

type runtimeSettings struct {
	mode, sub, apiURL, refreshURL string
	autoRefresh                   bool
}

func settingsFromSnapshot(s *config.Snapshot) runtimeSettings {
	settings := runtimeSettings{
		mode: strings.TrimSpace(s.GetString("weibo.mode")), sub: strings.TrimSpace(s.GetString("weibo.sub")),
		apiURL:     strings.TrimRight(strings.TrimSpace(s.GetString("weibo.apiModeBaseURL")), "/"),
		refreshURL: strings.TrimSpace(s.GetString("weibo.cookieRefreshAPI")), autoRefresh: s.GetBool("weibo.autorefresh"),
	}
	if settings.mode == "" {
		settings.mode = "guest"
		if settings.sub != "" {
			settings.mode = "login"
		}
	}
	return settings
}

type preparedRuntime struct {
	settings runtimeSettings
	options  []requests.Option
	ua       string
}

// PrepareRuntimeReload holds the refresh lock until commit or abort. An old
// background refresh therefore cannot overwrite credentials after publication.
func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	old, desired := settingsFromSnapshot(previous), settingsFromSnapshot(next)
	if reflect.DeepEqual(old, desired) {
		return nil, nil, nil
	}
	refreshMu.Lock()
	prepared, err := prepareRuntime(desired)
	if err != nil {
		refreshMu.Unlock()
		return nil, nil, err
	}
	return func() {
		prepared.apply()
		refreshMu.Unlock()
		reconcileSubAutoRefresh(desired.mode == "login" && desired.autoRefresh && desired.refreshURL != "")
	}, refreshMu.Unlock, nil
}

func prepareRuntime(settings runtimeSettings) (*preparedRuntime, error) {
	return prepareRuntimeWith(settings, fetchRuntimeCookies, validateRuntimeLogin, validateRuntimeAPI)
}

func prepareRuntimeWith(settings runtimeSettings,
	fetch func(string, string) ([]*http.Cookie, error),
	validateLogin func([]requests.Option, string) error, validateAPI func(string) error) (*preparedRuntime, error) {
	p := &preparedRuntime{settings: settings, ua: requests.RandomUA(requests.Chrome)}
	if settings.mode != "login" && settings.mode != "guest" && settings.mode != "api" {
		return nil, fmt.Errorf("weibo.mode 必须为 guest、login 或 api")
	}
	if settings.autoRefresh && settings.refreshURL != "" && !validHTTPURL(settings.refreshURL) {
		return nil, fmt.Errorf("weibo.cookieRefreshAPI 必须为有效的 HTTP(S) 地址")
	}
	if settings.mode == "api" {
		if !validHTTPURL(settings.apiURL) {
			return nil, fmt.Errorf("weibo.apiModeBaseURL 必须为有效的 HTTP(S) 地址")
		}
		if err := validateAPI(settings.apiURL); err != nil {
			return nil, err
		}
		return p, nil
	}
	if settings.mode == "login" && settings.sub == "" && settings.autoRefresh && settings.refreshURL != "" {
		cookies, err := freshCookieFromAPIURL(settings.refreshURL)
		if err != nil {
			return nil, fmt.Errorf("微博初始 Cookie API 请求失败，请检查配置和网络")
		}
		settings.sub = ExtractSUBFromCookies(cookies)
		if settings.sub == "" {
			return nil, fmt.Errorf("微博初始 Cookie API 未返回 SUB")
		}
		p.settings = settings
	}
	if settings.mode == "login" && settings.sub == "" {
		// Keep the consumer alive while waiting for WebUI QR login. A future
		// successful save can supply credentials without restarting the module.
		return p, nil
	}
	cookies, err := fetch(settings.mode, p.ua)
	if err != nil {
		return nil, fmt.Errorf("微博候选会话初始化失败，请检查网络后重试")
	}
	for _, cookie := range cookies {
		if cookie != nil && (settings.mode != "login" || cookie.Name != "SUB") {
			p.options = append(p.options, requests.CookieOption(cookie.Name, cookie.Value))
		}
	}
	if settings.mode == "login" {
		p.options = append(p.options, requests.CookieOption("SUB", settings.sub))
		if err := validateLogin(p.options, p.ua); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func validHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func fetchRuntimeCookies(mode, ua string) ([]*http.Cookie, error) {
	if mode == "guest" {
		return freshCookieGuestWithUA(ua)
	}
	return freshCookieLoginWithUA(ua)
}

func validateRuntimeLogin(opts []requests.Option, ua string) error {
	options := append([]requests.Option{}, opts...)
	options = append(options, requests.AddUAOption(ua), requests.TimeoutOption(15*time.Second),
		requests.HeaderOption("Referer", "https://weibo.com/"))
	response := new(ApiContainerGetIndexProfileResponse)
	if err := requests.Get(PathConcainerGetIndex_Profile_Login, CreateParam(5462373877), response, options...); err != nil {
		return fmt.Errorf("微博 Cookie 验证请求失败，请检查网络后重试")
	}
	if response.GetOk() != 1 {
		return fmt.Errorf("微博 Cookie 验证失败，请重新登录")
	}
	return nil
}

func validateRuntimeAPI(baseURL string) error {
	response := new(WeiboMobileProfileResponse)
	if err := requests.Get(baseURL+"/api/Weibo/GetMobileProfile?uid=5462373877", nil, response,
		requests.TimeoutOption(15*time.Second)); err != nil {
		return fmt.Errorf("微博 API 连接验证失败，请检查服务地址和网络")
	}
	if response.ToProfileResponse().GetOk() != 1 {
		return fmt.Errorf("微博 API 返回无效资料，请检查服务")
	}
	return nil
}

func (p *preparedRuntime) apply() {
	cookieStateMu.Lock()
	defer cookieStateMu.Unlock()
	storeCookieOptionsUnlocked(p.options)
	visitorUA.Store(p.ua)
	setRuntimeSUB(p.settings.sub)
	cookieHealthy.Store(p.settings.mode == "api" || len(p.options) > 0)
	consecutiveCookieFails.Store(0)
	freshCookiePauseUntil.Store(0)
}

// finishLogin persists the mode and SUB together. Before the reload participant
// is installed during startup, the same prepared session is applied explicitly.
func finishLogin(sub string) error {
	settings := settingsFromSnapshot(config.GlobalConfig.Snapshot())
	settings.mode, settings.sub = "login", sub
	refreshMu.Lock()
	prepared, err := prepareRuntime(settings)
	refreshMu.Unlock()
	if err != nil {
		return err
	}
	if err := writeLoginConfig(sub); err != nil {
		return err
	}
	if transactionalReloadEnabled.Load() {
		return nil
	}
	refreshMu.Lock()
	current := settingsFromSnapshot(config.GlobalConfig.Snapshot())
	if current.mode == "login" && current.sub == sub {
		prepared.apply()
	}
	refreshMu.Unlock()
	reconcileSubAutoRefresh(current.mode == "login" && current.autoRefresh && current.refreshURL != "")
	config.GlobalConfig.MarkApplied("weibo.sub", "weibo.mode")
	return nil
}
