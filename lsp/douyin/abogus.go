package douyin

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// douyinQueryParam 是抖音网页 API 的单个查询参数。
type douyinQueryParam struct {
	Key   string
	Value string
}

// 端点常量（与函数一一对应）。
const (
	douyinUserPostsURL    = BaseHost + "/aweme/v1/web/aweme/post/"
	douyinWorkDetailURL   = BaseHost + "/aweme/v1/web/aweme/detail/"
	douyinUserProfileURL  = BaseHost + "/aweme/v1/web/user/profile/other/"
	douyinLiveProfileHost = BaseHost + "/webcast/user/profile/"
	douyinLiveProfileAlt  = BaseLiveHost + "/webcast/user/profile/"
)

// a_bogus / 签名相关参数名。
const (
	douyinParamABogus       = "a_bogus"
	douyinParamABogusPrefix = "&a_bogus="
	douyinParamVerifyFp     = "verifyFp"
	douyinParamMsToken      = "msToken"
	douyinParamWebid        = "webid"
	douyinParamUserAgent    = "user_agent"
	douyinParamAid          = "aid"
	douyinAidValue          = "6383"
)

// 内置浏览器指纹与环境常量（经典 a_bogus canvas/屏幕指纹串）。
const (
	douyinBrowserFingerprint = "1536|747|1536|834|0|30|0|0|1536|834|1536|864|1525|747|24|24|Win32"
	douyinBuiltInBrowserVer  = "Chrome/143.0.0.0"
	douyinBuiltInChromiumVer = "143"

	// douyinSecCHUAFormat 为 sec-ch-ua 取值格式。
	// 成品 rodata 0x10e69cc 的 63 字节原文："Not_A Brand";v="99", "Chromium";v="%s", "Google Chrome";v="%s"。
	douyinSecCHUAFormat = `"Not_A Brand";v="99", "Chromium";v="%s", "Google Chrome";v="%s"`
	// douyinSecCHUAFallback 是 UA 匹配不到浏览器主版本号时的回退值。
	// 成品 douyinSecCHUA 0xd65360 在不匹配分支加载 rodata 0x109885d（"125"）。
	douyinSecCHUAFallback = "125"
)

// a_bogus 静态管线常量（取值与出处均为成品 linux-x64 二进制）。
const (
	// 摘要盐：SM3(SM3(query+"cus"))，成品 .rodata 0x1098860（公开资料为 "dhzx"，成品字节为准）。
	douyinABogusSalt = "cus"
	// UA 封装 RC4 密钥 {0x00,0x01,0x0E}：generateDouyinRC4BB/generateDouyinDynamicABogus 栈上 0x430 数组。
	douyinABogusUAKey = "\x00\x01\x0e"
	// 静态管线最终封装密钥：generateDouyinRC4BB 末尾 movb $0x79（d662bc），
	// 单字节 0x79 直接作为 RC4 密钥；公开资料为单字节 0xD3（那是动态管线的取值，见 abogus_dynamic.go）。
	douyinABogusStaticKey  = 0x79
	douyinABogusPageID     = 6241 // 0x1861，槽 51/54/55（高字节 24，低字节 97）
	douyinABogusAid        = 6383 // 0x18EF，槽 56/57/58（低字节 239，高字节 24）
	douyinABogusEnvFlags   = 1    // 槽 30-33 的四字节 LE
	douyinABogusDetectFlag = 14   // 槽 34-37 的四字节 LE
	// webid 参数的成品常量（WorkDetailURL 0xd64d45 处 value 0x10b3924，19 字节）。
	douyinBuiltInWebid = "7351848354471872041"
)

