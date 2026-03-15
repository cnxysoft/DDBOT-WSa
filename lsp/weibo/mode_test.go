package weibo

import (
	"testing"

	miraiConfig "github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/sirupsen/logrus"
	logrustest "github.com/sirupsen/logrus/hooks/test"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestWeiboModeSelection(t *testing.T) {
	resetConfig := useTestConfig(t)
	defer resetConfig()

	miraiConfig.GlobalConfig.Set("weibo.mode", "guest")
	assert.True(t, isGuestMode())

	miraiConfig.GlobalConfig.Set("weibo.mode", "login")
	assert.False(t, isGuestMode())
}

func TestWeiboModeInvalidFallback(t *testing.T) {
	resetConfig := useTestConfig(t)
	defer resetConfig()

	hook := logrustest.NewGlobal()
	defer hook.Reset()

	miraiConfig.GlobalConfig.Set("weibo.mode", "unknown")
	mode := cfg.GetWeiboMode()

	assert.Equal(t, "guest", mode)
	assert.NotEmpty(t, hook.Entries)
	assert.Equal(t, logrus.WarnLevel, hook.LastEntry().Level)
}

func useTestConfig(t *testing.T) func() {
	t.Helper()
	original := miraiConfig.GlobalConfig
	miraiConfig.GlobalConfig = &miraiConfig.Config{Viper: viper.New()}
	return func() {
		miraiConfig.GlobalConfig = original
	}
}
