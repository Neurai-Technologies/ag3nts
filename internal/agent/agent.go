// Package agent defines the unified interface for executing prompts against
// any AI backend (CLI subprocess or HTTP API) and streaming structured events.
package agent

import (
	"context"
	"sync"
	"time"
)

// EventKind classifies the type of event emitted by an agent.
type EventKind int

const (
	EventInit       EventKind = iota // Agent session initialized
	EventMessage                     // Text output from the agent
	EventToolUse                     // Agent is calling a tool
	EventToolResult                  // Result of a tool invocation
	EventReasoning                   // Thinking/reasoning tokens
	EventCommand                     // Shell command execution
	EventFileChange                  // File modification by the agent
	EventError                       // Error during execution
	EventProgress                    // Streaming delta / partial token
	EventComplete                    // Final result with usage stats
)

// String returns the human-readable name of the event kind.
func (k EventKind) String() string {
	switch k {
	case EventInit:
		return "init"
	case EventMessage:
		return "message"
	case EventToolUse:
		return "tool_use"
	case EventToolResult:
		return "tool_result"
	case EventReasoning:
		return "reasoning"
	case EventCommand:
		return "command"
	case EventFileChange:
		return "file_change"
	case EventError:
		return "error"
	case EventProgress:
		return "progress"
	case EventComplete:
		return "complete"
	default:
		return "unknown"
	}
}

// AgentEvent is the normalized event type emitted by all agent implementations.
// Heterogeneous CLI/API outputs are parsed into this common structure.
type AgentEvent struct {
	Kind      EventKind      // What type of event this is
	Agent     string         // Source agent name (e.g. "claude", "gemini")
	SessionID string         // Agent session identifier
	TaskID    string         // Orchestrator task this event belongs to
	Content   string         // Primary text content
	Metadata  map[string]any // Provider-specific fields
	Timestamp time.Time      // When the event was produced
	Usage     *TokenUsage    // Token usage (typically on EventComplete)
}

// TokenUsage tracks token consumption and cost for a single event or session.
type TokenUsage struct {
	InputTokens  int     // Tokens in the prompt
	OutputTokens int     // Tokens in the response
	CachedTokens int     // Tokens served from cache
	TotalCost    float64 // Estimated cost in USD
}

// AgentStatus represents the lifecycle state of an agent session.
type AgentStatus int

const (
	StatusIdle    AgentStatus = iota // No active session
	StatusRunning                    // Actively processing
	StatusStopped                    // Gracefully stopped
	StatusFailed                     // Terminated with error
)

// String returns the human-readable name of the agent status.
func (s AgentStatus) String() string {
	switch s {
	case StatusIdle:
		return "idle"
	case StatusRunning:
		return "running"
	case StatusStopped:
		return "stopped"
	case StatusFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Session represents an active or completed interaction with an agent.
type Session struct {
	ID        string             // Unique session identifier
	Agent     string             // Agent name that owns this session
	Status    AgentStatus        // Current lifecycle state
	TaskID    string             // Orchestrator task ID (if task-driven)
	StartedAt time.Time          // When the session began
	events    chan AgentEvent     // Internal event channel
	cancel    context.CancelFunc // Cancels the session context

	mu        sync.Mutex
	resumeID  string // Provider-side session ID for conversation resume
}

// NewSession creates a session with the given parameters and an internal
// event channel of the specified buffer size.
func NewSession(id, agentName, taskID string, bufSize int, cancel context.CancelFunc) *Session {
	return &Session{
		ID:        id,
		Agent:     agentName,
		Status:    StatusRunning,
		TaskID:    taskID,
		StartedAt: time.Now(),
		events:    make(chan AgentEvent, bufSize),
		cancel:    cancel,
	}
}

// Events returns a receive-only channel for consuming session events.
func (s *Session) Events() <-chan AgentEvent {
	return s.events
}

// Emit sends an event into the session's event channel. Non-blocking if the
// channel buffer is full — the event is dropped.
func (s *Session) Emit(e AgentEvent) {
	select {
	case s.events <- e:
	default:
	}
}

// Close closes the event channel and updates the session status.
func (s *Session) Close(status AgentStatus) {
	s.Status = status
	close(s.events)
}

// Cancel cancels the session's context, signaling the agent to stop.
func (s *Session) Cancel() {
	if s.cancel != nil {
		s.cancel()
	}
}

// SetResumeID stores the provider-side session ID for conversation resume.
func (s *Session) SetResumeID(id string) {
	s.mu.Lock()
	s.resumeID = id
	s.mu.Unlock()
}

// ResumeID returns the provider-side session ID, or empty if not set.
func (s *Session) ResumeID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resumeID
}

// StartOpts configures how an agent session is launched.
type StartOpts struct {
	SessionID       string            // External session ID for resume (empty = new session)
	ResumeSessionID string            // Provider-side session ID for conversation continuity
	Model           string            // Model override (empty = agent default)
	WorkDir         string            // Working directory for the agent
	Context         string            // Prepended context from other agents' results
	TaskID          string            // Orchestrator task ID for event tagging
	Env             map[string]string // Extra environment variables
}

// Agent is the core interface that all agent backends implement.
// Subprocess agents (Claude, Gemini, Codex) and HTTP agents (Ollama,
// OpenAI-compatible) both conform to this contract.
type Agent interface {
	// Name returns the agent's unique identifier (e.g. "claude", "gemini").
	Name() string

	// Start launches a new session with the given prompt and options.
	// Returns a Session whose Events() channel streams AgentEvents.
	Start(ctx context.Context, prompt string, opts *StartOpts) (*Session, error)

	// Send continues an existing session with a follow-up message.
	Send(session *Session, message string) error

	// Stop gracefully terminates a running session.
	Stop(session *Session) error

	// Events returns the event channel for a session. Convenience alias
	// for session.Events() — allows the orchestrator to access events
	// without reaching into the Session struct.
	Events(session *Session) <-chan AgentEvent

	// Available reports whether the agent backend is reachable and ready.
	// For subprocess agents: binary exists. For HTTP agents: server responds.
	Available() bool

	// Capabilities returns the list of declared capabilities for routing.
	// Examples: "research", "code-generation", "large-context", "offline".
	Capabilities() []string
}
