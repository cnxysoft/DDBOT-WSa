package utils

import (
	"os"
	"os/signal"
	"syscall"
)

// ExitHookFunc 定义退出钩子函数类型
type ExitHookFunc func()

// exitHookManager 管理退出钩子
type exitHookManager struct {
	hooks []ExitHookFunc
}

// 全局退出钩子管理器
var exitManager = &exitHookManager{}

// AddExitHook 添加一个退出钩子函数
func AddExitHook(hook ExitHookFunc) {
	exitManager.hooks = append(exitManager.hooks, hook)
}

// RunExitHooks 执行所有退出钩子
func RunExitHooks() {
	for _, hook := range exitManager.hooks {
		if hook != nil {
			hook()
		}
	}
}

// SetupExitHook 设置退出钩子处理器
// 在Windows上会处理控制台事件，在其他系统上处理标准信号
func SetupExitHook() {
	if isWindows() {
		setupWindowsExitHook()
	} else {
		setupSignalExitHook()
	}
}

// setupSignalExitHook 设置Unix系统信号处理
func setupSignalExitHook() {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		RunExitHooks()
		os.Exit(0)
	}()
}

// isWindows 检查是否为Windows系统
func isWindows() bool {
	return os.PathSeparator == '\\' && os.PathListSeparator == ';'
}