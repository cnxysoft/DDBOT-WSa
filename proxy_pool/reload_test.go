package proxy_pool

import (
	"sync/atomic"
	"testing"
	"time"
)

type leasePool struct {
	entered, release chan struct{}
	stops            atomic.Int32
}

func (p *leasePool) Get(Prefer) (IProxy, error) {
	close(p.entered)
	<-p.release
	return &mockProxy{"old"}, nil
}
func (*leasePool) Delete(string) bool { return false }
func (p *leasePool) Stop() error      { p.stops.Add(1); return nil }

func TestSwapDrainsAndStopsOnce(t *testing.T) {
	old := &leasePool{entered: make(chan struct{}), release: make(chan struct{})}
	Init(old)
	done := make(chan struct{})
	go func() { _, _ = Get(PreferAny); close(done) }()
	<-old.entered
	swapped := make(chan struct{})
	go func() { Swap(nil); close(swapped) }()
	select {
	case <-swapped:
		t.Fatal("old pool stopped while Get was active")
	case <-time.After(20 * time.Millisecond):
	}
	close(old.release)
	<-done
	<-swapped
	if old.stops.Load() != 1 {
		t.Fatal("pool was not stopped once")
	}
	_ = Stop()
	if old.stops.Load() != 1 {
		t.Fatal("retired pool stopped again")
	}
}
