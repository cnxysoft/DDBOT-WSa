package cfg

import (
	"sync"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/sirupsen/logrus"
)

// 配置热重载钩子。
//
// viper 的 WatchConfig 会在文件变化时重新读盘，但「日志等级」「定时任务」这类
// 需要额外动作的模块不会自动更新；Web 面板保存后由 admin 主动调用
// ReloadConfigFile()，在重新读盘后依次执行这些钩子。

var (
	configReloadHooksMu sync.Mutex
	configReloadHooks   []func()
)

// RegisterConfigReloadHook 注册一个热重载钩子。重复注册会重复执行，
// 调用方（lsp/module.go）用 sync.Once 保证只注册一次。
func RegisterConfigReloadHook(fn func()) {
	if fn == nil {
		return
	}
	configReloadHooksMu.Lock()
	defer configReloadHooksMu.Unlock()
	configReloadHooks = append(configReloadHooks, fn)
}

// ReloadConfigFile 重新读取 application.yaml，并触发所有热重载钩子。
//
// 调用方应持有配置写锁（admin 的保存路径已经持有），避免与其它写配置操作并发。
// 注意：adapter.mode / websocket 连接这类「启动时构造」的配置无法靠本函数生效，
// 仍需重启 Bot；本函数负责的是日志等级、自定义命令前缀、定时任务等运行期配置。
func ReloadConfigFile() error {
	if err := config.GlobalConfig.ReadInConfig(); err != nil {
		return err
	}
	ReloadCustomCommandPrefix()

	configReloadHooksMu.Lock()
	hooks := append([]func(){}, configReloadHooks...)
	configReloadHooksMu.Unlock()
	for _, fn := range hooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					// 钩子失败不应把已经写好的配置回滚，也不能让面板请求直接崩掉
					logrus.Errorf("配置热重载钩子执行失败: %v", r)
				}
			}()
			fn()
		}()
	}
	return nil
}
