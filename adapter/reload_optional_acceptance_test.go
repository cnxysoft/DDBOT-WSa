package adapter

import "testing"

type independentOptionalAdapter struct{ reloadTestAdapter }

func (a *independentOptionalAdapter) GetBotName() string                  { return "bot-name" }
func (a *independentOptionalAdapter) SupportsForward() bool               { return false }
func (a *independentOptionalAdapter) MaxMessagePayloadBytes() int         { return 1234 }
func (a *independentOptionalAdapter) GeneratedImageOutputLimitBytes() int { return 5678 }
func (a *independentOptionalAdapter) SendJoinIntro(int64, interface{}) (int32, error) {
	return 4321, nil
}
func (a *independentOptionalAdapter) SetGroupAddRequest(flag string, approve bool, reason string) error {
	if flag != "flag" || !approve || reason != "reason" {
		panic("group request lost parameters")
	}
	return nil
}
func (a *independentOptionalAdapter) SetFriendAddRequest(flag string, approve bool) error {
	if flag != "flag" || !approve {
		panic("friend request lost parameters")
	}
	return nil
}

func TestIndependentReloadableAdapterPreservesOptionalCapabilities(t *testing.T) {
	build := func() (Adapter, error) {
		return &independentOptionalAdapter{reloadTestAdapter: reloadTestAdapter{name: "test", identity: "test/id"}}, nil
	}
	initial, _ := build()
	r := NewReloadableAdapter(initial, build)
	if err := r.Start(); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	check := func() {
		if r.GetBotName() != "bot-name" || r.SupportsForward() || r.MaxMessagePayloadBytes() != 1234 || r.GeneratedImageOutputLimitBytes() != 5678 {
			t.Fatal("optional capabilities lost through wrapper")
		}
		if id, err := r.SendJoinIntro(1, "hello"); err != nil || id != 4321 {
			t.Fatal("join intro lost through wrapper")
		}
		if err := r.SetGroupAddRequest("flag", true, "reason"); err != nil {
			t.Fatal(err)
		}
		if err := r.SetFriendAddRequest("flag", true); err != nil {
			t.Fatal(err)
		}
	}
	check()
	commit, _, err := r.PrepareReload("test", "test/id", build)
	if err != nil {
		t.Fatal(err)
	}
	commit()
	check()
}
