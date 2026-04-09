package bus

import (
	"testing"
	"time"
)

func TestPublishSubscribe(t *testing.T) {
	b := New()
	defer b.Close()

	ch := b.Subscribe(10, "test.topic")
	b.Publish("test.topic", "sender", "hello")

	select {
	case event := <-ch:
		if event.Topic != "test.topic" {
			t.Errorf("Topic = %q, want test.topic", event.Topic)
		}
		if event.Source != "sender" {
			t.Errorf("Source = %q, want sender", event.Source)
		}
		if event.Payload != "hello" {
			t.Errorf("Payload = %v, want hello", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("Timed out waiting for event")
	}
}

func TestWildcardSubscription(t *testing.T) {
	b := New()
	defer b.Close()

	ch := b.Subscribe(10, "*")
	b.Publish("any.topic", "sender", "data")

	select {
	case event := <-ch:
		if event.Topic != "any.topic" {
			t.Errorf("Topic = %q, want any.topic", event.Topic)
		}
	case <-time.After(time.Second):
		t.Fatal("Wildcard subscriber did not receive event")
	}
}

func TestTopicFiltering(t *testing.T) {
	b := New()
	defer b.Close()

	ch := b.Subscribe(10, "agent.claude")
	b.Publish("agent.gemini", "gemini", "ignored")
	b.Publish("agent.claude", "claude", "received")

	select {
	case event := <-ch:
		if event.Source != "claude" {
			t.Errorf("Received wrong event: source = %q", event.Source)
		}
	case <-time.After(time.Second):
		t.Fatal("Timed out")
	}
}

func TestNonBlockingPublish(t *testing.T) {
	b := New()
	defer b.Close()

	// Buffer size 1 — second publish should be dropped, not block.
	ch := b.Subscribe(1, "test")
	b.Publish("test", "s", "first")
	b.Publish("test", "s", "second") // should be dropped

	event := <-ch
	if event.Payload != "first" {
		t.Errorf("Expected first event, got %v", event.Payload)
	}

	// Channel should be empty now.
	select {
	case <-ch:
		t.Error("Should not have received second event (buffer overflow drop)")
	default:
		// correct — channel is empty
	}
}

func TestUnsubscribe(t *testing.T) {
	b := New()
	defer b.Close()

	ch := b.Subscribe(10, "test")
	b.Unsubscribe(ch)

	// Channel should be closed.
	_, ok := <-ch
	if ok {
		t.Error("Channel should be closed after Unsubscribe")
	}
}

func TestCloseClosesAllChannels(t *testing.T) {
	b := New()
	ch1 := b.Subscribe(10, "a")
	ch2 := b.Subscribe(10, "b")

	b.Close()

	if _, ok := <-ch1; ok {
		t.Error("ch1 should be closed")
	}
	if _, ok := <-ch2; ok {
		t.Error("ch2 should be closed")
	}
}

// --- Sequence numbers ---

func TestSequenceMonotonic(t *testing.T) {
	b := New()
	defer b.Close()

	ch := b.Subscribe(100, "*")
	for i := 0; i < 10; i++ {
		b.Publish("test", "s", i)
	}

	var lastSeq uint64
	for i := 0; i < 10; i++ {
		event := <-ch
		if event.Seq <= lastSeq {
			t.Errorf("event %d: seq %d not greater than previous %d", i, event.Seq, lastSeq)
		}
		lastSeq = event.Seq
	}
}

func TestSeqMethod(t *testing.T) {
	b := New()
	defer b.Close()

	if b.Seq() != 0 {
		t.Errorf("initial seq = %d, want 0", b.Seq())
	}
	b.Publish("test", "s", "a")
	b.Publish("test", "s", "b")
	if b.Seq() != 2 {
		t.Errorf("seq after 2 publishes = %d, want 2", b.Seq())
	}
}

// --- Replay buffer ---

func TestReplayBuffer(t *testing.T) {
	b := New()
	defer b.Close()

	// Publish 5 events before subscribing.
	for i := 0; i < 5; i++ {
		b.Publish("test", "s", i)
	}

	// Late subscriber with replay from seq 0 (get everything).
	ch := b.SubscribeWithReplay(100, 0, "*")

	// Should receive all 5 replayed events.
	for i := 0; i < 5; i++ {
		select {
		case event := <-ch:
			if event.Payload != i {
				t.Errorf("replay[%d]: payload = %v, want %d", i, event.Payload, i)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for replay event %d", i)
		}
	}

	// Now publish a live event — should also arrive.
	b.Publish("test", "s", "live")
	select {
	case event := <-ch:
		if event.Payload != "live" {
			t.Errorf("live event payload = %v, want 'live'", event.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for live event after replay")
	}
}

func TestReplayAfterSeq(t *testing.T) {
	b := New()
	defer b.Close()

	// Publish 5 events.
	for i := 0; i < 5; i++ {
		b.Publish("test", "s", i)
	}

	// Subscribe with afterSeq=3 — should only get events with seq > 3 (seq 4 and 5).
	ch := b.SubscribeWithReplay(100, 3, "*")

	count := 0
	for {
		select {
		case event := <-ch:
			if event.Seq <= 3 {
				t.Errorf("got event with seq %d, should only get > 3", event.Seq)
			}
			count++
		case <-time.After(100 * time.Millisecond):
			goto done
		}
	}
done:
	if count != 2 {
		t.Errorf("replayed %d events, want 2", count)
	}
}

func TestReplayOverflow(t *testing.T) {
	b := New()
	defer b.Close()

	// Publish more than ringSize events.
	total := ringSize + 100
	for i := 0; i < total; i++ {
		b.Publish("test", "s", i)
	}

	// Should only replay the last ringSize events.
	ch := b.SubscribeWithReplay(ringSize+10, 0, "*")

	count := 0
	for {
		select {
		case <-ch:
			count++
		case <-time.After(100 * time.Millisecond):
			goto done
		}
	}
done:
	if count != ringSize {
		t.Errorf("replayed %d events, want %d (ring size)", count, ringSize)
	}
}

func TestReplayFiltersByTopic(t *testing.T) {
	b := New()
	defer b.Close()

	b.Publish("agent.claude", "claude", "c1")
	b.Publish("agent.gemini", "gemini", "g1")
	b.Publish("agent.claude", "claude", "c2")
	b.Publish("agent.gemini", "gemini", "g2")

	// Subscribe with replay, filtered to claude only.
	ch := b.SubscribeWithReplay(100, 0, "agent.claude")

	count := 0
	for {
		select {
		case event := <-ch:
			if event.Topic != "agent.claude" {
				t.Errorf("got topic %q, want agent.claude", event.Topic)
			}
			count++
		case <-time.After(100 * time.Millisecond):
			goto done
		}
	}
done:
	if count != 2 {
		t.Errorf("replayed %d claude events, want 2", count)
	}
}

func TestReplayEmpty(t *testing.T) {
	b := New()
	defer b.Close()

	// No events published. Replay should return empty channel.
	ch := b.SubscribeWithReplay(10, 0, "*")

	select {
	case <-ch:
		t.Error("should not receive any events from empty replay")
	case <-time.After(50 * time.Millisecond):
		// correct
	}
}
