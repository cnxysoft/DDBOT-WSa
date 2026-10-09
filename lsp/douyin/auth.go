package douyin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/authprompt"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/sirupsen/logrus"
)

// Cookie 来源标签（日志 cookie_source 取值）。
// 成品 initializeDouyinAuth 内四个字面量的使用点（objdump 逐条取证，均为 .rodata 原文）：
//   - 手填完整 Cookie 分支 @0xd68fde：lea 0x10af535,len16 = "application_yaml"；
//   - 状态文件（扫码保存）分支 @0xd69118/@0xd6930e/@0xd693b2：
//     lea 0x10a7679,len10 = "state_file"；
//   - 旧版三字段分支 @0xd69b25：lea 0x10b3937,len19 = "legacy_three_fields"；
//   - 无 Cookie（内置默认/待扫码）@0xd68e0d：lea 0x10af555,len16 = "built_in_default"。
//
// 注意："configured_in_yaml"（0x10b23f2）在成品里只作 logrus Fields 的键
// （@0xd69c7a mapassign_faststr，字段名），并不是 cookie_source 的取值，故配置来源
// 的标签应为 application_yaml 而非 configured_in_yaml。
const (
	douyinCookieSourceConfigured = "application_yaml"
	douyinCookieSourceStateFile  = "state_file"
	douyinCookieSourceLegacy     = "legacy_three_fields"
	douyinCookieSourceDefault    = "built_in_default"
)

// 登录会话 Cookie 名称：包含其中之一才算持有登录态。
const (
	douyinCookieSessionID   = "sessionid"
	douyinCookieSessionIDSS = "sessionid_ss"
	douyinCookieSidGuard    = "sid_guard"
)

// 旧版三字段 Cookie 名称（仅兼容旧配置）。
const (
	douyinCookieAcSignature = "__ac_signature"
	douyinCookieAcNonce     = "__ac_nonce"
)

// 内置稳定 UA（与内置浏览器版本 Chrome/143 一致）。
const douyinBuiltInUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36"

// douyinDefaultStateFile 是扫码取得的完整 Cookie Jar 默认保存路径。
const douyinDefaultStateFile = ".douyin-state/cookies.json"

// douyinStateFilePerm / douyinStateDirPerm：状态文件仅当前用户可读。
const (
	douyinStateFilePerm = 0600
	douyinStateDirPerm  = 0700
)

var (
	douyinJarMutex       sync.RWMutex
	douyinJar            *cookiejar.Jar
	douyinUAMutex        sync.RWMutex
	douyinUserAgentValue = douyinBuiltInUserAgent
	douyinMsTokenMutex   sync.RWMutex
	douyinMsTokenValue   string
)

// douyinStateCookie 是状态文件中的单个 Cookie。
type douyinStateCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain,omitempty"`
	Path     string    `json:"path,omitempty"`
	Expires  time.Time `json:"expires,omitempty"`
	Secure   bool      `json:"secure,omitempty"`
	HttpOnly bool      `json:"http_only,omitempty"`
}

// douyinCookieState 是扫码登录保存的完整 Cookie 状态文件结构。
type douyinCookieState struct {
	Version   int                 `json:"version"`
	UpdatedAt time.Time           `json:"updated_at"`
	UserAgent string              `json:"user_agent"`
	Cookies   []douyinStateCookie `json:"cookies"`
}

// httpCookies 把状态文件 Cookie 转换为标准 Cookie 列表。
func (s douyinCookieState) httpCookies() []*http.Cookie {
	result := make([]*http.Cookie, 0, len(s.Cookies))
	for i := range s.Cookies {
		sc := &s.Cookies[i]
		c := &http.Cookie{
			Name:     sc.Name,
			Value:    sc.Value,
			Domain:   sc.Domain,
			Path:     sc.Path,
			Secure:   sc.Secure,
			HttpOnly: sc.HttpOnly,
		}
		if !sc.Expires.IsZero() {
			c.Expires = sc.Expires
		}
		result = append(result, c)
	}
	return result
}

