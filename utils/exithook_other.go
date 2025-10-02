//go:build !windows
// +build !windows

package utils

// setupWindowsExitHook 在非Windows系统上为空实现
func setupWindowsExitHook() {
	// 非Windows系统不需要特殊处理
}