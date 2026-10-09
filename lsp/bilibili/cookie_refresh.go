package bilibili

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"go.uber.org/atomic"
)

// B站官方 Cookie 刷新（ac_time_value）：
// cookie/info 取刷新时间戳 → correspond/1 页面取 refresh_csrf →
// cookie/refresh 换新 Cookie → confirm/refresh 确认作废旧 refresh_token。
// 新 refresh_token 持久化为 bilibili.ac_time_value。
const (
	PathBilibiliCookieInfo         = "https://passport.bilibili.com/x/passport-login/web/cookie/info"
	PathBilibiliCookieRefresh      = "https://passport.bilibili.com/x/passport-login/web/cookie/refresh"
	PathBilibiliCookieConfirm      = "https://passport.bilibili.com/x/passport-login/web/confirm/refresh"
	PathBilibiliCorrespondPrefix   = "https://www.bilibili.com/correspond/1/"
	bilibiliCookieRefreshSourceTag = "main_web"
)

// bilibiliCookieInfoResponse 是 cookie/info 响应。
type bilibiliCookieInfoResponse struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Refresh   bool  `json:"refresh"`
		Timestamp int64 `json:"timestamp"`
	} `json:"data"`
}

// bilibiliCookieRefreshResponse 是 cookie/refresh 响应。
type bilibiliCookieRefreshResponse struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Status       int32  `json:"status"`
		Message      string `json:"message"`
		RefreshToken string `json:"refresh_token"`
	} `json:"data"`
}

// bilibiliCookieConfirmResponse 是 confirm/refresh 响应。
type bilibiliCookieConfirmResponse struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

// bilibiliCookieRefreshDeps 抽象官方刷新的外部依赖，便于测试。
type bilibiliCookieRefreshDeps struct {
	FetchTimestamp func() (*bilibiliCookieInfoResponse, error)
	FetchCSRF      func(timestamp int64) (string, error)
	PostRefresh    func(refreshCSRF string, refreshToken string) (*bilibiliCookieRefreshResponse, []*http.Cookie, error)
	PostConfirm    func(refreshToken string) (*bilibiliCookieConfirmResponse, error)
	Validate       func() error
	Persist        func(refresh *bilibiliCookieRefreshResponse, cookies []*http.Cookie, newRefreshToken string) error
}

var (
	// refreshBilibiliCookieMu 防止官方刷新重入。
	refreshBilibiliCookieMu atomic.Bool
)

// canRetainBilibiliCookieForOfficialRefresh 判断当前凭据是否具备官方刷新条件：
// 需要配置 refresh_token（ac_time_value）。
func canRetainBilibiliCookieForOfficialRefresh() bool {
	if config.GlobalConfig == nil {
		return false
	}
	return strings.TrimSpace(config.GlobalConfig.GetString("bilibili.ac_time_value")) != ""
}

// tryRefreshBilibiliCookieAfterAuthFailure 在鉴权失败后尝试官方刷新：
// 具备 ac_time_value 才进行，其余情况交给上层告警流程。
func tryRefreshBilibiliCookieAfterAuthFailure(reason string) error {
	if !refreshBilibiliCookieMu.CompareAndSwap(false, true) {
		// 成品 tryRefreshBilibiliCookieAfterAuthFailure @0xd11624：仍在重试冷却时
		// logrus.(*Entry).Log(level=5=Debug, "B站 Cookie 自动刷新仍在重试冷却时间内")。
		logger.Debug("B站 Cookie 自动刷新仍在重试冷却时间内")
		return nil
	}
	defer refreshBilibiliCookieMu.Store(false)
	if !canRetainBilibiliCookieForOfficialRefresh() {
		return errors.New("未配置 ac_time_value，跳过官方刷新")
	}
	logger.WithField("reason", reason).
		Info("检测到 B站 Cookie 失效，开始使用 ac_time_value 自动刷新")
	deps := defaultBilibiliCookieRefreshDependencies()
	if err := refreshBilibiliCookieWithDependencies(deps); err != nil {
		logger.Warn("B站 Cookie 自动刷新失败，保留原 Cookie")
		return err
	}
	return nil
}

