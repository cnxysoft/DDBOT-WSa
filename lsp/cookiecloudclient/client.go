package cookiecloudclient

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// CryptoTypeAuto 表示跟随服务端返回的加密类型标记自动判断，推荐保持默认。
	CryptoTypeAuto = "auto"
	// CryptoTypeLegacy 是 CookieCloud 原始加密格式：密文为
	// base64("Salted__" + 8 字节 salt + AES-CBC(密文))，即 OpenSSL/CryptoJS 输出格式。
	CryptoTypeLegacy = "legacy"
	// CryptoTypeAES128CBCFixed 是固定 IV 变体，密文整体 base64，key/iv 均由密码派生。
	CryptoTypeAES128CBCFixed = "aes-128-cbc-fixed"

	// DefaultTimeout 是未配置单次拉取超时时使用的默认值。
	DefaultTimeout = 15 * time.Second

	// UserAgent 是访问 CookieCloud 服务端时使用的 UA。
	UserAgent = "DDBOT-WSa CookieCloud Client"
)

var (
	uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

	ErrEndpointRequired   = errors.New("CookieCloud endpoint 不能为空")
	ErrEndpointInvalid    = errors.New("CookieCloud endpoint 必须是 http(s) 完整地址")
	ErrUUIDRequired       = errors.New("CookieCloud uuid 不能为空")
	ErrUUIDInvalid        = errors.New("CookieCloud uuid 格式无效")
	ErrPasswordRequired   = errors.New("CookieCloud password 不能为空")
	ErrCryptoTypeUnknown  = errors.New("CookieCloud cryptoType 只支持 auto、legacy 或 aes-128-cbc-fixed")
	ErrTimeoutNegative    = errors.New("CookieCloud timeout 不能为负数")
	ErrEncryptedEmpty     = errors.New("CookieCloud 返回的密文为空")
	ErrLegacySaltMissing  = errors.New("CookieCloud legacy 密文缺少 Salted__ 标记")
	ErrAESPayloadShort    = errors.New("CookieCloud AES 密文长度无效")
	ErrPKCS7DataLength    = errors.New("无效的 PKCS7 数据长度")
	ErrPKCS7Padding       = errors.New("无效的 PKCS7 填充")
	ErrCryptoTypeNotMatch = errors.New("CookieCloud 返回了不支持的加密类型")
)

// Config 是访问 CookieCloud 服务端所需的全部参数。
// interval 不在此处配置：拉取节奏由站点侧调度器控制。
type Config struct {
	Endpoint   string
	UUID       string
	Password   string
	CryptoType string
	Timeout    time.Duration
	// Label 是调用方的配置段名（如 "weibo.cookieCloud"），用于拼接 Validate 的错误文案。
	// 成品 Config 字段顺序（client.go:41 反汇编）：Endpoint/UUID/Password/CryptoType/Timeout/Label。
	Label string
}

// Validate 校验配置合法性，空 cryptoType 视为 auto。
// 成品 Config.Validate（client.go:41）共 5 条 fmt.Errorf，全部以 Label 前缀拼接；
// rodata 原文与长度：
//
//	"%s.endpoint 必须是有效的 http/https 地址" 0x10dc25d len 0x30（空 endpoint 与非法
//	    endpoint 共用：url.Parse + Scheme http/https + Host 非空）
//	"%s.uuid 未配置"                        0x10b17e3 len 0x11
//	"%s.uuid 不能包含路径分隔符"              0x10cc383 len 0x23（strings.IndexAny(uuid,"/\\")）
//	"%s.password 未配置"                    0x10b7470 len 0x15
//	"不支持的 %s.cryptoType: %s"             0x10c4a2d len 0x1e（第二个 %s 取原始 CryptoType）
//
// 成品无 uuid 正则格式校验、无 Timeout 负数校验（相关 Err* 变量为重建保留，不再参与 Validate）。
func (c Config) Validate() error {
	if strings.TrimSpace(c.Endpoint) == "" {
		return fmt.Errorf("%s.endpoint 必须是有效的 http/https 地址", c.Label)
	}
	u, err := url.Parse(c.Endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s.endpoint 必须是有效的 http/https 地址", c.Label)
	}
	if strings.TrimSpace(c.UUID) == "" {
		return fmt.Errorf("%s.uuid 未配置", c.Label)
	}
	if strings.IndexAny(c.UUID, "/\\") >= 0 {
		return fmt.Errorf("%s.uuid 不能包含路径分隔符", c.Label)
	}
	if c.Password == "" {
		return fmt.Errorf("%s.password 未配置", c.Label)
	}
	switch c.cryptoType() {
	case CryptoTypeAuto, CryptoTypeLegacy, CryptoTypeAES128CBCFixed:
	default:
		return fmt.Errorf("不支持的 %s.cryptoType: %s", c.Label, c.CryptoType)
	}
	return nil
}

