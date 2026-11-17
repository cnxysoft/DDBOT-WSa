package twitcasting

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/nobuf/cas"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func tcReloadSnapshot(id, secret string) *config.Snapshot {
	v := viper.New()
	v.Set("twitcasting.clientId", id)
	v.Set("twitcasting.clientSecret", secret)
	return (&config.Config{Viper: v}).Snapshot()
}

func TestTwitcastingRuntimeReloadValidatesAndAtomicallyReplacesClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, secret, _ := r.BasicAuth()
		if id == "bad" || secret != "secret" {
			http.Error(w, "invalid", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/search/lives":
			_ = json.NewEncoder(w).Encode(cas.Movies{})
		case "/users/test-user":
			_ = json.NewEncoder(w).Encode(cas.UserContainer{User: cas.User{Name: id}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	tc := &TwitCastConcern{}
	previous := tcReloadSnapshot("", "")
	_, err := tc.clientSnapshot()
	require.Error(t, err)
	commit, abort, err := tc.prepareRuntimeReload(previous, tcReloadSnapshot("new", "secret"), server.URL)
	require.NoError(t, err)
	require.Nil(t, tc.client.Load())
	commit()
	abort()
	client, err := tc.clientSnapshot()
	require.NoError(t, err)
	user, err := client.User("test-user")
	require.NoError(t, err)
	require.Equal(t, "new", user.User.Name)
	previous = tcReloadSnapshot("new", "secret")
	_, _, err = tc.prepareRuntimeReload(previous, tcReloadSnapshot("bad", "secret"), server.URL)
	require.Error(t, err)
	require.Same(t, client, tc.client.Load())
	_, abort, err = tc.prepareRuntimeReload(previous, tcReloadSnapshot("candidate", "secret"), server.URL)
	require.NoError(t, err)
	abort()
	require.Same(t, client, tc.client.Load())
	_, _, err = tc.prepareRuntimeReload(previous, tcReloadSnapshot("partial", ""), server.URL)
	require.Error(t, err)
	commit, _, err = tc.prepareRuntimeReload(previous, tcReloadSnapshot("", ""), server.URL)
	require.NoError(t, err)
	commit()
	require.Nil(t, tc.client.Load())
}

func TestTwitcastingRuntimeReloadConcurrentClientSnapshots(t *testing.T) {
	tc := &TwitCastConcern{}
	first, second := cas.New("first", "secret"), cas.New("second", "secret")
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				client, _ := tc.clientSnapshot()
				if client != nil && client != first && client != second {
					t.Error("unexpected client generation")
				}
			}
		}()
	}
	for i := 0; i < 1000; i++ {
		tc.client.Store(first)
		tc.client.Store(second)
		tc.client.Store(nil)
	}
	wg.Wait()
}
