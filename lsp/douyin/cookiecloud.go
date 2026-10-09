package douyin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
)

// cryptoType 取值。
const (
	douyinCookieCloudCryptoAuto   = "auto"
	douyinCookieCloudCryptoLegacy = "legacy"
	douyinCookieCloudCryptoFixed  = "aes-128-cbc-fixed"
)

// douyinCookieCloudConfig 是 douyin.cookieCloud 配置段。
type douyinCookieCloudConfig struct {
	Endpoint   string
	UUID       string
	Password   string
	CryptoType string
	Interval   time.Duration
	Timeout    time.Duration
}

// douyinCookieCloudCookie 是 CookieCloud 数据中的单个 Cookie（浏览器导出格式）。
type douyinCookieCloudCookie struct {
	Name           string  `json:"name"`
	Value          string  `json:"value"`
	Domain         string  `json:"domain"`
	Path           string  `json:"path"`
	ExpirationDate float64 `json:"expirationDate"`
	Secure         bool    `json:"secure"`
	HTTPOnly       bool    `json:"httpOnly"`
}

// candidate 是 CookieCloud 去重时的候选项（Cookie + 过期时间）。
// 成品 reflect 元数据（types-closure-x64.tsv:8114）size=16 =
// {cookie *http.Cookie@0; expiration float64@8}；成品在
// extractDouyinCookieCloudCookies @cookiecloud.go:233（0xd7aec0，3200B）里用
// map[string]douyin.candidate 做「同 (Name,Domain,Path) Cookie 保留较晚过期者」的去重
// （map 类型描述符引用点 @0xd7b63f/@0xd7b67d/@0xd7b75d/@0xd7b943，
// 并以 @0xd7b665 ucomisd existing.expiration,new.expiration 比较 expiration）。
// 重建版 extractDouyinCookieCloudCookies 已按该 map + ucomisd 语义迁移。
type candidate struct {
	cookie     *http.Cookie
	expiration float64
}

// douyinCookieCloudServerResponse 是 CookieCloud 服务端 /get 响应。
type douyinCookieCloudServerResponse struct {
	Encrypted  string `json:"encrypted"`
	CryptoType string `json:"crypto_type"`
}

// douyinCookieCloudServerHost 是解析后的 CookieCloud 服务端地址。
type douyinCookieCloudServerHost struct {
	scheme   string
	host     string
	basePath string
}

// douyinCookieCloudHTTPDoer 抽象 HTTP 执行器，便于测试注入。
type douyinCookieCloudHTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// douyinCookieCloudHTTPClient 默认 HTTP 执行器。
type douyinCookieCloudHTTPClient struct {
	timeout time.Duration
}

// Do 实现 douyinCookieCloudHTTPDoer。
func (c *douyinCookieCloudHTTPClient) Do(req *http.Request) (*http.Response, error) {
	client := &http.Client{Timeout: c.timeout}
	client.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return client.Do(req)
}

// loadDouyinCookieCloudConfig 读取并校验 douyin.cookieCloud 配置段。
// loadDouyinCookieCloudConfig 读取并校验 douyin.cookieCloud 配置段。
// 各项统一走 lsp/cfg 的 getter（默认值与区间收敛也在 getter 内完成）。
func loadDouyinCookieCloudConfig() (*douyinCookieCloudConfig, error) {
	cloud := &douyinCookieCloudConfig{
		Endpoint:   cfg.GetDouyinCookieCloudEndpoint(),
		UUID:       cfg.GetDouyinCookieCloudUUID(),
		Password:   cfg.GetDouyinCookieCloudPassword(),
		CryptoType: cfg.GetDouyinCookieCloudCryptoType(),
		Interval:   cfg.GetDouyinCookieCloudInterval(),
		Timeout:    cfg.GetDouyinCookieCloudTimeout(),
	}
	if cloud.Endpoint == "" {
		return nil, errors.New("douyin.cookieCloud.endpoint 未配置")
	}
	if u, err := url.Parse(cloud.Endpoint); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("douyin.cookieCloud.endpoint 必须是有效的 http/https 地址")
	}
	if cloud.UUID == "" {
		return nil, errors.New("douyin.cookieCloud.uuid 未配置")
	}
	if strings.ContainsAny(cloud.UUID, "/\\") {
		return nil, errors.New("douyin.cookieCloud.uuid 不能包含路径分隔符")
	}
	if cloud.Password == "" {
		return nil, errors.New("douyin.cookieCloud.password 未配置")
	}
	switch cloud.CryptoType {
	case douyinCookieCloudCryptoAuto, douyinCookieCloudCryptoLegacy, douyinCookieCloudCryptoFixed:
	default:
		return nil, fmt.Errorf("不支持的 douyin.cookieCloud.cryptoType: %s", cloud.CryptoType)
	}
	return cloud, nil
}

