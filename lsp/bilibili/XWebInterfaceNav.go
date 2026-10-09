package bilibili

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/cnxysoft/DDBOT-WSa/utils"
	"github.com/samber/lo"
)

const PathXWebInterfaceNav = "/x/web-interface/nav"

func refreshNavWbi() {
	resp, err := XWebInterfaceNav(false)
	if err != nil {
		logger.Errorf("bilibili: refreshNavWbi error %v", err)
		return
	}
	wbiImg := resp.GetData().GetWbiImg()
	if wbiImg != nil {
		wbi.Store(wbiImg)
	}
	logger.Trace("bilibili: refreshNavWbi ok")
}

func getWbi() (imgKey string, subKey string) {
	wbi := wbi.Load()
	getKey := func(url string) string {
		path, _ := lo.Last(strings.Split(url, "/"))
		key := strings.Split(path, ".")[0]
		return key
	}
	if wbi != nil {
		imgKey = getKey(wbi.ImgUrl)
		subKey = getKey(wbi.SubUrl)
	}
	return
}
func getMixinKey(orig string) string {
	var str strings.Builder
	for _, v := range mixinKeyEncTab {
		if v < len(orig) {
			str.WriteByte(orig[v])
		}
	}
	return str.String()[:32]
}

func signWbi(params map[string]string) map[string]string {
	imgKey, subKey := getWbi()
	if imgKey == "" && subKey == "" {
		logger.Errorf("bilibili: get wbi fail")
		return params
	}
	mixinKey := getMixinKey(imgKey + subKey)
	currTime := strconv.FormatInt(time.Now().Unix(), 10)
	params["wts"] = currTime
	urlParams := url.Values{}
	for key, value := range params {
		urlParams.Add(key, fmt.Sprintf("%v", value))
	}
	query := urlParams.Encode()
	hash := md5.Sum([]byte(query + mixinKey))
	params["w_rid"] = hex.EncodeToString(hash[:])
	return params
}

// XWebInterfaceNav 请求 x/web-interface/nav。
// login 为 true 时要求已配置 cookie 凭据，并把当前 VerifyInfo 一起下传。
// 成品 XWebInterfaceNav(XWebInterfaceNav.go:75, @0xcedcc0) 反汇编（@0xcedcce..0xcedcf0）：
//
//	test al,al（@0xcedcce）→ 未登录直接走 verify=nil；
//	登录时只读全局 0x1bcc298（atomicVerifyInfo.Load()）：rdx==nil（@0xcedce0）
//	  或 cmpq $0x0,0x48(%rdx)（len(VerifyOpts)==0，@0xcedce5）→ 返回 (nil, ErrVerifyRequired)
//	  （0x1b90e30/0x1b90e38 全局 error 接口，@0xcedcf0）；
//	末尾 rax=login、rbx=verify 调 xWebInterfaceNav（@0xcedd18）。
//
// 即只判 cookie（VerifyOpts），不读 IsAccountGiven()；账号登录路径在 GetVerifyInfo 里先
// SetVerify→newVerifyInfoWithCookies 写入 VerifyOpts 后才调 FreshSelfInfo，故不受影响。
func XWebInterfaceNav(login bool) (*WebInterfaceNavResponse, error) {
	if login && !IsCookieGiven() {
		return nil, ErrVerifyRequired
	}
	var verify *VerifyInfo
	if login {
		verify = getVerify()
	}
	return xWebInterfaceNav(login, verify)
}

// xWebInterfaceNavWithVerify 带 verify 参数的 nav 请求。
// 成品 xWebInterfaceNavWithVerify(XWebInterfaceNav.go:86, @0xcedd40) 反汇编：
//
//	test rax,rax / cmpq $0,0x48(rax) → nil 或 VerifyOpts 为空即 ErrVerifyRequired；
//	否则 mov rax,rbx; mov $1,eax; call xWebInterfaceNav（login=true, verify=入参）。
func xWebInterfaceNavWithVerify(verify *VerifyInfo) (*WebInterfaceNavResponse, error) {
	if verify == nil || len(verify.VerifyOpts) == 0 {
		return nil, ErrVerifyRequired
	}
	return xWebInterfaceNav(true, verify)
}

// xWebInterfaceNav 是 nav 请求的公共实现：login 为 true 且 verify 非空时附带其 VerifyOpts。
// 成品 xWebInterfaceNav(:93, @0xcedda0) 反汇编：入参 al=login、rbx=verify；
// movzbl 0x120(%rsp),%eax / test al,al → 非登录跳过；再 test verify,verify → nil 跳过；
// 仅两者同时成立才追加 verify.VerifyOpts（+0x40 ptr/+0x48 len append）。
func xWebInterfaceNav(login bool, verify *VerifyInfo) (*WebInterfaceNavResponse, error) {
	st := time.Now()
	defer func() {
		ed := time.Now()
		logger.WithField("FuncName", utils.FuncName()).Tracef("cost %v", ed.Sub(st))
	}()
	path := BPath(PathXWebInterfaceNav)
	var opts = []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone),
		requests.TimeoutOption(time.Second * 15),
		AddUAOption(),
		delete412ProxyOption,
	}
	if login && verify != nil {
		opts = append(opts, verify.VerifyOpts...)
	}
	xwin := new(WebInterfaceNavResponse)
	err := requests.Get(path, nil, xwin, opts...)
	if err != nil {
		return nil, err
	}
	return xwin, nil
}
