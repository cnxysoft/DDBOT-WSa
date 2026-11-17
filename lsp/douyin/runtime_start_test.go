package douyin

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/spf13/viper"
)

func TestRuntimeEmptyStartupCanReceiveNewCredentialsWithoutRestart(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	oldCfg, oldCred := config.GlobalConfig, activeCredentials.Load()
	cfg := &config.Config{Viper: viper.New()}
	config.GlobalConfig = cfg
	defer func() { config.GlobalConfig = oldCfg; activeCredentials.Store(oldCred) }()
	c := NewConcern(make(chan concern.Notify, 10))
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	defer c.Stop()
	if currentCredentials().ready() {
		t.Fatal("empty startup unexpectedly ready")
	}
	next := &config.Config{Viper: viper.New()}
	next.Set("douyin.acSignature", "new-sig")
	next.Set("douyin.acNonce", "new-nonce")
	next.Set("douyin.sessionId", "new-session")
	commit, _, err := PrepareRuntimeReload(cfg.Snapshot(), next.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	commit()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for key, want := range map[string]string{"__ac_signature": "new-sig", "__ac_nonce": "new-nonce", "sessionid": "new-session"} {
			cookie, err := r.Cookie(key)
			if err != nil || cookie.Value != want {
				t.Errorf("%s cookie mismatch", key)
			}
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer server.Close()
	var out string
	if err = requests.Get(server.URL, nil, &out, SetRequestOptions()...); err != nil {
		t.Fatal(err)
	}
	if !currentCredentials().ready() {
		t.Fatal("same live concern did not become ready")
	}
}
