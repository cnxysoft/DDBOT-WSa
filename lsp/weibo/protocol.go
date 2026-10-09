package weibo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/html/charset"
)

// 微博协议层：纯 HTTP 的访客流程与扫码登录流程。
// Cookie Jar 与访客 TID 等状态持久化在 stateDir 下，供重启后复用。
const (
	pathPassportVisitor   = "https://passport.weibo.com/visitor/visitor"
	pathLoginVisitor      = "https://login.sina.com.cn/visitor/visitor"
	visitorGrayCallback   = "visitor_gray_callback"
	crossDomainByLocation = "CROSSDOMAIN_BY_LOCATION"

	visitorCookieStateFile = "visitor-cookies.json"
	loginCookieStateFile   = "login-cookies.json"
	cookieMetaFile         = "meta.json"

	defaultWeiboStateDir = ".weibo-state"

	weiboCookieStateVersion = 1

	maxProtocolBodySize  = 8 << 20
	maxProtocolRedirects = 8

	// defaultNavigationMax 成品 navigate 在 max<=0 时的默认跳转上限（prologue cmovle $0x18）。
	defaultNavigationMax = 24

	// defaultQRCheckInterval 成品 waitForQRCode 在入参 interval<=0 时的回退值
	// （prologue mov $0xb2d05e00,%r8d; cmovle），0xb2d05e00 ns = 3s。
	defaultQRCheckInterval = 3 * time.Second
)

var (
	crossDomainJSRegex   = regexp.MustCompile("(?is)\\bcrossDomainUrlList\\s*=\\s*(\\[[^\\]]*\\])")
	crossDomainJSONRegex = regexp.MustCompile("(?is)[\"']crossDomainUrlList[\"']\\s*:\\s*(\\[[^\\]]*\\])")
	visitorVerRegex      = regexp.MustCompile("(?i)/visitor/genvisitor2[^\"'<>]*?[?&]ver=([0-9]+)")
	requestIDRegex       = regexp.MustCompile("(?i)var\\s+request_id\\s*=\\s*[\"']([^\"']+)[\"']")
	returnURLRegex       = regexp.MustCompile("(?i)var\\s+return_url\\s*=\\s*[\"']([^\"']+)[\"']")
	clientRedirectRegex  = regexp.MustCompile("(?is)(?:window\\.)?location\\s*=\\s*\"([^\"']+)\"")
	clientReplaceRegex   = regexp.MustCompile("(?is)location\\.replace\\(\\s*'([^']+)'\\s*\\)")
	clientRedirectAlt    = regexp.MustCompile("(?is)(?:window\\.)?location\\s*=\\s*'([^']+)'")
	quotedStringRegex    = regexp.MustCompile("[\"']([^\"']+)[\"']")
	sensitiveParamRegex  = regexp.MustCompile("(?i)([?&](?:alt|token|ticket|code|data|s|sp|sub|subp)=)[^&#\\s]*")
)

// weiboEndpoints 微博协议使用的端点集合
type weiboEndpoints struct {
	Home       string
	NewLogin   string
	VisitorGen string
	QRImage    string
	QRCheck    string
	QRLogin    string
	SpaConfig  string
	Heartbeat  string
}

var defaultWeiboEndpoints = weiboEndpoints{
	Home:       "https://m.weibo.cn/",
	NewLogin:   "https://weibo.com/newlogin?url=https%3A%2F%2Fweibo.com%2F",
	VisitorGen: "https://passport.weibo.com/visitor/genvisitor2",
	QRImage:    "https://login.sina.com.cn/sso/qrcode/image",
	QRCheck:    "https://login.sina.com.cn/sso/qrcode/check",
	QRLogin:    "https://login.sina.com.cn/sso/login.php",
	SpaConfig:  "https://weibo.com/ajax/getSpaConfig",
	Heartbeat:  "https://weibo.com/ajax/config/get_config",
}

// storedCookie 可序列化的 Cookie 记录，用于状态文件持久化
type storedCookie struct {
	Name     string        `json:"name"`
	Value    string        `json:"value"`
	Domain   string        `json:"domain,omitempty"`
	Host     string        `json:"host"`
	HostOnly bool          `json:"hostOnly,omitempty"`
	Path     string        `json:"path"`
	Expires  time.Time     `json:"expires,omitempty"`
	MaxAge   int           `json:"maxAge,omitempty"`
	Secure   bool          `json:"secure,omitempty"`
	HTTPOnly bool          `json:"httpOnly,omitempty"`
	SameSite http.SameSite `json:"sameSite,omitempty"`
}

// weiboCookieMeta 访客 TID 等轻量元信息，与 Cookie 状态分开保存
type weiboCookieMeta struct {
	VisitorTID       string    `json:"visitorTid,omitempty"`
	VisitorNewTID    string    `json:"visitorNewTid,omitempty"`
	VisitorUpdatedAt time.Time `json:"visitorUpdatedAt,omitempty"`
	LoginUpdatedAt   time.Time `json:"loginUpdatedAt,omitempty"`
}

// weiboCookieState Cookie 状态文件结构
type weiboCookieState struct {
	Version   int            `json:"version"`
	UserAgent string         `json:"userAgent"`
	UpdatedAt time.Time      `json:"updatedAt"`
	Cookies   []storedCookie `json:"cookies"`
}

// persistentCookieJar 带记录副本的 Cookie Jar
type persistentCookieJar struct {
	inner   http.CookieJar
	mu      sync.RWMutex
	records map[string]storedCookie
}

// weiboProtocol 微博协议客户端
type weiboProtocol struct {
	client    *http.Client
	jar       *persistentCookieJar
	userAgent string
	endpoints weiboEndpoints
}

// visitorJSONPResponse genvisitor2 JSONP 响应
type visitorJSONPResponse struct {
	Retcode int64  `json:"retcode"`
	Msg     string `json:"msg"`
	Data    struct {
		Alt    string      `json:"alt"`
		SUB    string      `json:"sub"`
		SUBP   string      `json:"subp"`
		TID    interface{} `json:"tid"`
		NewTID interface{} `json:"new_tid"`
		Next   string      `json:"next"`
	} `json:"data"`
}

// userJSON 移动端用户信息负载（profile 相关接口返回的原始 JSON 用户对象）
type userJSON struct {
	Id              int64  `json:"id,omitempty"`
	ScreenName      string `json:"screen_name,omitempty"`
	ProfileImageUrl string `json:"profile_image_url,omitempty"`
	ProfileUrl      string `json:"profile_url,omitempty"`
}

// qrImageResponse 扫码登录二维码图片响应。
// 成品证据：
//   - JSONP 解析目标 type @0xfcba80（大小 0x48 = 72）；
//   - 成品类型表 recovered/types/types-closure-x64.tsv:8860 明确 Interval 在 Data 内：
//     weibo.qrImageResponse struct {
//     Retcode int64 `json:"retcode"`; Msg string `json:"msg"`;
//     Data struct { QRID string `json:"qrid"`; Image string `json:"image"`;
//     Interval interface{} `json:"interval"` } `json:"data"` }
//
// 字段偏移：Retcode 0x00、Msg 0x08、Data.QRID 0x18/0x20、Data.Image 0x28/0x30、
// Data.Interval 0x38——Interval 属于 data 内层，不是顶层。
type qrImageResponse struct {
	Retcode int64  `json:"retcode"`
	Msg     string `json:"msg"`
	Data    struct {
		QRID     string      `json:"qrid"`
		Image    string      `json:"image"`
		Interval interface{} `json:"interval"`
	} `json:"data"`
}

// qrCheckResponse 扫码轮询响应（4 词聚合，成品类型表 types-closure-x64.tsv:8859）：
//
//	weibo.qrCheckResponse struct {
//	    Retcode int64 `json:"retcode"`; Msg string `json:"msg"`;
//	    Data map[string]interface{} `json:"data"` }
//
// 字段偏移：Retcode 0x00、Msg 0x08/0x10、Data 0x18。checkQRCode 与 waitForQRCode
// 都以「该聚合 + error」返回（6 个结果寄存器：聚合 4 词在 RAX/RBX/RCX/RDI，
// error 2 词在 RSI/R8；见 waitForQRCode 0xc5f4f8 只清零前 4 词、保留 RSI/R8）；
// RunQRLogin 用 Data["alt"]("alt" @0x1098896 len3) 取扫码结果。
type qrCheckResponse struct {
	Retcode int64                  `json:"retcode"`
	Msg     string                 `json:"msg"`
	Data    map[string]interface{} `json:"data"`
}

