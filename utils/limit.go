package utils

import (
	"context"
	"sync"
)

// AdjustableLimit retains the identity of in-flight permits when its capacity changes.
type AdjustableLimit struct {
	mu             sync.Mutex
	capacity, used int64
	changed        chan struct{}
}

func NewAdjustableLimit(capacity int64) *AdjustableLimit {
	l := &AdjustableLimit{changed: make(chan struct{})}
	l.Resize(capacity)
	return l
}

func (l *AdjustableLimit) Resize(capacity int64) {
	if capacity < 1 {
		capacity = 1
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.capacity = capacity
	close(l.changed)
	l.changed = make(chan struct{})
}

func (l *AdjustableLimit) Acquire(ctx context.Context, n int64) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		l.mu.Lock()
		if n > 0 && l.used+n <= l.capacity {
			l.used += n
			l.mu.Unlock()
			return nil
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (l *AdjustableLimit) Release(n int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n < 1 || n > l.used {
		panic("invalid notification permit release")
	}
	l.used -= n
	close(l.changed)
	l.changed = make(chan struct{})
}
