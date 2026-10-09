package bilibili

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/authprompt"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cookiecloudclient"
	"github.com/sirupsen/logrus"
	"go.uber.org/atomic"
)

// CookieCloud 模式：从用户部署的 CookieCloud 服务定时同步完整B站登录 Cookie。
// 加密/解密细节由包内自带实现完成（decryptCookieCloud 链：legacy 与
// aes-128-cbc-fixed），不再复用共享 cookiecloudclient 的 FetchCookies；
// 本文件负责配置、HTTP 拉取、解密、凭据提取、验证与激活；
// 解密仅本地，失败保留旧 Cookie，不回写 yaml，不输出密码。
const (
	cookieCloudDefaultInterval = 10 * time.Minute
	cookieCloudMinInterval     = time.Minute
	cookieCloudDefaultTimeout  = 15 * time.Second

	cookieModeQrcode      = "qrcode"
	cookieModeCookieCloud = "cookiecloud"
)

// cookieCloudCookie 是 CookieCloud 同步数据中的单条 Cookie 候选。
type cookieCloudCookie struct {
	Name           string  `json:"name"`
	Value          string  `json:"value"`
	Domain         string  `json:"domain"`
	ExpirationDate float64 `json:"expirationDate"`
}

// cookieCloudConfig 是 bilibili.cookieCloud 配置段。
type cookieCloudConfig struct {
	Endpoint   string
	UUID       string
	Password   string
	CryptoType string
	Interval   time.Duration
	Timeout    time.Duration
}

var (
	// cloudRefreshMu 防止 CookieCloud 拉取重入。
	cloudRefreshMu atomic.Bool
	// cookieCloudStartOnce 保证客户端启动日志只打一次。
	cookieCloudStartOnce sync.Once
)

// currentBilibiliCookieMode 返回B站 Cookie 来源模式：qrcode（默认）或 cookiecloud。
func currentBilibiliCookieMode() string {
	mode := strings.TrimSpace(cfg.GetBilibiliCookieMode())
	if mode == "" {
		return cookieModeQrcode
	}
	return mode
}

// isBilibiliCookieCloudMode 是否启用 CookieCloud 模式。
func isBilibiliCookieCloudMode() bool {
	return strings.EqualFold(currentBilibiliCookieMode(), cookieModeCookieCloud)
}

