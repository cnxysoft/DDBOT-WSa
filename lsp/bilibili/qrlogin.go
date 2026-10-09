package bilibili

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/cnxysoft/DDBOT-WSa/requests"
)

// 纯 HTTP 扫码登录（Lite 重写）：
// qrcode/generate 申请二维码 → 每秒轮询 qrcode/poll → 登录成功后导航
// 返回的 cross-domain URL（oauth2/login）完成登录，全程共用同一 Cookie 容器；
// 回调请求关闭自动重定向，手动跟随最多 12 跳且仅允许B站 HTTPS 地址；
// 须同时取得 SESSDATA/bili_jct/DedeUserID 并实时验证通过才算登录成功。
const (
	PathQRLoginGenerateQR  = "/x/passport-login/web/qrcode/generate"
	PathQRLoginOAuth2Login = "/x/passport-login/web/qrcode/poll"

	// qrLoginRedirectMaxHops 是扫码回调重定向的跟随上限。
	qrLoginRedirectMaxHops = 12
	// qrLoginPollInterval 是扫码轮询间隔。
	qrLoginPollInterval = time.Second
)

// qrLoginSession 扫码登录会话：client、Cookie 容器与全程观察到的 Cookie。
type qrLoginSession struct {
	client          *http.Client
	jar             http.CookieJar
	observedCookies []*http.Cookie
}

// newQRLoginSession 创建扫码会话：共享 Cookie Jar，关闭自动重定向。
func newQRLoginSession() *qrLoginSession {
	jar, _ := cookiejar.New(nil)
	session := &qrLoginSession{
		jar: jar,
	}
	session.client = &http.Client{
		Jar:       jar,
		Timeout:   20 * time.Second,
		Transport: cloneDefaultHTTPTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// 关闭自动重定向，扫码回调重定向由 followQRLoginRedirects 手动跟随
			return http.ErrUseLastResponse
		},
	}
	return session
}

// getQRCode 申请登录二维码。
func (s *qrLoginSession) getQRCode() (*GetQRCodeResponse, error) {
	resp := new(GetQRCodeResponse)
	if err := s.getJSON(BPath(PathQRLoginGenerateQR), nil, resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 {
		return nil, errors.New(resp.Message)
	}
	if resp.Data == nil || resp.Data.Url == "" {
		return nil, errors.New("B站二维码申请响应不完整")
	}
	return resp, nil
}

// writeQRCode 生成工作目录下的 qrcode.png，并在终端打印简易二维码。
func writeQRCode(qrURL string) error {
	if err := qrcode.WriteFile(qrURL, qrcode.Low, 256, "qrcode.png"); err != nil {
		return err
	}
	logger.Info("若无法识别下方二维码，请打开qrcode.png扫描~")
	qrCode, err := qrcode.New(qrURL, 0)
	if err != nil {
		return err
	}
	fmt.Println(qrCode.ToSmallString(true))
	return nil
}

// pollQRLogin 轮询一次扫码状态。
func (s *qrLoginSession) pollQRLogin(qrcodeKey string) (*QRLoginResponse, error) {
	if qrcodeKey == "" {
		return nil, errors.New("查询的Token为空")
	}
	resp := new(QRLoginResponse)
	if err := s.getJSON(BPath(PathQRLoginOAuth2Login), url.Values{
		"qrcode_key": {qrcodeKey},
	}, resp); err != nil {
		return nil, err
	}
	if resp.Data == nil {
		return nil, errors.New("B站扫码轮询响应缺少data")
	}
	return resp, nil
}

// getJSON 会话内 JSON GET：携带 XHR 头与登录态无关的公共头。
func (s *qrLoginSession) getJSON(target string, params url.Values, out interface{}) error {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return fmt.Errorf("解析B站扫码回调URL失败: %w", err)
	}
	if params != nil {
		req.URL.RawQuery = params.Encode()
	}
	applyQRXHRHeaders(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("访问B站扫码接口失败: %w", err)
	}
	defer resp.Body.Close()
	s.observedCookies = append(s.observedCookies, resp.Cookies()...)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("B站扫码接口HTTP状态异常: %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("解析B站扫码接口响应失败: %w", err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解析B站扫码接口响应失败: %w", err)
	}
	return nil
}

