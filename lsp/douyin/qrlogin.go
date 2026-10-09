package douyin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/authprompt"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
)

// 抖音二维码轮询状态取值 [推断]：Passport 端点返回字符串状态，
// 具体取值表无法从字符串证据复原，按常见 Passport 行为赋值。
const (
	douyinQRStatusWaiting   = "1"
	douyinQRStatusScanned   = "2"
	douyinQRStatusConfirmed = "3"
	douyinQRStatusExpired   = "5"
	douyinQRStatusCanceled  = "7"
	douyinQRStatusCanceledB = "8"
)

// douyinQRResponse 是 Passport 二维码接口响应。
type douyinQRResponse struct {
	Message string `json:"message"`
	Data    struct {
		Token          string `json:"token"`
		QRCode         string `json:"qrcode"`
		QRCodeIndexURL string `json:"qrcode_index_url"`
		Status         string `json:"status"`
		RedirectURL    string `json:"redirect_url"`
		Description    string `json:"description"`
	} `json:"data"`
}

// 登录链路常量。
const (
	douyinQRCodePath        = "https://login.douyin.com/passport/web/get_qrcode/"
	douyinQRConnectPath     = "https://login.douyin.com/passport/web/check_qrconnect/"
	douyinQRChallengePath   = "https://login.douyin.com/passport/web/challenge"
	douyinMsTokenURL        = "https://mssdk.bytedance.com/web/r/token"
	douyinTicketGuardPath   = BaseHost + "/passport/ticket_guard/get_client_cert/"
	douyinQRCodeImageFile   = "douyin-qr.png"
	douyinLoginRedirectMax  = 12
	douyinQRPollingInterval = 2 * time.Second
	douyinPassportSDKVer    = "3.0.0"
)

// RunDouyinQRLogin 执行抖音扫码登录：
// msToken -> 访客初始化 -> 申请二维码 -> 保存 douyin-qr.png -> 轮询确认 -> 站内跳转 -> 保存 Cookie。
// 由管理员在私聊回复"生成 抖音"后经 authprompt 框架调用。
func RunDouyinQRLogin() error {
	// authprompt 触发后的二次校验（与 B站 manual_qr.go 同构）：
	// CookieCloud 模式不生成二维码；douyin.qrlogin 关闭时拒绝生成。
	// 文案取自成品字符串（recovered/strings/douyin-lite-strings.txt L110/L111）。
	if config.GlobalConfig != nil {
		if cfg.GetDouyinCookieMode() == authprompt.ModeCookieCloud {
			return errors.New("当前为 CookieCloud 模式，Bot不会生成二维码；请在浏览器重新登录并等待CookieCloud同步。")
		}
		if !isDouyinQRLoginEnabled() {
			return errors.New("当前配置不允许生成二维码，请更新Cookie或调整登录模式。")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), getDouyinQRLoginTimeout())
	defer cancel()

	jar, err := cookiejar.New(nil)
	if err != nil {
		return fmt.Errorf("初始化抖音 Cookie Jar 失败: %w", err)
	}
	stop := make(chan struct{})
	defer close(stop) // deferwrap1：通知后台监听退出
	go func() {       // func1：超时监听，提示管理员二维码已失效
		select {
		case <-ctx.Done():
			logger.Warnf("抖音扫码登录超时: %v", ctx.Err())
		case <-stop:
		}
	}()

	// msToken 与访客 Cookie（失败仅告警，不阻断扫码）。
	msToken := ""
	if token, msErr := requestDouyinMsToken(jar); msErr == nil {
		msToken = token
	} else {
		logger.WithError(msErr).Warnf("获取抖音 msToken 失败: %v", msErr)
	}
	if err := initializeDouyinPassport(jar); err != nil {
		logger.WithError(err).Warnf("初始化抖音访客 Cookie 失败: %v", err)
	}

	qrResp, err := createDouyinQRCode(jar, msToken)
	if err != nil {
		return fmt.Errorf("申请抖音登录二维码失败: %w", err)
	}
	pngData, err := douyinQRCodePNGData(qrResp)
	if err != nil {
		return fmt.Errorf("解析抖音二维码响应失败: %w", err)
	}
	if err := os.WriteFile(douyinQRCodeImageFile, pngData, douyinStateFilePerm); err != nil {
		return fmt.Errorf("保存抖音二维码失败: %w", err)
	}
	logger.Info("抖音登录二维码已生成，请使用抖音 App 扫码并确认")

	lastStatus := ""
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("抖音扫码登录超时: %w", ctx.Err())
		case <-time.After(douyinQRPollingInterval):
		}
		qrResp, err := checkDouyinQRCode(jar, qrResp.Data.Token, msToken)
		if err != nil {
			logger.WithError(err).Warn("抖音二维码状态查询失败，本轮继续")
			continue
		}
		status := qrResp.Data.Status
		if status != lastStatus {
			logger.WithField("status", status).Info("抖音二维码轮询状态变化")
			lastStatus = status
		}
		switch status {
		case douyinQRStatusExpired:
			return errors.New("抖音登录二维码已过期")
		case douyinQRStatusCanceled, douyinQRStatusCanceledB:
			return errors.New("抖音扫码登录已被取消")
		case douyinQRStatusConfirmed, douyinQRStatusScanned:
			if status == douyinQRStatusScanned {
				continue
			}
			if err := finishDouyinQRLogin(jar, qrResp); err != nil {
				return err
			}
			setCurrentDouyinCookieJar(jar)
			if err := saveCurrentDouyinCookieState(); err != nil {
				return fmt.Errorf("抖音扫码成功但保存 Cookie 失败: %w", err)
			}
			logger.Info("抖音扫码登录成功，完整 Cookie Jar 已保存")
			return nil
		}
	}
}

