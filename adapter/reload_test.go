package adapter

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type reloadTestAdapter struct {
	Adapter
	mu               sync.Mutex
	name, identity   string
	version          int
	group            func(*GroupMessageEvent)
	meta             func(*MetaEvent)
	startErr         error
	stopped          atomic.Bool
	stopHook         func()
	entered, release chan struct{}
}

func (a *reloadTestAdapter) Start() error {
	if a.startErr != nil {
		return a.startErr
	}
	a.mu.Lock()
	h := a.meta
	a.mu.Unlock()
	if h != nil {
		h(&MetaEvent{MetaEventType: "lifecycle", SelfID: int64(a.version)})
	}
	return nil
}
func (a *reloadTestAdapter) Stop() error {
	a.stopped.Store(true)
	if a.stopHook != nil {
		a.stopHook()
	}
	return nil
}
func (a *reloadTestAdapter) GetAdapterName() string { return a.name }
func (a *reloadTestAdapter) Identity() string       { return a.identity }
func (a *reloadTestAdapter) IsConnected() bool      { return !a.stopped.Load() }
func (a *reloadTestAdapter) GetSelfID() int64       { return int64(a.version) }
func (a *reloadTestAdapter) OnGroupMessage(h func(*GroupMessageEvent)) {
	a.mu.Lock()
	a.group = h
	a.mu.Unlock()
}
func (a *reloadTestAdapter) OnPrivateMessage(func(*PrivateMessageEvent)) {}
func (a *reloadTestAdapter) OnMetaEvent(h func(*MetaEvent))              { a.mu.Lock(); a.meta = h; a.mu.Unlock() }
func (a *reloadTestAdapter) OnNoticeEvent(func(*NoticeEvent))            {}
func (a *reloadTestAdapter) OnRequestEvent(func(*RequestEvent))          {}
func (a *reloadTestAdapter) SendApi(string, map[string]interface{}) (interface{}, error) {
	if a.entered != nil {
		close(a.entered)
		<-a.release
	}
	return a.version, nil
}
func (a *reloadTestAdapter) emit(version int64) {
	a.mu.Lock()
	h := a.group
	a.mu.Unlock()
	if h != nil {
		h(&GroupMessageEvent{MessageID: version})
	}
}

func TestAdapterReloadCommitPreservesSubscriptions(t *testing.T) {
	old := &reloadTestAdapter{name: "test", identity: "test/1", version: 1}
	r := NewReloadableAdapter(old, func() (Adapter, error) { return &reloadTestAdapter{name: "test", identity: "test/1", version: 1}, nil })
	metas, messages := make(chan int64, 10), make(chan int64, 10)
	r.OnMetaEvent(func(e *MetaEvent) { metas <- e.SelfID; _, _ = r.SendApi("callback", nil) })
	r.OnGroupMessage(func(e *GroupMessageEvent) { messages <- e.MessageID })
	require.NoError(t, r.Start())
	require.Equal(t, int64(1), <-metas)
	old.stopHook = func() {
		_, err := r.SendApi("stop-callback", nil)
		if !errors.Is(err, ErrRequestNotSent) {
			panic(err)
		}
	}
	next := &reloadTestAdapter{name: "test", identity: "test/1", version: 2}
	commit, abort, err := r.PrepareReload("test", "test/1", func() (Adapter, error) { return next, nil })
	require.NoError(t, err)
	_, err = r.SendApi("pending", nil)
	require.ErrorIs(t, err, ErrRequestNotSent)
	old.emit(100)
	next.emit(200)
	select {
	case <-messages:
		t.Fatal("candidate event escaped before commit")
	default:
	}
	commit()
	commit()
	abort()
	require.Equal(t, int64(2), <-metas)
	require.Equal(t, int64(200), <-messages)
	require.Equal(t, int64(2), r.GetSelfID())
	require.True(t, old.stopped.Load())
	require.False(t, next.stopped.Load())
	require.NoError(t, r.Stop())
	require.NoError(t, r.Stop())
}

func TestAdapterReloadAbortAndFailedCandidateRestoreOld(t *testing.T) {
	for _, failStart := range []bool{false, true} {
		t.Run(fmt.Sprint(failStart), func(t *testing.T) {
			old := &reloadTestAdapter{name: "test", version: 1}
			var restored *reloadTestAdapter
			r := NewReloadableAdapter(old, func() (Adapter, error) { restored = &reloadTestAdapter{name: "test", version: 1}; return restored, nil })
			require.NoError(t, r.Start())
			next := &reloadTestAdapter{name: "test", version: 2}
			if failStart {
				next.startErr = errors.New("bad token")
			}
			_, abort, err := r.PrepareReload("test", "", func() (Adapter, error) { return next, nil })
			if failStart {
				require.ErrorContains(t, err, "bad token")
			} else {
				require.NoError(t, err)
				abort()
				abort()
			}
			value, err := r.SendApi("version", nil)
			require.NoError(t, err)
			require.Equal(t, 1, value)
			require.True(t, next.stopped.Load())
			require.NotNil(t, restored)
			require.True(t, r.IsConnected())
			require.NoError(t, r.Stop())
		})
	}
}

