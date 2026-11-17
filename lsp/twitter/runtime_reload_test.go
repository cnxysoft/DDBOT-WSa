package twitter

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/spf13/viper"
)

func reloadSnapshot(mode, ua, base string) *config.Snapshot {
	v := viper.New()
	v.Set("twitter.mode", mode)
	v.Set("twitter.userAgent", ua)
	if base != "" {
		v.Set("twitter.baseUrl", []string{base})
	}
	return (&config.Config{Viper: v}).Snapshot()
}
func TestLateMirrorChallengeCannotPolluteNewJar(t *testing.T) {
	oldLive := liveTwitter.Load()
	oldCount, oldRecovering := twitterFetchFailures.Load(), twitterRecovering.Load()
	defer liveTwitter.Store(oldLive)
	defer twitterFetchFailures.Store(oldCount)
	defer twitterRecovering.Store(oldRecovering)
	proxy_pool.Swap(nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			close(entered)
			<-release
			fmt.Fprint(w, `<title>Verifying your browser</title><script>const triple=['85096F39A78F6E75C6AF3EC06D8E44676575C514','retired_challenge=','array'];</script>`)
			return
		}
		fmt.Fprint(w, mirrorTestHTML)
	}))
	defer server.Close()
	previous := reloadSnapshot(ModeMirror, "old", server.URL)
	old, e := newTwitterRuntime(previous)
	if e != nil {
		t.Fatal(e)
	}
	liveTwitter.Store(old)
	done := make(chan struct{})
	go func() { _, _, _ = (&twitterConcern{}).fetchMirror("testuser"); close(done) }()
	<-entered
	next := reloadSnapshot(ModeMirror, "new", server.URL)
	commit, _, e := PrepareRuntimeReload(previous, next)
	if e != nil {
		t.Fatal(e)
	}
	commit()
	newLive := liveTwitter.Load()
	close(release)
	target, _ := url.Parse(server.URL)
	target.Scheme = "https"
	select {
	case <-done:
	case <-time.After(6 * time.Second):
		t.Fatal("retired mirror request did not finish")
	}
	if len(newLive.jar.Cookies(target)) > 0 {
		t.Fatal("retired HTTP response wrote challenge cookie into new session jar")
	}
}
func TestSuccessfulGenerationResetsFailureBudget(t *testing.T) {
	oldLive := liveTwitter.Load()
	oldCount := twitterFetchFailures.Load()
	oldRecovering := twitterRecovering.Load()
	defer liveTwitter.Store(oldLive)
	defer twitterFetchFailures.Store(oldCount)
	defer twitterRecovering.Store(oldRecovering)
	previous := reloadSnapshot(ModeMirror, "old", "")
	r, _ := newTwitterRuntime(previous)
	liveTwitter.Store(r)
	twitterFetchFailures.Store(19)
	next := reloadSnapshot(ModeMirror, "new", "")
	commit, _, e := PrepareRuntimeReload(previous, next)
	if e != nil {
		t.Fatal(e)
	}
	commit()
	if twitterFetchFailures.Load() != 0 {
		t.Fatal("new verified session inherited retired failure count", twitterFetchFailures.Load())
	}
}

func TestAPIModeWithoutCredentialsPreparesDisabledRuntime(t *testing.T) {
	oldLive := liveTwitter.Load()
	oldCount, oldRecovering := twitterFetchFailures.Load(), twitterRecovering.Load()
	defer liveTwitter.Store(oldLive)
	defer twitterFetchFailures.Store(oldCount)
	defer twitterRecovering.Store(oldRecovering)
	previous := reloadSnapshot(ModeMirror, "old", "")
	commit, _, err := PrepareRuntimeReload(previous, reloadSnapshot(ModeAPI, "new", ""))
	if err != nil {
		t.Fatal("empty credentials should leave the account API disabled", err)
	}
	commit()
	if runtime := liveTwitter.Load(); runtime.mode != ModeAPI || runtime.api != nil {
		t.Fatal("credential removal did not install disabled API generation")
	}
}

func TestRetiredTwitterResponsesCannotChangeNewRecoveryState(t *testing.T) {
	oldLive := liveTwitter.Load()
	oldCount, oldRecovering := twitterFetchFailures.Load(), twitterRecovering.Load()
	oldSince := twitterRecoveringSince.Load()
	oldValidate := validateTwitterCandidate
	defer liveTwitter.Store(oldLive)
	defer twitterFetchFailures.Store(oldCount)
	defer twitterRecovering.Store(oldRecovering)
	defer twitterRecoveringSince.Store(oldSince)
	defer func() { validateTwitterCandidate = oldValidate }()
	validateTwitterCandidate = func(*TwitterAPI) error { return nil }
	apiSnapshot := func(credential string) *config.Snapshot {
		v := viper.New()
		_ = v.MergeConfigMap(map[string]interface{}{"twitter": map[string]interface{}{
			"mode": ModeAPI, "ct0": credential, "auth_token": credential,
		}})
		return (&config.Config{Viper: v}).Snapshot()
	}
	previous := apiSnapshot("old")
	retired, err := newTwitterRuntime(previous)
	if err != nil {
		t.Fatal(err)
	}
	liveTwitter.Store(retired)
	twitterRecovering.Store(true)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		twitterRecoveryLoopFor(retired, func() (string, string, error) {
			close(entered)
			<-release
			return "old-account", "", nil
		}, []time.Duration{0})
		close(done)
	}()
	<-entered
	commit, _, err := PrepareRuntimeReload(previous, apiSnapshot("new"))
	if err != nil {
		t.Fatal(err)
	}
	commit()
	twitterRecovering.Store(true)
	twitterRecoveringSince.Store(1234)
	twitterFetchFailures.Store(7)
	close(release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retired verification did not finish")
	}
	for _, success := range []bool{false, true, false, true} {
		recordTwitterFetchResultFor(retired.api, success)
	}
	if !twitterRecovering.Load() || twitterRecoveringSince.Load() != 1234 || twitterFetchFailures.Load() != 7 {
		t.Fatalf("retired API result or successful verifier changed new generation recovery state: recovering=%v since=%d failures=%d oldAPI=%p newAPI=%p", twitterRecovering.Load(), twitterRecoveringSince.Load(), twitterFetchFailures.Load(), retired.api, liveTwitter.Load().api)
	}
	recordTwitterFetchResultFor(liveTwitter.Load().api, true)
	if twitterFetchFailures.Load() != 0 {
		t.Fatal("current generation result was ignored")
	}
}