// douyinCookieCloudServerHostOf 解析服务端地址（支持部署在子目录）。
func douyinCookieCloudServerHostOf(endpoint string) (*douyinCookieCloudServerHost, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("douyin.cookieCloud.endpoint 无效: %s", endpoint)
	}
	host := &douyinCookieCloudServerHost{
		scheme: u.Scheme,
		host:   u.Host,
	}
	if u.Path != "" {
		host.basePath = strings.Trim(u.Path, "/")
	}
	return host, nil
}

// douyinCookieCloudServerHostForLog 返回日志用的 server_host：
// 解析 endpoint 的 Host；无法解析或 Host 为空时返回字面量 "invalid"
// （成品 Start @0xd73f19 net/url.Parse → @0xd73f33 "invalid"@0x10a30f0,len7 /
// @0xd73f42 u.Host@+0x28/+0x30）。
func douyinCookieCloudServerHostForLog(endpoint string) string {
	host, err := douyinCookieCloudServerHostOf(endpoint)
	if err != nil || host.host == "" {
		return "invalid"
	}
	return host.host
}

// douyinCookieCloudDownloadURL 拼出 CookieCloud 下载地址 <endpoint>/get/<uuid>。
func douyinCookieCloudDownloadURL(host *douyinCookieCloudServerHost, uuid string) string {
	if host.basePath != "" {
		return host.scheme + "://" + host.host + "/" + host.basePath + "/get/" + url.PathEscape(uuid)
	}
	return host.scheme + "://" + host.host + "/get/" + url.PathEscape(uuid)
}

