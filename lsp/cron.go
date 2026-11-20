package lsp

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/mmsg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/template"
	"github.com/sirupsen/logrus"
)

var cronLog = logrus.WithField("module", "cronjob")

type cronjobRun struct {
	*cfg.CronJob
	l *Lsp
}

func (c *cronjobRun) Run() {
	templateName := fmt.Sprintf("custom.cronjob.%s.tmpl", c.TemplateName)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for _, groupCode := range c.Target.Group {
			m, err := template.LoadAndExec(templateName, map[string]interface{}{
				"target": groupCode,
			})
			if err != nil {
				cronLog.WithError(err).WithField("template", templateName).
					Errorf("定时任务模板执行失败 (group=%d)", groupCode)
			}
			if m != nil {
				c.l.SendMsg(m, mmsg.NewGroupTarget(groupCode))
			}
		}
	}()
	go func() {
		defer wg.Done()
		for _, uin := range c.Target.Private {
			m, err := template.LoadAndExec(templateName, map[string]interface{}{
				"target": uin,
			})
			if err != nil {
				cronLog.WithError(err).WithField("template", templateName).
					Errorf("定时任务模板执行失败 (private=%d)", uin)
			}
			if m != nil {
				c.l.SendMsg(m, mmsg.NewPrivateTarget(uin))
			}
		}
	}()
	wg.Wait()
}

func (l *Lsp) CronjobReload() {
	l.cronMu.Lock()
	defer l.cronMu.Unlock()
	jobs, schedules, err := cfg.ParseCronJobs(config.GlobalConfig.Snapshot())
	if err != nil {
		cronLog.WithError(err).Error("定时任务配置无效，保留现有任务")
		return
	}
	if l.cronConfigured && reflect.DeepEqual(l.cronConfig, jobs) {
		return
	}
	for _, entry := range l.cron.Entries() {
		l.cron.Remove(entry.ID)
	}
	for i, entry := range jobs {
		l.cron.Schedule(schedules[i], &cronjobRun{entry, l})
	}
	l.cronConfig, l.cronConfigured = jobs, true
}

func (l *Lsp) CronStart() {
	l.cron.Start()
}

func (l *Lsp) CronStop() {
	<-l.cron.Stop().Done()
}
