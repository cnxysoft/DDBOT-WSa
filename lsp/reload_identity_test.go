package lsp

import "testing"

func TestIdentityReservationAbortsAndRejectsChangingBot(t *testing.T) {
	s := &spyIdentityStore{}
	commit, abort, err := prepareBotIdentity(s, "qqofficial/123")
	if err != nil || s.stored != "qqofficial/123" {
		t.Fatal(err, s.stored)
	}
	abort()
	abort()
	if s.stored != "" {
		t.Fatal("aborted binding left identity behind")
	}
	commit, abort, err = prepareBotIdentity(s, "qqofficial/123")
	if err != nil {
		t.Fatal(err)
	}
	commit()
	abort()
	if s.stored != "qqofficial/123" {
		t.Fatal("committed binding was rolled back")
	}
	if _, _, err := prepareBotIdentity(s, "qqofficial/456"); err == nil || s.stored != "qqofficial/123" {
		t.Fatal("cross-bot binding accepted")
	}
}
