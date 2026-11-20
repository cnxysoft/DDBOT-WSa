package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postSetup 用临时配置文件跑一次初始化向导请求。
func postSetup(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleSetup(rec, req)
	return rec
}

// TestHandleSetupQQOfficial 覆盖向导选 QQ 开放平台的主路径：
// AppID / AppSecret 落盘、adapter.mode 切到 qqofficial、qqofficial.mode 补默认。
func TestHandleSetupQQOfficial(t *testing.T) {
	path := withConfigFile(t, "adapter:\n  mode: onebot-v11\n")

	rec := postSetup(t, `{"token":"secret123","adapterMode":"qqofficial","qqAppId":"10293045","qqAppSecret":"app-secret-value"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	got := readConfigMap(t, path)
	adapterCfg, _ := got["adapter"].(map[string]interface{})
	require.NotNil(t, adapterCfg)
	assert.Equal(t, "qqofficial", adapterCfg["mode"])

	qq, _ := got["qqofficial"].(map[string]interface{})
	require.NotNil(t, qq)
	assert.Equal(t, "10293045", configValueString(qq["app-id"]))
	assert.Equal(t, "app-secret-value", qq["app-secret"])
	assert.Equal(t, "websocket", qq["mode"])

	adm, _ := got["admin"].(map[string]interface{})
	require.NotNil(t, adm)
	assert.Equal(t, "secret123", adm["token"])
}

// TestHandleSetupQQOfficialRequiresCredentials 没有凭据也没有扫码结果时必须拒绝，
// 否则会写出「adapter.mode=qqofficial、启动直接报缺 AppID」的配置。
func TestHandleSetupQQOfficialRequiresCredentials(t *testing.T) {
	path := withConfigFile(t, "adapter:\n  mode: onebot-v11\n")

	rec := postSetup(t, `{"token":"secret123","adapterMode":"qqofficial"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "AppID")

	// 拒绝时不能落盘任何东西（文件应保持原样）
	got := readConfigMap(t, path)
	assert.Nil(t, got["qqofficial"])
	adapterCfg, _ := got["adapter"].(map[string]interface{})
	assert.Equal(t, "onebot-v11", adapterCfg["mode"])
}

// TestHandleSetupQQOfficialAcceptsQRPreboundCredentials 覆盖「先扫码再提交向导」：
// 扫码已经把 AppSecret 写进配置，此时请求体里不带 Secret 也必须放行。
func TestHandleSetupQQOfficialAcceptsQRPreboundCredentials(t *testing.T) {
	path := withConfigFile(t,
		"qqofficial:\n  app-id: \"10293045\"\n  app-secret: \"qr-secret\"\n")

	rec := postSetup(t, `{"token":"secret123","adapterMode":"qqofficial"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	got := readConfigMap(t, path)
	qq, _ := got["qqofficial"].(map[string]interface{})
	require.NotNil(t, qq)
	assert.Equal(t, "10293045", configValueString(qq["app-id"]))
	assert.Equal(t, "qr-secret", qq["app-secret"])
	adapterCfg, _ := got["adapter"].(map[string]interface{})
	assert.Equal(t, "qqofficial", adapterCfg["mode"])
}

// TestHandleSetupQQOfficialRejectsAppIDChangeWithoutSecret 手填换 AppID 却不换 Secret 时，
// 合并后必然是「新 AppID + 旧 Secret」这种连不上的组合，直接拒绝。
func TestHandleSetupQQOfficialRejectsAppIDChangeWithoutSecret(t *testing.T) {
	withConfigFile(t,
		"qqofficial:\n  app-id: \"10293045\"\n  app-secret: \"old-secret\"\n")

	rec := postSetup(t, `{"token":"secret123","adapterMode":"qqofficial","qqAppId":"1999999999"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "AppSecret")

	// 同时给新 Secret 就允许
	rec = postSetup(t, `{"token":"secret123","adapterMode":"qqofficial","qqAppId":"1999999999","qqAppSecret":"new-secret"}`)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestHandleSetupQQOfficialRejectsNonNumericAppID AppID 必须是纯数字（与扫码协议一致）。
func TestHandleSetupQQOfficialRejectsNonNumericAppID(t *testing.T) {
	withConfigFile(t, "")

	rec := postSetup(t, `{"token":"secret123","adapterMode":"qqofficial","qqAppId":"abc","qqAppSecret":"x"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "纯数字")
}

// TestHandleSetupInfersQQOfficialFromCredentials 老前端 / 手写请求可能只带凭据，
// 这时按 QQ 开放平台推断，避免「凭据写了、adapter.mode 没切」。
func TestHandleSetupInfersQQOfficialFromCredentials(t *testing.T) {
	path := withConfigFile(t, "adapter:\n  mode: onebot-v11\n")

	rec := postSetup(t, `{"token":"secret123","qqAppId":"10293045","qqAppSecret":"x"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	got := readConfigMap(t, path)
	adapterCfg, _ := got["adapter"].(map[string]interface{})
	assert.Equal(t, "qqofficial", adapterCfg["mode"])
}

// TestHandleSetupOneBotWritesAdapterMode 选 OneBot 时同时写 adapter.mode 与 websocket 段。
func TestHandleSetupOneBotWritesAdapterMode(t *testing.T) {
	path := withConfigFile(t, "")

	rec := postSetup(t, `{"token":"secret123","adapterMode":"onebot-v11","obMode":"ws-server","obAddr":"127.0.0.1:15630"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	got := readConfigMap(t, path)
	adapterCfg, _ := got["adapter"].(map[string]interface{})
	assert.Equal(t, "onebot-v11", adapterCfg["mode"])
	ws, _ := got["websocket"].(map[string]interface{})
	require.NotNil(t, ws)
	assert.Equal(t, "ws-server", ws["mode"])
	assert.Equal(t, "127.0.0.1:15630", ws["ws-server"])
}

func TestHandleSetupRejectsUnknownAdapterMode(t *testing.T) {
	withConfigFile(t, "")

	rec := postSetup(t, `{"token":"secret123","adapterMode":"onebot-v1"}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "未知的接入方式")
}

// TestHandleSetupStatusScopesQQInfoToLoopback 固化状态接口的暴露面：
// 本机可以看到接入方式 / AppID，局域网匿名请求只看得到 needed。
func TestHandleSetupStatusScopesQQInfoToLoopback(t *testing.T) {
	prevMode := config.GlobalConfig.GetString("adapter.mode")
	prevID := config.GlobalConfig.GetString("qqofficial.app-id")
	prevSecret := config.GlobalConfig.GetString("qqofficial.app-secret")
	t.Cleanup(func() {
		config.GlobalConfig.Set("adapter.mode", prevMode)
		config.GlobalConfig.Set("qqofficial.app-id", prevID)
		config.GlobalConfig.Set("qqofficial.app-secret", prevSecret)
	})
	config.GlobalConfig.Set("adapter.mode", "qqofficial")
	config.GlobalConfig.Set("qqofficial.app-id", "10293045")
	config.GlobalConfig.Set("qqofficial.app-secret", "s3cret")

	s := &Server{}
	read := func(remote string) SetupStatus {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://localhost/api/v1/setup/status", nil)
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		s.handleSetupStatus(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		var st SetupStatus
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
		return st
	}

	local := read("127.0.0.1:12345")
	assert.True(t, local.Needed)
	assert.Equal(t, "qqofficial", local.AdapterMode)
	assert.Equal(t, "10293045", local.QQAppID)
	assert.True(t, local.QQSecretSet)

	remote := read("192.168.1.20:12345")
	assert.False(t, remote.Needed, "远端不展示初始化向导，直接看登录页")
	assert.Empty(t, remote.AdapterMode)
	assert.Empty(t, remote.QQAppID)
	assert.False(t, remote.QQSecretSet)
}

// TestSetupQQOfficialQRCodeClosedAfterInit 固化安全契约：初始化完成后，
// 向导专用的匿名扫码入口必须整体 404，不能变成常驻的匿名绑定接口。
func TestSetupQQOfficialQRCodeClosedAfterInit(t *testing.T) {
	s := &Server{token: "already-set"}

	rec := httptest.NewRecorder()
	s.handleSetupQQOfficialQRCode(rec, httptest.NewRequest(http.MethodPost, "/api/v1/setup/qqofficial/qrcode", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = httptest.NewRecorder()
	s.handleSetupQQOfficialQRCodeStatus(rec, httptest.NewRequest(http.MethodGet, "/api/v1/setup/qqofficial/qrcode/status?session=x", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)

	rec = httptest.NewRecorder()
	s.handleSetupQQOfficialQRCodeCancel(rec, httptest.NewRequest(http.MethodPost, "/api/v1/setup/qqofficial/qrcode/cancel", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// TestValidateAdapterModePatch 配置页写入 adapter.mode 时的取值校验。
func TestValidateAdapterModePatch(t *testing.T) {
	tests := []struct {
		name    string
		patch   map[string]interface{}
		wantErr bool
	}{
		{"没带 adapter 段", map[string]interface{}{"bot": map[string]interface{}{"commandPrefix": "!"}}, false},
		{"adapter 整体替换", map[string]interface{}{"adapter": nil}, false},
		{"onebot-v11", map[string]interface{}{"adapter": map[string]interface{}{"mode": "onebot-v11"}}, false},
		{"qqofficial", map[string]interface{}{"adapter": map[string]interface{}{"mode": "qqofficial"}}, false},
		{"satori", map[string]interface{}{"adapter": map[string]interface{}{"mode": "satori"}}, false},
		{"空值按默认", map[string]interface{}{"adapter": map[string]interface{}{"mode": ""}}, false},
		{"笔误 onebot-v1", map[string]interface{}{"adapter": map[string]interface{}{"mode": "onebot-v1"}}, true},
		{"不认识的通道", map[string]interface{}{"adapter": map[string]interface{}{"mode": "telegram"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAdapterModePatch(tt.patch)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestHandleConfigRejectsUnknownAdapterMode 把上面的校验接到配置 POST 上。
func TestHandleConfigRejectsUnknownAdapterMode(t *testing.T) {
	withConfigFile(t, "adapter:\n  mode: onebot-v11\n")

	rec := postConfig(t, `{"config":{"adapter":{"mode":"onebot-v1"}}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "adapter.mode")
}

// TestHandleConfigRejectsQQOfficialWithoutCredentials 是这次线上事故的回归：
// 只把 adapter.mode 切成 qqofficial、凭据留空时，保存必须被拒绝，
// 否则重启后 qqofficial adapter 直接 Fatal（缺少 qqofficial.app-id）。
func TestHandleConfigRejectsQQOfficialWithoutCredentials(t *testing.T) {
	path := withConfigFile(t, "adapter:\n  mode: onebot-v11\n")

	rec := postConfig(t, `{"config":{"adapter":{"mode":"qqofficial"}}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "AppID")

	// 被拒绝时不能落盘
	got := readConfigMap(t, path)
	adapterCfg, _ := got["adapter"].(map[string]interface{})
	assert.Equal(t, "onebot-v11", adapterCfg["mode"])

	// 同一次保存里同时给凭据：允许
	rec = postConfig(t, `{"config":{
		"adapter":{"mode":"qqofficial"},
		"qqofficial":{"app-id":"10293045","app-secret":"app-secret-value"}
	}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got = readConfigMap(t, path)
	adapterCfg, _ = got["adapter"].(map[string]interface{})
	assert.Equal(t, "qqofficial", adapterCfg["mode"])
	qq, _ := got["qqofficial"].(map[string]interface{})
	assert.Equal(t, "10293045", configValueString(qq["app-id"]))
	assert.Equal(t, "app-secret-value", configValueString(qq["app-secret"]))
}

// TestHandleConfigAllowsQQOfficialWithExistingCredentials 已有凭据时只切接入方式应放行。
func TestHandleConfigAllowsQQOfficialWithExistingCredentials(t *testing.T) {
	withConfigFile(t,
		"adapter:\n  mode: onebot-v11\nqqofficial:\n  app-id: \"10293045\"\n  app-secret: \"app-secret-value\"\n")

	rec := postConfig(t, `{"config":{"adapter":{"mode":"qqofficial"}}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// TestHandleConfigRejectsClearingQQSecret 清空 AppSecret 时也必须拒绝。
func TestHandleConfigRejectsClearingQQSecret(t *testing.T) {
	withConfigFile(t,
		"adapter:\n  mode: qqofficial\nqqofficial:\n  app-id: \"10293045\"\n  app-secret: \"app-secret-value\"\n")

	rec := postConfig(t, `{"config":{"qqofficial":{"app-secret":""}}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "AppSecret")
}

// TestHandleConfigRejectsNonNumericQQAppID 配置页手填的 AppID 也必须是纯数字。
func TestHandleConfigRejectsNonNumericQQAppID(t *testing.T) {
	withConfigFile(t, "adapter:\n  mode: onebot-v11\n")

	rec := postConfig(t, `{"config":{
		"adapter":{"mode":"qqofficial"},
		"qqofficial":{"app-id":"abc","app-secret":"s"}
	}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "纯数字")
}

// TestHandleConfigRejectsSavingBrokenQQConfig 现有配置已经是「qqofficial + 空凭据」时，
// 保存其它字段也要被拦下，避免用户以为改好了、重启仍然起不来。
func TestHandleConfigRejectsSavingBrokenQQConfig(t *testing.T) {
	withConfigFile(t, "adapter:\n  mode: qqofficial\nqqofficial:\n  app-id:\n  app-secret:\nlogLevel: info\n")

	rec := postConfig(t, `{"config":{"logLevel":"debug"}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Body.String(), "AppID")
}

func TestSetupCanSetPasswordWithoutSwitchingSatori(t *testing.T) {
	path := withConfigFile(t, "adapter:\n  mode: satori\nwebsocket:\n  ws-reverse: ws://localhost:3001\n")
	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup", strings.NewReader(`{"token":"sample-pass","adapterMode":""}`))
	rec := httptest.NewRecorder()
	s.handleSetup(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	got := readConfigMap(t, path)
	assert.Equal(t, "satori", nestedConfigString(got, "adapter", "mode"))
	assert.Equal(t, "ws://localhost:3001", nestedConfigString(got, "websocket", "ws-reverse"))
	assert.Equal(t, "sample-pass", s.getToken())
}
