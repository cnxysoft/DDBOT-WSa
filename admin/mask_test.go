package admin

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// getConfigPath 按 "a.b.c" 从嵌套 map 取值，便于断言脱敏结果。
func getConfigPath(t *testing.T, cfg map[string]interface{}, path string) interface{} {
	t.Helper()
	cur := cfg
	parts := strings.Split(path, ".")
	for i, p := range parts {
		v, ok := cur[p]
		if !ok {
			t.Fatalf("配置路径 %s 不存在", path)
		}
		if i == len(parts)-1 {
			return v
		}
		next, ok := v.(map[string]interface{})
		if !ok {
			t.Fatalf("配置路径 %s 在 %s 处不是 map", path, p)
		}
		cur = next
	}
	return nil
}

// TestMaskSensitiveConfigMasksCredentialPaths 覆盖审查报告 H1：
// 旧实现只按叶子键名子串判断、且只处理 string，导致 bili_jct / weibo.sub / twitter.ct0 /
// douyin.sessionId / xhs.cookies.* / 数值型 token 等在 GET /api/v1/config 明文返回。
// 新实现按完整路径白名单判断，这里逐一断言。
func TestMaskSensitiveConfigMasksCredentialPaths(t *testing.T) {
	cfg := map[string]interface{}{
		"admin":       map[string]interface{}{"token": "admin-secret", "addr": "127.0.0.1:15631"},
		"websocket":   map[string]interface{}{"token": "ob-secret", "mode": "ws-server"},
		"telegram":    map[string]interface{}{"token": "tg-secret", "enable": false},
		"bilibili":    map[string]interface{}{"SESSDATA": "sd", "bili_jct": "jct", "interval": "25s"},
		"weibo":       map[string]interface{}{"sub": "sub-cookie", "onlyOnlineNotify": true},
		"twitter":     map[string]interface{}{"auth_token": "at", "ct0": "ct0-val", "bearerToken": "bt", "queryId": "qid"},
		"douyin":      map[string]interface{}{"sessionId": "sid", "acSignature": "as", "acNonce": "an", "userAgent": "ua"},
		"acfun":       map[string]interface{}{"password": "pw", "authKey": 12345, "acPassToken": "apt", "account": "user"},
		"twitch":      map[string]interface{}{"clientId": "cid", "clientSecret": "csec"},
		"twitcasting": map[string]interface{}{"clientId": "cid", "clientSecret": "csec"},
		"heybox":      map[string]interface{}{"x_xhh_tokenid": float64(1234567890), "interval": "30s"},
		"loliconPool": map[string]interface{}{"apikey": "lolikey", "cacheMax": float64(20)},
		"xhs": map[string]interface{}{"cookies": map[string]interface{}{
			"a1": "a1-val", "web_session": "ws-val", "webId": "wid",
		}},
		"proxy": map[string]interface{}{"type": "systemProxy"},
	}

	maskSensitiveConfig(cfg)

	// 完整路径白名单：必须脱敏（含数值型 token 与整个 xhs.cookies 命名空间）
	for _, path := range []string{
		"admin.token", "websocket.token", "telegram.token",
		"bilibili.SESSDATA", "bilibili.bili_jct", "weibo.sub",
		"twitter.auth_token", "twitter.ct0", "twitter.bearerToken",
		"douyin.sessionId", "douyin.acSignature", "douyin.acNonce",
		"acfun.password", "acfun.authKey", "acfun.acPassToken",
		"twitch.clientSecret", "twitcasting.clientSecret",
		"heybox.x_xhh_tokenid", "loliconPool.apikey",
		"xhs.cookies.a1", "xhs.cookies.web_session", "xhs.cookies.webId",
	} {
		assert.Equal(t, redactedPlaceholder, getConfigPath(t, cfg, path), "路径 %s 应被脱敏", path)
	}

	// 非凭据字段保持原值（避免过度脱敏影响面板读配置）
	assert.Equal(t, "127.0.0.1:15631", getConfigPath(t, cfg, "admin.addr"))
	assert.Equal(t, "ws-server", getConfigPath(t, cfg, "websocket.mode"))
	assert.Equal(t, "25s", getConfigPath(t, cfg, "bilibili.interval"))
	assert.Equal(t, true, getConfigPath(t, cfg, "weibo.onlyOnlineNotify"))
	assert.Equal(t, "qid", getConfigPath(t, cfg, "twitter.queryId"))
	assert.Equal(t, "ua", getConfigPath(t, cfg, "douyin.userAgent"))
	assert.Equal(t, "user", getConfigPath(t, cfg, "acfun.account"))
	assert.Equal(t, false, getConfigPath(t, cfg, "telegram.enable"))
	assert.Equal(t, "cid", getConfigPath(t, cfg, "twitch.clientId"))
	assert.Equal(t, "systemProxy", getConfigPath(t, cfg, "proxy.type"))
}

// TestMaskSensitiveConfigKeepsUnsetValues 未设置（nil/空串）保持原样，前端才能区分「未配置」。
func TestMaskSensitiveConfigKeepsUnsetValues(t *testing.T) {
	cfg := map[string]interface{}{
		"admin": map[string]interface{}{"token": ""},
		"weibo": map[string]interface{}{"sub": nil},
		"xhs":   map[string]interface{}{"cookies": map[string]interface{}{"a1": ""}},
	}
	maskSensitiveConfig(cfg)

	assert.Equal(t, "", getConfigPath(t, cfg, "admin.token"))
	assert.Nil(t, getConfigPath(t, cfg, "weibo.sub"))
	assert.Equal(t, "", getConfigPath(t, cfg, "xhs.cookies.a1"))
}

// TestMaskSensitiveConfigHandlesSlices 敏感值不是 string 时同样要脱敏；非敏感列表保持原样。
func TestMaskSensitiveConfigHandlesSlices(t *testing.T) {
	cfg := map[string]interface{}{
		// 敏感路径的值是列表：整体替换，不能因为不是 string 就漏掉
		"weibo":  map[string]interface{}{"sub": []interface{}{"a", "b"}},
		"douyin": map[string]interface{}{"sessionId": []interface{}{}},
		// 非敏感列表原样保留
		"localProxyPool": map[string]interface{}{"oversea": []interface{}{"http://127.0.0.1:7890"}},
	}
	maskSensitiveConfig(cfg)

	assert.Equal(t, redactedPlaceholder, getConfigPath(t, cfg, "weibo.sub"))
	assert.Equal(t, redactedPlaceholder, getConfigPath(t, cfg, "douyin.sessionId"))
	assert.Equal(t, []interface{}{"http://127.0.0.1:7890"}, getConfigPath(t, cfg, "localProxyPool.oversea"))
}
