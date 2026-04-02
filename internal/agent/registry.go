package agent

import (
	"fmt"
	"sync"
)

// Registry holds all configured agents, keyed by name.
// Thread-safe for concurrent access from the orchestrator and TUI.
type Registry struct {
	agents map[string]Agent
	mu     sync.RWMutex
}

// NewRegistry creates an empty agent registry.
func NewRegistry() *Registry {
	return &Registry{
		agents: make(map[string]Agent),
	}
}

// Register adds an agent to the registry. Returns an error if an agent
// with the same name is already registered.
func (r *Registry) Register(a Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := a.Name()
	if _, exists := r.agents[name]; exists {
		return fmt.Errorf("agent %q already registered", name)
	}
	r.agents[name] = a
	return nil
}

// Get returns the agent with the given name, or nil if not found.
func (r *Registry) Get(name string) Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[name]
}

// List returns all registered agents in no guaranteed order.
func (r *Registry) List() []Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]Agent, 0, len(r.agents))
	for _, a := range r.agents {
		agents = append(agents, a)
	}
	return agents
}

// Available returns only agents whose Available() method returns true.
func (r *Registry) Available() []Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var available []Agent
	for _, a := range r.agents {
		if a.Available() {
			available = append(available, a)
		}
	}
	return available
}

// Names returns the names of all registered agents.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	return names
}

// Count returns the number of registered agents.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}