// 五张自定义 base64 alphabet（成品 .rodata，已逐字节比对）：
// s0=0x10e7b7a(65) s1=0x10e7bbb(65) s2=0x10e7bfc(65) s3=0x10e7171(64) s4=0x10e7131(64)。
// 65 字符表尾部的 '=' 仅占位，编码移位最大 0x3F，永远取不到下标 64。
const (
	douyinABogusAlphabetS0 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="
	douyinABogusAlphabetS1 = "Dkdpgh4ZKsQB80/Mfvw36XI1R25+WUAlEi7NLboqYTOPuzmFjJnryx9HVGcaStCe="
	douyinABogusAlphabetS2 = "Dkdpgh4ZKsQB80/Mfvw36XI1R25-WUAlEi7NLboqYTOPuzmFjJnryx9HVGcaStCe="
	douyinABogusAlphabetS3 = "ckdp1h4ZKsUB80/Mfvw36XIgR25+WQAlEi7NLboqYTOPuzmFjJnryx9HVGDaStCe"
	douyinABogusAlphabetS4 = "Dkdpgh2ZmsQB80/MfvV36XI1R45-WUAlEixNLwoqYTOPuzKFjJnry79HbGcaStCe"
)

// 静态管线两张槽位表（成品 .rodata 0x114e408 / 0x114e6c0，44 项 int64，
// 第 44 项为下一 rodata 对象首项，校验和只取前 43 项，体索引取全部 44 项）。
var douyinABogusChecksumOrder = [43]int{18, 20, 26, 30, 38, 40, 42, 21, 27, 31, 35, 39, 41, 43,
	22, 28, 32, 36, 23, 29, 33, 37, 44, 45, 46, 47, 48, 49, 50, 24, 25, 52, 53, 54, 55, 57, 58,
	59, 60, 65, 66, 70, 71}

var douyinABogusBodyOrder = [44]int{18, 20, 52, 26, 30, 34, 58, 38, 40, 53, 42, 21, 27, 54, 55,
	31, 35, 57, 39, 41, 43, 22, 28, 32, 60, 36, 23, 29, 33, 37, 44, 45, 59, 46, 47, 48, 49, 50,
	24, 25, 65, 66, 70, 71}

// douyinBrowserVersionPattern 从 UA 中提取浏览器主版本号（成品 0x10aa098 "Chrome/(\d+)"）。
var douyinBrowserVersionPattern = regexp.MustCompile(`Chrome/(\d+)`)

// douyinEdgeVersionPattern 兼容 Edge UA 的版本提取（成品无独立 Edg 提取表，这里保留回退）。
var douyinEdgeVersionPattern = regexp.MustCompile(`Edg/(\d+)`)

// douyinABogusEdgeCleanPattern 签名前清理 UA（成品 0x10ab632 "\s+Edg/[\d.]+"，
// 存于全局 0x1bcc360，appendDouyinLiveABogus 与各 buildSigned* 共用）。
var douyinABogusEdgeCleanPattern = regexp.MustCompile(`\s+Edg/[\d.]+`)

// buildSignedDouyinUserPostsURL 构造带签名的用户作品列表请求地址。
// 成品 0xd63c40：走动态 a_bogus 管线并最后追加网页签名；
// device_web_cpu_core/dy_swidth/dy_sheight/device_web_memory_size 四个参数
// 取 Cookie 值并带成品默认值（0xd64320 的六次读取，含 UIFID/UIFID_TEMP）。
func buildSignedDouyinUserPostsURL(secUID string, count int, cursor string) string {
	params := appendDouyinBaseParams(nil)
	params = append(params,
		douyinQueryParam{Key: "sec_user_id", Value: secUID},
		douyinQueryParam{Key: "count", Value: strconv.Itoa(count)},
		douyinQueryParam{Key: "max_cursor", Value: cursor},
		douyinQueryParam{Key: "min_cursor", Value: "0"},
		douyinQueryParam{Key: "device_web_cpu_core", Value: douyinBrowserCookieValue("device_web_cpu_core", "16")},
		douyinQueryParam{Key: "dy_swidth", Value: douyinBrowserCookieValue("dy_swidth", "1920")},
		douyinQueryParam{Key: "dy_sheight", Value: douyinBrowserCookieValue("dy_sheight", "1080")},
		douyinQueryParam{Key: "device_web_memory_size", Value: douyinBrowserCookieValue("device_web_memory_size", "8")},
	)
	// 成品顺序：先动态 a_bogus，后网页签名（签名的 md5 覆盖含 a_bogus 的 RawQuery）。
	// uifid 不在此直接入参：成品 0xd63f58 的 uifid 参数写点在 Cookie 缺失时为空，
	// 而冒烟实测（2026-10-01）直接带 uifid 参数会让 appendDouyinWebSign 自跳过、
	// 缺 x-secsdk-web-signature 被 403 Signature Not Found；统一由网页签名追加
	// uifid+timestamp+签名（成品 appendDouyinWebSign 本就负责这三项）。
	return appendDouyinWebSign(appendDouyinDynamicABogus(douyinUserPostsURL + "?" + buildDouyinQuery(params)))
}

