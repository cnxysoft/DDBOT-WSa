package weibo

import (
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	jsoniter "github.com/json-iterator/go"
	"go.uber.org/atomic"
	"sync"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

const (
	Site = "weibo"
	// CookieAlert 是内部使用的告警类型，用于 Cookie 失效时向群发送通知
	CookieAlert concern_type.Type = "cookie_alert"
)

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
