package weibo

import (
	"context"
	"testing"
	"time"

	miraiConfig "github.com/Sora233/MiraiGo-Template/config"
	"github.com/cnxysoft/DDBOT-WSa/internal/test"
	"github.com/cnxysoft/DDBOT-WSa/lsp/concern"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeFreshTimer struct {
	c       chan time.Time
	resetCh chan time.Duration
}

func (t *fakeFreshTimer) Chan() <-chan time.Time {
	return t.c
}

func (t *fakeFreshTimer) Reset(d time.Duration) bool {
	t.resetCh <- d
	return true
}

func (t *fakeFreshTimer) Stop() bool {
	return true
}

func TestWeiboIntervalFreshUsesConfigInterval(t *testing.T) {
	resetConfig := useTestConfig(t)
	defer resetConfig()
	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)

	interval := 15 * time.Second
	miraiConfig.GlobalConfig.Set("weibo.interval", interval)

	fakeTimer := &fakeFreshTimer{
		c:       make(chan time.Time, 1),
		resetCh: make(chan time.Duration, 1),
	}
	oldTimerFactory := newFreshTimer
	newFreshTimer = func(d time.Duration) freshTimer {
		require.Equal(t, 3*time.Second, d)
		return fakeTimer
	}
	defer func() {
		newFreshTimer = oldTimerFactory
	}()

	c := NewConcern(nil)
	c.StateManager.FreshIndex(test.G1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.fresh()(ctx, make(chan concern.Event, 1))
	}()

	fakeTimer.c <- time.Now()

	select {
	case got := <-fakeTimer.resetCh:
		assert.Equal(t, interval, got)
	case <-time.After(time.Second):
		t.Fatal("fresh loop did not reset timer")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("fresh loop did not stop after context cancellation")
	}
}
