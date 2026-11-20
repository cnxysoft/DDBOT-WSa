//go:build !windows
// +build !windows

package warn

import (
	"fmt"
	"os"

	"github.com/cnxysoft/DDBOT-WSa/logging/style"
)

// Warn 在日志尚未就位的极早期阶段输出一条阻断性提示（例如数据库被占用）。
// 走到这里的错误通常紧接着就会退出，所以固定写 stderr，终端里标红。
func Warn(content string) {
	fmt.Fprint(os.Stderr, alertText(content, style.Terminal(os.Stderr)))
}
