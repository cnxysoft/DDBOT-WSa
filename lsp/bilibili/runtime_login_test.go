package bilibili

import (
	"errors"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/spf13/viper"
)

func TestPreparedCredentialsPreserveLiveSessionUntilApply(t *testing.T) {
	original, originalUID := atomicVerifyInfo.Load(), accountUid.Load()
	t.Cleanup(func() { atomicVerifyInfo.Store(original); accountUid.Store(originalUID) })
	SetVerify("old-session", "old-csrf")
	accountUid.Store(12)
	for _, failure := range []bool{true, false} {
		prepared, err := prepareCredentials("new-session", "new-csrf", func(candidate *VerifyInfo) (*WebInterfaceNavResponse, error) {
			if getVerify().SESSDATA != "old-session" || candidate.SESSDATA != "new-session" {
				t.Fatal("candidate leaked into the running requests during validation")
			}
			if failure {
				return nil, errors.New("offline probe")
			}
			return &WebInterfaceNavResponse{Data: &WebInterfaceNavResponse_Data{IsLogin: false}}, nil
		})
		if err == nil || prepared != nil || getVerify().SESSDATA != "old-session" || accountUid.Load() != 12 {
			t.Fatal("rejected candidate changed the running login")
		}
	}
	prepared, err := prepareCredentials("new-session", "new-csrf", func(candidate *VerifyInfo) (*WebInterfaceNavResponse, error) {
		return &WebInterfaceNavResponse{Data: &WebInterfaceNavResponse_Data{IsLogin: true, Mid: 34}}, nil
	})
	if err != nil || getVerify().SESSDATA != "old-session" {
		t.Fatalf("prepare should retain old session until commit: %v", err)
	}
	prepared.Apply()
	if getVerify().SESSDATA != "new-session" || GetVerifyBiliJct() != "new-csrf" || accountUid.Load() != 34 {
		t.Fatal("candidate session was not applied together")
	}
	logout, err := PrepareCredentials("", "")
	if err != nil {
		t.Fatal(err)
	}
	logout.Apply()
	if IsCookieGiven() || accountUid.Load() != 0 {
		t.Fatal("clearing both credentials must log out without a network request")
	}
}

func TestRuntimeLoginIgnoresOldConcernHealthResponses(t *testing.T) {
	original := atomicVerifyInfo.Load()
	t.Cleanup(func() { atomicVerifyInfo.Store(original); bilibiliLoginAlertState.reset() })
	bilibiliLoginAlertState.reset()
	SetVerify("old-session", "old-csrf")
	old := getVerify()
	SetVerify("new-session", "new-csrf")
	notifyBilibiliLoginExpiredForGeneration(bilibiliLoginSourceDynamic, old)
	notifyBilibiliLoginExpiredForGeneration(bilibiliLoginSourceLive, old)
	bilibiliLoginAlertState.mu.Lock()
	invalid := len(bilibiliLoginAlertState.invalidSources)
	bilibiliLoginAlertState.mu.Unlock()
	if invalid != 0 {
		t.Fatal("old request invalidated the new login")
	}
	bilibiliLoginAlertState.markInvalid(bilibiliLoginSourceLive)
	markBilibiliLoginRecoveredForGeneration(bilibiliLoginSourceLive, old)
	bilibiliLoginAlertState.mu.Lock()
	invalid = len(bilibiliLoginAlertState.invalidSources)
	bilibiliLoginAlertState.mu.Unlock()
	if invalid != 1 {
		t.Fatal("old success cleared the current login failure")
	}
}

func TestRuntimePasswordLoginUsesCandidateWithoutPublishing(t *testing.T) {
	original := atomicAccount.Load()
	t.Cleanup(func() { atomicAccount.Store(original) })
	SetAccount("old-user", "old-password")
	s := &config.Config{Viper: viper.New()}
	s.Set("bilibili.account", "candidate-user")
	s.Set("bilibili.password", "candidate-password")
	called := false
	_, err := prepareRuntimeCredentials(s.Snapshot(), func(username, password string) (*LoginResponse, error) {
		called = true
		if username != "candidate-user" || password != "candidate-password" || getAccount().username != "old-user" {
			t.Fatal("password validation used/published wrong account generation")
		}
		return nil, errors.New("offline failure")
	}, nil)
	if err == nil || !called || getAccount().username != "old-user" {
		t.Fatal("failed password login changed live account")
	}
}

func TestPreparedCredentialsRejectPartialCookiePair(t *testing.T) {
	for _, pair := range [][2]string{{"sess", ""}, {"", "csrf"}} {
		if _, err := PrepareCredentials(pair[0], pair[1]); err == nil {
			t.Fatal("partial cookie pair accepted")
		}
	}
}
