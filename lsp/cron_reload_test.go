package lsp

import (
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/robfig/cron/v3"
	"github.com/spf13/viper"
)

func TestCronReloadPreservesJobsWhenAnyScheduleIsInvalid(t *testing.T) {
	previous := config.GlobalConfig
	config.GlobalConfig = &config.Config{Viper: viper.New()}
	t.Cleanup(func() { config.GlobalConfig = previous })
	l := &Lsp{cron: cron.New()}
	config.GlobalConfig.Set("cronjob", []map[string]interface{}{{"cron": "0 * * * *", "templateName": "old"}})
	l.CronjobReload()
	before := l.cron.Entries()
	if len(before) != 1 {
		t.Fatal("initial job missing")
	}
	config.GlobalConfig.Set("cronjob", []map[string]interface{}{{"cron": "*/5 * * * *"}, {"cron": "invalid"}})
	l.CronjobReload()
	got := l.cron.Entries()
	if len(got) != 1 || got[0].ID != before[0].ID {
		t.Fatal("invalid replacement removed existing jobs")
	}
	config.GlobalConfig.Set("cronjob", []map[string]interface{}{{"cron": "*/5 * * * *", "templateName": "new"}})
	l.CronjobReload()
	got = l.cron.Entries()
	if len(got) != 1 || got[0].Job.(*cronjobRun).TemplateName != "new" {
		t.Fatal("valid replacement failed")
	}
}

func TestCronReloadKeepsIntervalScheduleForUnrelatedSettings(t *testing.T) {
	previous := config.GlobalConfig
	config.GlobalConfig = &config.Config{Viper: viper.New()}
	t.Cleanup(func() { config.GlobalConfig = previous })
	l := &Lsp{cron: cron.New()}
	config.GlobalConfig.Set("cronjob", []map[string]interface{}{{"cron": "@every 1h", "templateName": "hourly"}})
	l.CronjobReload()
	l.CronStart()
	defer l.CronStop()
	before := l.cron.Entries()[0]
	config.GlobalConfig.Set("bot.commandPrefix", "!")
	l.CronjobReload()
	after := l.cron.Entries()[0]
	if before.ID != after.ID || !before.Next.Equal(after.Next) {
		t.Fatal("unrelated settings postponed an interval job")
	}
	config.GlobalConfig.Set("cronjob", []map[string]interface{}{{"cron": "@every 2h", "templateName": "hourly"}})
	l.CronjobReload()
	if l.cron.Entries()[0].ID == before.ID {
		t.Fatal("changed schedule was not replaced")
	}
}