// loadCookieCloudConfig 读取并校验 bilibili.cookieCloud 配置段。
// 成品 loadCookieCloudConfig @0xd14a60（1504B, cookiecloud.go:74）签名实证：
// 返回值 = (cookieCloudConfig 栈结构 80B) + error 接口(rax=itab 0x112be60、rbx=data)；
// 成功路径 xor eax/ebx 返回 nil，失败路径逐字段 errors.New / fmt.Errorf。
// 5 条 errors.New（rodata 原文）：
//
//	@0x10d197e len 0x27 "bilibili.cookieCloud.endpoint 未配置"
//	@0x10e84a2 len 0x42 "bilibili.cookieCloud.endpoint 必须是有效的 http/https 地址"
//	@0x10cb9f1 len 0x23 "bilibili.cookieCloud.uuid 未配置"
//	@0x10dfd3e len 0x35 "bilibili.cookieCloud.uuid 不能包含路径分隔符"
//	@0x10d19a5 len 0x27 "bilibili.cookieCloud.password 未配置"
//
// 另加 cryptoType 分支的 fmt.Errorf("不支持的 bilibili.cookieCloud.cryptoType: %s")。
// 成品本函数内部无任何 logger 调用：原「B站 Cookie来源已设为 CookieCloud，但配置不完整」
// 文案（@0x1125210, len 59）由 Init 在收到 error 后按 Error 级别打印并 return（@0xcee651）。
//
// 反汇编细节：
//   - endpoint = strings.TrimRight(GetBilibiliCookieCloudEndpoint(), "/")（+0x3f，cutset
//     @0x1098229="/"）；getter 自身已 TrimSpace（cfg @0xae7e40）。
//   - endpoint 空 → 未配置；否则 net/url.Parse（+0x1ae）：err!=nil、parsed.Host==""（0x30(%rax)，
//     Host.len 偏移 0x30）或 Scheme 非 "http"/"https"（(%rax)/0x8 与 4/5 字节比较）→ 同一错误。
//   - uuid 空 → 未配置；strings.IndexAny(uuid, cutset@0x1098830 len 3 = 0x2f 0x5c 0x5c
//     = '/' '\' '\') >= 0 → 不能包含路径分隔符（IndexAny 对重复字符等价，重建写作 "/\\"）。
//   - password 空 → 未配置。
//   - interval = GetDuration 默认 10m、最小 1m（+0x28b..+0x2c7：0x8bb2c97000/0xdf8475800）；
//     timeout = GetDuration 默认 15s（+0x2e6 cmovle 0x37e11d600）。二者与 cfg 的
//     getCookieCloudInterval/getCookieCloudTimeout 语义一致，故仍走 cfg getter。
//   - cryptoType 精确比较 "auto"(4)/"legacy"(6)/"aes-128-cbc-fixed"(0x11)，否则 fmt.Errorf。
func loadCookieCloudConfig() (cookieCloudConfig, error) {
	var cfgConfig cookieCloudConfig
	cfgConfig.Endpoint = strings.TrimRight(cfg.GetBilibiliCookieCloudEndpoint(), "/")
	cfgConfig.UUID = cfg.GetBilibiliCookieCloudUUID()
	cfgConfig.Password = cfg.GetBilibiliCookieCloudPassword()
	cfgConfig.CryptoType = cfg.GetBilibiliCookieCloudCryptoType()
	if cfgConfig.Endpoint == "" {
		return cfgConfig, errors.New("bilibili.cookieCloud.endpoint 未配置")
	}
	parsed, err := url.Parse(cfgConfig.Endpoint)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return cfgConfig, errors.New("bilibili.cookieCloud.endpoint 必须是有效的 http/https 地址")
	}
	if cfgConfig.UUID == "" {
		return cfgConfig, errors.New("bilibili.cookieCloud.uuid 未配置")
	}
	if strings.ContainsAny(cfgConfig.UUID, "/\\") {
		return cfgConfig, errors.New("bilibili.cookieCloud.uuid 不能包含路径分隔符")
	}
	if cfgConfig.Password == "" {
		return cfgConfig, errors.New("bilibili.cookieCloud.password 未配置")
	}
	cfgConfig.Interval = cfg.GetBilibiliCookieCloudInterval()
	cfgConfig.Timeout = cfg.GetBilibiliCookieCloudTimeout()
	switch cfgConfig.CryptoType {
	case cookiecloudclient.CryptoTypeAuto, cookiecloudclient.CryptoTypeLegacy, cookiecloudclient.CryptoTypeAES128CBCFixed:
	default:
		// 成品 @0xd14dca（lea 0x10dbaad,len 0x30）+ fmt.errorf @0x51bb20。
		return cfgConfig, fmt.Errorf("不支持的 bilibili.cookieCloud.cryptoType: %s", cfgConfig.CryptoType)
	}
	return cfgConfig, nil
}

// cookieCloudDownloadURL 拼装 CookieCloud 的下载地址（{endpoint}/get/{uuid}）。
func cookieCloudDownloadURL(config cookieCloudConfig) (string, error) {
	endpoint := strings.TrimSuffix(strings.TrimSpace(config.Endpoint), "/")
	if endpoint == "" || strings.TrimSpace(config.UUID) == "" {
		return "", errors.New("CookieCloud endpoint 或 uuid 为空")
	}
	u := endpoint + "/get/" + strings.TrimSpace(config.UUID)
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return "", fmt.Errorf("生成 CookieCloud 下载地址失败: %w", errors.New("endpoint 必须是 http(s) 地址"))
	}
	return u, nil
}

// cookieCloudServerResponse 是 GET {endpoint}/get/{uuid} 的响应信封。
// 证据：成品类型表 struct { Encrypted string "json:\"encrypted\""; CryptoType string "json:\"crypto_type\"" }
// （recovered/types/types-x64.tsv:7409）。
type cookieCloudServerResponse struct {
	Encrypted  string `json:"encrypted"`
	CryptoType string `json:"crypto_type"`
}

// cookieCloudDecryptedData 是解密后的同步数据外壳：cookie_data 先保留原始 JSON，
// 再二次解析为按域分组的 Cookie。
// 证据：成品类型表 struct { CookieData json.RawMessage "json:\"cookie_data\"" }（types-x64.tsv:7402）。
type cookieCloudDecryptedData struct {
	CookieData stdjson.RawMessage `json:"cookie_data"`
}

// cookieCloudMaxResponseBytes 是 CookieCloud 响应体上限。成品证据（fetchCookieCloudCredentials
// +0x5b4..+0x5e6）：构造 io.LimitedReader 时 N=0x2000001，随后 cmp $0x2000000 判超限，
// 即 32 MiB。
const cookieCloudMaxResponseBytes = 32 << 20

