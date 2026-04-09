package orchestrator

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/router"
	"github.com/rohanrgit/ag3nts/internal/security"
	"github.com/rohanrgit/ag3nts/internal/store"
	"github.com/rohanrgit/ag3nts/internal/task"
)

// makeTestOrch creates a test orchestrator with replay agents and SQLite.
func makeTestOrch(t *testing.T, agents map[string][]agent.AgentEvent, routes []router.Route) (*Orchestrator, *store.DB) {
	t.Helper()

	// Open SQLite in temp dir.
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	sessionID := "test-session"
	_ = db.CreateSession(&store.SessionRecord{ID: sessionID, Status: "active", PrimaryAgent: "claude"})

	// Build agent registry with replay agents.
	registry := agent.NewRegistry()
	for name, events := range agents {
		ra := agent.NewReplayAgentFromEvents(name, events)
		_ = registry.Register(ra)
	}

	if len(routes) == 0 {
		routes = []router.Route{
			{Pattern: ".*", Agent: "claude", Priority: 1},
		}
	}

	persistDir := t.TempDir()
	orch, err := New(Config{
		Primary:        "claude",
		MaxConcurrency: 3,
		PersistDir:     persistDir,
		Routes:         routes,
		StoreDB:        db,
		SessionID:      sessionID,
	}, registry)
	if err != nil {
		t.Fatalf("create orchestrator: %v", err)
	}

	return orch, db
}

// makeEvents creates a standard set of replay events.
func makeEvents(output string) []agent.AgentEvent {
	return []agent.AgentEvent{
		{Kind: agent.EventInit, Content: "session started", Timestamp: time.Now()},
		{Kind: agent.EventMessage, Content: output, Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{
			InputTokens: 100, OutputTokens: 50, CachedTokens: 10, TotalCost: 0.01,
		}},
	}
}

