package weibo

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/spf13/viper"
)

func TestRuntimeReloadPreparationDoesNotPublishFailedCredentials(t *testing.T) {
	oldSub, oldOpts, oldUA := getRuntimeSUB(), CookieOption(), GetVisitorUA()
	t.Cleanup(func() { setRuntimeSUB(oldSub); visitorCookiesOpt.Store(oldOpts); visitorUA.Store(oldUA) })
	setRuntimeSUB("old-sub")
	visitorCookiesOpt.Store([]requests.Option{requests.CookieOption("SUB", "old-sub")})
	settings := runtimeSettings{mode: "login", sub: "candidate-sub"}
	fetch := func(mode, ua string) ([]*http.Cookie, error) {
		if mode != "login" || ua == "" || getRuntimeSUB() != "old-sub" {
			t.Fatal("candidate fetch leaked into running state")
		}
		return []*http.Cookie{{Name: "SUB", Value: "visitor-sub"}, {Name: "XSRF-TOKEN", Value: "csrf"}}, nil
	}
	validate := func(opts []requests.Option, ua string) error {
		if extractCookieValue(opts, "SUB") != "candidate-sub" || extractCookieValue(opts, "XSRF-TOKEN") != "csrf" {
			t.Fatal("candidate request did not carry the requested SUB and session cookies")
		}
		return errors.New("rejected candidate")
	}
	if _, err := prepareRuntimeWith(settings, fetch, validate, nil); err == nil {
		t.Fatal("failed validation was accepted")
	}
	if getRuntimeSUB() != "old-sub" || extractCookieValue(CookieOption(), "SUB") != "old-sub" {
		t.Fatal("failed candidate overwrote old live credentials")
	}
	p, err := prepareRuntimeWith(settings, fetch, func([]requests.Option, string) error { return nil }, nil)
	if err != nil || getRuntimeSUB() != "old-sub" {
		t.Fatalf("prepare unexpectedly published: %v", err)
	}
	p.apply()
	if getRuntimeSUB() != "candidate-sub" || extractCookieValue(CookieOption(), "SUB") != "candidate-sub" {
		t.Fatal("commit did not publish prepared session")
	}
}

func TestRuntimeReloadBootstrapsMissingSUBFromCandidateAPI(t *testing.T) {
	oldSub, oldOpts, oldUA := getRuntimeSUB(), CookieOption(), GetVisitorUA()
	t.Cleanup(func() { setRuntimeSUB(oldSub); visitorCookiesOpt.Store(oldOpts); visitorUA.Store(oldUA) })
	setRuntimeSUB("running-sub")
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte(`{"success":true,"cookies":[{"name":"SUB","value":"bootstrap-sub"}]}`))
	}))
	defer server.Close()
	p, err := prepareRuntimeWith(runtimeSettings{mode: "login", autoRefresh: true, refreshURL: server.URL},
		func(string, string) ([]*http.Cookie, error) { return nil, nil },
		func(opts []requests.Option, _ string) error {
			if extractCookieValue(opts, "SUB") != "bootstrap-sub" {
				t.Fatal("bootstrap candidate was not validated with API SUB")
			}
			return nil
		}, nil)
	if err != nil || !called || p.settings.sub != "bootstrap-sub" {
		t.Fatalf("missing SUB was not bootstrapped immediately: %v", err)
	}
	if getRuntimeSUB() != "running-sub" {
		t.Fatal("bootstrap preparation published before commit")
	}
}

func TestRuntimeReloadWaitingLoginAndAPIMode(t *testing.T) {
	noFetch := func(string, string) ([]*http.Cookie, error) {
		t.Fatal("unexpected cookie network request")
		return nil, nil
	}
	if p, err := prepareRuntimeWith(runtimeSettings{mode: "login"}, noFetch, nil, nil); err != nil || len(p.options) != 0 {
		t.Fatalf("empty login must remain available for later QR login: %v", err)
	}
	if _, err := prepareRuntimeWith(runtimeSettings{mode: "api", apiURL: "bad-url"}, noFetch, nil, nil); err == nil {
		t.Fatal("invalid API URL accepted")
	}
	called := false
	p, err := prepareRuntimeWith(runtimeSettings{mode: "api", apiURL: "https://api.example.test"}, noFetch, nil,
		func(base string) error { called = true; return nil })
	if err != nil || !called || len(p.options) != 0 {
		t.Fatalf("API mode must validate endpoint without Cookie: %v", err)
	}
}

func TestRuntimeReloadNoopAbortAndConcurrentRefreshLifecycle(t *testing.T) {
	previous := &config.Config{Viper: viper.New()}
	previous.Set("weibo.mode", "login")
	next := &config.Config{Viper: viper.New()}
	next.Set("weibo.mode", "login")
	commit, abort, err := PrepareRuntimeReload(previous.Snapshot(), next.Snapshot())
	if err != nil || commit != nil || abort != nil {
		t.Fatalf("unrelated save rebuilt runtime session: %v", err)
	}
	previous.Set("weibo.mode", "guest")
	commit, abort, err = PrepareRuntimeReload(previous.Snapshot(), next.Snapshot())
	if err != nil || commit == nil || abort == nil {
		t.Fatalf("empty login transition cannot prepare: %v", err)
	}
	abort()
	if !refreshMu.TryLock() {
		t.Fatal("abort leaked refresh lock")
	}
	refreshMu.Unlock()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); reconcileSubAutoRefresh(true); StopSubAutoRefresh() }()
	}
	wg.Wait()
	StopSubAutoRefresh()
}
