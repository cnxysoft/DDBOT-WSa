//go:build windows
// +build windows

package warn

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"github.com/cnxysoft/DDBOT-WSa/logging/style"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
)

func runningByDoubleClick() bool {
	lp := kernel32.NewProc("GetConsoleProcessList")
	if lp != nil {
		var ids [2]uint32
		var maxCount uint32 = 2
		ret, _, _ := lp.Call(uintptr(unsafe.Pointer(&ids)), uintptr(maxCount))
		if ret > 1 {
			return false
		}
	}
	return true
}

func boxW(hwnd uintptr, caption, title string, flags uint) int {
	captionPtr, _ := syscall.UTF16PtrFromString(caption)
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	ret, _, _ := user32.NewProc("MessageBoxW").Call(
		hwnd,
		uintptr(unsafe.Pointer(captionPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		uintptr(flags),
	)
	return int(ret)
}

// Warn 在日志尚未就位的极早期阶段输出一条阻断性提示。
// 双击 exe 启动时控制台会随进程一起关掉，这种场景改用系统弹窗，否则用户什么都看不到。
func Warn(content string) {
	if runningByDoubleClick() {
		_ = boxW(0, content, "警告", 0x00000030|0x00000000)
		return
	}
	fmt.Fprint(os.Stderr, alertText(content, style.Terminal(os.Stderr)))
}