// resolveBilibiliCookieCloudCryptoType 依据配置与服务端标记确定实际加密类型。
// 成品证据（fetchCookieCloudCredentials +0x9b0..+0xa50）：配置值为空或等于 "auto"
// （len==4 且四字节 "auto"）时取 ToLower(TrimSpace(serverMarked))，为空则回落 "legacy"；
// 否则原样使用配置值（不做 trim/lower）。
func resolveBilibiliCookieCloudCryptoType(configured string, serverMarked string) string {
	if configured == "" || configured == cookiecloudclient.CryptoTypeAuto {
		marked := strings.ToLower(strings.TrimSpace(serverMarked))
		if marked == "" {
			return cookiecloudclient.CryptoTypeLegacy
		}
		return marked
	}
	return configured
}

// fetchCookieCloudCredentials 从 CookieCloud 服务端拉取并解密全部 Cookie。
// 解密全部在包内完成（decryptCookieCloud 链），不再委托 cookiecloudclient。
//
// 成品证据（fetchCookieCloudCredentials @0xd15180，cookiecloud.go:126）：
//   - +0xa3  cookieCloudDownloadURL；失败 fmt.Errorf("生成 CookieCloud 下载地址失败: %w")
//     （格式串 @0x10d422a）。
//   - +0x1c5 http.NewRequestWithContext(context.Background(), "GET", url, nil)
//     （方法串 @0x1098824="GET"，ctx itab @0x1136250 + 全局 emptyCtx @0x1bf43a0）；
//     失败 "创建 CookieCloud 请求失败: %w"（@0x10cba14）。
//   - +0x2e0 req.Header.Set("Accept","application/json")（键 @0x109f6a6，值 @0x10af365）；
//     +0x395 req.Header.Set("User-Agent","DDBOT-WSa CookieCloud Client")
//     （键 @0x10a751b，值 @0x10c0b84）。
//   - +0x444 client.Do(req)（首参为 HTTPDoer，由 refresh 注入并已设 Timeout）；
//     失败 "请求 CookieCloud 服务失败: %w"（@0x10cba37）。
//   - +0x488 defer resp.Body.Close()（deferwrap1 @cookiecloud.go:142）；
//     +0x48d cmp $0xc8,resp.StatusCode，非 200 →
//     fmt.Errorf("CookieCloud 服务返回 HTTP %d", code)（@0x10c6fa1）。
//   - +0x5b4..+0x646 io.ReadAll(io.LimitReader(Body, 32MiB+1))；失败
//     "读取 CookieCloud 响应失败: %w"（@0x10cba5a）；len>32MiB →
//     "CookieCloud 响应超过 %d MiB 限制"（@0x10d0159）。
//   - +0x8a0 json.Unmarshal 到 cookieCloudServerResponse；失败
//     "解析 CookieCloud 响应失败: %w"（@0x10cba7d）。
//   - +0x9c0 TrimSpace(Encrypted)，为空 → errors.New("CookieCloud 响应中没有 encrypted 数据")
//     （@0x10d785b，len 44）。
//   - 解析 cryptoType 后 +0xa90 调 decryptCookieCloud(uuid, password, encrypted, cryptoType)。
//   - 成品在包内 extractBilibiliCookieCloudCredentials（cookiecloud.go:255）里再做
//     "解析 CookieCloud 解密数据失败: %w"（@0x10d4253）与
//     "解析 CookieCloud cookie_data 失败: %w"（@0x10d427c）两级解析并提取凭据。
//     本重实现保持既有分层：此处只解出按域分组的 Cookie，凭据提取仍由
//     extractBilibiliCookieCloudCredentials / refreshCookieFromCookieCloud 完成，
//     两级解析错误消息在此按成品原文保留。
//
// [推断] 成品把 HTTP 客户端（Timeout 字段由 refresh 设置）作为首参 HTTPDoer 传入；
// 本实现无注入点，改为用 config.Timeout 就地构造 &http.Client，语义等价。
func fetchCookieCloudCredentials(config cookieCloudConfig) (map[string][]cookieCloudCookie, error) {
	target, err := cookieCloudDownloadURL(config)
	if err != nil {
		return nil, fmt.Errorf("生成 CookieCloud 下载地址失败: %w", err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("创建 CookieCloud 请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "DDBOT-WSa CookieCloud Client")

	timeout := config.Timeout
	if timeout <= 0 {
		timeout = cookieCloudDefaultTimeout
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 CookieCloud 服务失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CookieCloud 服务返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cookieCloudMaxResponseBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取 CookieCloud 响应失败: %w", err)
	}
	if len(body) > cookieCloudMaxResponseBytes {
		return nil, fmt.Errorf("CookieCloud 响应超过 %d MiB 限制", cookieCloudMaxResponseBytes>>20)
	}

	var envelope cookieCloudServerResponse
	if err := stdjson.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("解析 CookieCloud 响应失败: %w", err)
	}
	encrypted := strings.TrimSpace(envelope.Encrypted)
	if encrypted == "" {
		return nil, errors.New("CookieCloud 响应中没有 encrypted 数据")
	}
	cryptoType := resolveBilibiliCookieCloudCryptoType(config.CryptoType, envelope.CryptoType)
	plain, err := decryptCookieCloud(config.UUID, config.Password, encrypted, cryptoType)
	if err != nil {
		return nil, err
	}

	var data cookieCloudDecryptedData
	if err := stdjson.Unmarshal(plain, &data); err != nil {
		return nil, fmt.Errorf("解析 CookieCloud 解密数据失败: %w", err)
	}
	// 成品类型表：map[string][]bilibili.cookieCloudCookie(:6691)、
	// []bilibili.cookieCloudCookie(:1553) —— cookie_data 直接按 cookieCloudCookie 解析。
	cookies := make(map[string][]cookieCloudCookie)
	if len(data.CookieData) != 0 {
		if err := stdjson.Unmarshal(data.CookieData, &cookies); err != nil {
			return nil, fmt.Errorf("解析 CookieCloud cookie_data 失败: %w", err)
		}
	}
	return cookies, nil
}

// —— 包内 CookieCloud 解密链 ——
//
// 成品 linux-x64/DDBOT-Lite 证据（funcs-x64.tsv / cg.py）：
//
//	decryptCookieCloud       @0xd16220 1504B cookiecloud.go:182  唯一调用者 fetchCookieCloudCredentials +0xa90
//	decryptCookieCloudCBC    @0xd16800  704B cookiecloud.go:208  调用者 decryptCookieCloud +0x348/+0x541
//	cookieCloudPKCS7Unpad    @0xd16ac0  384B cookiecloud.go:225  调用者 decryptCookieCloudCBC +0x1b4
//	cookieCloudEVPBytesToKey @0xd16c40  800B cookiecloud.go:241  调用者 decryptCookieCloud +0x2f2
//
// 注意参数顺序与共享 cookiecloudclient 不同：
// decryptCookieCloud(uuid, password, encrypted, cryptoType)、decryptCookieCloudCBC(data, key, iv)。

// decryptCookieCloud 按 cryptoType 解密 CookieCloud 密文。
//
// 寄存器证据：入口 rax:rbx=uuid、rcx:rdi=password、rsi:r8=encrypted、r9:r10=cryptoType。
// passphrase=[16]byte 由 concatbyte3(uuid,"-",password)（+0x6d）→ crypto/md5.Sum（+0x72）→
// 表 @0x10af375 "0123456789abcdef" 手工十六进制编码（+0x8c..+0xea，32 字节缓冲取前 16）
// → stringtoslicebyte(...,16)（+0x120）得到。
func decryptCookieCloud(uuid string, password string, encrypted string, cryptoType string) ([]byte, error) {
	sum := md5.Sum([]byte(uuid + "-" + password))
	passphrase := []byte(hex.EncodeToString(sum[:])[:16])

	switch strings.ToLower(strings.TrimSpace(cryptoType)) {
	case cookiecloudclient.CryptoTypeLegacy:
		// 证据：+0x196 TrimSpace → +0x1b1 base64.StdEncoding.DecodeString；
		// 失败格式串 @0x10d544a（len 0x2a）"CookieCloud legacy Base64 解码失败: %w"。
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
		if err != nil {
			return nil, fmt.Errorf("CookieCloud legacy Base64 解码失败: %w", err)
		}
		// 证据：+0x241 len<16 或 +0x26e memequal(data[:8],"Salted__" @0x10a49d6) 失败 →
		// errors.New(@0x10dab97,len 0x2f) "CookieCloud legacy 密文缺少 Salted__ 标记"。
		if len(data) < 16 || string(data[:8]) != "Salted__" {
			return nil, errors.New("CookieCloud legacy 密文缺少 Salted__ 标记")
		}
		// 证据：+0x2f2 cookieCloudEVPBytesToKey(password=passphrase, salt=data[8:16],
		// keyLen=0x20, ivLen=0x10) → +0x348 decryptCookieCloudCBC(data[16:], key, iv)。
		salt := data[8:16]
		key, iv := cookieCloudEVPBytesToKey(passphrase, salt, 32, aes.BlockSize)
		return decryptCookieCloudCBC(data[16:], key, iv)
	case cookiecloudclient.CryptoTypeAES128CBCFixed:
		// 证据：+0x420 TrimSpace → +0x440 base64.StdEncoding.DecodeString；
		// 失败格式串 @0x10d7887（len 0x2c）"CookieCloud fixed-IV Base64 解码失败: %w"。
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
		if err != nil {
			return nil, fmt.Errorf("CookieCloud fixed-IV Base64 解码失败: %w", err)
		}
		// 证据：+0x500 makeslice(16) 全零 IV；+0x541 decryptCookieCloudCBC(data, passphrase, iv)，
		// 不做长度/Salted__ 校验（长度校验在 CBC 内，见 +0x541 前的边界处理）。
		return decryptCookieCloudCBC(data, passphrase, make([]byte, aes.BlockSize))
	default:
		// 分支判定：+0x16a len==6 且 "legacy"；+0x35c len==0x11 且 "aes-128-cbc-fixed"
		// （@0x10b097a，17 字节）；否则 fmt.Errorf(@0x10dcaa3,len 0x31) 打印原始 cryptoType。
		return nil, fmt.Errorf("CookieCloud 返回了不支持的加密类型: %s", cryptoType)
	}
}

// decryptCookieCloudCBC 执行 AES-CBC 解密并去除 PKCS7 填充。
// 寄存器证据：data=rax:rbx:rcx、key=rdi:rsi:r8、iv=r9:r10:r11（注意 data 在前）。
func decryptCookieCloudCBC(data []byte, key []byte, iv []byte) ([]byte, error) {
	// 证据：+0x2a test len==0；+0x2f test $0xf,len%16!=0 → errors.New(@0x10ca244,len 0x22)
	// "CookieCloud AES 密文长度无效"。
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, errors.New("CookieCloud AES 密文长度无效")
	}
	// 证据：+0xa7 crypto/aes.NewCipher(key)；失败 fmt.Errorf(@0x10d5474,len 0x2a)
	// "创建 CookieCloud AES 解密器失败: %w"。
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("创建 CookieCloud AES 解密器失败: %w", err)
	}
	// 证据：+0x142 runtime.makeslice 申请 len(data)；+0x16e cipher.NewCBCDecrypter(block,iv)；
	// +0x19d 间接调用 CryptBlocks(plain, data)（dst=data 的长度）。
	plain := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, data)
	// 证据：+0x1b4 cookieCloudPKCS7Unpad(plain, 0x10)；失败 fmt.Errorf(@0x10ea333,len 0x47)
	// "CookieCloud 解密失败，请检查 uuid、password 和 cryptoType: %w"。
	out, err := cookieCloudPKCS7Unpad(plain, aes.BlockSize)
	if err != nil {
		return nil, fmt.Errorf("CookieCloud 解密失败，请检查 uuid、password 和 cryptoType: %w", err)
	}
	return out, nil
}