// weiboQRCode 是 createQRCode 的返回聚合。
// 成品证据：
//   - createQRCode(@protocol.go:880, 0xc5e280) 返回 9 个机器字：7 词聚合 + error；
//   - RunQRLogin 在 +0x6a0 调用后把前 8 词拷到局部 0x100，再于 +0xcf2 整体压栈
//     传给 waitForQRCode（outgoing arg 区 rsp+0..0x40）；
//   - waitForQRCode prologue 从栈参 0x110/0x118 读 Qrid、从 0x140 读 Interval（offset 0x30）。
//
// 字段偏移：Qrid 0x00、ImageURL 0x10、Data 0x20、Interval 0x30。
type weiboQRCode struct {
	Qrid     string        // +0x00
	ImageURL string        // +0x10
	Data     string        // +0x20
	Interval time.Duration // +0x30
}

// protocolResponse 协议层响应快照。
// 成品类型证据（recovered/types/types-closure-x64.tsv:10200）：
//
//	weibo.protocolResponse struct {
//	    URL string; Status int; Header http.Header;
//	    Body []uint8; Text string; SetCookieNames []string
//	}
//
// 成品 request 尾部 newobject(type @0x10159c0,size 0x60) 后写：
// URL=入参 target(0x00)、Status=resp.StatusCode(0x10)、Header=resp.Header(0x18)、
// Body=解码后的响应体(0x20)、Text=string(Body)(0x38)、SetCookieNames=resp.Cookies() 名称(0x48)。
type protocolResponse struct {
	URL            string
	Status         int
	Header         http.Header
	Body           []byte
	Text           string
	SetCookieNames []string
}

// navigationStep 单次导航记录。
// 成品类型证据（types-closure-x64.tsv:8858）：
//
//	weibo.navigationStep struct { URL string; Status int; SetCookieNames []string }
type navigationStep struct {
	URL            string
	Status         int
	SetCookieNames []string
}

// navigationResult 导航结果。
// 成品类型证据（types-closure-x64.tsv:8862）：
//
//	weibo.navigationResult struct {
//	    Response *weibo.protocolResponse; Trail []weibo.navigationStep; FinalURL string
//	}
type navigationResult struct {
	Response *protocolResponse
	Trail    []navigationStep
	FinalURL string
}

// defaultCookiePath 按请求路径推导 Cookie 的默认 Path（RFC 6265 default-path）。
// 成品调用图证据：defaultCookiePath(protocol.go:112) 只被 (*persistentCookieJar).SetCookies(:123)
// 调用，函数体在 +0x45 处调用 strings.LastIndex，说明入参是 URL 路径字符串。
// [推断] 具体算法按 RFC 6265 default-path 复原（取最后一个 '/' 之前的部分，根路径回退 "/"）。
func defaultCookiePath(rawPath string) string {
	i := strings.LastIndex(rawPath, "/")
	if i <= 0 {
		return "/"
	}
	return rawPath[:i]
}

// SetCookies 实现 http.CookieJar，同时维护记录副本
func (j *persistentCookieJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, cookie := range cookies {
		if cookie == nil || cookie.Name == "" {
			continue
		}
		sc := storedCookie{
			Name:     cookie.Name,
			Value:    cookie.Value,
			Path:     cookie.Path,
			MaxAge:   cookie.MaxAge,
			Secure:   cookie.Secure,
			HTTPOnly: cookie.HttpOnly,
			SameSite: cookie.SameSite,
		}
		if !cookie.Expires.IsZero() {
			sc.Expires = cookie.Expires
		}
		host := u.Hostname()
		sc.HostOnly = true
		if cookie.Domain != "" {
			domain := strings.TrimPrefix(cookie.Domain, ".")
			sc.Domain = domain
			sc.HostOnly = false
			host = domain
		}
		sc.Host = host
		if sc.Path == "" {
			sc.Path = defaultCookiePath(u.Path)
		}
		j.records[cookieRecordKey(sc.Name, sc.Domain, sc.Path)] = sc
	}
	if j.inner != nil {
		j.inner.SetCookies(u, cookies)
	}
}

// Cookies 实现 http.CookieJar
func (j *persistentCookieJar) Cookies(u *url.URL) []*http.Cookie {
	if j.inner == nil {
		return nil
	}
	return j.inner.Cookies(u)
}

// importRecords 将状态文件中的记录回放到 inner jar
func (j *persistentCookieJar) importRecords(records []storedCookie) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, sc := range records {
		if sc.Name == "" {
			continue
		}
		key := cookieRecordKey(sc.Name, sc.Domain, sc.Path)
		if !sc.Expires.IsZero() && sc.Expires.Before(time.Now()) {
			continue
		}
		j.records[key] = sc
		if j.inner == nil {
			continue
		}
		host := sc.Host
		if host == "" {
			host = sc.Domain
		}
		if host == "" {
			continue
		}
		path := sc.Path
		if path == "" {
			path = "/"
		}
		u := &url.URL{
			Scheme: "https",
			Host:   host,
			Path:   path,
		}
		j.inner.SetCookies(u, []*http.Cookie{{
			Name:     sc.Name,
			Value:    sc.Value,
			Domain:   sc.Domain,
			Path:     path,
			Expires:  sc.Expires,
			MaxAge:   sc.MaxAge,
			Secure:   sc.Secure,
			HttpOnly: sc.HTTPOnly,
			SameSite: sc.SameSite,
		}})
	}
}

// snapshot 返回按键名排序的 Cookie 记录快照，保证状态文件内容稳定
func (j *persistentCookieJar) snapshot() []storedCookie {
	j.mu.RLock()
	defer j.mu.RUnlock()
	entries := make([]struct {
		key  string
		elem *storedCookie
	}, 0, len(j.records))
	for key, record := range j.records {
		sc := record
		entries = append(entries, struct {
			key  string
			elem *storedCookie
		}{key, &sc})
	}
	sort.Slice(entries, func(i, k int) bool {
		return entries[i].key < entries[k].key
	})
	result := make([]storedCookie, 0, len(entries))
	for _, entry := range entries {
		result = append(result, *entry.elem)
	}
	return result
}

// fingerprint 当前 Cookie 集合的指纹，用于检测轮换
func (j *persistentCookieJar) fingerprint() string {
	h := sha256.New()
	for _, sc := range j.snapshot() {
		fmt.Fprintf(h, "%s=%s;", sc.Name, sc.Value)
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// setConfiguredSUB 将配置中的 SUB 注入 Jar（weibo.com 域），保证订阅接口可用
func (j *persistentCookieJar) setConfiguredSUB() {
	sub := GetSettingCookie()
	if sub == "" {
		return
	}
	u, err := url.Parse("https://weibo.com/")
	if err != nil {
		return
	}
	for _, cookie := range j.Cookies(u) {
		if cookie.Name == "SUB" && cookie.Value != "" {
			return
		}
	}
	logger.Debug("微博 Cookie Jar 未找到 SUB，使用配置中的 SUB 注入")
	j.SetCookies(u, []*http.Cookie{{Name: "SUB", Value: sub, Path: "/"}})
}

// currentWeiboStatePaths 返回访客与登录 Cookie 状态文件路径。
// 成品证据：currentWeiboStatePaths(protocol.go:265) 直接调用 cfg.GetWeiboStateDir()，
// 默认目录由调用方补齐。
func currentWeiboStatePaths() (string, string) {
	stateDir := cfg.GetWeiboStateDir()
	if stateDir == "" {
		stateDir = defaultWeiboStateDir
	}
	_ = os.MkdirAll(stateDir, 0o700)
	return filepath.Join(stateDir, visitorCookieStateFile),
		filepath.Join(stateDir, loginCookieStateFile)
}

// loadCookieState 读取 Cookie 状态文件，文件不存在时返回 (nil, nil)
func loadCookieState(path string) (*weiboCookieState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取微博 Cookie 状态失败: %w", err)
	}
	var state weiboCookieState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("解析微博 Cookie 状态失败: %w", err)
	}
	if state.Version != weiboCookieStateVersion {
		return nil, fmt.Errorf("不支持的微博 Cookie 状态版本: %d", state.Version)
	}
	return &state, nil
}

// saveCookieState 持久化 Cookie 状态
func saveCookieState(path string, userAgent string, cookies []storedCookie) error {
	state := weiboCookieState{
		Version:   weiboCookieStateVersion,
		UserAgent: userAgent,
		UpdatedAt: time.Now(),
		Cookies:   cookies,
	}
	return writePrivateJSON(path, &state)
}

// loadCookieMeta 读取 Cookie 元信息（访客 TID 等），不存在时返回空结构与 nil error
func loadCookieMeta(stateDir string) (*weiboCookieMeta, error) {
	meta := new(weiboCookieMeta)
	if stateDir == "" {
		return meta, nil
	}
	data, err := os.ReadFile(filepath.Join(stateDir, cookieMetaFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return meta, nil
		}
		return meta, err
	}
	if err := json.Unmarshal(data, meta); err != nil {
		return meta, fmt.Errorf("解析微博 Cookie 元数据失败: %w", err)
	}
	return meta, nil
}

// saveCookieMeta 持久化 Cookie 元信息（访客 TID 等）
func saveCookieMeta(stateDir string, meta *weiboCookieMeta) error {
	if meta == nil {
		return nil
	}
	return writePrivateJSON(filepath.Join(stateDir, cookieMetaFile), meta)
}