// buildSignedDouyinUserProfileURL 构造带签名的用户资料请求地址（网页版 other 接口）。
// 成品 0xd64a40 调用动态管线 0xd66ea0（重建先前误接静态管线，已按成品修正）；
// webid 取成品常量（WorkDetailURL 0xd64d45 处证据；UserPosts/UserProfile 未完整解码，取同一常量）。
func buildSignedDouyinUserProfileURL(secUID string) string {
	params := appendDouyinBaseParams(nil)
	params = append(params,
		douyinQueryParam{Key: "sec_user_id", Value: secUID},
		douyinQueryParam{Key: douyinParamWebid, Value: douyinBuiltInWebid},
	)
	return appendDouyinDynamicABogus(douyinUserProfileURL + "?" + buildDouyinQuery(params))
}

// buildSignedDouyinLiveRoomURL 构造带签名的直播间 enter 请求地址（成品 0xd647a0）：
// 端点为 https://live.douyin.com/webcast/room/web/enter/?（rodata 0x10dbb6d），
// 22 项基础参数（含 verifyFp/fp=activeDouyinFP(live 根) 与 msToken）后追加 web_rid，
// 末尾走静态 a_bogus。唯一调用点 getDouyinRoomDataFromSignedAPI(web_rid)。
func buildSignedDouyinLiveRoomURL(webRID string) string {
	params := appendDouyinBaseParams(nil)
	params = append(params,
		douyinQueryParam{Key: "web_rid", Value: webRID},
	)
	return appendDouyinLiveABogus(BaseLiveHost + "/webcast/room/web/enter/?" + buildDouyinQuery(params))
}

// buildSignedDouyinWorkDetailURL 构造带签名的作品详情请求地址。
// 成品 0xd64aa0 逐项复原：基础参数后追加 8 个特定参数（键值全部取自
// 成品机器码内的常量），先动态 a_bogus，后网页签名。
func buildSignedDouyinWorkDetailURL(awemeID string) string {
	params := appendDouyinBaseParams(nil)
	params = append(params,
		douyinQueryParam{Key: "aweme_id", Value: awemeID},
		douyinQueryParam{Key: "update_version_code", Value: "170400"},
		douyinQueryParam{Key: "version_code", Value: "190500"},
		douyinQueryParam{Key: "version_name", Value: "19.5.0"},
		douyinQueryParam{Key: "screen_width", Value: "2328"},
		douyinQueryParam{Key: "screen_height", Value: "1310"},
		douyinQueryParam{Key: "round_trip_time", Value: "150"},
		douyinQueryParam{Key: douyinParamWebid, Value: douyinBuiltInWebid},
	)
	return appendDouyinWebSign(appendDouyinDynamicABogus(douyinWorkDetailURL + "?" + buildDouyinQuery(params)))
}

