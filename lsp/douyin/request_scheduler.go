package douyin

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/requests"
)

// douyinRequestGate 是全局抖音请求调度闸门：
// 同一时刻最多只有一个日常抖音网络请求在途，且相邻请求间隔不小于 spacing。
// 日志中的 schedule_mode=strict_serial_shared_session 即由该闸门实现。
type douyinRequestGate struct {
	mu      chan struct{}
	next    time.Time
	spacing time.Duration
}

var douyinRequestGateInstance = &douyinRequestGate{mu: make(chan struct{}, 1)}

// scheduleMode 当前调度模式标记，供启动日志输出。
const scheduleMode = "strict_serial_shared_session"

// acquireWithWait 在闸门中取得一个请求时段。
// 返回 release 释放函数；调用方必须在请求结束后（含失败）调用 release。
// 返回值为本次排队等待时长。
func (g *douyinRequestGate) acquireWithWait(ctx context.Context, spacing time.Duration) (release func(), waited time.Duration, err error) {
	if spacing < 0 {
		spacing = 0
	}
	start := time.Now()
	select {
	case g.mu <- struct{}{}:
	case <-ctx.Done():
		return func() {}, 0, ctx.Err()
	}
	if wait := time.Until(g.next); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			<-g.mu
			return func() {}, time.Since(start), ctx.Err()
		}
	}
	g.next = time.Now().Add(spacing)
	waited = time.Since(start)
	return func() { <-g.mu }, waited, nil
}

// acquireDouyinRequestSlotWithWait 在全局闸门中取得请求时段，
// 并把排队等待情况写入 trace（queue_wait / through_gate 字段）。
func acquireDouyinRequestSlotWithWait(ctx context.Context, trace *douyinAPIRequestTrace) (func(), error) {
	release, waited, err := douyinRequestGateInstance.acquireWithWait(ctx, getDouyinRequestSpacing())
	if trace != nil {
		trace.queueWait = waited
		trace.throughGate = true
	}
	if err == nil && waited > time.Second {
		logger.WithField("request_type", douyinRequestTypeAllDouyinAPI).
			WithField("error_kind", douyinErrorKindQueueWait).
			WithField("queue_wait", waited.String()).
			Tracef("抖音请求排队等待")
	}
	return release, err
}

// douyinWebAPIRequestOptions 组装网页 API（aweme/webcast JSON 接口）的请求选项。
func douyinWebAPIRequestOptions(trace *douyinAPIRequestTrace) []requests.Option {
	opts := douyinBaseRequestOptions(trace)
	target := ""
	if trace != nil {
		target = trace.target
	}
	opts = append(opts,
		requests.HeaderOption("Accept", "application/json, text/plain, */*"),
		requests.HeaderOption("Sec-Fetch-Site", douyinSecFetchSite(target)),
		requests.HeaderOption("Sec-Fetch-Mode", "cors"),
		requests.HeaderOption("Sec-Fetch-Dest", "empty"),
		requests.HeaderOption("Referer", BaseHost+"/"),
		requests.HeaderOption("Sec-CH-UA", douyinSecCHUA(activeDouyinUserAgent())),
	)
	if cookieHeader := douyinCookieHeaderFor(target); cookieHeader != "" {
		opts = append(opts, requests.HeaderOption("Cookie", cookieHeader))
	}
	return opts
}

// douyinSecFetchSite 依据目标域名给出 Sec-Fetch-Site 取值。
func douyinSecFetchSite(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "empty"
	}
	host := strings.ToLower(u.Hostname())
	if isAllowedDouyinHost(host) {
		return "same-origin"
	}
	return "cross-site"
}

// douyinMinimalLiveRequestOptions 组装精简直播接口的请求选项（头最少化）。
func douyinMinimalLiveRequestOptions(trace *douyinAPIRequestTrace) []requests.Option {
	opts := douyinBaseRequestOptions(trace)
	target := ""
	if trace != nil {
		target = trace.target
	}
	opts = append(opts,
		requests.HeaderOption("Accept", "application/json, text/plain, */*"),
		requests.HeaderOption("Sec-Fetch-Site", douyinSecFetchSite(target)),
	)
	if cookieHeader := douyinCookieHeaderFor(target); cookieHeader != "" {
		opts = append(opts, requests.HeaderOption("Cookie", cookieHeader))
	}
	return opts
}

// douyinWebPageRequestOptions 组装网页 SSR（直播页/首页）请求选项。
func douyinWebPageRequestOptions(trace *douyinAPIRequestTrace) []requests.Option {
	opts := douyinBaseRequestOptions(trace)
	target := ""
	if trace != nil {
		target = trace.target
	}
	opts = append(opts,
		requests.HeaderOption("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8"),
		requests.HeaderOption("Sec-Fetch-Site", douyinSecFetchSite(target)),
		requests.HeaderOption("Sec-Fetch-Mode", "navigate"),
		requests.HeaderOption("Sec-Fetch-Dest", "document"),
		requests.HeaderOption("Upgrade-Insecure-Requests", "1"),
		requests.HeaderOption("Sec-CH-UA", douyinSecCHUA(activeDouyinUserAgent())),
	)
	if cookieHeader := douyinCookieHeaderFor(target); cookieHeader != "" {
		opts = append(opts, requests.HeaderOption("Cookie", cookieHeader))
	}
	return opts
}