// writePrivateJSON 以 0600 权限原子写入 JSON 文件（临时文件 + rename）
func writePrivateJSON(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// newPersistentCookieJar 创建空的持久化 Cookie Jar
func newPersistentCookieJar() *persistentCookieJar {
	inner, _ := cookiejar.New(nil)
	return &persistentCookieJar{
		inner:   inner,
		records: make(map[string]storedCookie),
	}
}

// loadPersistentCookieJar 创建 Cookie Jar 并回放已保存的状态
func loadPersistentCookieJar(path string) *persistentCookieJar {
	jar := newPersistentCookieJar()
	state, err := loadCookieState(path)
	if err != nil {
		logger.Warnf("微博登录 Cookie 预热失败: %v", err)
		return jar
	}
	if state != nil && len(state.Cookies) > 0 {
		jar.importRecords(state.Cookies)
	}
	return jar
}

// newWeiboProtocol 以默认端点创建协议客户端
func newWeiboProtocol(jar *persistentCookieJar) *weiboProtocol {
	return newWeiboProtocolWithEndpoints(defaultWeiboEndpoints, jar)
}

// newWeiboProtocolWithEndpoints 以指定端点创建协议客户端
func newWeiboProtocolWithEndpoints(endpoints weiboEndpoints, jar *persistentCookieJar) *weiboProtocol {
	if jar == nil {
		jar = newPersistentCookieJar()
	}
	client := &http.Client{
		Jar:     jar,
		Timeout: weiboRequestTimeout(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return nil
		},
	}
	// 成品证据：newWeiboProtocolWithEndpoints(protocol.go:384) 调用 cfg.GetWeiboUserAgent()
	// 与 cfg.GetWeiboRequestTimeout()。
	ua := cfg.GetWeiboUserAgent()
	if ua == "" {
		ua = requests.RandomUA(requests.Chrome)
	}
	return &weiboProtocol{
		client:    client,
		jar:       jar,
		userAgent: ua,
		endpoints: endpoints,
	}
}

// decodeProtocolBody 按 Content-Type 做字符集识别，把响应体解码为字符串。
//
// 成品证据（decodeProtocolBody @protocol.go:418，320B）：callees 只有
// golang.org/x/net/html/charset.NewReader(r io.Reader, contentType string) 与 io.ReadAll；
// 流程为 bytes.NewReader(body) -> charset.NewReader(reader, contentType) -> io.ReadAll，
// 两处出错都回退 string(body)。读取与 8MiB 超限检查在调用方 request 内完成。
func decodeProtocolBody(body []byte, contentType string) string {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return string(body)
	}
	decoded, err := io.ReadAll(r)
	if err != nil {
		return string(body)
	}
	return string(decoded)
}

// 协议层 Accept 常量（成品 rodata 逐字还原）：
//
//	acceptHTML     @0x10c5601 len 31 "text/html,application/xhtml+xml"
//	acceptAny      @0x1098866 len 3  "*/*"
//	acceptJSON     @0x10c8953 len 33 "application/json, text/plain, */*"
//	acceptHTMLJSON @0x10e7271 len 64 "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8"
const (
	acceptHTML     = "text/html,application/xhtml+xml"
	acceptAny      = "*/*"
	acceptJSON     = "application/json, text/plain, */*"
	acceptHTMLJSON = "text/html,application/xhtml+xml,application/json;q=0.9,*/*;q=0.8"
)

// request 协议层 HTTP 请求：统一 UA、超时与响应体读取。
// 成品证据：(*weiboProtocol).request @protocol.go:430（0xc59780，3424B）
//   - prologue（0xc597f9..0xc59820）把入参 R8/R9（target）原样转发给
//     net/http.NewRequestWithContext（0x7deba0）作为 url，**没有 strings.TrimSpace**、
//     也没有空串检查；request 的 callees 中无 errors.New（"empty url" 亦未被本函数引用）；
//   - 第 5 参 body 为 io.Reader，由调用方经栈传入（prologue 从 0x230/0x238 取）；
//   - get 调用本函数前用 movups %xmm15,(%rsp) 把 16 字节接口清零 → body=nil；
//   - createVisitor +0xd36 构造 *strings.Reader（newobject type @0xfcda00，itab @0x112c100）
//     作为 body 调用本函数；
//   - 默认头（仅当调用方 headers 未给该键时）：User-Agent=p.userAgent（@0x10a751b）、
//     Accept="*/*"（@0x1098866）；成品 request 未设置 Accept-Language。
func (p *weiboProtocol) request(ctx context.Context, method string, target string, headers map[string]string, body io.Reader) (*protocolResponse, error) {
	st := time.Now()
	// 成品不 trim target：空 URL 直接交给 NewRequestWithContext，由其返回错误。
	sanitized := sanitizeProtocolURL(target)
	defer func() {
		logger.WithField("FuncName", utils.FuncName()).
			WithField("URL", sanitized).
			Tracef("cost %v", time.Since(st))
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		logger.WithField("URL", sanitized).Errorf("构建微博协议请求失败: %v", err)
		return nil, err
	}
	req.Header.Set("user-agent", p.userAgent)
	req.Header.Set("accept", acceptAny)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		logger.WithField("URL", sanitized).Errorf("微博协议请求失败: %v", err)
		return nil, err
	}
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == 432 {
		logger.WithFields(logrus.Fields{
			"URL":        sanitized,
			"StatusCode": resp.StatusCode,
		}).Warn("微博协议请求被人机验证拦截")
	}
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxProtocolBodySize+1))
	_ = resp.Body.Close()
	if err != nil {
		return newProtocolResponse(resp, target, nil), err
	}
	if len(respBody) > maxProtocolBodySize {
		return newProtocolResponse(resp, target, nil), fmt.Errorf("微博协议响应超过 %d 字节限制", maxProtocolBodySize)
	}
	pr := newProtocolResponse(resp, target, respBody)
	pr.Text = decodeProtocolBody(respBody, resp.Header.Get("Content-Type"))
	return pr, nil
}

// newProtocolResponse 把 *http.Response 折叠为协议层响应快照（成品 protocolResponse）。
// 成品证据（request @0xc59780）：
//   - prologue 直接把入参 target（R8/R9）存到 0x268/0x270，+0x8c0 写入 newobject
//     (type @0x10159c0,size 0x60) 的 +0x00/+0x08——URL 是原始 target，**不做 sanitize**
//     （sanitizeProtocolURL 只用于日志）；
//   - Status=resp.StatusCode(+0x10)、Header=resp.Header(+0x18)、Body(+0x20)、Text(+0x38)；
//   - SetCookieNames(+0x48) 由 request 内联构建：按 resp.Cookies() 名字顺序填充 slice
//     （+0x58d makeslice、+0x5c8 起填充；两次 net/http.readSetCookies），
//     **没有 sort.Strings / sort.Slice 调用**，即不排序。
func newProtocolResponse(resp *http.Response, target string, body []byte) *protocolResponse {
	pr := &protocolResponse{URL: target, Body: body, Text: string(body)}
	if resp != nil {
		pr.Status = resp.StatusCode
		pr.Header = resp.Header
		pr.SetCookieNames = cookieNamesOnly(resp.Cookies())
	}
	return pr
}

// get 协议层 GET：按调用方给定的 Referer/Accept 组装请求头后发起请求。
// 成品证据：(*weiboProtocol).get @protocol.go:480（0xc5a520，800B）签名
// (ctx, url, referer, accept) -> (*protocolResponse, err)；入参 R8/R9=referer、R10/R11=accept
// （get prologue 保存 R8→0x218/R9→0x220 后在 +0x10c 构造 "Referer" 头、
//
//	保存 R10→0x228/R11→0x230 后在 +0x1c0 构造 "Accept" 头，len==0 则跳过）；
//
// 尾调用 request(ctx, "GET", url, headers, nil)（+0x2a8）。
func (p *weiboProtocol) get(ctx context.Context, target string, referer string, accept string) (*protocolResponse, error) {
	headers := map[string]string{}
	if referer != "" {
		headers["Referer"] = referer
	}
	if accept != "" {
		headers["Accept"] = accept
	}
	return p.request(ctx, http.MethodGet, target, headers, nil)
}

