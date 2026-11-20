package admin

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghodss/yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// defaultApplicationYAML 读仓库里的默认配置。它是「默认生成格式」的真实样本：
// 大量说明性注释、空行、行内注释和 2 空格缩进。
func defaultApplicationYAML(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "dist", "application.yaml"))
	if err != nil {
		t.Skipf("默认配置不存在，跳过: %v", err)
	}
	return string(data)
}

func parseConfigFile(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]interface{}
	require.NoError(t, yaml.Unmarshal(data, &m))
	return m
}

func configStringAt(m map[string]interface{}, path ...string) string {
	var cur interface{} = m
	for _, key := range path {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return ""
		}
		cur = mm[key]
	}
	return configValueString(cur)
}

func configBoolAt(m map[string]interface{}, path ...string) (bool, bool) {
	var cur interface{} = m
	for _, key := range path {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return false, false
		}
		cur = mm[key]
	}
	v, ok := cur.(bool)
	return v, ok
}

// TestHandleConfigPreservesDefaultConfigFormat 是用户明确提出的约束：
// 在默认生成的 application.yaml 上保存配置，不能把注释 / 空行 / 缩进冲掉。
// 这条走完整的 handleConfig 路径（含受保护键校验、脱敏占位符过滤）。
func TestHandleConfigPreservesDefaultConfigFormat(t *testing.T) {
	original := defaultApplicationYAML(t)
	path := withConfigFile(t, original)

	rec := postConfig(t, `{"config":{
		"adapter":{"mode":"qqofficial"},
		"qqofficial":{"app-id":"10293045","app-secret":"app-secret-value"},
		"platform":{"bilibili":{"enable":false}}
	}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(after)

	// 抽查默认配置里几条有代表性的注释（整份 yaml.Marshal 会全部吃掉）
	for _, needle := range []string{
		"# 命令前缀，默认 \"/\"",
		"# 是否启用离线缓存",
		"# 你的 b 站 cookie",
		"# 定时任务列表",
		"# 仅支持 websocket（webhook 尚未实现，填了会在启动时报错退出）",
	} {
		assert.Contains(t, text, needle, "注释 %q 不应丢失", needle)
	}

	// 空行不能被大量吞掉
	beforeBlank := strings.Count(original, "\n\n")
	afterBlank := strings.Count(text, "\n\n")
	assert.GreaterOrEqual(t, afterBlank, beforeBlank-2, "空行被大量删除")

	// 写入的值必须真的生效
	m := parseConfigFile(t, path)
	assert.Equal(t, "qqofficial", configStringAt(m, "adapter", "mode"))
	assert.Equal(t, "10293045", configStringAt(m, "qqofficial", "app-id"))
	assert.Equal(t, "app-secret-value", configStringAt(m, "qqofficial", "app-secret"))
	enabled, ok := configBoolAt(m, "platform", "bilibili", "enable")
	require.True(t, ok, "platform.bilibili.enable 应写入布尔值")
	assert.False(t, enabled)

	// 默认配置里没动过的段保持原值（抽查）
	assert.Equal(t, "info", configStringAt(m, "logLevel"))
	assert.Equal(t, "mirror", configStringAt(m, "twitter", "mode"))
}

// TestHandleSetupPreservesDefaultConfigFormat 同一条约束接到初始化向导上：
// 选 QQ 开放平台后，默认配置的注释与格式仍然是原样。
func TestHandleSetupPreservesDefaultConfigFormat(t *testing.T) {
	original := defaultApplicationYAML(t)
	path := withConfigFile(t, original)

	s := &Server{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/setup",
		strings.NewReader(`{"token":"secret123","adapterMode":"qqofficial","qqAppId":"10293045","qqAppSecret":"app-secret-value"}`))
	rec := httptest.NewRecorder()
	s.handleSetup(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	text := string(after)
	for _, needle := range []string{
		"# 命令前缀，默认 \"/\"",
		"# 你的 b 站 cookie",
		"# 定时任务列表",
	} {
		assert.Contains(t, text, needle, "注释 %q 不应丢失", needle)
	}

	m := parseConfigFile(t, path)
	assert.Equal(t, "secret123", configStringAt(m, "admin", "token"))
	assert.Equal(t, "qqofficial", configStringAt(m, "adapter", "mode"))
	assert.Equal(t, "10293045", configStringAt(m, "qqofficial", "app-id"))
	assert.Equal(t, "app-secret-value", configStringAt(m, "qqofficial", "app-secret"))
}
