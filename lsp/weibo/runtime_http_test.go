package weibo

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/spf13/viper"
)

func restoreWeiboRuntimeForHTTPTest(t *testing.T) {
	t.Helper()
	oldCfg := config.GlobalConfig
	oldSub, oldOpts, oldUA := getRuntimeSUB(), CookieOption(), visitorUA.Load()
	oldHealth, oldFails, oldPause := cookieHealthy.Load(), consecutiveCookieFails.Load(), freshCookiePauseUntil.Load()
	oldGeneration := sessionGeneration.Load()
	t.Cleanup(func() {
		cookieStateMu.Lock()
		defer cookieStateMu.Unlock()
		config.GlobalConfig = oldCfg
		setRuntimeSUB(oldSub)
		visitorCookiesOpt.Store(oldOpts)
		visitorUA.Store(oldUA)
		cookieHealthy.Store(oldHealth)
		consecutiveCookieFails.Store(oldFails)
		freshCookiePauseUntil.Store(oldPause)
		sessionGeneration.Store(oldGeneration)
	})
}

func TestRuntimeLateProfileFailureDoesNotPoisonNewSession(t *testing.T) {
	restoreWeiboRuntimeForHTTPTest(t)
	cfg := &config.Config{Viper: viper.New()}
	cfg.Set("weibo.mode", "login")
	config.GlobalConfig = cfg
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, "<html>expired OLD login</html>")
	}))
	defer server.Close()
	defer unblock()
	transport := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}}
	defer transport.CloseIdleConnections()
	setRuntimeSUB("old-sub")
	visitorCookiesOpt.Store([]requests.Option{requests.CookieOption("SUB", "old-sub"), requests.WithTransport(transport)})
	cookieHealthy.Store(true)
	done := make(chan error, 1)
	go func() { _, err := apiContainerGetIndexProfileLogin(1); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old request did not reach fake server")
	}
	p := &preparedRuntime{settings: runtimeSettings{mode: "login", sub: "new-sub"}, options: []requests.Option{requests.CookieOption("SUB", "new-sub")}, ua: "new-ua"}
	p.apply()
	unblock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("fake OLD invalid response unexpectedly parsed")
		}
	case <-time.After(time.Second):
		t.Fatal("old request did not complete")
	}
	if !cookieHealthy.Load() {
		t.Fatal("late OLD profile failure marked freshly committed new session unhealthy")
	}
}

func TestRuntimeInitialRefreshAPIUsesCandidateURLBeforeCommit(t *testing.T) {
	restoreWeiboRuntimeForHTTPTest(t)
	cfg := &config.Config{Viper: viper.New()}
	cfg.Set("weibo.cookieRefreshAPI", "http://127.0.0.1:1/old-unreachable")
	config.GlobalConfig = cfg
	setRuntimeSUB("old-sub")
	visitorCookiesOpt.Store([]requests.Option{requests.CookieOption("SUB", "old-sub")})
	var called atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		fmt.Fprint(w, `{"success":true,"cookies":[{"name":"SUB","value":"candidate-api-sub"}]}`)
	}))
	defer server.Close()
	settings := runtimeSettings{mode: "login", autoRefresh: true, refreshURL: server.URL}
	fetch := func(mode, ua string) ([]*http.Cookie, error) {
		if called.Load() != 1 || mode != "login" {
			t.Fatal("candidate refresh API did not run before session initialization")
		}
		return []*http.Cookie{{Name: "XSRF-TOKEN", Value: "candidate-xsrf"}}, nil
	}
	validate := func(opts []requests.Option, ua string) error {
		if extractCookieValue(opts, "SUB") != "candidate-api-sub" {
			t.Fatal("initial refresh SUB was not validated")
		}
		return nil
	}
	p, err := prepareRuntimeWith(settings, fetch, validate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if getRuntimeSUB() != "old-sub" || extractCookieValue(CookieOption(), "SUB") != "old-sub" {
		t.Fatal("prepare published candidate before commit")
	}
	if called.Load() != 1 || p.settings.sub != "candidate-api-sub" {
		t.Fatal("candidate refresh was deferred or used old URL")
	}
	p.apply()
	if getRuntimeSUB() != "candidate-api-sub" {
		t.Fatal("initial API SUB not applied")
	}
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"success":false}`) }))
	defer failing.Close()
	settings.refreshURL = failing.URL
	if _, err := prepareRuntimeWith(settings, fetch, validate, nil); err == nil {
		t.Fatal("failed initial refresh was accepted")
	}
	if getRuntimeSUB() != "candidate-api-sub" || extractCookieValue(CookieOption(), "SUB") != "candidate-api-sub" {
		t.Fatal("failed refresh overwrote running credentials")
	}
}

func TestRuntimeEmptyLoginConcernCanStartAndStopImmediately(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	restoreWeiboRuntimeForHTTPTest(t)
	cfg := &config.Config{Viper: viper.New()}
	cfg.Set("weibo.mode", "login")
	cfg.Set("weibo.qrlogin", false)
	config.GlobalConfig = cfg
	for i := 0; i < 30; i++ {
		consumer := NewConcern(make(chan concern.Notify, 10))
		if err := consumer.Start(); err != nil {
			t.Fatal(err)
		}
		if consumer.stopRuntime == nil || !consumer.started {
			t.Fatal("no-SUB login dropped the runtime consumer")
		}
		consumer.Stop()
	}
}
