package lsp

import (
	"fmt"

	"github.com/robfig/cron/v3"
	"github.com/sirupsen/logrus"
)

// cronLogger 把 robfig/cron 自带日志接到本项目的 logger 上。
// 库自己打的是 "start" / "stop" / "added" 这类纯英文短语，直接透传会在中文日志里很突兀，
// 这里按动作翻成中文；键值对原样转成 logrus 字段，排障时该有的信息不丢。
type cronLogger struct {
	log *logrus.Entry
}

// cronMessage 是 robfig/cron 用到的全部 Info 动作。
var cronMessage = map[string]string{
	"start":    "定时任务调度已启动",
	"stop":     "定时任务调度已停止",
	"wake":     "定时任务调度被唤醒",
	"schedule": "已排定任务",
	"run":      "正在执行任务",
	"added":    "已新增任务",
	"removed":  "已移除任务",
}

// cronVisible 是保持 Info 级别可见的调度动作。改造前这些动作都走 VerbosePrintfLogger，
// 全在 info 级别；这里只把高频的 run / wake / schedule 降到 Debug（否则每个任务执行都刷一行），
// 任务的增删与调度的启停仍在 info 可见，用户能看见"任务加了没、调度起没起"。
var cronVisible = map[string]bool{
	"start":   true,
	"stop":    true,
	"added":   true,
	"removed": true,
}

func (l cronLogger) Info(msg string, keysAndValues ...interface{}) {
	entry := l.log.WithFields(cronFields(keysAndValues))
	visible := cronVisible[msg]
	if zh, ok := cronMessage[msg]; ok {
		msg = zh
	}
	if visible {
		entry.Info(msg)
		return
	}
	entry.Debug(msg)
}

func (l cronLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	fields := cronFields(keysAndValues)
	if zh, ok := cronMessage[msg]; ok {
		msg = zh
	}
	l.log.WithFields(fields).WithError(err).Error(msg)
}

// cronFields 把 cron 的 key/value 序列转成 logrus 字段，非字符串值按原样保留。
func cronFields(keysAndValues []interface{}) logrus.Fields {
	fields := logrus.Fields{}
	for i := 0; i+1 < len(keysAndValues); i += 2 {
		key := fmt.Sprint(keysAndValues[i])
		fields[key] = keysAndValues[i+1]
	}
	return fields
}

// 编译期确认实现了 cron.Logger。
var _ cron.Logger = cronLogger{}
