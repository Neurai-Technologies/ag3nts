package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
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
	clients map[string]*MCPClient       // keyed by config name
	configs map[string]MCPManagerConfig  // original configs for auto-restart
	tools   map[string]managedTool      // keyed by qualified name
	mu      sync.RWMutex
}

// NewMCPManager creates an empty manager. Call StartServer for each
// configured MCP server, then AllTools to get the aggregated catalog.
func NewMCPManager() *MCPManager {
	return &MCPManager{
		clients: make(map[string]*MCPClient),
		configs: make(map[string]MCPManagerConfig),
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

	// Set up notification handler: when the server signals that its
	// tool list changed, re-discover and update the catalog.
	serverName := cfg.Name
	client.OnToolsChanged = func() {
		fmt.Fprintf(os.Stderr, "[mcp:%s] tools changed, re-discovering...\n", serverName)
		reCtx, reCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer reCancel()
		newTools, err := client.ListTools(reCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mcp:%s] re-discovery failed: %v\n", serverName, err)
			return
		}
		m.mu.Lock()
		// Remove old tools for this server.
		for name, mt := range m.tools {
			if mt.client == client {
				delete(m.tools, name)
			}
		}
		// Add new tools.
		for _, t := range newTools {
			qualName := serverName + "__" + t.Name
			m.tools[qualName] = managedTool{
				client:   client,
				mcpTool:  t,
				origName: t.Name,
			}
		}
		m.mu.Unlock()
		fmt.Fprintf(os.Stderr, "[mcp:%s] now has %d tools\n", serverName, len(newTools))
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.clients[cfg.Name] = client
	m.configs[cfg.Name] = cfg
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

	// Auto-restart: if the server crashed, try to respawn it once
	// before failing. This handles transient crashes (OOM, segfault)
	// without requiring the user to restart ag3nts.
	if !mt.client.Alive() {
		serverName := mt.client.Name()
		fmt.Fprintf(os.Stderr, "[mcp:%s] server died, attempting restart...\n", serverName)
		if err := m.restartServer(ctx, serverName); err != nil {
			return nil, fmt.Errorf("MCP server %q crashed and restart failed: %w", serverName, err)
		}
		fmt.Fprintf(os.Stderr, "[mcp:%s] restarted successfully\n", serverName)
		// Re-lookup the tool since restartServer replaces the client.
		m.mu.RLock()
		mt, ok = m.tools[qualifiedName]
		m.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("MCP tool %q unavailable after restart", qualifiedName)
		}
	}

	return mt.client.CallTool(ctx, mt.origName, arguments)
}

// restartServer closes the dead client and spawns a fresh one using
// the stored config. Re-discovers tools and updates the catalog.
// Caller must NOT hold m.mu.
func (m *MCPManager) restartServer(ctx context.Context, serverName string) error {
	m.mu.Lock()
	cfg, ok := m.configs[serverName]
	oldClient := m.clients[serverName]
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("no config for server %q", serverName)
	}

	// Close the dead client (idempotent).
	if oldClient != nil {
		_ = oldClient.Close()
	}

	// Spawn a fresh client.
	client, err := NewMCPClient(cfg.Name, cfg.Command, cfg.Args, cfg.Env)
	if err != nil {
		return fmt.Errorf("respawn: %w", err)
	}

	listCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	tools, err := client.ListTools(listCtx)
	if err != nil {
		_ = client.Close()
		return fmt.Errorf("re-discover tools: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove old tools for this server.
	for name, mt := range m.tools {
		if mt.client == oldClient {
			delete(m.tools, name)
		}
	}

	// Register new client and tools.
	m.clients[serverName] = client
	for _, t := range tools {
		qualName := serverName + "__" + t.Name
		m.tools[qualName] = managedTool{
			client:   client,
			mcpTool:  t,
			origName: t.Name,
		}
	}

	return nil
}

// StartHealthCheck launches a background goroutine that periodically
// checks if MCP servers are still alive. Dead servers are auto-restarted
// proactively rather than waiting for the next tool call to fail. Runs
// every 30 seconds. Call StopAll to stop the health checker.
func (m *MCPManager) StartHealthCheck(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.mu.RLock()
				var dead []string
				for name, client := range m.clients {
					if !client.Alive() {
						dead = append(dead, name)
					}
				}
				m.mu.RUnlock()
				for _, name := range dead {
					fmt.Fprintf(os.Stderr, "[mcp:%s] health check: server died, restarting...\n", name)
					if err := m.restartServer(ctx, name); err != nil {
						fmt.Fprintf(os.Stderr, "[mcp:%s] health check: restart failed: %v\n", name, err)
					} else {
						fmt.Fprintf(os.Stderr, "[mcp:%s] health check: restarted\n", name)
					}
				}
			}
		}
	}()
}

// RestartServer manually restarts a named MCP server. Used by the
// TUI's /mcp restart <name> command. Returns an error if the server
// name is unknown or the restart fails.
func (m *MCPManager) RestartServer(ctx context.Context, name string) error {
	m.mu.RLock()
	_, ok := m.configs[name]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown MCP server %q", name)
	}
	return m.restartServer(ctx, name)
}

// ServerNames returns the names of all configured servers.
func (m *MCPManager) ServerNames() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.clients))
	for name := range m.clients {
		names = append(names, name)
	}
	return names
}

// ServerAlive returns whether a named server's subprocess is running.
func (m *MCPManager) ServerAlive(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	client, ok := m.clients[name]
	if !ok {
		return false
	}
	return client.Alive()
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
