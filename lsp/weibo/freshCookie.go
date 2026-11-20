package weibo

import (
	crand "crypto/rand"
	"encoding/hex"
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
	pathWeiboPub               = "https://weibo.cn/pub"
	pathWeiboCN                = "https://m.weibo.cn/"
	pathWeiboDesktop           = "https://weibo.com"
	pathPassportGenvisitorTest = "https://visitor.passport.weibo.cn/visitor/genvisitor2"
	pathPassportGenvisitorProd = "https://passport.weibo.com/visitor/genvisitor2"
	pathPassportGenvisitor     = "https://passport.weibo.com/visitor/genvisitor"
	pathPassportVisitor        = "https://passport.weibo.com/visitor/visitor"
	pathLoginVisitor           = "https://login.sina.com.cn/visitor/visitor"
)

var (
	genvisitorRegex       = regexp.MustCompile(`\((.*)\)`)
	freshCookiePauseUntil atomic.Int64 // 暂停截止时间戳（Unix），0表示未暂停
)

// RidInfo 访客模式要的 rid 和配套 UA
type RidInfo struct {
	Rid string
	UA  string
}

// getRid 生成访客模式需要的 rid 和 UA。
//
// 本地随机生成就够：实测微博的 genvisitor2 接受任意字符串当 rid
// （16 位 hex / 32 位 hex / 时间戳 / uuid 都返回 retcode 20000000 "succ"，
// 并正常下发 sub / subp 访客凭据）。
//
// 以前这里会去调一个外部浏览器渲染服务（sc.znin.net）拿「真」rid ——
// 那个服务已经彻底不可用，而且它给回来的 rid 跟本地随机的没有任何区别。
func getRid(ua string) (*RidInfo, error) {
	return &RidInfo{Rid: randomRid(), UA: ua}, nil
}

// randomRid 生成 32 位 hex 的 rid。
func randomRid() string {
	b := make([]byte, 16)
	if _, err := crand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
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
	jar, _ := cookiejar.New(nil)

	ua := requests.RandomUA(requests.Chrome)
	visitorUA.Store(ua)

	// 取 rid 和 UA（本地随机生成）
	info, err := getRid(ua)
	if err != nil {
		logger.Errorf("getRid error %v", err)
		return nil, err
	}
	ua = info.UA
	uaPreview := info.UA
	if len(uaPreview) > 20 {
		uaPreview = uaPreview[:20]
	}
	logger.Infof("获取 rid 成功: %s, UA: %s", info.Rid, uaPreview)

	err = refreshGuestPub(jar, ua)
	if err != nil {
		logger.Errorf("refreshGuestPub error %v", err)
		return nil, err
	}

	// 随机延迟 1-3s，避免固定间隔被检测
	time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)

	var requestID string
	requestID, err = GetRequestId(jar, ua)
	if err != nil {
		logger.Errorf("GetRequestId error %v", err)
		return nil, err
	}

	// 随机延迟 1-3s
	time.Sleep(time.Duration(1000+rand.Intn(2000)) * time.Millisecond)

	genVisitorResp, err := genvisitorGuest(requestID, info.Rid, requests.WithCookieJar(jar), requests.AddUAOption(ua))
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

	err = refreshGuestCN(jar, ua)
	if err != nil {
		logger.Errorf("refreshGuestCN error %v", err)
		return nil, err
	}

	cookieUrl, err := url.Parse(pathWeiboCN)
	if err != nil {
		panic(fmt.Sprintf("path %v url parse error", pathWeiboCN))
	}
	visitorUA.Store(ua)
	return jar.Cookies(cookieUrl), nil
}