// defaultBilibiliCookieRefreshDependencies 返回真实接口实现的依赖集合。
func defaultBilibiliCookieRefreshDependencies() *bilibiliCookieRefreshDeps {
	return &bilibiliCookieRefreshDeps{
		FetchTimestamp: getBilibiliCookieRefreshTimestamp,
		FetchCSRF:      getBilibiliRefreshCSRF,
		PostRefresh:    postBilibiliCookieRefresh,
		PostConfirm:    confirmBilibiliCookieRefresh,
		Validate:       validateRefreshedBilibiliCookie,
		Persist:        persistRefreshedBilibiliCookie,
	}
}

// refreshBilibiliCookieWithDependencies 执行完整的官方刷新流程：
// 时间戳 → correspond → refresh → confirm → 校验 → 保存。
// 任何一步失败都直接返回错误，不推进旧 refresh_token。
func refreshBilibiliCookieWithDependencies(deps *bilibiliCookieRefreshDeps) error {
	info, err := deps.FetchTimestamp()
	if err != nil {
		return fmt.Errorf("请求 B站 cookie/info 失败: %w", err)
	}
	if info.Code != 0 {
		return fmt.Errorf("B站 cookie/info 返回失败: code=%d message=%s", info.Code, info.Message)
	}
	timestamp := info.Data.Timestamp
	if timestamp <= 0 {
		logger.Warn("读取 B站 Cookie 刷新时间戳失败，改用本机时间继续尝试")
		timestamp = time.Now().UnixMilli()
	}
	refreshCSRF, err := deps.FetchCSRF(timestamp)
	if err != nil {
		return err
	}
	oldRefreshToken := strings.TrimSpace(config.GlobalConfig.GetString("bilibili.ac_time_value"))
	if oldRefreshToken == "" {
		return errors.New("缺少 bilibili.ac_time_value")
	}
	refreshResp, refreshCookies, err := deps.PostRefresh(refreshCSRF, oldRefreshToken)
	if err != nil {
		return err
	}
	if refreshResp.Code != 0 {
		return fmt.Errorf("B站 Cookie refresh 接口失败: code=%d message=%s", refreshResp.Code, refreshResp.Message)
	}
	if refreshResp.Data.Status != 0 {
		return fmt.Errorf("B站 Cookie refresh 状态失败: status=%d message=%s", refreshResp.Data.Status, refreshResp.Data.Message)
	}
	newRefreshToken := strings.TrimSpace(refreshResp.Data.RefreshToken)
	if newRefreshToken == "" {
		return errors.New("B站 Cookie refresh 响应缺少新的 refresh_token")
	}
	sessdata, biliJct := refreshCookiesValue(refreshCookies)
	if sessdata == "" || biliJct == "" {
		return fmt.Errorf("B站 Cookie refresh 未返回完整 Cookie: %w", errors.New("缺少 SESSDATA 或 bili_jct"))
	}
	if _, err := deps.PostConfirm(newRefreshToken); err != nil {
		return err
	}
	if err := deps.Validate(); err != nil {
		return err
	}
	return deps.Persist(refreshResp, refreshCookies, newRefreshToken)
}

// refreshCookiesValue 从刷新响应 Cookie 中提取 SESSDATA 与 bili_jct。
func refreshCookiesValue(cookies []*http.Cookie) (string, string) {
	sessdata := ""
	biliJct := ""
	for _, cookie := range cookies {
		switch cookie.Name {
		case "SESSDATA":
			sessdata = cookie.Value
		case "bili_jct":
			biliJct = cookie.Value
		}
	}
	return sessdata, biliJct
}

// refreshCookiesIdentity 从刷新响应 Cookie 中提取 DedeUserID 与 buvid3。
func refreshCookiesIdentity(cookies []*http.Cookie) (string, string) {
	dedeUserID := ""
	buvid3 := ""
	for _, cookie := range cookies {
		switch cookie.Name {
		case "DedeUserID":
			dedeUserID = cookie.Value
		case "buvid3":
			buvid3 = cookie.Value
		}
	}
	return dedeUserID, buvid3
}