// fetchDouyinCookieCloudCookies 下载、解密并提取 CookieCloud 中的抖音 Cookie。
func fetchDouyinCookieCloudCookies(client douyinCookieCloudHTTPDoer, cfg *douyinCookieCloudConfig) ([]*http.Cookie, error) {
	host, err := douyinCookieCloudServerHostOf(cfg.Endpoint)
	if err != nil {
		return nil, err
	}
	downloadURL := douyinCookieCloudDownloadURL(host, cfg.UUID)
	trace := beginDouyinAPIRequest(douyinRequestTypeAllDouyinAPI, "GET", downloadURL)
	req, err := http.NewRequest(http.MethodGet, downloadURL, nil)
	if err != nil {
		trace.finish(err)
		return nil, err
	}
	req.Header.Set("User-Agent", activeDouyinUserAgent())
	if client == nil {
		client = &douyinCookieCloudHTTPClient{timeout: cfg.Timeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		trace.finish(err)
		return nil, fmt.Errorf("CookieCloud 获取抖音Cookie: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		trace.finish(err)
		return nil, fmt.Errorf("CookieCloud 获取抖音Cookie: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("解析抖音短链接失败: HTTP %d", resp.StatusCode)
		trace.finish(err)
		return nil, fmt.Errorf("CookieCloud 获取抖音Cookie: HTTP %d", resp.StatusCode)
	}
	trace.finish(nil)

	var serverResp douyinCookieCloudServerResponse
	if err := json.Unmarshal(body, &serverResp); err != nil {
		return nil, fmt.Errorf("解析抖音CookieCloud响应失败: %w", err)
	}
	if serverResp.Encrypted == "" {
		return nil, errors.New("CookieCloud 响应中没有加密数据")
	}
	decrypted, err := decryptDouyinCookieCloud(serverResp, cfg)
	if err != nil {
		return nil, err
	}
	return extractDouyinCookieCloudCookies(decrypted)
}

// decryptDouyinCookieCloud 依据 cryptoType 解密 CookieCloud 数据。
func decryptDouyinCookieCloud(resp douyinCookieCloudServerResponse, cfg *douyinCookieCloudConfig) ([]byte, error) {
	cipherData, err := base64.StdEncoding.DecodeString(resp.Encrypted)
	if err != nil {
		return nil, fmt.Errorf("解析抖音CookieCloud数据失败: %w", err)
	}
	cryptoType := cfg.CryptoType
	if cryptoType == douyinCookieCloudCryptoAuto {
		cryptoType = resp.CryptoType
		if cryptoType != douyinCookieCloudCryptoLegacy && cryptoType != douyinCookieCloudCryptoFixed {
			cryptoType = douyinCookieCloudCryptoFixed
		}
	}
	var key, iv []byte
	switch cryptoType {
	case douyinCookieCloudCryptoLegacy:
		// TODO(lite): legacy 模式密钥派生按 CookieCloud 标准协议推断
		// （MD5(uuid-password) 取前16字节作 key，再对 key 摘要取 IV），未与成品对账。
		keySum := md5.Sum([]byte(cfg.UUID + "-" + cfg.Password))
		key = []byte(hex.EncodeToString(keySum[:])[:16])
		ivSum := md5.Sum([]byte(hex.EncodeToString(keySum[:])[:16]))
		iv = []byte(hex.EncodeToString(ivSum[:])[:16])
	default:
		key, iv = douyinCookieCloudEVPBytesToKey([]byte(cfg.Password), nil, 16, 16)
	}
	return decryptDouyinCookieCloudCBC(cipherData, key, iv)
}

// decryptDouyinCookieCloudCBC 以 AES-128-CBC 解密并去 PKCS7 填充。
func decryptDouyinCookieCloudCBC(data []byte, key []byte, iv []byte) ([]byte, error) {
	if len(key) != 16 {
		return nil, errors.New("CookieCloud 密钥长度无效")
	}
	if len(iv) != 16 {
		return nil, errors.New("CookieCloud IV 长度无效")
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("CookieCloud 数据长度无效")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	mode := cipher.NewCBCDecrypter(block, iv)
	out := make([]byte, len(data))
	mode.CryptBlocks(out, data)
	return douyinCookieCloudPKCS7Unpad(out)
}

// douyinCookieCloudPKCS7Unpad 去除 PKCS7 填充。
func douyinCookieCloudPKCS7Unpad(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("CookieCloud 数据为空")
	}
	padding := int(data[len(data)-1])
	if padding == 0 || padding > aes.BlockSize || padding > len(data) {
		return nil, errors.New("CookieCloud 数据填充无效")
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, errors.New("CookieCloud 数据填充无效")
		}
	}
	return data[:len(data)-padding], nil
}

// douyinCookieCloudEVPBytesToKey 复刻 OpenSSL EVP_BytesToKey（MD5 摘要，无迭代计数）。
func douyinCookieCloudEVPBytesToKey(password []byte, salt []byte, keyLen int, ivLen int) ([]byte, []byte) {
	var out []byte
	prev := []byte{}
	for len(out) < keyLen+ivLen {
		h := md5.New()
		h.Write(prev)
		h.Write(password)
		h.Write(salt)
		prev = h.Sum(nil)
		out = append(out, prev...)
	}
	return out[:keyLen], out[keyLen : keyLen+ivLen]
}

// extractDouyinCookieCloudCookies 解析解密后的 cookie_data，提取 douyin.com 及其子域 Cookie，
// 按 (Name, Domain, Path) 去重并保留过期时间更晚的一份，返回按去重键排序的 []*http.Cookie。
//
// 成品 @0xd7aec0（cookiecloud.go:233，3200B）逐条证据：
//   - 域过滤用 cookie.Domain（不是 CookieData 的 map 键）：@0xd7b2c5 strings.TrimSpace
//     → @0xd7b2ca strings.ToLower → 剥前导 '.'（@0xd7b2d5 cmpb $0x2e）→ @0xd7b340
//     精确比较 "douyin.com"（len10）或 @0xd7b360 以 ".douyin.com"（rodata@0x10a8c04,len11）
//     结尾；不匹配则 continue（@0xd7b3a4）；
//   - TrimSpace(Name) 为空（@0xd7b3b1/c1）或 Value 为空（@0xd7b3cb cmpq $0,0x100）则跳过；
//   - 构造 *http.Cookie（@0xd7b473 newobject）：Name/Value 原样，Path 为空时置 "/"
//     （@0xd7b4a3 cmove 默认串 rodata@0x1098229='/'），Domain = "." + 规整后的域
//     （@0xd7b527 concatstring2，前缀 rodata@0x11212e8='.'），Secure/HttpOnly 原样
//     （@0xd7b58a/d7b597 写 +0x78/+0x79）；ExpirationDate>0 时 Expires=time.Unix(int64,0)
//     （@0xd7b5a7 ucomisd 0 后 cvttsd2si，加 Unix 纪元常量 62135596800）；
//   - 去重表 map[string]douyin.candidate（map 类型描述符 0xf67820；candidate{cookie *http.Cookie@0,
//     expiration float64@8}）：key = Name + "\x00" + Domain + "\x00" + Path
//     （@0xd7b624 runtime.concatstring5，分隔串 rodata@0x1124090 首字节 0x00）；
//     命中时 @0xd7b657 取 existing.expiration、@0xd7b665 ucomisd existing,new → jbe 跳过，
//     即仅当 new.expiration > existing.expiration 时 @0xd7b6a0 mapassign 覆盖
//     （@0xd7b6ae 写 candidate+8、@0xd7b6dd 写 candidate+0）；
//   - 收集 map key → @0xd7b8c0 sort.Strings → 逐 key @0xd7b952 mapaccess1_faststr 取
//     candidate.cookie，追加进 8 字节元素切片（@0xd7b8ec），返回 []*http.Cookie。
func extractDouyinCookieCloudCookies(decrypted []byte) ([]*http.Cookie, error) {
	var payload struct {
		CookieData map[string][]douyinCookieCloudCookie `json:"cookie_data"`
	}
	if err := json.Unmarshal(decrypted, &payload); err != nil {
		return nil, fmt.Errorf("解析抖音CookieCloud数据失败: %w", err)
	}
	candidates := make(map[string]candidate)
	for _, cookies := range payload.CookieData {
		for i := range cookies {
			c := &cookies[i]
			domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(c.Domain)), ".")
			if domain != "douyin.com" && !strings.HasSuffix(domain, ".douyin.com") {
				continue
			}
			if strings.TrimSpace(c.Name) == "" || c.Value == "" {
				continue
			}
			path := c.Path
			if path == "" {
				path = "/"
			}
			cookie := &http.Cookie{
				Name:     c.Name,
				Value:    c.Value,
				Path:     path,
				Domain:   "." + domain,
				Secure:   c.Secure,
				HttpOnly: c.HTTPOnly,
			}
			if c.ExpirationDate > 0 {
				cookie.Expires = time.Unix(int64(c.ExpirationDate), 0)
			}
			key := cookie.Name + douyinFingerprintFieldSeparator +
				cookie.Domain + douyinFingerprintFieldSeparator +
				cookie.Path
			if existing, ok := candidates[key]; ok && c.ExpirationDate <= existing.expiration {
				continue
			}
			candidates[key] = candidate{cookie: cookie, expiration: c.ExpirationDate}
		}
	}
	keys := make([]string, 0, len(candidates))
	for key := range candidates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]*http.Cookie, 0, len(keys))
	for _, key := range keys {
		result = append(result, candidates[key].cookie)
	}
	return result, nil
}

