package eventbus

import "sync"

type Bus struct {
	subscribers map[string][]chan interface{}
	mu          sync.RWMutex
}

var BusObj = New()

func New() *Bus {
	return &Bus{subscribers: make(map[string][]chan interface{})}
}

func (b *Bus) Subscribe(topic string) <-chan interface{} {
	ch := make(chan interface{}, 1)
	b.mu.Lock()
	b.subscribers[topic] = append(b.subscribers[topic], ch)
	b.mu.Unlock()
	return ch
}

func (b *Bus) Publish(topic string, msg interface{}) {
	b.mu.RLock()
	for _, ch := range b.subscribers[topic] {
		select {
		case ch <- msg:
		default:
		}
	}
	b.mu.RUnlock()
}
