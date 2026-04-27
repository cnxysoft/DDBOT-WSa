package weibo

import (
	"fmt"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/guonaihong/gout"
	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

const (
	pathWeiboCfg               = "https://m.weibo.cn/api/config"
	pathWeiboPub               = "https://weibo.cn/pub"
	pathWeiboCN                = "https://m.weibo.cn/"
	pathWeiboDesktop           = "https://weibo.com"
	pathPassportGenvisitorTest = "https://visitor.passport.weibo.cn/visitor/genvisitor2"
	pathPassportGenvisitorProd = "https://passport.weibo.com/visitor/genvisitor2"
)

var (
	genvisitorRegex        = regexp.MustCompile(`\((.*)\)`)
	freshCookiePauseUntil atomic.Int64 // 暂停截止时间戳（Unix），0表示未暂停
)

// getSnapCastURL 获取 SnapCast 服务地址
func getSnapCastURL() string {
	if url := cfg.GetSnapCastURL(); url != "" {
		return url
	}
	return "https://sc.znin.net/render"
}

// SnapCastResult SnapCast JS 模式返回结果
type SnapCastResult struct {
	Status string `json:"status"`
	Data   any    `json:"data"`
}

// SnapCastRidInfo rid 和 UA 信息
type SnapCastRidInfo struct {
	Rid string
	UA  string
}

// getSnapCastRid 通过 SnapCast 浏览器渲染获取 rid 和 UA
func getSnapCastRid(ua string) (*SnapCastRidInfo, error) {
	payload := map[string]any{
		"site":       "weibo",
		"type":       "rid",
		"output":     "json",
		"user_agent": ua,
		"data":       map[string]any{},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload failed: %w", err)
	}

	resp, err := http.Post(getSnapCastURL(), "application/json", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("post to snapcast failed: %w", err)
	}
	defer resp.Body.Close()

	var result SnapCastResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode snapcast response failed: %w", err)
	}

	if result.Status != "ok" {
		return nil, fmt.Errorf("snapcast error: %v", result.Data)
	}

	data, ok := result.Data.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("invalid snapcast data type")
	}

	ridVal, ok := data["rid"].(string)
	if !ok || ridVal == "" {
		return nil, fmt.Errorf("rid not found in snapcast response")
	}

	uaVal, _ := data["ua"].(string)

	return &SnapCastRidInfo{Rid: ridVal, UA: uaVal}, nil
}

func genvisitor(path string, params gout.H, externalOpts ...requests.Option) (*GenVisitorResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	var opts = []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.AddUAOption(),
		requests.TimeoutOption(time.Second * 10),
	}
	opts = append(opts, externalOpts...)
	var result string
	err := requests.Get(path, params, &result, opts...)
	if err != nil {
		return nil, err
	}
	submatch := genvisitorRegex.FindStringSubmatch(result)
	if len(submatch) < 2 {
		logger.Errorf("genvisitorRegex submatch not found")
		return nil, fmt.Errorf("genvisitor response regex extract failed")
	}
	var resp = new(GenVisitorResponse)
	err = json.Unmarshal([]byte(submatch[1]), resp)
	if err != nil {
		logger.WithField("Content", submatch[1]).Errorf("genvisitor data unmarshal error %v", err)
		resp = nil
	}
	return resp, err
}

func genvisitorGuest(requestID, rid string, externalOpts ...requests.Option) (*GenVisitorResponse, error) {
	params := gout.H{
		"cb":         "visitor_gray_callback",
		"request_id": requestID,
		"ver":        "20250916",
		"rid":        rid,
		"tid":        "",
		"from":       "weibo",
		"webdriver":  "false",
		"return_url": "https://m.weibo.cn/",
	}
	return genvisitor(pathPassportGenvisitorTest, params, externalOpts...)
}

func genvisitorLogin(externalOpts ...requests.Option) (*GenVisitorResponse, error) {
	params := gout.H{
		"cb":   "visitor_gray_callback",
		"tid":  "",
		"from": "weibo",
	}
	return genvisitor(pathPassportGenvisitorProd, params, externalOpts...)
}

