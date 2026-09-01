//go:build windows

package system_proxy

import "golang.org/x/sys/windows/registry"

func detectSystemProxy() (proxy string, enabled bool) {
	return detectWindowsProxy()
}

// detectWindowsProxy 从 Windows 注册表读取代理设置
func detectWindowsProxy() (proxy string, enabled bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.READ)
	if err != nil {
		return "", false
	}
	defer k.Close()

	// 检查代理是否启用
	proxyEnable, _, err := k.GetIntegerValue("ProxyEnable")
	if err != nil || proxyEnable == 0 {
		return "", false
	}

	// 获取代理服务器地址
	proxyServer, _, err := k.GetStringValue("ProxyServer")
	if err != nil || proxyServer == "" {
		return "", false
	}

	return parseProxyAddress(proxyServer), true
}
