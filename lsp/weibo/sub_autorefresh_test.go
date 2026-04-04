package weibo

import (
	"testing"

	miraiConfig "github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestGetWeiboAutoRefresh(t *testing.T) {
	resetConfig := useTestConfigForAutoRefresh(t)
	defer resetConfig()

	// 默认应该是 false
	assert.False(t, cfg.GetWeiboAutoRefresh())

	// 设置为 true
	miraiConfig.GlobalConfig.Set("weibo.autorefresh", true)
	assert.True(t, cfg.GetWeiboAutoRefresh())

	// 设置为 false
	miraiConfig.GlobalConfig.Set("weibo.autorefresh", false)
	assert.False(t, cfg.GetWeiboAutoRefresh())
}

func TestMaskSub(t *testing.T) {
	testCases := []struct {
		name     string
		sub      string
		expected string
	}{
		{
			name:     "short sub",
			sub:      "short",
			expected: "***",
		},
		{
			name:     "exact 20 chars",
			sub:      "12345678901234567890",
			expected: "***",
		},
		{
			name:     "long sub",
			sub:      "1234567890abcdef1234567890abcdef",
			expected: "1234567890...7890abcdef",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := maskSub(tc.sub)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestStartSubAutoRefreshNotLoginMode(t *testing.T) {
	resetConfig := useTestConfigForAutoRefresh(t)
	defer resetConfig()

	// 设置为 guest 模式，不应该启动
	miraiConfig.GlobalConfig.Set("weibo.mode", "guest")
	miraiConfig.GlobalConfig.Set("weibo.autorefresh", true)

	// 调用 StartSubAutoRefresh 应该不会 panic
	StartSubAutoRefresh()

	// 清理
	StopSubAutoRefresh()
}

func TestStartSubAutoRefreshNoAPI(t *testing.T) {
	resetConfig := useTestConfigForAutoRefresh(t)
	defer resetConfig()

	// 设置为 login 模式，启用 autorefresh，但没有配置 API
	miraiConfig.GlobalConfig.Set("weibo.mode", "login")
	miraiConfig.GlobalConfig.Set("weibo.autorefresh", true)
	miraiConfig.GlobalConfig.Set("weibo.cookieRefreshAPI", "")

	// 调用 StartSubAutoRefresh 应该不会 panic
	StartSubAutoRefresh()

	// 清理
	StopSubAutoRefresh()
}

func useTestConfigForAutoRefresh(t *testing.T) func() {
	t.Helper()
	original := miraiConfig.GlobalConfig
	miraiConfig.GlobalConfig = &miraiConfig.Config{Viper: viper.New()}
	return func() {
		miraiConfig.GlobalConfig = original
	}
}
