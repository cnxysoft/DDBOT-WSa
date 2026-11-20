package logging

import (
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// entryAt 造一条固定时间戳的日志，方便断言输出。
func entryAt(level logrus.Level, msg string, data logrus.Fields) *logrus.Entry {
	return &logrus.Entry{
		Logger:  logrus.StandardLogger(),
		Time:    time.Date(2026, 11, 15, 1, 7, 31, 0, time.Local),
		Level:   level,
		Message: msg,
		Data:    data,
	}
}

// ansiRe 匹配 ANSI 颜色序列，用来断言"剥掉颜色后内容一致"。
var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

// TestConsoleFormatter_Plain 验证方括号格式：[时间] [级别] [模块] 消息。
func TestConsoleFormatter_Plain(t *testing.T) {
	f := &ConsoleFormatter{DisableColors: true, TimestampFormat: "01-02 15:04:05"}

	out, err := f.Format(entryAt(logrus.InfoLevel, "订阅添加成功", logrus.Fields{
		"module": "bilibili-concern",
		"room":   123,
	}))
	require.NoError(t, err)
	assert.Equal(t, "[11-15 01:07:31] [INFO] [bilibili-concern] 订阅添加成功 room=123\n", string(out))

	// 无 module 的日志不能出现空方括号
	out, err = f.Format(entryAt(logrus.WarnLevel, "Web 管理面板未启用", nil))
	require.NoError(t, err)
	assert.Equal(t, "[11-15 01:07:31] [WARN] Web 管理面板未启用\n", string(out))
}

// TestConsoleFormatter_LevelTag 验证 logrus 的 "warning" 缩成 WARN。
func TestConsoleFormatter_LevelTag(t *testing.T) {
	f := &ConsoleFormatter{DisableColors: true}
	for _, c := range []struct {
		level logrus.Level
		tag   string
	}{
		{logrus.TraceLevel, "TRACE"},
		{logrus.DebugLevel, "DEBUG"},
		{logrus.InfoLevel, "INFO"},
		{logrus.WarnLevel, "WARN"},
		{logrus.ErrorLevel, "ERROR"},
		{logrus.FatalLevel, "FATAL"},
		{logrus.PanicLevel, "PANIC"},
	} {
		out, err := f.Format(entryAt(c.level, "m", nil))
		require.NoError(t, err)
		assert.Equal(t, "[11-15 01:07:31] ["+c.tag+"] m\n", string(out))
	}
}

// TestConsoleFormatter_Fields 验证字段排序、module 不重复、值的按需加引号。
func TestConsoleFormatter_Fields(t *testing.T) {
	f := &ConsoleFormatter{DisableColors: true}

	out, err := f.Format(entryAt(logrus.ErrorLevel, "请求失败", logrus.Fields{
		"module": "huya-concern",
		"uid":    456,
		"room":   123,
		"error":  errors.New("boom"),
	}))
	require.NoError(t, err)
	assert.Equal(t, "[11-15 01:07:31] [ERROR] [huya-concern] 请求失败 error=boom room=123 uid=456\n", string(out))
	assert.Equal(t, 1, strings.Count(string(out), "huya-concern"))

	valueCases := []struct {
		in   interface{}
		want string
	}{
		{"", "\"\""},
		{"a b", "\"a b\""},
		{"a=b", "\"a=b\""},
		{"简单中文", "简单中文"},
		{123, "123"},
		{true, "true"},
	}
	for _, c := range valueCases {
		assert.Equal(t, c.want, consoleValue(c.in), "value=%v", c.in)
	}
}

// TestConsoleFormatter_Colors 验证着色输出剥掉 ANSI 后与无色输出一致。
func TestConsoleFormatter_Colors(t *testing.T) {
	entry := entryAt(logrus.WarnLevel, "cookie 即将过期", logrus.Fields{
		"module": "lsp",
		"error":  "file does not exist",
	})

	plain, err := (&ConsoleFormatter{DisableColors: true}).Format(entry)
	require.NoError(t, err)
	colored, err := (&ConsoleFormatter{}).Format(entry)
	require.NoError(t, err)

	assert.Contains(t, string(colored), "\x1b[33m", "warn 应该是黄色")
	assert.Equal(t, string(plain), ansiRe.ReplaceAllString(string(colored), ""))
}

// TestConsoleFormatter_MessageTrailingNewline 验证消息末尾换行被吃掉，不会多出空行。
func TestConsoleFormatter_MessageTrailingNewline(t *testing.T) {
	f := &ConsoleFormatter{DisableColors: true}
	out, err := f.Format(entryAt(logrus.InfoLevel, "多行消息第一行\n", nil))
	require.NoError(t, err)
	assert.Equal(t, "[11-15 01:07:31] [INFO] 多行消息第一行\n", string(out))
}

// TestColorEnabled 验证两个流都得是终端才着色：只重定向其中一个时也不着色。
func TestColorEnabled(t *testing.T) {
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	require.NoError(t, err)
	defer devNull.Close()

	assert.False(t, colorEnabled(nil, nil))
	assert.False(t, colorEnabled(os.Stdout, nil))
	assert.False(t, colorEnabled(devNull, devNull))
}
