package acfun

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestRuntimeCandidateHTTPValidationRejectAndCommit(t *testing.T) {
	oldVerify, oldAccount, oldUID := atomicVerifyInfo.Load(), atomicAccount.Load(), accountUid.Load()
	oldHost := BasePath[PathApiPersonalInfo]
	defer func() {
		atomicVerifyInfo.Store(oldVerify)
		atomicAccount.Store(oldAccount)
		accountUid.Store(oldUID)
		BasePath[PathApiPersonalInfo] = oldHost
	}()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, _ := r.Cookie("acPasstoken")
		if cookie == nil || cookie.Value != "new-token" {
			fmt.Fprint(w, `{"result":401}`)
			return
		}
		fmt.Fprint(w, `{"result":0,"info":{"userId":222}}`)
	}))
	defer server.Close()
	BasePath[PathApiPersonalInfo] = server.URL
	SetVerify(111, "old-token")
	accountUid.Store(111)
	previous := &config.Config{Viper: viper.New()}
	previous.Set("acfun.authKey", 111)
	previous.Set("acfun.acPassToken", "old-token")
	next := &config.Config{Viper: viper.New()}
	next.Set("acfun.authKey", 222)
	next.Set("acfun.acPassToken", "rejected")
	if _, _, err := PrepareRuntimeReload(previous.Snapshot(), next.Snapshot()); err == nil {
		t.Fatal("actual rejected HTTP candidate accepted")
	}
	if getVerify().AcPassToken != "old-token" || accountUid.Load() != 111 {
		t.Fatal("rejection modified live session")
	}
	next.Set("acfun.acPassToken", "new-token")
	commit, abort, err := PrepareRuntimeReload(previous.Snapshot(), next.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if getVerify().AcPassToken != "old-token" {
		abort()
		t.Fatal("prepare published before commit")
	}
	commit()
	if getVerify().AcPassToken != "new-token" || accountUid.Load() != 222 {
		t.Fatal("commit did not update identity")
	}
	if !mux.TryLock() {
		t.Fatal("commit leaked credential lock")
	}
	mux.Unlock()
}
