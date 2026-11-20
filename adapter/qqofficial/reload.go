package qqofficial

import (
	"fmt"

	"github.com/cnxysoft/DDBOT-WSa/adapter"
)

// AdoptReloadState runs after the old instance has stopped and all API requests
// drained. Same-identity reconnections preserve IDs, cached messages, passive
// reply budgets and dedup state instead of rebuilding them from a partial file.
func (a *QQOfficialAdapter) AdoptReloadState(previous adapter.Adapter) error {
	old, ok := previous.(*QQOfficialAdapter)
	if !ok || old.Identity() != a.Identity() {
		return fmt.Errorf("qqofficial: refusing to reuse state across bot identities")
	}
	if old.store.Path() != a.store.Path() {
		return fmt.Errorf("qqofficial: state-file cannot change during live reload")
	}
	a.ids, a.store = old.ids, old.store
	a.msgs, a.passive, a.introAnchors, a.dedup = old.msgs, old.passive, old.introAnchors, old.dedup
	a.groups, a.friends = old.groups, old.friends
	a.groups.mu.Lock()
	a.groups.onNew = a.fetchGroupInfoAsync
	a.groups.mu.Unlock()
	a.parser = &eventParser{appID: a.cfg.AppID, ids: a.ids, msgs: a.msgs, self: a.GetSelfID, isSelf: a.isSelfOpenID}
	a.selfID.Store(old.selfID.Load())
	old.selfMu.Lock()
	for alias := range old.selfAliases {
		a.selfAliases[alias] = struct{}{}
	}
	for group, resolved := range old.selfResolved {
		a.selfResolved[group] = resolved
	}
	old.selfMu.Unlock()
	// seqTrackerFor is serialized by sync.Once; both clients share the ledger.
	a.client.seqs = old.client.seqTrackerFor()
	a.client.seqOnce.Do(func() {})
	return nil
}
