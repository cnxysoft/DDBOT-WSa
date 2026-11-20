package bot

import (
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
)

func adapterSnapshot(mode, addr, token string) *config.Snapshot {
	v := viper.New()
	_ = v.MergeConfigMap(map[string]interface{}{
		"adapter":   map[string]interface{}{"mode": mode},
		"websocket": map[string]interface{}{"mode": "ws-server", "ws-server": addr, "token": token},
	})
	return (&config.Config{Viper: v}).Snapshot()
}

func TestAdapterBuilderRetainsOriginalTransportAcrossGlobalChanges(t *testing.T) {
	previousGlobal := config.GlobalConfig
	defer func() { config.GlobalConfig = previousGlobal }()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := reservation.Addr().String()
	reservation.Close()
	build := adapterBuilder(adapterSnapshot("onebot-v11", addr, "original-token"))
	v := viper.New()
	_ = v.MergeConfigMap(map[string]interface{}{
		"adapter":   map[string]interface{}{"mode": "satori"},
		"websocket": map[string]interface{}{"mode": "ws-reverse", "ws-reverse": "ws://127.0.0.1:1", "token": "changed-token"},
	})
	config.GlobalConfig = &config.Config{Viper: v}
	var previous adapter.Adapter
	for range 2 {
		a, err := build()
		if err != nil || a == nil {
			t.Fatal("captured builder failed", err)
		}
		if a == previous {
			t.Fatal("rollback builder reused the stopped instance")
		}
		previous = a
		if a.GetAdapterName() != "onebot-v11" {
			t.Fatal("builder read changed global protocol")
		}
		if err := a.Start(); err != nil {
			t.Fatal("captured listener failed", err)
		}
		func() {
			defer a.Stop()
			dialer := websocket.Dialer{HandshakeTimeout: time.Second}
			headers := http.Header{"Authorization": []string{"Bearer changed-token"}}
			conn, resp, err := dialer.Dial("ws://"+addr+"/", headers)
			if conn != nil {
				conn.Close()
			}
			if resp != nil {
				resp.Body.Close()
			}
			if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
				t.Fatal("changed global token entered captured transport", err)
			}
			headers.Set("Authorization", "Bearer original-token")
			conn, resp, err = dialer.Dial("ws://"+addr+"/", headers)
			if resp != nil {
				resp.Body.Close()
			}
			if err != nil {
				t.Fatal("captured token no longer accepted", err)
			}
			conn.Close()
		}()
	}
}

func TestUnrelatedSaveDoesNotRebuildBotConnection(t *testing.T) {
	previousInstance := Instance
	defer func() { Instance = previousInstance }()
	previous := adapterSnapshot("onebot-v11", "127.0.0.1:0", "token")
	build := adapterBuilder(previous)
	initial, err := build()
	if err != nil {
		t.Fatal(err)
	}
	defer initial.Stop()
	live := adapter.NewReloadableAdapter(initial, build)
	Instance = &Bot{Messenger: &adapter.Messenger{Adapter: live}}
	v := viper.New()
	_ = v.MergeConfigMap(map[string]interface{}{
		"adapter":   map[string]interface{}{"mode": "onebot-v11"},
		"websocket": map[string]interface{}{"mode": "ws-server", "ws-server": "127.0.0.1:0", "token": "token"},
		"notify":    map[string]interface{}{"parallel": 7},
	})
	next := (&config.Config{Viper: v}).Snapshot()
	commit, abort, err := PrepareAdapterReload(previous, next)
	if err != nil || commit != nil || abort != nil {
		t.Fatal("unrelated save attempted to rebuild disconnected transport", err)
	}
	changed := adapterSnapshot("onebot-v11", "127.0.0.1:0", "changed")
	if _, _, err := PrepareAdapterReload(previous, changed); err == nil {
		t.Fatal("relevant transport change did not reach lifecycle validation")
	}
}