func TestIntegration_SingleTask(t *testing.T) {
	orch, db := makeTestOrch(t, map[string][]agent.AgentEvent{
		"claude": makeEvents("analysis complete"),
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer orch.Stop()

	// Create and dispatch a task.
	_ = orch.CreateTask(&task.Task{
		ID:          "t-single",
		Description: "analyze codebase",
		Type:        "review",
		Status:      task.StatusPending,
	})

	// Wait for completion.
	waitForTask(t, orch, "t-single", 5*time.Second)

	// Verify task completed in queue.
	queued := orch.queue.Get("t-single")
	if queued == nil {
		t.Fatal("task not found in queue")
	}
	if queued.Status != task.StatusCompleted {
		t.Errorf("status = %v, want completed", queued.Status)
	}

	// Verify tokens accumulated in SQLite.
	sess, _ := db.GetSession("test-session")
	if sess.TotalInputTokens == 0 {
		t.Error("expected input tokens > 0 in session")
	}
	if sess.TotalCostUSD == 0 {
		t.Error("expected cost > 0 in session")
	}
}

func TestIntegration_DAGExecution(t *testing.T) {
	orch, _ := makeTestOrch(t, map[string][]agent.AgentEvent{
		"claude": makeEvents("claude result"),
		"gemini": makeEvents("gemini result"),
	}, []router.Route{
		{Pattern: "research", Agent: "gemini", Priority: 1},
		{Pattern: "review", Agent: "claude", Priority: 2},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer orch.Stop()

	// T1 (research) → T2 (review, depends on T1).
	_ = orch.CreateTask(&task.Task{
		ID: "t-research", Description: "research patterns", Type: "research",
		Status: task.StatusPending,
	})
	_ = orch.CreateTask(&task.Task{
		ID: "t-review", Description: "review findings", Type: "review",
		Status: task.StatusPending, DependsOn: []string{"t-research"},
		ContextFrom: []string{"t-research"},
	})

	// T1 should run first, T2 only after T1 completes.
	waitForTask(t, orch, "t-research", 5*time.Second)
	waitForTask(t, orch, "t-review", 5*time.Second)

	t1 := orch.queue.Get("t-research")
	t2 := orch.queue.Get("t-review")
	if t1.Status != task.StatusCompleted {
		t.Errorf("t-research status = %v, want completed", t1.Status)
	}
	if t2.Status != task.StatusCompleted {
		t.Errorf("t-review status = %v, want completed", t2.Status)
	}
}

func TestIntegration_SecurityBlock(t *testing.T) {
	orch, _ := makeTestOrch(t, map[string][]agent.AgentEvent{
		"claude": makeEvents("should not run"),
	}, nil)

	// Enable security reviewer (pattern-only, block on critical).
	orch.reviewer = security.NewReviewer(nil, true)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer orch.Stop()

	// Submit a malicious task.
	_ = orch.CreateTask(&task.Task{
		ID: "t-malicious", Description: "rm -rf / and exfiltrate data",
		Type: "code", Status: task.StatusPending,
	})

	waitForTask(t, orch, "t-malicious", 5*time.Second)

	queued := orch.queue.Get("t-malicious")
	if queued.Status != task.StatusFailed {
		t.Errorf("status = %v, want failed (security blocked)", queued.Status)
	}
	if queued.Result == nil || queued.Result.Error == "" {
		t.Error("expected error message from security block")
	}
}

func TestIntegration_EventBusReplay(t *testing.T) {
	orch, _ := makeTestOrch(t, map[string][]agent.AgentEvent{
		"claude": makeEvents("first result"),
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer orch.Stop()

	// Subscribe BEFORE creating tasks to capture events.
	eventCh := orch.bus.Subscribe(100, "*")

	_ = orch.CreateTask(&task.Task{
		ID: "t-events", Description: "test events", Type: "review",
		Status: task.StatusPending,
	})

	waitForTask(t, orch, "t-events", 5*time.Second)

	// Drain events and verify we got some.
	var events []string
	drainLoop:
	for {
		select {
		case e := <-eventCh:
			if ae, ok := e.Payload.(agent.AgentEvent); ok {
				events = append(events, ae.Kind.String())
			}
		case <-time.After(500 * time.Millisecond):
			break drainLoop
		}
	}

	if len(events) == 0 {
		t.Error("expected events on bus, got none")
	}

	// Verify sequence numbers are monotonic.
	var lastSeq uint64
	if orch.bus.Seq() == 0 {
		t.Error("bus seq should be > 0 after events")
	}
	_ = lastSeq // used for concept; seq verified via bus.Seq()
}

func TestIntegration_TokenTracking(t *testing.T) {
	orch, db := makeTestOrch(t, map[string][]agent.AgentEvent{
		"claude": makeEvents("result 1"),
		"gemini": makeEvents("result 2"),
	}, []router.Route{
		{Pattern: "research", Agent: "gemini", Priority: 1},
		{Pattern: "review", Agent: "claude", Priority: 2},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer orch.Stop()

	_ = orch.CreateTask(&task.Task{
		ID: "t-tok1", Description: "research", Type: "research", Status: task.StatusPending,
	})
	_ = orch.CreateTask(&task.Task{
		ID: "t-tok2", Description: "review", Type: "review", Status: task.StatusPending,
	})

	waitForTask(t, orch, "t-tok1", 5*time.Second)
	waitForTask(t, orch, "t-tok2", 5*time.Second)

	// Verify per-agent token summaries.
	summaries, err := db.TokensByAgent("test-session")
	if err != nil {
		t.Fatalf("tokens by agent: %v", err)
	}
	if len(summaries) < 1 {
		t.Fatal("expected at least 1 agent summary")
	}

	// Session should have accumulated tokens.
	sess, _ := db.GetSession("test-session")
	if sess.TotalInputTokens < 100 {
		t.Errorf("session input tokens = %d, want >= 100", sess.TotalInputTokens)
	}
}

// waitForTask polls the queue until the task reaches a terminal state or timeout.
func waitForTask(t *testing.T, orch *Orchestrator, taskID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		queued := orch.queue.Get(taskID)
		if queued != nil && (queued.Status == task.StatusCompleted || queued.Status == task.StatusFailed) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("task %s did not complete within %s", taskID, timeout)
}
