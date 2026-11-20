package acfun

import (
	"errors"
	"testing"

	"github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/requests"
	"github.com/spf13/viper"
)

func TestRuntimeReloadPrepareCredentialsPreservesLiveState(t *testing.T) {
	original, originalAccount, originalUID := getVerify(), atomicAccount.Load(), accountUid.Load()
	t.Cleanup(func() {
		atomicVerifyInfo.Store(original)
		atomicAccount.Store(originalAccount)
		accountUid.Store(originalUID)
	})
	SetVerify(12, "old-token")
	accountUid.Store(12)
	s := &config.Config{Viper: viper.New()}
	s.Set("acfun.authKey", 34)
	s.Set("acfun.acPassToken", "new-token")
	validate := func(opts []requests.Option) (*UserResponse, error) {
		if getVerify().AcPassToken != "old-token" || requests.ExtractCookieOption(opts, "acPasstoken") != "new-token" {
			t.Fatal("validation did not isolate candidate credentials")
		}
		return nil, errors.New("invalid candidate")
	}
	if _, err := prepareCredentials(s.Snapshot(), nil, validate); err == nil || getVerify().AcPassToken != "old-token" {
		t.Fatal("invalid credentials replaced live login")
	}
	p, err := prepareCredentials(s.Snapshot(), nil, func([]requests.Option) (*UserResponse, error) {
		return &UserResponse{Info: &Info{UserId: 34}}, nil
	})
	if err != nil || p.uid != 34 || p.verify.AcPassToken != "new-token" || accountUid.Load() != 12 {
		t.Fatalf("unexpected candidate result: %v", err)
	}
	if _, err := prepareCredentials((&config.Config{Viper: viper.New()}).Snapshot(), nil, nil); err != nil {
		t.Fatalf("clearing all credentials should succeed offline: %v", err)
	}
}