// applyQRXHRHeaders 设置扫码接口 XHR 请求头（二进制原文）。
func applyQRXHRHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("User-Agent", bilibiliWebUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Sec-Ch-Ua", bilibiliWebSecChUa)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", "\"Windows\"")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Referer", "https://passport.bilibili.com/")
	req.Header.Set("Origin", "https://passport.bilibili.com")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
}

// applyQRNavigationHeaders 设置扫码回调导航请求头。
func applyQRNavigationHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("User-Agent", bilibiliWebUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Sec-Ch-Ua", bilibiliWebSecChUa)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-CH-UA-Platform", "\"Windows\"")
	req.Header.Set("Sec-Fetch-Site", "same-site")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Referer", "https://passport.bilibili.com/login")
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
}

// jarCookiesFromURL 取指定地址当前会话（CookieJar）中的 Cookie。
func jarCookiesFromURL(jar http.CookieJar, rawURL string) []*http.Cookie {
	parsed, err := url.Parse(rawURL)
	if err != nil || jar == nil {
		return nil
	}
	return jar.Cookies(parsed)
}

// BiliCookies 是扫码回调 URL 查询串中携带的B站登录凭据。
//
// 成品证据（recovered/types/types-closure-x64.tsv:9880）：
//
//	struct 80 bilibili.BiliCookies struct {
//	    SESSDATA string; BILI_JCT string; DedeUserID string; Buvid3 string; AC_TIME_VALUE string }
//
// 由 getCookiesFromURL 从回调 URL 的 query 中提取前 4 个（反汇编 @0xd47640：
// mapaccess1_faststr 读 "SESSDATA"/"bili_jct"/"DedeUserID"/"buvid3"），
// AC_TIME_VALUE 字段保留以对齐成品布局。
type BiliCookies struct {
	SESSDATA      string
	BILI_JCT      string
	DedeUserID    string
	Buvid3        string
	AC_TIME_VALUE string
}

// toHTTPCookies 把凭据快照展开为 Cookie 列表，空值不输出。
func (c *BiliCookies) toHTTPCookies() []*http.Cookie {
	if c == nil {
		return nil
	}
	pairs := []struct {
		name  string
		value string
	}{
		{"SESSDATA", c.SESSDATA},
		{"bili_jct", c.BILI_JCT},
		{"DedeUserID", c.DedeUserID},
		{"buvid3", c.Buvid3},
		{"ac_time_value", c.AC_TIME_VALUE},
	}
	result := make([]*http.Cookie, 0, len(pairs))
	for _, pair := range pairs {
		if pair.value == "" {
			continue
		}
		result = append(result, &http.Cookie{Name: pair.name, Value: pair.value})
	}
	return result
}

// getCookiesFromURL 解析B站扫码回调 URL 的查询串，提取登录凭据。
// 成品 getCookiesFromURL(qrlogin.go:223, @0xd47640, 672B) 反汇编：
//   - TrimSpace(url)，为空直接报错；
//   - url.Parse 失败 → fmt.Errorf("解析B站扫码回调URL失败: %w")（@0x10cbb2c,len 0x23）；
//   - (*url.URL).Query 后按 "SESSDATA"(8)/"bili_jct"(8)/"DedeUserID"(10)/"buvid3"(6)
//     四次 mapaccess1_faststr，取每条 query 的首值写入 BiliCookies（newobject @0x1002500）。
func getCookiesFromURL(rawURL string) (*BiliCookies, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("B站扫码回调URL为空")
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("解析B站扫码回调URL失败: %w", err)
	}
	query := parsed.Query()
	first := func(name string) string {
		values := query[name]
		if len(values) == 0 {
			return ""
		}
		return values[0]
	}
	return &BiliCookies{
		SESSDATA:   first("SESSDATA"),
		BILI_JCT:   first("bili_jct"),
		DedeUserID: first("DedeUserID"),
		Buvid3:     first("buvid3"),
	}, nil
}

