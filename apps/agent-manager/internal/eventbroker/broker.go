package eventbroker

import "sync"

// Broker notifies subscribers that a session may have new durable events.
// Notifications are intentionally coalesced: subscribers must read the Event
// Store using their last observed sequence as the source of truth.
type Broker struct {
	mu          sync.Mutex
	nextID      uint64
	subscribers map[string]map[uint64]chan struct{}
}

func New() *Broker {
	return &Broker{subscribers: make(map[string]map[uint64]chan struct{})}
}

func (b *Broker) Publish(sessionID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, subscriber := range b.subscribers[sessionID] {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func (b *Broker) Subscribe(sessionID string) (<-chan struct{}, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.nextID++
	id := b.nextID
	subscriber := make(chan struct{}, 1)
	if b.subscribers[sessionID] == nil {
		b.subscribers[sessionID] = make(map[uint64]chan struct{})
	}
	b.subscribers[sessionID][id] = subscriber

	var once sync.Once
	return subscriber, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.subscribers[sessionID], id)
			if len(b.subscribers[sessionID]) == 0 {
				delete(b.subscribers, sessionID)
			}
		})
	}
}
