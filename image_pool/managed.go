package image_pool

import (
	"errors"
	"sync"
)

// ManagedPool keeps a stable reference and drains old Get operations on replacement.
type ManagedPool struct {
	mu   sync.RWMutex
	pool Pool
}

func NewManagedPool(pool Pool) *ManagedPool { return &ManagedPool{pool: pool} }

func (p *ManagedPool) Get(opts ...OptionFunc) ([]Image, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.pool == nil {
		return nil, errors.New("image pool disabled")
	}
	return p.pool.Get(opts...)
}

func (p *ManagedPool) Enabled() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pool != nil
}

// Swap starts a prepared pool only after the old background workers have stopped.
func (p *ManagedPool) Swap(next Pool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pool == next {
		return
	}
	ClosePool(p.pool)
	p.pool = next
	if starter, ok := next.(interface{ Start() }); ok {
		starter.Start()
	}
}

func (p *ManagedPool) Close() error { p.Swap(nil); return nil }

func ClosePool(pool Pool) {
	if closer, ok := pool.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
