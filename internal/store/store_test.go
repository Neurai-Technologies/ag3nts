package store

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(Config{Path: dbPath})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenAndMigrate(t *testing.T) {
	db := openTestDB(t)

	// Verify schema version.
	var version int
	if err := db.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != currentSchemaVersion {
		t.Errorf("version = %d, want %d", version, currentSchemaVersion)
	}
}

func TestOpenIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")

	// Open twice — second open should not fail.
	db1, err := Open(Config{Path: dbPath})
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	db1.Close()

	db2, err := Open(Config{Path: dbPath})
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	db2.Close()
}

func TestSessionCRUD(t *testing.T) {
	db := openTestDB(t)

	// Create.
	rec := &SessionRecord{
		ID:           "s-001",
		Name:         "test session",
		WorkingDir:   "/tmp/test",
		PrimaryAgent: "claude",
		Status:       "active",
	}
	if err := db.CreateSession(rec); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Get.
	got, err := db.GetSession("s-001")
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if got == nil {
		t.Fatal("session not found")
	}
	if got.Name != "test session" {
		t.Errorf("name = %q, want %q", got.Name, "test session")
	}
	if got.PrimaryAgent != "claude" {
		t.Errorf("primary_agent = %q, want %q", got.PrimaryAgent, "claude")
	}

	// Get nonexistent.
	missing, err := db.GetSession("nonexistent")
	if err != nil {
		t.Fatalf("get missing: %v", err)
	}
	if missing != nil {
		t.Error("expected nil for nonexistent session")
	}

	// Update status.
	if err := db.UpdateSessionStatus("s-001", "completed"); err != nil {
		t.Fatalf("update status: %v", err)
	}
	got, _ = db.GetSession("s-001")
	if got.Status != "completed" {
		t.Errorf("status = %q, want %q", got.Status, "completed")
	}

	// List.
	sessions, err := db.ListSessions(10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("len = %d, want 1", len(sessions))
	}
}