// cookieCloudPKCS7Unpad 校验并去除 PKCS7 填充。
// 寄存器证据：data=rax:rbx:rcx、blockSize=rdi。
func cookieCloudPKCS7Unpad(data []byte, blockSize int) ([]byte, error) {
	// 证据：+0x23 test len==0；+0x3d idiv blockSize 取余非 0 → errors.New(@0x10c0ba0,len 0x1c)
	// "无效的 PKCS7 数据长度"。
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, errors.New("无效的 PKCS7 数据长度")
	}
	// 证据：+0x65 padding=int(data[len-1])；+0x6d padding==0、+0x72 blockSize<padding、
	// +0x77 len<padding → errors.New(@0x10b7e6a,len 0x16) "无效的 PKCS7 填充"。
	padding := int(data[len(data)-1])
	if padding == 0 || padding > blockSize || padding > len(data) {
		return nil, errors.New("无效的 PKCS7 填充")
	}
	// 证据：+0xba..+0xbe 逐字节比较 data[len-padding:] 全等于 padding，否则同一 22 字节错误。
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, errors.New("无效的 PKCS7 填充")
		}
	}
	return data[:len(data)-padding], nil
}

// cookieCloudEVPBytesToKey 按 OpenSSL EVP_BytesToKey(MD5, salt, count=1) 派生 key 与 iv。
// 寄存器证据：password=rax:rbx:rcx、salt=rdi:rsi:r8、keyLen=r9、ivLen=r10。
//
// 证据：+0x5d total=r9+r10；+0x6f makeslice(type,0,total)；循环内固定 3 次
// crypto/md5.(*digest).Write（+0x135/+0x15a/+0x180）：第 1 次写 prev（首轮空切片，
// +0x69 三寄存器置零）、第 2 次写 slice1（+0x7a 载入 0x140/0x148/0x150 = password）、
// 第 3 次写 slice2（+0x9f 载入 0x158/0x160/0x168 = salt）；digest.checkSum（+0x1cd）
// 后 growslice/append 累积；+0x29e 按 keyLen/ivLen 切出 key 与 iv。
func cookieCloudEVPBytesToKey(password []byte, salt []byte, keyLen int, ivLen int) (key []byte, iv []byte) {
	var (
		derived []byte
		prev    []byte
	)
	total := keyLen + ivLen
	for len(derived) < total {
		h := md5.New()
		h.Write(prev)
		h.Write(password)
		h.Write(salt)
		prev = h.Sum(nil)
		derived = append(derived, prev...)
	}
	return derived[:keyLen], derived[keyLen : keyLen+ivLen]
}

