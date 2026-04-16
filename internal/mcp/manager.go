package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// MCPManagerConfig describes one MCP server to connect to.
// For stdio servers: Command + Args + Env.
// For HTTP servers: URL + AuthToken (Command is empty).
type MCPManagerConfig struct {
	Name      string
	Command   string   // empty for HTTP transport
	Args      []string
	Env       []string // pre-formatted "KEY=VALUE" strings
	URL       string   // HTTP endpoint (empty for stdio)
	AuthToken string   // Bearer token for HTTP auth
}

// IsHTTP returns true if this config describes an HTTP transport server.
func (c MCPManagerConfig) IsHTTP() bool {
	return c.URL != "" && c.Command == ""
}

// MCPTransport is the common interface for stdio and HTTP MCP clients.
// Both MCPClient and MCPHTTPClient implement this interface.
type MCPTransport interface {
	Name() string
	Alive() bool
	Close() error
	HasCapability(name string) bool
	ListTools(ctx context.Context) ([]MCPTool, error)
	CallTool(ctx context.Context, toolName string, arguments json.RawMessage) (*MCPToolResult, error)
	ListResources(ctx context.Context) ([]MCPResource, error)
	ReadResource(ctx context.Context, uri string) ([]MCPResourceContents, error)
	ListPrompts(ctx context.Context) ([]MCPPrompt, error)
	GetPrompt(ctx context.Context, name string, arguments map[string]string) ([]MCPPromptMessage, string, error)
}

// managedTool maps a tool back to its owning client.
type managedTool struct {
	client   MCPTransport
	mcpTool  MCPTool
	origName string // tool name as reported by the server
}

// managedResource maps a resource back to its owning client.
type managedResource struct {
	client   MCPTransport
	resource MCPResource
}

// managedPrompt maps a prompt back to its owning client.
type managedPrompt struct {
	client   MCPTransport
	prompt   MCPPrompt
	origName string
}

// MCPManager manages the lifecycle of multiple MCP server connections
// and provides a unified tool, resource, and prompt catalog across all
// of them.
//
// Tool names are qualified as "servername__toolname" (double underscore)
// to avoid collisions when multiple servers expose tools with the same
// name. This matches the convention used by Claude Code's MCP integration.
// Resources are keyed by "servername__uri" and prompts by "servername__promptname".
type MCPManager struct {
	clients   map[string]MCPTransport      // keyed by config name
	configs   map[string]MCPManagerConfig  // original configs for auto-restart
	tools     map[string]managedTool       // keyed by qualified name
	resources map[string]managedResource   // keyed by "server__uri"
	prompts   map[string]managedPrompt     // keyed by "server__name"
	mu        sync.RWMutex

	// onSampling is wired by the orchestrator to route sampling
	// requests to the local LLM. Shared across all clients.
	onSampling SamplingHandler
}

// NewMCPManager creates an empty manager. Call StartServer for each
// configured MCP server, then AllTools to get the aggregated catalog.
func NewMCPManager() *MCPManager {
	return &MCPManager{
		clients:   make(map[string]MCPTransport),
		configs:   make(map[string]MCPManagerConfig),
		tools:     make(map[string]managedTool),
		resources: make(map[string]managedResource),
		prompts:   make(map[string]managedPrompt),
	}
}

// SetSamplingHandler configures the callback for handling
// sampling/createMessage requests from MCP servers. Call before
// StartServer so new clients inherit the handler.
func (m *MCPManager) SetSamplingHandler(fn SamplingHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onSampling = fn
	// Update existing clients.
	for _, client := range m.clients {
		switch c := client.(type) {
		case *MCPClient:
			c.OnSampling = fn
		case *MCPHTTPClient:
			c.OnSampling = fn
		}
	}
}

