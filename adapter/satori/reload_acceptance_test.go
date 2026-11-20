package satori

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/gorilla/websocket"
)

func TestIndependentSatoriCandidateMustConfirmActualLogin(t *testing.T) {
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		var request struct {
			Body identifyBody `json:"body"`
		}
		if conn.ReadJSON(&request) != nil {
			return
		}
		logins := []loginResource{{Platform: "qq", User: &userResource{ID: "actual-bot"}, Status: 1}}
		if request.Body.Token == "empty" {
			logins = nil
		}
		if request.Body.Token == "other" {
			logins[0].User.ID = "different-bot"
		}
		_ = conn.WriteJSON(map[string]interface{}{"op": opReady, "body": readyBody{Logins: logins}})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	var built []*SatoriAdapter
	build := func(token string) adapter.AdapterBuilder {
		return func() (adapter.Adapter, error) {
			a := NewSatoriAdapter(&adapter.AdapterConfig{WSAddr: server.URL, Token: token, Timeout: time.Second})
			built = append(built, a)
			return a, nil
		}
	}
	initial, _ := build("old")()
	old := initial.(*SatoriAdapter)
	r := adapter.NewReloadableAdapter(initial, build("old"))
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	group := old.rememberID("group-actual")
	old.mu.Lock()
	old.groupChannelMap[group] = "channel-actual"
	old.mu.Unlock()
	for _, token := range []string{"empty", "other"} {
		if _, _, err := r.PrepareReload("satori", "", build(token)); err == nil {
			t.Fatalf("candidate %s inherited old login without validating current identity", token)
		}
		if err := r.LastReloadError(); err != nil {
			t.Fatalf("old connection did not restore: %v", err)
		}
		if !r.IsConnected() {
			t.Fatal("old connection no longer connected")
		}
	}
	commit, _, err := r.PrepareReload("satori", "", build("valid"))
	if err != nil {
		t.Fatal(err)
	}
	commit()
	next := built[len(built)-1]
	if next.rawID(group) != "group-actual" || next.resolveGroupChannelID(group) != "channel-actual" {
		t.Fatal("successful reload lost permissions/ID/channel mappings")
	}
}