// finishDouyinQRLogin 扫码确认后的收尾：取回会话 Cookie、站内跳转、校验登录会话。
func finishDouyinQRLogin(jar http.CookieJar, qrResp *douyinQRResponse) error {
	redirectURL := strings.TrimSpace(qrResp.Data.RedirectURL)
	if redirectURL == "" {
		return errors.New("抖音已确认扫码，但未取得 sessionid/sessionid_ss；当前网络触发了 Passport 浏览器校验，请改用 douyin.cookie 完整 Cookie")
	}
	if err := douyinFollowRedirectChain(jar, redirectURL); err != nil {
		return fmt.Errorf("抖音登录确认后的站内跳转失败: %w", err)
	}
	if !hasDouyinLoginCookie(jar) {
		return errors.New("抖音已确认扫码，但未取得 sessionid/sessionid_ss；当前网络触发了 Passport 浏览器校验，请改用 douyin.cookie 完整 Cookie")
	}
	return nil
}

// douyinFollowRedirectChain 依次跟随站内跳转（上限 12 次），把会话 Cookie 收进 Jar。
func douyinFollowRedirectChain(jar http.CookieJar, startURL string) error {
	current := startURL
	for step := 0; step < douyinLoginRedirectMax; step++ {
		next, err := douyinLoginRequest(jar, http.MethodGet, current, nil)
		if err != nil {
			return err
		}
		if next == "" {
			return nil
		}
		current = next
	}
	return errors.New("抖音登录重定向超过12次")
}

