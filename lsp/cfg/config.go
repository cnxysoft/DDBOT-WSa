package cfg

import (
	"errors"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/ghodss/yaml"
	"github.com/spf13/cast"
	"go.uber.org/atomic"
	"os"
	"strconv"
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

func GetWeiboCookieRefreshAPI() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("weibo.cookieRefreshAPI"))
}

// GetWeiboAPIModeBaseURL 获取微博 API 模式的基础 URL
func GetWeiboAPIModeBaseURL() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("weibo.apiModeBaseURL"))
}

// GetWeiboAutoRefresh 检查是否启用 SUB 自动刷新功能（仅 login 模式有效）。
// 成品证据（cfg.GetWeiboAutoRefresh @0xae8d60，128B）：IsSet("weibo.autorefresh")
// 为 false 时返回 true（默认开启），否则返回 GetBool。
func GetWeiboAutoRefresh() bool {
	if !config.GlobalConfig.IsSet("weibo.autorefresh") {
		return true
	}
	return config.GlobalConfig.GetBool("weibo.autorefresh")
}

// GetWeiboCookieAlertEnabled 检查是否启用 Cookie 失效告警通知
func GetWeiboCookieAlertEnabled() bool {
	// 默认启用，显式设置为 false 时禁用
	return !config.GlobalConfig.GetBool("weibo.disableCookieAlert")
}

func GetHeyboxToken() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("heybox.x_xhh_tokenid"))
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

