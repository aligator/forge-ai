package agent

import "sync"

type Broadcaster struct {
	mu          sync.Mutex
	subscribers map[chan OutputChunk]struct{}
}

func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subscribers: map[chan OutputChunk]struct{}{}}
}

func (b *Broadcaster) Subscribe(buffer int) (<-chan OutputChunk, func()) {
	ch := make(chan OutputChunk, buffer)
	b.mu.Lock()
	b.subscribers[ch] = struct{}{}
	b.mu.Unlock()
	unsubscribe := func() {
		b.mu.Lock()
		if _, ok := b.subscribers[ch]; ok {
			delete(b.subscribers, ch)
			close(ch)
		}
		b.mu.Unlock()
	}
	return ch, unsubscribe
}

func (b *Broadcaster) WriteOutput(chunk OutputChunk) error {
	b.mu.Lock()
	subscribers := make([]chan OutputChunk, 0, len(b.subscribers))
	for subscriber := range b.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	b.mu.Unlock()
	for _, subscriber := range subscribers {
		select {
		case subscriber <- chunk:
		default:
		}
	}
	return nil
}