func refreshGuestPub(jar *cookiejar.Jar, ua string) error {
	return requests.Get(pathWeiboPub, nil, nil,
		requests.WithCookieJar(jar),
		requests.AddUAOption(ua),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second*10),
	)
}

func refreshGuestCN(jar *cookiejar.Jar, ua string) error {
	return requests.Get(pathWeiboCN, nil, nil,
		requests.WithCookieJar(jar),
		requests.AddUAOption(ua),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second*10),
	)
}

func GetRequestId(jar *cookiejar.Jar, ua string) (string, error) {
	// 获取 visitor.html 并提取 request_id
	var visitorHTML string
	err := requests.Get(pathWeiboCN, nil, &visitorHTML,
		requests.WithCookieJar(jar),
		requests.AddUAOption(ua),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second*10),
	)
	if err != nil {
		logger.Errorf("refreshGuestCN error %v", err)
		return "", err
	}

	// 从 HTML 中提取 request_id
	requestIDRe := regexp.MustCompile(`var request_id\s*=\s*"([^"]+)"`)
	matches := requestIDRe.FindStringSubmatch(visitorHTML)
	if len(matches) < 2 {
		logger.Errorf("request_id not found in visitor.html")
		return "", fmt.Errorf("request_id not found in visitor.html")
	}
	requestID := matches[1]
	logger.Infof("获取 request_id 成功: %s", requestID)
	return requestID, nil
}

func refreshLoginXsrfToken(jar *cookiejar.Jar, ua string) error {
	return requests.Get(pathWeiboDesktop, nil, nil,
		requests.WithCookieJar(jar),
		requests.AddUAOption(ua),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second*10),
	)
}

func FreshCookieGuest() ([]*http.Cookie, error) {
	JAR, _ = cookiejar.New(nil)

	ua := requests.RandomUA(requests.Chrome)
	visitorUA.Store(ua)

	// 通过 SnapCast 获取 rid 和 UA
	info, err := getSnapCastRid(ua)
	if err != nil {
		logger.Errorf("getSnapCastRid error %v", err)
		return nil, err
	} else if info.UA != ua {
		logger.Warnf("getSnapCastRid Warning: UserAgent invalid, return UA: %v", info.UA)
		ua = info.UA
		visitorUA.Store(ua)
	}
	uaPreview := info.UA
	if len(uaPreview) > 20 {
		uaPreview = uaPreview[:20]
	}
	logger.Infof("获取 rid 成功: %s, UA: %s", info.Rid, uaPreview)

	err = refreshGuestPub(JAR, ua)
	if err != nil {
		logger.Errorf("refreshGuestPub error %v", err)
		return nil, err
	}

	// 随机延迟 1-3s，避免固定间隔被检测
	time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)

	var requestID string
	requestID, err = GetRequestId(JAR, ua)
	if err != nil {
		logger.Errorf("GetRequestId error %v", err)
		return nil, err
	}

	// 随机延迟 1-3s
	time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)

	genVisitorResp, err := genvisitorGuest(requestID, info.Rid, requests.WithCookieJar(JAR), requests.AddUAOption(ua))
	if err != nil {
		logger.Errorf("genvisitor error %v", err)
		return nil, err
	}
	if genVisitorResp.GetRetcode() != 20000000 || !strings.Contains(genVisitorResp.GetMsg(), "succ") {
		logger.WithFields(logrus.Fields{
			"Msg":     genVisitorResp.GetMsg(),
			"Retcode": genVisitorResp.GetRetcode(),
		}).Errorf("incarnateResp error")
		return nil, fmt.Errorf("genvisitor response error %v - %v",
			genVisitorResp.GetRetcode(), genVisitorResp.GetMsg())
	}

	// 随机延迟 1-3s
	time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)

	err = refreshGuestCN(JAR, ua)
	if err != nil {
		logger.Errorf("refreshGuestCN error %v", err)
		return nil, err
	}

	cookieUrl, err := url.Parse(pathWeiboCN)
	if err != nil {
		panic(fmt.Sprintf("path %v url parse error", pathWeiboCN))
	}
	return JAR.Cookies(cookieUrl), nil
}

