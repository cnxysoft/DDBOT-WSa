package twitcasting

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nobuf/cas"
)

func TestIndependentTwitcastingActualCredentialTrafficAndFailedCandidate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, ok := r.BasicAuth()
		if !ok || id != "new" || secret != "new-secret" {
			http.Error(w, "invalid", 401)
			return
		}
		if r.URL.Path == "/search/lives" {
			_ = json.NewEncoder(w).Encode(cas.Movies{})
			return
		}
		if r.URL.Path == "/users/test-user" {
			_ = json.NewEncoder(w).Encode(cas.UserContainer{User: cas.User{Name: id}})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	tc := &TwitCastConcern{}
	old, next := tcReloadSnapshot("", ""), tcReloadSnapshot("new", "new-secret")
	commit, _, err := tc.prepareRuntimeReload(old, next, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if tc.client.Load() != nil {
		t.Fatal("candidate published before commit")
	}
	commit()
	live, _ := tc.clientSnapshot()
	user, err := live.User("test-user")
	if err != nil || user.User.Name != "new" {
		t.Fatalf("consumer uses wrong credential: %v", err)
	}
	if _, _, err := tc.prepareRuntimeReload(next, tcReloadSnapshot("bad", "secret"), server.URL); err == nil {
		t.Fatal("bad candidate accepted")
	}
	if tc.client.Load() != live {
		t.Fatal("failed candidate lost existing client")
	}
	clear, abort, err := tc.prepareRuntimeReload(next, old, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	abort()
	clear()
	if tc.client.Load() != live {
		t.Fatal("aborted credential clear changed live client")
	}
	clear, _, err = tc.prepareRuntimeReload(next, old, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	clear()
	if _, err := tc.clientSnapshot(); err == nil {
		t.Fatal("clear left old client usable")
	}
}