// navigate 协议层导航：单次 get 后跟随 3xx Location 或页面 JS 跳转，
// 并按跨域同步列表继续 get；记录每一步 Trail，返回 navigationResult。
//
// 成品证据（(*weiboProtocol).navigate @protocol.go:491，0xc5a840，2464B）：
//   - 签名 (ctx,target,referer string,max int) -> (*navigationResult,error)；
//     max<=0 取默认 24（prologue: test r10,r10; cmovle $0x18）；
//   - +0x72 make([]navigationStep,0,max)（makeslice type @0xfcb900）；
//   - +0x129 首个 get：Referer=入参 referer、Accept=acceptHTMLJSON(@0x10e7271 len 64)；
//     之后外层每轮 Referer=上一跳 URL（0xa8/0x68）；
//   - 3xx 且 Header.Get("Location")(+0x2ac) 非空 -> resolveProtocolURL 后继续外层；
//   - 否则 +0x2c6 extractCrossDomainURLs(resp.Text)，对每个 URL url.Parse，
//     其 Query().Get("action") 为空则 Set("action","login") 回写 RawQuery，
//     +0x681 get(target=该 URL, Referer=当前页, Accept="*/*" @0x1098866) 并记入 Trail；
//   - +0x862 extractClientRedirect(resp.Text) 命中 -> resolveProtocolURL 后继续外层；
//   - 超过 max 次 -> "微博导航超过最大跳转次数: %s"(0x10d2fab len 40)。
func (p *weiboProtocol) navigate(ctx context.Context, target string, referer string, max int) (*navigationResult, error) {
	if max <= 0 {
		max = defaultNavigationMax
	}
	trail := make([]navigationStep, 0, max)
	// 成品不 trim 入参 target：prologue 把 RDI/RSI 原样存到 0x1a8/0x1b0，
	// 首次 get 直接以该 ptr/len 作为 url（c5a8bc/c5a8c4 -> c5a948/c5a966），
	// 直到首次 get（c5a969）之前没有任何 strings.TrimSpace 调用。
	current := target
	pageReferer := referer
	for i := 0; i < max; i++ {
		resp, err := p.get(ctx, current, pageReferer, acceptHTMLJSON)
		if err != nil {
			return nil, fmt.Errorf("导航 %s 失败: %w", sanitizeProtocolURL(current), err)
		}
		pageReferer = current
		trail = append(trail, navigationStep{
			URL:            sanitizeProtocolURL(current),
			Status:         resp.Status,
			SetCookieNames: resp.SetCookieNames,
		})
		if resp.Status >= 300 && resp.Status < 400 {
			if loc := resp.Header.Get("Location"); loc != "" {
				next, err := resolveProtocolURL(current, loc)
				if err != nil {
					return nil, err
				}
				current = next
				continue
			}
		}
		for _, raw := range extractCrossDomainURLs(resp.Body) {
			parsed, err := url.Parse(strings.TrimSpace(raw))
			if err != nil || parsed == nil {
				continue
			}
			syncURL := parsed.String()
			if parsed.Query().Get("action") == "" {
				q := parsed.Query()
				q.Set("action", "login")
				parsed.RawQuery = q.Encode()
				syncURL = parsed.String()
			}
			syncResp, err := p.get(ctx, syncURL, current, acceptAny)
			if err != nil {
				return nil, fmt.Errorf("微博跨域 Cookie 同步失败: %w", err)
			}
			trail = append(trail, navigationStep{
				URL:            sanitizeProtocolURL(syncURL),
				Status:         syncResp.Status,
				SetCookieNames: syncResp.SetCookieNames,
			})
		}
		if redirect := extractClientRedirect(resp.Body); redirect != "" {
			next, err := resolveProtocolURL(current, redirect)
			if err != nil {
				return nil, err
			}
			current = next
			continue
		}
		return &navigationResult{Response: resp, Trail: trail, FinalURL: current}, nil
	}
	return nil, fmt.Errorf("微博导航超过最大跳转次数: %s", sanitizeProtocolURL(current))
}

// resolveProtocolURL 以 base 解析相对地址（成品 protocol.go:555）。
// 成品证据（0xc5b1e0）：url.Parse(base) -> TrimSpace(ref) -> html.UnescapeString(ref)
// -> url.Parse(ref) -> base.ResolveReference(ref).String()；两处 Parse 失败都返回 ("", err)。
func resolveProtocolURL(base string, ref string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	ref = html.UnescapeString(strings.TrimSpace(ref))
	refURL, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return baseURL.ResolveReference(refURL).String(), nil
}

// sanitizeProtocolURL 抹除 URL 中的敏感参数值，用于日志输出
func sanitizeProtocolURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !looksLikeHTTPURL(raw) {
		return raw
	}
	return sensitiveParamRegex.ReplaceAllString(raw, "$1[REDACTED]")
}

