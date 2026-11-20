package weibo

import (
	"sync"
	stdatomic "sync/atomic"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	jsoniter "github.com/json-iterator/go"
	"go.uber.org/atomic"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

const (
	Site = "weibo"
	// CookieAlert 是内部使用的告警类型，用于 Cookie 失效时向群发送通知
	CookieAlert concern_type.Type = "cookie_alert"
)

var sessionGeneration stdatomic.Uint64
var cookieStateMu sync.Mutex

func storeCookieOptions(options []requests.Option, ua ...string) {
	cookieStateMu.Lock()
	defer cookieStateMu.Unlock()
	storeCookieOptionsUnlocked(options)
	if len(ua) > 0 {
		visitorUA.Store(ua[0])
	}
}

func storeCookieOptionsUnlocked(options []requests.Option) {
	sessionGeneration.Add(1)
	visitorCookiesOpt.Store(options)
}

var (
	visitorCookiesOpt atomic.Value
	visitorUA         atomic.String

	// cookieHealthy 标记当前 Cookie 是否健康可用
	cookieHealthy atomic.Bool
	// consecutiveCookieFails 记录连续 Cookie 刷新失败次数
	consecutiveCookieFails atomic.Int64
	// refreshMu 防止并发刷新 Cookie
	refreshMu sync.Mutex
)

func CookieOption() []requests.Option {
	if c := visitorCookiesOpt.Load(); c != nil {
		return c.([]requests.Option)
	}
	return nil
}

// IsCookieHealthy 返回当前 Cookie 是否健康
func IsCookieHealthy() bool {
	return cookieHealthy.Load()
}

func GetVisitorUA() string {
	if ua := visitorUA.Load(); ua != "" {
		return ua
	}
	return requests.DefaultUA()
}
