package adapter

import (
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
)

type AdapterType string

const (
	AdapterTypeOneBotV11 AdapterType = "onebot-v11"
	AdapterTypeSatori    AdapterType = "satori"
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
		Mode:    config.GlobalConfig.GetString("adapter.mode"),
		WSMode:  config.GlobalConfig.GetString("websocket.mode"),
		WSAddr:  getWSAddr(),
		Token:   config.GlobalConfig.GetString("websocket.token"),
		Timeout: 10 * time.Second,
	}

	if cfg.Mode == "" {
		cfg.Mode = string(AdapterTypeOneBotV11)
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
			addr = "0.0.0.0:15630"
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

func GetAdapterType() AdapterType {
	adapterType := config.GlobalConfig.GetString("adapter.mode")
	if adapterType == "" {
		return AdapterTypeOneBotV11
	}
	return AdapterType(adapterType)
}