// StartServer launches an MCP server (stdio subprocess or HTTP) and
// registers its tools, resources, and prompts. Returns the number of
// tools discovered.
//
// For stdio: spawns a subprocess and communicates via JSON-RPC over stdin/stdout.
// For HTTP: connects to the given URL using Streamable HTTP transport.
func (m *MCPManager) StartServer(ctx context.Context, cfg MCPManagerConfig) (int, error) {
	var client MCPTransport
	var err error

	if cfg.IsHTTP() {
		client, err = NewMCPHTTPClient(cfg.Name, cfg.URL, cfg.AuthToken)
	} else {
		client, err = NewMCPClient(cfg.Name, cfg.Command, cfg.Args, cfg.Env)
	}
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

	// Discover resources and prompts if the server declares those capabilities.
	var resources []MCPResource
	var prompts []MCPPrompt
	if client.HasCapability("resources") {
		resCtx, resCancel := context.WithTimeout(ctx, 30*time.Second)
		res, err := client.ListResources(resCtx)
		resCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mcp:%s] resources/list: %v\n", cfg.Name, err)
		} else {
			resources = res
		}
	}
	if client.HasCapability("prompts") {
		prCtx, prCancel := context.WithTimeout(ctx, 30*time.Second)
		pr, err := client.ListPrompts(prCtx)
		prCancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mcp:%s] prompts/list: %v\n", cfg.Name, err)
		} else {
			prompts = pr
		}
	}

	// Set up notification handlers for catalog changes.
	serverName := cfg.Name
	toolsChanged := func() {
		fmt.Fprintf(os.Stderr, "[mcp:%s] tools changed, re-discovering...\n", serverName)
		reCtx, reCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer reCancel()
		newTools, err := client.ListTools(reCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mcp:%s] re-discovery failed: %v\n", serverName, err)
			return
		}
		m.mu.Lock()
		for name, mt := range m.tools {
			if mt.client == client {
				delete(m.tools, name)
			}
		}
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
	resourcesChanged := func() {
		fmt.Fprintf(os.Stderr, "[mcp:%s] resources changed, re-discovering...\n", serverName)
		reCtx, reCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer reCancel()
		newRes, err := client.ListResources(reCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mcp:%s] resources re-discovery failed: %v\n", serverName, err)
			return
		}
		m.mu.Lock()
		for key, mr := range m.resources {
			if mr.client == client {
				delete(m.resources, key)
			}
		}
		for _, r := range newRes {
			qualKey := serverName + "__" + r.URI
			m.resources[qualKey] = managedResource{client: client, resource: r}
		}
		m.mu.Unlock()
		fmt.Fprintf(os.Stderr, "[mcp:%s] now has %d resources\n", serverName, len(newRes))
	}
	promptsChanged := func() {
		fmt.Fprintf(os.Stderr, "[mcp:%s] prompts changed, re-discovering...\n", serverName)
		reCtx, reCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer reCancel()
		newPr, err := client.ListPrompts(reCtx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[mcp:%s] prompts re-discovery failed: %v\n", serverName, err)
			return
		}
		m.mu.Lock()
		for key, mp := range m.prompts {
			if mp.client == client {
				delete(m.prompts, key)
			}
		}
		for _, p := range newPr {
			qualKey := serverName + "__" + p.Name
			m.prompts[qualKey] = managedPrompt{client: client, prompt: p, origName: p.Name}
		}
		m.mu.Unlock()
		fmt.Fprintf(os.Stderr, "[mcp:%s] now has %d prompts\n", serverName, len(newPr))
	}

	// Wire notification handlers and sampling onto the transport.
	switch c := client.(type) {
	case *MCPClient:
		c.OnToolsChanged = toolsChanged
		c.OnResourcesChanged = resourcesChanged
		c.OnPromptsChanged = promptsChanged
		if m.onSampling != nil {
			c.OnSampling = m.onSampling
		}
	case *MCPHTTPClient:
		c.OnToolsChanged = toolsChanged
		c.OnResourcesChanged = resourcesChanged
		c.OnPromptsChanged = promptsChanged
		if m.onSampling != nil {
			c.OnSampling = m.onSampling
		}
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
	for _, r := range resources {
		qualKey := cfg.Name + "__" + r.URI
		m.resources[qualKey] = managedResource{client: client, resource: r}
	}
	for _, p := range prompts {
		qualKey := cfg.Name + "__" + p.Name
		m.prompts[qualKey] = managedPrompt{client: client, prompt: p, origName: p.Name}
	}

	total := len(tools)
	if len(resources) > 0 {
		fmt.Fprintf(os.Stderr, "  ├─ %d resources\n", len(resources))
	}
	if len(prompts) > 0 {
		fmt.Fprintf(os.Stderr, "  ├─ %d prompts\n", len(prompts))
	}

	return total, nil
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

// AllResources returns all discovered resources across all servers,
// keyed by their qualified key (servername__uri).
func (m *MCPManager) AllResources() map[string]MCPResource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]MCPResource, len(m.resources))
	for key, mr := range m.resources {
		result[key] = mr.resource
	}
	return result
}

// ReadResource reads a resource by URI from the correct server.
// The URI is matched against the discovered resource catalog.
func (m *MCPManager) ReadResource(ctx context.Context, uri string) ([]MCPResourceContents, error) {
	m.mu.RLock()
	// Find which server owns this URI.
	var client MCPTransport
	for _, mr := range m.resources {
		if mr.resource.URI == uri {
			client = mr.client
			break
		}
	}
	m.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("no server owns resource URI %q", uri)
	}

	// Auto-restart dead server.
	if !client.Alive() {
		serverName := client.Name()
		fmt.Fprintf(os.Stderr, "[mcp:%s] server died, attempting restart...\n", serverName)
		if err := m.restartServer(ctx, serverName); err != nil {
			return nil, fmt.Errorf("MCP server %q crashed and restart failed: %w", serverName, err)
		}
		m.mu.RLock()
		client = nil
		for _, mr := range m.resources {
			if mr.resource.URI == uri {
				client = mr.client
				break
			}
		}
		m.mu.RUnlock()
		if client == nil {
			return nil, fmt.Errorf("resource %q unavailable after restart", uri)
		}
	}

	return client.ReadResource(ctx, uri)
}

