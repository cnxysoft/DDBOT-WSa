package adapter

import (
	"errors"
	"fmt"
	"sync"
)

// AdapterBuilder constructs a fresh instance from an immutable configuration.
// It must not read a mutable global configuration during rollback.
type AdapterBuilder func() (Adapter, error)

type adapterGeneration struct {
	adapter Adapter
	calls   sync.WaitGroup
	events  reloadEvents
}

// ReloadableAdapter keeps Messenger and event subscriptions stable while a
// connection is rebuilt. Reloads briefly disconnect: new requests return
// ErrRequestNotSent until commit/rollback, while existing requests finish first.
// Changing protocol or identity requires a separate data migration and is refused.
type ReloadableAdapter struct {
	opMu      sync.Mutex
	mu        sync.Mutex
	current   *adapterGeneration
	build     AdapterBuilder
	started   bool
	pending   bool
	lastError error
	sink      MemberListSink

	handlersMu sync.RWMutex
	group      []func(*GroupMessageEvent)
	private    []func(*PrivateMessageEvent)
	meta       []func(*MetaEvent)
	notice     []func(*NoticeEvent)
	request    []func(*RequestEvent)
}

var _ Adapter = (*ReloadableAdapter)(nil)

func NewReloadableAdapter(initial Adapter, rebuild AdapterBuilder) *ReloadableAdapter {
	r := &ReloadableAdapter{build: rebuild}
	r.current = r.generation(initial)
	return r
}

func (r *ReloadableAdapter) generation(a Adapter) *adapterGeneration {
	g := &adapterGeneration{adapter: a}
	if a == nil {
		return g
	}
	a.OnGroupMessage(func(event *GroupMessageEvent) {
		g.events.emit(func() {
			r.handlersMu.RLock()
			handlers := append([]func(*GroupMessageEvent){}, r.group...)
			r.handlersMu.RUnlock()
			for _, handler := range handlers {
				handler(event)
			}
		})
	})
	a.OnPrivateMessage(func(event *PrivateMessageEvent) {
		g.events.emit(func() {
			r.handlersMu.RLock()
			handlers := append([]func(*PrivateMessageEvent){}, r.private...)
			r.handlersMu.RUnlock()
			for _, handler := range handlers {
				handler(event)
			}
		})
	})
	a.OnMetaEvent(func(event *MetaEvent) {
		g.events.emit(func() {
			r.handlersMu.RLock()
			handlers := append([]func(*MetaEvent){}, r.meta...)
			r.handlersMu.RUnlock()
			for _, handler := range handlers {
				handler(event)
			}
		})
	})
	a.OnNoticeEvent(func(event *NoticeEvent) {
		g.events.emit(func() {
			r.handlersMu.RLock()
			handlers := append([]func(*NoticeEvent){}, r.notice...)
			r.handlersMu.RUnlock()
			for _, handler := range handlers {
				handler(event)
			}
		})
	})
	a.OnRequestEvent(func(event *RequestEvent) {
		g.events.emit(func() {
			r.handlersMu.RLock()
			handlers := append([]func(*RequestEvent){}, r.request...)
			r.handlersMu.RUnlock()
			for _, handler := range handlers {
				handler(event)
			}
		})
	})
	if setter, ok := a.(MemberListSinkSetter); ok {
		setter.SetMemberListSink(reloadMemberSink{owner: r, generation: g})
	}
	return g
}

func (r *ReloadableAdapter) Start() error {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	r.mu.Lock()
	if r.started || r.current.adapter == nil {
		r.mu.Unlock()
		return fmt.Errorf("adapter is already started or unavailable")
	}
	r.pending = true
	g := r.current
	r.mu.Unlock()
	err := g.adapter.Start()
	r.mu.Lock()
	r.pending = false
	r.started = err == nil
	r.lastError = err
	r.mu.Unlock()
	if err == nil {
		g.events.activate()
	} else {
		g.events.disable()
	}
	return err
}

func (r *ReloadableAdapter) Stop() error {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	r.mu.Lock()
	if !r.started {
		r.lastError = nil
		r.mu.Unlock()
		return nil
	}
	r.pending, r.started = true, false
	g := r.current
	g.events.disable()
	r.mu.Unlock()
	g.calls.Wait()
	err := g.adapter.Stop()
	r.mu.Lock()
	r.pending = false
	r.mu.Unlock()
	return err
}

