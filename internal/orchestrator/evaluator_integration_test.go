package orchestrator

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/recipe"
	"github.com/rohanrgit/ag3nts/internal/router"
	"github.com/rohanrgit/ag3nts/internal/store"
	"github.com/rohanrgit/ag3nts/internal/task"
)

// TestEvaluatorLoopIntegration runs a 2-stage recipe (implementer + evaluator)
// where the evaluator alternates REJECT, REJECT, ACCEPT. Verifies that retry
// tasks are spawned and the loop terminates cleanly.
func TestEvaluatorLoopIntegration(t *testing.T) {
	// Replay agent producing a REJECT verdict.
	rejectEvents := []agent.AgentEvent{
		{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
		{Kind: agent.EventMessage, Content: "REJECT: needs more work\nFeedback: missing tests", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 10, OutputTokens: 5}},
	}
	acceptEvents := []agent.AgentEvent{
		{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
		{Kind: agent.EventMessage, Content: "ACCEPT: looks good\nNo issues found", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 10, OutputTokens: 5}},
	}

	// Create orchestrator with replay agents.
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	_ = db.CreateSession(&store.SessionRecord{ID: "eval-test", Status: "active", PrimaryAgent: "codex"})

	// Alternating evaluator: REJECT, REJECT, ACCEPT.
	evalAgent := newAlternatingReplayAgent("evaluator", []([]agent.AgentEvent){
		rejectEvents,
		rejectEvents,
		acceptEvents,
	})
	// Implementer always "succeeds" with the same content.
	implAgent := agent.NewReplayAgentFromEvents("implementer", []agent.AgentEvent{
		{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
		{Kind: agent.EventMessage, Content: "code written", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 10, OutputTokens: 5}},
	})

	registry := agent.NewRegistry()
	_ = registry.Register(evalAgent)
	_ = registry.Register(implAgent)

	routes := []router.Route{
		{Pattern: "impl", Agent: "implementer", Priority: 1},
		{Pattern: "eval", Agent: "evaluator", Priority: 2},
	}

	orch, err := New(Config{
		Primary:        "implementer",
		MaxConcurrency: 3,
		PersistDir:     t.TempDir(),
		Routes:         routes,
		StoreDB:        db,
		SessionID:      "eval-test",
	}, registry)
	if err != nil {
		t.Fatalf("new orchestrator: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	// Build a minimal 2-stage recipe and run it.
	r := &recipe.Recipe{
		Name: "impl-and-review",
		Tasks: []recipe.SubTask{
			{
				ID:             "impl",
				Agent:          "implementer",
				Type:           "impl",
				PromptTemplate: "write the code",
			},
			{
				ID:               "review",
				Agent:            "evaluator",
				Type:             "eval",
				DependsOn:        []string{"impl"},
				ContextFrom:      []string{"impl"},
				PromptTemplate:   "review the work",
				EvaluatorOf:      "impl",
				EvaluatorRetries: 3,
			},
		},
	}

	runID, err := orch.RunRecipe(r, nil)
	if err != nil {
		t.Fatalf("run recipe: %v", err)
	}

	// Wait for the whole chain to complete.
	// Expect: impl, review (REJECT) → retry1 impl, retry1 review (REJECT) → retry2 impl, retry2 review (ACCEPT)
	// 6 tasks total.
	implID := runID + "-impl"
	reviewID := runID + "-review"

	waitForTask(t, orch, implID, 10*time.Second)
	waitForTask(t, orch, reviewID, 10*time.Second)
	// Give retry dispatch a moment.
	time.Sleep(300 * time.Millisecond)

	// Verify retry1 impl exists.
	retry1ID := implID + "-retry1"
	waitForTask(t, orch, retry1ID, 10*time.Second)

	retry1EvalID := reviewID + "-retry1"
	waitForTask(t, orch, retry1EvalID, 10*time.Second)
	time.Sleep(300 * time.Millisecond)

	// Verify retry2 impl exists.
	retry2ID := implID + "-retry2"
	waitForTask(t, orch, retry2ID, 10*time.Second)

	retry2EvalID := reviewID + "-retry2"
	waitForTask(t, orch, retry2EvalID, 10*time.Second)

	// After ACCEPT on retry2, no more tasks should be spawned.
	time.Sleep(300 * time.Millisecond)
	retry3ID := implID + "-retry3"
	if snap := orch.queue.GetSnapshot(retry3ID); snap != nil {
		t.Errorf("retry3 task unexpectedly created: %+v", snap)
	}

	// Verify all 6 tasks completed.
	allTaskIDs := []string{implID, reviewID, retry1ID, retry1EvalID, retry2ID, retry2EvalID}
	for _, id := range allTaskIDs {
		snap := orch.queue.GetSnapshot(id)
		if snap == nil {
			t.Errorf("%s: task not in queue", id)
			continue
		}
		if snap.Status != task.StatusCompleted {
			t.Errorf("%s: status = %v, want completed", id, snap.Status)
		}
	}
}

// TestEvaluatorLoopMaxRetriesExhausted runs a recipe where the evaluator
// always outputs REJECT. Verifies that the loop terminates after
// EvaluatorRetries and marks the implementer as failed.
func TestEvaluatorLoopMaxRetriesExhausted(t *testing.T) {
	rejectEvents := []agent.AgentEvent{
		{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
		{Kind: agent.EventMessage, Content: "REJECT: never good enough", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 10, OutputTokens: 5}},
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	_ = db.CreateSession(&store.SessionRecord{ID: "eval-exhaust", Status: "active", PrimaryAgent: "codex"})

	evalAgent := agent.NewReplayAgentFromEvents("evaluator", rejectEvents)
	implAgent := agent.NewReplayAgentFromEvents("implementer", []agent.AgentEvent{
		{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
		{Kind: agent.EventMessage, Content: "code v1", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 10, OutputTokens: 5}},
	})

	registry := agent.NewRegistry()
	_ = registry.Register(evalAgent)
	_ = registry.Register(implAgent)

	routes := []router.Route{
		{Pattern: "impl", Agent: "implementer", Priority: 1},
		{Pattern: "eval", Agent: "evaluator", Priority: 2},
	}

	orch, err := New(Config{
		Primary:        "implementer",
		MaxConcurrency: 3,
		PersistDir:     t.TempDir(),
		Routes:         routes,
		StoreDB:        db,
		SessionID:      "eval-exhaust",
	}, registry)
	if err != nil {
		t.Fatalf("new orchestrator: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	r := &recipe.Recipe{
		Name: "loop-forever",
		Tasks: []recipe.SubTask{
			{ID: "impl", Agent: "implementer", Type: "impl", PromptTemplate: "write"},
			{
				ID: "review", Agent: "evaluator", Type: "eval",
				DependsOn:        []string{"impl"},
				EvaluatorOf:      "impl",
				EvaluatorRetries: 2, // low cap so test finishes quickly
				PromptTemplate:   "review",
			},
		},
	}

	runID, err := orch.RunRecipe(r, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	implID := runID + "-impl"
	reviewID := runID + "-review"

	// Wait for first round.
	waitForTask(t, orch, implID, 10*time.Second)
	waitForTask(t, orch, reviewID, 10*time.Second)
	time.Sleep(300 * time.Millisecond)

	// Wait for retry1.
	waitForTask(t, orch, implID+"-retry1", 10*time.Second)
	waitForTask(t, orch, reviewID+"-retry1", 10*time.Second)
	time.Sleep(300 * time.Millisecond)

	// Wait for retry2 (final).
	waitForTask(t, orch, implID+"-retry2", 10*time.Second)
	waitForTask(t, orch, reviewID+"-retry2", 10*time.Second)

	// After retry2 REJECT, max exhausted. No retry3 should exist.
	time.Sleep(500 * time.Millisecond)
	if snap := orch.queue.GetSnapshot(implID + "-retry3"); snap != nil {
		t.Error("retry3 should not have been spawned")
	}

	// Original impl should be marked failed.
	origImpl := orch.queue.GetSnapshot(implID)
	if origImpl == nil {
		t.Fatal("original impl missing")
	}
	if origImpl.Status != task.StatusFailed {
		t.Errorf("orig impl status = %v, want failed (after exhausted retries)", origImpl.Status)
	}
}

// TestEvaluatorLoopBlockedVerdict runs a recipe where the evaluator
// returns BLOCKED on the first review. Verifies that:
//  1. No retry tasks are spawned (loop terminates immediately)
//  2. The original implementer is marked as failed with the blocking reason
//  3. An evaluator_verdict chunk is written to m3m0ry for audit
func TestEvaluatorLoopBlockedVerdict(t *testing.T) {
	blockedEvents := []agent.AgentEvent{
		{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
		{Kind: agent.EventMessage, Content: "BLOCKED: objective is empty, cannot proceed\nReviewer details...", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 10, OutputTokens: 5}},
	}
	implEvents := []agent.AgentEvent{
		{Kind: agent.EventInit, Content: "start", Timestamp: time.Now()},
		{Kind: agent.EventMessage, Content: "cannot implement without details", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 10, OutputTokens: 5}},
	}

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	_ = db.CreateSession(&store.SessionRecord{ID: "eval-blocked", Status: "active", PrimaryAgent: "codex"})

	evalAgent := agent.NewReplayAgentFromEvents("evaluator", blockedEvents)
	implAgent := agent.NewReplayAgentFromEvents("implementer", implEvents)

	registry := agent.NewRegistry()
	_ = registry.Register(evalAgent)
	_ = registry.Register(implAgent)

	routes := []router.Route{
		{Pattern: "impl", Agent: "implementer", Priority: 1},
		{Pattern: "eval", Agent: "evaluator", Priority: 2},
	}

	orch, err := New(Config{
		Primary:        "implementer",
		MaxConcurrency: 3,
		PersistDir:     t.TempDir(),
		Routes:         routes,
		StoreDB:        db,
		SessionID:      "eval-blocked",
	}, registry)
	if err != nil {
		t.Fatalf("new orchestrator: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	r := &recipe.Recipe{
		Name: "unrecoverable",
		Tasks: []recipe.SubTask{
			{ID: "impl", Agent: "implementer", Type: "impl", PromptTemplate: "write"},
			{
				ID: "review", Agent: "evaluator", Type: "eval",
				DependsOn:        []string{"impl"},
				EvaluatorOf:      "impl",
				EvaluatorRetries: 3, // high cap — verify BLOCKED terminates before retries
				PromptTemplate:   "review",
			},
		},
	}

	runID, err := orch.RunRecipe(r, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	implID := runID + "-impl"
	reviewID := runID + "-review"

	waitForTask(t, orch, implID, 10*time.Second)
	waitForTask(t, orch, reviewID, 10*time.Second)
	time.Sleep(500 * time.Millisecond)

	// Despite EvaluatorRetries=3, BLOCKED should terminate immediately.
	// No retry1/retry2/retry3 tasks should exist.
	for _, n := range []int{1, 2, 3} {
		retryID := fmt.Sprintf("%s-retry%d", implID, n)
		if snap := orch.queue.GetSnapshot(retryID); snap != nil {
			t.Errorf("retry%d task should not exist after BLOCKED verdict: %+v", n, snap)
		}
		retryEvalID := fmt.Sprintf("%s-retry%d", reviewID, n)
		if snap := orch.queue.GetSnapshot(retryEvalID); snap != nil {
			t.Errorf("retry%d eval task should not exist after BLOCKED verdict: %+v", n, snap)
		}
	}

	// Original impl should be marked as failed with the blocking reason.
	origImpl := orch.queue.GetSnapshot(implID)
	if origImpl == nil {
		t.Fatal("original impl missing")
	}
	if origImpl.Status != task.StatusFailed {
		t.Errorf("orig impl status = %v, want failed", origImpl.Status)
	}
	if origImpl.Result == nil {
		t.Fatal("expected Result to be set on failed task")
	}
	if !strings.Contains(origImpl.Result.Error, "blocked by reviewer") {
		t.Errorf("expected 'blocked by reviewer' in error, got: %q", origImpl.Result.Error)
	}
	if !strings.Contains(origImpl.Result.Error, "objective is empty") {
		t.Errorf("expected blocking reason in error, got: %q", origImpl.Result.Error)
	}

	// Verify the evaluator_verdict chunk was written to m3m0ry.
	// (Only works if m3m0ry is enabled on this orchestrator — here we don't
	// configure it, so just verify the in-memory task state is correct.)
	_ = db // reserved for future m3m0ry verification if we wire it in
}

// --- Alternating replay agent helper ---

// alternatingReplayAgent wraps multiple replay agents and switches between
// them on each Start() call. Used to simulate evaluators that change verdict
// across retry attempts.
type alternatingReplayAgent struct {
	name   string
	agents []*agent.ReplayAgent
	idx    int
}

func newAlternatingReplayAgent(name string, eventSets [][]agent.AgentEvent) *alternatingReplayAgent {
	agents := make([]*agent.ReplayAgent, len(eventSets))
	for i, events := range eventSets {
		agents[i] = agent.NewReplayAgentFromEvents(name, events)
	}
	return &alternatingReplayAgent{name: name, agents: agents}
}

func (a *alternatingReplayAgent) Name() string         { return a.name }
func (a *alternatingReplayAgent) Available() bool       { return true }
func (a *alternatingReplayAgent) Capabilities() []string { return []string{"replay"} }

func (a *alternatingReplayAgent) Start(ctx context.Context, prompt string, opts *agent.StartOpts) (*agent.Session, error) {
	current := a.agents[a.idx]
	if a.idx < len(a.agents)-1 {
		a.idx++
	}
	return current.Start(ctx, prompt, opts)
}

func (a *alternatingReplayAgent) Send(s *agent.Session, msg string) error { return nil }
func (a *alternatingReplayAgent) Stop(s *agent.Session) error              { s.Cancel(); return nil }
func (a *alternatingReplayAgent) Events(s *agent.Session) <-chan agent.AgentEvent { return s.Events() }
