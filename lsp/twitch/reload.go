package twitch

import (
	"fmt"
	"strings"
	"sync"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
)

func PrepareRuntimeReload(previous, next *config.Snapshot) (func(), func(), error) {
	return PrepareRuntimeReloadWithProxy(previous, next, nil)
}

func PrepareRuntimeReloadWithProxy(previous, next *config.Snapshot, getter proxy_pool.Getter) (func(), func(), error) {
	return prepareRuntimeReloadWithProxy(previous, next, getter, authURL)
}

func prepareRuntimeReloadWithProxy(previous, next *config.Snapshot, getter proxy_pool.Getter, endpoint string) (func(), func(), error) {
	return prepareRuntimeReload(previous, next, func(id, secret string) (tokenResponse, error) {
		return requestAppTokenAtWithProxy(endpoint, id, secret, getter)
	})
}

func prepareRuntimeReload(previous, next *config.Snapshot, fetch func(string, string) (tokenResponse, error)) (func(), func(), error) {
	id, secret := strings.TrimSpace(next.GetString("twitch.clientId")), strings.TrimSpace(next.GetString("twitch.clientSecret"))
	if id == strings.TrimSpace(previous.GetString("twitch.clientId")) && secret == strings.TrimSpace(previous.GetString("twitch.clientSecret")) {
		return func() {}, func() {}, nil
	}
	if (id == "") != (secret == "") {
		return nil, nil, fmt.Errorf("Twitch 凭据必须同时填写或同时清空")
	}
	candidate := &appToken{clientId: id, clientSecret: secret}
	if id != "" {
		if _, err := candidate.auth(fetch); err != nil {
			return nil, nil, fmt.Errorf("Twitch 新凭据校验失败，保留原运行配置")
		}
	}
	var once sync.Once
	return func() { once.Do(func() { tokenStore.install(candidate) }) }, func() { once.Do(func() {}) }, nil
}
