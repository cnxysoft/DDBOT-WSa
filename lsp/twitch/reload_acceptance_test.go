package twitch

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/guonaihong/gout"
)

func TestIndependentTwitchLateOAuthResponseCannotReplaceCommittedCredentials(t *testing.T) {
	previousStore := tokenStore
	tokenStore = &appToken{clientId: "old", clientSecret: "old-secret"}
	defer func() { tokenStore = previousStore }()
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		id := r.Form.Get("client_id")
		if id == "old" {
			close(entered)
			<-release
		}
		_ = json.NewEncoder(w).Encode(tokenResponse{AccessToken: "token-" + id, ExpiresIn: 7200})
	}))
	defer server.Close()
	fetch := func(id, secret string) (tokenResponse, error) {
		var result tokenResponse
		err := requests.PostWWWForm(server.URL, gout.H{"client_id": id, "client_secret": secret}, &result)
		return result, err
	}
	done := make(chan apiAuth, 1)
	go func() {
		auth, err := tokenStore.auth(fetch)
		if err != nil {
			done <- apiAuth{}
			return
		}
		done <- auth
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old OAuth request missing")
	}
	commit, _, err := prepareRuntimeReload(twitchReloadSnapshot("old", "old-secret"), twitchReloadSnapshot("new", "new-secret"), fetch)
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	commit()
	close(release)
	var actual apiAuth
	select {
	case actual = <-done:
	case <-time.After(time.Second):
		t.Fatal("stale fetch did not finish")
	}
	if actual.clientID != "new" || actual.token != "token-new" {
		t.Fatalf("stale OAuth overwrote new auth: %+v", actual)
	}
	headerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-Id") != "new" || r.Header.Get("Authorization") != "Bearer token-new" {
			t.Error("mixed Twitch credential headers")
		}
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer headerServer.Close()
	var response StreamsResponse
	if err := requests.Get(headerServer.URL, nil, &response, apiOptions(actual)...); err != nil {
		t.Fatal(err)
	}
	invalidateToken(apiAuth{generation: actual.generation - 1})
	current, err := tokenStore.auth(fetch)
	if err != nil || current.token != "token-new" {
		t.Fatal("stale 401 invalidated committed OAuth token")
	}
}
