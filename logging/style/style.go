// Package style 提供控制台着色所需的 ANSI 码和终端探测。
// 日志格式、启动横幅、warn 的阻断提示共用同一套判断，避免各自实现漂移。
package style

import (
	"os"

	"github.com/mattn/go-isatty"
)

// SGR 参数。多个参数（例如加粗+红）用分号连接后一起传。
const (
	Reset   = "0"
	Bold    = "1"
	Dim     = "90"
	Red     = "31"
	Green   = "32"
	Yellow  = "33"
	Blue    = "34"
	Magenta = "35"
	Cyan    = "36"
	White   = "37"
)

// Terminal 判断 f 是不是可以安全上色的终端。
// 设置了非空的 NO_COLOR（见 https://no-color.org）或 TERM=dumb 时一律返回 false。
func Terminal(f *os.File) bool {
	if f == nil {
		return false
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fd := f.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// Paint 用 SGR 参数包裹文本；enabled 为 false 或 code 为空时原样返回。
func Paint(enabled bool, code, s string) string {
	if !enabled || code == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[" + Reset + "m"
}
