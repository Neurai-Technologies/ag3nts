package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReplayAgentFromEvents(t *testing.T) {
	events := []AgentEvent{
		{Kind: EventInit, Content: "initialized", Timestamp: time.Now()},
		{Kind: EventMessage, Content: "hello world", Timestamp: time.Now()},
		{Kind: EventComplete, Content: "done", Timestamp: time.Now()},
	}

	agent := NewReplayAgentFromEvents("test-agent", events)
	if agent.Name() != "test-agent" {
		t.Errorf("name = %q", agent.Name())
	}
	if !agent.Available() {
		t.Error("should be available")
	}

	sess, err := agent.Start(context.Background(), "test prompt", &StartOpts{TaskID: "T1"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var received []AgentEvent
	for e := range sess.Events() {
		received = append(received, e)
	}

	if len(received) != 3 {
		t.Fatalf("expected 3 events, got %d", len(received))
	}
	if received[0].Kind != EventInit {
		t.Errorf("event[0].Kind = %q", received[0].Kind)
	}
	if received[1].Content != "hello world" {
		t.Errorf("event[1].Content = %q", received[1].Content)
	}
	if received[2].Kind != EventComplete {
		t.Errorf("event[2].Kind = %q", received[2].Kind)
	}
	// Verify task ID is overridden.
	for _, e := range received {
		if e.TaskID != "T1" {
			t.Errorf("event.TaskID = %q, want T1", e.TaskID)
		}
	}
}

func TestReplayAgentFromJSONL(t *testing.T) {
	// Write a recording file, then replay it.
	dir := t.TempDir()
	recPath := filepath.Join(dir, "recording.jsonl")

	// Create a recording agent wrapping a simple replay agent.
	inner := NewReplayAgentFromEvents("inner", []AgentEvent{
		{Kind: EventInit, Content: "start", Timestamp: time.Now()},
		{Kind: EventMessage, Content: "test output", Timestamp: time.Now()},
		{Kind: EventComplete, Content: "finished", Timestamp: time.Now()},
	})

	recorder := NewRecordingAgent(inner, recPath)
	sess, err := recorder.Start(context.Background(), "record this", &StartOpts{TaskID: "REC1"})
	if err != nil {
		t.Fatalf("Start recording: %v", err)
	}
	// Drain events to complete recording.
	for range sess.Events() {
	}

	// Verify recording file exists.
	info, err := os.Stat(recPath)
	if err != nil {
		t.Fatalf("recording file: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("recording file is empty")
	}

	// Replay from the recording file.
	replay, err := NewReplayAgent("replayed", recPath)
	if err != nil {
		t.Fatalf("NewReplayAgent: %v", err)
	}

	sess2, err := replay.Start(context.Background(), "replay", &StartOpts{TaskID: "RPL1"})
	if err != nil {
		t.Fatalf("Start replay: %v", err)
	}

	var replayed []AgentEvent
	for e := range sess2.Events() {
		replayed = append(replayed, e)
	}

	if len(replayed) != 3 {
		t.Fatalf("expected 3 replayed events, got %d", len(replayed))
	}
	if replayed[1].Content != "test output" {
		t.Errorf("replayed[1].Content = %q", replayed[1].Content)
	}
}

func TestReplayAgentErrorRecovery(t *testing.T) {
	// Simulate an agent that fails then succeeds (error event followed by completion).
	events := []AgentEvent{
		{Kind: EventInit, Content: "started", Timestamp: time.Now()},
		{Kind: EventError, Content: "rate limit exceeded", Timestamp: time.Now()},
		{Kind: EventMessage, Content: "retried successfully", Timestamp: time.Now()},
		{Kind: EventComplete, Content: "done after retry", Timestamp: time.Now()},
	}

	agent := NewReplayAgentFromEvents("retry-agent", events)
	sess, err := agent.Start(context.Background(), "test", nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	var received []AgentEvent
	for e := range sess.Events() {
		received = append(received, e)
	}

	if len(received) != 4 {
		t.Fatalf("expected 4 events, got %d", len(received))
	}
	if received[1].Kind != EventError {
		t.Errorf("event[1].Kind = %q, want error", received[1].Kind)
	}
	if received[3].Kind != EventComplete {
		t.Errorf("event[3].Kind = %q, want complete", received[3].Kind)
	}
}

func TestReplayAgentMultipleSessions(t *testing.T) {
	// Verify that a replay agent can be started multiple times.
	events := []AgentEvent{
		{Kind: EventInit, Content: "init", Timestamp: time.Now()},
		{Kind: EventComplete, Content: "done", Timestamp: time.Now()},
	}

	agent := NewReplayAgentFromEvents("multi", events)

	for i := 0; i < 3; i++ {
		sess, err := agent.Start(context.Background(), "test", &StartOpts{TaskID: "T" + string(rune('0'+i))})
		if err != nil {
			t.Fatalf("Start %d: %v", i, err)
		}
		count := 0
		for range sess.Events() {
			count++
		}
		if count != 2 {
			t.Errorf("session %d: expected 2 events, got %d", i, count)
		}
	}
}