// candidate 是 CookieCloud 中同名 Cookie 的候选项。
// 成品证据（recovered/types/types-closure-x64.tsv:9462，字段偏移由 reflect 元数据交叉核对）：
//
//	struct 48 bilibili.candidate struct {
//	    value string; domain string; score int; expiration float64 }
//
// 偏移：value@0、domain@16、score@32、expiration@40。字段名小写，仅包内使用。
type candidate struct {
	value      string
	domain     string
	score      int
	expiration float64
}

// extractBilibiliCookieCloudCredentials 从按域名分组的 Cookie 中提取B站登录凭据。
// 同名 Cookie 出现在多个域时按域匹配度取最优；返回
// SESSDATA、bili_jct、DedeUserID、buvid3。
// 成品 VerifyInfo（88B，5 字段）不含 ac_time_value，CookieCloud 路径也不写配置，
// 故不从 CookieCloud 提取该值。
//
// 成品类型表另有 map[string]bilibili.candidate（types-closure-x64.tsv:6702）与
// []struct{key string; elem bilibili.candidate}（:7612），即成品按 Cookie 名构造
// candidate 后选最优；重建按同一形状实现：遍历时算好 score 并保留当前最优候选。
func extractBilibiliCookieCloudCredentials(cookies map[string][]cookieCloudCookie) (string, string, string, string) {
	candidates := make(map[string]candidate)
	for domain, list := range cookies {
		for _, item := range list {
			if !isBilibiliCookieDomain(domain) && !isBilibiliCookieDomain(item.Domain) {
				continue
			}
			c := candidate{
				value:      item.Value,
				domain:     domain,
				score:      bilibiliCookieDomainScore(domain),
				expiration: item.ExpirationDate,
			}
			current, ok := candidates[item.Name]
			if !ok || c.score > current.score ||
				(c.score == current.score && c.expiration > current.expiration) {
				candidates[item.Name] = c
			}
		}
	}
	pick := func(name string) string {
		return candidates[name].value
	}
	return pick("SESSDATA"), pick("bili_jct"), pick("DedeUserID"), pick("buvid3")
}

