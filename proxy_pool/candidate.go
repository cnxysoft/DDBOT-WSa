package proxy_pool

import "sync"

// PrepareSwap owns an unpublished pool. During preparation the getter only uses
// this candidate, including an explicit direct connection when candidate is nil.
// Commit publishes it and promotes the getter to follow future global changes.
// Abort invalidates the getter and drains reads before stopping the candidate.
// Commit and abort are mutually exclusive and idempotent.
func PrepareSwap(candidate IProxyPool) (getter Getter, commit func(), abort func()) {
	var mu sync.RWMutex
	var state uint8 // 0 prepared, 1 committed, 2 aborted
	getter = func(prefer Prefer) (IProxy, error) {
		mu.RLock()
		defer mu.RUnlock()
		if state == 2 {
			return nil, ErrCandidateAborted
		}
		if prefer == PreferNone {
			return nil, ErrNil
		}
		if state == 1 {
			return Get(prefer)
		}
		if candidate == nil {
			return nil, ErrNil
		}
		return candidate.Get(prefer)
	}
	commit = func() {
		mu.Lock()
		defer mu.Unlock()
		if state != 0 {
			return
		}
		Swap(candidate)
		candidate, state = nil, 1
	}
	abort = func() {
		mu.Lock()
		defer mu.Unlock()
		if state != 0 {
			return
		}
		state = 2
		if candidate != nil {
			_ = candidate.Stop()
			candidate = nil
		}
	}
	return getter, commit, abort
}
