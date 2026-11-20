package bot

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/adapter/qqofficial"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
)

func TestIndependentProductionReloadPublishesFastAndFirstBindingOnline(t *testing.T) {
	for _, protocol := range []string{"satori", "qqofficial"} {
		t.Run(protocol, func(t *testing.T) {
			upgrader := websocket.Upgrader{}
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if protocol == "satori" {
					if !websocket.IsWebSocketUpgrade(r) {
						_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []interface{}{}})
						return
					}
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()
					var identify interface{}
					if conn.ReadJSON(&identify) != nil {
						return
					}
					_ = conn.WriteJSON(map[string]interface{}{"op": 4, "body": map[string]interface{}{"logins": []interface{}{map[string]interface{}{"platform": "qq", "user": map[string]interface{}{"id": "same-bot"}, "status": 1}}}})
					for {
						if _, _, err := conn.ReadMessage(); err != nil {
							return
						}
					}
				}
				switch r.URL.Path {
				case "/app/getAppAccessToken":
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "token", "expires_in": 7200})
				case qqofficial.PathGateway:
					_ = json.NewEncoder(w).Encode(map[string]string{"url": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"})
				case "/ws":
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()
					_ = conn.WriteJSON(map[string]interface{}{"op": qqofficial.OpHello, "d": map[string]interface{}{"heartbeat_interval": 5000}})
					var identify interface{}
					if conn.ReadJSON(&identify) != nil {
						return
					}
					_ = conn.WriteJSON(map[string]interface{}{"op": qqofficial.OpDispatch, "s": 1, "t": qqofficial.EventReady, "d": map[string]interface{}{"session_id": "same-session", "shard": []int{0, 1}, "user": map[string]interface{}{"id": "10001", "username": "bot", "bot": true}}})
					for {
						if _, _, err := conn.ReadMessage(); err != nil {
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
			configPath := filepath.Join(t.TempDir(), "application.yaml")
			initial := fmt.Sprintf("adapter:\n  mode: satori\nwebsocket:\n  mode: ws-reverse\n  ws-reverse: %q\n  token: old\n  timeout: 1s\n", server.URL)
			next := strings.Replace(initial, "token: old", "token: new", 1)
			if protocol == "qqofficial" {
				initial = fmt.Sprintf("adapter:\n  mode: qqofficial\nqqofficial:\n  api-base-url: %q\n  state-file: %q\n  start-timeout: 1s\n  http-timeout: 1s\n", server.URL, filepath.Join(t.TempDir(), "qq-state.json"))
				next = initial + "  app-id: app\n  app-secret: secret\n"
			}
			if err := os.WriteFile(configPath, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Viper: viper.New()}
			cfg.SetConfigFile(configPath)
			cfg.SetConfigType("yaml")
			if err := cfg.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			oldConfig, oldInstance, oldBus := config.GlobalConfig, Instance, eventbus.BusObj
			config.GlobalConfig = cfg
			eventbus.BusObj = eventbus.New()
			defer func() { config.GlobalConfig = oldConfig; Instance = oldInstance; eventbus.BusObj = oldBus }()
			build := adapterBuilder(cfg.Snapshot())
			initialAdapter, err := build()
			if err != nil {
				t.Fatal(err)
			}
			live := adapter.NewReloadableAdapter(initialAdapter, build)
			Instance = &Bot{Messenger: adapter.NewMessenger(live)}
			instance := Instance
			if err := live.Start(); protocol == "satori" && err != nil {
				t.Fatal(err)
			} else if protocol == "qqofficial" && err == nil {
				t.Fatal("unconfigured QQ unexpectedly started")
			}
			defer Instance.Messenger.Stop()
			cfg.OnPrepareReload(PrepareAdapterReload)
			online := eventbus.BusObj.Subscribe("bot_online")
			reentered := make(chan error, 1)
			go func() {
				select {
				case <-online:
				case <-time.After(2 * time.Second):
					reentered <- fmt.Errorf("rapid reload did not publish bot_online")
					return
				}
				if !instance.Online.Load() || !instance.Messenger.Online.Load() ||
					instance.Uin.Load() != live.GetSelfID() || instance.Messenger.GetUin() != live.GetSelfID() {
					reentered <- fmt.Errorf("online notification exposed inconsistent identity")
					return
				}
				reentered <- cfg.Save([]byte(next), func() error { return nil })
			}()
			if err := cfg.Save([]byte(next), func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-reentered:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("bot_online subscriber config save deadlocked")
			}
		})
	}
}