// getDouyinStateFile 返回扫码 Cookie 状态文件路径。
func getDouyinStateFile() string {
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetString("douyin.stateFile"); v != "" {
			return v
		}
	}
	return douyinDefaultStateFile
}

// getDouyinRequestTimeout 返回单次 HTTP 请求超时（默认15s，限制在5s到2m）。
func getDouyinRequestTimeout() time.Duration {
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetDuration("douyin.requestTimeout"); v > 0 {
			if v < 5*time.Second {
				return 5 * time.Second
			}
			if v > 2*time.Minute {
				return 2 * time.Minute
			}
			return v
		}
	}
	return 15 * time.Second
}

// getDouyinQRLoginTimeout 返回扫码轮询总时限（默认3m，限制在30s到10m）。
func getDouyinQRLoginTimeout() time.Duration {
	if config.GlobalConfig != nil {
		if v := config.GlobalConfig.GetDuration("douyin.qrloginTimeout"); v > 0 {
			if v < 30*time.Second {
				return 30 * time.Second
			}
			if v > 10*time.Minute {
				return 10 * time.Minute
			}
			return v
		}
	}
	return 3 * time.Minute
}

// isDouyinQRLoginEnabled 返回是否允许管理员确认生成二维码（默认 true）。
func isDouyinQRLoginEnabled() bool {
	if config.GlobalConfig != nil {
		return config.GlobalConfig.GetBool("douyin.qrlogin")
	}
	return true
}

// configuredDouyinCookieHeader 按优先级读取手填完整 Cookie：
// douyin.cookie -> douyin.cookies -> douyin.cookieRaw。
func configuredDouyinCookieHeader() string {
	for _, key := range []string{"douyin.cookie", "douyin.cookies", "douyin.cookieRaw"} {
		if config.GlobalConfig != nil {
			if v := strings.TrimSpace(config.GlobalConfig.GetString(key)); v != "" {
				return v
			}
		}
	}
	return ""
}

// normalizeCookieHeader 规整手填 Cookie 串：去掉 Cookie: 前缀与换行。
func normalizeCookieHeader(header string) string {
	header = strings.TrimSpace(header)
	header = strings.TrimPrefix(header, "Cookie:")
	header = strings.TrimPrefix(header, "cookie:")
	header = strings.ReplaceAll(header, "\r\n", " ")
	header = strings.ReplaceAll(header, "\n", " ")
	return strings.TrimSpace(header)
}

// parseCookieHeader 解析 "k=v; k2=v2" 形式的 Cookie 串。
func parseCookieHeader(header string) []*http.Cookie {
	var result []*http.Cookie
	for _, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := strings.Index(part, "=")
		if idx <= 0 {
			continue
		}
		name := strings.TrimSpace(part[:idx])
		value := strings.TrimSpace(part[idx+1:])
		if name == "" {
			continue
		}
		result = append(result, &http.Cookie{Name: name, Value: value})
	}
	return result
}

// legacyDouyinCookieHeader 将旧版三字段配置拼成 Cookie 串（无值字段返回空串）。
func legacyDouyinCookieHeader() string {
	if config.GlobalConfig == nil {
		return ""
	}
	acSignature := strings.TrimSpace(config.GlobalConfig.GetString("douyin.acSignature"))
	acNonce := strings.TrimSpace(config.GlobalConfig.GetString("douyin.acNonce"))
	sessionID := strings.TrimSpace(config.GlobalConfig.GetString("douyin.sessionId"))
	if acSignature == "" && acNonce == "" && sessionID == "" {
		return ""
	}
	var parts []string
	if sessionID != "" {
		parts = append(parts, douyinCookieSessionID+"="+sessionID)
	}
	if acSignature != "" {
		parts = append(parts, douyinCookieAcSignature+"="+acSignature)
	}
	if acNonce != "" {
		parts = append(parts, douyinCookieAcNonce+"="+acNonce)
	}
	return strings.Join(parts, "; ")
}