// GetEnabledSites 获取站点启动白名单。
// 第二个返回值表示 sites.enabled 是否被显式配置：
// false（未配置）时切片为 nil，兼容旧配置启动全部已注册站点；
// true（含显式 enabled: []）时切片为去重小写后的站点名，可能为空，一个站点都不启动。
//
// 成品证据（cfg.GetEnabledSites @0xae7840，704B）：
//   - IsSet("sites.enabled") 为 false 时返回 (nil,0,0,false)：rax=rbx=rcx=rdi=0；
//   - 否则 GetStringSlice -> 逐项 strings.TrimSpace -> strings.ToLower，
//     丢弃空项，用 map[string]struct{} 去重后 append 返回，rdi=1。
//   - 调用点 concern.resolveSiteStartupSelection @0xaefb80（callsite +0x32 后
//     test %dil，为 0 走兼容分支）与 douyin.messageLinkParser.Enabled @0xd8dbc0
//     （callsite +0xb3 后 test %dil）都消费该布尔。
func GetEnabledSites() ([]string, bool) {
	if !config.GlobalConfig.IsSet("sites.enabled") {
		return nil, false
	}
	raw := config.GlobalConfig.GetStringSlice("sites.enabled")
	result := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		item = strings.ToLower(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result, true
}

// GetDefaultBlockAllGroups 获取新群默认屏蔽策略。
// 成品证据（cfg.GetDefaultBlockAllGroups @0xae7ce0，128B）：
// IsSet("bot.defaultBlockAllGroups") 为 false 时返回 true（未配置=默认全 block），
// 否则返回 GetBool。
// true：所有群默认处于block状态，不响应群指令、链接解析和群事件模板。
func GetDefaultBlockAllGroups() bool {
	if !config.GlobalConfig.IsSet("bot.defaultBlockAllGroups") {
		return true
	}
	return config.GlobalConfig.GetBool("bot.defaultBlockAllGroups")
}

// GetBilibiliCookieMode 获取B站 Cookie 来源模式：qrcode | cookiecloud。
func GetBilibiliCookieMode() string {
	return getCookieMode("bilibili")
}

// GetDouyinCookieMode 获取抖音 Cookie 来源模式：qrcode | cookiecloud。
func GetDouyinCookieMode() string {
	return getCookieMode("douyin")
}

// GetWeiboCookieMode 获取微博 Cookie 来源模式：qrcode | cookiecloud。
func GetWeiboCookieMode() string {
	return getCookieMode("weibo")
}

// getCookieMode 读取站点级 cookieMode。
//
// 成品证据（cfg.GetBilibiliCookieMode @0xae7d60 / cfg.GetDouyinCookieMode @0xae7f20 /
// cfg.GetWeiboCookieMode @0xae8060，各 96B；getCookieMode 已被内联，无独立符号）：
// 三段实现完全相同，仅键名不同，流程为
//
//	GetString("<site>.cookieMode") -> strings.TrimSpace -> strings.ToLower
//	  -> 长度为 0 时返回常量 "qrcode"（@0x109f6c4），否则原样返回。
//
// 没有对 "qrcode"/"cookiecloud" 的枚举校验，也没有 logger.Warnf 调用；
// 未知模式原样返回，由站点消费方（如 douyin）自行 Warn+fallback。
func getCookieMode(site string) string {
	mode := strings.ToLower(strings.TrimSpace(config.GlobalConfig.GetString(site + ".cookieMode")))
	if mode == "" {
		return "qrcode"
	}
	return mode
}

// GetBilibiliQRLoginEnabled 是否允许B站管理员确认后生成登录二维码；默认允许。
func GetBilibiliQRLoginEnabled() bool {
	if !config.GlobalConfig.IsSet("bilibili.qrlogin") {
		return true
	}
	return config.GlobalConfig.GetBool("bilibili.qrlogin")
}

// GetDouyinQRLoginEnabled 是否允许抖音管理员回复“生成 抖音”后生成二维码；默认允许。
func GetDouyinQRLoginEnabled() bool {
	if !config.GlobalConfig.IsSet("douyin.qrlogin") {
		return true
	}
	return config.GlobalConfig.GetBool("douyin.qrlogin")
}

// GetWeiboQRLoginEnabled 是否允许微博管理员回复“生成 微博”后生成二维码；默认允许。
func GetWeiboQRLoginEnabled() bool {
	if !config.GlobalConfig.IsSet("weibo.qrlogin") {
		return true
	}
	return config.GlobalConfig.GetBool("weibo.qrlogin")
}

// GetBilibiliCookieAlertEnabled 检查是否启用B站 Cookie 失效告警通知。
// 默认启用，显式设置为 false 时禁用。
func GetBilibiliCookieAlertEnabled() bool {
	return !config.GlobalConfig.GetBool("bilibili.disableCookieAlert")
}

// GetDouyinCookieAlertEnabled 检查是否启用抖音 Cookie 失效告警通知。
// 默认启用，显式设置为 false 时禁用。
func GetDouyinCookieAlertEnabled() bool {
	return !config.GlobalConfig.GetBool("douyin.disableCookieAlert")
}

// GetDouyinNotLiveConfirmTimes 获取抖音未开播确认次数；默认3，最大10。
// 成品证据：cfg.GetDouyinNotLiveConfirmTimes @config.go:453（0xae87e0，96B）：
// <=0 回退 3，>10 截断为 10。
func GetDouyinNotLiveConfirmTimes() int {
	times := config.GlobalConfig.GetInt("douyin.notLiveConfirmTimes")
	if times <= 0 {
		times = 3
	}
	if times > 10 {
		times = 10
	}
	return times
}

// GetWeiboStateDir 获取微博状态目录；保存完整 Cookie Jar 等状态。
// 成品证据：cfg.GetWeiboStateDir @config.go:497（0xae89a0，64B）只做
// TrimSpace(GetString("weibo.stateDir"))，不含默认值——默认由调用方补。
func GetWeiboStateDir() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("weibo.stateDir"))
}

// GetWeiboUserAgent 获取微博请求使用的 UA；空值由站点侧回退默认 UA。
func GetWeiboUserAgent() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("weibo.userAgent"))
}

// GetWeiboRequestTimeout 获取微博单次请求超时；默认15s，钳制到 [5s, 2m]。
// 成品证据：cfg.GetWeiboRequestTimeout @config.go:505（0xae8a20，128B）：
// <=0 -> 15s；<5s -> 5s；>2m -> 2m。
func GetWeiboRequestTimeout() time.Duration {
	timeout := config.GlobalConfig.GetDuration("weibo.requestTimeout")
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if timeout < 5*time.Second {
		timeout = 5 * time.Second
	}
	if timeout > 2*time.Minute {
		timeout = 2 * time.Minute
	}
	return timeout
}

