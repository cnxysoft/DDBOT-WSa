package twitch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/local_proxy_pool"
)

func twitchProxyPool(address string) proxy_pool.IProxyPool {
	return local_proxy_pool.NewLocalPool([]*local_proxy_pool.Proxy{{Proxy: address, Type: proxy_pool.PreferOversea}})
}

func TestTwitchProxyCandidateTokenValidationAndFutureRefresh(t *testing.T) {
	previousStore := tokenStore
	tokenStore = &appToken{clientId: "old", clientSecret: "old-secret", accessToken: "old-token", expiresIn: 7200, fetchedAt: time.Now()}
	defer func() { tokenStore = previousStore; proxy_pool.Swap(nil) }()
	type seenRequest struct{ proxy, id string }
	var mu sync.Mutex
	var seen []seenRequest
	proxy := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !r.URL.IsAbs() || r.URL.Host != "twitch.test" {
				t.Error("OAuth request bypassed HTTP proxy")
			}
			_ = r.ParseForm()
			id := r.Form.Get("client_id")
			if r.Form.Get("client_secret") != id+"-secret" || r.Form.Get("grant_type") != "client_credentials" {
				t.Error("invalid OAuth candidate payload")
			}
			mu.Lock()
			seen = append(seen, seenRequest{name, id})
			mu.Unlock()
			token := "token-" + id
			if id == "bad" {
				token = ""
			}
			_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: token, ExpiresIn: 7200})
		}))
	}
	oldProxy, newProxy, laterProxy := proxy("old"), proxy("candidate"), proxy("later")
	defer oldProxy.Close()
	defer newProxy.Close()
	defer laterProxy.Close()
	proxy_pool.Swap(twitchProxyPool(oldProxy.URL))
	previous, next := twitchReloadSnapshot("old", "old-secret"), twitchReloadSnapshot("new", "new-secret")
	endpoint := "http://twitch.test/oauth2/token"
	getter, _, abortProxy := proxy_pool.PrepareSwap(twitchProxyPool(newProxy.URL))
	if _, _, err := prepareRuntimeReloadWithProxy(previous, twitchReloadSnapshot("bad", "bad-secret"), getter, endpoint); err == nil {
		t.Fatal("invalid candidate token accepted")
	}
	abortProxy()
	if auth, err := getAPIAuth(); err != nil || auth.token != "old-token" {
		t.Fatal("failed candidate overwrote existing token")
	}
	getter, _, abortProxy = proxy_pool.PrepareSwap(twitchProxyPool(newProxy.URL))
	commit, abort, err := prepareRuntimeReloadWithProxy(previous, next, getter, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	if auth, err := getAPIAuth(); err != nil || auth.clientID != "old" {
		t.Fatal("candidate changed token before commit")
	}
	if _, err := requestAppTokenAtWithProxy(endpoint, "old", "old-secret", nil); err != nil {
		t.Fatal(err)
	}
	abort()
	abortProxy()
	commit()
	if auth, err := getAPIAuth(); err != nil || auth.clientID != "old" {
		t.Fatal("abort changed token")
	}
	if _, err := requestAppTokenAtWithProxy(endpoint, "new", "new-secret", getter); err == nil {
		t.Fatal("aborted token fetch fell back to global")
	}
	getter, publishProxy, _ := proxy_pool.PrepareSwap(twitchProxyPool(newProxy.URL))
	commit, _, err = prepareRuntimeReloadWithProxy(previous, next, getter, endpoint)
	if err != nil {
		t.Fatal(err)
	}
	publishProxy()
	commit()
	if auth, err := getAPIAuth(); err != nil || auth.token != "token-new" {
		t.Fatal("commit did not publish candidate token")
	}
	proxy_pool.Swap(twitchProxyPool(laterProxy.URL))
	tokenStore.mu.Lock()
	tokenStore.accessToken = ""
	tokenStore.mu.Unlock()
	auth, err := tokenStore.auth(func(id, secret string) (tokenResponse, error) {
		return requestAppTokenAtWithProxy(endpoint, id, secret, nil)
	})
	if err != nil || auth.token != "token-new" {
		t.Fatal("future token refresh failed")
	}
	mu.Lock()
	actual := append([]seenRequest(nil), seen...)
	mu.Unlock()
	want := []seenRequest{{"candidate", "bad"}, {"candidate", "new"}, {"old", "old"}, {"candidate", "new"}, {"later", "new"}}
	if len(actual) != len(want) {
		t.Fatalf("OAuth wire requests=%+v", actual)
	}
	for i := range want {
		if actual[i] != want[i] {
			t.Fatalf("OAuth wire requests=%+v", actual)
		}
	}
	_, abort, err = PrepareRuntimeReloadWithProxy(next, next, func(proxy_pool.Prefer) (proxy_pool.IProxy, error) {
		t.Fatal("unrelated save authorized Twitch")
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	abort()
	var directHits atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "direct", ExpiresIn: 7200})
	}))
	defer direct.Close()
	directGetter, _, abortDirect := proxy_pool.PrepareSwap(nil)
	if _, err := requestAppTokenAtWithProxy(direct.URL, "new", "new-secret", directGetter); err != nil {
		t.Fatal(err)
	}
	if directHits.Load() != 1 {
		t.Fatal("off candidate used old proxy")
	}
	abortDirect()
	if _, err := requestAppTokenAtWithProxy(direct.URL, "new", "new-secret", directGetter); err == nil {
		t.Fatal("aborted direct candidate sent network request")
	}
	if directHits.Load() != 1 {
		t.Fatal("aborted token fetch went direct")
	}
}
