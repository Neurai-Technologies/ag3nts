package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanrgit/ag3nts/internal/agent"
	m3m0ry "github.com/rohanrgit/ag3nts/internal/context"
	"github.com/rohanrgit/ag3nts/internal/router"
	"github.com/rohanrgit/ag3nts/internal/store"
	"github.com/rohanrgit/ag3nts/internal/task"
)

// makeTestOrchWithM3m0ry creates an orchestrator with m3m0ry rolling context enabled.
func makeTestOrchWithM3m0ry(t *testing.T, agents map[string][]agent.AgentEvent) (*Orchestrator, *store.DB, *m3m0ry.RollingStore) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	sessionID := "m3m0ry-test"
	_ = db.CreateSession(&store.SessionRecord{ID: sessionID, Status: "active", PrimaryAgent: "claude"})

	// Build replay agent registry.
	registry := agent.NewRegistry()
	for name, events := range agents {
		ra := agent.NewReplayAgentFromEvents(name, events)
		_ = registry.Register(ra)
	}

	// Create rolling context store.
	jsonlPath := filepath.Join(t.TempDir(), "m3m0ry.jsonl")
	cfg := m3m0ry.DefaultConfig()
	cfg.JSONLPath = jsonlPath
	rs, err := m3m0ry.Open(cfg, db, sessionID, nil)
	if err != nil {
		t.Fatalf("open m3m0ry: %v", err)
	}
	t.Cleanup(func() { rs.Close() })

	routes := []router.Route{{Pattern: ".*", Agent: "claude", Priority: 1}}
	persistDir := t.TempDir()
	orch, err := New(Config{
		Primary:        "claude",
		MaxConcurrency: 3,
		PersistDir:     persistDir,
		Routes:         routes,
		StoreDB:        db,
		SessionID:      sessionID,
		Context:        rs,
	}, registry)
	if err != nil {
		t.Fatalf("create orchestrator: %v", err)
	}

	return orch, db, rs
}

