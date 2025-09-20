package acfun

import "testing"

func TestExtraKeySet(t *testing.T) {
	var e ExtraKey
	e.LiveInfoKey()
	e.NotLiveKey()
	e.UserInfoKey()
	e.UidFirstTimestamp()
}
