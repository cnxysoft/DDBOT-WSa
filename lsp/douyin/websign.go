package douyin

import (
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// webSign 常量。成品里 x-secsdk-web-signature 不作为 HTTP 头出现，
// 而是作为 URL 查询参数名（appendDouyinWebSign 0xd9e885 处
// appendDouyinRawQueryParam(rawURL, "x-secsdk-web-signature", hex)）。
const (
	douyinWebSignHeader         = "x-secsdk-web-signature"
	douyinWebSignStrategy       = "byted-secsdk-strategy-1.0.40"
	douyinWebSignUIFIDParam     = "uifid"
	douyinWebSignTimestampParam = "timestamp"
	douyinWebSignMode           = "web_sign_runtime"
)

// douyinWebSignSalt 签名盐（成品 .rodata 0x10c70e1，32 字节）。
const douyinWebSignSalt = "A96D855A08C0A9707F8BEF0D9A527E4E"

// appendDouyinWebSign 为请求 URL 追加网页签名参数并返回（成品 0xd9e520 逐指令复原）。
//
// 流程：解析 URL → firstDouyinWebSignUIFID 取 UIFID（取不到则原样返回）→
// 请求已带非空 uifid 查询参数则原样返回 → 追加 uifid=<uifid> →
// 追加 timestamp=<Unix 秒>（成品无 ×1000，0xE7791F700=62135596800 证实为秒）→
// 重新解析取 RawQuery →
// sign = hex(md5(uifid + "_" + ts + "_" + 盐 + "_" + RawQuery)) →
// 追加 x-secsdk-web-signature=<sign>。
func appendDouyinWebSign(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	uifid := firstDouyinWebSignUIFID(u)
	if uifid == "" {
		return rawURL
	}
	if u.Query().Get(douyinWebSignUIFIDParam) != "" {
		return rawURL
	}
	signed := appendDouyinRawQueryParam(rawURL, douyinWebSignUIFIDParam, uifid)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	signed = appendDouyinRawQueryParam(signed, douyinWebSignTimestampParam, ts)
	u2, err := url.Parse(signed)
	if err != nil {
		return signed
	}
	sum := md5.Sum([]byte(uifid + "_" + ts + "_" + douyinWebSignSalt + "_" + u2.RawQuery))
	signature := hex.EncodeToString(sum[:])
	logger.WithField("request_type", douyinRequestTypeWebSignRuntime).
		WithField("signature_mode", douyinWebSignMode).
		Tracef("抖音网页签名参数已追加")
	return appendDouyinRawQueryParam(signed, douyinWebSignHeader, signature)
}

// firstDouyinWebSignUIFID 按成品 0xd9e900（websign.go:51，512 字节）复原：
// 先从 URL 自带查询参数找（TrimSpace 后非空即返回）：
//
//	uifid → uifid_temp → uifidtemp
//
// 再从 Cookie 罐找（同样 TrimSpace 后非空即返回）：
//
//	UIFID → UIFID_TEMP → UIFIDTEMP → uifid → uifid_temp → uifidtemp
//
// 都没有则返回空串。成品不访问首页 HTML，uifid 只能来自查询参数或 Cookie
// （Cookie 由配置 douyin.cookie、状态文件或响应 Set-Cookie 进入罐）。
func firstDouyinWebSignUIFID(u *url.URL) string {
	values := u.Query()
	for _, key := range []string{"uifid", "uifid_temp", "uifidtemp"} {
		if v := strings.TrimSpace(values.Get(key)); v != "" {
			return v
		}
	}
	for _, key := range []string{"UIFID", "UIFID_TEMP", "UIFIDTEMP", "uifid", "uifid_temp", "uifidtemp"} {
		if v := douyinBrowserCookieValue(key, ""); v != "" {
			return v
		}
	}
	return ""
}

// appendDouyinRawQueryParam 以原始文本形式追加查询参数，
// 不做 URL 编码（抖音部分参数要求原样透传）。
func appendDouyinRawQueryParam(rawURL string, key string, value string) string {
	if rawURL == "" {
		return rawURL
	}
	sep := "&"
	if !strings.Contains(rawURL, "?") {
		sep = "?"
	}
	if strings.HasSuffix(rawURL, "?") || strings.HasSuffix(rawURL, "&") {
		sep = ""
	}
	return rawURL + sep + key + "=" + value
}
