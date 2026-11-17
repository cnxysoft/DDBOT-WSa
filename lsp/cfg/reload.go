package cfg

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
)

// ConfigureReload installs application validation and the supported hot-setting policy.
// Unlisted settings keep their running value until restart.
func ConfigureReload() {
	config.GlobalConfig.ConfigureReload(ValidateReload, IsHotKey)
}

var intervalSites = map[string]bool{
	"bilibili": true, "acfun": true, "douyin": true, "twitter": true,
	"twitch": true, "weibo": true, "huya": true, "douyu": true, "xhs": true, "heybox": true,
}

var resourceHotReload atomic.Bool

// EnableResourceHotReload is called only after every live resource participant is installed.
func EnableResourceHotReload() { resourceHotReload.Store(true) }

var resourceHotKeys = map[string]bool{
	"adapter.mode": true, "websocket.mode": true, "websocket.ws-server": true, "websocket.ws-reverse": true, "websocket.token": true,
	"admin.enable": true, "admin.addr": true, "admin.token": true,
	"proxy.type": true, "localproxypool.oversea": true, "localproxypool.mainland": true, "pyproxypool.host": true,
	"imagepool.type": true, "loliconpool.apikey": true, "loliconpool.proxy": true, "loliconpool.cachemin": true, "loliconpool.cachemax": true, "localpool.imagedir": true,
	"template.enable": true, "extdb.enable": true, "extdb.path": true,
	"telegram.enable": true, "telegram.token": true, "telegram.endpoint": true, "telegram.chats": true,
	"bilibili.sessdata": true, "bilibili.bili_jct": true, "bilibili.qrlogin": true,
	"weibo.mode": true, "weibo.sub": true, "weibo.qrlogin": true, "weibo.autorefresh": true, "weibo.cookierefreshapi": true, "weibo.apimodebaseurl": true,
	"qqofficial.app-id": true, "qqofficial.app-secret": true, "qqofficial.mode": true, "qqofficial.intents": true,
	"qqofficial.appid": true, "qqofficial.appsecret": true, "qqofficial.secret": true,
	"qqofficial.shard-id": true, "qqofficial.shard-count": true, "qqofficial.api-base-url": true,
	"qqofficial.http-timeout": true, "qqofficial.token-refresh-ahead": true, "qqofficial.start-timeout": true,
	"qqofficial.state-file": true, "qqofficial.join-intro-tip": true,
	"bilibili.account": true, "bilibili.password": true,
	"acfun.account": true, "acfun.password": true, "acfun.authkey": true, "acfun.acpasstoken": true,
	"douyin.useragent": true, "douyin.acsignature": true, "douyin.acnonce": true, "douyin.sessionid": true,
	"twitch.clientid": true, "twitch.clientsecret": true,
	"twitcasting.clientid": true, "twitcasting.clientsecret": true,
	"twitter.mode": true, "twitter.apifetchmode": true, "twitter.baseurl": true, "twitter.useragent": true,
	"twitter.ct0": true, "twitter.auth_token": true, "twitter.bearertoken": true, "twitter.queryid": true, "twitter.usertweetsqueryid": true, "twitter.screenname": true,
	"twitter.queryidrefreshinterval": true,
	"heybox.x_xhh_tokenid":           true,
}