// initializeDouyinAuth 初始化抖音登录态：
// Cookie 来源按 手填完整 Cookie > 状态文件 > 旧版三字段 > 空(待扫码) 选择，来源之间不混用。
func initializeDouyinAuth() (bool, error) {
	if ua := strings.TrimSpace(config.GlobalConfig.GetString("douyin.userAgent")); ua != "" {
		setDouyinActiveUserAgent(cleanDouyinUserAgent(ua))
	} else {
		setDouyinActiveUserAgent(douyinBuiltInUserAgent)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		return false, fmt.Errorf("初始化抖音 Cookie Jar 失败: %w", err)
	}

	// cookie_source 是函数内局部变量：成品 initializeDouyinAuth 把四个来源字面量
	// （application_yaml @0xd68fde、state_file @0xd69118、legacy_three_fields @0xd69b25、
	// built_in_default @0xd68e0d）的指针/长度直接写入栈槽（0xa0/0x50(rsp)），
	// 不写包级全局；此处按成品形态改用局部变量。
	source := douyinCookieSourceDefault
	hasCookie := false
	if header := configuredDouyinCookieHeader(); header != "" {
		cookies := parseCookieHeader(normalizeCookieHeader(header))
		if err := setDouyinJarCookies(jar, cookies); err != nil {
			return false, fmt.Errorf("解析抖音 Cookie 失败: %w", err)
		}
		source = douyinCookieSourceConfigured
		logger.Info("抖音登录模式：完整 Cookie")
		hasCookie = true
		if legacyDouyinCookieHeader() != "" {
			logger.Warn("检测到旧版抖音三字段配置，已有更高优先级 Cookie，旧配置已忽略")
		}
	} else if state, stateErr := loadDouyinCookieState(); stateErr == nil && len(state.Cookies) > 0 {
		if err := setDouyinJarCookies(jar, state.httpCookies()); err != nil {
			return false, fmt.Errorf("解析抖音 Cookie 状态失败: %w", err)
		}
		if state.UserAgent != "" {
			setDouyinActiveUserAgent(cleanDouyinUserAgent(state.UserAgent))
		}
		source = douyinCookieSourceStateFile
		logger.Info("抖音登录模式：已保存的扫码 Cookie")
		hasCookie = true
	} else if stateErr != nil && !errors.Is(stateErr, os.ErrNotExist) {
		logger.WithError(stateErr).Warnf("读取抖音 Cookie 状态文件失败")
	} else if legacy := legacyDouyinCookieHeader(); legacy != "" {
		if err := setDouyinJarCookies(jar, parseCookieHeader(legacy)); err != nil {
			return false, fmt.Errorf("解析旧版抖音三字段失败: %w", err)
		}
		source = douyinCookieSourceLegacy
		logger.Warn("检测到旧版抖音三字段 Cookie 配置，建议改用 douyin.cookie 完整 Cookie 或扫码登录")
		hasCookie = true
	}

	// 成品 initializeDouyinAuth callees 中没有 loadDouyinCookieCloudConfig，也没有
	// cfg.*CookieCloud* getter：CookieCloud 配置完整性检查不在本函数，而在
	// (*Concern).Start（成品 d73ac5 → d73ce7/d73b7e）。这里只落 Jar 并输出就绪日志。
	setCurrentDouyinCookieJar(jar)

	// 成品 @auth.go:118（5376B）末尾 mode 分支逐条证据（offsets 相对函数入口）：
	//   - +0x1160 读一次 cfg.GetDouyinCookieMode()（call 0xae7f20），结果只与
	//     "cookiecloud"（内存比较 rodata 0x...→"cookiecloud",len11）比较：
	//       · mode==cookiecloud 且 !hasCookie（+0x11a6 je 0x131e）→ 直接 (false,nil)；
	//       · mode==cookiecloud 且 hasCookie → Info("CookieCloud 首次获取成功前，将临时使用
	//         本地抖音Cookie"，@0x1125de0,len69；field cookie_source=source)，
	//         再调 logDouyinAuthReadiness(source, jar)（call site +0x1314）后 (true,nil)。
	//   - +0x132b 再读一次 cfg.GetDouyinCookieMode()，只与 "qrcode"（len6）比较；
	//     非 qrcode → Warn("未知的抖音 Cookie来源模式，已回退到默认扫码模式"，
	//     @0x1125df0,len67；field cookie_mode=mode)。随后与 qrcode 路径合并到 +0x14cd：
	//       · hasCookie → logDouyinAuthReadiness(source, jar)（+0x14cd）后 (true,nil)；
	//       · !hasCookie → 调 isDouyinQRLoginEnabled()（+0x1487）并丢弃返回值，
	//         两条出口（+0x1490/+0x149f）都返回 (false, nil)。
	// 注：cfg.GetDouyinCookieMode（成品 0xae7f20）只做 lower(trim(getString))，空值默认
	// "qrcode"，非法值原样返回；重建的 lsp/cfg.getCookieMode 会把非法值回退成 "qrcode" 并
	// 在 cfg 侧告警，故下方 cookie_mode 分支在重建里通常不可达（保留以对齐成品行为）。
	if cfg.GetDouyinCookieMode() == authprompt.ModeCookieCloud {
		if !hasCookie {
			return false, nil
		}
		logger.WithField("cookie_source", source).
			Info("CookieCloud 首次获取成功前，将临时使用本地抖音Cookie")
		logDouyinAuthReadiness(source, jar)
		return true, nil
	}
	if mode := cfg.GetDouyinCookieMode(); mode != authprompt.ModeQrcode {
		logger.WithField("cookie_mode", mode).
			Warn("未知的抖音 Cookie来源模式，已回退到默认扫码模式")
	}
	if hasCookie {
		logDouyinAuthReadiness(source, jar)
		return true, nil
	}
	// 成品此处调用 isDouyinQRLoginEnabled() 但两条出口返回一致，返回值被丢弃。
	isDouyinQRLoginEnabled()
	return false, nil
}

