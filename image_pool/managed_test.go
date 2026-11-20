package image_pool

import (
	"sync/atomic"
	"testing"
	"time"
)

type blockingPool struct {
	entered, release chan struct{}
	closed           atomic.Bool
}

func (p *blockingPool) Get(...OptionFunc) ([]Image, error) {
	close(p.entered)
	<-p.release
	return nil, nil
}
func (p *blockingPool) Close() error { p.closed.Store(true); return nil }

func TestManagedPoolDrainsInFlightGet(t *testing.T) {
	old := &blockingPool{entered: make(chan struct{}), release: make(chan struct{})}
	p := NewManagedPool(old)
	done := make(chan struct{})
	go func() { _, _ = p.Get(); close(done) }()
	<-old.entered
	swapped := make(chan struct{})
	go func() { p.Swap(nil); close(swapped) }()
	select {
	case <-swapped:
		t.Fatal("retired an in-flight pool")
	case <-time.After(20 * time.Millisecond):
	}
	close(old.release)
	<-done
	<-swapped
	if !old.closed.Load() || p.Enabled() {
		t.Fatal("pool did not retire")
	}
	if _, err := p.Get(); err == nil {
		t.Fatal("disabled pool accepted Get")
	}
}