// cryptoType 返回归一化后的加密类型，空值视为 auto。
func (c Config) cryptoType() string {
	v := strings.ToLower(strings.TrimSpace(c.CryptoType))
	if v == "" {
		return CryptoTypeAuto
	}
	return v
}

// Cookie 是 CookieCloud 同步数据中的单条 Cookie，字段与浏览器导出格式一致。
type Cookie struct {
	Name           string  `json:"name"`
	Value          string  `json:"value"`
	Domain         string  `json:"domain"`
	Path           string  `json:"path"`
	ExpirationDate float64 `json:"expirationDate"`
	Secure         bool    `json:"secure"`
	HTTPOnly       bool    `json:"httpOnly"`
}

// HTTPDoer 允许调用方注入自定义 HTTP 客户端（例如走代理的 client），便于测试与复用连接。
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// serverResponse 是 GET {endpoint}/get/{uuid} 的响应信封。
// crypto_type 由服务端标记本次数据的加密格式，cryptoType: auto 时以它为准。
type serverResponse struct {
	Encrypted  string `json:"encrypted"`
	CryptoType string `json:"crypto_type"`
}

// serverPayload 是解密后的同步数据，cookie_data 按域名分组。
type serverPayload struct {
	ServerHost string              `json:"server_host"`
	CookieData map[string][]Cookie `json:"cookie_data"`
	CryptoType string              `json:"crypto_type"`
}

// FetchCookies 从 CookieCloud 服务端拉取并解密全部 Cookie，返回按域名分组的结果。
// doer 允许调用方注入自定义 HTTP 客户端；传 nil 时使用包内默认客户端。
func FetchCookies(cfg Config, doer HTTPDoer) (map[string][]Cookie, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	endpoint := strings.TrimSuffix(strings.TrimSpace(cfg.Endpoint), "/")
	target := endpoint + "/get/" + strings.TrimSpace(cfg.UUID)

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("构建 CookieCloud 请求失败: %w", err)
	}
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Accept", "application/json")

	var resp *http.Response
	if doer != nil {
		resp, err = doer.Do(req)
	} else {
		client := &http.Client{Timeout: timeout}
		resp, err = client.Do(req)
	}
	if err != nil {
		return nil, fmt.Errorf("请求 CookieCloud 服务端失败: %w", err)
	}
	defer resp.Body.Close()

	var envelope serverResponse
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("解析 CookieCloud 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CookieCloud 服务端返回 HTTP %d", resp.StatusCode)
	}
	if strings.TrimSpace(envelope.Encrypted) == "" {
		return nil, ErrEncryptedEmpty
	}

	cryptoType, err := resolveCryptoType(cfg.cryptoType(), envelope.CryptoType)
	if err != nil {
		return nil, err
	}

	plain, err := decrypt([]byte(envelope.Encrypted), cfg.Password, cfg.UUID, cryptoType)
	if err != nil {
		return nil, fmt.Errorf("CookieCloud 解密失败，请检查 uuid、password 和 cryptoType: %w", err)
	}

	var payload serverPayload
	if err := json.Unmarshal(plain, &payload); err != nil {
		return nil, fmt.Errorf("解析 CookieCloud 同步数据失败: %w", err)
	}
	if payload.CookieData == nil {
		payload.CookieData = make(map[string][]Cookie)
	}
	return payload.CookieData, nil
}

// resolveCryptoType 根据配置与服务端标记确定实际使用的加密类型。
func resolveCryptoType(configured string, serverMarked string) (string, error) {
	if configured != CryptoTypeAuto {
		return configured, nil
	}
	marked := strings.ToLower(strings.TrimSpace(serverMarked))
	switch marked {
	case "":
		// 老服务端没有标记，按原始格式处理
		return CryptoTypeLegacy, nil
	case CryptoTypeLegacy, CryptoTypeAES128CBCFixed:
		return marked, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrCryptoTypeNotMatch, serverMarked)
	}
}

// legacy（OpenSSL/CryptoJS）密文格式常量。
// 证据：成品 decrypt 内 memequal(data[:8], "Salted__")（常量 @0x10a49d6，8 字节），
// 命中后取 data[8:16] 为 salt、data[16:] 为密文；EVP 调用点 keyLen=0x20、ivLen=0x10。
const (
	legacySaltPrefix = "Salted__"
	legacySaltSize   = 8
	legacyEVPKeyLen  = 32
	legacyEVPIVLen   = aes.BlockSize
)

