package bot

import (
	"sync"
)

type EventHandle[T any] struct {
	mu       sync.RWMutex
	handlers []func(event T)
}

func (handle *EventHandle[T]) Subscribe(handler func(event T)) {
	handle.mu.Lock()
	defer handle.mu.Unlock()

	newHandlers := make([]func(event T), len(handle.handlers)+1)
	copy(newHandlers, handle.handlers)
	newHandlers[len(handle.handlers)] = handler
	handle.handlers = newHandlers
}

func (handle *EventHandle[T]) Dispatch(event T) {
	handle.mu.RLock()
	handlers := append([]func(event T){}, handle.handlers...)
	handle.mu.RUnlock()

	for _, handler := range handlers {
		handler(event)
	}
}
