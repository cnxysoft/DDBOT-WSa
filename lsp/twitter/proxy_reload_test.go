package twitter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/local_proxy_pool"
	"github.com/spf13/viper"
)

func twitterProxyPool(address string) proxy_pool.IProxyPool {
	return local_proxy_pool.NewLocalPool([]*local_proxy_pool.Proxy{{Proxy: address, Type: proxy_pool.PreferOversea}})
}

func TestTwitterProxyCandidateKeepsCredentialsAndRoutingIsolated(t *testing.T) {
	oldRuntime, oldValidate := liveTwitter.Load(), validateTwitterCandidate
	defer func() { liveTwitter.Store(oldRuntime); validateTwitterCandidate = oldValidate; proxy_pool.Swap(nil) }()
	type seenRequest struct{ proxy, token string }
	var mu sync.Mutex
	var seen []seenRequest
	proxy := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("auth_token")
			if err != nil {
				t.Error(err)
				return
			}
			if r.Header.Get("x-csrf-token") != cookie.Value+"-csrf" || r.UserAgent() != cookie.Value+"-ua" {
				t.Error("mixed candidate credential generation")
			}
			if !r.URL.IsAbs() || r.URL.Host != "twitter.test" {
				t.Error("request bypassed HTTP proxy")
			}
			mu.Lock()
			seen = append(seen, seenRequest{name, cookie.Value})
			mu.Unlock()
			_, _ = fmt.Fprintf(w, `{"valid":%t}`, cookie.Value != "bad")
		}))
	}
	oldProxy, newProxy, laterProxy := proxy("old"), proxy("candidate"), proxy("later")
	defer oldProxy.Close()
	defer newProxy.Close()
	defer laterProxy.Close()
	proxy_pool.Swap(twitterProxyPool(oldProxy.URL))
	request := func(api *TwitterAPI) error {
		var response struct {
			Valid bool `json:"valid"`
		}
		if err := api.doGetJSON(context.Background(), "http://twitter.test/validate", nil, nil, &response); err != nil {
			return err
		}
		if !response.Valid {
			return fmt.Errorf("invalid candidate")
		}
		return nil
	}
	validateTwitterCandidate = request
	snapshot := func(token string) *config.Snapshot {
		v := viper.New()
		v.Set("twitter", map[string]interface{}{"mode": ModeAPI, "ct0": token + "-csrf", "auth_token": token, "userAgent": token + "-ua"})
		return (&config.Config{Viper: v}).Snapshot()
	}
	previous, next := snapshot("old"), snapshot("new")
	old, err := newTwitterRuntime(previous)
	if err != nil {
		t.Fatal(err)
	}
	liveTwitter.Store(old)
	getter, _, abortProxy := proxy_pool.PrepareSwap(twitterProxyPool(newProxy.URL))
	if _, _, err := PrepareRuntimeReloadWithProxy(previous, snapshot("bad"), getter); err == nil {
		t.Fatal("bad candidate accepted")
	}
	abortProxy()
	if liveTwitter.Load() != old {
		t.Fatal("failed candidate replaced live runtime")
	}
	getter, _, abortProxy = proxy_pool.PrepareSwap(twitterProxyPool(newProxy.URL))
	var captured *TwitterAPI
	validateTwitterCandidate = func(api *TwitterAPI) error { captured = api; return request(api) }
	commit, abort, err := PrepareRuntimeReloadWithProxy(previous, next, getter)
	if err != nil {
		t.Fatal(err)
	}
	if err := request(old.api); err != nil {
		t.Fatal(err)
	}
	abort()
	abortProxy()
	commit()
	if liveTwitter.Load() != old {
		t.Fatal("abort followed by commit changed runtime")
	}
	if err := request(captured); err == nil {
		t.Fatal("aborted API silently reused global proxy")
	}
	getter, publishProxy, _ := proxy_pool.PrepareSwap(twitterProxyPool(newProxy.URL))
	commit, _, err = PrepareRuntimeReloadWithProxy(previous, next, getter)
	if err != nil {
		t.Fatal(err)
	}
	if liveTwitter.Load() != old {
		t.Fatal("candidate published during prepare")
	}
	publishProxy()
	commit()
	active := liveTwitter.Load()
	if active == old {
		t.Fatal("commit kept old API")
	}
	if err := request(active.api); err != nil {
		t.Fatal(err)
	}
	proxy_pool.Swap(twitterProxyPool(laterProxy.URL))
	if err := request(active.api); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	actual := append([]seenRequest(nil), seen...)
	mu.Unlock()
	want := []seenRequest{{"candidate", "bad"}, {"candidate", "new"}, {"old", "old"}, {"candidate", "new"}, {"candidate", "new"}, {"later", "new"}}
	if len(actual) != len(want) {
		t.Fatalf("wire requests=%+v", actual)
	}
	for i := range want {
		if actual[i] != want[i] {
			t.Fatalf("wire requests=%+v", actual)
		}
	}
	if commit, _, err := PrepareRuntimeReloadWithProxy(next, next, func(proxy_pool.Prefer) (proxy_pool.IProxy, error) {
		t.Fatal("unrelated save resolved proxy")
		return nil, nil
	}); err != nil || commit != nil {
		t.Fatal("unrelated save reauthorized Twitter")
	}
	var directHits atomic.Int32
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		directHits.Add(1)
		_, _ = w.Write([]byte(`{"valid":true}`))
	}))
	defer direct.Close()
	directGetter, _, abortDirect := proxy_pool.PrepareSwap(nil)
	api := NewTwitterAPI("direct-csrf", "direct", "", "", "")
	api.proxyGetter = directGetter
	var output interface{}
	if err := api.doGetJSON(context.Background(), direct.URL, nil, nil, &output); err != nil {
		t.Fatal(err)
	}
	if directHits.Load() != 1 {
		t.Fatal("off candidate did not connect directly")
	}
	abortDirect()
	if err := api.doGetJSON(context.Background(), direct.URL, nil, nil, &output); err == nil {
		t.Fatal("aborted direct getter sent network request")
	}
	if directHits.Load() != 1 {
		t.Fatal("aborted API fell back to direct")
	}
}
