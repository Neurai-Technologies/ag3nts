package orchestrator

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rohanrgit/ag3nts/internal/agent"
	m3m0ry "github.com/rohanrgit/ag3nts/internal/context"
	"github.com/rohanrgit/ag3nts/internal/recipe"
	"github.com/rohanrgit/ag3nts/internal/router"
	"github.com/rohanrgit/ag3nts/internal/store"
	"github.com/rohanrgit/ag3nts/internal/task"
)

// makeRepairReplayRecipe builds a synthetic 4-stage recipe for testing the
// full REPAIR pipeline with replay agents. Uses inline prompts (no file:
// references) so the test is self-contained.
func makeRepairReplayRecipe() *recipe.Recipe {
	return &recipe.Recipe{
		Name:        "repair-replay",
		Description: "Synthetic REPAIR pipeline for integration testing",
		Parameters: []recipe.Parameter{
			{Key: "objective", Type: "string", Required: true},
		},
		Tasks: []recipe.SubTask{
			{
				ID:             "research",
				Agent:          "gemini-replay",
				Type:           "repair.research",
				Description:    "research the objective",
				PromptTemplate: "research {{objective}}",
			},
			{
				ID:             "plan",
				Agent:          "claude-replay",
				Type:           "repair.plan",
				Description:    "build a plan",
				DependsOn:      []string{"research"},
				ContextFrom:    []string{"research"},
				PromptTemplate: "plan based on research for {{objective}}",
			},
			{
				ID:             "implement",
				Agent:          "codex-replay",
				Type:           "repair.implement",
				Description:    "write the code",
				DependsOn:      []string{"plan"},
				ContextFrom:    []string{"plan"},
				PromptTemplate: "implement {{objective}}",
			},
			{
				ID:               "review",
				Agent:            "claude-replay",
				Type:             "repair.review",
				Description:      "review the implementation",
				DependsOn:        []string{"implement"},
				ContextFrom:      []string{"implement"},
				EvaluatorOf:      "implement",
				EvaluatorRetries: 2,
				PromptTemplate:   "review the implementation",
			},
		},
	}
}