// PrepareReload pauses calls, flushes/stops the old instance, then starts the
// candidate. Invoke exactly one returned closure after persistence succeeds or
// fails. No Config mutation is performed by these closures. Both are idempotent.
// Start/Stop/event callbacks never run with the state mutex held.
func (r *ReloadableAdapter) PrepareReload(name, identity string, build AdapterBuilder) (func(), func(), error) {
	r.opMu.Lock()
	r.mu.Lock()
	old, oldBuild := r.current, r.build
	if (!r.started && r.lastError == nil) || old.adapter == nil || oldBuild == nil || build == nil {
		r.mu.Unlock()
		r.opMu.Unlock()
		return nil, nil, fmt.Errorf("adapter is not ready for live reload")
	}
	initialIdentity := adapterIdentity(old.adapter)
	initialBinding := !r.started && initialIdentity == "" && r.lastError != nil
	if name != old.adapter.GetAdapterName() || (identity != initialIdentity && !initialBinding) {
		r.mu.Unlock()
		r.opMu.Unlock()
		return nil, nil, fmt.Errorf("adapter protocol or bot identity cannot change during live reload; migrate its data separately")
	}
	r.pending = true
	old.events.disable()
	r.mu.Unlock()
	old.calls.Wait()
	if err := old.adapter.Stop(); err != nil {
		restoreErr := r.restore(oldBuild)
		r.opMu.Unlock()
		return nil, nil, errors.Join(fmt.Errorf("stop old adapter: %w", err), restoreErr)
	}
	newAdapter, err := build()
	var candidate *adapterGeneration
	if err == nil && newAdapter == nil {
		err = fmt.Errorf("adapter builder returned nil")
	}
	if err == nil && (newAdapter.GetAdapterName() != name || adapterIdentity(newAdapter) != identity) {
		err = fmt.Errorf("adapter candidate does not match its declared protocol or identity")
	}
	if err == nil {
		if adopter, ok := newAdapter.(interface{ AdoptReloadState(Adapter) error }); ok && !initialBinding {
			err = adopter.AdoptReloadState(old.adapter)
		}
	}
	if err == nil {
		candidate = r.generation(newAdapter)
		if ready, ok := newAdapter.(interface{ StartForReload() error }); ok {
			err = ready.StartForReload()
		} else {
			err = newAdapter.Start()
		}
	}
	if err == nil {
		if validator, ok := newAdapter.(interface{ ValidateReloadIdentity(Adapter) error }); ok {
			err = validator.ValidateReloadIdentity(old.adapter)
		}
	}
	if err != nil {
		if candidate != nil {
			candidate.events.disable()
		}
		if newAdapter != nil {
			_ = newAdapter.Stop()
		}
		restoreErr := r.restore(oldBuild)
		r.opMu.Unlock()
		return nil, nil, errors.Join(fmt.Errorf("start candidate adapter: %w", err), restoreErr)
	}
	var once sync.Once
	commit := func() {
		once.Do(func() {
			r.mu.Lock()
			r.current, r.build = candidate, build
			r.pending, r.started, r.lastError = false, true, nil
			r.mu.Unlock()
			r.opMu.Unlock()
			candidate.events.activate()
		})
	}
	abort := func() {
		once.Do(func() {
			candidate.events.disable()
			if err := newAdapter.Stop(); err != nil {
				adapterLogger.WithError(err).Error("stop aborted adapter candidate")
			}
			if err := r.restore(oldBuild); err != nil {
				adapterLogger.WithError(err).Error("adapter rollback failed; connection remains unavailable")
			}
			r.opMu.Unlock()
		})
	}
	return commit, abort, nil
}

func (r *ReloadableAdapter) restore(build AdapterBuilder) error {
	r.mu.Lock()
	previous := r.current.adapter
	r.mu.Unlock()
	a, err := build()
	var g *adapterGeneration
	if err == nil && a == nil {
		err = fmt.Errorf("adapter rollback builder returned nil")
	}
	if err == nil {
		if adopter, ok := a.(interface{ AdoptReloadState(Adapter) error }); ok {
			err = adopter.AdoptReloadState(previous)
		}
	}
	if err == nil {
		g = r.generation(a)
		if ready, ok := a.(interface{ StartForReload() error }); ok {
			err = ready.StartForReload()
		} else {
			err = a.Start()
		}
	}
	if err == nil {
		if validator, ok := a.(interface{ ValidateReloadIdentity(Adapter) error }); ok {
			err = validator.ValidateReloadIdentity(previous)
		}
	}
	if err != nil {
		if g != nil {
			g.events.disable()
		}
		if a != nil {
			_ = a.Stop()
		}
		err = fmt.Errorf("restore previous adapter: %w", err)
		r.mu.Lock()
		r.pending, r.started, r.lastError = false, false, err
		r.mu.Unlock()
		return err
	}
	r.mu.Lock()
	r.current, r.build = g, build
	r.pending, r.started, r.lastError = false, true, nil
	r.mu.Unlock()
	g.events.activate()
	return nil
}

