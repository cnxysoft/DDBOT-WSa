package logstyle

import (
	"bytes"
	"fmt"
	"io"

	"github.com/mattn/go-colorable"
	"github.com/sirupsen/logrus"
)

// consoleFormatter 在基础 TextFormatter 的输出之上，将行首的 level="xxx" 字段替换为
// 带 ANSI 颜色的版本，仅用于控制台输出；写文件时直接使用基础 TextFormatter，
// 避免颜色码进入日志文件。
type consoleFormatter struct {
	plain *logrus.TextFormatter
}

func NewFileFormatter() logrus.Formatter {
	return newTextFormatter()
}

// NewConsoleFormatter 返回控制台用的格式器：在基础 TextFormatter 输出之上，
// 按 level 给 level 字段染色（[推断] 成品反汇编无法区分无参/带参与返回接口/具体类型，此处按无参+接口复原）。
func NewConsoleFormatter() logrus.Formatter {
	return &consoleFormatter{plain: newTextFormatter()}
}

func NewConsoleStdout() io.Writer {
	return colorable.NewColorableStdout()
}

func NewConsoleStderr() io.Writer {
	return colorable.NewColorableStderr()
}

func newTextFormatter() *logrus.TextFormatter {
	return &logrus.TextFormatter{
		DisableColors:    true,
		ForceQuote:       true,
		FullTimestamp:    true,
		TimestampFormat:  "2006-01-02 15:04:05",
		PadLevelText:     true,
		QuoteEmptyFields: true,
	}
}

func (f *consoleFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	data, err := f.plain.Format(entry)
	if err != nil {
		return nil, err
	}
	level := []byte(fmt.Sprintf(`level="%s"`, entry.Level.String()))
	color := levelColor(entry)
	colored := fmt.Sprintf("[%sm%s[0m", color, level)
	return bytes.Replace(data, level, []byte(colored), 1), nil
}

// levelColor 返回 level 对应的 ANSI 颜色码（SGR 参数），未识别的级别返回 "0"（不着色）。
func levelColor(entry *logrus.Entry) string {
	switch entry.Level {
	case logrus.PanicLevel, logrus.FatalLevel:
		return "1;31"
	case logrus.ErrorLevel:
		return "31"
	case logrus.WarnLevel:
		return "33"
	case logrus.InfoLevel:
		return "32"
	case logrus.DebugLevel:
		return "36"
	case logrus.TraceLevel:
		return "90"
	default:
		return "0"
	}
}
