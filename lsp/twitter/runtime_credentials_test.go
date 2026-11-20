package twitter

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/spf13/viper"
)

func TestRuntimeCredentialsPrepareFailureAbortAndRequestIsolation(t *testing.T) {
	oldRuntime, oldValidate := liveTwitter.Load(), validateTwitterCandidate
	oldFailures, oldRecovering := twitterFetchFailures.Load(), twitterRecovering.Load()
	t.Cleanup(func() {
		liveTwitter.Store(oldRuntime)
		validateTwitterCandidate = oldValidate
		twitterFetchFailures.Store(oldFailures)
		twitterRecovering.Store(oldRecovering)
	})
	proxy_pool.Swap(nil)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		cookie, err := r.Cookie("auth_token")
		if err != nil {
			t.Error(err)
			return
		}
		if cookie.Value == "bad" {
			fmt.Fprint(w, `{"valid":false}`)
			return
		}
		if r.Header.Get("x-csrf-token") != cookie.Value+"-csrf" || r.UserAgent() != cookie.Value+"-ua" {
			t.Errorf("mixed credential generation: %s", cookie.Value)
		}
		fmt.Fprint(w, `{"valid":true}`)
	}))
	defer server.Close()
	validateTwitterCandidate = func(api *TwitterAPI) error {
		var response struct {
			Valid bool `json:"valid"`
		}
		if err := api.doGetJSON(context.Background(), server.URL, nil, nil, &response); err != nil {
			return err
		}
		if !response.Valid {
			return fmt.Errorf("invalid candidate")
		}
		return nil
	}
	snapshot := func(token string) *config.Snapshot {
		v := viper.New()
		v.Set("twitter", map[string]interface{}{
			"mode": ModeAPI, "ct0": token + "-csrf", "auth_token": token, "userAgent": token + "-ua",
		})
		return (&config.Config{Viper: v}).Snapshot()
	}
	previous, next := snapshot("old"), snapshot("new")
	old, err := newTwitterRuntime(previous)
	if err != nil {
		t.Fatal(err)
	}
	liveTwitter.Store(old)
	if _, _, err := PrepareRuntimeReload(previous, snapshot("bad")); err == nil || liveTwitter.Load() != old {
		t.Fatal("rejected candidate replaced the live client")
	}
	_, abort, err := PrepareRuntimeReload(previous, next)
	if err != nil {
		t.Fatal(err)
	}
	if abort != nil {
		abort()
	}
	if liveTwitter.Load() != old {
		t.Fatal("preparation or abort changed the live client")
	}
	commit, _, err := PrepareRuntimeReload(previous, next)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	fresh := liveTwitter.Load()
	if fresh == old || fresh.api == old.api {
		t.Fatal("client was reused across credentials")
	}
	for _, api := range []*TwitterAPI{old.api, fresh.api} {
		var out interface{}
		if err := api.doGetJSON(context.Background(), server.URL, nil, nil, &out); err != nil {
			t.Fatal(err)
		}
	}
	before := requests.Load()
	if commit, _, err := PrepareRuntimeReload(next, next); err != nil || commit != nil || requests.Load() != before {
		t.Fatal("unchanged settings reauthorized the client")
	}
	recordTwitterFetchResultFor(old.api, false)
	if twitterFetchFailures.Load() != 0 {
		t.Fatal("retired API changed current failure budget")
	}
}
