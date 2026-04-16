package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// MCPManagerConfig describes one MCP server to connect to.
type MCPManagerConfig struct {
	Name    string
	Command string
	Args    []string
	Env     []string // pre-formatted "KEY=VALUE" strings
}

// managedTool maps a tool back to its owning client.
type managedTool struct {
	client   *MCPClient
	mcpTool  MCPTool
	origName string // tool name as reported by the server
}

// MCPManager manages the lifecycle of multiple MCP server connections
// and provides a unified tool catalog across all of them.
//
// Tool names are qualified as "servername__toolname" (double underscore)
// to avoid collisions when multiple servers expose tools with the same
// name. This matches the convention used by Claude Code's MCP integration.
type MCPManager struct {
	clients map[string]*MCPClient  // keyed by config name
	tools   map[string]managedTool // keyed by qualified name
	mu      sync.RWMutex
}

// NewMCPManager creates an empty manager. Call StartServer for each
// configured MCP server, then AllTools to get the aggregated catalog.
func NewMCPManager() *MCPManager {
	return &MCPManager{
		clients: make(map[string]*MCPClient),
		tools:   make(map[string]managedTool),
	}
}

// StartServer launches an MCP server subprocess, performs the
// initialize handshake, discovers tools, and registers them in the
// manager's catalog. Returns the number of tools discovered.
//
// Errors during startup are returned to the caller (the server is
// not added to the manager). The caller decides whether to skip or
// abort — typically, a startup error is logged as a warning and the
// remaining servers are still started.
func (m *MCPManager) StartServer(ctx context.Context, cfg MCPManagerConfig) (int, error) {
	client, err := NewMCPClient(cfg.Name, cfg.Command, cfg.Args, cfg.Env)
	if err != nil {
		return 0, err
	}

	// Discover tools with a generous timeout (some servers are slow
	// to enumerate, e.g. if they query a remote schema).
	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tools, err := client.ListTools(listCtx)
	if err != nil {
		_ = client.Close()
		return 0, fmt.Errorf("list tools: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.clients[cfg.Name] = client
	for _, t := range tools {
		qualName := cfg.Name + "__" + t.Name
		m.tools[qualName] = managedTool{
			client:   client,
			mcpTool:  t,
			origName: t.Name,
		}
	}

	return len(tools), nil
}

// AllTools returns all discovered tools across all servers, keyed by
// their qualified name (servername__toolname).
func (m *MCPManager) AllTools() map[string]MCPTool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]MCPTool, len(m.tools))
	for name, mt := range m.tools {
		result[name] = mt.mcpTool
	}
	return result
}

// CallTool routes a tools/call request to the correct server by
// qualified name. The arguments are passed through as raw JSON.
func (m *MCPManager) CallTool(ctx context.Context, qualifiedName string, arguments json.RawMessage) (*MCPToolResult, error) {
	m.mu.RLock()
	mt, ok := m.tools[qualifiedName]
	m.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown MCP tool %q", qualifiedName)
	}

	if !mt.client.Alive() {
		return nil, fmt.Errorf("MCP server %q is not running", mt.client.Name())
	}

	return mt.client.CallTool(ctx, mt.origName, arguments)
}

// StopAll gracefully shuts down all MCP server subprocesses.
func (m *MCPManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, client := range m.clients {
		_ = client.Close()
	}
}

// ServerSummary returns a human-readable summary of connected servers
// and their tool counts, suitable for startup banners.
func (m *MCPManager) ServerSummary() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Count tools per server.
	counts := make(map[string]int)
	for _, mt := range m.tools {
		counts[mt.client.Name()]++
	}

	var lines []string
	for name, count := range counts {
		lines = append(lines, fmt.Sprintf("%s (%d tools)", name, count))
	}
	return lines
}
