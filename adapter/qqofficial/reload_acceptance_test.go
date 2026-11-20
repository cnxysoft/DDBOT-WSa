package qqofficial

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/gorilla/websocket"
)

func TestIndependentQQOfficialWireSequencesAcrossReloadAndRollback(t *testing.T) {
	type sent struct {
		seq          int
		auth, anchor string
	}
	var mu sync.Mutex
	var seen []sent
	var server *httptest.Server
	upgrader := websocket.Upgrader{}
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/getAppAccessToken":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["clientSecret"] == "bad" {
				http.Error(w, "invalid", 401)
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
			wsTestReady(conn, "independent-session")
			for {
				if _, _, err := wsTestRead(conn); err != nil {
					return
				}
			}
		case "/users/@me":
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "10001", "username": "independent-bot"})
		case "/v2/groups/group-openid/messages":
			var body struct {
				MsgSeq int    `json:"msg_seq"`
				MsgID  string `json:"msg_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			mu.Lock()
			seen = append(seen, sent{body.MsgSeq, r.Header.Get("Authorization"), body.MsgID})
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"id": fmt.Sprint(body.MsgSeq), "timestamp": time.Now().Format(time.RFC3339)})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	state := filepath.Join(t.TempDir(), "qq-state.json")
	build := func(secret string) adapter.AdapterBuilder {
		return func() (adapter.Adapter, error) {
			return NewQQOfficialAdapterWithConfig(&Config{AppID: "app", AppSecret: secret, APIBaseURL: server.URL, StartTimeout: time.Second, HTTPTimeout: time.Second}, state), nil
		}
	}
	initial, _ := build("old")()
	old := initial.(*QQOfficialAdapter)
	r := adapter.NewReloadableAdapter(initial, build("old"))
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	group := old.ids.ToInt64("app", "group-openid")
	old.passive.set(TargetGroup, group, "group-openid", "anchor")
	send := func() {
		if _, err := r.SendGroupMessage(group, "hello"); err != nil {
			t.Fatal(err)
		}
	}
	send()
	commit, _, err := r.PrepareReload("qqofficial", "qqofficial/app", build("new"))
	if err != nil {
		t.Fatal(err)
	}
	commit()
	send()
	if _, _, err := r.PrepareReload("qqofficial", "qqofficial/app", build("bad")); err == nil {
		t.Fatal("invalid credential accepted")
	}
	send()
	_, abort, err := r.PrepareReload("qqofficial", "qqofficial/app", build("candidate"))
	if err != nil {
		t.Fatal(err)
	}
	abort()
	send()
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 4 {
		t.Fatalf("expected four actual sends: %+v", seen)
	}
	for i, item := range seen {
		auth := "QQBot token-new"
		if i == 0 {
			auth = "QQBot token-old"
		}
		if item.seq != i+1 || item.anchor != "anchor" || item.auth != auth {
			t.Fatalf("wire state after reload: %+v", seen)
		}
	}
}
