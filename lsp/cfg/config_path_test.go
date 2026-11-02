package cfg

import (
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

// ConfigFilePath 必须跟随 viper 实际加载的配置文件，而不是硬编码 application.yaml。
// 回归背景：配置放在 ./config/ 下时，硬编码路径会读到另一个文件——
// 轻则改了不生效，重则往 CWD 写出一份与生效配置无关的新文件。
func TestConfigFilePathFollowsViper(t *testing.T) {
	old := config.GlobalConfig
	defer func() { config.GlobalConfig = old }()

	const want = "/tmp/ddbot-test-config/application.yaml"
	v := viper.New()
	v.SetConfigFile(want)
	config.GlobalConfig = &config.Config{Viper: v}

	if got := ConfigFilePath(); got != want {
		t.Fatalf("ConfigFilePath() = %q, want %q", got, want)
	}
}

// 配置尚未加载时（ConfigFileUsed 为空）回退到 CWD 的 application.yaml，
// 保证 ReloadCustomCommandPrefix 等调用方在测试环境下不 panic、不读空路径。
func TestConfigFilePathFallback(t *testing.T) {
	old := config.GlobalConfig
	defer func() { config.GlobalConfig = old }()

	config.GlobalConfig = &config.Config{Viper: viper.New()}

	if got := ConfigFilePath(); got != "application.yaml" {
		t.Fatalf("ConfigFilePath() = %q, want %q", got, "application.yaml")
	}
}
