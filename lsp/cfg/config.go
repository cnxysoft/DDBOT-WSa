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

// IsPlatformDisabled 判断平台是否被 WebUI 停用（platform.<site>.enable 显式为 false）。
// 缺省或 true 均视为启用。走 viper 热配置读取，admin 原子写入配置文件后
// 经 WatchConfig 自动重载，无需重启即生效。
func IsPlatformDisabled(site string) bool {
	if site == "" {
		return false
	}
	key := "platform." + site + ".enable"
	return config.GlobalConfig.IsSet(key) && !config.GlobalConfig.GetBool(key)
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

// ConfigFilePath 返回 viper 实际加载的配置文件路径。
//
// 配置读写一律经此函数，不要硬编码 "application.yaml"：viper 的搜索路径含 "."
// 与 "./config"（见 bot/config/config.go 的 Init），硬编码会在配置放在 ./config/
// 下时读写到另一个文件——轻则改了不生效，重则往当前目录写出一份与生效配置无关的新文件。
//
// 配置尚未加载时（ConfigFileUsed 为空，例如单元测试）回退到 CWD 的 application.yaml。
func ConfigFilePath() string {
	if p := config.GlobalConfig.ConfigFileUsed(); p != "" {
		return p
	}
	return "application.yaml"
}

// ReloadCustomCommandPrefix 重新读取 customCommandPrefix 并原子替换缓存。
//
// 这块最容易踩的坑是静默失效，所以失败一律落日志：
// YAML 里冒号写成全角「：」时，整个块不构成映射而是退化成一段多行字符串，
// cast.ToStringMapString 拿到空 map 且不报错，用户只会看到「自定义前缀不生效」。
func ReloadCustomCommandPrefix() {
	var result map[string]string
	defer func() {
		customCommandPrefixAtomic.Store(result)
	}()
	data, err := os.ReadFile(ConfigFilePath())
	if err != nil {
		return
	}
	var all = make(map[string]interface{})

	err = yaml.Unmarshal(data, &all)
	if err != nil {
		logger.Errorf("customCommandPrefix 读取失败：配置文件 %s 不是合法 YAML: %v", ConfigFilePath(), err)
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
	if len(result) == 0 {
		// 显式写成空表（customCommandPrefix: {}）是合法配置——WebUI 里清空条目就是这个，
		// 不能报错。只有「类型根本不是映射」才是要提醒的情况。
		if _, isMap := a.(map[string]interface{}); isMap {
			return
		}
		if str, isStr := a.(string); isStr && strings.TrimSpace(str) != "" {
			// 全角冒号不是 YAML 键分隔符，整块会退化成一段字符串——这是最常见的踩坑形态，
			// 报错必须点明原因，否则用户只会看到「自定义前缀不生效」。
			logger.Errorf("customCommandPrefix 解析结果为空：期望「命令: 前缀」映射，实际是一段字符串。"+
				"最常见的原因是键值之间的冒号写成了全角「：」（全角冒号不是 YAML 键分隔符）。实际值: %q", str)
			return
		}
		logger.Errorf("customCommandPrefix 解析结果为空：期望「命令: 前缀」映射，实际类型 %T，值 %v", a, a)
	}
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