// isBilibiliCookieDomain 判断 CookieCloud 的分组域是否属于 bilibili.com。
func isBilibiliCookieDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	if domain == "" {
		return false
	}
	return domain == "bilibili.com" || strings.HasSuffix(domain, ".bilibili.com")
}

// bilibiliCookieDomainScore 计算 Cookie 域匹配度：越具体的域得分越高。
// bilibili.com 与其子域均可用，.bilibili.com 通配域略低于精确域。
func bilibiliCookieDomainScore(domain string) int {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	switch {
	case domain == "bilibili.com":
		return 40
	case domain == ".bilibili.com":
		return 30
	case strings.HasSuffix(domain, ".bilibili.com"):
		return 20
	default:
		return 0
	}
}

// cookieCloudServerHost 提取 CookieCloud endpoint 的 server_host 日志字段。
// 成品三处（refresh @0xd17e14、Init @0xcee883、定时循环 @0xd1888c）均以
// net/url.Parse(endpoint) 的 Host 取值，解析失败回退常量 "invalid"
// （@0x10a30f0，len 7）；不校验 Host 是否为空。
func cookieCloudServerHost(endpoint string) string {
	if u, err := url.Parse(endpoint); err == nil {
		return u.Host
	}
	return "invalid"
}

// refreshCookieFromCookieCloud 从 CookieCloud 拉取并激活B站登录 Cookie。
// 成品签名 refreshCookieFromCookieCloud(@0xd17a80)：80B config 结构走栈
// （0x438(%rsp)），+0x98 把它原样交 fetchCookieCloudCredentials；函数内无
// loadCookieCloudConfig 调用（该函数 callers 只有 Init 一条）。
// config 由 Init 单次 load 后经 startCookieCloudRefresh 传入。
// CookieCloud 模式的 Cookie 仅内存保留，不回写 yaml。
func refreshCookieFromCookieCloud(config cookieCloudConfig) error {
	if !cloudRefreshMu.CompareAndSwap(false, true) {
		return nil
	}
	defer cloudRefreshMu.Store(false)

	cookies, err := fetchCookieCloudCredentials(config)
	if err != nil {
		return err
	}
	sessdata, biliJct, dedeUserID, buvid3 := extractBilibiliCookieCloudCredentials(cookies)
	if sessdata == "" || biliJct == "" {
		logger.Warn("CookieCloud 中未找到 bilibili.com 的 SESSDATA 和 bili_jct，请确认浏览器插件已同步B站Cookie")
		return errors.New("CookieCloud 中未找到B站登录Cookie")
	}
	// "CookieCloud 首次获取成功前…" 日志由 Init 在 SESSDATA/bili_jct 非空时打印
	// （成品 .text 对 @0x1125200 唯一引用在 Init @0xcee582），refresh 内无此日志。
	// 成品 refreshCookieFromCookieCloud(@0xd17a80) 共两次 newVerifyInfoWithCookies：
	//   第一次 +0x539：候选 = (SESSDATA, biliJct, DedeUserID, Buvid3)；若 CookieCloud 未同步
	//     DedeUserID/Buvid3，成品 +0xbb3 会读当前 VerifyInfo(0x1bcc298) 的同名字段回退
	//     （仅当候选槽位为空时拷贝 0x20/0x30）。
	//     随后成品 +0x546 调 xWebInterfaceNavWithVerify(候选) 实时验证登录态。
	if current := getVerify(); current != nil {
		if dedeUserID == "" {
			dedeUserID = current.DedeUserID
		}
		if buvid3 == "" {
			buvid3 = current.Buvid3
		}
	}
	verify := newVerifyInfoWithCookies(sessdata, biliJct, dedeUserID, buvid3)
	// 成品 +0x546 把候选 verify 直接传给 xWebInterfaceNavWithVerify（不再临时改写全局）。
	navResp, err := xWebInterfaceNavWithVerify(verify)
	if err != nil {
		// 成品 +0x58b fmt.Errorf(格式串 @0x10eb948, len 0x4b)
		// "验证 CookieCloud 中的B站Cookie失败，继续使用上一份Cookie: %w"
		return fmt.Errorf("验证 CookieCloud 中的B站Cookie失败，继续使用上一份Cookie: %w", err)
	}
	if navResp.GetCode() != 0 {
		// 成品 +0x5d6 读 resp.Code，非零在 +0x686 fmt.Errorf(格式串 @0x10eeadf, len 0x5a)
		// "CookieCloud 中的B站Cookie验证失败，继续使用上一份Cookie: code=%d message=%s"
		return fmt.Errorf("CookieCloud 中的B站Cookie验证失败，继续使用上一份Cookie: code=%d message=%s", navResp.GetCode(), navResp.GetMessage())
	}
	if !navResp.GetData().GetIsLogin() {
		// 成品 +0x6d8 判 IsLogin，false 在 +0xa49 errors.New(串 @0x10e8ca2, len 0x43)
		// "CookieCloud 中的B站Cookie未登录，继续使用上一份Cookie"
		return errors.New("CookieCloud 中的B站Cookie未登录，继续使用上一份Cookie")
	}
	//   第二次 +0x78e：候选未带 DedeUserID 且 nav 返回 Data.Mid>0 时，用 mid 作为
	//     DedeUserID 重建 VerifyInfo（成品 +0x1b7 读 nav.Data(0x28)->Mid(0x10) 后 FormatInt）。
	if verify.DedeUserID == "" && navResp != nil && navResp.GetData().GetMid() > 0 {
		verify = newVerifyInfoWithCookies(sessdata, biliJct, strconv.FormatInt(navResp.GetData().GetMid(), 10), buvid3)
	}
	// 成品 refreshCookieFromCookieCloud @0xd18486：验证通过并 atomic.StorePointer
	// 后，logrus.(*Entry).WithFields({cookie_source,server_host,crypto_type,cookie_changed})
	// .Log(level=4=Info, "已从 CookieCloud 获取并验证B站Cookie")，随后 acceptBilibiliSelfInfo。
	// cookie_changed 为「新 Cookie 与上一份不同」的布尔字段。
	// 未变化时另打 "CookieCloud 定时获取成功，B站Cookie没有变化"：成品 @0xd17f60
	// 的 ebx=5 实证为 level=5=Debug（非 Trace），带 cookie_source/server_host/crypto_type
	// 三字段（@0x1125540，len 55）。
	previous := bilibiliCookieFingerprint()
	activateBilibiliCookieCloud(verify)
	current := bilibiliCookieFingerprint()
	changed := previous == "" || current != previous
	logger.WithFields(logrus.Fields{
		"cookie_source":  cookieModeCookieCloud,
		"server_host":    cookieCloudServerHost(config.Endpoint),
		"crypto_type":    config.CryptoType,
		"cookie_changed": changed,
	}).Info("已从 CookieCloud 获取并验证B站Cookie")
	markBilibiliCookieHealthy()
	authprompt.ObserveCredential(Site, current)
	if !changed {
		logger.WithFields(logrus.Fields{
			"cookie_source": cookieModeCookieCloud,
			"server_host":   cookieCloudServerHost(config.Endpoint),
			"crypto_type":   config.CryptoType,
		}).Debug("CookieCloud 定时获取成功，B站Cookie没有变化")
	}
	return nil
}