// setCurrentDouyinCookieJar 设置当前生效的 Cookie Jar。
func setCurrentDouyinCookieJar(jar *cookiejar.Jar) {
	douyinJarMutex.Lock()
	defer douyinJarMutex.Unlock()
	douyinJar = jar
}

// currentDouyinCookieJar 返回当前生效的 Cookie Jar（可能为 nil）。
func currentDouyinCookieJar() *cookiejar.Jar {
	douyinJarMutex.RLock()
	defer douyinJarMutex.RUnlock()
	return douyinJar
}

// logDouyinAuthReadiness 输出登录态就绪日志（Cookie 来源、数量与名称，不输出值）。
//
// 成品 @0xd6a340（864B，auth.go:233）签名 func(source string, jar http.CookieJar)：
// 入口把 (rax,rbx) 存为 source 字符串、(rcx,rdi) 存为 jar 接口
// （0x1a0/0x1a8 = string、0x1b0/0x1b8 = interface），全程不再读当前全局 Jar；
// 调用点 initializeDouyinAuth @+0x1314/@+0x14cd 传 (source, jar)。
// 字段与文案逐项取证：
//   - "cookie_source"（0x10ab4b9,len13）= source 参数；
//   - "cookie_count"（0x10aa110,len12）= len(douyinCookiesForState(jar))；
//   - "cookie_names"（0x10aa11c,len12）= douyinCookieNames(douyinCookiesForState(jar))；
//   - "login_cookie_ready"（0x10b2404,len18）= hasDouyinLoginCookie(jar)；
//   - 分支只判定 hasDouyinLoginCookie：true → Info("抖音登录凭据检查通过"，
//     @0x1125e00,len30)；false → Warn("抖音 Cookie 中没有登录会话，直播状态可能可查，
//     但作品接口可能返回403" @0x1125e10,len95)。成品无 "抖音 Cookie Jar 为空" 分支，
//     也无 effective_user_agent 字段（旧的 active_cookie_source 字段名亦不在此处）。
func logDouyinAuthReadiness(source string, jar http.CookieJar) {
	cookies := douyinCookiesForState(jar)
	loginReady := hasDouyinLoginCookie(jar)
	log := logger.WithFields(logrus.Fields{
		"cookie_source":      source,
		"cookie_count":       len(cookies),
		"cookie_names":       douyinCookieNames(cookies),
		"login_cookie_ready": loginReady,
	})
	if loginReady {
		log.Info("抖音登录凭据检查通过")
		return
	}
	log.Warn("抖音 Cookie 中没有登录会话，直播状态可能可查，但作品接口可能返回403")
}

