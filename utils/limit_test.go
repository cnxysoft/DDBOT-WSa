package utils

import (
	"context"
	"testing"
	"time"
)

func TestAdjustableLimitRetainsPermitsAcrossResize(t *testing.T) {
	l := NewAdjustableLimit(2)
	if err := l.Acquire(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	l.Resize(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if l.Acquire(ctx, 1) == nil {
		t.Fatal("decrease admitted work above capacity")
	}
	l.Release(1)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel2()
	if l.Acquire(ctx2, 1) == nil {
		t.Fatal("in-flight task was lost after resize")
	}
	waiter := make(chan error, 1)
	go func() { waiter <- l.Acquire(context.Background(), 1) }()
	l.Resize(2)
	select {
	case err := <-waiter:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("increase did not wake waiter")
	}
	l.Release(2)
}
