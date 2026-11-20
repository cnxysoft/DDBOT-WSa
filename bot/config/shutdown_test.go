package config

import (
	"os"
	"testing"
	"time"
)

func TestBeginShutdownDrainsSaveAndRejectsFuturePublications(t *testing.T) {
	c, path := testConfig(t, "value: old\n")
	persistEntered := make(chan struct{})
	releasePersist := make(chan struct{})
	saved := make(chan error, 1)
	committed := make(chan struct{}, 1)
	c.OnPrepareReload(func(previous, next *Snapshot) (func(), func(), error) {
		return func() { committed <- struct{}{} }, nil, nil
	})
	go func() {
		saved <- c.Save([]byte("value: accepted\n"), func() error {
			close(persistEntered)
			<-releasePersist
			return os.WriteFile(path, []byte("value: accepted\n"), 0600)
		})
	}()
	<-persistEntered
	shutdown := make(chan struct{})
	go func() { c.BeginShutdown(); close(shutdown) }()
	select {
	case <-shutdown:
		t.Fatal("shutdown passed an in-flight accepted transaction")
	case <-time.After(20 * time.Millisecond):
	}
	close(releasePersist)
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	select {
	case <-shutdown:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not drain the accepted transaction")
	}
	select {
	case <-committed:
	default:
		t.Fatal("shutdown returned before accepted resource commit")
	}
	if c.GetString("value") != "accepted" || c.Status().Revision != 2 {
		t.Fatal("accepted transaction not preserved during shutdown")
	}
	persisted := false
	if err := c.Save([]byte("value: rejected\n"), func() error { persisted = true; return nil }); err == nil || persisted {
		t.Fatal("new save persisted after shutdown")
	}
	if err := c.Reload(); err == nil {
		t.Fatal("reload accepted after shutdown")
	}
	c.Set("value", "late override")
	if c.GetString("value") != "accepted" || c.Status().Revision != 2 {
		t.Fatal("late publication changed drained generation")
	}
	c.BeginShutdown()
	c.WatchConfig()
	if c.Status().Watching {
		t.Fatal("watcher restarted after shutdown")
	}
}
