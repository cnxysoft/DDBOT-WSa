package telegram

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/proxy_pool"
	"github.com/spf13/viper"
)

func TestQueuedOldCommandsAreDiscardedOnTokenSwitch(t *testing.T) {
	resetTelegramRuntimeForTest(t)
	proxy_pool.Swap(nil)
	defer Close()
	var oldReturned, newReturned atomic.Bool
	releaseServer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if strings.HasSuffix(r.URL.Path, "/getMe") {
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"first_name":"audit","username":"audit_bot"}}`)
			return
		}
		if strings.Contains(r.URL.Path, "old") && !oldReturned.Swap(true) {
			fmt.Fprint(w, `{"ok":true,"result":[{"update_id":1,"message":{"from":{"id":2},"chat":{"id":-100},"text":"block"}},{"update_id":2,"message":{"from":{"id":2},"chat":{"id":-100},"text":"obsolete2"}},{"update_id":3,"message":{"from":{"id":2},"chat":{"id":-100},"text":"obsolete3"}}]}`)
			return
		}
		if strings.Contains(r.URL.Path, "new") && !newReturned.Swap(true) {
			fmt.Fprint(w, `{"ok":true,"result":[{"update_id":1,"message":{"from":{"id":2},"chat":{"id":-100},"text":"new"}}]}`)
			return
		}
		select {
		case <-r.Context().Done():
		case <-releaseServer:
		}
	}))
	defer server.Close()
	defer close(releaseServer)
	oldCfg := config.GlobalConfig
	config.GlobalConfig = &config.Config{Viper: viper.New()}
	defer func() { config.GlobalConfig = oldCfg }()
	endpoint := server.URL + "/bot%s/%s"
	off := testSnapshot(map[string]interface{}{"telegram.enable": false})
	old := testSnapshot(map[string]interface{}{"telegram.enable": true, "telegram.token": "old", "telegram.endpoint": endpoint})
	next := testSnapshot(map[string]interface{}{"telegram.enable": true, "telegram.token": "new", "telegram.endpoint": endpoint})
	entered, unblock := make(chan struct{}), make(chan struct{})
	var unblockOnce sync.Once
	releaseCallback := func() { unblockOnce.Do(func() { close(unblock) }) }
	defer func() { releaseCallback(); Close() }()
	observed := make(chan string, 5)
	StartReceiving(func(_, _ int64, text string) {
		if text == "block" {
			close(entered)
			<-unblock
			return
		}
		observed <- text
	})
	commit, _, err := PrepareRuntimeReload(off, old)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("first callback never entered")
	}
	deadline := time.Now().Add(time.Second)
	for {
		managerMu.RLock()
		queued := offsets["old"] >= 4
		managerMu.RUnlock()
		if queued {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("old command batch did not enter dispatch queue")
		}
		time.Sleep(time.Millisecond)
	}
	commit, _, err = PrepareRuntimeReload(old, next)
	if err != nil {
		t.Fatal(err)
	}
	if commit == nil {
		t.Fatalf("changed token returned no commit: old=%#v next=%#v oldToken=%s newToken=%s", old.Get("telegram"), next.Get("telegram"), old.GetString("telegram.token"), next.GetString("telegram.token"))
	}
	commit()
	releaseCallback()
	select {
	case got := <-observed:
		if got != "new" {
			t.Fatalf("retired token's queued command still executed: %s", got)
		}
	case <-time.After(time.Second):
		t.Fatal("new token command was not dispatched")
	}
	Close()
}

func TestCloseDrainsActiveCommandAfterConfigShutdown(t *testing.T) {
	resetTelegramRuntimeForTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	g := &generation{ctx: ctx, cancel: cancel, client: &http.Client{}}
	managerMu.Lock()
	current, initialized = g, true
	managerMu.Unlock()
	cfg := &config.Config{Viper: viper.New()}
	entered, proceed := make(chan struct{}), make(chan struct{})
	var proceedOnce sync.Once
	release := func() { proceedOnce.Do(func() { close(proceed) }) }
	defer release()
	saved := make(chan error, 1)
	go dispatch(incomingText{g: g, handler: func(_, _ int64, _ string) {
		close(entered)
		<-proceed
		saved <- cfg.Save([]byte("value: new\n"), func() error {
			t.Error("shutdown command must not reach persistence")
			return nil
		})
	}})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("command did not start")
	}
	cfg.BeginShutdown()
	closed := make(chan struct{})
	go func() { Close(); close(closed) }()
	select {
	case <-g.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Close did not retire the generation")
	}
	select {
	case <-closed:
		t.Fatal("Close returned while a command was still active")
	default:
	}
	release()
	select {
	case err := <-saved:
		if err == nil {
			t.Fatal("command save succeeded after shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("command Save deadlocked with Close")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not drain the completed command")
	}
}