func TestTokenAccumulation(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateSession(&SessionRecord{ID: "s-tok", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Add tokens multiple times.
	_ = db.AddTokenUsage("s-tok", 100, 50, 10, 0.01)
	_ = db.AddTokenUsage("s-tok", 200, 100, 20, 0.02)
	_ = db.AddTokenUsage("s-tok", 300, 150, 30, 0.03)

	got, _ := db.GetSession("s-tok")
	if got.TotalInputTokens != 600 {
		t.Errorf("input = %d, want 600", got.TotalInputTokens)
	}
	if got.TotalOutputTokens != 300 {
		t.Errorf("output = %d, want 300", got.TotalOutputTokens)
	}
	if got.TotalCachedTokens != 60 {
		t.Errorf("cached = %d, want 60", got.TotalCachedTokens)
	}
	// Float comparison with tolerance.
	if got.TotalCostUSD < 0.059 || got.TotalCostUSD > 0.061 {
		t.Errorf("cost = %f, want ~0.06", got.TotalCostUSD)
	}
}

func TestTaskCRUD(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-task", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Create task.
	rec := &TaskRecord{
		ID:          "t-001",
		SessionID:   "s-task",
		Agent:       "gemini",
		Type:        "research",
		Description: "find patterns in goose",
		Status:      "pending",
		DependsOn:   []string{},
		ContextFrom: []string{},
	}
	if err := db.CreateTask(rec); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// Get.
	got, err := db.GetTask("t-001")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if got == nil {
		t.Fatal("task not found")
	}
	if got.Description != "find patterns in goose" {
		t.Errorf("description = %q", got.Description)
	}

	// Update status to running.
	_ = db.UpdateTaskStatus("t-001", "running")
	got, _ = db.GetTask("t-001")
	if got.Status != "running" {
		t.Errorf("status = %q, want running", got.Status)
	}
	if got.StartedAt == nil {
		t.Error("started_at should be set")
	}

	// Update result.
	_ = db.UpdateTaskResult("t-001", "gemini", "found 14 patterns", "",
		TokenRecord{InputTokens: 500, OutputTokens: 200, CostUSD: 0.005}, 3200)
	got, _ = db.GetTask("t-001")
	if got.Status != "completed" {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if got.ResultOutput != "found 14 patterns" {
		t.Errorf("output = %q", got.ResultOutput)
	}
	if got.DurationMs != 3200 {
		t.Errorf("duration = %d, want 3200", got.DurationMs)
	}

	// List tasks for session.
	tasks, err := db.ListTasks("s-task")
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("len = %d, want 1", len(tasks))
	}
}

func TestTaskDependencies(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-dep", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Create task with dependencies.
	_ = db.CreateTask(&TaskRecord{
		ID:          "t-dep",
		SessionID:   "s-dep",
		Status:      "pending",
		DependsOn:   []string{"t-001", "t-002"},
		ContextFrom: []string{"t-001"},
	})

	got, _ := db.GetTask("t-dep")
	if len(got.DependsOn) != 2 {
		t.Errorf("deps = %v, want 2 items", got.DependsOn)
	}
	if len(got.ContextFrom) != 1 {
		t.Errorf("context = %v, want 1 item", got.ContextFrom)
	}
}

func TestEventInsertAndQuery(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-evt", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Insert events.
	for i := 1; i <= 5; i++ {
		_ = db.InsertEvent(&EventRecord{
			SessionID: "s-evt",
			TaskID:    "t-001",
			Agent:     "claude",
			Kind:      "message",
			Content:   "msg",
			Metadata:  "{}",
			Seq:       int64(i),
		})
	}

	// Query since seq 2.
	events, err := db.EventsSince("s-evt", 2, 100)
	if err != nil {
		t.Fatalf("events since: %v", err)
	}
	if len(events) != 3 {
		t.Errorf("len = %d, want 3 (seq 3,4,5)", len(events))
	}
	if events[0].Seq != 3 {
		t.Errorf("first seq = %d, want 3", events[0].Seq)
	}

	// Count.
	count, err := db.EventCount("s-evt")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
}

func TestTokensByAgent(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-agents", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Create tasks for different agents.
	_ = db.CreateTask(&TaskRecord{ID: "t-c1", SessionID: "s-agents", Agent: "claude", Status: "completed"})
	_ = db.UpdateTaskResult("t-c1", "claude", "ok", "", TokenRecord{InputTokens: 100, OutputTokens: 50, CostUSD: 0.01}, 1000)

	_ = db.CreateTask(&TaskRecord{ID: "t-c2", SessionID: "s-agents", Agent: "claude", Status: "completed"})
	_ = db.UpdateTaskResult("t-c2", "claude", "ok", "", TokenRecord{InputTokens: 200, OutputTokens: 100, CostUSD: 0.02}, 2000)

	_ = db.CreateTask(&TaskRecord{ID: "t-g1", SessionID: "s-agents", Agent: "gemini", Status: "completed"})
	_ = db.UpdateTaskResult("t-g1", "gemini", "ok", "", TokenRecord{InputTokens: 500, OutputTokens: 200, CostUSD: 0.005}, 3000)

	summaries, err := db.TokensByAgent("s-agents")
	if err != nil {
		t.Fatalf("tokens by agent: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("len = %d, want 2", len(summaries))
	}

	// Claude should be first (higher cost).
	if summaries[0].Agent != "claude" {
		t.Errorf("first agent = %q, want claude", summaries[0].Agent)
	}
	if summaries[0].InputTokens != 300 {
		t.Errorf("claude input = %d, want 300", summaries[0].InputTokens)
	}
	if summaries[0].TaskCount != 2 {
		t.Errorf("claude tasks = %d, want 2", summaries[0].TaskCount)
	}
}

func TestConcurrentReads(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-conc", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Concurrent reads should not panic or error with WAL mode.
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = db.GetSession("s-conc")
			_, _ = db.ListSessions(10)
		}()
	}
	wg.Wait()
}

func TestConcurrentWrites(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-cw", Status: "active"}); err != nil {
		t.Fatal(err)
	}

	// Concurrent token additions should serialize correctly.
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = db.AddTokenUsage("s-cw", 10, 5, 1, 0.001)
		}()
	}
	wg.Wait()

	got, _ := db.GetSession("s-cw")
	if got.TotalInputTokens != 500 {
		t.Errorf("input = %d, want 500 (50 * 10)", got.TotalInputTokens)
	}
}

func TestFailedTask(t *testing.T) {
	db := openTestDB(t)
	if err := db.CreateSession(&SessionRecord{ID: "s-fail", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	_ = db.CreateTask(&TaskRecord{ID: "t-fail", SessionID: "s-fail", Status: "pending"})

	_ = db.UpdateTaskResult("t-fail", "codex", "", "rate limited",
		TokenRecord{InputTokens: 50}, 500)

	got, _ := db.GetTask("t-fail")
	if got.Status != "failed" {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.ResultError != "rate limited" {
		t.Errorf("error = %q", got.ResultError)
	}
}

func TestTimestampParsing(t *testing.T) {
	db := openTestDB(t)

	now := time.Now().UTC()
	db.CreateSession(&SessionRecord{
		ID:        "s-ts",
		Status:    "active",
		CreatedAt: now,
	})

	got, _ := db.GetSession("s-ts")
	// Should be within 2 seconds (RFC3339 truncates sub-second).
	diff := got.CreatedAt.Sub(now)
	if diff < -2*time.Second || diff > 2*time.Second {
		t.Errorf("created_at diff = %v, want within 2s", diff)
	}
}
