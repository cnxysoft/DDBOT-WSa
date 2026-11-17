package cfg

import (
	"fmt"
	"strings"
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

func IsHotKey(key string) bool {
	key = strings.ToLower(key)
	switch key {
	case "loglevel", "ffmpeg.loglevel", "bot.commandprefix", "customcommandprefix", "cronjob",
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
