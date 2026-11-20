package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/lsp/cfg"
	"github.com/fsnotify/fsnotify"
)

func loadReloadConfig(t *testing.T, content string) string {
	t.Helper()
	path := withConfigFile(t, content)
	cfg.ConfigureReload()
	if err := config.GlobalConfig.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	config.GlobalConfig.OnConfigChange(func(fsnotify.Event) { cfg.ReloadCustomCommandPrefix() })
	return path
}

func TestConfigSaveAppliesBeforeResponseAndReportsRestart(t *testing.T) {
	loadReloadConfig(t, "bot:\n  commandPrefix: /\nwebsocket:\n  ws-server: old\nbilibili:\n  SESSDATA: secret-do-not-expose\n")
	rec := postConfig(t, `{"config":{"bot":{"commandPrefix":"!"},"customCommandPrefix":{"Help":"#"},"websocket":{"ws-server":"new"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Code, rec.Body.String())
	}
	var result struct {
		Reload config.ReloadStatus `json:"reload"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if cfg.GetCommandPrefix() != "!" || cfg.GetCommandPrefix("Help") != "#" {
		t.Fatal("response returned before command prefixes were applied")
	}
	if config.GlobalConfig.GetString("websocket.ws-server") != "old" {
		t.Fatal("connection settings changed without rebuilding the connection")
	}
	if !reflect.DeepEqual(result.Reload.RestartRequired, []string{"websocket.ws-server"}) || result.Reload.Revision != 2 {
		t.Fatal(result.Reload)
	}
	s := &Server{}
	status := httptest.NewRecorder()
	s.handleConfigStatus(status, httptest.NewRequest(http.MethodGet, "/api/v1/config/status", nil))
	if status.Code != http.StatusOK || strings.Contains(status.Body.String(), "secret-do-not-expose") || !strings.Contains(status.Body.String(), "websocket.ws-server") {
		t.Fatal(status.Body.String())
	}
	revert := postConfig(t, `{"config":{"websocket":{"ws-server":"old"}}}`)
	if revert.Code != http.StatusOK || len(config.GlobalConfig.Status().RestartRequired) != 0 {
		t.Fatal("reverted connection change remained pending")
	}
}

func TestConfigSaveRejectsInvalidCandidateWithoutWriting(t *testing.T) {
	const original = "bot:\n  commandPrefix: /\ncronjob:\n  - cron: '0 * * * *'\n    templateName: hourly\n"
	path := loadReloadConfig(t, original)
	for _, patch := range []string{
		`{"config":{"cronjob":[{"cron":"invalid"}]}}`,
		`{"config":{"customCommandPrefix":"invalid"}}`,
		`{"config":{"logLevel":"invalid"}}`,
		`{"config":{"bilibili":{"interval":"invalid"}}}`,
		`{"config":{"bilibili":{"interval":" 4s "}}}`,
		`{"config":{"bot":{"commandPrefix":["invalid"]}}}`,
		`{"config":{"platform":{"bilibili":{"enable":"typo"}}}}`,
		`{"config":{"twitter":{"interval":true}}}`,
		`{"config":{"cronjob":[{"cron":"0 * * * *","target":{"group":[true]}}]}}`,
	} {
		rec := postConfig(t, patch)
		if rec.Code != http.StatusBadRequest {
			t.Fatal(rec.Code, rec.Body.String())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != original || config.GlobalConfig.Status().Revision != 1 {
			t.Fatal("invalid save changed disk or running configuration")
		}
	}
}

func TestConfigWatcherDoesNotSkipExternalChangeAfterWebSave(t *testing.T) {
	path := loadReloadConfig(t, "bot:\n  commandPrefix: /\n")
	var calls atomic.Int32
	config.GlobalConfig.OnConfigChange(func(fsnotify.Event) { calls.Add(1) })
	config.GlobalConfig.WatchConfig()
	rec := postConfig(t, `{"config":{"bot":{"commandPrefix":"!"}}}`)
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body.String())
	}
	if err := os.WriteFile(path, []byte("bot:\n  commandPrefix: '#'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(4 * time.Second)
	for config.GlobalConfig.GetString("bot.commandPrefix") != "#" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if config.GlobalConfig.GetString("bot.commandPrefix") != "#" || calls.Load() != 2 {
		t.Fatal("external change was skipped or duplicate event reapplied the save")
	}
}

func TestConfigKeyWriterValidatesAndAppliesSynchronously(t *testing.T) {
	path := loadReloadConfig(t, "bilibili:\n  interval: 20s\n")
	if err := cfg.WriteConfigKeyValue("bilibili.interval", "4s"); err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval("bilibili", time.Minute) != 4*time.Second {
		t.Fatal("key writer did not publish its change")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WriteConfigKeyValue("bilibili.interval", "invalid") == nil {
		t.Fatal("invalid interval accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("invalid key write changed the file")
	}
}
