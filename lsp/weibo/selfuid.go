package weibo

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"go.uber.org/atomic"
)

// PathNavConfig 桌面端导航配置接口。挑它是因为够轻，且登录态一定会带 x-log-uid。
const PathNavConfig = "https://weibo.com/ajax/getNavConfig"

// uidCaptureFunc 从响应头里抠出 x-log-uid。
type uidCaptureFunc func(uid int64)

func (f uidCaptureFunc) ModifyResponse(resp *http.Response) error {
	if v := resp.Header.Get("x-log-uid"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			f(n)
		}
	}
	return nil
}

// GetSelfUid 取当前登录账号的 uid。
//
// 微博的 ajax 接口在登录态下会带响应头 x-log-uid；访客态是 13 位访客 id，或者干脆没有这个头。
// 抓包实测（2026-11-02，151 条请求）：带登录 Cookie 请求 /ajax/getNavConfig 就能拿到，
// 不需要参数、也不需要 XSRF；无 Cookie 时返回 {"ok":-100} 且没有这个头。
//
// 拿到 uid 之后，昵称和头像可以直接复用 ApiContainerGetIndexProfile。
func GetSelfUid() (int64, error) {
	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.AddUAOption(),
		requests.HeaderOption("Referer", "https://weibo.com/"),
		requests.TimeoutOption(time.Second * 10),
	}

	cookieOpts := CookieOption()
	if len(cookieOpts) == 0 {
		// 内存里还没加载 cookie（比如 bot 尚未上线）时，用配置里的 SUB 兜一下
		if sub := GetSettingCookie(); sub != "" {
			cookieOpts = []requests.Option{requests.CookieOption("SUB", sub)}
		}
	}
	opts = append(opts, cookieOpts...)

	var uid atomic.Int64
	opts = append(opts, requests.WithResponseMiddleware(uidCaptureFunc(func(n int64) { uid.Store(n) })))

	var body struct{}
	if err := requests.Get(PathNavConfig, nil, &body, opts...); err != nil {
		return 0, err
	}
	if n := uid.Load(); n > 0 {
		return n, nil
	}
	return 0, errors.New("响应头里没有 x-log-uid（多半是未登录或 Cookie 已失效）")
}

// PathProfileInfo 微博桌面端的资料接口。
// 抓包实测（2026-11-02，entry 143）：登录态下
// GET https://weibo.com/ajax/profile/info?uid=<uid>&scene=profile
// 返回 200 json（1344 字节），referer 是 https://weibo.com/u/<uid>。
//
// 没走项目里已有的 m.weibo.cn container 接口：那个接口对「自己」这个 uid
// 返回的是 HTML（实测报 invalid character '<'），大概是缺 XSRF 或触发了风控。
const PathProfileInfo = "https://weibo.com/ajax/profile/info"

// WeiboProfile 自己账号的展示信息
type WeiboProfile struct {
	Uid             int64
	ScreenName      string
	ProfileImageUrl string
}

// GetSelfProfile 取自己账号的昵称和头像。
// 抓包没录到响应体，所以这里按字段名宽松解析；解析不到时把响应里实际有哪些键报出来，
// 方便一眼看出微博改了什么。
func GetSelfProfile(uid int64) (*WeiboProfile, error) {
	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.AddUAOption(),
		requests.HeaderOption("Referer", fmt.Sprintf("https://weibo.com/u/%d", uid)),
		requests.TimeoutOption(time.Second * 10),
	}
	cookieOpts := CookieOption()
	if len(cookieOpts) == 0 {
		if sub := GetSettingCookie(); sub != "" {
			cookieOpts = []requests.Option{requests.CookieOption("SUB", sub)}
		}
	}
	opts = append(opts, cookieOpts...)

	var body map[string]interface{}
	params := map[string]string{"uid": strconv.FormatInt(uid, 10), "scene": "profile"}
	if err := requests.Get(PathProfileInfo, params, &body, opts...); err != nil {
		return nil, err
	}
	if body == nil {
		return nil, errors.New("资料接口返回空")
	}
	if ok, isNum := body["ok"].(float64); isNum && ok == 0 {
		return nil, fmt.Errorf("资料接口返回 ok=0（多半是未登录或 Cookie 失效）")
	}

	data, _ := body["data"].(map[string]interface{})
	user, _ := data["user"].(map[string]interface{})
	if user == nil {
		return nil, fmt.Errorf("响应里没有 data.user（顶层键：%v）", sortedKeys(body))
	}

	p := &WeiboProfile{Uid: uid}
	p.ScreenName, _ = user["screen_name"].(string)
	p.ProfileImageUrl, _ = user["profile_image_url"].(string)
	if p.ProfileImageUrl == "" {
		p.ProfileImageUrl, _ = user["avatar_hd"].(string)
	}
	if p.ScreenName == "" && p.ProfileImageUrl == "" {
		return nil, fmt.Errorf("data.user 里没有昵称/头像（user 键：%v）", sortedKeys(user))
	}
	return p, nil
}

func sortedKeys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