// GetWeiboProtocolTimeout 获取微博完整协议步骤总超时；默认2m，
// 不小于 requestTimeout，最大10m。
func GetWeiboProtocolTimeout() time.Duration {
	timeout := config.GlobalConfig.GetDuration("weibo.protocolTimeout")
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	if requestTimeout := GetWeiboRequestTimeout(); timeout < requestTimeout {
		timeout = requestTimeout
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	return timeout
}

// GetWeiboQRLoginTimeout 获取微博二维码轮询总时限；默认3m，钳制到 [1m, 10m]。
// 成品证据：cfg.GetWeiboQRLoginTimeout @config.go:533（0xae8b20，128B）：
// <=0 -> 3m；<1m -> 1m；>10m -> 10m。
func GetWeiboQRLoginTimeout() time.Duration {
	timeout := config.GlobalConfig.GetDuration("weibo.qrloginTimeout")
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	if timeout < time.Minute {
		timeout = time.Minute
	}
	if timeout > 10*time.Minute {
		timeout = 10 * time.Minute
	}
	return timeout
}

// GetWeiboQRAutoOpen 是否自动打开二维码图片；默认 false，避免弹出外部窗口。
func GetWeiboQRAutoOpen() bool {
	return config.GlobalConfig.GetBool("weibo.qrAutoOpen")
}

// GetWeiboLoginRefreshInterval 获取微博登录态刷新间隔；默认30m，钳制到 [5m, 24h]。
// 成品证据：cfg.GetWeiboLoginRefreshInterval @config.go:559（0xae8c20，128B）：
// <=0 -> 30m；<5m -> 5m；>24h -> 24h。
func GetWeiboLoginRefreshInterval() time.Duration {
	interval := config.GlobalConfig.GetDuration("weibo.loginRefreshInterval")
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	if interval < 5*time.Minute {
		interval = 5 * time.Minute
	}
	if interval > 24*time.Hour {
		interval = 24 * time.Hour
	}
	return interval
}

// GetWeiboGuestRefreshInterval 获取微博访客态刷新间隔；默认1h，钳制到 [10m, 24h]。
// 成品证据：cfg.GetWeiboGuestRefreshInterval @config.go:573（0xae8ca0，128B）：
// <=0 -> 1h；<10m -> 10m；>24h -> 24h。
func GetWeiboGuestRefreshInterval() time.Duration {
	interval := config.GlobalConfig.GetDuration("weibo.guestRefreshInterval")
	if interval <= 0 {
		interval = time.Hour
	}
	if interval < 10*time.Minute {
		interval = 10 * time.Minute
	}
	if interval > 24*time.Hour {
		interval = 24 * time.Hour
	}
	return interval
}

// GetBilibiliCookieCloudCryptoType 获取B站 CookieCloud 加密类型；默认 auto。
func GetBilibiliCookieCloudCryptoType() string {
	return getCookieCloudCryptoType("bilibili")
}

// GetBilibiliCookieCloudEndpoint 获取B站 CookieCloud 服务端完整地址。
func GetBilibiliCookieCloudEndpoint() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("bilibili.cookieCloud.endpoint"))
}

// GetBilibiliCookieCloudUUID 获取B站 CookieCloud 浏览器插件配置的 UUID。
func GetBilibiliCookieCloudUUID() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("bilibili.cookieCloud.uuid"))
}

// GetBilibiliCookieCloudPassword 获取B站 CookieCloud 端到端加密密码。
func GetBilibiliCookieCloudPassword() string {
	return config.GlobalConfig.GetString("bilibili.cookieCloud.password")
}

// GetBilibiliCookieCloudInterval 获取B站 CookieCloud 刷新间隔；
// 默认10m，程序强制最小1m。
func GetBilibiliCookieCloudInterval() time.Duration {
	return getCookieCloudInterval("bilibili")
}

