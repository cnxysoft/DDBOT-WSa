package lsp

import (
	stdjson "encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool/local_proxy_pool"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/spf13/viper"
)

func proxyReloadSnapshot(mode, address string) *config.Snapshot {
	v := viper.New()
	_ = v.MergeConfigMap(map[string]interface{}{"proxy": map[string]interface{}{"type": mode}, "localProxyPool": map[string]interface{}{"oversea": []string{address}}})
	return (&config.Config{Viper: v}).Snapshot()
}

func TestProxyCandidateValidationAndPersistenceFailureKeepRunningRoute(t *testing.T) {
	defer proxy_pool.Swap(nil)
	var oldCalls, newCalls atomic.Int32
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { oldCalls.Add(1); _, _ = io.WriteString(w, "old") }))
	defer old.Close()
	next := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { newCalls.Add(1); _, _ = io.WriteString(w, "new") }))
	defer next.Close()
	proxy_pool.Swap(local_proxy_pool.NewLocalPool([]*local_proxy_pool.Proxy{{Proxy: old.URL, Type: proxy_pool.PreferOversea}}))
	path := filepath.Join(t.TempDir(), "application.yaml")
	initial := []byte("proxy:\n  type: localProxyPool\nlocalProxyPool:\n  oversea:\n    - " + old.URL + "\n")
	if err := os.WriteFile(path, initial, 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	var getter proxy_pool.Getter
	c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
		var commit, abort func()
		var err error
		getter, commit, abort, err = PrepareProxyReloadWithGetter(previous, next)
		if err != nil {
			return nil, nil, err
		}
		var body string
		if err = requests.Get("http://account.example.invalid", nil, &body, requests.ProxyOptionWithGetter(proxy_pool.PreferOversea, getter)); err != nil || body != "new" {
			t.Fatalf("candidate validation route=%q error=%v", body, err)
		}
		if err = requests.Get("http://account.example.invalid", nil, &body, requests.ProxyOption(proxy_pool.PreferOversea)); err != nil || body != "old" {
			t.Fatalf("live route changed during prepare: %q %v", body, err)
		}
		return commit, abort, nil
	})
	data := []byte("proxy:\n  type: localProxyPool\nlocalProxyPool:\n  oversea:\n    - " + next.URL + "\n")
	if err := c.Save(data, func() error { return errors.New("disk unavailable") }); err == nil {
		t.Fatal("failed persistence accepted")
	}
	if _, err := getter(proxy_pool.PreferOversea); !errors.Is(err, proxy_pool.ErrCandidateAborted) {
		t.Fatal("failed save left candidate usable", err)
	}
	if c.Status().Revision != 1 {
		t.Fatal("failed save published revision")
	}
	if disk, err := os.ReadFile(path); err != nil || string(disk) != string(initial) {
		t.Fatal("failed save changed file", err)
	}
	p, err := proxy_pool.Get(proxy_pool.PreferOversea)
	if err != nil || p.ProxyString() != old.URL {
		t.Fatal("failed save changed running proxy", p, err)
	}
	if err := c.Save(data, func() error { return os.WriteFile(path, data, 0600) }); err != nil {
		t.Fatal(err)
	}
	p, err = getter(proxy_pool.PreferOversea)
	if err != nil || p.ProxyString() != next.URL || c.Status().Revision != 2 {
		t.Fatal("successful candidate was not committed", p, err)
	}
	if oldCalls.Load() != 2 || newCalls.Load() != 2 {
		t.Fatal("unexpected route counts", oldCalls.Load(), newCalls.Load())
	}
}

func TestProxyPrepareNoopAndInvalidCandidate(t *testing.T) {
	previous := proxyReloadSnapshot("off", "")
	getter, commit, abort, err := PrepareProxyReloadWithGetter(previous, proxyReloadSnapshot("off", ""))
	if err != nil || getter != nil || commit != nil || abort != nil {
		t.Fatal("unchanged proxy rebuilt")
	}
	getter, commit, abort, err = PrepareProxyReloadWithGetter(previous, proxyReloadSnapshot("localProxyPool", "://invalid"))
	if err == nil || getter != nil || commit != nil || abort != nil {
		t.Fatal("invalid candidate accepted")
	}
}

func TestPYProxyCandidateUsesBareServiceAddressBeforeAndAfterCommit(t *testing.T) {
	defer proxy_pool.Swap(nil)
	proxy_pool.Swap(nil)
	var proxyCalls, getCalls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyCalls.Add(1); _, _ = io.WriteString(w, "candidate") }))
	defer proxy.Close()
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/get" {
			getCalls.Add(1)
			_ = stdjson.NewEncoder(w).Encode(map[string]string{"proxy": strings.TrimPrefix(proxy.URL, "http://")})
			return
		}
		_, _ = io.WriteString(w, "ready")
	}))
	defer service.Close()
	v := viper.New()
	_ = v.MergeConfigMap(map[string]interface{}{"proxy": map[string]interface{}{"type": "pyProxyPool"}, "pyProxyPool": map[string]interface{}{"host": service.URL}})
	getter, commit, abort, err := PrepareProxyReloadWithGetter(proxyReloadSnapshot("off", ""), (&config.Config{Viper: v}).Snapshot())
	if err != nil {
		t.Fatal("real PY candidate prepare failed", err)
	}
	defer abort()
	if _, err := proxy_pool.Get(proxy_pool.PreferOversea); !errors.Is(err, proxy_pool.ErrNil) {
		t.Fatal("PY prepare published pool early", err)
	}
	option := requests.ProxyOptionWithGetter(proxy_pool.PreferOversea, getter)
	for step := range 2 {
		var body string
		if err := requests.Get("http://account.example.invalid/check", nil, &body, option); err != nil || body != "candidate" {
			t.Fatal("bare PY address did not route request", step, body, err)
		}
		if step == 0 {
			commit()
		}
	}
	if getCalls.Load() != 2 || proxyCalls.Load() != 2 {
		t.Fatal("candidate or promoted getter bypassed PY proxy", getCalls.Load(), proxyCalls.Load())
	}
}
