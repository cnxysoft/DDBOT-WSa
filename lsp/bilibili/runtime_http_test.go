package bilibili

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func restoreBilibiliRuntimeForHTTPTest(t *testing.T) {
	t.Helper()
	oldVerify, oldAccount := atomicVerifyInfo.Load(), atomicAccount.Load()
	oldUID, oldWbi := accountUid.Load(), wbi.Load()
	t.Cleanup(func() {
		credentialPublishMu.Lock()
		defer credentialPublishMu.Unlock()
		atomicVerifyInfo.Store(oldVerify)
		atomicAccount.Store(oldAccount)
		accountUid.Store(oldUID)
		wbi.Store(oldWbi)
	})
}

func TestRuntimeLatePersonalInfoCannotOverwriteNewIdentity(t *testing.T) {
	restoreBilibiliRuntimeForHTTPTest(t)
	oldHost := BasePath[PathXWebInterfaceNav]
	defer func() { BasePath[PathXWebInterfaceNav] = oldHost }()
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		fmt.Fprint(w, `{"code":0,"data":{"isLogin":true,"mid":111}}`)
	}))
	defer server.Close()
	defer unblock()
	BasePath[PathXWebInterfaceNav] = server.URL
	SetVerify("old-session", "old-csrf")
	done := make(chan struct{})
	go func() { FreshSelfInfo(); close(done) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("old self-info request did not start")
	}
	next, err := prepareCredentials("new-session", "new-csrf", func(*VerifyInfo) (*WebInterfaceNavResponse, error) {
		return &WebInterfaceNavResponse{Data: &WebInterfaceNavResponse_Data{IsLogin: true, Mid: 222}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	next.Apply()
	unblock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("old self-info request did not finish")
	}
	if getVerify().SESSDATA != "new-session" || accountUid.Load() != 222 {
		t.Fatal("old request overwrote committed new identity")
	}
}

func TestRuntimeLateConcernFailuresCannotAlertForNewLogin(t *testing.T) {
	for _, live := range []bool{false, true} {
		name, path := "dynamic", PathWebDynamicFeedAll
		if live {
			name, path = "live", PathRelationFeedList
		}
		t.Run(name, func(t *testing.T) {
			restoreBilibiliRuntimeForHTTPTest(t)
			oldHost := BasePath[path]
			defer func() { BasePath[path] = oldHost }()
			calls := 0
			setBilibiliLoginAlertDependenciesForTest(t, []int64{1}, func(int64) bool { calls++; return true })
			entered, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-release
				fmt.Fprint(w, `{"code":-101,"message":"账号未登录"}`)
			}))
			defer server.Close()
			defer unblock()
			BasePath[path] = server.URL
			SetVerify("old-session", "old-csrf")
			done := make(chan struct{})
			go func() {
				if live {
					(&Concern{}).freshLive()
				} else {
					(&Concern{}).freshDynamicNew()
				}
				close(done)
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("old concern request not issued")
			}
			p, err := prepareCredentials("new-session", "new-csrf", func(*VerifyInfo) (*WebInterfaceNavResponse, error) {
				return &WebInterfaceNavResponse{Data: &WebInterfaceNavResponse_Data{IsLogin: true, Mid: 222}}, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			p.Apply()
			unblock()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("old concern response did not finish")
			}
			if calls != 0 {
				t.Fatal("late old failure sent a new-login expiry alert")
			}
			if getVerify().SESSDATA != "new-session" || accountUid.Load() != 222 {
				t.Fatal("late concern failure invalidated new account")
			}
			bilibiliLoginAlertState.mu.Lock()
			invalid := len(bilibiliLoginAlertState.invalidSources)
			bilibiliLoginAlertState.mu.Unlock()
			if invalid != 0 {
				t.Fatal("late old failure polluted new-login alert state")
			}
		})
	}
}
