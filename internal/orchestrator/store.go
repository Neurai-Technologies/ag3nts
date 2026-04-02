package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rohanrgit/ag3nts/internal/task"
)

// Store manages persistent storage of task results for context sharing
// between agents. Results are saved as JSON files and cached in memory.
type Store struct {
	dir   string
	cache map[string]*task.Result
	mu    sync.RWMutex
}

// NewStore creates a result store. If dir is empty, results are only
// kept in memory (no persistence).
func NewStore(dir string) *Store {
	return &Store{
		dir:   dir,
		cache: make(map[string]*task.Result),
	}
}

// SaveResult stores a task result both in memory and on disk.
func (s *Store) SaveResult(taskID string, result *task.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cache[taskID] = result

	if s.dir == "" {
		return
	}

	_ = os.MkdirAll(s.dir, 0700)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(s.dir, taskID+".json"), data, 0600)
}

// GetResult retrieves a task result, checking the memory cache first,
// then falling back to disk.
func (s *Store) GetResult(taskID string) *task.Result {
	s.mu.RLock()
	if r, ok := s.cache[taskID]; ok {
		s.mu.RUnlock()
		return r
	}
	s.mu.RUnlock()

	if s.dir == "" {
		return nil
	}

	// Try loading from disk.
	data, err := os.ReadFile(filepath.Join(s.dir, taskID+".json"))
	if err != nil {
		return nil
	}
	var result task.Result
	if err := json.Unmarshal(data, &result); err != nil {
		return nil
	}

	// Cache for future lookups.
	s.mu.Lock()
	s.cache[taskID] = &result
	s.mu.Unlock()

	return &result
}

// AppendFinding adds a line to the shared findings file that any agent
// can read for cross-agent context. Thread-safe, append-only.
func (s *Store) AppendFinding(source, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := fmt.Sprintf("[%s] %s: %s\n",
		time.Now().Format("15:04:05"), source, strings.TrimSpace(content))

	if s.dir == "" {
		// No persistence — finding only lives in memory for this session.
		return nil
	}

	contextDir := filepath.Join(filepath.Dir(s.dir), "context")
	_ = os.MkdirAll(contextDir, 0700)
	f, err := os.OpenFile(filepath.Join(contextDir, "shared.md"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("open shared findings: %w", err)
	}
	defer f.Close()

	_, err = f.WriteString(entry)
	return err
}

// ReadFindings returns the contents of the shared findings file.
func (s *Store) ReadFindings() string {
	if s.dir == "" {
		return ""
	}

	contextDir := filepath.Join(filepath.Dir(s.dir), "context")
	data, err := os.ReadFile(filepath.Join(contextDir, "shared.md"))
	if err != nil {
		return ""
	}
	return string(data)
}

// SaveSession persists an agent session ID for resume across restarts.
func (s *Store) SaveSession(agentName, sessionID string) error {
	if s.dir == "" {
		return nil
	}

	sessDir := filepath.Join(filepath.Dir(s.dir), "sessions")
	_ = os.MkdirAll(sessDir, 0700)

	data := map[string]string{
		"session_id": sessionID,
		"saved_at":   time.Now().Format(time.RFC3339),
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	return os.WriteFile(filepath.Join(sessDir, agentName+".json"), b, 0600)
}

// LoadSession retrieves a saved session ID for the given agent.
func (s *Store) LoadSession(agentName string) string {
	if s.dir == "" {
		return ""
	}

	sessDir := filepath.Join(filepath.Dir(s.dir), "sessions")
	data, err := os.ReadFile(filepath.Join(sessDir, agentName+".json"))
	if err != nil {
		return ""
	}

	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return ""
	}
	return m["session_id"]
}
