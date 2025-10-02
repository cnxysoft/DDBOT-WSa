//go:build windows
// +build windows

package utils

import (
	"os"
	"syscall"
	"time"
)

const (
	CTRL_C_EVENT        = uint32(0)
	CTRL_BREAK_EVENT    = uint32(1)
	CTRL_CLOSE_EVENT    = uint32(2)
	CTRL_LOGOFF_EVENT   = uint32(5)
	CTRL_SHUTDOWN_EVENT = uint32(6)
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleCtrlHandler = kernel32.NewProc("SetConsoleCtrlHandler")
)

// setupWindowsExitHook 设置Windows系统退出钩子
func setupWindowsExitHook() {
	// 注册Windows控制台事件处理函数
	n, _, err := procSetConsoleCtrlHandler.Call(
		syscall.NewCallback(func(controlType uint32) uint {
			// 执行所有退出钩子
			RunExitHooks()

			// 等待一段时间确保清理完成
			time.Sleep(time.Second * 1)

			// 对于关闭事件，返回1表示已处理
			switch controlType {
			case CTRL_CLOSE_EVENT:
				os.Exit(0)
				return 1
			default:
				os.Exit(0)
				return 0
			}
		}),
		1)
	if n == 0 || err != nil && err.Error() != "The operation completed successfully." {
		// 注册失败，使用备用方案
		setupSignalExitHook()
		return
	}
}