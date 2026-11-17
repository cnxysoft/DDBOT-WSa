package lsp

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/image_pool"
	"github.com/cnxysoft/DDBOT-WSa/image_pool/local_pool"
	"github.com/cnxysoft/DDBOT-WSa/image_pool/lolicon_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/local_proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/py"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/system_proxy"
)

func resourceChanged(previous, next *config.Snapshot, sections ...string) bool {
	if previous == nil {
		return true
	}
	for _, section := range sections {
		if !reflect.DeepEqual(previous.Get(section), next.Get(section)) {
			return true
		}
	}
	return false
}

// PrepareProxyReload constructs and validates a replacement without publishing it.
func PrepareProxyReload(previous, next *config.Snapshot) (func(), func(), error) {
	_, commit, abort, err := PrepareProxyReloadWithGetter(previous, next)
	return commit, abort, err
}

// PrepareProxyReloadWithGetter isolates candidate validation from live traffic.
// The getter follows future running pools after commit, or fails after abort.
func PrepareProxyReloadWithGetter(previous, next *config.Snapshot) (proxy_pool.Getter, func(), func(), error) {
	if !resourceChanged(previous, next, "proxy", "localProxyPool", "pyProxyPool") {
		return nil, nil, nil, nil
	}
	var candidate proxy_pool.IProxyPool
	switch next.GetString("proxy.type") {
	case "", "systemProxy":
		if address, _, enabled := system_proxy.DetectSystemProxyWithSource(); enabled && address != "" {
			candidate = local_proxy_pool.NewLocalPool([]*local_proxy_pool.Proxy{{Proxy: address, Type: proxy_pool.PreferOversea}})
		}
	case "off":
	case "localProxyPool":
		var proxies []*local_proxy_pool.Proxy
		for _, group := range []struct {
			key    string
			prefer proxy_pool.Prefer
		}{{"oversea", proxy_pool.PreferOversea}, {"mainland", proxy_pool.PreferMainland}} {
			for _, address := range next.GetStringSlice("localProxyPool." + group.key) {
				if !strings.Contains(address, "://") {
					address = "http://" + address
				}
				u, err := url.Parse(address)
				if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
					return nil, nil, nil, fmt.Errorf("localProxyPool.%s 包含无效代理地址", group.key)
				}
				proxies = append(proxies, &local_proxy_pool.Proxy{Proxy: address, Type: group.prefer})
			}
		}
		candidate = local_proxy_pool.NewLocalPool(proxies)
	case "pyProxyPool":
		var err error
		candidate, err = py.NewPYProxyPool(next.GetString("pyProxyPool.host"))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("pyProxyPool 无法连接，请检查配置与服务")
		}
	default:
		return nil, nil, nil, fmt.Errorf("proxy.type 不受支持")
	}
	getter, commit, abort := proxy_pool.PrepareSwap(candidate)
	return getter, commit, abort, nil
}

// PrepareImagePoolReload leaves candidate workers idle until the old pool drains.
func PrepareImagePoolReload(previous, next *config.Snapshot, pool *image_pool.ManagedPool) (func(), func(), error) {
	if !resourceChanged(previous, next, "imagePool", "loliconPool", "localPool") {
		return nil, nil, nil
	}
	if pool == nil {
		return nil, nil, fmt.Errorf("图片池管理器未初始化")
	}
	var candidate image_pool.Pool
	var err error
	switch next.GetString("imagePool.type") {
	case "", "off":
	case "localPool":
		candidate, err = local_pool.PrepareLocalPool(next.GetString("localPool.imageDir"))
	case "loliconPool":
		candidate, err = lolicon_pool.PrepareLoliconPool(&lolicon_pool.Config{ApiKey: next.GetString("loliconPool.apikey"), CacheMin: next.GetInt("loliconPool.cacheMin"), CacheMax: next.GetInt("loliconPool.cacheMax")})
	default:
		err = fmt.Errorf("imagePool.type 不受支持")
	}
	if err != nil {
		return nil, nil, fmt.Errorf("图片池配置无效，未替换运行中的图片池")
	}
	return func() { pool.Swap(candidate) }, func() { image_pool.ClosePool(candidate) }, nil
}