// 指纹分隔串（成品字节级证据，非打印字符故单独列出）。
//
// 成品 douyinCookieJarFingerprint @0xd7bb40 在循环里用 runtime.concatstrings
// （@+0xff，7 元素数组：Domain, sep, Path, sep, Name, sep, Value）把每条 Cookie 拼成 key：
//   - 数组 0x108/0x128/0x148 三处分隔串都来自 `lea 0x3a842c(%rip)` → 0x1124090，
//     长度 `movq $0x1`；读 .rodata@0x1124090 首字节 = 0x00 → 字段分隔串 "\x00"。
//   - 排序（sort.Strings @+0x2b7 之后）后 strings.Join @+0x2b7 传 rdi=0x1121250、
//     rsi=1；读 .rodata@0x1121250 首字节 = 0x01 → key 间隔串 "\x01"。
const (
	douyinFingerprintFieldSeparator = "\x00"
	douyinFingerprintKeySeparator   = "\x01"
)

// douyinCookieJarFingerprint 计算 Cookie Jar 的稳定指纹（不输出 Cookie 值）。
//
// 成品 @0xd7bb40（960B，cookiecloud.go:307）签名 func(jar http.CookieJar) string：
// 入口保存 rax/rbx 并 `test %rax,%rax; je ret`（nil 接口返回空串），随后
// `call douyinCookiesForState`（@0xd6aa80，auth.go:275）。循环体
// `mov (%rax,%rcx,8),%rdx` 按 8 字节步长取元素指针，再读 0x38/0x28/0x00/0x10
// 四个 string 字段：这正是 net/http.Cookie 的布局（Name 0x00、Value 0x10、
// Quoted 0x20、Path 0x28、Domain 0x38，go1.27 源码 net/http/cookie.go），
// 即成品 douyinCookiesForState 返回 []*http.Cookie，字段序为
// Domain + "\x00" + Path + "\x00" + Name + "\x00" + Value。
// 之后 sort.Strings 排序、strings.Join(keys, "\x01")（@+0x2b7）、
// crypto/sha256.Sum256（@+0x2cc）后 hex.EncodeToString 成 64 字符返回
// （@+0x2e1 起用 "0123456789abcdef" 表转 64 字节 → runtime.slicebytetostring）。
//
// 调用点（callers 证据）：markDouyinCookieUnhealthyFor @+0x4f、
// reportDouyinCookieFailure @+0x71、markDouyinCookieHealthy @+0x2f、
// markDouyinCookieHealthyFor @+0x40、refreshDouyinCookieFromCookieCloud @+0x177/@+0x1a0。
//
// 说明：douyinCookiesForState 已按成品对齐为返回 []*http.Cookie（auth.go），
// 此处直接读 *http.Cookie 的 Domain/Path/Name/Value，字段序与分隔串逐字节还原。
func douyinCookieJarFingerprint(jar http.CookieJar) string {
	// 成品入口 test %rax,%rax; je ret（@0xd7bb6d）：nil 接口直接返回空串。
	if jar == nil {
		return ""
	}
	cookies := douyinCookiesForState(jar)
	keys := make([]string, 0, len(cookies))
	for _, c := range cookies {
		keys = append(keys, c.Domain+douyinFingerprintFieldSeparator+
			c.Path+douyinFingerprintFieldSeparator+
			c.Name+douyinFingerprintFieldSeparator+
			c.Value)
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, douyinFingerprintKeySeparator)))
	return hex.EncodeToString(sum[:])
}