// requestDouyinMsToken 请求 msToken（mssdk token 接口）。
func requestDouyinMsToken(jar http.CookieJar) (string, error) {
	payload := map[string]interface{}{
		douyinParamAid: 6383,
		"service":      "www.douyin.com",
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, douyinMsTokenURL, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", activeDouyinUserAgent())
	trace := beginDouyinAPIRequest(douyinRequestTypeMsToken, http.MethodPost, douyinMsTokenURL)
	client := &http.Client{Timeout: getDouyinRequestTimeout()}
	resp, err := client.Do(req)
	if err != nil {
		trace.finish(err)
		return "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		trace.finish(err)
		return "", err
	}
	trace.finish(nil)
	var msResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &msResp); err != nil {
		return "", fmt.Errorf("抖音 msToken 响应格式无效: %w", err)
	}
	if msResp.Data.Token == "" {
		return "", errors.New("抖音 msToken 响应格式无效")
	}
	setDouyinMsTokenValue(msResp.Data.Token)
	return msResp.Data.Token, nil
}

// initializeDouyinPassport 访客初始化：访问 passport challenge 页取得访客 Cookie。
func initializeDouyinPassport(jar http.CookieJar) error {
	trace := beginDouyinAPIRequest(douyinRequestTypeQRChallenge, http.MethodGet, douyinQRChallengePath)
	req, err := http.NewRequest(http.MethodGet, douyinQRChallengePath, nil)
	if err != nil {
		trace.finish(err)
		return fmt.Errorf("初始化抖音访客 Cookie 失败: %w", err)
	}
	setDouyinNavigationHeaders(req, BaseHost+"/", "", "")
	client := &http.Client{
		Timeout: getDouyinRequestTimeout(),
		Jar:     jar,
	}
	resp, err := client.Do(req)
	if err != nil {
		trace.finish(err)
		return fmt.Errorf("初始化抖音访客 Cookie 失败: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	trace.finish(nil)
	return nil
}

// createDouyinQRCode 申请登录二维码。
func createDouyinQRCode(jar http.CookieJar, msToken string) (*douyinQRResponse, error) {
	form := url.Values{}
	form.Set("service", "https://www.douyin.com")
	form.Set("need_logo", "false")
	form.Set("need_short_url", "true")
	form.Set("device_platform", "web_app")
	form.Set(douyinParamAid, douyinAidValue)
	form.Set("account_sdk_source", "sso")
	form.Set("sdk_version", douyinPassportSDKVer)
	form.Set(douyinParamMsToken, msToken)
	body := strings.NewReader(form.Encode())

	req, err := http.NewRequest(http.MethodPost, douyinQRCodePath, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setDouyinNavigationHeaders(req, BaseHost+"/", msToken, "")
	trace := beginDouyinAPIRequest(douyinRequestTypeQRCreate, http.MethodPost, douyinQRCodePath)
	client := &http.Client{Timeout: getDouyinRequestTimeout(), Jar: jar}
	resp, err := client.Do(req)
	if err != nil {
		trace.finish(err)
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		trace.finish(err)
		return nil, err
	}
	trace.finish(nil)
	qrResp := new(douyinQRResponse)
	if err := json.Unmarshal(data, qrResp); err != nil {
		return nil, fmt.Errorf("解析抖音二维码响应失败: %w", err)
	}
	// 成品 createDouyinQRCode @qrlogin.go:187 +0x963 调 firstDouyinNonEmpty(Token, QRCodeIndexURL, QRCode)。
	if firstDouyinNonEmpty(qrResp.Data.Token, qrResp.Data.QRCodeIndexURL, qrResp.Data.QRCode) == "" {
		logger.WithField("body", previewDouyinBody(data)).
			Errorf("申请抖音登录二维码失败: %s", qrResp.Message)
		return nil, fmt.Errorf("申请抖音登录二维码失败: %s（若触发浏览器校验，请使用 douyin.cookie）", qrResp.Message)
	}
	return qrResp, nil
}

// checkDouyinQRCode 轮询二维码扫码状态。
func checkDouyinQRCode(jar http.CookieJar, token string, msToken string) (*douyinQRResponse, error) {
	form := url.Values{}
	form.Set("token", token)
	form.Set("service", "https://www.douyin.com")
	form.Set("need_logo", "false")
	form.Set("need_short_url", "true")
	form.Set("device_platform", "web_app")
	form.Set(douyinParamAid, douyinAidValue)
	form.Set("account_sdk_source", "sso")
	form.Set(douyinParamMsToken, msToken)
	body := strings.NewReader(form.Encode())

	req, err := http.NewRequest(http.MethodPost, douyinQRConnectPath, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	setDouyinNavigationHeaders(req, douyinQRCodePath, msToken, "")
	trace := beginDouyinAPIRequest(douyinRequestTypeQRPoll, http.MethodPost, douyinQRConnectPath)
	client := &http.Client{Timeout: getDouyinRequestTimeout(), Jar: jar}
	resp, err := client.Do(req)
	if err != nil {
		trace.finish(err)
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		trace.finish(err)
		return nil, err
	}
	trace.finish(nil)
	qrResp := new(douyinQRResponse)
	if err := json.Unmarshal(data, qrResp); err != nil {
		return nil, fmt.Errorf("解析抖音二维码响应失败: %w", err)
	}
	return qrResp, nil
}

// douyinQRCodePNGData 从二维码响应中取 PNG 数据：
// qrcode 字段为图片地址时下载，为 base64 时直接解码。
func douyinQRCodePNGData(qrResp *douyinQRResponse) ([]byte, error) {
	qrcode := strings.TrimSpace(qrResp.Data.QRCode)
	if qrcode == "" {
		qrcode = strings.TrimSpace(qrResp.Data.QRCodeIndexURL)
	}
	if qrcode == "" {
		return nil, errors.New("抖音二维码响应中没有有效 PNG 数据")
	}
	if strings.HasPrefix(qrcode, "data:image") {
		if idx := strings.Index(qrcode, "base64,"); idx >= 0 {
			if data, err := base64.StdEncoding.DecodeString(qrcode[idx+len("base64,"):]); err == nil {
				return data, nil
			}
		}
		return nil, errors.New("抖音二维码响应中没有有效 PNG 数据")
	}
	if strings.HasPrefix(qrcode, "http") {
		client := &http.Client{Timeout: getDouyinRequestTimeout()}
		resp, err := client.Get(qrcode)
		if err != nil {
			return nil, fmt.Errorf("抖音二维码响应中没有有效 PNG 数据: %w", err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("抖音二维码响应中没有有效 PNG 数据: %w", err)
		}
		if len(data) == 0 || !bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) {
			return nil, errors.New("抖音二维码响应中没有有效 PNG 数据")
		}
		return data, nil
	}
	if data, err := base64.StdEncoding.DecodeString(qrcode); err == nil && bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) {
		return data, nil
	}
	return nil, errors.New("抖音二维码响应中没有有效 PNG 数据")
}

// douyinLoginRequest 登录链路的低层请求：
// 手动跟随跳转并把每一步的 Set-Cookie 收进 Jar，返回下一跳地址（无跳转返回空串）。
func douyinLoginRequest(jar http.CookieJar, method string, rawURL string, body io.Reader) (string, error) {
	trace := beginDouyinAPIRequest(douyinLoginRequestType(rawURL), method, rawURL)
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		trace.finish(err)
		return "", err
	}
	setDouyinNavigationHeaders(req, BaseHost+"/", "", "")
	client := &http.Client{
		Timeout: getDouyinRequestTimeout(),
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		trace.finish(err)
		return "", err
	}
	defer resp.Body.Close() // deferwrap1
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		trace.finish(err)
		return "", err
	}
	trace.finish(nil)
	if next := resp.Header.Get("Location"); next != "" {
		if ref, refErr := url.Parse(next); refErr == nil {
			if base, baseErr := url.Parse(rawURL); baseErr == nil {
				return base.ResolveReference(ref).String(), nil
			}
		}
		return next, nil
	}
	// 页面内 JS 跳转兜底（document.location.href = "..."）。
	if next := firstDouyinJSRedirect(string(data)); next != "" {
		return next, nil
	}
	logger.WithField("status_code", resp.StatusCode).
		WithField("body", previewDouyinBody(data)).
		Tracef("抖音登录请求返回")
	return "", nil
}

// firstDouyinJSRedirect 从登录页 HTML 中提取 JS 跳转地址。
func firstDouyinJSRedirect(body string) string {
	for _, pattern := range douyinJSRedirectPatterns {
		if m := pattern.FindStringSubmatch(body); len(m) >= 2 {
			return m[1]
		}
	}
	return ""
}

// setDouyinNavigationHeaders 设置登录/导航请求的通用头。
// referer 为空时不设置 Referer；webSign 为空时按需现算。
func setDouyinNavigationHeaders(req *http.Request, referer string, msToken string, webSign string) {
	req.Header.Set("User-Agent", activeDouyinUserAgent())
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Upgrade-Insecure-Requests", "1")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	if msToken != "" {
		req.Header.Set("x-ms-token", msToken)
	}
	if webSign == "" {
		webSign = douyinWebSignStrategy
	}
	req.Header.Set(douyinWebSignHeader, webSign)
	req.Header.Set("passport_jssdk_version", douyinPassportSDKVer)
}

