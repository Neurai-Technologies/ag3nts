// Package bus provides a lightweight typed event bus for inter-agent
// communication and TUI updates. Uses Go channels for fan-out delivery.
package bus

import (
	"sync"
	"sync/atomic"
	"time"
)

const ringSize = 512

// Event is the envelope for all messages flowing through the bus.
type Event struct {
	Topic     string    // e.g. "agent.claude", "task.T123", "system"
	Source    string    // who published (agent name, "orchestrator", "user")
	Payload   any       // typed payload (AgentEvent, TaskUpdate, string, etc.)
	Timestamp time.Time
	Seq       uint64    // monotonic sequence number assigned by Publish
}

// subscriber holds a channel and its buffer size for one subscription.
type subscriber struct {
	ch     chan Event
	topics map[string]bool // topics this subscriber cares about, nil = wildcard
}

// Bus is a typed pub/sub event bus. Publishers send events to topics;
// subscribers receive them on buffered channels. Non-blocking delivery
// ensures a slow subscriber never blocks publishers.
//
// A ring buffer of the last 512 events enables late subscribers to replay
// history via SubscribeWithReplay. Each event carries a monotonic Seq number
// for ordering and gap detection.
type Bus struct {
	subs    []*subscriber
	mu      sync.RWMutex
	closed  bool
	ring    [ringSize]Event // circular replay buffer
	ringLen int             // number of events stored (max ringSize)
	ringIdx int             // next write position in ring
	seq     uint64          // monotonic counter (atomic)
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
		Seq:       atomic.AddUint64(&b.seq, 1),
	}

	// Store in replay ring buffer (under write lock, promoted below).
	b.mu.RUnlock()
	b.mu.Lock()
	b.ring[b.ringIdx] = event
	b.ringIdx = (b.ringIdx + 1) % ringSize
	if b.ringLen < ringSize {
		b.ringLen++
	}
	b.mu.Unlock()
	b.mu.RLock()

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

// SubscribeWithReplay creates a subscription and immediately replays buffered
// events with Seq > afterSeq that match the topic filter. The replayed events
// are sent on the returned channel before any live events, ensuring the
// subscriber sees a complete history without gaps.
func (b *Bus) SubscribeWithReplay(bufSize int, afterSeq uint64, topics ...string) <-chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()

	if bufSize <= 0 {
		bufSize = 256
	}

	sub := &subscriber{
		ch: make(chan Event, bufSize),
	}

	// Build topic set.
	if len(topics) > 0 && !(len(topics) == 1 && topics[0] == "*") {
		sub.topics = make(map[string]bool, len(topics))
		for _, t := range topics {
			sub.topics[t] = true
		}
	}

	// Replay buffered events with Seq > afterSeq.
	if b.ringLen > 0 {
		start := b.ringIdx - b.ringLen
		if start < 0 {
			start += ringSize
		}
		for i := 0; i < b.ringLen; i++ {
			idx := (start + i) % ringSize
			event := b.ring[idx]
			if event.Seq <= afterSeq {
				continue
			}
			if !sub.matches(event.Topic) {
				continue
			}
			// Non-blocking send for replay too.
			select {
			case sub.ch <- event:
			default:
			}
		}
	}

	b.subs = append(b.subs, sub)
	return sub.ch
}

// Seq returns the current sequence number (the last assigned value).
func (b *Bus) Seq() uint64 {
	return atomic.LoadUint64(&b.seq)
}

// matches returns true if the subscriber should receive events for the topic.
// A nil topic set means wildcard (receives everything).
func (s *subscriber) matches(topic string) bool {
	if s.topics == nil {
		return true // wildcard
	}
	return s.topics[topic]
}
