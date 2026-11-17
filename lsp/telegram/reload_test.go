package telegram

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/spf13/viper"
)

func testSnapshot(values map[string]interface{}) *config.Snapshot {
	c := &config.Config{Viper: viper.New()}
	for key, value := range values {
		c.Set(key, value)
	}
	return c.Snapshot()
}

func TestReloadAuthorizationFailurePollCancellationAndChats(t *testing.T) {
	Close()
	managerMu.Lock()
	initialized = false
	receiver = nil
	offsets = make(map[string]int)
	managerMu.Unlock()
	t.Cleanup(Close)
	proxy_pool.Swap(nil)
	var active, maxActive, authorizations atomic.Int32
	entered := make(chan string, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			authorizations.Add(1)
			if strings.Contains(r.URL.Path, "bad") {
				fmt.Fprint(w, `{"ok":false,"error_code":401,"description":"unauthorized"}`)
				return
			}
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"test","username":"test_bot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getUpdates"):
			n := active.Add(1)
			for old := maxActive.Load(); n > old && !maxActive.CompareAndSwap(old, n); old = maxActive.Load() {
			}
			defer active.Add(-1)
			entered <- r.URL.Path
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Second):
			}
			fmt.Fprint(w, `{"ok":true,"result":[]}`)
		default:
			fmt.Fprint(w, `{"ok":true,"result":true}`)
		}
	}))
	defer server.Close()
	endpoint := server.URL + "/bot%s/%s"
	off := testSnapshot(map[string]interface{}{"telegram.enable": false})
	oldCfg := config.GlobalConfig
	config.GlobalConfig = &config.Config{Viper: viper.New()}
	t.Cleanup(func() { config.GlobalConfig = oldCfg })
	// Handler registration must survive initial disabled mode.
	StartReceiving(func(int64, int64, string) {})
	on := testSnapshot(map[string]interface{}{"telegram.enable": true, "telegram.token": "first", "telegram.endpoint": endpoint, "telegram.chats": []string{"-100"}})
	commit, _, err := PrepareRuntimeReload(off, on)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("receiver did not start after off->on")
	}
	managerMu.RLock()
	old := current
	managerMu.RUnlock()
	bad := testSnapshot(map[string]interface{}{"telegram.enable": true, "telegram.token": "bad", "telegram.endpoint": endpoint})
	if _, _, err = PrepareRuntimeReload(on, bad); err == nil {
		t.Fatal("failed authorization accepted")
	}
	managerMu.RLock()
	retained := current == old
	managerMu.RUnlock()
	if !retained {
		t.Fatal("authorization failure changed live client")
	}
	chats := testSnapshot(map[string]interface{}{"telegram.enable": true, "telegram.token": "first", "telegram.endpoint": endpoint, "telegram.chats": []string{"-200"}})
	before := authorizations.Load()
	commit, _, err = PrepareRuntimeReload(on, chats)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	if authorizations.Load() != before {
		t.Fatal("chat-only update reauthorized client")
	}
	managerMu.RLock()
	same := current == old && len(globalChats) == 1 && globalChats[0] == -200
	managerMu.RUnlock()
	if !same {
		t.Fatal("chat-only change restarted receiver or lost chats")
	}
	next := testSnapshot(map[string]interface{}{"telegram.enable": true, "telegram.token": "second", "telegram.endpoint": endpoint})
	commit, _, err = PrepareRuntimeReload(chats, next)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	select {
	case path := <-entered:
		if !strings.Contains(path, "second") {
			t.Fatal(path)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement receiver did not start")
	}
	select {
	case <-old.done:
	default:
		t.Fatal("replacement started before old poller exited")
	}
	commit, _, err = PrepareRuntimeReload(next, off)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	deadline := time.Now().Add(time.Second)
	for active.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if active.Load() != 0 {
		t.Fatal("disabled receiver remained active")
	}
}
