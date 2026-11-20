package msg_marker

import (
	"sync"
	"testing"
)

func TestMarkerModule(t *testing.T) {
	t.Run("ModuleInfo should return correct ID", func(t *testing.T) {
		info := instance.MiraiGoModule()
		if info.ID != moduleId {
			t.Errorf("Expected module ID %s, got %s", moduleId, info.ID)
		}
		if info.Instance == nil {
			t.Error("Instance should not be nil")
		}
	})

	t.Run("Init should not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Init panicked: %v", r)
			}
		}()
		instance.Init()
	})

	t.Run("PostInit should not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("PostInit panicked: %v", r)
			}
		}()
		instance.PostInit()
	})

	t.Run("Serve should not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Serve panicked: %v", r)
			}
		}()
		// Pass nil bot since Serve doesn't use it in adapter mode
		instance.Serve(nil)
	})

	t.Run("Start should not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Start panicked: %v", r)
			}
		}()
		instance.Start(nil)
	})

	t.Run("Stop should call wg.Done", func(t *testing.T) {
		wg := &sync.WaitGroup{}
		wg.Add(1)
		instance.Stop(nil, wg)
		// Wait should return immediately if Done was called
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()
		select {
		case <-done:
			// Success - WaitGroup was properly decremented
		case <-t.Context().Done():
			t.Error("Stop did not call wg.Done")
		}
	})
}
