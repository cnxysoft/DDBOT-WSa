package system_proxy

import (
	"strings"
)

// DetectSystemProxy 检测系统代理设置。
// 平台相关实现拆分到 detect_windows.go（Windows：注册表）与 detect_other.go（Linux/macOS：环境变量），
// 避免在非 Windows 平台导入 golang.org/x/sys/windows/registry 导致构建失败。
func DetectSystemProxy() (proxy string, enabled bool) {
	return detectSystemProxy()
}

// parseProxyAddress 解析代理地址（支持多种格式）
// Windows 手动代理配置支持分协议条目，如 http=127.0.0.1:8080; https=127.0.0.1:8080; socks=127.0.0.1:1080
// 各协议按实际 scheme 生成代理 URL，socks 映射为 socks5://（requests 层仅支持 socks5 前缀）
func parseProxyAddress(proxyServer string) string {
	// 优先查找 http 或 https 代理
	if httpProxy := parseProtocolProxy(proxyServer, "http", "http://"); httpProxy != "" {
		return httpProxy
	}
	if httpsProxy := parseProtocolProxy(proxyServer, "https", "https://"); httpsProxy != "" {
		return httpsProxy
	}
	if socksProxy := parseProtocolProxy(proxyServer, "socks", "socks5://"); socksProxy != "" {
		return socksProxy
	}

	// 如果是单一代理格式（没有协议前缀）
	if !containsProtocolPrefix(proxyServer) {
		return "http://" + proxyServer
	}

	return ""
}

// parseProtocolProxy 解析协议特定的代理设置
func parseProtocolProxy(proxyServer, protocol, scheme string) string {
	prefix := protocol + "="
	parts := strings.Split(proxyServer, ";")

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, prefix) {
			addr := strings.TrimPrefix(part, prefix)
			addr = strings.TrimSpace(addr)
			if addr != "" {
				return scheme + addr
			}
		}
	}
	return ""
}

// containsProtocolPrefix 检查代理字符串是否包含协议前缀
func containsProtocolPrefix(proxyServer string) bool {
	protocols := []string{"http=", "https=", "socks=", "ftp="}
	for _, p := range protocols {
		if strings.Contains(proxyServer, p) {
			return true
		}
	}
	return false
}

// normalizeProxyURL 标准化代理 URL
func normalizeProxyURL(proxy string) string {
	// 如果已经是协议前缀，直接返回
	if strings.HasPrefix(proxy, "http://") ||
		strings.HasPrefix(proxy, "https://") ||
		strings.HasPrefix(proxy, "socks5://") {
		return proxy
	}

	// socks:// 按 socks5 处理（requests 层仅支持 socks5:// 前缀）
	if strings.HasPrefix(proxy, "socks://") {
		return "socks5://" + strings.TrimPrefix(proxy, "socks://")
	}

	// 否则添加 http:// 前缀
	return "http://" + proxy
}
