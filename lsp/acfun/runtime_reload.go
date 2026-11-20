package acfun

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/requests"
)

type preparedCredentials struct {
	verify  *VerifyInfo
	account *accountCredentials
	uid     int64
}

func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	keys := []string{"acfun.authKey", "acfun.acPassToken", "acfun.account", "acfun.password"}
	changed := false
	for _, key := range keys {
		if previous.GetString(key) != next.GetString(key) {
			changed = true
			break
		}
	}
	if !changed {
		return nil, nil, nil
	}
	mux.Lock()
	prepared, err := prepareCredentials(next, Login, getUserPersonalInfoWithOptions)
	if err != nil {
		mux.Unlock()
		return nil, nil, err
	}
	return func() {
		publishMu.Lock()
		atomicAccount.Store(prepared.account)
		atomicVerifyInfo.Store(prepared.verify)
		accountUid.Store(prepared.uid)
		publishMu.Unlock()
		mux.Unlock()
	}, mux.Unlock, nil
}

func prepareCredentials(s *config.Snapshot, login func(string, string) (*LoginResponse, error),
	validate func([]requests.Option) (*UserResponse, error)) (*preparedCredentials, error) {
	prepared := &preparedCredentials{verify: new(VerifyInfo), account: &accountCredentials{
		username: s.GetString("acfun.account"), password: s.GetString("acfun.password"),
	}}
	authKey, token := s.GetInt64("acfun.authKey"), s.GetString("acfun.acPassToken")
	if strings.ContainsAny(token, "\r\n;") || authKey < 0 {
		return nil, fmt.Errorf("A站 Cookie 格式无效")
	}
	if authKey == 0 && token == "" && prepared.account.username == "" && prepared.account.password == "" {
		return prepared, nil
	}
	if authKey == 0 && token == "" {
		if prepared.account.username == "" || prepared.account.password == "" {
			return nil, fmt.Errorf("A站 account 和 password 必须同时提供")
		}
		response, err := login(prepared.account.username, prepared.account.password)
		if err != nil || response.GetResult() != 0 {
			return nil, fmt.Errorf("A站账号登录验证失败，请检查凭据和网络")
		}
		authKey, token = response.GetAuthKey(), response.GetAcPassToken()
	}
	if authKey <= 0 || token == "" {
		return nil, fmt.Errorf("A站 authKey 和 acPassToken 必须同时提供")
	}
	prepared.verify = &VerifyInfo{AuthKey: authKey, AcPassToken: token, VerifyOpts: []requests.Option{
		requests.CookieOption("auth_key", strconv.FormatInt(authKey, 10)), requests.CookieOption("acPasstoken", token),
	}}
	opts := append(GetGeneralOptions(false), prepared.verify.VerifyOpts...)
	response, err := validate(opts)
	if err != nil || response.GetResult() != 0 || response.GetInfo().GetUserId() == 0 {
		return nil, fmt.Errorf("A站 Cookie 验证失败，请检查凭据和网络")
	}
	prepared.uid = response.GetInfo().GetUserId()
	return prepared, nil
}
