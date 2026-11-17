package onebot

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestOneBotReloadRebindsSamePortAndRotatesToken(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	builder := func(token string) adapter.AdapterBuilder {
		return func() (adapter.Adapter, error) {
			return NewOneBotAdapter(&adapter.AdapterConfig{WSMode: adapter.WSModeServer, WSAddr: address, Token: token, Timeout: time.Second}), nil
		}
	}
	old, err := builder("old")()
	require.NoError(t, err)
	r := adapter.NewReloadableAdapter(old, builder("old"))
	require.NoError(t, r.Start())
	defer r.Stop()
	dial := func(token string) (*websocket.Conn, *http.Response, error) {
		return websocket.DefaultDialer.Dial("ws://"+address+"/", http.Header{"Authorization": []string{"Bearer " + token}})
	}
	conn, _, err := dial("old")
	require.NoError(t, err)
	defer conn.Close()
	commit, _, err := r.PrepareReload("onebot-v11", "", builder("new"))
	require.NoError(t, err)
	commit()
	_, response, err := dial("old")
	require.Error(t, err)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	newConn, _, err := dial("new")
	require.NoError(t, err)
	defer newConn.Close()
	_, abort, err := r.PrepareReload("onebot-v11", "", builder("candidate"))
	require.NoError(t, err)
	abort()
	_, response, err = dial("candidate")
	require.Error(t, err)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	restored, _, err := dial("new")
	require.NoError(t, err)
	defer restored.Close()
}

func TestOneBotReloadFailedOutboundRestoresListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	build := func() (adapter.Adapter, error) {
		return NewOneBotAdapter(&adapter.AdapterConfig{WSMode: adapter.WSModeServer, WSAddr: address, Token: "old", Timeout: time.Second}), nil
	}
	old, err := build()
	require.NoError(t, err)
	r := adapter.NewReloadableAdapter(old, build)
	require.NoError(t, r.Start())
	defer r.Stop()
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "Unauthorized", http.StatusUnauthorized) }))
	defer failed.Close()
	_, _, err = r.PrepareReload("onebot-v11", "", func() (adapter.Adapter, error) {
		return NewOneBotAdapter(&adapter.AdapterConfig{WSMode: adapter.WSModeReverse, WSAddr: "ws" + strings.TrimPrefix(failed.URL, "http"), Token: "bad", Timeout: 50 * time.Millisecond}), nil
	})
	require.ErrorContains(t, err, "timeout")
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+address, http.Header{"Authorization": []string{"Bearer old"}})
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, r.LastReloadError())
}

func TestOneBotServerBindErrorIsSynchronous(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	a := NewOneBotAdapter(&adapter.AdapterConfig{WSMode: adapter.WSModeServer, WSAddr: listener.Addr().String()})
	require.Error(t, a.Start())
	require.NoError(t, a.Stop())
	require.NoError(t, a.Stop())
}

func TestOneBotReloadRejectsDifferentBot(t *testing.T) {
	var connections atomic.Int64
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		id := int64(101)
		if connections.Add(1) == 2 {
			id = 202
		}
		_ = conn.WriteJSON(map[string]interface{}{"post_type": "meta_event", "meta_event_type": "lifecycle", "self_id": id})
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
	initial, err := build()
	require.NoError(t, err)
	r := adapter.NewReloadableAdapter(initial, build)
	require.NoError(t, r.Start())
	defer r.Stop()
	require.Eventually(t, func() bool { return initial.GetSelfID() == 101 }, time.Second, time.Millisecond)
	_, _, err = r.PrepareReload("onebot-v11", "", build)
	require.ErrorContains(t, err, "identity")
	require.Eventually(t, func() bool { return r.IsConnected() }, time.Second, time.Millisecond)
	require.Equal(t, int64(101), r.GetSelfID())
}

func TestOneBotServerReloadRejectsLaterDifferentIdentity(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	var candidate *OneBotAdapter
	build := func() (adapter.Adapter, error) {
		candidate = NewOneBotAdapter(&adapter.AdapterConfig{WSMode: adapter.WSModeServer, WSAddr: address, Token: "token", Timeout: time.Second})
		return candidate, nil
	}
	initial, err := build()
	require.NoError(t, err)
	r := adapter.NewReloadableAdapter(initial, build)
	require.NoError(t, r.Start())
	defer r.Stop()
	dial := func() *websocket.Conn {
		conn, _, err := websocket.DefaultDialer.Dial("ws://"+address, http.Header{"Authorization": []string{"Bearer token"}})
		require.NoError(t, err)
		return conn
	}
	old := dial()
	defer old.Close()
	require.NoError(t, old.WriteJSON(map[string]interface{}{"post_type": "meta_event", "meta_event_type": "lifecycle", "self_id": 101}))
	require.Eventually(t, func() bool { return initial.GetSelfID() == 101 }, time.Second, time.Millisecond)
	commit, _, err := r.PrepareReload("onebot-v11", "", build)
	require.NoError(t, err)
	commit()
	require.False(t, r.IsConnected())
	bad := dial()
	defer bad.Close()
	require.NoError(t, bad.WriteJSON(map[string]interface{}{"post_type": "meta_event", "meta_event_type": "lifecycle", "self_id": 202}))
	require.Eventually(t, func() bool { return candidate.identityRejected.Load() }, time.Second, time.Millisecond)
	require.False(t, r.IsConnected())
	require.Equal(t, int64(101), r.GetSelfID())
	_, err = r.SendApi("get_login_info", nil)
	require.ErrorIs(t, err, adapter.ErrRequestNotSent)
	good := dial()
	defer good.Close()
	require.NoError(t, good.WriteJSON(map[string]interface{}{"post_type": "meta_event", "meta_event_type": "lifecycle", "self_id": 101}))
	require.Eventually(t, func() bool { return r.IsConnected() }, time.Second, time.Millisecond)
}