// extractClientRedirect 提取页面 JS 跳转地址
func extractClientRedirect(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	text := string(body)
	for _, re := range []*regexp.Regexp{clientRedirectRegex, clientReplaceRegex, clientRedirectAlt} {
		if m := re.FindStringSubmatch(text); len(m) > 1 {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// extractCrossDomainURLs 提取跨域 Cookie 同步 URL 列表（JSON 与 JS 赋值两种形态）
func extractCrossDomainURLs(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	text := string(body)
	if m := crossDomainJSONRegex.FindStringSubmatch(text); len(m) > 1 {
		var urls []string
		if err := json.Unmarshal([]byte(m[1]), &urls); err == nil && len(urls) > 0 {
			return urls
		}
	}
	if m := crossDomainJSRegex.FindStringSubmatch(text); len(m) > 1 {
		if urls := extractQuotedStrings([]byte(m[1])); len(urls) > 0 {
			return urls
		}
	}
	return nil
}

// extractQuotedStrings 提取文本中所有引号包裹的字符串
func extractQuotedStrings(body []byte) []string {
	matches := quotedStringRegex.FindAllStringSubmatch(string(body), -1)
	result := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) > 1 && m[1] != "" {
			result = append(result, m[1])
		}
	}
	return result
}

// uniqueJSONPCallback 生成一次性 JSONP 回调名
func uniqueJSONPCallback() string {
	return fmt.Sprintf("STK_%d%d", time.Now().UnixMilli(), time.Now().UnixNano()%1_000_000)
}

// parseJSONP 解析 JSONP 响应体为 JSON。
// 成品证据：parseJSONP @protocol.go:651（0xc5bec0，544B），签名为
// (text string, callback string, out interface{}) error——不是重建旧稿的
// ([]byte, interface{})：
//   - prologue 连续保存 6 个参数寄存器：RAX/RBX=text 头(0x78/0x80)、
//     RCX/RDI=callback 头(0x88，RDI 原地留作 callback.len)、RSI/R8=out 的
//     itab/data(0x98/0xa0)；
//   - +0x3e runtime.concatstring2(0x4717e0) 拼 callback+"("（"(" @0x1124098），
//     +0x6c internal/stringslite.Index(0x418560) 定位起点，
//     +0x8f strings.LastIndex(0x539180) 定位末尾 ")"（")" @0x11212f8），
//     切片经 runtime.stringtoslicebyte(0x472000) 直接交给
//     encoding/json.Unmarshal(0x53f680)——**不是**包级 jsoniter
//     （*jsoniter.ConfigCompatibleWithStandardLibrary，weibo.go 定义）；
//   - 无 TrimSpace、无 payload=="" 判定；仅要求 start>=0 且
//     end>start+len(callback+"(")；
//   - 两处错误文案：@0x10c56da len31「微博 JSONP 响应格式异常」经 fmt.Errorf
//     （fmt.Errorf 被内联为直呼 fmt.errorf @0x51bb20，无参）、
//     @0x10c2692 len29「解析微博 JSONP 失败: %w」带 Unmarshal error；
//   - 返回单值 error（成功 RAX/RBX 清零，失败 = itab @0x112be60）。
func parseJSONP(text string, callback string, out interface{}) error {
	marker := callback + "("
	start := strings.Index(text, marker)
	if start < 0 {
		return fmt.Errorf("微博 JSONP 响应格式异常")
	}
	end := strings.LastIndex(text, ")")
	if end <= start+len(marker) {
		return fmt.Errorf("微博 JSONP 响应格式异常")
	}
	if err := stdjson.Unmarshal([]byte(text[start+len(marker):end]), out); err != nil {
		return fmt.Errorf("解析微博 JSONP 失败: %w", err)
	}
	return nil
}

// visitorPageInfo 访客页解析结果
type visitorPageInfo struct {
	RequestID string
	ReturnURL string
	Ver       string
}

func (v *visitorPageInfo) empty() bool {
	return v.RequestID == "" && v.ReturnURL == "" && v.Ver == ""
}

// parseVisitorPage 解析访客页中的 request_id/return_url/ver
func parseVisitorPage(body []byte) *visitorPageInfo {
	info := new(visitorPageInfo)
	if len(body) == 0 {
		return info
	}
	text := string(body)
	if m := requestIDRegex.FindStringSubmatch(text); len(m) > 1 {
		info.RequestID = strings.TrimSpace(m[1])
	}
	if m := returnURLRegex.FindStringSubmatch(text); len(m) > 1 {
		info.ReturnURL = strings.TrimSpace(m[1])
	}
	if m := visitorVerRegex.FindStringSubmatch(text); len(m) > 1 {
		info.Ver = strings.TrimSpace(m[1])
	}
	return info
}

// createVisitor 纯 HTTP 访客流程：解析访客页 -> genvisitor2 -> 注入临时 Cookie -> 跨域同步。
//
// 成品签名证据（(*weiboProtocol).createVisitor @protocol.go:710，0xc5c300，7520B）：
//   - 入参：RAX=p、RBX/RCX=ctx、RDI/RSI=tid string；两个调用点（RunQRLogin +0x34b、
//     FreshCookieGuest +0x1d7）都把 loadCookieMeta 得到的 meta.VisitorTID 传入；
//   - 返回 7 词聚合 + error（R10/R11）：RAX=bool、RBX/RCX=string、RDI/RSI=string、R8/R9=string。
//     调用点把 0x38 字节整体拷出，按 0x00(bool)/0x08(string)/0x18(string)/0x28(string) 消费；
//   - spaConfig 分支（+0x10c，redirect 不含 passport.weibo.com/visitor/visitor）：不消费
//     spaConfig 的返回，直接返回 {bool=true, tid, "", ""}, nil（c5c40c..c5c47c 无 error 检查）；
//   - visitor 成功路径 bool=false，第 1/2 个 string 供调用方当访客 TID 候选
//     （RunQRLogin: firstNonEmpty(s1,tid) 再 firstNonEmpty(s2,prev)），第 3 个未被消费。
//
// 3 个 string 的语义（成品 disasm 坐实，不再是推断）：
//   - s1 = firstNonEmpty(protocolString(genResp.Data.TID), tid)：
//     +0x1880 调 protocolString 取 genResp+0x48（Data.TID interface{}）；
//     +0x1c41 内联 firstNonEmpty，数组第 2 项回退到入参 tid（prologue 存在 0x570/0x578）。
//     调用方在非 spaOnly 成功路径把它回放为 meta.VisitorTID 的候选。
//   - s2 = protocolString(genResp.Data.NewTID)：+0x1c79 取 genResp+0x58，函数内无回退；
//     调用方回放为 meta.VisitorNewTID 的候选。
//   - s3 = parseVisitorPage 返回的第 2 个 string = page.ReturnURL（保存在栈 0x190/0x198；
//     +0x788 以日志键 "return_url"(@0x10a77cd) 读取它，+0x1866 的 get 把它当 Referer），
//     两个调用方都不消费它。
//
// createVisitor **不持久化**：callees 中无 currentWeiboStatePaths/loadCookieMeta/
// saveCookieState/saveCookieMeta/writePrivateJSON；落盘全部由调用方完成
// （FreshCookieGuest +0x3f9 saveCookieState、+0x489 writePrivateJSON；
// RunQRLogin +0x17ce writePrivateJSON）。
func (p *weiboProtocol) createVisitor(ctx context.Context, tid string) (spaOnly bool, visitorTID string, visitorNewTID string, extra string, err error) {
	st := time.Now()
	defer func() {
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", time.Since(st))
	}()

	// 1. 首屏：get(NewLogin,"",HTML) -> 读 Location/JS 跳转 -> resolveProtocolURL
	//    -> get(resolved, Home, HTML)，再从跳转页解析访客参数。
	// 成品证据（createVisitor @protocol.go:710，0xc5c300）：
	//   +0x80  get(target=p.endpoints.NewLogin(0x30/0x38), referer="", Accept=acceptHTML(@0x10c5601 len31))；
	//   +0xa6  resp.Header.Get("Location")(@0x10a4a96 len8)；
	//   +0xc0  无 Location 时 extractClientRedirect(resp.Text)；
	//   +0xe6  strings.Index(跳转地址, "passport.weibo.com/visitor/visitor"(@0x10ca552 len34))；
	//   +0x1a0 resolveProtocolURL(base=NewLogin, ref=跳转地址)；
	//   +0x1f4 get(target=resolved, referer=p.endpoints.Home(0x20/0x28), Accept=acceptHTML)。
	firstResp, err := p.get(ctx, p.endpoints.NewLogin, "", acceptHTML)
	if err != nil {
		return false, "", "", "", err
	}
	redirect := ""
	if loc := firstResp.Header.Get("Location"); loc != "" {
		redirect = loc
	} else {
		redirect = extractClientRedirect(firstResp.Body)
	}
	if !strings.Contains(redirect, "passport.weibo.com/visitor/visitor") {
		// 成品 +0x10c 调用 spaConfig 后完全忽略其返回值（无 error 检查），
		// 直接返回 {bool=true, tid, "", ""}, nil。
		_, _ = p.spaConfig(ctx)
		return true, tid, "", "", nil
	}
	resolved, err := resolveProtocolURL(p.endpoints.NewLogin, redirect)
	if err != nil {
		return false, "", "", "", err
	}
	homeResp, err := p.get(ctx, resolved, p.endpoints.Home, acceptHTML)
	if err != nil {
		return false, "", "", "", err
	}
	page := parseVisitorPage(homeResp.Body)
	if page.empty() {
		return false, "", "", "", errors.New("微博访客页格式已变化，未找到 request_id/return_url/ver")
	}
	logger.WithFields(logrus.Fields{
		"Ver":       page.Ver,
		"RequestID": maskSensitive(page.RequestID),
	}).Debug("已解析微博访客页参数")

	// 2. genvisitor2 申请访客身份（JSONP），复用已保存的访客 TID
	params := url.Values{}
	params.Set("cb", visitorGrayCallback)
	if page.RequestID != "" {
		params.Set("request_id", page.RequestID)
	}
	if page.Ver != "" {
		params.Set("ver", page.Ver)
	}
	// 成品 createVisitor 不读 meta：tid 直接来自入参（prologue 0x570/0x578）。
	// 旧 TID 的回退由调用方在拿到返回值后用 firstNonEmpty 完成。
	params.Set("tid", tid)
	params.Set("from", "weibo")
	// 成品参数：webdriver="undefined"(@0x10a6197 len9)，并额外带 rid=UnixMilli(@0x1098842)。
	params.Set("webdriver", "undefined")
	params.Set("rid", strconv.FormatInt(time.Now().UnixMilli(), 10))
	returnURL := page.ReturnURL
	if returnURL == "" {
		returnURL = p.endpoints.NewLogin
	}
	params.Set("return_url", returnURL)

	// 成品证据：createVisitor+0xd36 不调用 get，而是直接调用 request：
	//   method=POST(@0x1099597 len4)、target=p.endpoints.VisitorGen(0x40/0x48)、
	//   body=*strings.Reader(newobject type @0xfcda00, itab @0x112c100) 包裹
	//   url.Values.Encode()（+0xc8a）；headers 5 项（CanonicalMIMEHeaderKey 调用点
	//   +0x8a0/+0x950/+0xa4f/+0xb19/+0xbe5）：
	//   Accept="*/*"、Content-Type="application/x-www-form-urlencoded; charset=UTF-8"
	//   (@0x10dbced len48)、Origin=url.Scheme+"://"+url.Host(@0x109f6ac, concatstring3 @0x471880)、
	//   Referer=登录跳转地址 resolved、X-Requested-With="XMLHttpRequest"(@0x10accef len14)。
	genHeaders := map[string]string{
		"Accept":           acceptAny,
		"Content-Type":     "application/x-www-form-urlencoded; charset=UTF-8",
		"Origin":           originOf(resolved),
		"Referer":          resolved,
		"X-Requested-With": "XMLHttpRequest",
	}
	genHTTP, err := p.request(ctx, http.MethodPost, p.endpoints.VisitorGen, genHeaders, strings.NewReader(params.Encode()))
	if err != nil {
		return false, "", "", "", err
	}
	var genResp visitorJSONPResponse
	if err := parseJSONP(genHTTP.Text, visitorGrayCallback, &genResp); err != nil {
		return false, "", "", "", err
	}
	if genResp.Retcode != 20000000 {
		return false, "", "", "", fmt.Errorf("微博 genvisitor2 失败: retcode=%d msg=%s", genResp.Retcode, genResp.Msg)
	}
	if genResp.Data.Alt == "" && genResp.Data.SUB == "" && genResp.Data.SUBP == "" {
		return false, "", "", "", errors.New("微博 genvisitor2 成功但未返回 alt 或 sub/subp，协议可能已变化")
	}

	// 3. 注入访客 Cookie（sub/subp -> weibo 域，alt -> passport 域）
	weiboURL, _ := url.Parse("https://weibo.com/")
	passportURL, _ := url.Parse("https://passport.weibo.com/")
	var visitorCookies []*http.Cookie
	if genResp.Data.SUB != "" {
		visitorCookies = append(visitorCookies, &http.Cookie{Name: "SUB", Value: genResp.Data.SUB, Path: "/"})
	}
	if genResp.Data.SUBP != "" {
		visitorCookies = append(visitorCookies, &http.Cookie{Name: "SUBP", Value: genResp.Data.SUBP, Path: "/"})
	}
	if len(visitorCookies) > 0 {
		p.jar.SetCookies(weiboURL, visitorCookies)
	}
	if genResp.Data.Alt != "" {
		p.jar.SetCookies(passportURL, []*http.Cookie{{Name: "ALT", Value: genResp.Data.Alt, Path: "/"}})
	}
	// 4. incarnate / 跨域同步：next 指向 visitor 端点；成品把 crossDomainUrlList 的
	//    同步 get 折叠进 navigate 内部，这里同样只调用 navigate。
	if next := strings.TrimSpace(genResp.Data.Next); next != "" {
		nextURL, err := resolveProtocolURL("https://weibo.com/", next)
		if err != nil || nextURL == "" {
			nextURL = next
		}
		if _, err := p.navigate(ctx, nextURL, p.endpoints.Home, defaultNavigationMax); err != nil {
			logger.Warnf("微博跨域 Cookie 同步失败: %v", err)
		}
	}

	// 5. 不落盘：成品由调用方持久化（避免双写）。
	//    返回值 = {spaOnly=false, s1=firstNonEmpty(protocolString(Data.TID), tid),
	//    s2=protocolString(Data.NewTID), s3=page.ReturnURL}。
	logger.Info("微博访客临时 Cookie 纯 HTTP 初始化完成")
	return false,
		firstNonEmpty(protocolString(genResp.Data.TID), tid),
		protocolString(genResp.Data.NewTID),
		page.ReturnURL, nil
}

// protocolString 将 JSON 中的动态类型字段转为字符串
// 成品证据（protocolString @protocol.go:807，0xc5e060，256B）：type-switch 恰 3 支——
// float64(0xee74c0)、json.Number(0xf63660)、string(0xee7100)，其余（含 nil/bool）返回空串。
func protocolString(value interface{}) string {
	switch v := value.(type) {
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case stdjson.Number:
		return v.String()
	case string:
		return v
	default:
		return ""
	}
}

// parseQRInterval 把扫码接口返回的动态 interval 值转成轮询间隔。
// 成品证据（protocol.go:861，0xc5e160，288B）：入参为 interface{}，type-switch 恰 3 支：
//
//	0x6ca6d294 / 0xee74c0 = float64     -> cvttsd2si 截断为 int64
//	0xbd2d7d09 / 0xf63660 = json.Number -> strconv.ParseInt(string(v),10,64)
//	0xf88732b8 / 0xee7100 = string      -> strconv.ParseInt(v,10,64)
//
// 其余（含 nil）n=0；n<500 返回 3s（0xb2d05e00 ns），否则 min(n,30000)（0x1f4/0x7530）×1ms（0xf4240 ns）。
// 注意 json.Number 那支是 encoding/json.Number（类型名 json.Number），不是 jsoniter.Number。
func parseQRInterval(value interface{}) time.Duration {
	var n int64
	switch v := value.(type) {
	case float64:
		n = int64(v)
	case stdjson.Number:
		n, _ = strconv.ParseInt(v.String(), 10, 64)
	case string:
		n, _ = strconv.ParseInt(v, 10, 64)
	default:
		n = 0
	}
	if n < 500 {
		return 3 * time.Second
	}
	if n > 30000 {
		n = 30000
	}
	return time.Duration(n) * time.Millisecond
}

// createQRCode 生成扫码登录二维码（成品只有 1 处 get，不下载图片字节）。
//
// 成品证据（(*weiboProtocol).createQRCode @protocol.go:880，0xc5e280，2304B）：
//   - url.Parse(p.endpoints.QRImage) + Query()，Set entry=miniblog(@0x109a915/@0x10a4bbe)、
//     size=180(@0x109960f/@0x1098899)、callback=uniqueJSONPCallback(+0x52)、
//     _=strconv.FormatInt(time.Now().UnixMilli(),10)（+0x242/+0x2a0）；
//   - Values.Encode + URL.String -> +0x3c0 唯一 get(target=该 URL, Referer=Home, Accept="*/*")；
//   - +0x417 parseJSONP -> qrImageResponse；retcode!=20000000 报错(@0x10d9b91 len46)；
//   - +0x56a resolveProtocolURL(base=p.endpoints.QRImage, ref=resp.Data.Image)；
//   - url.Parse(resolved) 后取 Query().Get("data")(@0x109953f len4)；
//     Qrid 为空或 data 为空 -> "微博二维码响应格式已变化，未找到 qrid 或扫码 URL"(@0x10e8dae len67)；
//   - +0x687 parseQRInterval(resp.Data.Interval) -> time.Duration；
//   - 返回聚合 weiboQRCode{Qrid, ImageURL, Data, Interval} + error（7 词 + error，
//     与调用点把前 8 词压栈传给 waitForQRCode 的证据一致）。
func (p *weiboProtocol) createQRCode(ctx context.Context) (weiboQRCode, error) {
	imageEndpoint, _ := url.Parse(p.endpoints.QRImage)
	callback := uniqueJSONPCallback()
	params := imageEndpoint.Query()
	params.Set("entry", "miniblog")
	params.Set("size", "180")
	params.Set("callback", callback)
	params.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))
	imageEndpoint.RawQuery = params.Encode()
	resp, err := p.get(ctx, imageEndpoint.String(), p.endpoints.Home, acceptAny)
	if err != nil {
		return weiboQRCode{}, err
	}
	var qrResp qrImageResponse
	if err := parseJSONP(resp.Text, callback, &qrResp); err != nil {
		return weiboQRCode{}, err
	}
	if qrResp.Retcode != 20000000 {
		return weiboQRCode{}, fmt.Errorf("微博二维码生成失败: retcode=%d msg=%s", qrResp.Retcode, qrResp.Msg)
	}
	imageURL, err := resolveProtocolURL(p.endpoints.QRImage, qrResp.Data.Image)
	if err != nil {
		return weiboQRCode{}, err
	}
	parsed, err := url.Parse(imageURL)
	if err != nil {
		return weiboQRCode{}, err
	}
	data := ""
	if parsed != nil {
		data = parsed.Query().Get("data")
	}
	if qrResp.Data.QRID == "" || data == "" {
		return weiboQRCode{}, errors.New("微博二维码响应格式已变化，未找到 qrid 或扫码 URL")
	}
	return weiboQRCode{
		Qrid:     qrResp.Data.QRID,
		ImageURL: imageURL,
		Data:     data,
		Interval: parseQRInterval(qrResp.Data.Interval),
	}, nil
}