// 成品 refreshCookieFromCookieCloud 无独立的验证包装函数：候选 verify 构造后
// 直接 xWebInterfaceNavWithVerify(verify)（+0x546），失败按错误/业务码/未登录三种
// fmt.Errorf / errors.New 返回，由 cookieCloudRefreshLoop 统一记录。

// activateBilibiliCookieCloud 激活 CookieCloud 同步的 Cookie（仅内存）。
func activateBilibiliCookieCloud(verify *VerifyInfo) {
	if verify == nil {
		return
	}
	atomicVerifyInfo.Store(verify)
	FreshSelfInfo()
}

// startCookieCloudRefresh 启动 CookieCloud 定时刷新协程。
// 成品 startCookieCloudRefresh(@0xd18520, 192B)：入口 cmp 全局 0x1bf4858（sync.Once），
// 未执行则 once.doSlow(@0x4a17e0)；func1(@0xd185e0) 把捕获的 80B config 拷进 func1.1
// 闭包后 runtime.newproc(@0x45f6e0)。首轮同步拉取与失败记录都在 Init，这里只按
// config.Interval 起 ticker 定时循环。
func startCookieCloudRefresh(config cookieCloudConfig) {
	cookieCloudStartOnce.Do(func() {
		// 启动文案由 Init 打印（成品 Init @0xceea1b 原文含结尾 "Cookie"）。
		go cookieCloudRefreshLoop(config)
	})
}

