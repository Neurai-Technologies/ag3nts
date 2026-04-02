package task

import (
	"os"
	"path/filepath"
	"testing"
)

func TestQueueAddAndGet(t *testing.T) {
	q := NewQueue("")
	task := &Task{ID: "T1", Description: "test", Type: "research"}
	if err := q.Add(task); err != nil {
		t.Fatal(err)
	}
	got := q.Get("T1")
	if got == nil || got.Description != "test" {
		t.Errorf("Get(T1) = %v", got)
	}
}

func TestQueueDuplicateID(t *testing.T) {
	q := NewQueue("")
	_ = q.Add(&Task{ID: "T1", Type: "a"})
	if err := q.Add(&Task{ID: "T1", Type: "b"}); err == nil {
		t.Error("Expected error for duplicate ID")
	}
}

func TestQueueReady(t *testing.T) {
	q := NewQueue("")
	_ = q.Add(&Task{ID: "T1", Type: "a", Status: StatusPending})
	_ = q.Add(&Task{ID: "T2", Type: "b", Status: StatusPending, DependsOn: []string{"T1"}})

	// Only T1 should be ready (T2 depends on T1).
	ready := q.Ready()
	if len(ready) != 1 || ready[0].ID != "T1" {
		t.Errorf("Ready() = %v, want [T1]", ready)
	}

	// Complete T1.
	_ = q.Update("T1", StatusCompleted, nil)

	// Now T2 should be ready.
	ready = q.Ready()
	if len(ready) != 1 || ready[0].ID != "T2" {
		t.Errorf("Ready() after T1 complete = %v, want [T2]", ready)
	}
}

func TestQueuePersistence(t *testing.T) {
	dir := t.TempDir()
	taskDir := filepath.Join(dir, "tasks")

	// Create and save.
	q1 := NewQueue(taskDir)
	_ = q1.Add(&Task{ID: "T1", Type: "test", Description: "persist me"})
	_ = q1.Save()

	// Verify file exists.
	if _, err := os.Stat(filepath.Join(taskDir, "T1.json")); err != nil {
		t.Fatalf("Task file not saved: %v", err)
	}

	// Load into fresh queue.
	q2 := NewQueue(taskDir)
	if err := q2.Load(); err != nil {
		t.Fatal(err)
	}
	got := q2.Get("T1")
	if got == nil || got.Description != "persist me" {
		t.Errorf("Loaded task = %v", got)
	}
}

func TestQueueCount(t *testing.T) {
	q := NewQueue("")
	_ = q.Add(&Task{ID: "T1", Status: StatusPending})
	_ = q.Add(&Task{ID: "T2", Status: StatusRunning})
	_ = q.Add(&Task{ID: "T3", Status: StatusCompleted})

	counts := q.Count()
	if counts[StatusPending] != 1 {
		t.Errorf("Pending = %d, want 1", counts[StatusPending])
	}
	if counts[StatusRunning] != 1 {
		t.Errorf("Running = %d, want 1", counts[StatusRunning])
	}
	if counts[StatusCompleted] != 1 {
		t.Errorf("Completed = %d, want 1", counts[StatusCompleted])
	}
}

func TestStatusString(t *testing.T) {
	if StatusPending.String() != "pending" {
		t.Error("StatusPending.String() wrong")
	}
	if StatusCompleted.String() != "completed" {
		t.Error("StatusCompleted.String() wrong")
	}
}
