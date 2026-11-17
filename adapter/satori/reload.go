package satori

import (
	"fmt"
	"maps"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

// AdoptReloadState is called after Stop has drained the old read loop.
func (a *SatoriAdapter) AdoptReloadState(previous adapter.Adapter) error {
	old, ok := previous.(*SatoriAdapter)
	if !ok {
		return fmt.Errorf("satori: incompatible previous adapter")
	}
	old.mu.RLock()
	defer old.mu.RUnlock()
	a.idMap = maps.Clone(old.idMap)
	a.directChannelMap = maps.Clone(old.directChannelMap)
	a.messageChannelMap = maps.Clone(old.messageChannelMap)
	a.msgIDMap = maps.Clone(old.msgIDMap)
	a.groupChannelMap = maps.Clone(old.groupChannelMap)
	return nil
}

// ValidateReloadIdentity refuses a credential/URL change that logs into a
// different account, whose group/user IDs could otherwise reuse old permissions.
func (a *SatoriAdapter) ValidateReloadIdentity(previous adapter.Adapter) error {
	old, ok := previous.(*SatoriAdapter)
	if !ok {
		return fmt.Errorf("satori: incompatible previous adapter")
	}
	old.mu.RLock()
	platform, login := old.platform, old.loginUserID
	old.mu.RUnlock()
	a.mu.RLock()
	defer a.mu.RUnlock()
	if login != "" && (platform != a.platform || login != a.loginUserID) {
		return fmt.Errorf("satori: live reload cannot switch bot identity")
	}
	return nil
}