// cookieCloudRefreshLoop 是 CookieCloud 的定时拉取循环。
// 成品 startCookieCloudRefresh.func1.1(@0xd186a0, 1152B)：入口 @0xd186d4 读闭包
// config+0x48 调 time.NewTicker(@0x4ef520)，for range ticker.C（runtime.chanrecv2
// @0x41c1a0，通道关闭即退出）内调 refreshCookieFromCookieCloud(config)（@+0x10c），
// 失败按 level=3=Warn 记 "CookieCloud 定时获取B站Cookie失败，继续使用上一份Cookie"
// （@0xd18abc）。首轮拉取不在循环内，故无 "首次获取B站Cookie失败" 文案。
func cookieCloudRefreshLoop(config cookieCloudConfig) {
	ticker := time.NewTicker(config.Interval)
	defer ticker.Stop()
	for range ticker.C {
		if err := refreshCookieFromCookieCloud(config); err != nil {
			// 成品 startCookieCloudRefresh.func1.1 @0xd186a0：WithFields(
			// {cookie_source,server_host,retry_interval=config.Interval}) 后再并入
			// error 字段（第二次 WithFields @0xd18aa8，即内联的 WithError），
			// Log(level=3=Warn, "CookieCloud 定时获取B站Cookie失败，继续使用上一份Cookie"
			// @0x1125560，len 70)。
			logger.WithFields(logrus.Fields{
				"cookie_source":  cookieModeCookieCloud,
				"server_host":    cookieCloudServerHost(config.Endpoint),
				"retry_interval": config.Interval,
			}).WithError(err).Warn("CookieCloud 定时获取B站Cookie失败，继续使用上一份Cookie")
		}
	}
}
