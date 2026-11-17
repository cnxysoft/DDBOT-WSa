package telegram

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/local_proxy_pool"
)

func telegramProxyPool(address string) proxy_pool.IProxyPool {
	return local_proxy_pool.NewLocalPool([]*local_proxy_pool.Proxy{{Proxy: address, Type: proxy_pool.PreferOversea}})
}

func TestTelegramProxyCandidateIsolationAbortPromotionAndDirect(t *testing.T) {
	Close()
	managerMu.Lock()
	initialized, receiver = false, nil
	managerMu.Unlock()
	t.Cleanup(func() { Close(); proxy_pool.Swap(nil) })
	var oldHits, candidateHits, laterHits atomic.Int32
	proxy := func(hits *atomic.Int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			if !r.URL.IsAbs() || r.URL.Host != "telegram.test" {
				t.Error("request did not use HTTP proxy")
			}
			if strings.Contains(r.URL.Path, "bad") {
				_, _ = fmt.Fprint(w, `{"ok":false,"error_code":401,"description":"invalid"}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"test","username":"test_bot"}}`)
		}))
	}
	oldProxy, candidateProxy, laterProxy := proxy(&oldHits), proxy(&candidateHits), proxy(&laterHits)
	defer oldProxy.Close()
	defer candidateProxy.Close()
	defer laterProxy.Close()
	proxy_pool.Swap(telegramProxyPool(oldProxy.URL))
	off := testSnapshot(map[string]interface{}{"telegram.enable": false})
	snapshot := func(token string) *config.Snapshot {
		return testSnapshot(map[string]interface{}{"telegram.enable": true, "telegram.token": token, "telegram.endpoint": "http://telegram.test/bot%s/%s"})
	}
	old, next := snapshot("old"), snapshot("new")
	commit, _, err := PrepareRuntimeReload(off, old)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	managerMu.RLock()
	oldGeneration := current
	managerMu.RUnlock()
	getter, _, abortProxy := proxy_pool.PrepareSwap(telegramProxyPool(candidateProxy.URL))
	_, _, err = PrepareRuntimeReloadWithProxy(old, snapshot("bad"), getter)
	if err == nil {
		t.Fatal("failed candidate accepted")
	}
	abortProxy()
	if oldHits.Load() != 1 || candidateHits.Load() != 1 {
		t.Fatal("candidate authorization used the live proxy")
	}
	getter, _, abortProxy = proxy_pool.PrepareSwap(telegramProxyPool(candidateProxy.URL))
	commit, abort, err := PrepareRuntimeReloadWithProxy(old, next, getter)
	if err != nil {
		t.Fatal(err)
	}
	managerMu.RLock()
	same := current == oldGeneration
	managerMu.RUnlock()
	if !same {
		t.Fatal("prepare published Telegram generation")
	}
	if _, err := oldGeneration.bot.GetMe(); err != nil {
		t.Fatal(err)
	}
	if oldHits.Load() != 2 {
		t.Fatal("candidate preparation changed background proxy routing")
	}
	abort()
	abortProxy()
	commit()
	managerMu.RLock()
	same = current == oldGeneration
	managerMu.RUnlock()
	if !same {
		t.Fatal("aborted candidate replaced Telegram client")
	}
	getter, publishProxy, _ := proxy_pool.PrepareSwap(telegramProxyPool(candidateProxy.URL))
	commit, _, err = PrepareRuntimeReloadWithProxy(old, next, getter)
	if err != nil {
		t.Fatal(err)
	}
	publishProxy()
	commit()
	managerMu.RLock()
	active := current
	managerMu.RUnlock()
	before := candidateHits.Load()
	if _, err := active.bot.GetMe(); err != nil {
		t.Fatal(err)
	}
	if candidateHits.Load() != before+1 {
		t.Fatal("committed client did not use new global pool")
	}
	proxy_pool.Swap(telegramProxyPool(laterProxy.URL))
	if _, err := active.bot.GetMe(); err != nil {
		t.Fatal(err)
	}
	if laterHits.Load() != 1 {
		t.Fatal("committed Telegram client pinned its original candidate pool")
	}
	if commit, _, err := PrepareRuntimeReloadWithProxy(next, next, func(proxy_pool.Prefer) (proxy_pool.IProxy, error) {
		t.Fatal("unrelated save resolved proxy")
		return nil, nil
	}); err != nil || commit != nil {
		t.Fatal("unrelated save rebuilt Telegram client")
	}
	var directHits atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { directHits.Add(1); _, _ = w.Write([]byte("direct")) }))
	defer direct.Close()
	directGetter, _, abortDirect := proxy_pool.PrepareSwap(nil)
	client := buildTelegramHTTPClientWithProxy(directGetter)
	defer client.CloseIdleConnections()
	response, err := client.Get(direct.URL)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if directHits.Load() != 1 || laterHits.Load() != 1 {
		t.Fatal("off candidate fell back to old proxy")
	}
	abortDirect()
	if response, err := client.Get(direct.URL); err == nil {
		response.Body.Close()
		t.Fatal("aborted getter silently fell back to direct")
	}
	if directHits.Load() != 1 {
		t.Fatal("aborted candidate sent network traffic")
	}
	invalid := buildTelegramHTTPClientWithProxy(func(proxy_pool.Prefer) (proxy_pool.IProxy, error) { return nil, nil })
	defer invalid.CloseIdleConnections()
	if response, err := invalid.Get(direct.URL); err == nil {
		response.Body.Close()
		t.Fatal("invalid selector silently connected directly")
	}
	if directHits.Load() != 1 {
		t.Fatal("invalid selector sent network traffic")
	}
}