// completeQRLogin 扫码确认后完成登录：
// 导航 cross-domain URL、手动跟随重定向、合并 Cookie 并校验鉴权字段。
func (s *qrLoginSession) completeQRLogin(crossURL string) ([]*http.Cookie, error) {
	// 成品 completeQRLogin(@0xd478e0, qrlogin.go:240) 反汇编顺序：
	// allBilibiliCookies(@0xd482c0) → mergeBiliCookies(@0xd486a0) →
	// validateBiliAuthCookies(@0xd48a60)；不足时 followQRLoginRedirects(@0xd47ac0) 后
	// 再次 allBilibiliCookies + mergeBiliCookies + validate；随后
	// validateBilibiliHTTPSURL(@0xd480a0) → getCookiesFromURL(@0xd47640) →
	// mergeMissingBiliCookies(@0xd488c0) 用回调 URL 查询串补齐凭据。
	cookies := s.allBilibiliCookies()
	if validateBiliAuthCookies(cookies) != nil {
		if err := s.followQRLoginRedirects(crossURL); err != nil {
			return nil, err
		}
		cookies = s.allBilibiliCookies()
		if err := validateBiliAuthCookies(cookies); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(crossURL) == "" {
		return cookies, nil
	}
	if _, err := validateBilibiliHTTPSURL(crossURL); err != nil {
		return nil, err
	}
	urlCookies, err := getCookiesFromURL(crossURL)
	if err != nil {
		return nil, err
	}
	cookies = mergeMissingBiliCookies(cookies, urlCookies.toHTTPCookies())
	if err := validateBiliAuthCookies(cookies); err != nil {
		return nil, err
	}
	return cookies, nil
}

// followQRLoginRedirects 手动跟随扫码回调重定向：
// 最多 12 跳，仅允许 HTTPS 的B站域名；中间响应的 Set-Cookie 全部并入会话。
func (s *qrLoginSession) followQRLoginRedirects(startURL string) error {
	current, err := validateBilibiliHTTPSURL(startURL)
	if err != nil {
		return err
	}
	for hop := 0; hop <= qrLoginRedirectMaxHops; hop++ {
		req, err := http.NewRequest(http.MethodGet, current.String(), nil)
		if err != nil {
			return fmt.Errorf("解析B站扫码回调URL失败: %w", err)
		}
		applyQRNavigationHeaders(req)
		resp, err := s.client.Do(req)
		if err != nil {
			return fmt.Errorf("访问B站扫码回调地址失败: %w", err)
		}
		s.observedCookies = append(s.observedCookies, resp.Cookies()...)
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if isRedirectStatus(resp.StatusCode) {
			location := strings.TrimSpace(resp.Header.Get("Location"))
			if location == "" {
				return errors.New("B站扫码回调重定向缺少Location")
			}
			next, err := url.Parse(location)
			if err != nil {
				return fmt.Errorf("解析B站扫码回调重定向失败: %w", err)
			}
			resolved := current.ResolveReference(next)
			if _, err := validateBilibiliHTTPSURL(resolved.String()); err != nil {
				return fmt.Errorf("B站扫码回调拒绝非B站域名: %s", resolved.Host)
			}
			current = resolved
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("B站扫码回调HTTP状态异常: %s", resp.Status)
		}
		return nil
	}
	return fmt.Errorf("B站扫码回调重定向超过%d跳", qrLoginRedirectMaxHops)
}

// validateBilibiliHTTPSURL 校验地址必须是 HTTPS 的B站域名。
func validateBilibiliHTTPSURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("解析B站扫码回调URL失败: %w", err)
	}
	if parsed.Scheme != "https" {
		return nil, errors.New("B站扫码回调仅允许HTTPS")
	}
	if !isBilibiliHost(parsed.Hostname()) {
		return nil, fmt.Errorf("B站扫码回调拒绝非B站域名: %s", parsed.Host)
	}
	return parsed, nil
}

// allBilibiliCookies 收集会话中全部B站域的 Cookie，SESSDATA 等核心字段优先。
func (s *qrLoginSession) allBilibiliCookies() []*http.Cookie {
	hosts := []string{
		"https://www.bilibili.com/",
		"https://passport.bilibili.com/",
		"https://api.bilibili.com/",
		"https://bilibili.com/",
	}
	var result []*http.Cookie
	for _, host := range hosts {
		result = mergeBiliCookies(result, jarCookiesFromURL(s.jar, host))
	}
	result = mergeMissingBiliCookies(result, s.observedCookies)
	return result
}

