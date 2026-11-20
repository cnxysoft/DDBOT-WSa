package utils

import (
	"sync"
	"testing"
	"time"
)

func TestEmitQueueImmediateAndConcurrentStop(t *testing.T) {
	for i := 0; i < 100; i++ {
		out := make(chan *EmitE)
		queue := NewEmitQueue(out, time.Nanosecond)
		queue.Add(NewEmitE(1, "test"))
		queue.Start()
		queue.Start()
		var stopped sync.WaitGroup
		stopped.Add(2)
		for j := 0; j < 2; j++ {
			go func() { defer stopped.Done(); queue.Stop() }()
		}
		stopped.Wait()
		close(out)
	}
}
