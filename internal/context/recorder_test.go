package context

import (
	goctx "context"
	"testing"
	"time"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
)

func TestRecorderCapturesEvents(t *testing.T) {
	rs, db := openTestStore(t)
	b := bus.New()
	defer b.Close()

	rec := NewRecorder(rs, b)
	rec.Start(goctx.Background())
	defer rec.Stop()

	// Publish 5 events with different kinds.
	events := []agent.AgentEvent{
		{Kind: agent.EventMessage, Agent: "claude", TaskID: "t-1", Content: "hello world", Timestamp: time.Now()},
		{Kind: agent.EventToolUse, Agent: "claude", TaskID: "t-1", Content: "Bash: ls -la", Timestamp: time.Now()},
		{Kind: agent.EventMessage, Agent: "gemini", TaskID: "t-2", Content: "research complete", Timestamp: time.Now()},
		{Kind: agent.EventProgress, Agent: "claude", TaskID: "t-1", Content: "thinking...", Timestamp: time.Now()}, // filtered
		{Kind: agent.EventInit, Agent: "claude", TaskID: "t-1", Content: "starting", Timestamp: time.Now()},        // filtered
	}

	for _, ev := range events {
		b.Publish("system", ev.Agent, ev)
	}

	// Allow recorder goroutine to drain.
	time.Sleep(100 * time.Millisecond)

	// Should have 3 chunks (5 events - 2 filtered).
	count, err := db.CountContextChunks("s-test")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 3 {
		t.Errorf("count = %d, want 3 (progress/init filtered)", count)
	}
}

func TestRecorderFiltersEmptyContent(t *testing.T) {
	rs, db := openTestStore(t)
	b := bus.New()
	defer b.Close()

	rec := NewRecorder(rs, b)
	rec.Start(goctx.Background())
	defer rec.Stop()

	// EventMessage with empty content should be filtered.
	// EventComplete with empty content should be kept (marker).
	b.Publish("system", "claude", agent.AgentEvent{
		Kind: agent.EventMessage, Agent: "claude", Content: "", Timestamp: time.Now(),
	})
	b.Publish("system", "claude", agent.AgentEvent{
		Kind: agent.EventComplete, Agent: "claude", Content: "", Timestamp: time.Now(),
	})

	time.Sleep(100 * time.Millisecond)

	count, _ := db.CountContextChunks("s-test")
	if count != 1 {
		t.Errorf("count = %d, want 1 (complete is a marker)", count)
	}
}

func TestRecorderKindPrefix(t *testing.T) {
	rs, db := openTestStore(t)
	b := bus.New()
	defer b.Close()

	rec := NewRecorder(rs, b)
	rec.Start(goctx.Background())
	defer rec.Stop()

	b.Publish("system", "claude", agent.AgentEvent{
		Kind: agent.EventToolUse, Agent: "claude", Content: "Bash: echo hi", Timestamp: time.Now(),
	})
	time.Sleep(100 * time.Millisecond)

	// Verify chunk kind has "event_" prefix.
	chunks, err := db.ListContextChunks("s-test", 0, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len = %d, want 1", len(chunks))
	}
	if chunks[0].Kind != "event_tool_use" {
		t.Errorf("kind = %q, want 'event_tool_use'", chunks[0].Kind)
	}
}

func TestRecorderStopCleanly(t *testing.T) {
	rs, _ := openTestStore(t)
	b := bus.New()
	defer b.Close()

	rec := NewRecorder(rs, b)
	rec.Start(goctx.Background())

	// Publish something then stop.
	b.Publish("system", "claude", agent.AgentEvent{
		Kind: agent.EventMessage, Agent: "claude", Content: "test", Timestamp: time.Now(),
	})
	time.Sleep(50 * time.Millisecond)

	rec.Stop() // should return without hanging

	// Calling Stop again should be safe.
	rec.Stop()
}

func TestRecorderIgnoresNonAgentPayload(t *testing.T) {
	rs, db := openTestStore(t)
	b := bus.New()
	defer b.Close()

	rec := NewRecorder(rs, b)
	rec.Start(goctx.Background())
	defer rec.Stop()

	// Publish string payload (not an AgentEvent).
	b.Publish("system", "test", "just a string")
	// Also publish a real event.
	b.Publish("system", "claude", agent.AgentEvent{
		Kind: agent.EventMessage, Agent: "claude", Content: "real event", Timestamp: time.Now(),
	})
	time.Sleep(100 * time.Millisecond)

	count, _ := db.CountContextChunks("s-test")
	if count != 1 {
		t.Errorf("count = %d, want 1 (string payload ignored)", count)
	}
}
