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
