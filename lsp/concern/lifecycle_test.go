package concern

import (
	"context"
	"testing"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
)

func TestStateManagerImmediateStopWaitsForRegisteredWorkers(t *testing.T) {
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	for i := 0; i < 100; i++ {
		sm := NewStateManagerWithInt64ID("lifecycle-test", nil)
		finished := make(chan struct{})
		sm.UseFreshFunc(func(ctx context.Context, events chan<- Event) {
			defer close(finished)
			<-ctx.Done()
		})
		sm.UseNotifyGeneratorFunc(func(int64, Event) []Notify { return nil })
		if err := sm.Start(); err != nil {
			t.Fatal(err)
		}
		sm.Stop()
		select {
		case <-finished:
		default:
			t.Fatal("Stop returned before its fresh worker exited")
		}
	}
}
