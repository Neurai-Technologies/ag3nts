package agent

import (
	"context"
	"testing"
)

func TestEventKindString(t *testing.T) {
	tests := []struct {
		kind EventKind
		want string
	}{
		{EventInit, "init"},
		{EventMessage, "message"},
		{EventToolUse, "tool_use"},
		{EventError, "error"},
		{EventComplete, "complete"},
	}
	for _, tt := range tests {
		if got := tt.kind.String(); got != tt.want {
			t.Errorf("EventKind(%d).String() = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestAgentStatusString(t *testing.T) {
	if got := StatusRunning.String(); got != "running" {
		t.Errorf("StatusRunning.String() = %q, want %q", got, "running")
	}
}

func TestNewSession(t *testing.T) {
	sess := NewSession("test-123", "claude", "task-1", 10, nil)
	if sess.ID != "test-123" {
		t.Errorf("ID = %q, want %q", sess.ID, "test-123")
	}
	if sess.Agent != "claude" {
		t.Errorf("Agent = %q, want %q", sess.Agent, "claude")
	}
	if sess.Status != StatusRunning {
		t.Errorf("Status = %v, want %v", sess.Status, StatusRunning)
	}

	// Test Emit and Events.
	sess.Emit(AgentEvent{Kind: EventMessage, Content: "hello"})
	event := <-sess.Events()
	if event.Content != "hello" {
		t.Errorf("Event.Content = %q, want %q", event.Content, "hello")
	}
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()

	// Register should succeed.
	mock := &mockAgent{name: "test-agent"}
	if err := reg.Register(mock); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	// Duplicate should fail.
	if err := reg.Register(mock); err == nil {
		t.Error("Expected error on duplicate Register")
	}

	// Get should return the agent.
	if got := reg.Get("test-agent"); got == nil {
		t.Error("Get returned nil")
	}

	// Unknown should return nil.
	if got := reg.Get("unknown"); got != nil {
		t.Errorf("Get(unknown) = %v, want nil", got)
	}

	// Count and Names.
	if reg.Count() != 1 {
		t.Errorf("Count() = %d, want 1", reg.Count())
	}
	names := reg.Names()
	if len(names) != 1 || names[0] != "test-agent" {
		t.Errorf("Names() = %v, want [test-agent]", names)
	}
}

func TestHTTPAgentLocalhostValidation(t *testing.T) {
	// Localhost should succeed.
	_, err := NewHTTPAgent(HTTPConfig{
		Name: "test", Endpoint: "http://localhost:11434", Model: "llama3",
	})
	if err != nil {
		t.Errorf("localhost should be allowed: %v", err)
	}

	// 127.0.0.1 should succeed.
	_, err = NewHTTPAgent(HTTPConfig{
		Name: "test2", Endpoint: "http://127.0.0.1:8080", Model: "test",
	})
	if err != nil {
		t.Errorf("127.0.0.1 should be allowed: %v", err)
	}

	// External host should fail.
	_, err = NewHTTPAgent(HTTPConfig{
		Name: "test3", Endpoint: "http://api.openai.com/v1", Model: "gpt-4",
	})
	if err == nil {
		t.Error("external host should be rejected by SR-11")
	}
}

func TestParseClaude(t *testing.T) {
	// Real Claude stream-json format: content nested in message.content[].text
	line := []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}`)
	event := ParseClaude(line, "claude", "sess-1", "task-1")
	if event == nil {
		t.Fatal("ParseClaude returned nil")
	}
	if event.Kind != EventMessage {
		t.Errorf("Kind = %v, want EventMessage", event.Kind)
	}
	if event.Content != "Hello world" {
		t.Errorf("Content = %q, want %q", event.Content, "Hello world")
	}
}

func TestParseClaudeResult(t *testing.T) {
	line := []byte(`{"type":"result","result":"done","total_cost_usd":0.05,"usage":{"input_tokens":10,"output_tokens":20}}`)
	event := ParseClaude(line, "claude", "sess-1", "task-1")
	if event == nil {
		t.Fatal("ParseClaude result returned nil")
	}
	if event.Kind != EventComplete {
		t.Errorf("Kind = %v, want EventComplete", event.Kind)
	}
	if event.Usage == nil || event.Usage.OutputTokens != 20 {
		t.Errorf("Usage = %v, want 20 output tokens", event.Usage)
	}
}

func TestParseClaudeSkipsRateLimit(t *testing.T) {
	line := []byte(`{"type":"rate_limit_event","rate_limit_info":{}}`)
	event := ParseClaude(line, "claude", "", "")
	if event != nil {
		t.Error("rate_limit_event should return nil (skip)")
	}
}

func TestParseGemini(t *testing.T) {
	line := []byte(`{"type":"message","content":"test output","delta":false}`)
	event := ParseGemini(line, "gemini", "sess-1", "task-1")
	if event == nil {
		t.Fatal("ParseGemini returned nil")
	}
	if event.Kind != EventMessage {
		t.Errorf("Kind = %v, want EventMessage", event.Kind)
	}
}

func TestParseCodex(t *testing.T) {
	line := []byte(`{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"done"}}`)
	event := ParseCodex(line, "codex", "sess-1", "task-1")
	if event == nil {
		t.Fatal("ParseCodex returned nil")
	}
	if event.Kind != EventMessage {
		t.Errorf("Kind = %v, want EventMessage", event.Kind)
	}
	if event.Content != "done" {
		t.Errorf("Content = %q, want %q", event.Content, "done")
	}
}

func TestParseInvalidJSON(t *testing.T) {
	event := ParseClaude([]byte("not json"), "claude", "", "")
	if event != nil {
		t.Error("Expected nil for invalid JSON")
	}
}

// mockAgent implements Agent for testing.
type mockAgent struct {
	name string
}

func (m *mockAgent) Name() string                                                    { return m.name }
func (m *mockAgent) Start(_ context.Context, _ string, _ *StartOpts) (*Session, error) { return nil, nil }
func (m *mockAgent) Send(_ *Session, _ string) error                                  { return nil }
func (m *mockAgent) Stop(_ *Session) error                                            { return nil }
func (m *mockAgent) Events(_ *Session) <-chan AgentEvent                              { return nil }
func (m *mockAgent) Available() bool                                                  { return true }
func (m *mockAgent) Capabilities() []string                                           { return nil }