// setDouyinJarCookies 把 Cookie 写入 Jar，域名统一落到 .douyin.com。
func setDouyinJarCookies(jar http.CookieJar, cookies []*http.Cookie) error {
	if jar == nil {
		return errors.New("抖音 Cookie Jar 未初始化")
	}
	targetURL, err := url.Parse(BaseHost + "/")
	if err != nil {
		return err
	}
	normalized := make([]*http.Cookie, 0, len(cookies))
	for _, c := range cookies {
		if c == nil || c.Name == "" {
			continue
		}
		copied := *c
		if copied.Domain == "" {
			copied.Domain = ".douyin.com"
		}
		if copied.Path == "" {
			copied.Path = "/"
		}
		normalized = append(normalized, &copied)
	}
	jar.SetCookies(targetURL, normalized)
	return nil
}

// douyinCookiesForState 汇总 Jar 中 8 个 douyin URL 域内的全部 Cookie，去重排序后返回。
//
// 成品 @0xd6aa80（1824B，auth.go:275）逐条证据：
//   - 入口 test %rax,%rax; je ret → nil 接口返回零值切片（rax=rbx=rcx=0）；
//   - 8 个 URL 常量数组（栈 0x128 起，每项 16B 为 string header），顺序与内容为：
//     https://www.douyin.com/、.../aweme/v1/web/aweme/post/、
//     .../aweme/v1/web/aweme/detail/、.../aweme/v1/web/user/profile/other/、
//     https://live.douyin.com/、.../webcast/room/web/enter/、
//     .../webcast/user/profile/、https://login.douyin.com/（rodata
//     0x10b94e5/0x10dabf5/0x10dcc5c/0x10e18b7/0x10bae7d/0x10dac24/0x10d8a45/0x10bc4ae）；
//   - 循环体 call net/url.Parse（0xd6ac67，错误寄存器随后的 mov %rax,%rbx 覆盖即忽略），
//     再按 itab fun[0]（+0x18 = Cookies）调 jar.Cookies(u)；
//   - 对每条 Cookie 调 runtime.newobject（0xd6ace4）复制 0xb8 字节（net/http.Cookie 大小），
//     Path 为空（cmpq $0x0,0x30）置 "/"（0x28/0x30），Domain 为空（cmpq $0x0,0x40）
//     置 ".douyin.com"（0x38/0x40，rodata 0x10a8c04）；
//   - 去重表 map[string]*http.Cookie：key 由 runtime.concatstring5（0xd6ae47）拼成
//     Name + "\x00" + Domain + "\x00" + Path（分隔串 rodata@0x1124090 首字节 0x00），
//     mapassign_faststr 后把新对象指针写入槽位，同 key 后写覆盖；
//   - 收集 map key → sort.Strings（0xd6b060）→ 按 key 顺序 mapaccess1_faststr 取回
//     *http.Cookie 追加进 makeslice(t=elem8B, 0xd6b073) 的切片，故返回 []*http.Cookie。
func douyinCookiesForState(jar http.CookieJar) []*http.Cookie {
	if jar == nil {
		return nil
	}
	urls := []string{
		"https://www.douyin.com/",
		"https://www.douyin.com/aweme/v1/web/aweme/post/",
		"https://www.douyin.com/aweme/v1/web/aweme/detail/",
		"https://www.douyin.com/aweme/v1/web/user/profile/other/",
		"https://live.douyin.com/",
		"https://live.douyin.com/webcast/room/web/enter/",
		"https://live.douyin.com/webcast/user/profile/",
		"https://login.douyin.com/",
	}
	dedup := make(map[string]*http.Cookie)
	for _, raw := range urls {
		// 成品不检查 url.Parse 的 error（全部为合法常量 URL），此处保持一致。
		u, _ := url.Parse(raw)
		for _, c := range jar.Cookies(u) {
			copied := *c
			if copied.Path == "" {
				copied.Path = "/"
			}
			if copied.Domain == "" {
				copied.Domain = ".douyin.com"
			}
			dedup[copied.Name+douyinFingerprintFieldSeparator+
				copied.Domain+douyinFingerprintFieldSeparator+
				copied.Path] = &copied
		}
	}
	keys := make([]string, 0, len(dedup))
	for key := range dedup {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]*http.Cookie, 0, len(keys))
	for _, key := range keys {
		result = append(result, dedup[key])
	}
	return result
}