func FreshCookieLogin() ([]*http.Cookie, error) {
	jar, _ := cookiejar.New(nil)

	// 使用随机 UA 并存储
	ua := requests.RandomUA(requests.Chrome)
	visitorUA.Store(ua)

	genVisitorResp, err := genvisitorLogin(requests.WithCookieJar(jar), requests.AddUAOption(ua))
	if err != nil {
		logger.Errorf("genvisitorLogin error %v", err)
		return nil, err
	}
	if genVisitorResp.GetRetcode() != 20000000 || !strings.Contains(genVisitorResp.GetMsg(), "succ") {
		logger.WithFields(logrus.Fields{
			"Msg":     genVisitorResp.GetMsg(),
			"Retcode": genVisitorResp.GetRetcode(),
		}).Errorf("genvisitorLogin error")
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

	// 从 weibo.com 获取 XSRF-TOKEN 和 WBPSESS
	weiboCookies := jar.Cookies(baseUrl)
	for _, c := range weiboCookies {
		if c.Name == "XSRF-TOKEN" || c.Name == "WBPSESS" {
			cookies = append(cookies, c)
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

func ForceFreshCookie() bool {
	refreshMu.Lock()
	defer refreshMu.Unlock()

	if cookieHealthy.Load() {
		return true
	}

	// API 模式不需要 Cookie
	if cfg.IsWeiboAPIMode() {
		cookieHealthy.Store(true)
		consecutiveCookieFails.Store(0)
		return true
	}

	if isGuestMode() {
		// Guest 模式：直接刷新全部 cookie
		cookies, err := FreshCookieGuest()
		if err != nil {
			fails := consecutiveCookieFails.Inc()
			cookieHealthy.Store(false)
			logger.WithField("ConsecutiveFails", fails).Errorf("ForceFreshCookie Guest 失败: %v", err)
			return false
		}
		var opt []requests.Option
		for _, cookie := range cookies {
			opt = append(opt, requests.CookieOption(cookie.Name, cookie.Value))
		}
		visitorCookiesOpt.Store(opt)
		cookieHealthy.Store(true)
		consecutiveCookieFails.Store(0)
		logger.Infof("微博 Guest Cookie 刷新成功")
		return true
	}

	// Login 模式：保留 SUB 和 XSRF-TOKEN，只刷新其他会话 cookie
	currentOpts := CookieOption()
	existingSUB := extractCookieValue(currentOpts, "SUB")
	existingXSRF := extractCookieValue(currentOpts, "XSRF-TOKEN")

	// 如果内存中没有 SUB，尝试从配置中读取
	if existingSUB == "" {
		existingSUB = GetSettingCookie()
		if existingSUB != "" {
			logger.Debugf("从配置中读取到 SUB")
		}
	}
	// 最后尝试从运行时 SUB 读取（扫码登录等场景会更新运行时 SUB）
	if existingSUB == "" {
		existingSUB = getRuntimeSUB()
		if existingSUB != "" {
			logger.Debugf("从运行时 SUB 读取到 SUB")
		}
	}

	cookies, err := FreshCookieLogin()
	if err != nil {
		fails := consecutiveCookieFails.Inc()
		cookieHealthy.Store(false)
		logger.WithField("ConsecutiveFails", fails).Errorf("ForceFreshCookie Login 失败: %v", err)
		return false
	}

	// 用 FreshCookieLogin 返回的 cookie 做基础，但保留现有的 SUB 和 XSRF-TOKEN
	var opt []requests.Option
	hasSUB := false
	hasXSRF := false

	for _, cookie := range cookies {
		if cookie.Name == "SUB" {
			hasSUB = true
			if existingSUB != "" {
				opt = append(opt, requests.CookieOption("SUB", existingSUB))
			} else {
				opt = append(opt, requests.CookieOption(cookie.Name, cookie.Value))
			}
		} else if cookie.Name == "XSRF-TOKEN" {
			hasXSRF = true
			if existingXSRF != "" {
				opt = append(opt, requests.CookieOption("XSRF-TOKEN", existingXSRF))
			} else {
				opt = append(opt, requests.CookieOption(cookie.Name, cookie.Value))
			}
		} else {
			opt = append(opt, requests.CookieOption(cookie.Name, cookie.Value))
		}
	}

	// 确保 SUB 和 XSRF-TOKEN 不会丢失
	if !hasSUB && existingSUB != "" {
		opt = append(opt, requests.CookieOption("SUB", existingSUB))
	}
	if !hasXSRF && existingXSRF != "" {
		opt = append(opt, requests.CookieOption("XSRF-TOKEN", existingXSRF))
	}

	visitorCookiesOpt.Store(opt)
	cookieHealthy.Store(true)
	consecutiveCookieFails.Store(0)
	logger.Infof("微博 Login Cookie 刷新成功（保留 SUB 和 XSRF-TOKEN）")
	return true
}

func extractCookieValue(opts []requests.Option, name string) string {
	return requests.ExtractCookieOption(opts, name)
}

// RefreshSessionCookie 刷新会话 cookie（XSRF-TOKEN、WBPSESS 等），保留 SUB
// 用于 Cookie 失效时自动恢复，不影响用户登录状态
func RefreshSessionCookie() bool {
	refreshMu.Lock()
	defer refreshMu.Unlock()

	// 获取当前的 SUB
	currentOpts := CookieOption()
	existingSUB := extractCookieValue(currentOpts, "SUB")
	if existingSUB == "" {
		existingSUB = GetSettingCookie()
	}
	if existingSUB == "" {
		logger.Warn("RefreshSessionCookie: 无可用的 SUB，跳过刷新")
		return false
	}

	// 获取新的会话 cookie
	cookies, err := FreshCookieLogin()
	if err != nil {
		logger.Errorf("RefreshSessionCookie: FreshCookieLogin 失败: %v", err)
		return false
	}

	// 构建新的 cookie 选项，保留 SUB
	var opt []requests.Option
	for _, cookie := range cookies {
		if cookie.Name == "SUB" {
			// 跳过 FreshCookieLogin 返回的 SUB（如果有），使用现有的
			continue
		}
		opt = append(opt, requests.CookieOption(cookie.Name, cookie.Value))
	}
	// 添加保留的 SUB
	opt = append(opt, requests.CookieOption("SUB", existingSUB))

	visitorCookiesOpt.Store(opt)
	logger.Infof("会话 cookie 已刷新，保留 SUB，共加载 %d 个 cookie", len(opt))
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
