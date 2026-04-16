package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// initTimeout is how long we wait for the MCP server to respond
	// to the initialize handshake before giving up.
	initTimeout = 10 * time.Second

	// callTimeout is the default deadline for a tools/call invocation
	// when the caller doesn't provide its own context deadline.
	callTimeout = 2 * time.Minute
)

// MCPTool represents a tool as returned by an MCP server's tools/list
// response. InputSchema is preserved as raw JSON to avoid lossy parsing
// — the llm package converts it to ToolDef at registration time.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// MCPToolResult holds the response from a tools/call invocation.
type MCPToolResult struct {
	Content []MCPContent `json:"content"`
	IsError bool         `json:"isError,omitempty"`
}

// MCPContent is one item in a tools/call result content array.
type MCPContent struct {
	Type string `json:"type"` // "text", "image", "resource"
	Text string `json:"text,omitempty"`
}

// --- Resource types ---

// MCPResource represents a concrete resource exposed by an MCP server.
type MCPResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// MCPResourceTemplate is a URI-template resource (RFC 6570).
type MCPResourceTemplate struct {
	URITemplate string `json:"uriTemplate"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

// MCPResourceContents is one item in a resources/read response.
// Either Text or Blob is populated, never both.
type MCPResourceContents struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"` // base64-encoded
}

// --- Prompt types ---

// MCPPrompt represents a prompt template exposed by an MCP server.
type MCPPrompt struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Arguments   []MCPPromptArgument `json:"arguments,omitempty"`
}

// MCPPromptArgument describes one parameter of a prompt template.
type MCPPromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// MCPPromptMessage is one message in a prompt's expanded output.
type MCPPromptMessage struct {
	Role    string     `json:"role"` // "user" or "assistant"
	Content MCPContent `json:"content"`
}

// --- Sampling types (server→client) ---

// MCPSamplingRequest is sent by the MCP server asking the client to
// run an LLM inference. The client must respond with the model's output.
type MCPSamplingRequest struct {
	Messages       []MCPSamplingMessage `json:"messages"`
	MaxTokens      int                  `json:"maxTokens"`
	SystemPrompt   string               `json:"systemPrompt,omitempty"`
	Temperature    *float64             `json:"temperature,omitempty"`
	StopSequences  []string             `json:"stopSequences,omitempty"`
	IncludeContext string               `json:"includeContext,omitempty"` // "none", "thisServer", "allServers"
}

// MCPSamplingMessage is one message in a sampling request.
type MCPSamplingMessage struct {
	Role    string     `json:"role"` // "user" or "assistant"
	Content MCPContent `json:"content"`
}

// MCPSamplingResponse is the client's response to a sampling request.
type MCPSamplingResponse struct {
	Role       string     `json:"role"` // "assistant"
	Content    MCPContent `json:"content"`
	Model      string     `json:"model"`
	StopReason string     `json:"stopReason,omitempty"`
}

// SamplingHandler is the callback type for handling incoming
// sampling/createMessage requests from MCP servers. Set on the client
// before use; the manager wires this to the local LLM.
type SamplingHandler func(ctx context.Context, req *MCPSamplingRequest) (*MCPSamplingResponse, error)

// MCPClient manages a persistent JSON-RPC 2.0 connection to an MCP
// server subprocess via stdin/stdout. The server stays alive for the
// ag3nts session; tools/call requests are sent on demand.
//
// Thread-safe: multiple goroutines can call ListTools/CallTool
// concurrently. Responses are demuxed by JSON-RPC request ID.
type MCPClient struct {
	name   string // human-readable name (config key)
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr io.ReadCloser

	// Response demux: readLoop dispatches responses by ID.
	reader   *bufio.Scanner
	pendMu   sync.Mutex // protects pending map only
	writeMu  sync.Mutex // protects stdin writes only (separate to avoid deadlock when pipe blocks)
	nextID   int64      // atomic
	pending  map[int64]chan *jsonRPCResponse

	serverInfo   map[string]any
	capabilities map[string]any

	done    chan struct{} // closed when readLoop exits
	readErr error        // first read error

	// OnToolsChanged is called when the server sends a
	// notifications/tools/list_changed notification. The manager uses
	// this to re-discover the server's tool catalog. Set before the
	// readLoop sees any notifications (i.e., during NewMCPClient).
	OnToolsChanged func()

	// OnResourcesChanged is called when the server sends
	// notifications/resources/list_changed.
	OnResourcesChanged func()

	// OnPromptsChanged is called when the server sends
	// notifications/prompts/list_changed.
	OnPromptsChanged func()

	// OnResourceUpdated is called when the server sends
	// notifications/resources/updated with the changed URI.
	OnResourceUpdated func(uri string)

	// OnSampling handles incoming sampling/createMessage requests.
	// If nil, the client responds with an error to the server.
	OnSampling SamplingHandler
}

