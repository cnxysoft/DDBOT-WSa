package douyin

import (
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/spf13/viper"
)

func TestRuntimeReloadCredentialsResumeAndClear(t *testing.T) {
	original := activeCredentials.Load()
	t.Cleanup(func() { activeCredentials.Store(original) })
	old, next := &config.Config{Viper: viper.New()}, &config.Config{Viper: viper.New()}
	activeCredentials.Store(&runtimeCredentials{})
	if currentCredentials().ready() {
		t.Fatal("empty credentials should wait")
	}
	next.Set("douyin.acSignature", "new-signature")
	if _, _, err := PrepareRuntimeReload(old.Snapshot(), next.Snapshot()); err == nil {
		t.Fatal("partial pair accepted")
	}
	next.Set("douyin.acNonce", "new-nonce")
	next.Set("douyin.sessionId", "new-session")
	commit, _, err := PrepareRuntimeReload(old.Snapshot(), next.Snapshot())
	if err != nil || currentCredentials().ready() {
		t.Fatalf("prepare mutated running session: %v", err)
	}
	commit()
	if !currentCredentials().ready() || requests.ExtractCookieOption(SetRequestOptions(), "sessionid") != "new-session" {
		t.Fatal("credentials did not resume requests")
	}
	clear, _, err := PrepareRuntimeReload(next.Snapshot(), old.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	clear()
	if currentCredentials().ready() || requests.ExtractCookieOption(SetRequestOptions(), "sessionid") != "" {
		t.Fatal("clearing configuration retained old credentials")
	}
}
