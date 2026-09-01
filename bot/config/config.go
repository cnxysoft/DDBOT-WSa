package config

import (
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type Config struct {
	*viper.Viper
}

// GlobalConfig 默认全局配置
//
// 注：GetString/GetInt/GetBool/GetDuration 等读取方法由内嵌的 *viper.Viper 提升提供，
// 无需在 Config 上重复声明；配置热重载由 bot.go 直接调用 WatchConfig() 建立。
var GlobalConfig = &Config{
	viper.New(),
}

// Init 使用 ./application.yaml 初始化全局配置
func Init() {
	GlobalConfig.SetConfigName("application")
	GlobalConfig.SetConfigType("yaml")
	GlobalConfig.AddConfigPath(".")
	GlobalConfig.AddConfigPath("./config")

	err := GlobalConfig.ReadInConfig()
	if err != nil {
		logrus.WithField("config", "GlobalConfig").WithError(err).Fatal("unable to read global config")
	}
}
