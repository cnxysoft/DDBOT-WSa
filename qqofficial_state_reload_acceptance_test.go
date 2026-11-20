package DDBOT

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/bot"
	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/adapter"
	"github.com/cnxysoft/DDBOT-WSa/adapter/qqofficial"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/cnxysoft/DDBOT-WSa/lsp/eventbus"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
)

// Exercise the actual policy -> effective snapshot -> production adapter builder
// -> QQ state adoption chain, rather than replacing the resource participant.
func TestIndependentQQStateFileStaysColdAcrossRealAdapterReloads(t *testing.T) {
	cfg.EnableResourceHotReload()
	for _, explicit := range []bool{true, false} {
		name := "explicit-runtime-path"
		if !explicit {
			name = "implicit-default-runtime-path"
		}
		t.Run(name, func(t *testing.T) {
			workdir := t.TempDir()
			t.Chdir(workdir)
			const appID = "independent-state-app"
			originalPath := ""
			actualPath := qqofficial.DefaultStateFilePath(appID)
			if explicit {
				originalPath = filepath.Join(workdir, "running-state.json")
				actualPath = originalPath
			}
			stateBytes, err := json.Marshal(map[string]interface{}{
				"version": 1, "app_id": appID,
				"idmap":  map[string]int64{appID + "\x1fretained-user": 77},
				"groups": []interface{}{},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(actualPath, stateBytes, 0600); err != nil {
				t.Fatal(err)
			}
			candidatePath := filepath.Join(workdir, "pending-state.json")
			// A different identity makes opening the desired path observably fail,
			// while the byte check also catches writes by an accidental candidate.
			const sentinel = `{"version":1,"app_id":"another-bot","idmap":{},"groups":[]}`
			if err := os.WriteFile(candidatePath, []byte(sentinel), 0600); err != nil {
				t.Fatal(err)
			}
			var connections atomic.Int32
			var authMu sync.Mutex
			var secrets []string
			var server *httptest.Server
			upgrader := websocket.Upgrader{}
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/app/getAppAccessToken":
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						http.Error(w, "bad request", http.StatusBadRequest)
						return
					}
					authMu.Lock()
					secrets = append(secrets, body["clientSecret"])
					authMu.Unlock()
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"access_token": "token-" + body["clientSecret"], "expires_in": 7200})
				case qqofficial.PathGateway:
					_ = json.NewEncoder(w).Encode(map[string]string{"url": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"})
				case "/ws":
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer conn.Close()
					_ = conn.WriteJSON(map[string]interface{}{"op": qqofficial.OpHello, "d": map[string]interface{}{"heartbeat_interval": 60000}})
					var identify interface{}
					if conn.ReadJSON(&identify) != nil {
						return
					}
					connections.Add(1)
					_ = conn.WriteJSON(map[string]interface{}{"op": qqofficial.OpDispatch, "s": 1, "t": qqofficial.EventReady,
						"d": map[string]interface{}{"session_id": "state-reload-session", "shard": []int{0, 1}, "user": map[string]interface{}{"id": "10001", "username": "bot", "bot": true}}})
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
			dataFor := func(path, secret, prefix string) []byte {
				state := ""
				if path != "" {
					state = fmt.Sprintf("  state-file: %q\n", path)
				}
				return []byte(fmt.Sprintf("adapter:\n  mode: qqofficial\nbot:\n  commandPrefix: %q\nqqofficial:\n  app-id: %s\n  app-secret: %q\n  api-base-url: %q\n  start-timeout: 1s\n  http-timeout: 1s\n%s", prefix, appID, secret, server.URL, state))
			}
			configPath := filepath.Join(workdir, "application.yaml")
			if err := os.WriteFile(configPath, dataFor(originalPath, "old", "/"), 0600); err != nil {
				t.Fatal(err)
			}
			c := &config.Config{Viper: viper.New()}
			c.SetConfigFile(configPath)
			c.ConfigureReload(cfg.ValidateReload, cfg.IsHotKey)
			if err := c.ReadInConfig(); err != nil {
				t.Fatal(err)
			}
			initialConfig := qqofficial.GetConfigFrom(c.Snapshot())
			initialBuild := func() (adapter.Adapter, error) {
				return qqofficial.NewQQOfficialAdapterWithConfig(initialConfig, originalPath), nil
			}
			initial, err := initialBuild()
			if err != nil {
				t.Fatal(err)
			}
			live := adapter.NewReloadableAdapter(initial, initialBuild)
			previousInstance := bot.Instance
			bot.Instance = &bot.Bot{Messenger: &adapter.Messenger{Adapter: live}}
			defer func() {
				_ = live.Stop()
				bot.Instance = previousInstance
			}()
			if err := live.Start(); err != nil {
				t.Fatal(err)
			}
			online := eventbus.BusObj.Subscribe("bot_online")
			reconnecting := false
			c.OnPrepareReload(func(previous, next *config.Snapshot) (func(), func(), error) {
				if next.GetString("qqofficial.state-file") != originalPath {
					return nil, nil, fmt.Errorf("candidate used desired state path before restart")
				}
				commit, abort, err := bot.PrepareAdapterReload(previous, next)
				reconnecting = commit != nil
				return commit, abort, err
			})
			for i, step := range []struct {
				name, path, secret, prefix string
				pending                    bool
				connections                int32
			}{
				{"path-only", candidatePath, "old", "/", true, 1},
				{"restore-only", originalPath, "old", "/", false, 1},
				{"mixed-secret-and-path", candidatePath, "new", "/", true, 2},
				{"later-secret-and-hot-setting", candidatePath, "later", "!", true, 3},
				{"restore-with-hot-setting", originalPath, "later", "#", false, 3},
			} {
				t.Run(step.name, func(t *testing.T) {
					data := dataFor(step.path, step.secret, step.prefix)
					reconnecting = false
					status, err := c.SaveAndStatus(data, func() error { return os.WriteFile(configPath, data, 0600) })
					if err != nil {
						t.Fatal("a cold path must be saved without entering live path adoption", err)
					}
					if reconnecting {
						select {
						case <-online:
						case <-time.After(2 * time.Second):
							t.Fatal("adapter commit notification did not finish")
						}
					}
					pending := []string{}
					if step.pending {
						pending = []string{"qqofficial.state-file"}
					}
					if !reflect.DeepEqual(status.RestartRequired, pending) || status.Revision != uint64(i+2) {
						t.Fatalf("unexpected saved status: %+v", status)
					}
					if c.GetString("qqofficial.state-file") != originalPath || c.GetString("qqofficial.app-secret") != step.secret || c.GetString("bot.commandPrefix") != step.prefix {
						t.Fatal("running snapshot did not separate the cold path from hot settings")
					}
					stored, err := os.ReadFile(configPath)
					if err != nil || !bytes.Equal(stored, data) || !bytes.Equal(c.Snapshot().RawYAML(), data) {
						t.Fatal("saved source did not retain the desired cold configuration", err)
					}
					untouched, err := os.ReadFile(candidatePath)
					if err != nil || string(untouched) != sentinel {
						t.Fatal("pending destination state file was changed before restart", err)
					}
					if connections.Load() != step.connections || !live.IsConnected() || live.LastReloadError() != nil {
						t.Fatalf("unexpected connection transition: count=%d want=%d error=%v", connections.Load(), step.connections, live.LastReloadError())
					}
					if _, err := live.GetStrangerInfo(77); err != nil {
						t.Fatal("the original ID mapping was lost", err)
					}
				})
				if t.Failed() {
					return
				}
			}
			authMu.Lock()
			gotSecrets := append([]string(nil), secrets...)
			authMu.Unlock()
			if !reflect.DeepEqual(gotSecrets, []string{"old", "new", "later"}) {
				t.Fatalf("hot credential preparation used unexpected versions: %v", gotSecrets)
			}
		})
	}
}
