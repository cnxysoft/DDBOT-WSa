package logging

import (
	"bytes"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/cnxysoft/DDBOT-WSa/logging/style"
	"github.com/mattn/go-colorable"
	"github.com/rifflock/lfshook"
	"github.com/sirupsen/logrus"
)

// ConsoleFormatter 把日志渲染成一行人读的样子：
//
//	11-14 23:10:41 INFO  [bilibili-concern] 订阅添加成功 room=123
//
// 它只用于控制台（stdout/stderr）。日志文件仍走 logrus.TextFormatter 的 logfmt
// （time="..." level="..." msg="..."），方便 grep 和脚本处理，两者互不影响。
type ConsoleFormatter struct {
	// DisableColors 关闭 ANSI 着色。默认由 NewConsoleFormatter 根据 stdout/stderr 是否终端决定，
	// 重定向到文件、或者设置了 NO_COLOR / TERM=dumb 时会自动关闭。
	DisableColors bool
	// TimestampFormat 时间戳格式，默认 "01-02 15:04:05"（月份-日期，不带年份，一屏里够看）。
	TimestampFormat string
}

// defaultConsoleTimestampFormat 控制台默认时间格式。
const defaultConsoleTimestampFormat = "01-02 15:04:05"

// consoleLevelTag 把 logrus 的级别名映射成定宽缩写（logrus 的 Warn 叫 "warning"，太长）。
var consoleLevelTag = map[logrus.Level]string{
	logrus.TraceLevel: "TRACE",
	logrus.DebugLevel: "DEBUG",
	logrus.InfoLevel:  "INFO",
	logrus.WarnLevel:  "WARN",
	logrus.ErrorLevel: "ERROR",
	logrus.FatalLevel: "FATAL",
	logrus.PanicLevel: "PANIC",
}

// consoleLevelColor 各级别对应的 SGR 参数。
var consoleLevelColor = map[logrus.Level]string{
	logrus.TraceLevel: style.Dim,
	logrus.DebugLevel: style.Dim,
	logrus.InfoLevel:  style.Green,
	logrus.WarnLevel:  style.Yellow,
	logrus.ErrorLevel: style.Red,
	logrus.FatalLevel: style.Red,
	logrus.PanicLevel: style.Red,
}

// NewConsoleFormatter 按当前进程的 stdout / stderr 是否终端决定是否着色。
func NewConsoleFormatter() *ConsoleFormatter {
	return &ConsoleFormatter{DisableColors: !colorEnabled(os.Stdout, os.Stderr)}
}

// colorEnabled 只有 stdout 与 stderr 都是可着色的终端时才开着色。
// 只重定向其中一个（例如 DDBOT 2>err.log）时也关掉，免得把 ANSI 写进文件。
func colorEnabled(stdout, stderr *os.File) bool {
	return style.Terminal(stdout) && style.Terminal(stderr)
}

// InstallConsole 给 logger 挂上控制台输出：人读格式 + 按级别分流（info 及以下走 stdout，
// warn 及以上走 stderr）。日志文件仍由 logger 自身的 Out 负责，两者格式互不影响。
func InstallConsole(l *logrus.Logger) {
	l.AddHook(lfshook.NewHook(consoleWriters(), NewConsoleFormatter()))
}

// consoleWriters 返回控制台 writer 的分流表。
// Windows 老控制台不认 ANSI，用 colorable 包一层做转换；非 Windows 上是原样透传。
func consoleWriters() lfshook.WriterMap {
	stdout := colorable.NewColorableStdout()
	stderr := colorable.NewColorableStderr()
	return lfshook.WriterMap{
		logrus.TraceLevel: stdout,
		logrus.DebugLevel: stdout,
		logrus.InfoLevel:  stdout,
		logrus.WarnLevel:  stderr,
		logrus.ErrorLevel: stderr,
		logrus.FatalLevel: stderr,
		logrus.PanicLevel: stderr,
	}
}

// Format 实现 logrus.Formatter。每个字段各占一个方括号，边界一眼能看出来：
//
//	[11-15 01:07:31] [INFO] [bilibili-concern] 订阅添加成功
func (f *ConsoleFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	timestampFormat := f.TimestampFormat
	if timestampFormat == "" {
		timestampFormat = defaultConsoleTimestampFormat
	}
	color := !f.DisableColors
	timestamp := entry.Time.Format(timestampFormat)
	levelText := consoleLevelText(entry.Level)
	module, hasModule := entry.Data["module"]

	var b bytes.Buffer
	b.WriteString(style.Paint(color, style.Dim, "["+timestamp+"]"))
	b.WriteString(" ")
	b.WriteString(style.Paint(color, consoleLevelColor[entry.Level], "["+levelText+"]"))
	if hasModule {
		b.WriteString(" ")
		b.WriteString(style.Paint(color, style.Cyan, "["+fmt.Sprint(module)+"]"))
	}
	b.WriteString(" ")
	// logrus 的 TextFormatter 会去掉消息末尾的一个换行，这里保持一致，避免空行
	b.WriteString(strings.TrimSuffix(entry.Message, "\n"))

	// 追加字段按 key 排序，输出稳定；module 已经渲染成独立列，不再重复
	keys := make([]string, 0, len(entry.Data))
	for k := range entry.Data {
		if k != "module" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteByte(' ')
		b.WriteString(style.Paint(color, style.Dim, k))
		b.WriteByte('=')
		b.WriteString(consoleValue(entry.Data[k]))
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// consoleLevelText 返回级别缩写（logrus 的 "warning" 缩成 "WARN"）。
func consoleLevelText(level logrus.Level) string {
	if tag, ok := consoleLevelTag[level]; ok {
		return tag
	}
	return strings.ToUpper(level.String())
}

// consoleValue 渲染字段值：只在有歧义时（含空白、引号、等号或为空）加引号。
func consoleValue(value interface{}) string {
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case error:
		s = v.Error()
	default:
		s = fmt.Sprint(v)
	}
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\n\"='") {
		return strconv.Quote(s)
	}
	return s
}