// isBilibiliHost 判断主机名是否属于B站域名白名单。
func isBilibiliHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	switch host {
	case "bilibili.com", "passport.bilibili.com", "www.bilibili.com", "api.bilibili.com", "live.bilibili.com", "space.bilibili.com":
		return true
	}
	return strings.HasSuffix(host, ".bilibili.com") || host == "bilibili.com"
}

// mergeBiliCookies 合并 Cookie 列表：dst 优先，src 中同名 Cookie 覆盖旧值。
func mergeBiliCookies(dst []*http.Cookie, src []*http.Cookie) []*http.Cookie {
	for _, cookie := range src {
		if cookie == nil {
			continue
		}
		replaced := false
		for index, existing := range dst {
			if existing != nil && existing.Name == cookie.Name {
				dst[index] = cookie
				replaced = true
				break
			}
		}
		if !replaced {
			dst = append(dst, cookie)
		}
	}
	return dst
}

// mergeMissingBiliCookies 仅把 dst 中缺少的 Cookie 补进去，不覆盖已有值。
func mergeMissingBiliCookies(dst []*http.Cookie, src []*http.Cookie) []*http.Cookie {
	for _, cookie := range src {
		if cookie == nil {
			continue
		}
		exists := false
		for _, existing := range dst {
			if existing != nil && existing.Name == cookie.Name {
				exists = true
				break
			}
		}
		if !exists {
			dst = append(dst, cookie)
		}
	}
	return dst
}

// validateBiliAuthCookies 校验鉴权三要素 SESSDATA/bili_jct/DedeUserID 是否齐全。
// 成品 validateBiliAuthCookies(qrlogin.go:412, @0xd48a60) 返回 error：
//   - 空切片 → errors.New("B站扫码响应中未取得Cookie")（@0xd48c06,len 0x22）；
//   - 缺少项 → fmt.Errorf("B站扫码响应缺少必要Cookie: %s", strings.Join(missing,", "))
//     （@0xd48b96,len 0x26；缺失名按次序拼接，成品缺失名常量 SESSDATA/bili_jct）。
func validateBiliAuthCookies(cookies []*http.Cookie) error {
	if len(cookies) == 0 {
		return errors.New("B站扫码响应中未取得Cookie")
	}
	var sessdata, biliJct, dedeUserID bool
	for _, cookie := range cookies {
		if cookie == nil {
			continue
		}
		switch cookie.Name {
		case "SESSDATA":
			sessdata = cookie.Value != ""
		case "bili_jct":
			biliJct = cookie.Value != ""
		case "DedeUserID":
			dedeUserID = cookie.Value != ""
		}
	}
	var missing []string
	if !sessdata {
		missing = append(missing, "SESSDATA")
	}
	if !biliJct {
		missing = append(missing, "bili_jct")
	}
	if !dedeUserID {
		missing = append(missing, "DedeUserID")
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("B站扫码响应缺少必要Cookie: %s", strings.Join(missing, ", "))
}

// isRedirectStatus 判断是否为重定向状态码。
func isRedirectStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusMultipleChoices, http.StatusMovedPermanently, http.StatusFound,
		http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	}
	return false
}

// newVerifyInfoWithCookies 构造登录凭据并挂载四个 Cookie。
// 成品证据：bilibili.go:176 newVerifyInfoWithCookies 收 4 个 string（8 个寄存器实参），
// 生成四个 requests.CookieOption 闭包（.CookieOption.HttpCookieOption.func1..func4），
// 对应 SESSDATA / bili_jct / DedeUserID / buvid3 四个 Cookie 名。
// 成品 VerifyInfo 无 ac_time_value 字段（types: 88B，5 字段），本函数只写 4 个 Cookie。
func newVerifyInfoWithCookies(sessdata string, biliJct string, dedeUserID string, buvid3 string) *VerifyInfo {
	return &VerifyInfo{
		SESSDATA:   sessdata,
		BiliJct:    biliJct,
		DedeUserID: dedeUserID,
		Buvid3:     buvid3,
		VerifyOpts: []requests.Option{
			requests.CookieOption("SESSDATA", sessdata),
			requests.CookieOption("bili_jct", biliJct),
			requests.CookieOption("DedeUserID", dedeUserID),
			requests.CookieOption("buvid3", buvid3),
		},
	}
}
