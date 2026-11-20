package satori

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestSatoriLiveReloadRetainsMappingsAndRefusesOtherLogin(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var payload struct {
			Body identifyBody `json:"body"`
		}
		if conn.ReadJSON(&payload) != nil {
			return
		}
		login := "bot1"
		if payload.Body.Token == "other" {
			login = "bot2"
		}
		_ = conn.WriteJSON(map[string]interface{}{"op": opReady, "body": readyBody{Logins: []loginResource{{Platform: "qq", User: &userResource{ID: login}, Status: 1}}}})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	var instances []*SatoriAdapter
	builder := func(token string) adapter.AdapterBuilder {
		return func() (adapter.Adapter, error) {
			a := NewSatoriAdapter(&adapter.AdapterConfig{WSAddr: server.URL, Token: token, Timeout: time.Second})
			instances = append(instances, a)
			return a, nil
		}
	}
	initial, err := builder("old")()
	require.NoError(t, err)
	old := initial.(*SatoriAdapter)
	r := adapter.NewReloadableAdapter(old, builder("old"))
	require.NoError(t, r.Start())
	defer r.Stop()
	group := old.rememberID("group-alpha")
	old.mu.Lock()
	old.groupChannelMap[group] = "channel-alpha"
	old.mu.Unlock()
	message := old.rememberSentMessage("message-alpha", "channel-alpha")
	commit, _, err := r.PrepareReload("satori", "", builder("new"))
	require.NoError(t, err)
	next := instances[len(instances)-1]
	commit()
	require.Equal(t, "group-alpha", next.rawID(group))
	require.Equal(t, "channel-alpha", next.resolveGroupChannelID(group))
	next.mu.RLock()
	rawMessage := next.msgIDMap[message]
	next.mu.RUnlock()
	require.Equal(t, "message-alpha", rawMessage)
	_, _, err = r.PrepareReload("satori", "", builder("other"))
	require.ErrorContains(t, err, "identity")
	require.True(t, r.IsConnected())
	require.NoError(t, r.LastReloadError())
	restored := instances[len(instances)-1]
	require.Equal(t, "bot1", restored.loginUserID)
	require.Equal(t, "group-alpha", restored.rawID(group))
}
