package qqofficial

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestQQOfficialLiveReloadRetainsIdentityAndLedgers(t *testing.T) {
	var server *httptest.Server
	var mu sync.Mutex
	var secrets []string
	upgrader := websocket.Upgrader{}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			secrets = append(secrets, body["clientSecret"])
			mu.Unlock()
			if body["clientSecret"] == "bad" {
				http.Error(w, "bad secret", http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "token-" + body["clientSecret"], "expires_in": 7200})
		case PathGateway:
			_ = json.NewEncoder(w).Encode(map[string]string{"url": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"})
		case "/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			wsTestHello(conn)
			if _, _, err := wsTestRead(conn); err != nil {
				return
			}
			wsTestReady(conn, "reload-session")
			for {
				if _, _, err := wsTestRead(conn); err != nil {
					return
				}
			}
		case "/users/@me":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "10001", "username": "bot"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	state := filepath.Join(t.TempDir(), "state.json")
	var instances []*QQOfficialAdapter
	builder := func(secret string) adapter.AdapterBuilder {
		return func() (adapter.Adapter, error) {
			qc := &Config{AppID: "app1", AppSecret: secret, APIBaseURL: server.URL, StartTimeout: 200 * time.Millisecond, HTTPTimeout: time.Second}
			a := NewQQOfficialAdapterWithConfig(qc, state).(*QQOfficialAdapter)
			instances = append(instances, a)
			return a, nil
		}
	}
	initial, err := builder("old")()
	require.NoError(t, err)
	old := initial.(*QQOfficialAdapter)
	r := adapter.NewReloadableAdapter(old, builder("old"))
	require.NoError(t, r.Start())
	defer r.Stop()
	id := old.ids.ToInt64("app1", "user-openid")
	msgID := old.msgs.register(&msgRecord{RawID: "old-message", Content: "retained"})
	ledger := old.client.seqTrackerFor()
	commit, _, err := r.PrepareReload("qqofficial", "qqofficial/app1", builder("new"))
	require.NoError(t, err)
	next := instances[len(instances)-1]
	require.Equal(t, id, next.ids.ToInt64("app1", "user-openid"))
	require.Same(t, ledger, next.client.seqTrackerFor())
	record, ok := next.msgs.get(msgID)
	require.True(t, ok)
	require.Equal(t, "retained", record.Content)
	commit()
	require.True(t, r.IsConnected())
	require.Equal(t, "qqofficial/app1", r.Identity())
	_, _, err = r.PrepareReload("qqofficial", "qqofficial/app1", builder("bad"))
	require.Error(t, err)
	require.True(t, r.IsConnected())
	require.NoError(t, r.LastReloadError())
	restored := instances[len(instances)-1]
	require.Equal(t, "new", restored.cfg.AppSecret)
	require.Same(t, ledger, restored.client.seqTrackerFor())
	mu.Lock()
	got := append([]string(nil), secrets...)
	mu.Unlock()
	require.Contains(t, got, "new")
	require.Contains(t, got, "bad")
	_, _, err = r.PrepareReload("qqofficial", "qqofficial/other", builder("other"))
	require.Error(t, err)
	require.True(t, r.IsConnected())
}
