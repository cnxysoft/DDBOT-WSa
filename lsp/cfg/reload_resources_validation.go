package cfg

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/Sora233/MiraiGo-Template/config"
)

func validateResourceValues(s *config.Snapshot) error {
	for _, section := range []string{"adapter", "websocket", "admin", "proxy", "localProxyPool", "pyProxyPool", "imagePool", "loliconPool", "localPool", "template", "extDb", "telegram", "bilibili", "acfun", "weibo", "douyin", "twitter", "twitch", "twitcasting", "xhs", "heybox", "qqofficial"} {
		if v := s.Get(section); v != nil {
			if _, ok := v.(map[string]interface{}); !ok {
				return fmt.Errorf("%s 必须是配置映射", section)
			}
		}
	}
	for _, key := range []string{"admin.enable", "template.enable", "extDb.enable", "telegram.enable", "bilibili.qrlogin", "weibo.qrlogin", "weibo.autorefresh"} {
		if v := s.Get(key); v != nil && !validBoolean(v) {
			return fmt.Errorf("%s 必须是布尔值", key)
		}
	}
	for _, key := range []string{
		"adapter.mode", "websocket.mode", "websocket.ws-server", "websocket.ws-reverse", "websocket.token", "admin.addr", "admin.token",
		"proxy.type", "pyProxyPool.host", "imagePool.type", "loliconPool.apikey", "loliconPool.proxy", "localPool.imageDir", "extDb.path",
		"telegram.token", "telegram.endpoint", "bilibili.SESSDATA", "bilibili.bili_jct", "bilibili.account", "bilibili.password",
		"acfun.acPassToken", "acfun.account", "acfun.password", "weibo.mode", "weibo.sub", "weibo.cookieRefreshAPI", "weibo.apiModeBaseURL",
		"douyin.userAgent", "douyin.acSignature", "douyin.acNonce", "douyin.sessionId", "twitter.mode", "twitter.apiFetchMode", "twitter.userAgent",
		"twitter.ct0", "twitter.auth_token", "twitter.bearerToken", "twitter.queryId", "twitter.userTweetsQueryId", "twitter.screenName",
		"twitch.clientId", "twitch.clientSecret", "twitcasting.clientId", "twitcasting.clientSecret", "heybox.x_xhh_tokenid",
		"qqofficial.app-secret", "qqofficial.mode", "qqofficial.api-base-url", "qqofficial.state-file", "qqofficial.join-intro-tip",
		"qqofficial.appSecret", "qqofficial.secret",
	} {
		if v := s.Get(key); v != nil {
			str, ok := v.(string)
			if !ok {
				return fmt.Errorf("%s 必须是字符串", key)
			}
			if strings.ContainsAny(str, "\r\n") {
				return fmt.Errorf("%s 不得包含换行", key)
			}
		}
	}
	for _, key := range []string{"acfun.authKey", "loliconPool.cacheMin", "loliconPool.cacheMax", "qqofficial.intents", "qqofficial.shard-id", "qqofficial.shard-count"} {
		if v := s.Get(key); v != nil {
			if n, ok := integerValue(v); !ok || n < 0 {
				return fmt.Errorf("%s 必须是非负整数", key)
			}
		}
	}
	for _, key := range []string{"qqofficial.http-timeout", "qqofficial.token-refresh-ahead", "qqofficial.start-timeout", "twitter.queryIdRefreshInterval"} {
		if !validInterval(s.Get(key), false) {
			return fmt.Errorf("%s 必须是非负时间间隔", key)
		}
	}
	for _, key := range []string{"twitter.baseUrl", "localProxyPool.oversea", "localProxyPool.mainland", "telegram.chats"} {
		v := s.Get(key)
		if v == nil {
			continue
		}
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Slice {
			return fmt.Errorf("%s 必须是列表", key)
		}
		for i := 0; i < rv.Len(); i++ {
			item := rv.Index(i).Interface()
			if _, ok := item.(string); ok {
				continue
			}
			if key == "telegram.chats" {
				if _, ok := integerValue(item); ok {
					continue
				}
			}
			return fmt.Errorf("%s[%d] 类型无效", key, i)
		}
	}
	return nil
}