// GetBilibiliCookieCloudTimeout 获取B站 CookieCloud 单次拉取超时；默认15s。
func GetBilibiliCookieCloudTimeout() time.Duration {
	return getCookieCloudTimeout("bilibili")
}

// GetDouyinCookieCloudCryptoType 获取抖音 CookieCloud 加密类型；默认 auto。
func GetDouyinCookieCloudCryptoType() string {
	return getCookieCloudCryptoType("douyin")
}

// GetDouyinCookieCloudEndpoint 获取抖音 CookieCloud 服务端完整地址。
func GetDouyinCookieCloudEndpoint() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("douyin.cookieCloud.endpoint"))
}

// GetDouyinCookieCloudUUID 获取抖音 CookieCloud 浏览器插件配置的 UUID。
func GetDouyinCookieCloudUUID() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("douyin.cookieCloud.uuid"))
}

// GetDouyinCookieCloudPassword 获取抖音 CookieCloud 端到端加密密码。
func GetDouyinCookieCloudPassword() string {
	return config.GlobalConfig.GetString("douyin.cookieCloud.password")
}

// GetDouyinCookieCloudInterval 获取抖音 CookieCloud 刷新间隔；
// 默认10m，程序强制最小1m。
func GetDouyinCookieCloudInterval() time.Duration {
	return getCookieCloudInterval("douyin")
}

// GetDouyinCookieCloudTimeout 获取抖音 CookieCloud 单次拉取超时；默认15s。
func GetDouyinCookieCloudTimeout() time.Duration {
	return getCookieCloudTimeout("douyin")
}

// GetWeiboCookieCloudCryptoType 获取微博 CookieCloud 加密类型；默认 auto。
func GetWeiboCookieCloudCryptoType() string {
	return getCookieCloudCryptoType("weibo")
}

// GetWeiboCookieCloudEndpoint 获取微博 CookieCloud 服务端完整地址。
func GetWeiboCookieCloudEndpoint() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("weibo.cookieCloud.endpoint"))
}

// GetWeiboCookieCloudUUID 获取微博 CookieCloud 浏览器插件配置的 UUID。
func GetWeiboCookieCloudUUID() string {
	return strings.TrimSpace(config.GlobalConfig.GetString("weibo.cookieCloud.uuid"))
}

// GetWeiboCookieCloudPassword 获取微博 CookieCloud 端到端加密密码。
func GetWeiboCookieCloudPassword() string {
	return config.GlobalConfig.GetString("weibo.cookieCloud.password")
}

// GetWeiboCookieCloudInterval 获取微博 CookieCloud 刷新间隔；
// 默认10m，程序强制最小1m。
func GetWeiboCookieCloudInterval() time.Duration {
	return getCookieCloudInterval("weibo")
}

// GetWeiboCookieCloudTimeout 获取微博 CookieCloud 单次拉取超时；默认15s。
func GetWeiboCookieCloudTimeout() time.Duration {
	return getCookieCloudTimeout("weibo")
}

// getCookieCloudCryptoType 读取站点级 CookieCloud 加密类型，缺省 auto。
func getCookieCloudCryptoType(site string) string {
	cryptoType := strings.ToLower(strings.TrimSpace(config.GlobalConfig.GetString(site + ".cookieCloud.cryptoType")))
	if cryptoType == "" {
		return "auto"
	}
	return cryptoType
}

// getCookieCloudInterval 读取站点级 CookieCloud 刷新间隔，默认10m，强制最小1m。
func getCookieCloudInterval(site string) time.Duration {
	interval := config.GlobalConfig.GetDuration(site + ".cookieCloud.interval")
	if interval <= 0 {
		interval = 10 * time.Minute
	}
	if interval < time.Minute {
		interval = time.Minute
	}
	return interval
}

