package onebot

import (
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndependentOneBotReloadRejectsChangedSelfID(t *testing.T) {
	var accepted atomic.Int64
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		index := accepted.Add(1)
		id := int64(101)
		if index == 2 {
			id = 202
		}
		_ = conn.WriteJSON(map[string]interface{}{"post_type": "meta_event", "meta_event_type": "lifecycle", "sub_type": "connect", "self_id": id, "time": time.Now().Unix()})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	build := func() (adapter.Adapter, error) {
		return NewOneBotAdapter(&adapter.AdapterConfig{WSMode: adapter.WSModeReverse, WSAddr: "ws" + strings.TrimPrefix(server.URL, "http"), Timeout: time.Second}), nil
	}
	initial, _ := build()
	r := adapter.NewReloadableAdapter(initial, build)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	deadline := time.Now().Add(time.Second)
	for initial.GetSelfID() != 101 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if initial.GetSelfID() != 101 {
		t.Fatal("initial lifecycle not received")
	}
	_, abort, err := r.PrepareReload("onebot-v11", "", build)
	if err == nil {
		time.Sleep(30 * time.Millisecond)
		abort()
		t.Fatal("accepted outbound candidate whose lifecycle self_id changed from 101 to 202")
	}
}

func TestIndependentOneBotFailedRollbackIsVisible(t *testing.T) {
	var accepted atomic.Int64
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		index := accepted.Add(1)
		id := int64(101)
		if index > 1 {
			id = 202
		}
		_ = conn.WriteJSON(map[string]interface{}{"post_type": "meta_event", "meta_event_type": "lifecycle", "sub_type": "connect", "self_id": id})
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	build := func() (adapter.Adapter, error) {
		return NewOneBotAdapter(&adapter.AdapterConfig{WSMode: adapter.WSModeReverse, WSAddr: "ws" + strings.TrimPrefix(server.URL, "http"), Timeout: 100 * time.Millisecond}), nil
	}
	initial, _ := build()
	r := adapter.NewReloadableAdapter(initial, build)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	deadline := time.Now().Add(time.Second)
	for initial.GetSelfID() != 101 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if initial.GetSelfID() != 101 {
		t.Fatal("initial identity missing")
	}
	_, _, err := r.PrepareReload("onebot-v11", "", build)
	if err == nil {
		t.Fatal("candidate identity switch accepted")
	}
	if r.LastReloadError() == nil {
		t.Fatal("failed rollback to wrong identity was falsely reported restored")
	}
	if r.IsConnected() {
		t.Fatal("failed rollback is connected")
	}
}
