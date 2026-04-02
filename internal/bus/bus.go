// Package bus provides a lightweight typed event bus for inter-agent
// communication and TUI updates. Uses Go channels for fan-out delivery.
package bus

import (
	"sync"
	"time"
)

// Event is the envelope for all messages flowing through the bus.
type Event struct {
	Topic     string // e.g. "agent.claude", "task.T123", "system"
	Source    string // who published (agent name, "orchestrator", "user")
	Payload   any    // typed payload (AgentEvent, TaskUpdate, string, etc.)
	Timestamp time.Time
}

// subscriber holds a channel and its buffer size for one subscription.
type subscriber struct {
	ch     chan Event
	topics map[string]bool // topics this subscriber cares about, nil = wildcard
}

// Bus is a typed pub/sub event bus. Publishers send events to topics;
// subscribers receive them on buffered channels. Non-blocking delivery
// ensures a slow subscriber never blocks publishers.
type Bus struct {
	subs   []*subscriber
	mu     sync.RWMutex
	closed bool
}

// New creates an empty event bus.
func New() *Bus {
	return &Bus{}
}

// Subscribe creates a subscription for the given topics and returns a
// receive-only channel. Pass "*" or no topics for a wildcard subscription
// that receives all events. Buffer size controls how many events can queue
// before drops occur.
func (b *Bus) Subscribe(bufSize int, topics ...string) <-chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	if bufSize <= 0 {
		bufSize = 256
	}

	sub := &subscriber{
		ch: make(chan Event, bufSize),
	}

	// Build topic set. Empty or "*" means wildcard.
	if len(topics) > 0 && !(len(topics) == 1 && topics[0] == "*") {
		sub.topics = make(map[string]bool, len(topics))
		for _, t := range topics {
			sub.topics[t] = true
		}
	}

	b.subs = append(b.subs, sub)
	return sub.ch
}

// Unsubscribe removes a subscription by its channel. The channel is closed.
func (b *Bus) Unsubscribe(ch <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i, sub := range b.subs {
		if sub.ch == ch {
			close(sub.ch)
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			return
		}
	}
}

// Publish sends an event to all matching subscribers. Delivery is
// non-blocking — if a subscriber's buffer is full, the event is dropped
// for that subscriber.
func (b *Bus) Publish(topic, source string, payload any) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	event := Event{
		Topic:     topic,
		Source:    source,
		Payload:   payload,
		Timestamp: time.Now(),
	}

	for _, sub := range b.subs {
		if !sub.matches(topic) {
			continue
		}
		// Non-blocking send — drop if buffer full.
		select {
		case sub.ch <- event:
		default:
		}
	}
}

// Close shuts down the bus and closes all subscriber channels.
func (b *Bus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}
	b.closed = true

	for _, sub := range b.subs {
		close(sub.ch)
	}
	b.subs = nil
}

// matches returns true if the subscriber should receive events for the topic.
// A nil topic set means wildcard (receives everything).
func (s *subscriber) matches(topic string) bool {
	if s.topics == nil {
		return true // wildcard
	}
	return s.topics[topic]
}