// getCookieCloudTimeout 读取站点级 CookieCloud 单次拉取超时，默认15s。
func getCookieCloudTimeout(site string) time.Duration {
	timeout := config.GlobalConfig.GetDuration(site + ".cookieCloud.timeout")
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return timeout
}

// IsAtAllRemainingLimiterEnabled 判断是否启用 @全体 剩余次数保护。
// 只要把 atAll 或其任一子项写进 application.yaml 就会启用；
// 完全不写 atAll 整块时不启用，也不调用 get_group_at_all_remain。
//
// 成品证据（cfg.IsAtAllRemainingLimiterEnabled @0xae81a0，192B）：连续 4 次
// viper.IsSet，命中即返回 true，键与长度分别为
// "atAll"(5) @0x109ab27、"atAll.remainingThreshold"(24) @0x10bb62d、
// "atAll.bilibiliUIDWhitelist"(26) @0x10be368、"atAll.douyinUIDWhitelist"(24) @0x10bb645。
func IsAtAllRemainingLimiterEnabled() bool {
	return config.GlobalConfig.IsSet("atAll") ||
		config.GlobalConfig.IsSet("atAll.remainingThreshold") ||
		config.GlobalConfig.IsSet("atAll.bilibiliUIDWhitelist") ||
		config.GlobalConfig.IsSet("atAll.douyinUIDWhitelist")
}

// GetAtAllRemainingThreshold 获取 @全体 剩余次数阈值。
// 非白名单主播：账号剩余次数小于此值时跳过 @全体，但正文仍正常发送。
// atAll已启用但本项没填时默认10；负数也会回退10；0表示只在剩余次数小于0时拦截。
func GetAtAllRemainingThreshold() int64 {
	const thresholdDefault int64 = 10
	if !config.GlobalConfig.IsSet("atAll.remainingThreshold") {
		return thresholdDefault
	}
	// 成品 @0xae8260 用 viper.GetInt（0x846420），返回值在 rax 上按 int64 比较：
	// IsSet 为 false 或 GetInt < 0 都回退 10（0xa）。
	threshold := int64(config.GlobalConfig.GetInt("atAll.remainingThreshold"))
	if threshold < 0 {
		return thresholdDefault
	}
	return threshold
}

// GetAtAllBilibiliUIDWhitelist 获取B站主播UID白名单；命中后跳过剩余次数限制。
// 不是QQ群号，也不会自动开启 @全体。
//
// 成品证据（cfg.GetAtAllBilibiliUIDWhitelist @0xae82e0，608B）：GetStringSlice 后
// makeslice(et = int64 @0xee72c0) 返回 []int64，元素大小 8（growslice 后
// 存 %rdx,-0x8(%rax,%rbx,8)）；逐项 strings.TrimSpace -> strconv.ParseInt(base=10,
// bitSize=64)，err != nil 或值 <= 0 丢弃，并用 map[int64]struct{}（@0xf67260）
// mapaccess2_fast64/mapassign_fast64 去重。因此返回类型是 []int64，且只含正数。
func GetAtAllBilibiliUIDWhitelist() []int64 {
	raw := config.GlobalConfig.GetStringSlice("atAll.bilibiliUIDWhitelist")
	result := make([]int64, 0, len(raw))
	seen := make(map[int64]struct{}, len(raw))
	for _, item := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(item), 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

// GetAtAllDouyinUIDWhitelist 获取抖音主播白名单；使用完整SEC_UID，不是短数字UID。
//
// 成品证据（cfg.GetAtAllDouyinUIDWhitelist @0xae8540，608B）：GetStringSlice 后
// makeslice(et = string @0xee7100) 返回 []string，元素大小 16
// （shl $0x4 + 两次 8 字节写）；逐项 strings.TrimSpace，丢弃空串，并用
// map[string]struct{}（@0xf663e0）mapaccess2_faststr/mapassign_faststr 去重。
// 返回类型确为 []string（不要改成 []int64）。
func GetAtAllDouyinUIDWhitelist() []string {
	raw := config.GlobalConfig.GetStringSlice("atAll.douyinUIDWhitelist")
	result := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}