// AllPrompts returns all discovered prompts across all servers,
// keyed by their qualified name (servername__promptname).
func (m *MCPManager) AllPrompts() map[string]MCPPrompt {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]MCPPrompt, len(m.prompts))
	for key, mp := range m.prompts {
		result[key] = mp.prompt
	}
	return result
}

// GetPrompt expands a prompt template by qualified name with arguments.
func (m *MCPManager) GetPrompt(ctx context.Context, qualifiedName string, arguments map[string]string) ([]MCPPromptMessage, string, error) {
	m.mu.RLock()
	mp, ok := m.prompts[qualifiedName]
	m.mu.RUnlock()

	if !ok {
		return nil, "", fmt.Errorf("unknown MCP prompt %q", qualifiedName)
	}

	if !mp.client.Alive() {
		serverName := mp.client.Name()
		fmt.Fprintf(os.Stderr, "[mcp:%s] server died, attempting restart...\n", serverName)
		if err := m.restartServer(ctx, serverName); err != nil {
			return nil, "", fmt.Errorf("MCP server %q crashed and restart failed: %w", serverName, err)
		}
		m.mu.RLock()
		mp, ok = m.prompts[qualifiedName]
		m.mu.RUnlock()
		if !ok {
			return nil, "", fmt.Errorf("MCP prompt %q unavailable after restart", qualifiedName)
		}
	}

	return mp.client.GetPrompt(ctx, mp.origName, arguments)
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

	// Spawn a fresh client using the same transport type.
	var client MCPTransport
	var err error
	if cfg.IsHTTP() {
		client, err = NewMCPHTTPClient(cfg.Name, cfg.URL, cfg.AuthToken)
	} else {
		client, err = NewMCPClient(cfg.Name, cfg.Command, cfg.Args, cfg.Env)
	}
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

	// Re-discover resources and prompts if supported.
	var resources []MCPResource
	var prompts []MCPPrompt
	if client.HasCapability("resources") {
		resCtx, resCancel := context.WithTimeout(ctx, 30*time.Second)
		resources, _ = client.ListResources(resCtx)
		resCancel()
	}
	if client.HasCapability("prompts") {
		prCtx, prCancel := context.WithTimeout(ctx, 30*time.Second)
		prompts, _ = client.ListPrompts(prCtx)
		prCancel()
	}

	// Wire sampling handler.
	switch c := client.(type) {
	case *MCPClient:
		if m.onSampling != nil {
			c.OnSampling = m.onSampling
		}
	case *MCPHTTPClient:
		if m.onSampling != nil {
			c.OnSampling = m.onSampling
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Remove old entries for this server.
	for name, mt := range m.tools {
		if mt.client == oldClient {
			delete(m.tools, name)
		}
	}
	for key, mr := range m.resources {
		if mr.client == oldClient {
			delete(m.resources, key)
		}
	}
	for key, mp := range m.prompts {
		if mp.client == oldClient {
			delete(m.prompts, key)
		}
	}

	// Register new client, tools, resources, and prompts.
	m.clients[serverName] = client
	for _, t := range tools {
		qualName := serverName + "__" + t.Name
		m.tools[qualName] = managedTool{
			client:   client,
			mcpTool:  t,
			origName: t.Name,
		}
	}
	for _, r := range resources {
		qualKey := serverName + "__" + r.URI
		m.resources[qualKey] = managedResource{client: client, resource: r}
	}
	for _, p := range prompts {
		qualKey := serverName + "__" + p.Name
		m.prompts[qualKey] = managedPrompt{client: client, prompt: p, origName: p.Name}
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
// and their tool/resource/prompt counts, suitable for startup banners.
func (m *MCPManager) ServerSummary() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	type counts struct {
		tools, resources, prompts int
	}
	c := make(map[string]*counts)
	for _, mt := range m.tools {
		name := mt.client.Name()
		if c[name] == nil {
			c[name] = &counts{}
		}
		c[name].tools++
	}
	for _, mr := range m.resources {
		name := mr.client.Name()
		if c[name] == nil {
			c[name] = &counts{}
		}
		c[name].resources++
	}
	for _, mp := range m.prompts {
		name := mp.client.Name()
		if c[name] == nil {
			c[name] = &counts{}
		}
		c[name].prompts++
	}

	var lines []string
	for name, ct := range c {
		parts := []string{fmt.Sprintf("%d tools", ct.tools)}
		if ct.resources > 0 {
			parts = append(parts, fmt.Sprintf("%d resources", ct.resources))
		}
		if ct.prompts > 0 {
			parts = append(parts, fmt.Sprintf("%d prompts", ct.prompts))
		}
		lines = append(lines, fmt.Sprintf("%s (%s)", name, strings.Join(parts, ", ")))
	}
	return lines
}