// NewMCPClient spawns the MCP server subprocess, performs the
// initialize handshake, sends notifications/initialized, and returns
// a ready-to-use client. Returns an error if the process fails to
// start or the handshake times out.
//
// The env slice should contain pre-formatted "KEY=VALUE" strings.
// They're overlaid on top of the filtered parent environment (SR-5).
func NewMCPClient(name, command string, args, env []string) (*MCPClient, error) {
	cmd := exec.Command(command, args...)

	// Build environment: filtered base + caller-provided overlay.
	cmdEnv := filteredEnv()
	cmdEnv = append(cmdEnv, env...)
	cmd.Env = cmdEnv

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", command, err)
	}

	c := &MCPClient{
		name:    name,
		cmd:     cmd,
		stdin:   stdin,
		stderr:  stderr,
		reader:  bufio.NewScanner(stdout),
		pending: make(map[int64]chan *jsonRPCResponse),
		done:    make(chan struct{}),
	}
	c.reader.Buffer(make([]byte, 0, 64*1024), 10*1024*1024) // 10MB max line

	// Drain stderr in background to prevent subprocess blocking.
	go c.drainStderr()

	// Start the response reader goroutine.
	go c.readLoop()

	// Perform the initialize handshake with a timeout.
	ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()

	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("initialize %s: %w", name, err)
	}

	return c, nil
}

// newMCPClientFromPipes creates a client from pre-connected pipes.
// Used by tests to avoid spawning real subprocesses.
func newMCPClientFromPipes(name string, stdin io.WriteCloser, stdout io.Reader) *MCPClient {
	c := &MCPClient{
		name:    name,
		stdin:   stdin,
		reader:  bufio.NewScanner(stdout),
		pending: make(map[int64]chan *jsonRPCResponse),
		done:    make(chan struct{}),
	}
	c.reader.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	go c.readLoop()
	return c
}

// initialize performs the MCP handshake: sends initialize, waits for
// response, sends notifications/initialized.
func (c *MCPClient) initialize(ctx context.Context) error {
	resp, err := c.sendRequest(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"sampling": map[string]any{},
		},
		"clientInfo": map[string]any{
			"name":    "ag3nts",
			"version": "0.2",
		},
	})
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("server error: %s", resp.Error.Message)
	}

	// Parse server capabilities.
	if result, ok := resp.Result.(map[string]any); ok {
		c.serverInfo, _ = result["serverInfo"].(map[string]any)
		c.capabilities, _ = result["capabilities"].(map[string]any)
	}

	// Send the initialized notification (no response expected).
	return c.sendNotification("notifications/initialized", nil)
}

// ListTools sends tools/list and returns the discovered tools with
// full input schemas.
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	resp, err := c.sendRequest(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}

	// The result is {"tools": [...]}. Re-marshal and parse into MCPTool.
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal tools/list result: %w", err)
	}
	var toolsResp struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(resultBytes, &toolsResp); err != nil {
		return nil, fmt.Errorf("parse tools/list result: %w", err)
	}
	return toolsResp.Tools, nil
}

// CallTool invokes a tool on the MCP server and returns the result.
func (c *MCPClient) CallTool(ctx context.Context, toolName string, arguments json.RawMessage) (*MCPToolResult, error) {
	resp, err := c.sendRequest(ctx, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": json.RawMessage(arguments), // pass through
	})
	if err != nil {
		return nil, fmt.Errorf("tools/call %s: %w", toolName, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/call %s error: %s", toolName, resp.Error.Message)
	}

	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal tools/call result: %w", err)
	}
	var result MCPToolResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("parse tools/call result: %w", err)
	}
	return &result, nil
}

