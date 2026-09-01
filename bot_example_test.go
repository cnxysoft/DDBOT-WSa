package DDBOT

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

// TestExampleConfigValid 验证 exampleConfig 生成的内容是可解析的 YAML
// 确保启动时自动生成的 application.yaml 不会因语法错误导致启动失败
func TestExampleConfigValid(t *testing.T) {
	v := viper.New()
	v.SetConfigType("yaml")
	assert.NoError(t, v.ReadConfig(strings.NewReader(exampleConfig)))

	// 抽查关键配置键存在且默认值正确
	assert.Equal(t, "/", v.GetString("bot.commandPrefix"))
	assert.Equal(t, "systemProxy", v.GetString("proxy.type"))
	assert.Equal(t, "mirror", v.GetString("twitter.mode"))
	assert.False(t, v.GetBool("twitter.translate.enabled"))
	assert.False(t, v.GetBool("twitter.retweetFullText"))
	assert.Equal(t, "168h", v.GetString("twitter.queryIdRefreshInterval"))
	assert.False(t, v.GetBool("notify.parallel") == false)
	assert.Equal(t, 50, v.GetInt("dispatch.largeNotifyLimit"))
	assert.Equal(t, 60, int(v.GetDuration("douyu.interval").Seconds()))
	assert.Equal(t, 60, int(v.GetDuration("huya.interval").Seconds()))
	assert.False(t, v.GetBool("message-marker.disable"))
	assert.Equal(t, "onebot-v11", v.GetString("adapter.mode"))
	assert.Equal(t, "ws-server", v.GetString("websocket.mode"))
	assert.Equal(t, "info", v.GetString("logLevel"))
}