func FreshCookieLogin() ([]*http.Cookie, error) {
	jar, _ := cookiejar.New(nil)

	// 使用随机 UA 并存储
	ua := requests.RandomUA(requests.Chrome)
	visitorUA.Store(ua)

	genVisitorResp, err := genvisitorLogin(requests.WithCookieJar(jar), requests.AddUAOption(ua))
	if err != nil {
		logger.Errorf("genvisitor error %v", err)
		return nil, err
	}
	if genVisitorResp.GetRetcode() != 20000000 || !strings.Contains(genVisitorResp.GetMsg(), "succ") {
		logger.WithFields(logrus.Fields{
			"Msg":     genVisitorResp.GetMsg(),
			"Retcode": genVisitorResp.GetRetcode(),
		}).Errorf("incarnateResp error")
		return nil, fmt.Errorf("genvisitor response error %v - %v",
			genVisitorResp.GetRetcode(), genVisitorResp.GetMsg())
	}

	// 随机延迟 1-3s，避免固定间隔被检测
	time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)

	err = refreshLoginXsrfToken(jar, ua)
	if err != nil {
		logger.Errorf("refreshLoginXsrfToken error %v", err)
		return nil, err
	}

	baseUrl, err := url.Parse(pathWeiboDesktop)
	if err != nil {
		panic(fmt.Sprintf("path %v url parse error", pathWeiboDesktop))
	}
	cookieUrl, err := url.Parse(pathPassportGenvisitorProd)
	if err != nil {
		panic(fmt.Sprintf("path %v url parse error", pathPassportGenvisitorProd))
	}
	cookies := jar.Cookies(cookieUrl)
	for _, cookie := range jar.Cookies(baseUrl) {
		if cookie.Name == "XSRF-TOKEN" || cookie.Name == "WBPSESS" {
			cookies = append(cookies, cookie)
		}
	}
	return cookies, nil
}

func FreshCookie() ([]*http.Cookie, error) {
	if isGuestMode() {
		return FreshCookieGuest()
	}
	return FreshCookieLogin()
}

// TryRefreshGuestCookie 尝试刷新 Guest Cookie
// 刷新后丢弃旧的 Cookie，用新的替代
// 如果在暂停期内（-100 频率限制），则跳过刷新
func TryRefreshGuestCookie() bool {
	// 检查是否在暂停期内（-100 频率限制触发）
	now := time.Now().Unix()
	if freshCookiePauseUntil.Load() > now {
		logger.Warnf("刷新 Guest Cookie 已暂停（频率限制），等待恢复...")
		return false
	}

	logger.Info("检测到 -100/432 错误，开始刷新 Guest Cookie")
	cookies, err := FreshCookieGuest()
	if err != nil {
		logger.Errorf("刷新 Guest Cookie 失败: %v", err)
		return false
	}

	// 将新的 Cookie 全部转换为 Option 并存储
	opt := []requests.Option{}
	for _, cookie := range cookies {
		opt = append(opt, requests.CookieOption(cookie.Name, cookie.Value))
	}
	visitorCookiesOpt.Store(opt)

	logger.Infof("Guest Cookie 刷新成功，已更新到内存")
	return true
}

// PauseRefreshOnRateLimit 当触发 -100（频率限制）时暂停刷新
// pauseDuration 暂停时长，默认 10 分钟
func PauseRefreshOnRateLimit(pauseDuration time.Duration) {
	if pauseDuration == 0 {
		pauseDuration = time.Minute * 10
	}
	pauseUntil := time.Now().Add(pauseDuration).Unix()
	freshCookiePauseUntil.Store(pauseUntil)
	logger.Infof("刷新 Guest Cookie 已暂停，将在 %v 后恢复", pauseDuration)
}