// HasCapability returns true if the server declared the given
// capability during the initialize handshake (e.g., "resources",
// "prompts", "tools").
func (c *MCPClient) HasCapability(name string) bool {
	if c.capabilities == nil {
		return false
	}
	_, ok := c.capabilities[name]
	return ok
}

// --- Resource methods ---

// ListResources sends resources/list and returns the discovered resources.
// Returns nil, nil if the server doesn't declare the resources capability.
func (c *MCPClient) ListResources(ctx context.Context) ([]MCPResource, error) {
	if !c.HasCapability("resources") {
		return nil, nil
	}
	resp, err := c.sendRequest(ctx, "resources/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("resources/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/list error: %s", resp.Error.Message)
	}
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal resources/list result: %w", err)
	}
	var result struct {
		Resources []MCPResource `json:"resources"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("parse resources/list result: %w", err)
	}
	return result.Resources, nil
}

// ListResourceTemplates sends resources/templates/list and returns
// URI-template resources. Returns nil, nil without resources capability.
func (c *MCPClient) ListResourceTemplates(ctx context.Context) ([]MCPResourceTemplate, error) {
	if !c.HasCapability("resources") {
		return nil, nil
	}
	resp, err := c.sendRequest(ctx, "resources/templates/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("resources/templates/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/templates/list error: %s", resp.Error.Message)
	}
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal resources/templates/list result: %w", err)
	}
	var result struct {
		ResourceTemplates []MCPResourceTemplate `json:"resourceTemplates"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("parse resources/templates/list result: %w", err)
	}
	return result.ResourceTemplates, nil
}

// ReadResource sends resources/read for the given URI and returns the
// content items.
func (c *MCPClient) ReadResource(ctx context.Context, uri string) ([]MCPResourceContents, error) {
	resp, err := c.sendRequest(ctx, "resources/read", map[string]any{
		"uri": uri,
	})
	if err != nil {
		return nil, fmt.Errorf("resources/read: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/read error: %s", resp.Error.Message)
	}
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal resources/read result: %w", err)
	}
	var result struct {
		Contents []MCPResourceContents `json:"contents"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("parse resources/read result: %w", err)
	}
	return result.Contents, nil
}

// --- Prompt methods ---

// ListPrompts sends prompts/list and returns the server's prompt templates.
// Returns nil, nil if the server doesn't declare the prompts capability.
func (c *MCPClient) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	if !c.HasCapability("prompts") {
		return nil, nil
	}
	resp, err := c.sendRequest(ctx, "prompts/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("prompts/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("prompts/list error: %s", resp.Error.Message)
	}
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("marshal prompts/list result: %w", err)
	}
	var result struct {
		Prompts []MCPPrompt `json:"prompts"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("parse prompts/list result: %w", err)
	}
	return result.Prompts, nil
}

