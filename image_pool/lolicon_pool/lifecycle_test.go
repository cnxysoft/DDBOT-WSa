package lolicon_pool

import (
	"context"
	"testing"
	"time"

	"github.com/cnxysoft/DDBOT-WSa/internal/test"
)

func TestPreparedAbortAndCloseCancelsBackgroundRequest(t *testing.T) {
	prepared, err := PrepareLoliconPool(&Config{CacheMin: 1, CacheMax: 2})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.started {
		t.Fatal("prepared candidate started workers")
	}
	if err = prepared.Close(); err != nil {
		t.Fatal(err)
	}
	prepared.Start()
	if prepared.started {
		t.Fatal("aborted candidate restarted")
	}

	test.InitBuntdb(t)
	defer test.CloseBuntdb(t)
	p, err := PrepareLoliconPool(&Config{CacheMin: 1, CacheMax: 2})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	p.fetch = func(ctx context.Context, _ string, _ R18Type, _ string, _ int) (*Response, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	p.Start()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("cache worker did not start")
	}
	done := make(chan struct{})
	go func() { _ = p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not cancel active remote request")
	}
	if err = p.Close(); err != nil {
		t.Fatal(err)
	}
}