// checkQRCode 轮询扫码状态；把 JSONP 响应解析成 qrCheckResponse 后原样返回，
// 不做状态分类、不校验 alt（分类与错误文案全在 waitForQRCode）。
//
// 成品证据（(*weiboProtocol).checkQRCode @protocol.go:920，0xc5eb80，1184B）：
//   - url.Parse(p.endpoints.QRCheck) + Query() 依次 Set 四个参数：
//     entry=miniblog（键 @0x109a915 len5，值 @0x10a4bbe len8）、qrid（键 @0x1099677 len4）、
//     callback=uniqueJSONPCallback()（键 @0x10a4bc6 len8）、
//     "_"=strconv.FormatInt(time.Now().UnixMilli(),10)（键 @0x11240d8 len1；+0x237 time.Now、
//     +0x242 UnixMilli、+0x2a0 FormatInt）；**没有** url 参数。
//     Values.Encode(@0x5c3540) + URL.String(@0x5c1ea0) 后唯一一次 p.get
//     (target=该 URL, Referer=p.endpoints.Home, Accept="*/*")。
//   - p.get 返回 error（+0x3b3 test rbx,rbx; jne）立即原样返回。
//   - +0x3d5 读 resp.Text(offset 0x38/0x40) 交给 parseJSONP(@0xc5bec0，dst @0xfcb9c0)，
//     失败（+0x400 test rax,rax）立即返回 error，**没有** json.Unmarshal 兜底分支。
//   - 成功从堆对象 @0x88 载入 4 词（c5ef9c..c5efb8）作为 qrCheckResponse 返回，error 清零。
//   - 函数内无任何 0x1312d00(20000000) / 0x2fcaddx(5011400x) 比较，也无 403/432 状态判断。
func (p *weiboProtocol) checkQRCode(ctx context.Context, qrid string) (qrCheckResponse, error) {
	checkURL, _ := url.Parse(p.endpoints.QRCheck)
	callback := uniqueJSONPCallback()
	params := checkURL.Query()
	params.Set("entry", "miniblog")
	params.Set("qrid", qrid)
	params.Set("callback", callback)
	params.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))
	checkURL.RawQuery = params.Encode()
	resp, err := p.get(ctx, checkURL.String(), p.endpoints.Home, acceptAny)
	if err != nil {
		return qrCheckResponse{}, err
	}
	var checkResp qrCheckResponse
	if err := parseJSONP(resp.Text, callback, &checkResp); err != nil {
		return qrCheckResponse{}, err
	}
	return checkResp, nil
}

