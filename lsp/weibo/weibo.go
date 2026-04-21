package weibo

import (
	"net/http/cookiejar"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	jsoniter "github.com/json-iterator/go"
	"go.uber.org/atomic"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

const (
	Site = "weibo"
)

var (
	visitorCookiesOpt atomic.Value
	visitorUA         atomic.String
	JAR               *cookiejar.Jar
)

func CookieOption() []requests.Option {
	if c := visitorCookiesOpt.Load(); c != nil {
		return c.([]requests.Option)
	}
	return nil
}

func GetVisitorUA() string {
	if ua := visitorUA.Load(); ua != "" {
		return ua
	}
	return requests.DefaultUA()
}