// douyinBaseParams 返回成品的 22 项基础参数。
// 键值与顺序逐项取自成品 rodata 模板 0x1156288（[22]douyinQueryParam 字面量），
// 运行期补丁与成品一致（douyinBaseParams 0xd64de0）：
// browser_version/engine_version = extractDouyinBrowserVersion()（同一取值）；
// msToken = 每次构建的 184 位随机串（0x10e614b 62 字符表 + 0xb8 循环），
// 成品在 UserPostsURL 里会用 Cookie msToken 覆盖，这里直接走
// currentDouyinMsTokenValue()（Cookie → 缓存 → 184 随机串）；
// verifyFp/fp = activeDouyinFP（Cookie s_v_web_id 以 verify_ 开头则用之，
// 否则进程内一次生成的值）。
func douyinBaseParams() [22]douyinQueryParam {
	params := [22]douyinQueryParam{
		{Key: "device_platform", Value: "webapp"},
		{Key: douyinParamAid, Value: douyinAidValue},
		{Key: "channel", Value: "channel_pc_web"},
		{Key: "pc_client_type", Value: "1"},
		{Key: "cookie_enabled", Value: "true"},
		{Key: "browser_language", Value: "zh-CN"},
		{Key: "browser_platform", Value: "Win32"},
		{Key: "browser_name", Value: "Chrome"},
		{Key: "browser_version", Value: ""},
		{Key: "browser_online", Value: "true"},
		{Key: "engine_name", Value: "Blink"},
		{Key: "engine_version", Value: ""},
		{Key: "os_name", Value: "Windows"},
		{Key: "os_version", Value: "10"},
		{Key: "cpu_core_num", Value: "16"},
		{Key: "device_memory", Value: "8"},
		{Key: "platform", Value: "PC"},
		{Key: "downlink", Value: "10"},
		{Key: "effective_type", Value: "4g"},
		{Key: douyinParamMsToken, Value: ""},
		{Key: douyinParamVerifyFp, Value: ""},
		{Key: "fp", Value: ""},
	}
	version := extractDouyinBrowserVersion(activeDouyinUserAgent())
	params[8].Value = version
	params[11].Value = version
	params[19].Value = currentDouyinMsTokenValue()
	fpValue := activeDouyinFP(BaseHost + "/")
	params[20].Value = fpValue
	params[21].Value = fpValue
	return params
}

// appendDouyinBaseParams 将 22 项基础参数追加到 params 后返回。
func appendDouyinBaseParams(params []douyinQueryParam) []douyinQueryParam {
	base := douyinBaseParams()
	return append(params, base[:]...)
}

// buildDouyinQuery 把参数列表拼成查询串（值做 URL 编码）。
func buildDouyinQuery(params []douyinQueryParam) string {
	var sb strings.Builder
	for i, p := range params {
		if i > 0 {
			sb.WriteByte('&')
		}
		sb.WriteString(p.Key)
		sb.WriteByte('=')
		sb.WriteString(url.QueryEscape(p.Value))
	}
	return sb.String()
}

// douyinSecCHUA 依据 UA 中的浏览器版本生成 sec-ch-ua 头取值。
// 成品 0xd65360 直接用一个 Chrome/(\d+) 正则匹配 UA，匹配失败回退 "125"。
func douyinSecCHUA(ua string) string {
	primary := douyinSecCHUAFallback
	if m := douyinBrowserVersionPattern.FindStringSubmatch(ua); len(m) >= 2 {
		primary = m[1]
	}
	return fmt.Sprintf(douyinSecCHUAFormat, primary, primary)
}

// extractDouyinBrowserVersion 从 UA 中提取浏览器主版本号；
// 内置 UA 命中 Chrome/143，Edge UA 的 Chrome 段同样命中。
func extractDouyinBrowserVersion(ua string) string {
	if ua == "" {
		return douyinBuiltInChromiumVer
	}
	if m := douyinBrowserVersionPattern.FindStringSubmatch(ua); len(m) >= 2 {
		return m[1]
	}
	if m := douyinEdgeVersionPattern.FindStringSubmatch(ua); len(m) >= 2 {
		return m[1]
	}
	return douyinBuiltInChromiumVer
}

