//go:build !windows

package system_proxy

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

func detectSystemProxy() (proxy string, enabled bool) {
	// 1) 环境变量优先（http_proxy/https_proxy/all_proxy，大小写均检查）
	if p, ok := detectEnvProxy(); ok && p != "" {
		return p, true
	}

	// 2) Linux GNOME 桌面代理设置（gsettings，Ubuntu 桌面"设置-网络-网络代理"写入的位置）
	if runtime.GOOS == "linux" {
		if p := detectGnomeProxy(); p != "" {
			return p, true
		}
	}

	return "", false
}

// detectEnvProxy 从环境变量读取代理设置（Linux/macOS 通用）
func detectEnvProxy() (proxy string, enabled bool) {
	// 按优先级检查环境变量
	envVars := []string{"https_proxy", "HTTPS_PROXY", "http_proxy", "HTTP_PROXY", "all_proxy", "ALL_PROXY"}

	for _, envVar := range envVars {
		if value := os.Getenv(envVar); value != "" {
			return normalizeProxyURL(value), true
		}
	}

	return "", false
}

// gsettingsGet 读取 gsettings 键值，返回去引号后的字符串；gsettings 不存在或键不存在时返回空串。
// gsettings get 的输出形如 "'127.0.0.1'"（字符串值带单引号）或 "7890"（整数）
func gsettingsGet(schema, key string) string {
	out, err := exec.Command("gsettings", "get", schema, key).Output()
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(string(out)), "'\"")
}

// detectGnomeProxy 检测 GNOME 桌面系统代理（仅处理 manual 手动模式；
// auto 模式为 PAC 脚本，无法在此解析）。
// 检查顺序：https -> http -> socks
func detectGnomeProxy() string {
	if mode := gsettingsGet("org.gnome.system.proxy", "mode"); mode != "manual" {
		return ""
	}

	// (schema, scheme) 组合按优先级排列
	try := []struct {
		schema string
		scheme string
	}{
		{"org.gnome.system.proxy.https", "https://"},
		{"org.gnome.system.proxy.http", "http://"},
		{"org.gnome.system.proxy.socks", "socks5://"},
	}

	for _, t := range try {
		host := gsettingsGet(t.schema, "host")
		if host == "" {
			continue
		}
		portStr := gsettingsGet(t.schema, "port")
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			continue
		}
		return t.scheme + host + ":" + strconv.Itoa(port)
	}
	return ""
}