func TestM3m0ry_CapturesTaskResults(t *testing.T) {
	orch, db, rs := makeTestOrchWithM3m0ry(t, map[string][]agent.AgentEvent{
		"claude": {
			{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
			{Kind: agent.EventMessage, Content: "analyzed the authentication module", Timestamp: time.Now()},
			{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{
				InputTokens: 100, OutputTokens: 50, TotalCost: 0.01,
			}},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	_ = orch.CreateTask(&task.Task{
		ID:          "t-m1",
		Description: "analyze auth module",
		Type:        "review",
		Status:      task.StatusPending,
	})

	waitForTask(t, orch, "t-m1", 5*time.Second)

	// Give recorder and append goroutine a moment.
	time.Sleep(200 * time.Millisecond)

	// Verify task_result was appended.
	chunks, err := db.ListContextChunks("m3m0ry-test", 0, 100)
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected chunks in m3m0ry")
	}

	var hasTaskResult bool
	for _, c := range chunks {
		if c.Kind == "task_result" {
			hasTaskResult = true
			break
		}
	}
	if !hasTaskResult {
		t.Error("expected at least one task_result chunk")
	}

	// Stats should reflect non-zero tokens.
	stats, err := rs.Stats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.TotalTokens == 0 {
		t.Error("expected non-zero total tokens")
	}
}

func TestM3m0ry_ContextFlowsToDownstreamTask(t *testing.T) {
	orch, _, _ := makeTestOrchWithM3m0ry(t, map[string][]agent.AgentEvent{
		"claude": {
			{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
			{Kind: agent.EventMessage, Content: "found three issues in the payment module", Timestamp: time.Now()},
			{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{
				InputTokens: 100, OutputTokens: 50,
			}},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	// First task produces content.
	_ = orch.CreateTask(&task.Task{
		ID: "t-upstream", Description: "review payment module",
		Type: "review", Status: task.StatusPending,
	})
	waitForTask(t, orch, "t-upstream", 5*time.Second)
	time.Sleep(100 * time.Millisecond) // let m3m0ry append

	// Second task references upstream.
	_ = orch.CreateTask(&task.Task{
		ID: "t-downstream", Description: "investigate payment issues",
		Type: "review", Status: task.StatusPending,
		ContextFrom: []string{"t-upstream"},
	})
	waitForTask(t, orch, "t-downstream", 5*time.Second)

	// Build context for downstream manually and verify m3m0ry section is present.
	built := orch.buildContext([]string{"t-upstream"})
	if !strings.Contains(built, "=== m3m0ry:") {
		t.Errorf("expected m3m0ry section in context; got: %s", truncateForLog(built, 500))
	}
}

func TestM3m0ry_EventBusRecording(t *testing.T) {
	orch, db, _ := makeTestOrchWithM3m0ry(t, map[string][]agent.AgentEvent{
		"claude": {
			{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
			{Kind: agent.EventMessage, Content: "first response", Timestamp: time.Now()},
			{Kind: agent.EventToolUse, Content: "Bash: ls", Timestamp: time.Now()},
			{Kind: agent.EventMessage, Content: "second response", Timestamp: time.Now()},
			{Kind: agent.EventComplete, Timestamp: time.Now()},
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	_ = orch.CreateTask(&task.Task{
		ID: "t-events", Description: "test events",
		Type: "review", Status: task.StatusPending,
	})
	waitForTask(t, orch, "t-events", 5*time.Second)
	time.Sleep(200 * time.Millisecond) // let recorder drain

	chunks, _ := db.ListContextChunks("m3m0ry-test", 0, 100)
	// Should have: event_message × 2, event_tool_use × 1, event_complete × 1 (init filtered), task_result × 1
	// Total ≥ 5 chunks.
	if len(chunks) < 4 {
		t.Errorf("expected ≥4 chunks, got %d", len(chunks))
	}

	// Verify kinds include event_* prefixes.
	kinds := make(map[string]bool)
	for _, c := range chunks {
		kinds[c.Kind] = true
	}
	if !kinds["event_message"] {
		t.Error("missing event_message chunks")
	}
	if !kinds["task_result"] {
		t.Error("missing task_result chunk")
	}
}

func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestM3m0ry_ProgressStreamFallback verifies that agents which only stream
// content via EventProgress (no EventMessage) still get their final output
// captured as a task_result chunk. This is the Gemini CLI pattern: delta
// messages stream in as EventProgress chunks, and the parser never emits a
// final non-delta EventMessage.
//
// Bug #2 from the first live test run: Gemini's research output was visible
// in the TUI but never persisted to m3m0ry, breaking downstream retrieval.
func TestM3m0ry_ProgressStreamFallback(t *testing.T) {
	// Replay agent emits ONLY EventProgress events (no EventMessage) —
	// simulates Gemini's delta-only output pattern.
	progressOnlyEvents := []agent.AgentEvent{
		{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
		{Kind: agent.EventProgress, Content: "Research finding: ", Timestamp: time.Now()},
		{Kind: agent.EventProgress, Content: "the MCP protocol uses ", Timestamp: time.Now()},
		{Kind: agent.EventProgress, Content: "JSON-RPC over stdio.", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Content: "success", Timestamp: time.Now(), Usage: &agent.TokenUsage{
			InputTokens: 100, OutputTokens: 50,
		}},
	}

	orch, db, _ := makeTestOrchWithM3m0ry(t, map[string][]agent.AgentEvent{
		"claude": progressOnlyEvents,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	_ = orch.CreateTask(&task.Task{
		ID:          "t-progress",
		Description: "delta-only agent",
		Type:        "review",
		Status:      task.StatusPending,
	})

	waitForTask(t, orch, "t-progress", 5*time.Second)
	time.Sleep(200 * time.Millisecond)

	// Verify a task_result chunk was appended with the concatenated deltas.
	chunks, err := db.ListContextChunks("m3m0ry-test", 0, 100)
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}

	var taskResult *store.ContextChunkRecord
	for _, c := range chunks {
		if c.Kind == "task_result" {
			taskResult = c
			break
		}
	}
	if taskResult == nil {
		t.Fatal("expected task_result chunk, got none — progressive fallback didn't fire")
	}

	// The content should be the concatenation of all three delta chunks.
	want := "Research finding: the MCP protocol uses JSON-RPC over stdio."
	if taskResult.Content != want {
		t.Errorf("task_result content = %q\n                   want %q", taskResult.Content, want)
	}
}

// TestM3m0ry_MessagePreferredOverProgress verifies that when an agent emits
// both EventMessage and EventProgress, the EventMessage content wins. This
// ensures the fallback only kicks in when needed.
func TestM3m0ry_MessagePreferredOverProgress(t *testing.T) {
	events := []agent.AgentEvent{
		{Kind: agent.EventProgress, Content: "delta chunk", Timestamp: time.Now()},
		{Kind: agent.EventMessage, Content: "final message content", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 10}},
	}

	orch, db, _ := makeTestOrchWithM3m0ry(t, map[string][]agent.AgentEvent{
		"claude": events,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	_ = orch.CreateTask(&task.Task{
		ID: "t-pref", Description: "test", Type: "review", Status: task.StatusPending,
	})
	waitForTask(t, orch, "t-pref", 5*time.Second)
	time.Sleep(200 * time.Millisecond)

	chunks, _ := db.ListContextChunks("m3m0ry-test", 0, 100)
	var tr *store.ContextChunkRecord
	for _, c := range chunks {
		if c.Kind == "task_result" {
			tr = c
			break
		}
	}
	if tr == nil {
		t.Fatal("expected task_result chunk")
	}
	if tr.Content != "final message content" {
		t.Errorf("task_result = %q, want 'final message content' (EventMessage wins)", tr.Content)
	}
}

// TestM3m0ry_SkipsCodexTurnStartedNoise verifies that the "Turn started"
// status event from Codex is NOT accumulated as progressive content — the
// fallback shouldn't pollute task results with pure status noise.
func TestM3m0ry_SkipsCodexTurnStartedNoise(t *testing.T) {
	events := []agent.AgentEvent{
		{Kind: agent.EventProgress, Content: "Turn started", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 10}},
	}

	orch, db, _ := makeTestOrchWithM3m0ry(t, map[string][]agent.AgentEvent{
		"claude": events,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	_ = orch.CreateTask(&task.Task{
		ID: "t-noise", Description: "codex noise", Type: "review", Status: task.StatusPending,
	})
	waitForTask(t, orch, "t-noise", 5*time.Second)
	time.Sleep(200 * time.Millisecond)

	chunks, _ := db.ListContextChunks("m3m0ry-test", 0, 100)
	for _, c := range chunks {
		if c.Kind == "task_result" {
			t.Errorf("no task_result should exist; found: %q", c.Content)
		}
	}
}
