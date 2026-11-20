package telegram

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/spf13/viper"
)

func TestTelegramCommandCanSaveAndSwitchToken(t *testing.T) {
	resetTelegramRuntimeForTest(t)
	proxy_pool.Swap(nil)
	defer Close()
	var firstReturned atomic.Bool
	nextPolled := make(chan struct{}, 1)
	releaseServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"audit","username":"audit_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			if strings.Contains(r.URL.Path, "first") && !firstReturned.Swap(true) {
				fmt.Fprint(w, `{"ok":true,"result":[{"update_id":101,"message":{"message_id":1,"date":1,"from":{"id":2,"is_bot":false,"first_name":"audit"},"chat":{"id":-100,"type":"supergroup"},"text":"change"}}]}`)
				return
			}
			if strings.Contains(r.URL.Path, "second") {
				select {
				case nextPolled <- struct{}{}:
				default:
				}
			}
			select {
			case <-r.Context().Done():
			case <-releaseServer:
			}
		}
	}))
	defer server.Close()
	defer close(releaseServer)
	defer Close()
	path := filepath.Join(t.TempDir(), "application.yaml")
	initial := []byte(fmt.Sprintf("telegram:\n  enable: true\n  token: first\n  endpoint: %q\n", server.URL+"/bot%s/%s"))
	if err := os.WriteFile(path, initial, 0600); err != nil {
		t.Fatal(err)
	}
	c := &config.Config{Viper: viper.New()}
	c.SetConfigFile(path)
	if err := c.ReadInConfig(); err != nil {
		t.Fatal(err)
	}
	oldConfig := config.GlobalConfig
	config.GlobalConfig = c
	defer func() { config.GlobalConfig = oldConfig }()
	c.OnPrepareReload(PrepareRuntimeReload)
	saved := make(chan error, 1)
	StartReceiving(func(int64, int64, string) {
		updated := []byte(strings.Replace(string(initial), "token: first", "token: second", 1))
		saved <- c.Save(updated, func() error { return os.WriteFile(path, updated, 0600) })
	})
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("command-triggered Save deadlocked retiring its own poller")
	}
	select {
	case <-nextPolled:
	case <-time.After(time.Second):
		t.Fatal("replacement token never polled")
	}
	if got := c.GetString("telegram.token"); got != "second" {
		t.Fatal(got)
	}
	Close()
}