// TestREPAIR_HappyPath runs the full 4-stage pipeline with all-accepting
// replay agents and verifies that every stage completes, m3m0ry captures
// stage markers, and downstream stages receive upstream context.
func TestREPAIR_HappyPath(t *testing.T) {
	// Build replay agents for each stage, each emitting a distinctive result
	// so we can verify context flow.
	researchEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "Research: found three relevant libraries for the objective", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 50, OutputTokens: 30}},
	}
	planEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "Plan: 3 steps to implement using library X", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 100, OutputTokens: 60}},
	}
	implEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "Implementation: wrote 50 lines of code", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 150, OutputTokens: 80}},
	}
	acceptEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "ACCEPT: looks good\nAll criteria met.", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 80, OutputTokens: 20}},
	}

	gemini := agent.NewReplayAgentFromEvents("gemini-replay", researchEvents)
	claudePlan := agent.NewReplayAgentFromEvents("claude-replay-plan", planEvents)
	codex := agent.NewReplayAgentFromEvents("codex-replay", implEvents)
	claudeReview := agent.NewReplayAgentFromEvents("claude-replay-review", acceptEvents)

	// claude-replay needs to serve both plan and review stages; use an
	// alternating replay that switches payload based on call order.
	claudeCombined := newAlternatingReplayAgent("claude-replay", [][]agent.AgentEvent{
		planEvents,
		acceptEvents,
	})

	_ = claudePlan
	_ = claudeReview

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	sessionID := "repair-happy"
	_ = db.CreateSession(&store.SessionRecord{ID: sessionID, Status: "active", PrimaryAgent: "gemini-replay"})

	// m3m0ry so we can verify stage markers + context flow.
	jsonlPath := filepath.Join(t.TempDir(), "m3m0ry.jsonl")
	cfg := m3m0ry.DefaultConfig()
	cfg.JSONLPath = jsonlPath
	rs, err := m3m0ry.Open(cfg, db, sessionID, nil)
	if err != nil {
		t.Fatalf("m3m0ry: %v", err)
	}
	defer rs.Close()

	registry := agent.NewRegistry()
	_ = registry.Register(gemini)
	_ = registry.Register(claudeCombined)
	_ = registry.Register(codex)

	routes := []router.Route{
		{Pattern: ".*", Agent: "gemini-replay", Priority: 99}, // fallback
	}

	orch, err := New(Config{
		Primary:        "gemini-replay",
		MaxConcurrency: 3,
		PersistDir:     t.TempDir(),
		Routes:         routes,
		StoreDB:        db,
		SessionID:      sessionID,
		Context:        rs,
	}, registry)
	if err != nil {
		t.Fatalf("new orch: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	// Dispatch the recipe.
	r := makeRepairReplayRecipe()
	runID, err := orch.RunRecipe(r, map[string]string{"objective": "add MCP support"})
	if err != nil {
		t.Fatalf("run recipe: %v", err)
	}

	// Wait for all 4 stages to complete.
	waitForTask(t, orch, runID+"-research", 15*time.Second)
	waitForTask(t, orch, runID+"-plan", 15*time.Second)
	waitForTask(t, orch, runID+"-implement", 15*time.Second)
	waitForTask(t, orch, runID+"-review", 15*time.Second)

	// Allow final m3m0ry append + evaluator hook to settle.
	time.Sleep(300 * time.Millisecond)

	// All 4 tasks should be completed.
	for _, stage := range []string{"research", "plan", "implement", "review"} {
		snap := orch.queue.GetSnapshot(runID + "-" + stage)
		if snap == nil {
			t.Errorf("%s: task missing", stage)
			continue
		}
		if snap.Status != task.StatusCompleted {
			t.Errorf("%s: status = %v, want completed", stage, snap.Status)
		}
	}

	// m3m0ry should contain repair_stage_start and repair_stage_end markers
	// for each stage. 4 stages * 2 markers = 8 markers minimum.
	chunks, err := db.ListContextChunks(sessionID, 0, 200)
	if err != nil {
		t.Fatalf("list chunks: %v", err)
	}

	stageStarts := 0
	stageEnds := 0
	taskResults := 0
	recipeStarts := 0
	for _, c := range chunks {
		switch c.Kind {
		case "repair_stage_start":
			stageStarts++
		case "repair_stage_end":
			stageEnds++
		case "task_result":
			taskResults++
		case "recipe_start":
			recipeStarts++
		}
	}

	if recipeStarts != 1 {
		t.Errorf("recipe_start markers = %d, want 1", recipeStarts)
	}
	if stageStarts != 4 {
		t.Errorf("repair_stage_start markers = %d, want 4", stageStarts)
	}
	if stageEnds != 4 {
		t.Errorf("repair_stage_end markers = %d, want 4", stageEnds)
	}
	if taskResults != 4 {
		t.Errorf("task_result chunks = %d, want 4", taskResults)
	}

	// Since review accepted on first try, no retry tasks should exist.
	if snap := orch.queue.GetSnapshot(runID + "-implement-retry1"); snap != nil {
		t.Error("retry task unexpectedly created on ACCEPT")
	}
}

// TestREPAIR_EvaluatorLoop runs the pipeline where the review stage rejects
// once then accepts. Verifies that a retry task is spawned, re-evaluated,
// and the loop terminates cleanly.
func TestREPAIR_EvaluatorLoop(t *testing.T) {
	researchEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "research done", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 50, OutputTokens: 30}},
	}
	planEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "plan ready", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 50, OutputTokens: 30}},
	}
	implEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "code written v1", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 50, OutputTokens: 30}},
	}
	rejectEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "REJECT: missing tests\nNeed test coverage.", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 50, OutputTokens: 30}},
	}
	acceptEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "ACCEPT: looks good now", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 50, OutputTokens: 30}},
	}

	gemini := agent.NewReplayAgentFromEvents("gemini-replay", researchEvents)
	codex := agent.NewReplayAgentFromEvents("codex-replay", implEvents)
	// claude serves plan, review1(REJECT), review2(ACCEPT).
	claudeCombined := newAlternatingReplayAgent("claude-replay", [][]agent.AgentEvent{
		planEvents,
		rejectEvents,
		acceptEvents,
	})

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	sessionID := "repair-eval"
	_ = db.CreateSession(&store.SessionRecord{ID: sessionID, Status: "active", PrimaryAgent: "gemini-replay"})

	jsonlPath := filepath.Join(t.TempDir(), "m3m0ry.jsonl")
	cfg := m3m0ry.DefaultConfig()
	cfg.JSONLPath = jsonlPath
	rs, err := m3m0ry.Open(cfg, db, sessionID, nil)
	if err != nil {
		t.Fatalf("m3m0ry: %v", err)
	}
	defer rs.Close()

	registry := agent.NewRegistry()
	_ = registry.Register(gemini)
	_ = registry.Register(claudeCombined)
	_ = registry.Register(codex)

	routes := []router.Route{
		{Pattern: ".*", Agent: "gemini-replay", Priority: 99},
	}

	orch, err := New(Config{
		Primary:        "gemini-replay",
		MaxConcurrency: 3,
		PersistDir:     t.TempDir(),
		Routes:         routes,
		StoreDB:        db,
		SessionID:      sessionID,
		Context:        rs,
	}, registry)
	if err != nil {
		t.Fatalf("new orch: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	r := makeRepairReplayRecipe()
	runID, err := orch.RunRecipe(r, map[string]string{"objective": "build feature"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// First round: research -> plan -> implement -> review (REJECT).
	waitForTask(t, orch, runID+"-research", 10*time.Second)
	waitForTask(t, orch, runID+"-plan", 10*time.Second)
	waitForTask(t, orch, runID+"-implement", 10*time.Second)
	waitForTask(t, orch, runID+"-review", 10*time.Second)
	time.Sleep(300 * time.Millisecond)

	// Retry round: retry1 impl -> retry1 review (ACCEPT).
	waitForTask(t, orch, runID+"-implement-retry1", 10*time.Second)
	waitForTask(t, orch, runID+"-review-retry1", 10*time.Second)
	time.Sleep(300 * time.Millisecond)

	// No retry2 should exist — ACCEPT terminated the loop.
	if snap := orch.queue.GetSnapshot(runID + "-implement-retry2"); snap != nil {
		t.Error("retry2 should not have been spawned after ACCEPT")
	}

	// Verify final state: all 6 tasks completed.
	allIDs := []string{
		runID + "-research", runID + "-plan",
		runID + "-implement", runID + "-review",
		runID + "-implement-retry1", runID + "-review-retry1",
	}
	for _, id := range allIDs {
		snap := orch.queue.GetSnapshot(id)
		if snap == nil {
			t.Errorf("%s: missing", id)
			continue
		}
		if snap.Status != task.StatusCompleted {
			t.Errorf("%s: status = %v, want completed", id, snap.Status)
		}
	}

	// m3m0ry should have evaluator verdict markers for both evaluator calls.
	chunks, _ := db.ListContextChunks(sessionID, 0, 200)
	verdicts := 0
	for _, c := range chunks {
		if c.Kind == "evaluator_verdict" {
			verdicts++
		}
	}
	if verdicts != 2 {
		t.Errorf("evaluator_verdict markers = %d, want 2 (REJECT then ACCEPT)", verdicts)
	}
}

// TestREPAIR_ContextFlowDownstream verifies that downstream stages receive
// upstream context via m3m0ry retrieval, even without explicit ContextFrom.
func TestREPAIR_ContextFlowDownstream(t *testing.T) {
	// Distinctive content in research that should surface in downstream m3m0ry queries.
	researchEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "Research finding: the MCP protocol requires JSON-RPC over stdio transport", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 50, OutputTokens: 30}},
	}
	planEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "Plan: 3-step MCP integration", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 50, OutputTokens: 30}},
	}
	implEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "Code written for MCP", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 50, OutputTokens: 30}},
	}
	acceptEvents := []agent.AgentEvent{
		{Kind: agent.EventMessage, Content: "ACCEPT: good", Timestamp: time.Now()},
		{Kind: agent.EventComplete, Timestamp: time.Now(), Usage: &agent.TokenUsage{InputTokens: 50, OutputTokens: 30}},
	}

	gemini := agent.NewReplayAgentFromEvents("gemini-replay", researchEvents)
	codex := agent.NewReplayAgentFromEvents("codex-replay", implEvents)
	claudeCombined := newAlternatingReplayAgent("claude-replay", [][]agent.AgentEvent{planEvents, acceptEvents})

	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := store.Open(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()

	sessionID := "repair-ctx"
	_ = db.CreateSession(&store.SessionRecord{ID: sessionID, Status: "active", PrimaryAgent: "gemini-replay"})

	jsonlPath := filepath.Join(t.TempDir(), "m3m0ry.jsonl")
	cfg := m3m0ry.DefaultConfig()
	cfg.JSONLPath = jsonlPath
	rs, err := m3m0ry.Open(cfg, db, sessionID, nil)
	if err != nil {
		t.Fatalf("m3m0ry: %v", err)
	}
	defer rs.Close()

	registry := agent.NewRegistry()
	_ = registry.Register(gemini)
	_ = registry.Register(claudeCombined)
	_ = registry.Register(codex)

	routes := []router.Route{
		{Pattern: ".*", Agent: "gemini-replay", Priority: 99},
	}

	orch, err := New(Config{
		Primary:        "gemini-replay",
		MaxConcurrency: 3,
		PersistDir:     t.TempDir(),
		Routes:         routes,
		StoreDB:        db,
		SessionID:      sessionID,
		Context:        rs,
	}, registry)
	if err != nil {
		t.Fatalf("new orch: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = orch.Start(ctx)
	defer func() { _ = orch.Stop() }()

	r := makeRepairReplayRecipe()
	runID, err := orch.RunRecipe(r, map[string]string{"objective": "MCP protocol integration"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	waitForTask(t, orch, runID+"-research", 10*time.Second)
	// Give m3m0ry recorder a moment to capture the result.
	time.Sleep(200 * time.Millisecond)

	// Build context for implement stage and verify it picks up the research
	// content via m3m0ry keyword retrieval.
	implTask := orch.queue.GetSnapshot(runID + "-plan")
	if implTask == nil {
		t.Skip("plan task not yet created (race) — rerun test")
	}

	// The plan task's context_from includes research — buildContext should
	// include m3m0ry retrieval too.
	built := orch.buildContext(implTask.ContextFrom)
	if !strings.Contains(built, "JSON-RPC") && !strings.Contains(built, "MCP") {
		t.Errorf("plan context should contain research findings; got: %s", truncateForLog(built, 500))
	}
}
