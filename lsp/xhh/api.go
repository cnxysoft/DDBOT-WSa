package xhh

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

const (
	APIHost = "https://api.xiaoheihe.cn"
	PathProfileEvents = "/bbs/app/profile/events"
)

const charTable = "AB45STUVWZEFGJ6CH01D237IXYPQRKLMN89"

// Vm 位运算函数 - 来自JS算法
func Vm(e int) int {
	if e&128 != 0 {
		return 255 & ((e << 1) ^ 27)
	}
	return (e << 1) & 0xFF
}

func qm(e int) int {
	return Vm(e) ^ e
}

func dollarM(e int) int {
	return qm(Vm(e))
}

func Ym(e int) int {
	return dollarM(qm(Vm(e)))
}

func Gm(e int) int {
	return Ym(e) ^ dollarM(e) ^ qm(e)
}

func Km(e []int) []int {
	t := []int{0, 0, 0, 0}
	t[0] = Gm(e[0]) ^ Ym(e[1]) ^ dollarM(e[2]) ^ qm(e[3])
	t[1] = qm(e[0]) ^ Gm(e[1]) ^ Ym(e[2]) ^ dollarM(e[3])
	t[2] = dollarM(e[0]) ^ qm(e[1]) ^ Gm(e[2]) ^ Ym(e[3])
	t[3] = Ym(e[0]) ^ dollarM(e[1]) ^ qm(e[2]) ^ Gm(e[3])
	e[0] = t[0]
	e[1] = t[1]
	e[2] = t[2]
	e[3] = t[3]
	return e
}

// av 字符映射函数
func av(e string, t string, n int) string {
	var i string
	if n < 0 {
		i = t[:len(t)+n]
	} else {
		i = t[:n]
	}
	result := ""
	for o := 0; o < len(e); o++ {
		idx := int(e[o]) % len(i)
		result += string(i[idx])
	}
	return result
}

// sv 字符映射函数
func sv(e string, t string) string {
	result := ""
	for i := 0; i < len(e); i++ {
		idx := int(e[i]) % len(t)
		result += string(t[idx])
	}
	return result
}

// interleave 字符串交织
func interleave(strs []string) string {
	maxLen := 0
	for _, s := range strs {
		if len(s) > maxLen {
			maxLen = len(s)
		}
	}
	result := ""
	for i := 0; i < maxLen; i++ {
		for _, s := range strs {
			if i < len(s) {
				result += string(s[i])
			}
		}
	}
	return result
}

// md5Hash 计算MD5
func md5Hash(s string) string {
	h := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", h)
}

// GenerateNonce 生成32字符hex随机字符串
func GenerateNonce() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// GenerateHkey 生成hkey
// path: API路径如 /bbs/app/profile/events
// t: Unix时间戳
// nonce: 32字符hex随机字符串
func GenerateHkey(path string, t int64, nonce string) string {
	if len(path) == 0 {
		path = "/"
	}
	if path[0] != '/' {
		path = "/" + path
	}
	if path[len(path)-1] != '/' {
		path = path + "/"
	}

	tStr := fmt.Sprintf("%d", t)
	avResult := av(tStr, charTable, -2)
	svPath := sv(path, charTable)
	svNonce := sv(nonce, charTable)

	combined := interleave([]string{avResult, svPath, svNonce})
	if len(combined) > 20 {
		combined = combined[:20]
	}

	md5Result := md5Hash(combined)

	first5 := md5Result[:5]
	last6 := md5Result[len(md5Result)-6:]
	last6Codes := []int{}
	for _, c := range last6 {
		last6Codes = append(last6Codes, int(c))
	}

	kmInput := []int{last6Codes[0], last6Codes[1], last6Codes[2], last6Codes[3]}
	kmResult := Km(kmInput)
	sum := 0
	for _, v := range kmResult {
		sum += v
	}
	sum += last6Codes[4] + last6Codes[5]
	checksum := sum % 100
	checksumStr := fmt.Sprintf("%d", checksum)
	if len(checksumStr) < 2 {
		checksumStr = "0" + checksumStr
	}

	firstPart := av(first5, charTable, -4)
	return firstPart + checksumStr
}

// GetProfileEvents 获取用户动态列表
// token: x_xhh_tokenid，如果为空则使用 smidv2
func GetProfileEvents(token, userid string) (*EventsResponse, error) {
	t := time.Now().Unix()
	nonce := GenerateNonce()
	hkey := GenerateHkey(PathProfileEvents, t-5, nonce) // offset -5

	params := map[string]interface{}{
		"os_type":      "web",
		"app":          "heybox",
		"client_type":  "web",
		"version":      "999.0.4",
		"web_version":  "2.5",
		"x_client_type": "web",
		"x_app":        "heybox_website",
		"x_os_type":    "Windows",
		"hkey":         hkey,
		"_time":        t,
		"nonce":        nonce,
		"list_type":    "moment",
		"userid":       userid,
		"dw":           628,
	}

	opts := []requests.Option{
		requests.CookieOption("x_xhh_tokenid", token),
		requests.AddUAOption(),
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second * 10),
	}

	var resp EventsResponse
	err := requests.Get(APIHost+PathProfileEvents, params, &resp, opts...)
	if err != nil {
		return nil, err
	}

	return &resp, nil
}
