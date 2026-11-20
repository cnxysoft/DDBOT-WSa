package adapter

import (
	"strings"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/sirupsen/logrus"
)

// adapterLogger 是 adapter 包级的配置日志（目前只有 adapter.mode 的回落告警会用到）。
var adapterLogger = logrus.WithField("module", "adapter config")

type AdapterType string

const (
	AdapterTypeOneBotV11  AdapterType = "onebot-v11"
	AdapterTypeSatori     AdapterType = "satori"
	AdapterTypeQQOfficial AdapterType = "qqofficial"
)

// NewAdapter creates an adapter instance based on the adapter type.
// Factory function is set by bot package at init time to avoid circular imports.
var NewAdapterFactory func(adapterType AdapterType, cfg *AdapterConfig) Adapter

func NewAdapter(adapterType AdapterType, cfg *AdapterConfig) Adapter {
	if NewAdapterFactory != nil {
		return NewAdapterFactory(adapterType, cfg)
	}
	return nil
}

func GetAdapterConfig() *AdapterConfig {
	cfg := &AdapterConfig{
		// Mode 与 GetAdapterType 走同一个收敛函数：不认识的写法回落到 onebot-v11，
		// 免得「类型是 OneBot、Mode 还留着笔误原值」这种自相矛盾的组合流到下游。
		Mode:    string(resolveAdapterType(config.GlobalConfig.GetString("adapter.mode"))),
		WSMode:  config.GlobalConfig.GetString("websocket.mode"),
		WSAddr:  getWSAddr(),
		Token:   config.GlobalConfig.GetString("websocket.token"),
		Timeout: 10 * time.Second,
	}

	return cfg
}

func getWSAddr() string {
	wsMode := config.GlobalConfig.GetString("websocket.mode")
	if wsMode == "" {
		wsMode = WSModeServer
	}

	if wsMode == WSModeServer {
		addr := config.GlobalConfig.GetString("websocket.ws-server")
		if addr == "" {
			// 默认仅监听本机回环：未配置 token 时不会把无鉴权控制端口暴露到局域网/公网
			addr = defaultWSServerAddr
		}
		return addr
	}

	addr := config.GlobalConfig.GetString("websocket.ws-reverse")
	if addr == "" {
		addr = "ws://localhost:3001"
	}
	return addr
}

func GetWSMode() string {
	mode := config.GlobalConfig.GetString("websocket.mode")
	if mode == "" {
		mode = WSModeServer
	}
	return mode
}

// GetAdapterType 返回本次运行该用的 adapter 类型；不认识的 adapter.mode 回落到 OneBot。
func GetAdapterType() AdapterType {
	return resolveAdapterType(config.GlobalConfig.GetString("adapter.mode"))
}

// resolveAdapterType 把配置里的 adapter.mode 收敛成一个已知的 adapter 类型。
//
// 暂时性决策（2026-11 定）：不认识的值不再让程序起不来，而是醒目 Warn + 走既有默认
// 通道（onebot-v11）。依据是历史行为：旧实现在工厂 default 分支返回 nil，bot.Init 里
// 一句 Fatalf 直接退出，而 adapter.mode 是用户手写的配置（onebot-v1 / onebot_v11 /
// onebto-v11 这类笔误在工单里很常见）—— 结果是整个程序起不来，用户只能在「程序怎么
// 没了」和「日志里到底写了啥」之间猜。回落之后至少能起、能看见告警、能收发消息。
//
// 何时该重新评估、退回硬失败：
//   - 各通道行为差异变大到「跑错通道会破坏数据」（比如 satori 与 onebot 的语义不再兼容）；
//   - adapter.mode 改由 Web 管理端写入并校验（笔误在保存那一步就拦掉了），
//     那时静默回落反而会掩盖配置漂移。
func resolveAdapterType(raw string) AdapterType {
	mode := strings.TrimSpace(raw)
	switch AdapterType(mode) {
	case "":
		// 历史默认值：没配就是 onebot-v11，这是正常配置，不该告警
		return AdapterTypeOneBotV11
	case AdapterTypeOneBotV11, AdapterTypeSatori, AdapterTypeQQOfficial:
		return AdapterType(mode)
	default:
		adapterLogger.Warnf("adapter.mode=%q 不认识（当前支持 %s / %s / %s）：已回落到 %s 通道继续启动。"+
			"如果你本来想用别的通道，请改正这一行配置再重启，否则跑起来的是 %s。",
			raw, AdapterTypeOneBotV11, AdapterTypeSatori, AdapterTypeQQOfficial,
			AdapterTypeOneBotV11, AdapterTypeOneBotV11)
		return AdapterTypeOneBotV11
	}
}
