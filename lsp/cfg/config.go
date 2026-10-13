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

func GetXHSOnlyOnlineNotify() bool {
	return config.GlobalConfig.GetBool("xhs.onlyOnlineNotify")
}

func GetTwitchOnlyOnlineNotify() bool {
	return config.GlobalConfig.GetBool("twitch.onlyOnlineNotify")
}

func GetWeiboMode() string {
	mode := strings.TrimSpace(config.GlobalConfig.GetString("weibo.mode"))
	if mode == "" {
		// 未指定模式时，如果已配置 SUB 则自动使用 login 模式
		sub := strings.TrimSpace(config.GlobalConfig.GetString("weibo.sub"))
		if sub != "" {
			mode = "login"
			logger.Debugf("weibo.mode 未配置，检测到 weibo.sub 已配置，自动使用 login 模式")
		} else {
			mode = "guest"
		}
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

// GetWeiboAPIModeBaseURL 获取微博 API 模式的基础 URL
func GetWeiboAPIModeBaseURL() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("weibo.apiModeBaseURL"))
}

// GetWeiboAutoRefresh 检查是否启用 SUB 自动刷新功能（仅 login 模式有效）
func GetWeiboAutoRefresh() bool {
	return config.GlobalConfig.GetBool("weibo.autorefresh")
}

// GetWeiboAlertGroupId 获取 Cookie 告警通知的目标群号，0 表示不发群
// Deprecated: 新告警逻辑改为通过 /config weibo_alert 逐群控制，此配置项已不再使用
func GetWeiboAlertGroupId() int64 {
	return config.GlobalConfig.GetInt64("weibo.alertGroupId")
}

// GetWeiboCookieAlertEnabled 检查是否启用 Cookie 失效告警通知
// 默认关闭：仅当显式配置 weibo.disableCookieAlert: false 时才启用。
// 自动生成的配置写的是 disableCookieAlert: true，与此默认一致；
// 未配置该键（例如使用最小配置）时同样按关闭处理。
func GetWeiboCookieAlertEnabled() bool {
	if !config.GlobalConfig.IsSet("weibo.disableCookieAlert") {
		return false
	}
	return !config.GlobalConfig.GetBool("weibo.disableCookieAlert")
}

// GetWeiboAlertQQList 获取 Cookie 告警通知的 QQ 列表（私聊）
// 支持两种格式：
// 1. 数组格式：[123456, 789012]
// 2. 字符串格式："123456,789012" 或 "123456 789012"
func GetWeiboAlertQQList() []int64 {
	var result []int64

	// 尝试从 viper 获取数组
	list := config.GlobalConfig.GetStringSlice("weibo.alertQQList")
	if len(list) > 0 {
		for _, s := range list {
			if id := cast.ToInt64(s); id > 0 {
				result = append(result, id)
			}
		}
		return result
	}

	// 尝试从字符串解析（逗号或空格分隔）
	str := strings.TrimSpace(config.GlobalConfig.GetString("weibo.alertQQList"))
	if str == "" {
		return nil
	}

	// 替换逗号为空格，然后按空格分割
	str = strings.ReplaceAll(str, ",", " ")
	for _, s := range strings.Fields(str) {
		if id := cast.ToInt64(s); id > 0 {
			result = append(result, id)
		}
	}
	return result
}

// GetSnapCastURL 获取 SnapCast 服务地址，用于生成微博 rid
func GetSnapCastURL() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("weibo.snapcastURL"))
}

func GetHeyboxToken() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("heybox.x_xhh_tokenid"))
}

func GetHeyboxInterval() time.Duration {
	interval := config.GlobalConfig.GetDuration("heybox.interval")
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return interval
}

func GetHeyboxOnlyOnlineNotify() bool {
	return config.GlobalConfig.GetBool("heybox.onlyOnlineNotify")
}

func GetExtDbEnable() bool {
	return config.GlobalConfig.GetBool("extDb.enable")
}

func GetExtDbPath() string {
	return config.GlobalConfig.GetString("extDb.path")
}