// douyinCookieHeaderFor 按请求地址的域名范围取出 Cookie 并拼成 "k=v; k2=v2"。
// 成品 0xd6b1a0 的调用面：resolveDouyinShortURL、douyinWebAPIRequestOptions、
// douyinMinimalLiveRequestOptions、douyinWebPageRequestOptions。
func douyinCookieHeaderFor(rawURL string) string {
	jar := currentDouyinCookieJar()
	u, err := url.Parse(rawURL)
	if jar == nil || err != nil {
		return ""
	}
	if !isDouyinDomain(u.Hostname()) {
		return ""
	}
	var parts []string
	for _, c := range jar.Cookies(u) {
		if c == nil || c.Name == "" {
			continue
		}
		parts = append(parts, c.Name+"="+c.Value)
	}
	return strings.Join(parts, "; ")
}

// activeDouyinFP 返回当前生效的浏览器指纹（成品 0xd6b680）：
// 先按 rawURL 的域名范围读取 Cookie s_v_web_id，以 verify_ 开头则直接使用，
// 否则回退到进程内一次生成并缓存的指纹（sharedDouyinFP，Once 闭包 0xd65500）。
func activeDouyinFP(rawURL string) string {
	if v := strings.TrimSpace(douyinCookieValueFor(rawURL, "s_v_web_id")); strings.HasPrefix(v, "verify_") {
		return v
	}
	return sharedDouyinFP()
}