var (
	douyinFPOnce sync.Once
	douyinFP     string
)

// sharedDouyinFP 返回进程内共享的浏览器指纹（webid/verifyFp 取值来源）。
func sharedDouyinFP() string {
	douyinFPOnce.Do(func() {
		douyinFP = generateDouyinRandomPrefix(19) + ".tmp"
	})
	return douyinFP
}

// secureDouyinInt 以当前纳秒时间做确定性取模（成品 0xd65560：
// v>0 时返回 time.Now().UnixNano()%v，v<=0 走独立随机源分支，
// 重建包内全部调用均为正数，这里对非正数简单返回 0）。
func secureDouyinInt(v int) int {
	if v <= 0 {
		return 0
	}
	return int(time.Now().UnixNano() % int64(v))
}

// appendDouyinLiveABogus 为直播相关请求追加 a_bogus 参数（静态管线）。
// 成品流程（0xd65680）：解析 URL 取 RawQuery → 清理 UA → generateDouyinRC4BB
// → 前置 12 字节掩码前缀 → douyinResultEncrypt(..., "s4") → 拼 &a_bogus=<sig>=。
// 注意结尾的 '=' 由调用方固定追加（concatstring4），编码本身无填充。
func appendDouyinLiveABogus(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	ua := douyinSignatureUserAgent(activeDouyinUserAgent())
	sealed := generateDouyinRC4BB(u.RawQuery, ua, douyinBrowserFingerprint)
	payload := append(douyinLiveRandomPrefix(), sealed...)
	signature := douyinResultEncrypt(payload, "s4")
	return rawURL + douyinParamABogusPrefix + signature + "="
}

// douyinCookieScopeURL 成品 douyinBrowserCookieValue 读取 Cookie 用的固定 URL
// （.rodata 0x10dabf5，47 字节）。
const douyinCookieScopeURL = "https://www.douyin.com/aweme/v1/web/aweme/post/"

// douyinBrowserCookieValue 返回指定 Cookie 的取值，取不到时返回 fallback
// （成品 0xd64320，abogus.go:96；TrimSpace 后为空才用 fallback）。
func douyinBrowserCookieValue(key string, fallback string) string {
	if v := strings.TrimSpace(douyinCookieValueFor(douyinCookieScopeURL, key)); v != "" {
		return v
	}
	return fallback
}

// douyinCookieValueFor 从当前 Cookie 罐按域名范围读取指定 Cookie
// （成品 douyinCookieValueFor 0xd6b540，auth.go:340）：
// 解析 URL → 罐为空或解析失败返回空 → host 去端口后须为 douyin.com 及其子域
// → jar.Cookies(u) 里首个同名 Cookie 的值。
// 重建此前的 douyinCookieValueByName/currentDouyinCookies/douyinJarAllCookies 已删除。
func douyinCookieValueFor(rawURL string, key string) string {
	jar := currentDouyinCookieJar()
	u, err := url.Parse(rawURL)
	if jar == nil || err != nil {
		return ""
	}
	host := u.Host
	if h, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = h
	}
	if !isDouyinDomain(host) {
		return ""
	}
	for _, c := range jar.Cookies(u) {
		if c != nil && c.Name == key {
			return c.Value
		}
	}
	return ""
}

// douyinSignatureUserAgent 返回参与签名的 UA（成品：删除 Edge 后缀）。
func douyinSignatureUserAgent(ua string) string {
	return douyinABogusEdgeCleanPattern.ReplaceAllString(ua, "")
}

