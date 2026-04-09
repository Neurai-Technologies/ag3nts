package tools

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// ToolSet represents a dynamically registered set of tools.
// Tool-sets can be MCP servers, scripts, or builtin tool groups.
// Recipes reference tool-sets by name in their `tools` field.
type ToolSet struct {
	Name        string   `toml:"name"`
	Type        string   `toml:"type"`        // "mcp", "builtin", "script"
	Command     string   `toml:"command"`      // binary to run (for mcp/script)
	Args        []string `toml:"args"`         // arguments
	Description string   `toml:"description"`
	Tools       []string `toml:"tools"`        // exposed tool names (discovered or declared)
}

// ToolManager manages dynamic tool-set registration and lifecycle.
type ToolManager struct {
	sets map[string]*ToolSet
	mu   sync.RWMutex
}

// NewToolManager creates an empty tool manager.
func NewToolManager() *ToolManager {
	return &ToolManager{
		sets: make(map[string]*ToolSet),
	}
}

// Register adds a tool-set. If one with the same name exists, it's replaced.
func (m *ToolManager) Register(ts *ToolSet) error {
	if ts.Name == "" {
		return fmt.Errorf("tool-set name is required")
	}
	if ts.Type == "" {
		return fmt.Errorf("tool-set %q: type is required (mcp, builtin, script)", ts.Name)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.sets[ts.Name] = ts
	return nil
}

// Unregister removes a tool-set by name.
func (m *ToolManager) Unregister(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sets[name]; !ok {
		return fmt.Errorf("tool-set %q not found", name)
	}
	delete(m.sets, name)
	return nil
}

// Get returns a tool-set by name, or nil if not found.
func (m *ToolManager) Get(name string) *ToolSet {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sets[name]
}

// Available returns all registered tool-sets.
func (m *ToolManager) Available() []*ToolSet {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*ToolSet, 0, len(m.sets))
	for _, ts := range m.sets {
		result = append(result, ts)
	}
	return result
}

// HasAll checks if all named tool-sets are registered.
// Returns the first missing name, or empty string if all present.
func (m *ToolManager) HasAll(names []string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, name := range names {
		if _, ok := m.sets[name]; !ok {
			return name
		}
	}
	return ""
}

// DiscoverMCPTools queries an MCP server for its available tools.
// Sends a JSON-RPC tools/list request to the command's stdin and reads stdout.
// This is a best-effort operation — returns nil tools on failure.
func (ts *ToolSet) DiscoverMCPTools() ([]string, error) {
	if ts.Type != "mcp" {
		return ts.Tools, nil
	}
	if ts.Command == "" {
		return nil, fmt.Errorf("mcp tool-set %q has no command", ts.Name)
	}

	// Build the command.
	args := append(ts.Args, ts.Command) // will be reordered below
	cmd := exec.Command(ts.Command, ts.Args...)

	// Send initialize + tools/list via stdin.
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"ag3nts","version":"0.1"}}}` + "\n"
	listReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}` + "\n"
	cmd.Stdin = strings.NewReader(initReq + listReq)

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("mcp discovery for %q: %w", ts.Name, err)
	}

	// Parse JSONL response — look for tools/list result.
	var tools []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var resp map[string]any
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			continue
		}
		// Check for tools/list response (id=2).
		if id, _ := resp["id"].(float64); id == 2 {
			result, _ := resp["result"].(map[string]any)
			toolList, _ := result["tools"].([]any)
			for _, t := range toolList {
				if toolMap, ok := t.(map[string]any); ok {
					if name, ok := toolMap["name"].(string); ok {
						tools = append(tools, name)
					}
				}
			}
		}
	}

	_ = args // suppress unused warning from reorder comment
	return tools, nil
}