// refreshDouyinCookieFromCookieCloud 拉取并应用一次 CookieCloud 数据。
// 返回 Cookie 是否发生变化；云端数据缺少登录会话或解密失败时不覆盖当前 Cookie。
func refreshDouyinCookieFromCookieCloud(cfg *douyinCookieCloudConfig) (bool, error) {
	cookies, err := fetchDouyinCookieCloudCookies(nil, cfg)
	if err != nil {
		return false, err
	}
	// 成品 callees 顺序：fetch @+0x98 → setDouyinJarCookies @+0x140 → hasDouyinLoginCookie @+0x154
	// → currentDouyinCookieJar @+0x168 → douyinCookieJarFingerprint @+0x177/@+0x1a0
	// → setCurrentDouyinCookieJar @+0x4f3。登录会话判定改为对新 Jar 调 hasDouyinLoginCookie
	//（成品符号表无 douyinCloudCookiesHaveSession / douyinCloudCookiesToHTTP 两个独立符号）。
	jar, jarErr := newDouyinCookieJar()
	if jarErr != nil {
		return false, jarErr
	}
	if err := setDouyinJarCookies(jar, cookies); err != nil {
		return false, err
	}
	if !hasDouyinLoginCookie(jar) {
		logger.Warn("CookieCloud 中的抖音Cookie缺少登录会话，继续使用上一份Cookie")
		return false, nil
	}
	before := ""
	if current := currentDouyinCookieJar(); current != nil {
		before = douyinCookieJarFingerprint(current)
	}
	if before == douyinCookieJarFingerprint(jar) {
		logger.Info("CookieCloud 定时获取成功，抖音Cookie没有变化")
		return false, nil
	}
	setCurrentDouyinCookieJar(jar)
	// 成品 refreshDouyinCookieFromCookieCloud 的 callees 里没有任何来源赋值：
	// 无 douyinSourceMutex.Lock/Unlock、无 "state_file"(@0x10a7679) 字面量引用，
	// 也无任何 .data 段 rip 相对写。故此处不再改写 cookie_source。
	logger.WithField("cookie_count", len(cookies)).
		Info("已从 CookieCloud 获取抖音Cookie")
	return true, nil
}

