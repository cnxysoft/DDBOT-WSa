package cfg

import (
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

// IsPlatformDisabled 语义：缺省或 true = 启用；显式 false = 停用
func TestIsPlatformDisabled(t *testing.T) {
	old := config.GlobalConfig
	defer func() { config.GlobalConfig = old }()
	v := viper.New()
	v.Set("platform.bilibili.enable", false)
	v.Set("platform.weibo.enable", true)
	config.GlobalConfig = &config.Config{Viper: v}

	if !IsPlatformDisabled("bilibili") {
		t.Error("显式 false 应视为停用")
	}
	if IsPlatformDisabled("weibo") {
		t.Error("显式 true 应视为启用")
	}
	if IsPlatformDisabled("twitter") {
		t.Error("未设置应视为启用（缺省启用）")
	}
	if IsPlatformDisabled("") {
		t.Error("空 site 不应停用")
	}
}
