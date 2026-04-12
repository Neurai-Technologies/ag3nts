// Package task defines the Task struct and queue for managing work items
// with states, dependencies, and persistence.
package task

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/rohanrgit/ag3nts/internal/agent"
)

// validateLocalName checks that a name is safe for use in file paths.
// M-2 fix: prevents path traversal via task IDs or agent names.
func validateLocalName(name string) error {
	if !filepath.IsLocal(name) {
		return fmt.Errorf("invalid name %q: must be local (no path separators or traversal)", name)
	}
	return nil
}

// Status represents the lifecycle state of a task.
type Status int

const (
	StatusPending   Status = iota // Waiting for dependencies
	StatusQueued                  // Dependencies met, ready to dispatch
	StatusRunning                 // Assigned to an agent, in progress
	StatusCompleted               // Successfully finished
	StatusFailed                  // Terminated with error
)

// String returns the human-readable name of the task status.
func (s Status) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusQueued:
		return "queued"
	case StatusRunning:
		return "running"
	case StatusCompleted:
		return "completed"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Task represents a unit of work to be dispatched to an agent.
type Task struct {
	ID          string        `json:"id"`
	Description string        `json:"description"`
	Type        string        `json:"type"`                  // matches routing patterns
	Agent       string        `json:"agent,omitempty"`       // explicit agent override (empty = use router)
	Status      Status        `json:"status"`
	DependsOn   []string      `json:"depends_on,omitempty"`  // task IDs that must complete first
	ContextFrom []string      `json:"context_from,omitempty"` // task IDs whose results to inject as context
	Timeout     time.Duration `json:"timeout,omitempty"`     // 0 = no timeout
	Result      *Result       `json:"result,omitempty"`
	CreatedAt   time.Time     `json:"created_at"`
	StartedAt   time.Time     `json:"started_at,omitempty"`
	CompletedAt time.Time     `json:"completed_at,omitempty"`
}

// Result holds the output of a completed or failed task.
type Result struct {
	Output   string             `json:"output"`
	Events   []agent.AgentEvent `json:"events,omitempty"`
	Usage    *agent.TokenUsage  `json:"usage,omitempty"`
	Duration time.Duration      `json:"duration"`
	Error    string             `json:"error,omitempty"`
}

// Queue manages tasks with thread-safe access and optional disk persistence.
type Queue struct {
	tasks map[string]*Task
	order []string // insertion order for deterministic listing
	mu    sync.RWMutex
	dir   string // persistence directory (empty = no persistence)
}

// NewQueue creates a task queue. If persistDir is non-empty, tasks are
// saved to and loaded from that directory as JSON files.
func NewQueue(persistDir string) *Queue {
	return &Queue{
		tasks: make(map[string]*Task),
		dir:   persistDir,
	}
}

// Add inserts a new task into the queue. The task must have a unique ID.
func (q *Queue) Add(t *Task) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if t.ID == "" {
		return fmt.Errorf("task ID is required")
	}
	if err := validateLocalName(t.ID); err != nil {
		return fmt.Errorf("task ID: %w", err)
	}
	if _, exists := q.tasks[t.ID]; exists {
		return fmt.Errorf("task %q already exists", t.ID)
	}

	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	if t.Status == 0 {
		t.Status = StatusPending
	}

	q.tasks[t.ID] = t
	q.order = append(q.order, t.ID)

	if q.dir != "" {
		_ = q.persistTask(t)
	}
	return nil
}

// Get returns the task with the given ID, or nil if not found.
//
// NOTE: The returned pointer references a task that may be mutated
// concurrently. Callers should not read mutable fields (Status, Result)
// without synchronization. Use GetStatus or GetSnapshot instead.
func (q *Queue) Get(id string) *Task {
	q.mu.RLock()
	defer q.mu.RUnlock()
	return q.tasks[id]
}

// GetStatus returns the current status of a task under the queue lock.
// Returns StatusPending and false if the task does not exist.
func (q *Queue) GetStatus(id string) (Status, bool) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	t, ok := q.tasks[id]
	if !ok {
		return StatusPending, false
	}
	return t.Status, true
}

// GetSnapshot returns a value copy of the task, safe to read from any
// goroutine. Returns nil if the task does not exist.
func (q *Queue) GetSnapshot(id string) *Task {
	q.mu.RLock()
	defer q.mu.RUnlock()
	t, ok := q.tasks[id]
	if !ok {
		return nil
	}
	snapshot := *t
	return &snapshot
}

// List returns all tasks in insertion order.
func (q *Queue) List() []*Task {
	q.mu.RLock()
	defer q.mu.RUnlock()

	tasks := make([]*Task, 0, len(q.order))
	for _, id := range q.order {
		if t, ok := q.tasks[id]; ok {
			tasks = append(tasks, t)
		}
	}
	return tasks
}

// Ready returns tasks whose dependencies are all satisfied (completed)
// and whose status is Pending. These are eligible for dispatch.
func (q *Queue) Ready() []*Task {
	q.mu.RLock()
	defer q.mu.RUnlock()

	var ready []*Task
	for _, id := range q.order {
		t := q.tasks[id]
		if t.Status != StatusPending {
			continue
		}
		if q.depsComplete(t) {
			ready = append(ready, t)
		}
	}
	return ready
}

// Update changes a task's status and optionally sets its result.
// Persists the change to disk if persistence is enabled.
func (q *Queue) Update(id string, status Status, result *Result) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	t, ok := q.tasks[id]
	if !ok {
		return fmt.Errorf("task %q not found", id)
	}

	t.Status = status
	if result != nil {
		t.Result = result
	}

	now := time.Now()
	switch status {
	case StatusRunning:
		t.StartedAt = now
	case StatusCompleted, StatusFailed:
		t.CompletedAt = now
	}

	if q.dir != "" {
		_ = q.persistTask(t)
	}
	return nil
}

// Count returns the number of tasks in each status.
func (q *Queue) Count() map[Status]int {
	q.mu.RLock()
	defer q.mu.RUnlock()

	counts := make(map[Status]int)
	for _, t := range q.tasks {
		counts[t.Status]++
	}
	return counts
}

// Save persists all tasks to disk. No-op if persistence is disabled.
func (q *Queue) Save() error {
	if q.dir == "" {
		return nil
	}

	q.mu.RLock()
	defer q.mu.RUnlock()

	for _, t := range q.tasks {
		if err := q.persistTask(t); err != nil {
			return err
		}
	}
	return nil
}

// Load restores tasks from disk. No-op if persistence is disabled.
func (q *Queue) Load() error {
	if q.dir == "" {
		return nil
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	entries, err := os.ReadDir(q.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read task dir: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(q.dir, entry.Name()))
		if err != nil {
			continue
		}
		var t Task
		if err := json.Unmarshal(data, &t); err != nil {
			continue
		}
		// L-2 fix: skip duplicates.
		if _, exists := q.tasks[t.ID]; exists {
			continue
		}
		q.tasks[t.ID] = &t
		q.order = append(q.order, t.ID)
	}
	return nil
}

// depsComplete checks if all tasks in t.DependsOn have StatusCompleted.
// Must be called with at least a read lock held.
func (q *Queue) depsComplete(t *Task) bool {
	for _, depID := range t.DependsOn {
		dep, ok := q.tasks[depID]
		if !ok || dep.Status != StatusCompleted {
			return false
		}
	}
	return true
}

// persistTask writes a single task to disk as a JSON file.
// Must be called with at least a read lock held.
func (q *Queue) persistTask(t *Task) error {
	if err := os.MkdirAll(q.dir, 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(q.dir, t.ID+".json"), data, 0600)
}
