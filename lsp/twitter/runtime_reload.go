package twitter

import (
	"fmt"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
)

type twitterRuntime struct {
	mode, fetchMode, userAgent string
	baseURLs                   []string
	jar                        *cookiejar.Jar
	api                        *TwitterAPI
}

var liveTwitter atomic.Pointer[twitterRuntime]
var twitterStateMu sync.Mutex

// Legacy globals remain construction defaults for integrations. Live generations
// are immutable; in-flight API requests retain their own client after a reload.
func currentTwitterAPI() *TwitterAPI {
	if r := liveTwitter.Load(); r != nil {
		return r.api
	}
	return twitterAPI
}
func currentTwitterMode() string {
	if r := liveTwitter.Load(); r != nil {
		return r.mode
	}
	return TwitterMode
}
func currentTwitterFetchMode() string {
	if r := liveTwitter.Load(); r != nil {
		return r.fetchMode
	}
	return TwitterAPIFetchMode
}
func twitterUserAgent() string {
	if r := liveTwitter.Load(); r != nil {
		return r.userAgent
	}
	return UserAgent
}
func twitterCookieJar() *cookiejar.Jar {
	if r := liveTwitter.Load(); r != nil {
		return r.jar
	}
	return Cookie
}
func twitterBaseURLs() []string {
	if r := liveTwitter.Load(); r != nil {
		return r.baseURLs
	}
	return BaseURL
}

func newTwitterRuntime(s *config.Snapshot) (*twitterRuntime, error) {
	return newTwitterRuntimeWithProxy(s, nil)
}

func newTwitterRuntimeWithProxy(s *config.Snapshot, getter proxy_pool.Getter) (*twitterRuntime, error) {
	r := &twitterRuntime{mode: resolveTwitterMode(strings.ToLower(strings.TrimSpace(s.GetString("twitter.mode")))), fetchMode: normalizeAPIFetchMode(s.GetString("twitter.apiFetchMode")), userAgent: s.GetString("twitter.userAgent"), baseURLs: s.GetStringSlice("twitter.baseUrl")}
	if mode := strings.TrimSpace(s.GetString("twitter.mode")); mode != "" && !strings.EqualFold(mode, ModeAPI) && !strings.EqualFold(mode, ModeMirror) {
		return nil, fmt.Errorf("twitter.mode 必须为 api 或 mirror")
	}
	if r.userAgent == "" {
		r.userAgent = UserAgent
	}
	if len(r.baseURLs) == 0 {
		r.baseURLs = append([]string(nil), BaseURL...)
	}
	for _, address := range r.baseURLs {
		u, e := url.Parse(address)
		if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
			return nil, fmt.Errorf("twitter.baseUrl 必须为 HTTP(S) 地址列表")
		}
	}
	r.jar, _ = cookiejar.New(nil)
	r.api = NewTwitterAPI("", "", "", "", "")
	if r.mode == ModeAPI {
		ct0, auth := s.GetString("twitter.ct0"), s.GetString("twitter.auth_token")
		if (ct0 == "") != (auth == "") {
			return nil, fmt.Errorf("Twitter ct0 和 auth_token 必须同时提供")
		}
		r.api = NewTwitterAPI(ct0, auth, s.GetString("twitter.bearerToken"), s.GetString("twitter.queryId"), s.GetString("twitter.screenName"))
		r.api.SetUserTimelineQueryId(s.GetString("twitter.userTweetsQueryId"))
		if r.api != nil {
			r.api.runtimeUserAgent = r.userAgent
			r.api.proxyGetter = getter
		}
	}
	return r, nil
}

var validateTwitterCandidate = func(api *TwitterAPI) error {
	screenName, mainJS, err := api.FetchInitialState()
	if err != nil || screenName == "" {
		return fmt.Errorf("Twitter Cookie 验证失败，请检查凭据与网络")
	}
	api.SetScreenName(screenName)
	api.setMainJS(mainJS)
	return nil
}

func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	return PrepareRuntimeReloadWithProxy(previous, next, nil)
}

func PrepareRuntimeReloadWithProxy(previous, next *config.Snapshot, getter proxy_pool.Getter) (func(), func(), error) {
	changed := previous == nil
	for _, key := range []string{"mode", "apiFetchMode", "baseUrl", "userAgent", "ct0", "auth_token", "bearerToken", "queryId", "userTweetsQueryId", "screenName"} {
		if previous != nil && !reflect.DeepEqual(previous.Get("twitter."+key), next.Get("twitter."+key)) {
			changed = true
		}
	}
	if !changed {
		return nil, nil, nil
	}
	r, err := newTwitterRuntimeWithProxy(next, getter)
	if err != nil {
		return nil, nil, err
	}
	if r.api != nil && r.api.IsEnabled() {
		if err = validateTwitterCandidate(r.api); err != nil {
			return nil, nil, err
		}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			twitterStateMu.Lock()
			defer twitterStateMu.Unlock()
			liveTwitter.Store(r)
			twitterFetchFailures.Store(0)
			twitterRecovering.Store(false)
		})
	}, func() { once.Do(func() {}) }, nil
}

func twitterSnapshot() *twitterRuntime {
	if r := liveTwitter.Load(); r != nil {
		return r
	}
	return &twitterRuntime{mode: TwitterMode, fetchMode: TwitterAPIFetchMode, userAgent: UserAgent, baseURLs: BaseURL, jar: Cookie, api: twitterAPI}
}
