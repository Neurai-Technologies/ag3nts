package router

import (
	"context"
	"testing"

	"github.com/rohanrgit/ag3nts/internal/agent"
)

func TestResolveMatchesPattern(t *testing.T) {
	reg := agent.NewRegistry()
	_ = reg.Register(&mockAgent{name: "gemini"})
	_ = reg.Register(&mockAgent{name: "claude"})

	r, err := New([]Route{
		{Pattern: "research|explore", Agent: "gemini", Priority: 1},
	}, "claude", reg)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.Resolve("research", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "gemini" {
		t.Errorf("Resolve(research) = %q, want gemini", got)
	}
}

func TestResolveFallsBackToPrimary(t *testing.T) {
	reg := agent.NewRegistry()
	_ = reg.Register(&mockAgent{name: "claude"})

	r, err := New([]Route{
		{Pattern: "research", Agent: "gemini", Priority: 1}, // gemini not registered
	}, "claude", reg)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.Resolve("unknown-type", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "claude" {
		t.Errorf("Resolve(unknown) = %q, want claude (primary)", got)
	}
}

func TestResolveUserOverride(t *testing.T) {
	reg := agent.NewRegistry()
	_ = reg.Register(&mockAgent{name: "gemini"})
	_ = reg.Register(&mockAgent{name: "claude"})

	r, err := New([]Route{
		{Pattern: "research", Agent: "gemini", Priority: 1},
	}, "claude", reg)
	if err != nil {
		t.Fatal(err)
	}

	// User override should take precedence over pattern match.
	got, err := r.Resolve("research", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if got != "claude" {
		t.Errorf("Resolve with override = %q, want claude", got)
	}
}

func TestResolveFallbackAgent(t *testing.T) {
	reg := agent.NewRegistry()
	_ = reg.Register(&mockAgent{name: "claude"})
	_ = reg.Register(&mockAgent{name: "gemini", unavail: true}) // gemini unavailable

	r, err := New([]Route{
		{Pattern: "research", Agent: "gemini", Fallback: "claude", Priority: 1},
	}, "claude", reg)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.Resolve("research", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "claude" {
		t.Errorf("Resolve(research) = %q, want claude (fallback)", got)
	}
}

func TestResolvePriorityOrder(t *testing.T) {
	reg := agent.NewRegistry()
	_ = reg.Register(&mockAgent{name: "gemini"})
	_ = reg.Register(&mockAgent{name: "codex"})
	_ = reg.Register(&mockAgent{name: "claude"})

	r, err := New([]Route{
		{Pattern: "code", Agent: "codex", Priority: 10},
		{Pattern: "code", Agent: "gemini", Priority: 1}, // lower = higher priority
	}, "claude", reg)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.Resolve("code", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "gemini" {
		t.Errorf("Resolve(code) = %q, want gemini (higher priority)", got)
	}
}

func TestSetPrimary(t *testing.T) {
	reg := agent.NewRegistry()
	_ = reg.Register(&mockAgent{name: "claude"})
	_ = reg.Register(&mockAgent{name: "gemini"})

	r, err := New(nil, "claude", reg)
	if err != nil {
		t.Fatal(err)
	}

	if r.Primary() != "claude" {
		t.Errorf("Primary() = %q, want claude", r.Primary())
	}

	if err := r.SetPrimary("gemini"); err != nil {
		t.Fatal(err)
	}
	if r.Primary() != "gemini" {
		t.Errorf("Primary() = %q, want gemini", r.Primary())
	}

	if err := r.SetPrimary("nonexistent"); err == nil {
		t.Error("Expected error for nonexistent agent")
	}
}

func TestInvalidPattern(t *testing.T) {
	reg := agent.NewRegistry()
	_, err := New([]Route{
		{Pattern: "[invalid", Agent: "test", Priority: 1},
	}, "test", reg)
	if err == nil {
		t.Error("Expected error for invalid regex pattern")
	}
}

type mockAgent struct {
	name   string
	avail  bool // when true → available; when false with unavail set → unavailable
	unavail bool // explicitly mark as unavailable
}

func (m *mockAgent) Name() string { return m.name }
func (m *mockAgent) Start(_ context.Context, _ string, _ *agent.StartOpts) (*agent.Session, error) {
	return nil, nil
}
func (m *mockAgent) Send(_ *agent.Session, _ string) error           { return nil }
func (m *mockAgent) Stop(_ *agent.Session) error                     { return nil }
func (m *mockAgent) Events(_ *agent.Session) <-chan agent.AgentEvent { return nil }
func (m *mockAgent) Available() bool                                 { return !m.unavail }
func (m *mockAgent) Capabilities() []string                          { return nil }