// refreshDouyinCookieCloud 由 Concern 启动的定时刷新入口：
// 启动后立即获取一次，此后按配置间隔定时获取；
// 失败时保留上一份 Cookie 并在下轮重试。
func (d *Concern) refreshDouyinCookieCloud() {
	cfg, err := loadDouyinCookieCloudConfig()
	if err != nil {
		logger.WithError(err).Warn("抖音 CookieCloud 配置不完整，将临时使用本地备用Cookie")
		return
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("CookieCloud 定时获取抖音Cookie失败，继续使用上一份Cookie: %v", r)
		}
	}()
	changed, err := refreshDouyinCookieFromCookieCloud(cfg)
	if err != nil {
		if douyinCloudFirstFetchFailed {
			logger.WithError(err).Warnf("CookieCloud 定时获取抖音Cookie失败，继续使用上一份Cookie")
		} else {
			douyinCloudFirstFetchFailed = true
			logger.WithError(err).Warnf("CookieCloud 首次获取抖音Cookie失败，将保留本地备用Cookie并按配置间隔重试")
		}
		return
	}
	douyinCloudFirstFetchFailed = false
	if changed {
		// 定时把新 Cookie 落盘（成品 (*Concern).persistAuthState 的"定时保存"调用点）。
		d.persistAuthState("定时保存抖音 Cookie 状态失败")
		resetDouyinCookieFailures()
		markDouyinCookieHealthy()
	}
}

var (
	douyinCloudFirstFetchFailed bool
)

// newDouyinCookieJar 创建空 Jar。
func newDouyinCookieJar() (*cookiejar.Jar, error) {
	return cookiejar.New(nil)
}