func TestAdapterReloadDrainsCallsAndRefusesIdentityChange(t *testing.T) {
	old := &reloadTestAdapter{name: "test", identity: "test/1", version: 1, entered: make(chan struct{}), release: make(chan struct{})}
	r := NewReloadableAdapter(old, func() (Adapter, error) { return &reloadTestAdapter{name: "test", identity: "test/1", version: 1}, nil })
	require.NoError(t, r.Start())
	built := false
	_, _, err := r.PrepareReload("test", "test/2", func() (Adapter, error) { built = true; return nil, nil })
	require.Error(t, err)
	require.False(t, built)
	require.False(t, old.stopped.Load())
	apiDone := make(chan struct{})
	go func() { defer close(apiDone); _, _ = r.SendApi("block", nil) }()
	<-old.entered
	prepared := make(chan func(), 1)
	go func() {
		commit, _, err := r.PrepareReload("test", "test/1", func() (Adapter, error) { return &reloadTestAdapter{name: "test", identity: "test/1", version: 2}, nil })
		if err != nil {
			panic(err)
		}
		prepared <- commit
	}()
	require.Eventually(t, func() bool { return !r.IsConnected() }, time.Second, time.Millisecond)
	require.False(t, old.stopped.Load(), "old connection stopped before in-flight request finished")
	close(old.release)
	<-apiDone
	select {
	case commit := <-prepared:
		commit()
	case <-time.After(time.Second):
		t.Fatal("reload deadlocked")
	}
	require.True(t, old.stopped.Load())
	require.NoError(t, r.Stop())
}

func TestAdapterReloadRollbackFailureIsObservable(t *testing.T) {
	old := &reloadTestAdapter{name: "test", version: 1}
	r := NewReloadableAdapter(old, func() (Adapter, error) { return nil, errors.New("previous connection unavailable") })
	require.NoError(t, r.Start())
	_, abort, err := r.PrepareReload("test", "", func() (Adapter, error) { return &reloadTestAdapter{name: "test", version: 2}, nil })
	require.NoError(t, err)
	abort()
	require.ErrorContains(t, r.LastReloadError(), "previous connection unavailable")
	require.False(t, r.IsConnected())
	_, err = r.SendApi("version", nil)
	require.ErrorIs(t, err, ErrRequestNotSent)
	// A later valid save can recover even when both the candidate and rollback
	// were previously unavailable. The user need not restart the process.
	commit, _, err := r.PrepareReload("test", "", func() (Adapter, error) {
		return &reloadTestAdapter{name: "test", version: 3}, nil
	})
	require.NoError(t, err)
	commit()
	require.True(t, r.IsConnected())
	require.NoError(t, r.LastReloadError())
	require.Equal(t, int64(3), r.GetSelfID())
	require.NoError(t, r.Stop())
}

func TestAdapterReloadDeferredFirstBinding(t *testing.T) {
	initial := &reloadTestAdapter{name: "qqofficial", startErr: errors.New("missing credentials")}
	r := NewReloadableAdapter(initial, func() (Adapter, error) {
		return &reloadTestAdapter{name: "qqofficial", startErr: errors.New("missing credentials")}, nil
	})
	require.Error(t, r.Start())
	require.Error(t, r.LastReloadError())
	commit, _, err := r.PrepareReload("qqofficial", "qqofficial/first-app", func() (Adapter, error) {
		return &reloadTestAdapter{name: "qqofficial", identity: "qqofficial/first-app", version: 1}, nil
	})
	require.NoError(t, err)
	commit()
	require.True(t, r.IsConnected())
	require.Equal(t, "qqofficial/first-app", r.Identity())
	_, _, err = r.PrepareReload("qqofficial", "qqofficial/other-app", func() (Adapter, error) {
		return &reloadTestAdapter{name: "qqofficial", identity: "qqofficial/other-app", version: 2}, nil
	})
	require.Error(t, err)
	require.NoError(t, r.Stop())
}

func TestAdapterReloadEventCallbackPanicDoesNotStopDispatch(t *testing.T) {
	initial := &reloadTestAdapter{name: "test", version: 1}
	r := NewReloadableAdapter(initial, nil)
	called := make(chan int64, 1)
	r.OnGroupMessage(func(event *GroupMessageEvent) {
		if event.MessageID == 1 {
			panic("callback failure")
		}
		called <- event.MessageID
	})
	require.NoError(t, r.Start())
	defer r.Stop()
	initial.emit(1)
	initial.emit(2)
	select {
	case id := <-called:
		require.Equal(t, int64(2), id)
	case <-time.After(time.Second):
		t.Fatal("event dispatch stopped after handler panic")
	}
}