// seedBilibiliRefreshCookieJar 用当前登录 Cookie 播种一个独立 Jar，
// 官方刷新流程在该 Jar 中完成，避免污染全局 Cookie。
// 同一份凭据需要分别播到 passport 与 www 两个域下。
func seedBilibiliRefreshCookieJar() *cookiejar.Jar {
	jar, _ := cookiejar.New(nil)
	verify := getVerify()
	if verify == nil {
		return jar
	}
	for _, host := range []string{"https://passport.bilibili.com/", "https://www.bilibili.com/"} {
		u, err := url.Parse(host)
		if err != nil {
			continue
		}
		var cookies []*http.Cookie
		cookies = append(cookies, &http.Cookie{Name: "SESSDATA", Value: verify.SESSDATA})
		cookies = append(cookies, &http.Cookie{Name: "bili_jct", Value: verify.BiliJct})
		if verify.DedeUserID != "" {
			cookies = append(cookies, &http.Cookie{Name: "DedeUserID", Value: verify.DedeUserID})
		}
		if verify.Buvid3 != "" {
			cookies = append(cookies, &http.Cookie{Name: "buvid3", Value: verify.Buvid3})
		}
		jar.SetCookies(u, cookies)
	}
	return jar
}

// getBilibiliCookieRefreshTimestamp 请求 cookie/info 获取刷新时间戳。
func getBilibiliCookieRefreshTimestamp() (*bilibiliCookieInfoResponse, error) {
	var resp bilibiliCookieInfoResponse
	err := requests.Get(PathBilibiliCookieInfo, nil, &resp, bilibiliPassportRequestOptions()...)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// getBilibiliRefreshCSRF 生成 correspond 刷新路径并获取 refresh_csrf。
func getBilibiliRefreshCSRF(timestamp int64) (string, error) {
	path, err := encryptBilibiliCorrespondPath(timestamp)
	if err != nil {
		return "", fmt.Errorf("生成 B站 correspond 刷新路径失败: %w", err)
	}
	target := PathBilibiliCorrespondPrefix + path
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		// 成品 @0xd12b59 "创建 B站 correspond 请求失败: %w"（len 0x27）。
		return "", fmt.Errorf("创建 B站 correspond 请求失败: %w", err)
	}
	setBilibiliRefreshHeaders(req)
	client := &http.Client{Jar: seedBilibiliRefreshCookieJar(), Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("请求 B站 correspond 页面失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("B站 correspond 页面返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		// 成品 @0xd12e4d "读取 B站 correspond 页面失败: %w"（len 0x27）。
		return "", fmt.Errorf("读取 B站 correspond 页面失败: %w", err)
	}
	// TODO(lite): correspond 页面返回体中 refresh_csrf 的确切承载格式（HTML 内嵌
	// 或 JSON 字段）未能从证据复原，此处按 JSON 字段尝试解析。
	var payload struct {
		RefreshCSRF string `json:"refresh_csrf"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", errors.New("B站 correspond 页面中未找到 refresh_csrf")
	}
	if payload.RefreshCSRF == "" {
		return "", errors.New("B站 correspond 页面中的 refresh_csrf 为空")
	}
	return payload.RefreshCSRF, nil
}

// postBilibiliCookieRefresh 提交 cookie/refresh 换取新 Cookie。
func postBilibiliCookieRefresh(refreshCSRF string, refreshToken string) (*bilibiliCookieRefreshResponse, []*http.Cookie, error) {
	now := strconv.FormatInt(time.Now().UnixMilli(), 10)
	form := url.Values{
		"csrf":          {GetVerifyBiliJct()},
		"refresh_csrf":  {refreshCSRF},
		"refresh_token": {refreshToken},
		"source":        {bilibiliCookieRefreshSourceTag},
		"starttime":     {now},
		"endtime":       {now},
	}
	value, cookies, err := doBilibiliRefreshRequest(PathBilibiliCookieRefresh, form, new(bilibiliCookieRefreshResponse))
	if err != nil {
		return nil, nil, fmt.Errorf("请求 B站 Cookie refresh 接口失败: %w", err)
	}
	resp := value.(*bilibiliCookieRefreshResponse)
	if resp.Code != 0 {
		return nil, nil, fmt.Errorf("B站 Cookie refresh 接口失败: code=%d message=%s", resp.Code, resp.Message)
	}
	if resp.Data.Status != 0 {
		return nil, nil, fmt.Errorf("B站 Cookie refresh 状态失败: status=%d message=%s", resp.Data.Status, resp.Data.Message)
	}
	return resp, cookies, nil
}

// confirmBilibiliCookieRefresh 提交 confirm/refresh 作废旧 refresh_token。
func confirmBilibiliCookieRefresh(refreshToken string) (*bilibiliCookieConfirmResponse, error) {
	form := url.Values{
		"csrf":          {GetVerifyBiliJct()},
		"refresh_token": {refreshToken},
	}
	value, _, err := doBilibiliRefreshRequest(PathBilibiliCookieConfirm, form, new(bilibiliCookieConfirmResponse))
	if err != nil {
		return nil, fmt.Errorf("请求 B站 confirm/refresh 接口失败: %w", err)
	}
	resp := value.(*bilibiliCookieConfirmResponse)
	if resp.Code != 0 {
		return nil, fmt.Errorf("B站 confirm/refresh 接口失败: code=%d message=%s", resp.Code, resp.Message)
	}
	return resp, nil
}

// doBilibiliRefreshRequest 执行 passport 域的表单 POST，返回解析结果与响应 Cookie。
func doBilibiliRefreshRequest(target string, form url.Values, out interface{}) (interface{}, []*http.Cookie, error) {
	req, err := http.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nil, err
	}
	setBilibiliRefreshHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Jar: seedBilibiliRefreshCookieJar(), Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal(body, out); err != nil {
		// [推断] 二进制未观测到该分支文案，按站点通用解析失败描述
		return nil, nil, fmt.Errorf("解析B站接口响应失败: %w", err)
	}
	return out, resp.Cookies(), nil
}

// setBilibiliRefreshHeaders 设置 passport 刷新请求的公共头。
func setBilibiliRefreshHeaders(req *http.Request) {
	if req == nil {
		return
	}
	req.Header.Set("User-Agent", bilibiliWebUserAgent)
	req.Header.Set("Referer", "https://www.bilibili.com/")
	req.Header.Set("Origin", "https://www.bilibili.com")
}

// encryptBilibiliCorrespondPath 生成 correspond/1/ 的加密路径。
// TODO(lite): 官方使用站点内置 RSA 公钥对毫秒时间戳加密后拼路径，二进制中
// 仅见 "correspond RSA" 与 EncryptPKCS1v15 调用名，内置公钥未能复原。
// 这里改用 /x/passport-login/web/key 返回的公钥做 PKCS1v15 加密占位，
// 刷新失败会保留原 Cookie 并按上层退避处理，不影响主流程。
func encryptBilibiliCorrespondPath(timestamp int64) (string, error) {
	keyResp, err := GetKey()
	if err != nil {
		return "", err
	}
	keyPEM := strings.TrimSpace(keyResp.GetData().GetKey())
	if keyPEM == "" {
		return "", errors.New("获取 correspond RSA 公钥失败")
	}
	block, _ := pem.Decode([]byte(keyPEM))
	if block == nil {
		return "", errors.New("解析 B站 correspond RSA 公钥失败")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("解析 B站 correspond RSA 公钥失败: %w", err)
	}
	pubKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return "", errors.New("correspond RSA 公钥类型无效")
	}
	encrypted, err := rsa.EncryptPKCS1v15(rand.Reader, pubKey, []byte(strconv.FormatInt(timestamp, 10)))
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encrypted), nil
}

// persistRefreshedBilibiliCookie 保存刷新结果：更新内存凭据并写回配置。
func persistRefreshedBilibiliCookie(refresh *bilibiliCookieRefreshResponse, cookies []*http.Cookie, newRefreshToken string) error {
	if refresh == nil {
		return errors.New("验证刷新后的 B站 Cookie 返回空响应")
	}
	if strings.TrimSpace(newRefreshToken) == "" {
		return errors.New("新的 B站 refresh_token 为空")
	}
	sessdata, biliJct := refreshCookiesValue(cookies)
	if sessdata == "" || biliJct == "" {
		return fmt.Errorf("B站 Cookie refresh 未返回完整 Cookie: %w", errors.New("缺少 SESSDATA 或 bili_jct"))
	}
	// 成品 persistRefreshedBilibiliCookie 的 WriteConfigKeyValues 键集合含
	// bilibili.DedeUserID(@0x10b37a8) 与 bilibili.buvid3，刷新响应未带时回退当前 VerifyInfo。
	dedeUserID, buvid3 := refreshCookiesIdentity(cookies)
	if current := getVerify(); current != nil {
		if dedeUserID == "" {
			dedeUserID = current.DedeUserID
		}
		if buvid3 == "" {
			buvid3 = current.Buvid3
		}
	}
	if err := writeBilibiliCredentialConfig(sessdata, biliJct, dedeUserID, buvid3, newRefreshToken); err != nil {
		return fmt.Errorf("保存刷新后的 B站 Cookie 失败: %w", err)
	}
	atomicVerifyInfo.Store(newVerifyInfoWithCookies(sessdata, biliJct, dedeUserID, buvid3))
	FreshSelfInfo()
	logger.Info("B站 Cookie 已通过 ac_time_value 自动刷新并保存")
	return nil
}

// validateRefreshedBilibiliCookie 刷新后实时校验新 Cookie 的登录态。
func validateRefreshedBilibiliCookie() error {
	navResp, err := XWebInterfaceNav(true)
	if err != nil {
		return fmt.Errorf("验证刷新后的 B站 Cookie 失败: %w", err)
	}
	if navResp == nil {
		return errors.New("验证刷新后的 B站 Cookie 返回空响应")
	}
	if !navResp.GetData().GetIsLogin() {
		return fmt.Errorf("刷新后的 B站 Cookie 仍未登录: code=%d message=%s", navResp.GetCode(), navResp.GetMessage())
	}
	return nil
}

// bilibiliPassportRequestOptions 组装 passport 域请求的公共选项。
func bilibiliPassportRequestOptions() []requests.Option {
	return []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(15 * time.Second),
		requests.RetryOption(3),
		AddUAOption(),
		AddReferOption(),
	}
}

// writeBilibiliCredentialConfig 把扫码/刷新结果写回配置文件。
// 键集合按成品 persistRefreshedBilibiliCookie(@0xd145c0) 与 runBilibiliQRLoginOnce(@0xd38640)
// 的 cfg.WriteConfigKeyValues 实证：bilibili.SESSDATA / bilibili.bili_jct /
// bilibili.DedeUserID(@0x10b37a8,len 19) / bilibili.buvid3 / bilibili.ac_time_value。
func writeBilibiliCredentialConfig(sessdata string, biliJct string, dedeUserID string, buvid3 string, acTimeValue string) error {
	kv := make(map[string]string, 5)
	if sessdata != "" {
		kv["bilibili.SESSDATA"] = sessdata
	}
	if biliJct != "" {
		kv["bilibili.bili_jct"] = biliJct
	}
	if dedeUserID != "" {
		kv["bilibili.DedeUserID"] = dedeUserID
	}
	if buvid3 != "" {
		kv["bilibili.buvid3"] = buvid3
	}
	if acTimeValue != "" {
		kv["bilibili.ac_time_value"] = acTimeValue
	}
	if len(kv) == 0 {
		return nil
	}
	return cfg.WriteConfigKeyValues(kv)
}
