package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// recordEntry is a single line in a recording JSONL file.
type recordEntry struct {
	Type  string     `json:"type"`            // "event" or "meta"
	Event AgentEvent `json:"event,omitempty"` // for type=event
	Meta  recordMeta `json:"meta,omitempty"`  // for type=meta
}

type recordMeta struct {
	Agent   string `json:"agent"`
	Prompt  string `json:"prompt"`
	TaskID  string `json:"task_id"`
}

// --- RecordingAgent ---

// RecordingAgent wraps a real Agent and records all events to a JSONL file.
// Used to capture agent I/O for replay testing.
type RecordingAgent struct {
	inner   Agent
	outPath string
	mu      sync.Mutex
}

// NewRecordingAgent creates a recording wrapper around an existing agent.
// Events are written to outPath as JSONL.
func NewRecordingAgent(inner Agent, outPath string) *RecordingAgent {
	return &RecordingAgent{
		inner:   inner,
		outPath: outPath,
	}
}

func (r *RecordingAgent) Name() string              { return r.inner.Name() }
func (r *RecordingAgent) Available() bool            { return r.inner.Available() }
func (r *RecordingAgent) Capabilities() []string     { return r.inner.Capabilities() }
func (r *RecordingAgent) Send(s *Session, msg string) error { return r.inner.Send(s, msg) }
func (r *RecordingAgent) Stop(s *Session) error      { return r.inner.Stop(s) }
func (r *RecordingAgent) Events(s *Session) <-chan AgentEvent { return s.Events() }

func (r *RecordingAgent) Start(ctx context.Context, prompt string, opts *StartOpts) (*Session, error) {
	sess, err := r.inner.Start(ctx, prompt, opts)
	if err != nil {
		return nil, err
	}

	// Open recording file.
	f, ferr := os.OpenFile(r.outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if ferr != nil {
		return sess, nil // recording fails silently — don't break the agent
	}

	// Write metadata line.
	taskID := ""
	if opts != nil {
		taskID = opts.TaskID
	}
	r.writeLine(f, recordEntry{
		Type: "meta",
		Meta: recordMeta{Agent: r.inner.Name(), Prompt: prompt, TaskID: taskID},
	})

	// Snapshot fields from the inner session before the replay goroutine can
	// mutate Status via sess.Close(). mu guards Status writes in Close().
	sess.mu.Lock()
	initialStatus := sess.Status
	sess.mu.Unlock()

	// Wrap the session's event channel to record events as they flow.
	wrappedCh := make(chan AgentEvent, 256)
	go func() {
		defer f.Close()
		defer close(wrappedCh)
		for event := range sess.Events() {
			r.writeLine(f, recordEntry{Type: "event", Event: event})
			wrappedCh <- event
		}
	}()

	// Return a new session that uses the wrapped channel.
	recorded := &Session{
		ID:        sess.ID,
		Agent:     sess.Agent,
		Status:    initialStatus,
		TaskID:    sess.TaskID,
		StartedAt: sess.StartedAt,
		events:    wrappedCh,
		cancel:    sess.cancel,
	}
	return recorded, nil
}

func (r *RecordingAgent) writeLine(f *os.File, entry recordEntry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	data = append(data, '\n')
	_, _ = f.Write(data)
}

// --- ReplayAgent ---

// ReplayAgent replays recorded events without making external API calls.
// Used for CI testing without API keys.
type ReplayAgent struct {
	name         string
	capabilities []string
	recordings   []recordEntry
	mu           sync.Mutex
}

// NewReplayAgent creates a replay agent from a JSONL recording file.
func NewReplayAgent(name, inputPath string) (*ReplayAgent, error) {
	f, err := os.Open(inputPath)
	if err != nil {
		return nil, fmt.Errorf("open recording %s: %w", inputPath, err)
	}
	defer f.Close()

	var entries []recordEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB line limit
	for scanner.Scan() {
		var entry recordEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue // skip malformed lines
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read recording: %w", err)
	}

	return &ReplayAgent{
		name:         name,
		capabilities: []string{"replay"},
		recordings:   entries,
	}, nil
}

// NewReplayAgentFromEvents creates a replay agent directly from events (for tests).
func NewReplayAgentFromEvents(name string, events []AgentEvent) *ReplayAgent {
	entries := make([]recordEntry, len(events))
	for i, e := range events {
		entries[i] = recordEntry{Type: "event", Event: e}
	}
	return &ReplayAgent{
		name:         name,
		capabilities: []string{"replay"},
		recordings:   entries,
	}
}

func (r *ReplayAgent) Name() string          { return r.name }
func (r *ReplayAgent) Available() bool        { return true }
func (r *ReplayAgent) Capabilities() []string { return r.capabilities }
func (r *ReplayAgent) Send(_ *Session, _ string) error { return nil }
func (r *ReplayAgent) Stop(s *Session) error  { s.Cancel(); return nil }
func (r *ReplayAgent) Events(s *Session) <-chan AgentEvent { return s.Events() }

func (r *ReplayAgent) Start(_ context.Context, _ string, opts *StartOpts) (*Session, error) {
	_, cancel := context.WithCancel(context.Background())
	taskID := ""
	if opts != nil {
		taskID = opts.TaskID
	}
	sessID := fmt.Sprintf("replay_%d", time.Now().UnixNano()%100000)
	sess := NewSession(sessID, r.name, taskID, 256, cancel)

	// Replay events in a goroutine.
	r.mu.Lock()
	events := make([]recordEntry, len(r.recordings))
	copy(events, r.recordings)
	r.mu.Unlock()

	go func() {
		for _, entry := range events {
			if entry.Type != "event" {
				continue
			}
			event := entry.Event
			event.Agent = r.name
			event.SessionID = sessID
			if taskID != "" {
				event.TaskID = taskID
			}
			sess.Emit(event)
		}
		sess.Close(StatusStopped)
	}()

	return sess, nil
}