// isDouyinDomain 判断 host 是否为 douyin.com 及其子域。
func isDouyinDomain(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "douyin.com" {
		return true
	}
	return strings.HasSuffix(host, ".douyin.com")
}

// hasDouyinLoginCookie 判断 Jar 中是否包含登录会话。
//
// 成品 @0xd99de0（288B，qrlogin.go:286）：入口把接口实参 (rax,rbx) 原样转交
// douyinCookiesForState（@+0x20 call 0xd6aa80），故形参是 http.CookieJar 而非
// Cookie 切片；随后对每条 Cookie 的 Name 调 strings.ToLower（@+0x63 call
// 0x53b860 = strings/strings.go:727）再与 sessionid / sessionid_ss / sid_guard
// 比较，命中且 Value 非空（cmpq $0x0,0x18(%rcx)）返回 true。
func hasDouyinLoginCookie(jar http.CookieJar) bool {
	for _, c := range douyinCookiesForState(jar) {
		switch strings.ToLower(c.Name) {
		case douyinCookieSessionID, douyinCookieSessionIDSS, douyinCookieSidGuard:
			if c.Value != "" {
				return true
			}
		}
	}
	return false
}

// douyinCookieNames 返回 Cookie 名称列表（日志用，不输出值）。
func douyinCookieNames(cookies []*http.Cookie) []string {
	names := make([]string, 0, len(cookies))
	for _, c := range cookies {
		if c == nil {
			continue
		}
		names = append(names, c.Name)
	}
	return names
}

// previewDouyinBody 返回响应体预览（日志安全，超长截断）。
func previewDouyinBody(body []byte) string {
	const previewLimit = 160
	text := strings.TrimSpace(string(body))
	if len(text) > previewLimit {
		return text[:previewLimit] + "..."
	}
	return text
}

// firstDouyinNonEmpty 返回第一个非空字符串。
func firstDouyinNonEmpty(items ...string) string {
	for _, item := range items {
		if item != "" {
			return item
		}
	}
	return ""
}

// douyinTicketGuardCert 登录成功后的 ticket_guard 证书请求（尽力而为）。
func douyinTicketGuardCert(jar http.CookieJar, uid int64) {
	target := fmt.Sprintf("%suid=%d:", douyinTicketGuardPath, uid)
	trace := beginDouyinAPIRequest(douyinRequestTypeQRTicketGuard, http.MethodGet, douyinTicketGuardPath)
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		trace.finish(err)
		return
	}
	setDouyinNavigationHeaders(req, BaseHost+"/", "", "")
	client := &http.Client{Timeout: getDouyinRequestTimeout(), Jar: jar}
	resp, err := client.Do(req)
	if err != nil {
		trace.finish(err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	trace.finish(nil)
}