// decrypt 按 cryptoType 解密 CookieCloud 密文，uuid 与 password 共同参与密钥派生。
//
// legacy 分支按成品 disasm（client.go:137，符号 0xb91e00）对齐：
//
//	passphrase = []byte(hex(md5(uuid + "-" + password))[:16])
//	data       = base64.StdEncoding.DecodeString(strings.TrimSpace(encrypted))
//	if len(data) < 16 || string(data[:8]) != "Salted__" → ErrLegacySaltMissing
//	salt       = data[8:16]
//	key, iv    = evpBytesToKey(passphrase, salt, 32, 16)
//	return decryptCBC(key, iv, data[16:])
func decrypt(encrypted []byte, password string, uuid string, cryptoType string) ([]byte, error) {
	switch cryptoType {
	case CryptoTypeLegacy:
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encrypted)))
		if err != nil {
			return nil, fmt.Errorf("解析 CookieCloud 密文失败: %w", err)
		}
		if len(data) < legacySaltSize*2 || string(data[:legacySaltSize]) != legacySaltPrefix {
			return nil, ErrLegacySaltMissing
		}
		salt := data[legacySaltSize : legacySaltSize*2]
		key, iv := evpBytesToKey(cookieCloudLegacyPassphrase(uuid, password), salt, legacyEVPKeyLen, legacyEVPIVLen)
		return decryptCBC(key, iv, data[legacySaltSize*2:])
	case CryptoTypeAES128CBCFixed:
		// 成品证据（decrypt @0xb91e00 的 fixed 分支）：不走 EVP、不用盐——
		// key = []byte(hex(md5(uuid+"-"+password))[:16])（16 字节），
		// iv  = make([]byte, 16)（全 0；b92206 makeslice 后无 memmove），AES-128-CBC。
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encrypted)))
		if err != nil {
			return nil, fmt.Errorf("解析 CookieCloud 密文失败: %w", err)
		}
		return decryptCBC(cookieCloudLegacyPassphrase(uuid, password), make([]byte, aes.BlockSize), data)
	default:
		return nil, fmt.Errorf("%w: %s", ErrCryptoTypeNotMatch, cryptoType)
	}
}

// cookieCloudLegacyPassphrase 复刻 CookieCloud（CryptoJS）legacy 口令：
// hex(md5(uuid + "-" + password))[:16]。
// 证据：成品 decrypt 入口 concatbyte3(uuid, "-", password) → crypto/md5.Sum →
// 用 0x10af375 的 "0123456789abcdef" 表十六进制编码 → stringtoslicebyte(...,16)（b91e4d..b91f20）。
// uuid 与 password 均按调用点原值拼接（decrypt 入口未先 TrimSpace）。
func cookieCloudLegacyPassphrase(uuid string, password string) []byte {
	material := []byte(uuid + "-" + password)
	sum := md5.Sum(material)
	hexSum := hex.EncodeToString(sum[:])
	return []byte(hexSum[:16])
}

// decryptCBC 执行 AES-128-CBC 解密并去除 PKCS7 填充。
func decryptCBC(key []byte, iv []byte, data []byte) ([]byte, error) {
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, ErrAESPayloadShort
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(data))
	mode := cipher.NewCBCDecrypter(block, iv)
	mode.CryptBlocks(plain, data)
	return pkcs7Unpad(plain, aes.BlockSize)
}

// evpBytesToKey 按 OpenSSL EVP_BytesToKey(MD5, salt, count=1) 规则从口令与 salt 派生 key 与 iv。
// 成品证据（client.go:184，符号 0xb92560）：循环内固定 3 次 crypto/md5.(*digest).Write
// （+0x135/+0x15a/+0x180），第 1 个写 prev（首轮为空切片）、第 2 个写 password、第 3 个写 salt；
// keyLen/ivLen 为 int 参数（legacy 调用点 b92051 mov $0x20,%r9d、b92057 mov $0x10,%r10d → 32/16）。
// salt 为 nil 时该次 Write 不贡献字节，等价于无盐派生。
func evpBytesToKey(password []byte, salt []byte, keyLen int, ivLen int) (key []byte, iv []byte) {
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

// pkcs7Unpad 校验并去除 PKCS7 填充。
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		return nil, ErrPKCS7DataLength
	}
	padding := int(data[len(data)-1])
	if padding <= 0 || padding > blockSize || padding > len(data) {
		return nil, ErrPKCS7Padding
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			return nil, ErrPKCS7Padding
		}
	}
	return data[:len(data)-padding], nil
}
