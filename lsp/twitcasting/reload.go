package twitcasting

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/nobuf/cas"
)

func (tc *TwitCastConcern) clientSnapshot() (*cas.Client, error) {
	client := tc.client.Load()
	if client == nil {
		return nil, fmt.Errorf("TwitCasting 凭据尚未就绪")
	}
	return client, nil
}

func validateCredentials(id, secret, endpoint string) error {
	request, err := http.NewRequest(http.MethodGet, strings.TrimRight(endpoint, "/")+"/search/lives?limit=1&type=recommend&lang=ja", nil)
	if err != nil {
		return fmt.Errorf("TwitCasting API 地址无效")
	}
	request.SetBasicAuth(id, secret)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Api-Version", "2.0")
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("TwitCasting API 认证请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("TwitCasting API 认证未通过")
	}
	var movies cas.Movies
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&movies); err != nil {
		return fmt.Errorf("TwitCasting API 返回无效响应")
	}
	return nil
}

func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	instance, err := concern.GetConcernBySite(Site)
	if err != nil {
		return nil, nil, fmt.Errorf("TwitCasting 模块尚未就绪")
	}
	tc, ok := instance.(*TwitCastConcern)
	if !ok {
		return nil, nil, fmt.Errorf("TwitCasting 模块类型无效")
	}
	return tc.prepareRuntimeReload(previous, next, cas.TwitCastingAPI)
}

func (tc *TwitCastConcern) prepareRuntimeReload(previous, next *config.Snapshot, endpoint string) (func(), func(), error) {
	id, secret := strings.TrimSpace(next.GetString("twitcasting.clientId")), strings.TrimSpace(next.GetString("twitcasting.clientSecret"))
	if id == strings.TrimSpace(previous.GetString("twitcasting.clientId")) && secret == strings.TrimSpace(previous.GetString("twitcasting.clientSecret")) {
		return func() {}, func() {}, nil
	}
	if (id == "") != (secret == "") {
		return nil, nil, fmt.Errorf("TwitCasting 凭据必须同时填写或同时清空")
	}
	var candidate *cas.Client
	if id != "" {
		if err := validateCredentials(id, secret, endpoint); err != nil {
			return nil, nil, fmt.Errorf("TwitCasting 新凭据校验失败，保留原运行配置")
		}
		candidate = cas.New(id, secret)
		candidate.SetEndpoint(endpoint)
	}
	var once sync.Once
	return func() { once.Do(func() { tc.client.Store(candidate) }) }, func() { once.Do(func() {}) }, nil
}
