package proxy_pool

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type candidateTestPool struct {
	address string
	stops   atomic.Int32
}

func (p *candidateTestPool) Get(Prefer) (IProxy, error) { return &mockProxy{p.address}, nil }
func (*candidateTestPool) Delete(string) bool           { return false }
func (p *candidateTestPool) Stop() error                { p.stops.Add(1); return nil }

func requireProxy(t *testing.T, getter Getter, want string) {
	t.Helper()
	p, err := getter(PreferOversea)
	if err != nil || p == nil || p.ProxyString() != want {
		t.Fatalf("proxy=%v err=%v, want %s", p, err, want)
	}
}

func TestCandidateIsolationPromotionAndIdempotence(t *testing.T) {
	defer Swap(nil)
	old := &candidateTestPool{address: "old"}
	candidate := &candidateTestPool{address: "candidate"}
	Swap(old)
	getter, commit, abort := PrepareSwap(candidate)
	requireProxy(t, Get, "old")
	requireProxy(t, getter, "candidate")
	commit()
	commit()
	abort()
	requireProxy(t, Get, "candidate")
	if old.stops.Load() != 1 || candidate.stops.Load() != 0 {
		t.Fatal("commit did not transfer ownership once")
	}
	Swap(&candidateTestPool{address: "future"})
	requireProxy(t, getter, "future")
	if candidate.stops.Load() != 1 {
		t.Fatal("candidate stayed pinned after global replacement")
	}
	_, offCommit, offAbort := PrepareSwap(nil)
	offCommit()
	offAbort()
	if p, err := getter(PreferOversea); p != nil || !errors.Is(err, ErrNil) {
		t.Fatal("promoted getter ignored later off", p, err)
	}
}

func TestCandidateOffAndAbortNeverFallBackToGlobal(t *testing.T) {
	defer Swap(nil)
	old := &candidateTestPool{address: "old"}
	Swap(old)
	getter, commit, abort := PrepareSwap(nil)
	if p, err := getter(PreferOversea); p != nil || !errors.Is(err, ErrNil) {
		t.Fatal("off candidate used old global", p, err)
	}
	abort()
	abort()
	commit()
	for _, prefer := range []Prefer{PreferOversea, PreferNone} {
		if p, err := getter(prefer); p != nil || !errors.Is(err, ErrCandidateAborted) {
			t.Fatal("aborted candidate reused", p, err)
		}
	}
	requireProxy(t, Get, "old")
	if old.stops.Load() != 0 {
		t.Fatal("abort stopped global")
	}
}

func TestCandidateAbortDrainsGetAndStopsOnce(t *testing.T) {
	pool := &leasePool{entered: make(chan struct{}), release: make(chan struct{})}
	getter, commit, abort := PrepareSwap(pool)
	done := make(chan struct{})
	go func() { _, _ = getter(PreferAny); close(done) }()
	<-pool.entered
	aborted := make(chan struct{})
	go func() { abort(); close(aborted) }()
	select {
	case <-aborted:
		t.Fatal("abort stopped a pool while Get was active")
	case <-time.After(20 * time.Millisecond):
	}
	close(pool.release)
	<-done
	<-aborted
	abort()
	commit()
	if pool.stops.Load() != 1 {
		t.Fatal("candidate stop count", pool.stops.Load())
	}
}

func TestCandidateConcurrentGetCommitAbort(t *testing.T) {
	defer Swap(nil)
	for range 30 {
		Swap(&candidateTestPool{address: "old"})
		candidate := &candidateTestPool{address: "candidate"}
		getter, commit, abort := PrepareSwap(candidate)
		var wg sync.WaitGroup
		for range 12 {
			wg.Go(func() {
				for range 30 {
					_, err := getter(PreferAny)
					if err != nil && !errors.Is(err, ErrCandidateAborted) {
						t.Error(err)
					}
				}
			})
		}
		wg.Go(commit)
		wg.Go(abort)
		wg.Go(commit)
		wg.Go(abort)
		wg.Wait()
		if _, err := getter(PreferAny); errors.Is(err, ErrCandidateAborted) {
			requireProxy(t, Get, "old")
		} else {
			requireProxy(t, Get, "candidate")
		}
		Swap(nil)
		if candidate.stops.Load() != 1 {
			t.Fatal("candidate was leaked or stopped twice", candidate.stops.Load())
		}
	}
}