// douyinLiveRandomPrefix 生成静态管线的 12 字节固定形态前缀
// （成品 generateDouyinRandomPrefix 0xd65880：三次 secureDouyinInt(10000)，
// 每次派生 4 字节 [(n&0xAA)|0x01, (n&0x55)|0x02, ((n>>8)&0xAA)|0x05, ((n>>8)&0x55)|0x28]，
// 三组写法相同，即 12 字节为同一 4 字节模式重复三次）。
func douyinLiveRandomPrefix() []byte {
	out := make([]byte, 12)
	n := secureDouyinInt(10000)
	p0 := byte((n & 0xAA) | 0x01)
	p1 := byte((n & 0x55) | 0x02)
	p2 := byte(((n >> 8) & 0xAA) | 0x05)
	p3 := byte(((n >> 8) & 0x55) | 0x28)
	for group := 0; group < 3; group++ {
		out[group*4+0] = p0
		out[group*4+1] = p1
		out[group*4+2] = p2
		out[group*4+3] = p3
	}
	return out
}

// generateDouyinRandomPrefix 生成 n 位随机前缀（数字与大小写字母）。
func generateDouyinRandomPrefix(n int) string {
	if n <= 0 {
		return ""
	}
	const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	out := make([]byte, 0, n)
	for len(out) < n {
		v, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			break
		}
		out = append(out, alphabet[v.Int64()])
	}
	return string(out)
}

// generateDouyinRC4BB 计算静态管线 a_bogus 密文（成品 0xd659c0，逐指令复原）。
//
// 流程：now1=UnixMilli → queryDigest=SM3(SM3(query+"cus")) →
// saltDigest=SM3(SM3("cus")) → now2=UnixMilli → uaDigest=SM3(s3编码(RC4({0,1,14},ua))) →
// 填 72 个 int64 槽位 → 43 项表 XOR 得校验和 → 44 项表取字节 + 追加指纹串 + 校验和低字节
// → 标准 RC4(单字节密钥 0x79)。
func generateDouyinRC4BB(query string, ua string, fingerprint string) []byte {
	now1 := time.Now().UnixMilli()
	queryDigest := sm3Digest(sm3Digest([]byte(query + douyinABogusSalt)))
	saltDigest := sm3Digest(sm3Digest([]byte(douyinABogusSalt)))
	now2 := time.Now().UnixMilli()
	uaSealed := douyinResultEncrypt(douyinRC4([]byte(douyinABogusUAKey), []byte(ua)), "s3")
	uaDigest := sm3Digest([]byte(uaSealed))

	// 72 个 int64 槽位（栈上 0x448 数组，索引即成品写入偏移/8）。
	// 槽位缺省为 0；now1/now2 槽位为有符号整槽，输出时按字节截断。
	var fields [72]int64
	fields[8] = 3
	fields[18] = 44
	fields[19] = 1
	fields[20] = (now1 >> 24) & 0xFF
	fields[21] = (now1 >> 16) & 0xFF
	fields[22] = (now1 >> 8) & 0xFF
	fields[23] = now1 & 0xFF
	fields[24] = now1 >> 32
	fields[25] = now1 >> 24
	// 槽 26-29 = douyinABogusEnvFlags(1) 前未用，恒 0；
	// 槽 30-33 / 34-37 = 1 / 14 的四字节小端展开。
	fields[33] = douyinABogusEnvFlags
	fields[37] = douyinABogusDetectFlag
	// 摘要字节槽：query/salt 取 [21][22]，UA 取 [23][24]（栈偏移 0x58/0x59、0xb8/0xb9、0x9a/0x9b）。
	fields[38] = int64(queryDigest[21])
	fields[39] = int64(queryDigest[22])
	fields[40] = int64(saltDigest[21])
	fields[41] = int64(saltDigest[22])
	fields[42] = int64(uaDigest[23])
	fields[43] = int64(uaDigest[24])
	fields[44] = (now2 >> 24) & 0xFF
	fields[45] = (now2 >> 16) & 0xFF
	fields[46] = (now2 >> 8) & 0xFF
	fields[47] = now2 & 0xFF
	fields[48] = 3
	fields[49] = now2 >> 32
	fields[50] = now2 >> 24
	fields[51] = douyinABogusPageID
	// 槽 54-58 = [6241>>8, 6241&0xFF, 6383, 6383&0xFF, 6383>>8] = [24, 97, 6383, 239, 24]。
	fields[54] = douyinABogusPageID >> 8
	fields[55] = douyinABogusPageID & 0xFF
	fields[56] = douyinABogusAid
	fields[57] = douyinABogusAid & 0xFF
	fields[58] = douyinABogusAid >> 8
	fields[64] = int64(len(fingerprint))
	fields[65] = int64(len(fingerprint) & 0xFF)
	fields[66] = int64((len(fingerprint) >> 8) & 0xFF)

	// 校验和：43 项索引表对应槽位按 int64 全值 XOR，追加时取低字节。
	checksum := int64(0)
	for _, idx := range douyinABogusChecksumOrder {
		checksum ^= fields[idx]
	}

	// 体：44 项索引表逐槽取低字节 + 指纹串原文 + 校验和低字节。
	body := make([]byte, 0, len(douyinABogusBodyOrder)+len(fingerprint)+1)
	for _, idx := range douyinABogusBodyOrder {
		body = append(body, byte(fields[idx]))
	}
	body = append(body, fingerprint...)
	body = append(body, byte(checksum))

	return douyinRC4([]byte{douyinABogusStaticKey}, body)
}