// saveCurrentDouyinCookieState 把当前 Jar 保存到状态文件（原子替换）。
func saveCurrentDouyinCookieState() error {
	// 成品 @0xd6b700（1120B，auth.go:361）：先取当前 Jar 的 []*http.Cookie，
	// len==0 返回 errors.New("抖音 Cookie Jar 为空")（rodata 0x10bae95,len24）；
	// 再逐条转成 douyinStateCookie（0xd6b83e 起读 http.Cookie 的
	// Name 0x00 / Value 0x10 / Domain 0x38 / Path 0x28 / Expires 0x48 /
	// Secure 0x78 / HttpOnly 0x79）后 json.MarshalIndent。
	jarCookies := douyinCookiesForState(currentDouyinCookieJar())
	if len(jarCookies) == 0 {
		return errors.New("抖音 Cookie Jar 为空")
	}
	state := &douyinCookieState{
		Version:   1,
		UpdatedAt: time.Now(),
		UserAgent: activeDouyinUserAgent(),
		Cookies:   make([]douyinStateCookie, 0, len(jarCookies)),
	}
	for _, c := range jarCookies {
		state.Cookies = append(state.Cookies, douyinStateCookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Expires:  c.Expires,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		})
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	// 成品 @0xd6b700 内联写盘，调用序（callees 偏移）：
	//   +0x2cc getDouyinStateFile → +0x2e0 filepath.Dir → +0x2ea os.MkdirAll(dir,0700)
	//   → +0x362 concatstring2(path, ".tmp") → +0x38c os.WriteFile(tmp,data,0600)
	//   → +0x3b4 os.rename(tmp,path)；rename 失败 → +0x3e0 os.Remove(tmp) 后返回错误；
	//   rename 成功 → +0x40a os.Chmod(path,0600) 并返回其结果。
	// 权限立即数：MkdirAll ecx=0x1c0=0700、WriteFile r8=0x180=0600、Chmod ecx=0x180=0600；
	// 后缀常量 ".tmp" 取证 @0x1099647,len4。重建不再走 replaceDouyinStateFile（已删除）。
	path := getDouyinStateFile()
	if err := os.MkdirAll(filepath.Dir(path), douyinStateDirPerm); err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, douyinStateFilePerm); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Chmod(path, douyinStateFilePerm)
}

// loadDouyinCookieState 从状态文件读取 Cookie 状态。
func loadDouyinCookieState() (*douyinCookieState, error) {
	data, err := os.ReadFile(getDouyinStateFile())
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errors.New("抖音 Cookie 状态文件没有 Cookie")
	}
	state := new(douyinCookieState)
	if err := json.Unmarshal(data, state); err != nil {
		return nil, fmt.Errorf("解析抖音 Cookie 状态文件失败: %w", err)
	}
	return state, nil
}

// cleanDouyinUserAgent 清理 UA：去掉首尾空白与内部换行，
// 签名和实际请求使用同一个清理后的 UA，避免 UA 与 A-Bogus 不一致。
func cleanDouyinUserAgent(ua string) string {
	ua = strings.ReplaceAll(ua, "\r", " ")
	ua = strings.ReplaceAll(ua, "\n", " ")
	return strings.TrimSpace(ua)
}

// setDouyinActiveUserAgent 设置当前生效 UA。
func setDouyinActiveUserAgent(ua string) {
	if ua == "" {
		return
	}
	douyinUAMutex.Lock()
	defer douyinUAMutex.Unlock()
	douyinUserAgentValue = ua
}

// activeDouyinUserAgent 返回当前生效 UA。
func activeDouyinUserAgent() string {
	douyinUAMutex.RLock()
	defer douyinUAMutex.RUnlock()
	return douyinUserAgentValue
}

// currentDouyinMsTokenValue 返回当前 msToken：优先取 Cookie，其次缓存，最后生成占位值。
func currentDouyinMsTokenValue() string {
	// 成品唯一的按名取 Cookie 路径是 douyinCookieValueFor(rawURL,key)（auth.go:340）；
	// 重建此前的 currentDouyinCookies/douyinJarAllCookies/douyinCookieValueByName
	// 在成品符号表均不存在，统一改走该函数（域名范围 URL 与 douyinBrowserCookieValue 相同）。
	if v := douyinCookieValueFor(douyinCookieScopeURL, douyinParamMsToken); v != "" {
		return v
	}
	douyinMsTokenMutex.RLock()
	v := douyinMsTokenValue
	douyinMsTokenMutex.RUnlock()
	if v != "" {
		return v
	}
	// 成品基础参数的 msToken 为每次构建的 184 位随机串（0xd64e9e 循环上限 0xb8）。
	return generateDouyinRandomPrefix(184)
}

// setDouyinMsTokenValue 缓存 msToken（登录/刷新链路调用）。
func setDouyinMsTokenValue(v string) {
	douyinMsTokenMutex.Lock()
	defer douyinMsTokenMutex.Unlock()
	douyinMsTokenValue = v
}
