package bilibili

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

// PreparedCredentials owns a verified candidate. Preparing never changes the
// credentials used by other requests; Apply performs no network or config IO.
var transactionalReloadEnabled atomic.Bool

func SetTransactionalReloadEnabled() { transactionalReloadEnabled.Store(true) }

type PreparedCredentials struct {
	verify  *VerifyInfo
	nav     *WebInterfaceNavResponse
	account *accountCredentials
}

func PrepareCredentials(sessdata, biliJct string) (*PreparedCredentials, error) {
	return prepareCredentials(sessdata, biliJct, fetchCandidateNav)
}

func prepareCredentials(sessdata, biliJct string,
	fetch func(*VerifyInfo) (*WebInterfaceNavResponse, error)) (*PreparedCredentials, error) {
	if strings.TrimSpace(sessdata) == "" && strings.TrimSpace(biliJct) == "" {
		return &PreparedCredentials{verify: new(VerifyInfo)}, nil
	}
	if strings.TrimSpace(sessdata) == "" || strings.TrimSpace(biliJct) == "" {
		return nil, fmt.Errorf("B站 SESSDATA 和 bili_jct 必须同时提供")
	}
	if strings.ContainsAny(sessdata+biliJct, "\r\n;") {
		return nil, fmt.Errorf("B站 Cookie 值不得包含换行或分号")
	}
	verify := &VerifyInfo{
		SESSDATA: sessdata, BiliJct: biliJct,
		VerifyOpts: []requests.Option{
			requests.CookieOption("SESSDATA", sessdata), requests.CookieOption("bili_jct", biliJct),
		},
	}
	nav, err := fetch(verify)
	if err != nil {
		return nil, fmt.Errorf("B站 Cookie 验证请求失败，请检查网络后重试")
	}
	if nav == nil || nav.GetCode() != 0 || !nav.GetData().GetIsLogin() {
		return nil, fmt.Errorf("B站 Cookie 验证失败，请重新登录")
	}
	return &PreparedCredentials{verify: verify, nav: nav}, nil
}

func fetchCandidateNav(verify *VerifyInfo) (*WebInterfaceNavResponse, error) {
	opts := []requests.Option{
		requests.ProxyOption(proxy_pool.PreferNone), requests.TimeoutOption(15 * time.Second), AddUAOption(),
	}
	opts = append(opts, verify.VerifyOpts...)
	response := new(WebInterfaceNavResponse)
	if err := requests.Get(BPath(PathXWebInterfaceNav), nil, response, opts...); err != nil {
		return nil, err
	}
	return response, nil
}

func (p *PreparedCredentials) Apply() {
	if p == nil {
		return
	}
	credentialPublishMu.Lock()
	defer credentialPublishMu.Unlock()
	if p.account != nil {
		atomicAccount.Store(p.account)
	}
	accountUid.Store(p.nav.GetData().GetMid())
	if image := p.nav.GetData().GetWbiImg(); image != nil {
		wbi.Store(image)
	}
	atomicVerifyInfo.Store(p.verify)
	if p.nav.GetData().GetIsLogin() {
		markBilibiliLoginRecovered(bilibiliLoginSourceSelf)
	}
}

// PrepareRuntimeReload prepares both cookie and password-login configuration as
// a single generation. Account login uses only the candidate, never live state.
func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	changed := false
	for _, key := range []string{"bilibili.SESSDATA", "bilibili.bili_jct", "bilibili.account", "bilibili.password"} {
		if previous.GetString(key) != next.GetString(key) {
			changed = true
			break
		}
	}
	if !changed {
		return nil, nil, nil
	}
	mux.Lock()
	prepared, err := prepareRuntimeCredentials(next, Login, fetchCandidateNav)
	if err != nil {
		mux.Unlock()
		return nil, nil, err
	}
	return func() { prepared.Apply(); mux.Unlock() }, mux.Unlock, nil
}

func prepareRuntimeCredentials(next *config.Snapshot, login func(string, string) (*LoginResponse, error),
	fetch func(*VerifyInfo) (*WebInterfaceNavResponse, error)) (*PreparedCredentials, error) {
	sessdata, biliJct := next.GetString("bilibili.SESSDATA"), next.GetString("bilibili.bili_jct")
	account := &accountCredentials{username: next.GetString("bilibili.account"), password: next.GetString("bilibili.password")}
	if sessdata == "" && biliJct == "" && (account.username != "" || account.password != "") {
		if account.username == "" || account.password == "" {
			return nil, fmt.Errorf("B站 account 和 password 必须同时提供")
		}
		response, err := login(account.username, account.password)
		if err != nil || response.GetCode() != 0 || response.GetData().GetStatus() != 0 {
			return nil, fmt.Errorf("B站账号验证失败，请检查凭据和网络")
		}
		for _, cookie := range response.GetData().GetCookieInfo().GetCookies() {
			switch cookie.GetName() {
			case "SESSDATA":
				sessdata = cookie.GetValue()
			case "bili_jct":
				biliJct = cookie.GetValue()
			}
		}
		if sessdata == "" || biliJct == "" {
			return nil, fmt.Errorf("B站登录未返回完整的 Cookie")
		}
	}
	prepared, err := prepareCredentials(sessdata, biliJct, fetch)
	if err != nil {
		return nil, err
	}
	prepared.account = account
	return prepared, nil
}