// LastReloadError exposes a failed rollback instead of silently claiming that
// the previous connection was recovered.
func (r *ReloadableAdapter) LastReloadError() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastError
}

func (r *ReloadableAdapter) acquire() (Adapter, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending || !r.started || r.current.adapter == nil {
		return nil, nil, fmt.Errorf("%w: adapter is reconnecting or stopped", ErrRequestNotSent)
	}
	g := r.current
	g.calls.Add(1)
	return g.adapter, g.calls.Done, nil
}

func adapterIdentity(a Adapter) string {
	if provider, ok := a.(interface{ Identity() string }); ok {
		return provider.Identity()
	}
	return ""
}

// reloadEvents keeps user callbacks off the transport's read/Stop goroutine.
// Old-generation queued events are discarded after shutdown starts. A running
// command can finish independently, including one waiting on a config write.
type reloadEvents struct {
	mu    sync.Mutex
	state uint8 // 0 preparing, 1 active, 2 disabled
	queue []func()
	wake  chan struct{}
}

func (g *reloadEvents) emit(f func()) {
	g.mu.Lock()
	switch g.state {
	case 0, 1:
		g.queue = append(g.queue, f)
		if g.wake != nil {
			select {
			case g.wake <- struct{}{}:
			default:
			}
		}
		g.mu.Unlock()
	default:
		g.mu.Unlock()
	}
}

func (g *reloadEvents) activate() {
	g.mu.Lock()
	if g.state != 0 {
		g.mu.Unlock()
		return
	}
	g.state = 1
	g.wake = make(chan struct{}, 1)
	g.wake <- struct{}{}
	g.mu.Unlock()
	go func() {
		for {
			<-g.wake
			for {
				g.mu.Lock()
				if g.state == 2 {
					g.mu.Unlock()
					return
				}
				if len(g.queue) == 0 {
					g.mu.Unlock()
					break
				}
				f := g.queue[0]
				g.queue[0] = nil
				g.queue = g.queue[1:]
				g.mu.Unlock()
				dispatchReloadEvent(f)
			}
		}
	}()
}

func dispatchReloadEvent(f func()) {
	defer func() {
		if value := recover(); value != nil {
			adapterLogger.WithField("panic", value).Error("adapter event callback failed")
		}
	}()
	f()
}

func (g *reloadEvents) disable() {
	g.mu.Lock()
	g.state, g.queue = 2, nil
	if g.wake != nil {
		select {
		case g.wake <- struct{}{}:
		default:
		}
	}
	g.mu.Unlock()
}

type reloadMemberSink struct {
	owner      *ReloadableAdapter
	generation *adapterGeneration
}

func (s reloadMemberSink) InvalidateGroupMembers(groupID int64) {
	s.generation.events.mu.Lock()
	active := s.generation.events.state == 1
	s.generation.events.mu.Unlock()
	if !active {
		return
	}
	s.owner.mu.Lock()
	sink := s.owner.sink
	s.owner.mu.Unlock()
	if sink != nil {
		sink.InvalidateGroupMembers(groupID)
	}
}

func (r *ReloadableAdapter) SetMemberListSink(sink MemberListSink) {
	r.mu.Lock()
	r.sink = sink
	r.mu.Unlock()
}

func (r *ReloadableAdapter) Identity() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return adapterIdentity(r.current.adapter)
}

func (r *ReloadableAdapter) OnGroupMessage(handler func(*GroupMessageEvent)) {
	r.handlersMu.Lock()
	defer r.handlersMu.Unlock()
	r.group = append(r.group, handler)
}
func (r *ReloadableAdapter) OnPrivateMessage(handler func(*PrivateMessageEvent)) {
	r.handlersMu.Lock()
	defer r.handlersMu.Unlock()
	r.private = append(r.private, handler)
}
func (r *ReloadableAdapter) OnMetaEvent(handler func(*MetaEvent)) {
	r.handlersMu.Lock()
	defer r.handlersMu.Unlock()
	r.meta = append(r.meta, handler)
}
func (r *ReloadableAdapter) OnNoticeEvent(handler func(*NoticeEvent)) {
	r.handlersMu.Lock()
	defer r.handlersMu.Unlock()
	r.notice = append(r.notice, handler)
}
func (r *ReloadableAdapter) OnRequestEvent(handler func(*RequestEvent)) {
	r.handlersMu.Lock()
	defer r.handlersMu.Unlock()
	r.request = append(r.request, handler)
}