// douyinRC4 标准 RC4 加解密。
func douyinRC4(key []byte, data []byte) []byte {
	out := make([]byte, len(data))
	box := make([]byte, 256)
	for i := 0; i < 256; i++ {
		box[i] = byte(i)
	}
	j := 0
	for i := 0; i < 256; i++ {
		j = (j + int(box[i]) + int(key[i%len(key)])) % 256
		box[i], box[j] = box[j], box[i]
	}
	i := 0
	j = 0
	for k := 0; k < len(data); k++ {
		i = (i + 1) % 256
		j = (j + int(box[i])) % 256
		box[i], box[j] = box[j], box[i]
		out[k] = data[k] ^ box[(int(box[i])+int(box[j]))%256]
	}
	return out
}

// douyinResultEncrypt 按材料名选择 alphabet 编码（成品 0xd665c0 的 switch）：
// "s0"→标准表，"s1"/"s2"→+/−变体，"s3"→ckdp1h 表，默认（含 "s4"）→Dkdpgh2 表。
func douyinResultEncrypt(data []byte, name string) string {
	alphabet := douyinABogusAlphabetS4
	switch name {
	case "s0":
		alphabet = douyinABogusAlphabetS0
	case "s1":
		alphabet = douyinABogusAlphabetS1
	case "s2":
		alphabet = douyinABogusAlphabetS2
	case "s3":
		alphabet = douyinABogusAlphabetS3
	}
	return douyinBase64Encode(data, alphabet)
}

// douyinBase64Encode 无填充自定义 base64（成品 0xd665c0 编码主体）：
// 移位表 [18,12,6,0]，输出长度 ⌊(4n+2)/3⌋，越界字节按 0 参与移位。
func douyinBase64Encode(data []byte, alphabet string) string {
	outLen := (4*len(data) + 2) / 3
	out := make([]byte, outLen)
	shifts := [4]uint{18, 12, 6, 0}
	for i := 0; i < outLen; i++ {
		group := i / 4
		var b0, b1, b2 byte
		if group*3 < len(data) {
			b0 = data[group*3]
		}
		if group*3+1 < len(data) {
			b1 = data[group*3+1]
		}
		if group*3+2 < len(data) {
			b2 = data[group*3+2]
		}
		block := uint32(b0)<<16 | uint32(b1)<<8 | uint32(b2)
		out[i] = alphabet[(block>>shifts[i%4])&0x3F]
	}
	return string(out)
}