// waitForQRCode 轮询等待指定二维码被扫码确认，返回 checkQRCode 的状态聚合。
//
// 成品签名证据（(*weiboProtocol).waitForQRCode @protocol.go:959，0xc5f020，1344B）：
//   - 4 个寄存器参数：RAX=p、RBX/RCX=ctx interface、RDI=回调 funcval
//     （RunQRLogin +0xd45 传 &0x111d8f8 → 0xc69cc0 = RunQRLogin.func2，qrlogin.go:104）；
//   - 第 5 个参数是 0x40 字节聚合，整体走栈：RunQRLogin 把 createQRCode 的返回（weiboQRCode）
//     拷到 outgoing arg 区；prologue 从 0x110/0x118 读 Qrid、从 0x140 读 Interval(offset 0x30)；
//   - 间隔：mov $0xb2d05e00,%r8d; cmovle → 入参 <=0 取 3s；callee 不读任何 cfg；
//   - 返回「4 词聚合 + error」：成功路径 c5f2a5 只把 retcode 移回 RAX、清 RSI/R8（error=nil），
//     RBX/RCX/RDI 保留 checkQRCode 的 Msg/Data；错误路径 c5f4f8 只清 RAX/RBX/RCX/RDI、
//     保留 RSI/R8（error 原样上抛）。
//
// 控制流（与重建旧稿的关键差异）：
//  1. checkQRCode 的任何 error（c5f0c0 test rsi,rsi; jne 0xc5f4f8）都立即原样返回，
//     不存在「非 sentinel 继续轮询到最后超时」的路径；
//  2. retcode 分类在本函数（c5f140 起），且在回调之后：c5f0e0 cmp prev,retcode; je 跳过，
//     retcode 变化时先调用 RDI 回调（c5f102，第二参=Msg），再进入 c5f140 分类；
//  3. 终止态 50114003/50114004/50114013-15/50114018 先回调、再返回
//     fmt.Errorf("微博扫码登录停止: retcode=%d status=%s msg=%s")（@0x10dfe12 len53，
//     第 2 参 = qrStatusMessage(retcode)）；
//  4. 其余未知 retcode 先回调、再返回
//     fmt.Errorf("微博扫码返回未知状态: retcode=%d msg=%s")（@0x10dcd82 len49）；
//  5. 20000000 先回调、再返回该聚合与 nil；50114001/50114002 回调后继续轮询；
//  6. ctx.Done 时返回 fmt.Errorf("微博扫码登录等待超时: %w", ctx.Err())（@0x10ca574 len34），
//     不再包装上一次轮询 error。
func (p *weiboProtocol) waitForQRCode(ctx context.Context, onStatus func(retcode int64, msg string), qr weiboQRCode) (qrCheckResponse, error) {
	interval := qr.Interval
	if interval <= 0 {
		interval = defaultQRCheckInterval
	}
	prev := int64(-1)
	for {
		resp, err := p.checkQRCode(ctx, qr.Qrid)
		if err != nil {
			return qrCheckResponse{}, err
		}
		if resp.Retcode != prev {
			// 成品仅在 retcode 变化时回调（c5f0e0: cmp %r9,%rax; je 跳过），
			// 第二参传 checkQRCode 的 Msg（c5f102 的 rbx/rcx）；回调不使用该参，
			// 文本由回调内部 qrStatusMessage(retcode) 生成。
			prev = resp.Retcode
			if onStatus != nil {
				onStatus(resp.Retcode, resp.Msg)
			}
		}
		switch resp.Retcode {
		case 20000000:
			return resp, nil
		case 50114001, 50114002:
			// 等待扫码 / 已扫码等待手机端确认：继续轮询。
		case 50114003, 50114004, 50114013, 50114014, 50114015, 50114018:
			return qrCheckResponse{}, fmt.Errorf("微博扫码登录停止: retcode=%d status=%s msg=%s",
				resp.Retcode, qrStatusMessage(resp.Retcode), resp.Msg)
		default:
			return qrCheckResponse{}, fmt.Errorf("微博扫码返回未知状态: retcode=%d msg=%s", resp.Retcode, resp.Msg)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return qrCheckResponse{}, fmt.Errorf("微博扫码登录等待超时: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

// completeQRLogin 以 alt 完成扫码登录：先 navigate(QRLogin+params, Home, 24)，
// 再 get(Home, Referer=navigationResult.FinalURL, HTML-31)，返回 navigate 的结果。
//
// 成品证据（(*weiboProtocol).completeQRLogin @protocol.go:1008，0xc5f560）：
//   - 首行 test rsi,rsi; je -> alt 为空时报 "微博扫码成功响应未包含 alt"(@0x10cedee len37)；
//   - url.Parse(p.endpoints.QRLogin) + Query()，Set entry=miniblog、
//     return_type=CROSSDOMAIN_BY_LOCATION(@0x10a77d7 len10 / @0x10b966c len23)、
//     alt=入参、url=p.endpoints.Home(@0x10987f4 len3)；+0x2c1 Values.Encode、+0x2f3 URL.String；
//   - +0x327 navigate(ctx, url, referer=p.endpoints.Home, max=0x18=24)；
//   - +0x36e get(ctx, target=p.endpoints.Home, referer=navResult.FinalURL(0x20/0x28), Accept=acceptHTML)；
//   - 返回 (*navigationResult, error)（成功时 rax=navResult）。
func (p *weiboProtocol) completeQRLogin(ctx context.Context, alt string) (*navigationResult, error) {
	if alt == "" {
		return nil, errors.New("微博扫码成功响应未包含 alt")
	}
	loginURL, _ := url.Parse(p.endpoints.QRLogin)
	params := loginURL.Query()
	params.Set("entry", "miniblog")
	params.Set("return_type", crossDomainByLocation)
	params.Set("alt", alt)
	params.Set("url", p.endpoints.Home)
	loginURL.RawQuery = params.Encode()
	nav, err := p.navigate(ctx, loginURL.String(), p.endpoints.Home, defaultNavigationMax)
	if err != nil {
		return nil, err
	}
	if _, err := p.get(ctx, p.endpoints.Home, nav.FinalURL, acceptHTML); err != nil {
		return nil, err
	}
	return nav, nil
}

// spaConfig 请求 getSpaConfig 检查登录状态
func (p *weiboProtocol) spaConfig(ctx context.Context) ([]byte, error) {
	// 成品 spaConfig（0xc5f980）：+0x362 直接调用 (*weiboProtocol).request(GET, p.endpoints.SpaConfig,
	// headers, nil)，不经 get。headers 三键按序构造：
	//   Accept        = "application/json, text/plain, */*"（@0x10c8953 len 33，+0xa9 CanonicalMIMEHeaderKey
	//                   "Accept" @0x109f6a6 后写入）
	//   Referer       = p.endpoints.Home（weiboProtocol.endpoints 起始于 p+0x20，故 Home=p+0x20/0x28，
	//                   +0x163 写入；键 @0x10a30db len 7）
	//   X-XSRF-TOKEN  = cookieValueForURL(p.jar, p.endpoints.SpaConfig, "XSRF-TOKEN")（+0x222 调用，
	//                   键 @0x10aa230 len 12、cookie 名 @0x10a77a5 len 10；返回长度非 0 才写入，+0x395 test rbx,rbx）
	headers := map[string]string{
		"Accept":  acceptJSON,
		"Referer": p.endpoints.Home,
	}
	if token := cookieValueForURL(p.jar, p.endpoints.SpaConfig, "XSRF-TOKEN"); token != "" {
		headers["X-XSRF-TOKEN"] = token
	}
	resp, err := p.request(ctx, http.MethodGet, p.endpoints.SpaConfig, headers, nil)
	if err != nil {
		return nil, err
	}
	body := resp.Body
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		status := 0
		if resp != nil {
			status = resp.Status
		}
		return nil, fmt.Errorf("微博 getSpaConfig 返回非 JSON（HTTP %d）", status)
	}
	return body, nil
}

// spaConfigLoggedIn 判断 getSpaConfig 响应中的登录状态
func spaConfigLoggedIn(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	var payload struct {
		Login    interface{} `json:"login"`
		User     *userJSON   `json:"user"`
		UserInfo *userJSON   `json:"userInfo"`
		Data     *struct {
			Login    interface{} `json:"login"`
			User     *userJSON   `json:"user"`
			UserInfo *userJSON   `json:"userInfo"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	if payload.Data != nil {
		if protocolTruthy(payload.Data.Login) || payload.Data.User != nil || payload.Data.UserInfo != nil {
			return true
		}
	}
	if protocolTruthy(payload.Login) {
		return true
	}
	return payload.User != nil || payload.UserInfo != nil
}

// protocolTruthy 将 JSON 动态类型的登录标记转为布尔值
func protocolTruthy(value interface{}) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return false
		}
		if n, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return n != 0
		}
		switch strings.ToLower(trimmed) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
		return true
	case stdjson.Number:
		n, err := v.Float64()
		return err == nil && n != 0
	case float64:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	default:
		return false
	}
}

// refreshLoginCookies 微博 Cookie 心跳：请求心跳接口捕获 Set-Cookie 轮换，并以 getSpaConfig 验证登录状态
func (p *weiboProtocol) refreshLoginCookies(ctx context.Context) error {
	// 1. 心跳请求：容忍失败，登录状态以 getSpaConfig 为准
	// 成品 refreshLoginCookies（0xc60240）：+0x3a1 直接调用 request(GET, p.endpoints.Heartbeat)，
	// headers 与 spaConfig 同构：Accept=acceptJSON、Referer=p.endpoints.Home（p+0x20/0x28）、
	// X-XSRF-TOKEN=cookieValueForURL(p.jar, p.endpoints.Heartbeat, "XSRF-TOKEN")
	// （Heartbeat 是 endpoints 第 8 个字段 = p+0x90/0x98；返回串非空才写入）。
	headers := map[string]string{
		"Accept":  acceptJSON,
		"Referer": p.endpoints.Home,
	}
	if token := cookieValueForURL(p.jar, p.endpoints.Heartbeat, "XSRF-TOKEN"); token != "" {
		headers["X-XSRF-TOKEN"] = token
	}
	_, heartbeatErr := p.request(ctx, http.MethodGet, p.endpoints.Heartbeat, headers, nil)
	if heartbeatErr != nil {
		logger.Warnf("微博 Cookie 心跳请求异常，将以 getSpaConfig 的登录状态为准: %v", heartbeatErr)
	}
	// 2. 登录状态检查
	body, err := p.spaConfig(ctx)
	if err != nil {
		return fmt.Errorf("微博 Cookie 心跳失败: %v；登录状态检查失败: %w", heartbeatErr, err)
	}
	if !spaConfigLoggedIn(body) {
		return errors.New("微博登录状态检查未通过")
	}
	// 3. 登录有效：确认 SUB 存在并持久化轮换后的 Cookie
	weiboURL, _ := url.Parse("https://weibo.com/")
	if cookieValueForURL(p.jar, weiboURL.String(), "SUB") == "" {
		subErr := errors.New("微博登录状态有效，但 Cookie Jar 中未找到 SUB")
		p.jar.setConfiguredSUB()
		if cookieValueForURL(p.jar, weiboURL.String(), "SUB") == "" {
			return subErr
		}
	}
	_, loginPath := currentWeiboStatePaths()
	stateDir := filepath.Dir(loginPath)
	if err := saveCookieState(loginPath, p.userAgent, p.jar.snapshot()); err != nil {
		logger.Warnf("写入微博登录 Cookie 状态失败: %v", err)
	}
	meta, metaErr := loadCookieMeta(stateDir)
	if metaErr != nil {
		logger.Warnf("%v", metaErr)
		meta = new(weiboCookieMeta)
	}
	meta.LoginUpdatedAt = time.Now()
	if err := saveCookieMeta(stateDir, meta); err != nil {
		logger.Warnf("写入微博登录 Cookie 元信息失败: %v", err)
	}
	// 登录态此前被判失效过时，提示订阅将恢复正常；与心跳完成日志不重复
	if !cookieHealthy.Load() {
		logger.Info("✓ Cookie已自动恢复，订阅推送将正常工作")
	}
	logger.Info("微博登录 Cookie 心跳完成，账号登录仍有效")
	return nil
}

// cookieValueForURL 取指定 URL 下名称为 name 的 Cookie 值
func cookieValueForURL(jar *persistentCookieJar, rawURL string, name string) string {
	if jar == nil || rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	for _, cookie := range jar.Cookies(u) {
		if cookie.Name == name && cookie.Value != "" {
			return cookie.Value
		}
	}
	return ""
}

// cookieNamesForURL 列出指定 URL 下可用的 Cookie 名称（日志中不输出值）
func cookieNamesForURL(jar *persistentCookieJar, rawURL string) []string {
	if jar == nil || rawURL == "" {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil
	}
	return cookieNamesOnly(jar.Cookies(u))
}

// cookieNamesOnly 提取 Cookie 名称列表，保持 resp.Cookies() 的原始顺序。
// 成品证据：request 内联构建 SetCookieNames 时不排序（见 newProtocolResponse 注释）。
func cookieNamesOnly(cookies []*http.Cookie) []string {
	names := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie != nil && cookie.Name != "" {
			names = append(names, cookie.Name)
		}
	}
	return names
}

// cookieRecordKey Cookie 记录键：name/domain/path 唯一确定一条记录
func cookieRecordKey(name, domain, path string) string {
	return strings.Join([]string{name, domain, path}, "|")
}

// qrStatusMessage 扫码状态码的可读描述。
// 成品无独立符号（已内联进 RunQRLogin.func2 0xc69cc0 的 c69dc8 起、以及
// waitForQRCode 0xc5f020 的 c5f140 起）。按 retcode 常量分支载入 rodata 字面量，
// convTstring 写入 "status" 字段；分支与字面量如下（len 为编译期长度）：
//
//	20000000           -> 0x10b6a1a len 21 "手机端确认成功"
//	50114001           -> 0x10aa218 len 12 "等待扫码"
//	50114002           -> 0x10cd52f len 36 "已扫码，等待在手机端确认"
//	50114003           -> 0x10b2500 len 18 "二维码已过期"
//	50114004           -> 0x10b2512 len 18 "二维码已使用"
//	50114013/14/15/18  -> 0x10e314a len 57 "扫码登录被服务端停止（异常或风控状态）"
//	default            -> 0x10aa224 len 12 "未知状态"
func qrStatusMessage(retcode int64) string {
	switch retcode {
	case 20000000:
		return "手机端确认成功"
	case 50114001:
		return "等待扫码"
	case 50114002:
		return "已扫码，等待在手机端确认"
	case 50114003:
		return "二维码已过期"
	case 50114004:
		return "二维码已使用"
	case 50114013, 50114014, 50114015, 50114018:
		return "扫码登录被服务端停止（异常或风控状态）"
	default:
		return "未知状态"
	}
}

// isWeiboLoginDomain 判断是否为需要携带登录 Cookie 的微博域
func isWeiboLoginDomain(host string) bool {
	host = strings.ToLower(strings.TrimPrefix(host, "www."))
	switch host {
	case "weibo.com", "weibo.cn", "m.weibo.cn", "passport.weibo.com", "login.sina.com.cn":
		return true
	}
	return strings.HasSuffix(host, ".weibo.com") || strings.HasSuffix(host, ".weibo.cn") ||
		strings.HasSuffix(host, ".sina.com.cn")
}

// originOf 返回 URL 的 scheme://host 部分
func originOf(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// firstNonEmpty 返回第一个非空字符串
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// mustJSON 序列化为 JSON 字符串（失败时返回空串，仅用于调试日志）
func mustJSON(value interface{}) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

// weiboRequestTimeout 单次协议请求超时；成品走 cfg.GetWeiboRequestTimeout（含钳制）。
func weiboRequestTimeout() time.Duration {
	return cfg.GetWeiboRequestTimeout()
}

// weiboQRLoginTimeout 扫码登录整体等待超时；成品走 cfg.GetWeiboQRLoginTimeout（含钳制）。
func weiboQRLoginTimeout() time.Duration {
	return cfg.GetWeiboQRLoginTimeout()
}

// weiboProtocolContext 为一个协议步骤创建受 weibo.protocolTimeout 约束的 context。
// 成品证据：FreshCookieGuest(freshCookie.go:73)、FreshCookieLogin(freshCookie.go:126)、
// RunQRLogin(qrlogin.go:30)、refreshWeiboCookieFromCookieCloud(cookiecloud.go:49)
// 均先 context.WithTimeout(context.Background(), cfg.GetWeiboProtocolTimeout())（0x533f00）
// 再进入协议方法。
func weiboProtocolContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), cfg.GetWeiboProtocolTimeout())
}

// weiboRequestContext 为单次协议请求创建受 weibo.requestTimeout 约束的 context。
// 成品证据：RunQRLogin(qrlogin.go:30) +0x665 先 cfg.GetWeiboRequestTimeout()，
// 再 context.WithTimeout(background, d)（+0x680），随后 +0x6a0 调 createQRCode。
func weiboRequestContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), weiboRequestTimeout())
}

// weiboQRLoginContext 为扫码轮询创建受 weibo.qrloginTimeout 约束的 context。
// 成品证据：RunQRLogin(@qrlogin.go:30) +0xcc5 先 cfg.GetWeiboQRLoginTimeout()（0xae8b20），
// 再 context.WithTimeout(background, d)（0x533f00）后把它传给 waitForQRCode；
// waitForQRCode 自身不读任何配置。
func weiboQRLoginContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), weiboQRLoginTimeout())
}

// maskSensitive 日志中隐藏敏感值主体
func maskSensitive(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "[REDACTED]"
	}
	return value[:4] + "***[REDACTED]"
}
