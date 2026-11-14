package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// 本文件固化 qqofficial 凭据的两条契约（R5 修正版）：
//
//	1. AppSecret 与 bilibili.SESSDATA 同级 —— 属于「凭据可写不可读」：
//	   允许配置 API 覆盖写入（不在 protectedConfigKeys 里），
//	   但 GET /api/v1/config 必须脱敏（在 sensitiveConfigLeafPaths 里）。
//	2. protectedConfigKeys 只放「可提权 / 留后门」那两个键，别往里塞凭据。
//
// 反面教材：曾把 qqofficial.app-secret 放进 protectedConfigKeys 而没放进脱敏名单，
// 结果是「手填写不进去 + GET 明文返回」两头都错。

// 配置 API 覆盖写 AppSecret 不该被判违规（与 bilibili.SESSDATA 一致）。
func TestQQOfficialSecret_WritableViaConfigAPI(t *testing.T) {
	existing := map[string]interface{}{
		"qqofficial": map[string]interface{}{"app-id": "1", "app-secret": "old"},
	}
	updated := map[string]interface{}{
		"qqofficial": map[string]interface{}{"app-id": "1", "app-secret": "new"},
	}
	assert.Empty(t, collectConfigWriteViolations(existing, updated, ""),
		"AppSecret 是凭据不是提权键，应当允许覆盖写入")

	// 回归保护：真正的提权键仍然要挡住
	assert.Equal(t, []string{"admin.token"},
		collectConfigWriteViolations(nil, map[string]interface{}{
			"admin": map[string]interface{}{"token": "hacked"},
		}, ""))
	assert.Equal(t, []string{"websocket.token"},
		collectConfigWriteViolations(nil, map[string]interface{}{
			"websocket": map[string]interface{}{"token": "hacked"},
		}, ""))
}

// GET /api/v1/config 必须把 AppSecret 换成占位符，不能明文返回。
func TestQQOfficialSecret_MaskedInConfigResponse(t *testing.T) {
	cfg := map[string]interface{}{
		"qqofficial": map[string]interface{}{
			"app-id":     "10293045",
			"app-secret": "sekret-value",
			"mode":       "websocket",
			"intents":    float64(0),
		},
	}

	maskSensitiveConfig(cfg)

	assert.Equal(t, redactedPlaceholder, getConfigPath(t, cfg, "qqofficial.app-secret"),
		"AppSecret 必须脱敏")
	// AppID 是标识符不是密钥：按本名单既有标准（twitch.clientId/clientSecret 的对照）不脱敏
	assert.Equal(t, "10293045", getConfigPath(t, cfg, "qqofficial.app-id"))
	assert.Equal(t, "websocket", getConfigPath(t, cfg, "qqofficial.mode"))
}

// adapter 读配置时 app-secret / appSecret / secret 互为回退别名，别名拼写同样要脱敏。
func TestQQOfficialSecret_AliasSpellingsAlsoMasked(t *testing.T) {
	cfg := map[string]interface{}{
		"qqofficial": map[string]interface{}{
			"appSecret": "sekret-camel",
			"secret":    "sekret-short",
		},
	}

	maskSensitiveConfig(cfg)

	assert.Equal(t, redactedPlaceholder, getConfigPath(t, cfg, "qqofficial.appSecret"))
	assert.Equal(t, redactedPlaceholder, getConfigPath(t, cfg, "qqofficial.secret"))
}

// 未配置时保持原样，前端才能区分「未配置」与「已脱敏」。
func TestQQOfficialSecret_UnsetStaysUnset(t *testing.T) {
	cfg := map[string]interface{}{
		"qqofficial": map[string]interface{}{"app-secret": ""},
	}

	maskSensitiveConfig(cfg)

	assert.Equal(t, "", getConfigPath(t, cfg, "qqofficial.app-secret"))
}
