package utils

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/lsp/concern_type"
)

func TestEmitQueueReevaluatesIntervalWhenSchedulingNextRound(t *testing.T) {
	events := make(chan *EmitE)
	q := NewEmitQueue(events, time.Millisecond)
	var rounds atomic.Int32
	q.IntervalProvider = func() time.Duration {
		if rounds.Add(1) == 1 {
			return time.Millisecond
		}
		return time.Hour
	}
	q.Add(NewEmitE(1, concern_type.Type("live")))
	q.Start()
	defer q.Stop()
	for i := 0; i < 2; i++ {
		select {
		case <-events:
		case <-time.After(time.Second):
			t.Fatal("queue did not emit")
		}
	}
	select {
	case <-events:
		t.Fatal("queue kept its original interval after configuration changed")
	case <-time.After(50 * time.Millisecond):
	}
}