// sm3Digest 计算 SM3 摘要（国标 GB/T 32905-2016）。
func sm3Digest(data []byte) []byte {
	var digest [8]uint32
	copy(digest[:], sm3IV[:])
	// 填充：追加 0x80、零与 64 位消息长度。
	padded := make([]byte, 0, len(data)+64)
	padded = append(padded, data...)
	padded = append(padded, 0x80)
	for len(padded)%64 != 56 {
		padded = append(padded, 0x00)
	}
	bitLen := uint64(len(data)) * 8
	var lenBytes [8]byte
	for i := 0; i < 8; i++ {
		lenBytes[i] = byte(bitLen >> (56 - 8*i))
	}
	padded = append(padded, lenBytes[:]...)
	for offset := 0; offset < len(padded); offset += 64 {
		var block [64]byte
		copy(block[:], padded[offset:offset+64])
		sm3Compress(&block, &digest)
	}
	out := make([]byte, 32)
	for i := 0; i < 8; i++ {
		out[i*4] = byte(digest[i] >> 24)
		out[i*4+1] = byte(digest[i] >> 16)
		out[i*4+2] = byte(digest[i] >> 8)
		out[i*4+3] = byte(digest[i])
	}
	return out
}

var sm3IV = [8]uint32{
	0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600,
	0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e,
}

// sm3Compress 对单个 64 字节分组执行 SM3 压缩函数。
func sm3Compress(block *[64]byte, digest *[8]uint32) {
	var w [68]uint32
	var w1 [64]uint32
	for i := 0; i < 16; i++ {
		w[i] = uint32(block[i*4])<<24 | uint32(block[i*4+1])<<16 |
			uint32(block[i*4+2])<<8 | uint32(block[i*4+3])
	}
	for i := 16; i < 68; i++ {
		w[i] = sm3P1(w[i-16]^w[i-9]^sm3Rotl(w[i-3], 15)) ^ sm3Rotl(w[i-13], 7) ^ w[i-6]
	}
	for i := 0; i < 64; i++ {
		w1[i] = w[i] ^ w[i+4]
	}
	a, b, c, d, e, f, g, h := digest[0], digest[1], digest[2], digest[3],
		digest[4], digest[5], digest[6], digest[7]
	for j := 0; j < 64; j++ {
		t := uint32(0x79cc4519)
		if j >= 16 {
			t = 0x7a879d8a
		}
		ss1 := sm3Rotl(sm3Rotl(a, 12)+e+sm3Rotl(t, uint(j)%32), 7)
		ss2 := ss1 ^ sm3Rotl(a, 12)
		var tt1, tt2 uint32
		if j < 16 {
			tt1 = (a ^ b ^ c) + d + ss2 + w1[j]
			tt2 = (e ^ f ^ g) + h + ss1 + w[j]
		} else {
			tt1 = ((a & b) | (a & c) | (b & c)) + d + ss2 + w1[j]
			tt2 = ((e & f) | (^e & g)) + h + ss1 + w[j]
		}
		d = c
		c = sm3Rotl(b, 9)
		b = a
		a = tt1
		h = g
		g = sm3Rotl(f, 19)
		f = e
		e = sm3P0(tt2)
	}
	digest[0] ^= a
	digest[1] ^= b
	digest[2] ^= c
	digest[3] ^= d
	digest[4] ^= e
	digest[5] ^= f
	digest[6] ^= g
	digest[7] ^= h
}

func sm3Rotl(x uint32, n uint) uint32 {
	return (x << n) | (x >> (32 - n))
}

func sm3P0(x uint32) uint32 {
	return x ^ sm3Rotl(x, 9) ^ sm3Rotl(x, 17)
}

func sm3P1(x uint32) uint32 {
	return x ^ sm3Rotl(x, 15) ^ sm3Rotl(x, 23)
}