// GetPrompt sends prompts/get to expand a prompt template with arguments.
func (c *MCPClient) GetPrompt(ctx context.Context, name string, arguments map[string]string) ([]MCPPromptMessage, string, error) {
	params := map[string]any{"name": name}
	if len(arguments) > 0 {
		params["arguments"] = arguments
	}
	resp, err := c.sendRequest(ctx, "prompts/get", params)
	if err != nil {
		return nil, "", fmt.Errorf("prompts/get: %w", err)
	}
	if resp.Error != nil {
		return nil, "", fmt.Errorf("prompts/get error: %s", resp.Error.Message)
	}
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, "", fmt.Errorf("marshal prompts/get result: %w", err)
	}
	var result struct {
		Messages    []MCPPromptMessage `json:"messages"`
		Description string             `json:"description"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, "", fmt.Errorf("parse prompts/get result: %w", err)
	}
	return result.Messages, result.Description, nil
}

// --- Resource subscription methods ---

// SubscribeResource subscribes to change notifications for a resource URI.
// Requires the server to declare resources.subscribe capability.
func (c *MCPClient) SubscribeResource(ctx context.Context, uri string) error {
	resp, err := c.sendRequest(ctx, "resources/subscribe", map[string]any{"uri": uri})
	if err != nil {
		return fmt.Errorf("resources/subscribe: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("resources/subscribe error: %s", resp.Error.Message)
	}
	return nil
}

// UnsubscribeResource unsubscribes from change notifications for a resource URI.
func (c *MCPClient) UnsubscribeResource(ctx context.Context, uri string) error {
	resp, err := c.sendRequest(ctx, "resources/unsubscribe", map[string]any{"uri": uri})
	if err != nil {
		return fmt.Errorf("resources/unsubscribe: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("resources/unsubscribe error: %s", resp.Error.Message)
	}
	return nil
}

// --- Logging method ---

// SetLogLevel sends logging/setLevel to control server-side log verbosity.
func (c *MCPClient) SetLogLevel(ctx context.Context, level string) error {
	resp, err := c.sendRequest(ctx, "logging/setLevel", map[string]any{"level": level})
	if err != nil {
		return fmt.Errorf("logging/setLevel: %w", err)
	}
	if resp.Error != nil {
		return fmt.Errorf("logging/setLevel error: %s", resp.Error.Message)
	}
	return nil
}

// Close gracefully shuts down the MCP server. Closes stdin (which
// signals the server to exit), waits briefly, then kills if needed.
func (c *MCPClient) Close() error {
	if c.stdin != nil {
		_ = c.stdin.Close()
	}

	// Wait for readLoop to finish (server closes stdout on exit).
	select {
	case <-c.done:
	case <-time.After(3 * time.Second):
		// Server didn't exit gracefully — kill it.
		if c.cmd != nil && c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
	}

	if c.cmd != nil {
		_ = c.cmd.Wait()
	}
	return nil
}

// Alive returns true if the readLoop is still running (subprocess
// hasn't crashed).
func (c *MCPClient) Alive() bool {
	select {
	case <-c.done:
		return false
	default:
		return true
	}
}

// Name returns the human-readable name of this MCP server.
func (c *MCPClient) Name() string {
	return c.name
}

// --- Internal JSON-RPC plumbing ---

// sendRequest sends a JSON-RPC request and waits for the matching
// response. Thread-safe — multiple goroutines can call concurrently.
func (c *MCPClient) sendRequest(ctx context.Context, method string, params any) (*jsonRPCResponse, error) {
	id := atomic.AddInt64(&c.nextID, 1)

	// Register a response channel BEFORE writing the request so there's
	// no window where a fast response arrives before the channel exists.
	ch := make(chan *jsonRPCResponse, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()

	// Clean up on exit.
	defer func() {
		c.pendMu.Lock()
		delete(c.pending, id)
		c.pendMu.Unlock()
	}()

	// Marshal and write the request. writeMu is separate from pendMu
	// so stdin pipe blocking (which can happen with in-memory pipes in
	// tests) doesn't prevent other goroutines from registering their
	// pending channels.
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      mustMarshal(id),
		Method:  method,
	}
	if params != nil {
		req.Params = mustMarshalRaw(params)
	}

	if err := c.writeJSON(req); err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Wait for response, context cancellation, or server death.
	select {
	case resp := <-ch:
		if resp == nil {
			return nil, fmt.Errorf("server closed connection")
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, fmt.Errorf("server exited: %v", c.readErr)
	}
}

// sendNotification sends a JSON-RPC notification (no id, no response).
func (c *MCPClient) sendNotification(method string, params any) error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
	}
	if params != nil {
		req.Params = mustMarshalRaw(params)
	}
	return c.writeJSON(req)
}

// writeJSON marshals and writes a JSON-RPC message to the server's stdin.
// Uses writeMu (separate from pendMu) so pipe-blocking writes don't
// prevent other goroutines from registering pending response channels.
func (c *MCPClient) writeJSON(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = fmt.Fprintf(c.stdin, "%s\n", data)
	return err
}

// readLoop reads JSON-RPC responses from stdout and dispatches them
// to pending request channels by matching IDs. Runs as a goroutine
// for the lifetime of the client.
func (c *MCPClient) readLoop() {
	defer close(c.done)

	for c.reader.Scan() {
		line := c.reader.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp jsonRPCResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			// Malformed line — skip silently. Could be a log line
			// from the server that leaked to stdout.
			continue
		}

		// Parse method field to distinguish responses, notifications,
		// and incoming requests (sampling).
		var msg struct {
			Method string          `json:"method,omitempty"`
			ID     json.RawMessage `json:"id,omitempty"`
			Params json.RawMessage `json:"params,omitempty"`
		}
		_ = json.Unmarshal(line, &msg)

		// Incoming request (has method + has ID): e.g., sampling/createMessage.
		if msg.Method != "" && len(msg.ID) > 0 && string(msg.ID) != "null" {
			go c.handleIncomingRequest(msg.Method, msg.ID, msg.Params)
			continue
		}

		// Notification (has method, no ID).
		if msg.Method != "" {
			switch msg.Method {
			case "notifications/tools/list_changed":
				if c.OnToolsChanged != nil {
					go c.OnToolsChanged()
				}
			case "notifications/resources/list_changed":
				if c.OnResourcesChanged != nil {
					go c.OnResourcesChanged()
				}
			case "notifications/resources/updated":
				if c.OnResourceUpdated != nil {
					var params struct {
						URI string `json:"uri"`
					}
					_ = json.Unmarshal(msg.Params, &params)
					if params.URI != "" {
						go c.OnResourceUpdated(params.URI)
					}
				}
			case "notifications/prompts/list_changed":
				if c.OnPromptsChanged != nil {
					go c.OnPromptsChanged()
				}
			}
			continue
		}

		// Response to our request (no method, has ID).
		if len(resp.ID) == 0 || string(resp.ID) == "null" {
			continue
		}

		// Parse the ID as int64 to match our request IDs.
		var id int64
		if err := json.Unmarshal(resp.ID, &id); err != nil {
			continue
		}

		c.pendMu.Lock()
		ch, ok := c.pending[id]
		c.pendMu.Unlock()

		if ok {
			ch <- &resp
		}
	}

	// Scanner exited — server closed stdout or crashed.
	c.readErr = c.reader.Err()
	if c.readErr == nil {
		c.readErr = io.EOF
	}

	// Wake up all pending callers with nil responses.
	c.pendMu.Lock()
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.pendMu.Unlock()
}

// drainStderr reads the MCP server's stderr and logs it with a prefix.
// Prevents the subprocess from blocking on a full stderr pipe.
func (c *MCPClient) drainStderr() {
	if c.stderr == nil {
		return
	}
	scanner := bufio.NewScanner(c.stderr)
	for scanner.Scan() {
		fmt.Fprintf(os.Stderr, "[mcp:%s] %s\n", c.name, scanner.Text())
	}
}

// handleIncomingRequest processes a server-initiated JSON-RPC request
// (e.g., sampling/createMessage) and sends the response back. Runs in
// its own goroutine to avoid blocking readLoop.
func (c *MCPClient) handleIncomingRequest(method string, id, params json.RawMessage) {
	var result any
	var rpcErr *rpcError

	switch method {
	case "sampling/createMessage":
		if c.OnSampling == nil {
			rpcErr = &rpcError{Code: -1, Message: "sampling not supported by this client"}
		} else {
			var req MCPSamplingRequest
			if err := json.Unmarshal(params, &req); err != nil {
				rpcErr = &rpcError{Code: -32602, Message: "invalid sampling params: " + err.Error()}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
				defer cancel()
				resp, err := c.OnSampling(ctx, &req)
				if err != nil {
					rpcErr = &rpcError{Code: -1, Message: err.Error()}
				} else {
					result = resp
				}
			}
		}
	default:
		rpcErr = &rpcError{Code: -32601, Message: "method not found: " + method}
	}

	response := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
	}
	if rpcErr != nil {
		response.Error = rpcErr
	} else {
		response.Result = result
	}
	_ = c.writeJSON(response)
}

// --- Helpers ---

func mustMarshal(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func mustMarshalRaw(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// ResultText concatenates all text content from an MCPToolResult
// into a single string.
func (r *MCPToolResult) ResultText() string {
	var sb strings.Builder
	for _, c := range r.Content {
		if c.Text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(c.Text)
		}
	}
	return sb.String()
}
