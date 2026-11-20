//go:build windows

package system_proxy

import "golang.org/x/sys/windows/registry"

// detectSystemProxyWithSource 检测系统代理并返回来源说明（Windows：注册表 Internet Settings）
func detectSystemProxyWithSource() (proxy, source string, enabled bool) {
	p, ok := detectWindowsProxy()
	if !ok || p == "" {
		return "", "", false
	}
	return p, "Windows 注册表(Internet Settings)", true
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
