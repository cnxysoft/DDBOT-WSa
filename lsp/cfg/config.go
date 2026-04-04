package cfg

import (
	"errors"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/ghodss/yaml"
	"github.com/spf13/cast"
	"go.uber.org/atomic"
	"os"
	"strings"
	"time"
)

func MatchCmdWithPrefix(cmd string) (prefix string, command string, err error) {
	var customPrefixCfg = GetCustomCommandPrefix()
	if customPrefixCfg != nil {
		for k, v := range customPrefixCfg {
			if v+k == cmd {
				return v, k, nil
			}
		}
	}
	commonPrefix := GetCommandPrefix()
	if strings.HasPrefix(cmd, commonPrefix) {
		return commonPrefix, strings.TrimPrefix(cmd, commonPrefix), nil
	}
	return "", "", errors.New("match failed")
}

func GetCommandPrefix(commands ...string) string {
	if len(commands) > 0 {
		var customPrefixCfg = GetCustomCommandPrefix()
		if customPrefixCfg != nil {
			if prefix, found := customPrefixCfg[commands[0]]; found {
				return prefix
			}
		}
	}
	prefix := strings.TrimSpace(config.GlobalConfig.GetString("bot.commandPrefix"))
	if len(prefix) == 0 {
		prefix = "/"
	}
	return prefix
}

var customCommandPrefixAtomic atomic.Value

// ReloadCustomCommandPrefix TODO wtf
func ReloadCustomCommandPrefix() {
	var result map[string]string
	defer func() {
		customCommandPrefixAtomic.Store(result)
	}()
	data, err := os.ReadFile("application.yaml")
	if err != nil {
		return
	}
	var all = make(map[string]interface{})

	err = yaml.Unmarshal(data, &all)
	if err != nil {
		return
	}
	var a interface{}
	if val, ok := all["customCommandPrefix"]; ok && val != nil {
		a = val
	} else if val, ok := all["customcommandprefix"]; ok {
		a = val
	}
	if a == nil {
		return
	}
	result = cast.ToStringMapString(a)
}

func GetCustomCommandPrefix() map[string]string {
	var m = customCommandPrefixAtomic.Load()
	if m == nil {
		m = make(map[string]string)
	}
	return m.(map[string]string)
}

func GetEmitInterval() time.Duration {
	return config.GlobalConfig.GetDuration("concern.emitInterval")
}

// GetEmitIntervalForSite 获取站点级别的 emit interval
// 优先级：site.interval > concern.emitInterval > 默认值 5s
func GetEmitIntervalForSite(site string) time.Duration {
	// 先尝试站点配置
	interval := config.GlobalConfig.GetDuration(site + ".interval")
	if interval > 0 {
		return interval
	}
	// 返回全局配置
	return GetEmitInterval()
}

func GetLargeNotifyLimit() int {
	var limit = config.GlobalConfig.GetInt("dispatch.largeNotifyLimit")
	if limit <= 0 {
		limit = 50
	}
	return limit
}

type CronJob struct {
	Cron         string `yaml:"cron"`
	TemplateName string `yaml:"templateName"`
	Target       struct {
		Group   []int64 `yaml:"group"`
		Private []int64 `yaml:"private"`
	} `yaml:"target"`
}

func GetCronJob() []*CronJob {
	var result []*CronJob
	if err := config.GlobalConfig.UnmarshalKey("cronjob", &result); err != nil {
		logger.Errorf("GetCronJob UnmarshalKey <cronjob> error %v", err)
		return nil
	}
	return result
}

func GetTemplateEnabled() bool {
	return config.GlobalConfig.GetBool("template.enable")
}

func GetCustomGroupCommand() []string {
	return config.GlobalConfig.GetStringSlice("autoreply.group.command")
}

func GetCustomPrivateCommand() []string {
	return config.GlobalConfig.GetStringSlice("autoreply.private.command")
}

func GetAcfunDisableSub() bool {
	return config.GlobalConfig.GetBool("acfun.disableSub")
}

func GetAcfunUnsub() bool {
	return config.GlobalConfig.GetBool("acfun.unsub")
}

func GetBilibiliMinFollowerCap() int {
	return config.GlobalConfig.GetInt("bilibili.minFollowerCap")
}

func GetBilibiliDisableSub() bool {
	return config.GlobalConfig.GetBool("bilibili.disableSub")
}

func GetBilibiliHiddenSub() bool {
	return config.GlobalConfig.GetBool("bilibili.hiddenSub")
}

func GetBilibiliUnsub() bool {
	return config.GlobalConfig.GetBool("bilibili.unsub")
}

func GetTwitterUnsub() bool {
	return config.GlobalConfig.GetBool("twitter.unsub")
}

func GetNotifyParallel() int {
	var parallel = config.GlobalConfig.GetInt("notify.parallel")
	if parallel <= 0 {
		parallel = 1
	}
	return parallel
}

func GetBilibiliOnlyOnlineNotify() bool {
	return config.GlobalConfig.GetBool("bilibili.onlyOnlineNotify")
}

func GetWeiboOnlyOnlineNotify() bool {
	return config.GlobalConfig.GetBool("weibo.onlyOnlineNotify")
}

func GetYoutubeOnlyOnlineNotify() bool {
	return config.GlobalConfig.GetBool("youtube.onlyOnlineNotify")
}

func GetDouyinOnlyOnlineNotify() bool {
	return config.GlobalConfig.GetBool("douyin.onlyOnlineNotify")
}

func GetAcfunOnlyOnlineNotify() bool {
	return config.GlobalConfig.GetBool("acfun.onlyOnlineNotify")
}

func GetTwitchOnlyOnlineNotify() bool {
	return config.GlobalConfig.GetBool("twitch.onlyOnlineNotify")
}

func GetWeiboMode() string {
	mode := strings.TrimSpace(config.GlobalConfig.GetString("weibo.mode"))
	if mode == "" {
		mode = "guest"
		return mode
	}
	if mode != "guest" && mode != "login" && mode != "api" {
		logger.Warnf("GetWeiboMode invalid mode %q, fallback to guest", mode)
		mode = "guest"
	}
	return mode
}

// IsWeiboAPIMode 检查是否使用 API 刷新模式
func IsWeiboAPIMode() bool {
	return GetWeiboMode() == "api"
}

func GetWeiboInterval() time.Duration {
	interval := config.GlobalConfig.GetDuration("weibo.interval")
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return interval
}

func GetWeiboCookieRefreshAPI() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("weibo.cookieRefreshAPI"))
}

// GetWeiboAutoRefresh 检查是否启用 SUB 自动刷新功能（仅 login 模式有效）
func GetWeiboAutoRefresh() bool {
	return config.GlobalConfig.GetBool("weibo.autorefresh")
}

func GetExtDbEnable() bool {
	return config.GlobalConfig.GetBool("extDb.enable")
}

func GetExtDbPath() string {
	return config.GlobalConfig.GetString("extDb.path")
}