func IsHotKey(key string) bool {
	key = strings.ToLower(key)
	if resourceHotReload.Load() && (resourceHotKeys[key] || strings.HasPrefix(key, "xhs.cookies.") || key == "xhs.cookies" || strings.HasPrefix(key, "telegram.commands.")) {
		return true
	}
	switch key {
	case "loglevel", "ffmpeg.loglevel", "bot.commandprefix", "customcommandprefix", "cronjob", "notify.parallel", "twitch.onlyonlinenotify",
		"autoreply.group.command", "autoreply.private.command", "dispatch.largenotifylimit",
		"debug.group", "debug.uin", "concern.emitinterval", "bilibili.imagemergemode",
		"bot.onjoingroup.rename", "bot.ondisconnected", "bot.offlinequeue.enable", "bot.offlinequeue.expire",
		"bilibili.unsub", "bilibili.hiddensub", "bilibili.disablesub", "bilibili.minfollowercap",
		"bilibili.autoparseposts", "bilibili.secanalysis", "bilibili.onlyonlinenotify",
		"acfun.unsub", "acfun.disablesub", "acfun.onlyonlinenotify", "douyin.onlyonlinenotify",
		"weibo.onlyonlinenotify", "weibo.disablecookiealert", "youtube.onlyonlinenotify",
		"xhs.onlyonlinenotify", "heybox.onlyonlinenotify", "twitter.unsub", "twitter.retweetfulltext",
		"twitter.translate.enabled", "twitter.translate.targetlang", "twitter.translate.mininterval",
		"twitter.translate.batchwait", "twitcasting.namestrategy", "twitcasting.broadcaster.title",
		"twitcasting.broadcaster.created", "twitcasting.broadcaster.image":
		return true
	}
	if strings.HasPrefix(key, "customcommandprefix.") {
		return true
	}
	parts := strings.Split(key, ".")
	if len(parts) == 3 && parts[0] == "platform" && parts[2] == "enable" {
		return true
	}
	if len(parts) == 2 && intervalSites[parts[0]] && parts[1] == "interval" {
		return true
	}
	if len(parts) == 2 && parts[0] == "imagemerge" || len(parts) == 3 && parts[1] == "imagemerge" {
		switch parts[len(parts)-1] {
		case "mode", "layout", "maxwidth", "maxheight":
			return true
		}
	}
	return false
}

func ValidateReload(s *config.Snapshot) error {
	if err := validateResourceValues(s); err != nil {
		return err
	}
	if err := validateHotValues(s); err != nil {
		return err
	}
	if _, err := parseCustomCommandPrefix(s.RawYAML()); err != nil {
		return err
	}
	if level := s.GetString("logLevel"); level != "" {
		if _, err := logrus.ParseLevel(level); err != nil {
			return fmt.Errorf("logLevel 不是有效日志等级")
		}
	}
	switch strings.ToLower(strings.TrimSpace(s.GetString("ffmpeg.logLevel"))) {
	case "", "quiet", "off", "silent", "panic", "fatal", "error", "warning", "warn", "info", "verbose", "debug", "trace":
	default:
		return fmt.Errorf("ffmpeg.logLevel 不是有效日志等级")
	}
	if _, _, err := ParseCronJobs(s); err != nil {
		return err
	}
	for site := range intervalSites {
		key := site + ".interval"
		if !s.IsSet(key) {
			continue
		}
		if !validInterval(s.Get(key), site == "twitter") {
			return fmt.Errorf("%s 必须是非负时间间隔，例如 30s", key)
		}
	}
	return nil
}

func ParseCronJobs(s *config.Snapshot) ([]*CronJob, []cron.Schedule, error) {
	if err := validateCronSchema(s.Get("cronjob")); err != nil {
		return nil, nil, err
	}
	var jobs []*CronJob
	if err := s.UnmarshalKey("cronjob", &jobs); err != nil {
		return nil, nil, fmt.Errorf("cronjob 格式无效")
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	schedules := make([]cron.Schedule, 0, len(jobs))
	for i, job := range jobs {
		if job == nil {
			return nil, nil, fmt.Errorf("cronjob[%d] 不能为空", i)
		}
		if len(job.Target.Group) == 0 {
			job.Target.Group = nil
		}
		if len(job.Target.Private) == 0 {
			job.Target.Private = nil
		}
		schedule, err := parser.Parse(job.Cron)
		if err != nil {
			return nil, nil, fmt.Errorf("cronjob[%d].cron 不是有效的五字段定时表达式", i)
		}
		schedules = append(schedules, schedule)
	}
	return jobs, schedules, nil
}

// PollInterval is read when a polling round schedules its next run.
func PollInterval(site string, fallback time.Duration) time.Duration {
	if config.GlobalConfig == nil {
		return fallback
	}
	interval := config.GlobalConfig.GetDuration(site + ".interval")
	if interval <= 0 {
		return fallback
	}
	return interval
}
