package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// httpCallTimeout is the default deadline for an HTTP request
	// when the caller doesn't provide its own context deadline.
	httpCallTimeout = 2 * time.Minute
)

// MCPHTTPClient manages a persistent connection to a remote MCP server
// using the Streamable HTTP transport (MCP spec 2025-03-26).
//
// Client→Server: HTTP POST with JSON-RPC body.
// Server→Client: SSE stream via GET or within POST response.
// Session management: Mcp-Session-Id header on all requests after init.
//
// Thread-safe: multiple goroutines can call methods concurrently.
type MCPHTTPClient struct {
	name     string // human-readable name (config key)
	endpoint string // server URL (e.g., "https://example.com/mcp")
	client   *http.Client

	sessionID string // Mcp-Session-Id from initialize response
	authToken string // Bearer token for Authorization header (empty = no auth)

	serverInfo   map[string]any
	capabilities map[string]any

	nextID int64 // atomic request ID counter

	// SSE stream for server-initiated messages.
	sseMu     sync.Mutex
	sseCancel context.CancelFunc
	sseReader io.ReadCloser

	// Callbacks — same as MCPClient for manager compatibility.
	OnToolsChanged     func()
	OnResourcesChanged func()
	OnPromptsChanged   func()
	OnSampling         SamplingHandler

	closed int32 // atomic
}

// NewMCPHTTPClient creates a client for a remote MCP server using the
// Streamable HTTP transport. Performs the initialize handshake.
func NewMCPHTTPClient(name, endpoint string, authToken string) (*MCPHTTPClient, error) {
	c := &MCPHTTPClient{
		name:     name,
		endpoint: strings.TrimRight(endpoint, "/"),
		client: &http.Client{
			Timeout: 0, // per-request timeouts via context
		},
		authToken: authToken,
	}

	ctx, cancel := context.WithTimeout(context.Background(), initTimeout)
	defer cancel()

	if err := c.initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize %s: %w", name, err)
	}

	// Open SSE stream for server-initiated messages.
	go c.openSSEStream()

	return c, nil
}

// initialize performs the MCP handshake over HTTP POST.
func (c *MCPHTTPClient) initialize(ctx context.Context) error {
	resp, err := c.postJSON(ctx, "initialize", map[string]any{
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

	// Capture session ID from response (set by postJSON from headers).
	// Parse server capabilities.
	if result, ok := resp.Result.(map[string]any); ok {
		c.serverInfo, _ = result["serverInfo"].(map[string]any)
		c.capabilities, _ = result["capabilities"].(map[string]any)
	}

	// Send the initialized notification.
	return c.postNotification(ctx, "notifications/initialized", nil)
}

// HasCapability returns true if the server declared the given capability.
func (c *MCPHTTPClient) HasCapability(name string) bool {
	if c.capabilities == nil {
		return false
	}
	_, ok := c.capabilities[name]
	return ok
}

// Name returns the human-readable name of this MCP server.
func (c *MCPHTTPClient) Name() string {
	return c.name
}

// Alive returns true if the client hasn't been closed.
func (c *MCPHTTPClient) Alive() bool {
	return atomic.LoadInt32(&c.closed) == 0
}

// Close terminates the HTTP session.
func (c *MCPHTTPClient) Close() error {
	if !atomic.CompareAndSwapInt32(&c.closed, 0, 1) {
		return nil
	}
	// Cancel SSE stream.
	c.sseMu.Lock()
	if c.sseCancel != nil {
		c.sseCancel()
	}
	if c.sseReader != nil {
		_ = c.sseReader.Close()
	}
	c.sseMu.Unlock()

	// Send session termination (best-effort).
	if c.sessionID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "DELETE", c.endpoint, nil)
		if req != nil {
			c.setHeaders(req)
			_, _ = c.client.Do(req)
		}
	}
	return nil
}

// --- Tool methods ---

func (c *MCPHTTPClient) ListTools(ctx context.Context) ([]MCPTool, error) {
	resp, err := c.postJSON(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/list error: %s", resp.Error.Message)
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var result struct {
		Tools []MCPTool `json:"tools"`
	}
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("parse tools/list: %w", err)
	}
	return result.Tools, nil
}

func (c *MCPHTTPClient) CallTool(ctx context.Context, toolName string, arguments json.RawMessage) (*MCPToolResult, error) {
	resp, err := c.postJSON(ctx, "tools/call", map[string]any{
		"name":      toolName,
		"arguments": json.RawMessage(arguments),
	})
	if err != nil {
		return nil, fmt.Errorf("tools/call %s: %w", toolName, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("tools/call %s error: %s", toolName, resp.Error.Message)
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var result MCPToolResult
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		return nil, fmt.Errorf("parse tools/call: %w", err)
	}
	return &result, nil
}

// --- Resource methods ---

func (c *MCPHTTPClient) ListResources(ctx context.Context) ([]MCPResource, error) {
	if !c.HasCapability("resources") {
		return nil, nil
	}
	resp, err := c.postJSON(ctx, "resources/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("resources/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/list error: %s", resp.Error.Message)
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var result struct {
		Resources []MCPResource `json:"resources"`
	}
	_ = json.Unmarshal(resultBytes, &result)
	return result.Resources, nil
}

func (c *MCPHTTPClient) ReadResource(ctx context.Context, uri string) ([]MCPResourceContents, error) {
	resp, err := c.postJSON(ctx, "resources/read", map[string]any{"uri": uri})
	if err != nil {
		return nil, fmt.Errorf("resources/read: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("resources/read error: %s", resp.Error.Message)
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var result struct {
		Contents []MCPResourceContents `json:"contents"`
	}
	_ = json.Unmarshal(resultBytes, &result)
	return result.Contents, nil
}

// --- Prompt methods ---

func (c *MCPHTTPClient) ListPrompts(ctx context.Context) ([]MCPPrompt, error) {
	if !c.HasCapability("prompts") {
		return nil, nil
	}
	resp, err := c.postJSON(ctx, "prompts/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("prompts/list: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("prompts/list error: %s", resp.Error.Message)
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var result struct {
		Prompts []MCPPrompt `json:"prompts"`
	}
	_ = json.Unmarshal(resultBytes, &result)
	return result.Prompts, nil
}

func (c *MCPHTTPClient) GetPrompt(ctx context.Context, name string, arguments map[string]string) ([]MCPPromptMessage, string, error) {
	params := map[string]any{"name": name}
	if len(arguments) > 0 {
		params["arguments"] = arguments
	}
	resp, err := c.postJSON(ctx, "prompts/get", params)
	if err != nil {
		return nil, "", fmt.Errorf("prompts/get: %w", err)
	}
	if resp.Error != nil {
		return nil, "", fmt.Errorf("prompts/get error: %s", resp.Error.Message)
	}
	resultBytes, _ := json.Marshal(resp.Result)
	var result struct {
		Messages    []MCPPromptMessage `json:"messages"`
		Description string             `json:"description"`
	}
	_ = json.Unmarshal(resultBytes, &result)
	return result.Messages, result.Description, nil
}

// --- HTTP plumbing ---

// postJSON sends a JSON-RPC request via HTTP POST and returns the response.
func (c *MCPHTTPClient) postJSON(ctx context.Context, method string, params any) (*jsonRPCResponse, error) {
	id := atomic.AddInt64(&c.nextID, 1)

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      mustMarshal(id),
		Method:  method,
	}
	if params != nil {
		req.Params = mustMarshalRaw(params)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json, text/event-stream")

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close()

	// Capture session ID from response headers.
	if sid := httpResp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}

	if httpResp.StatusCode == http.StatusAccepted {
		// 202 = notification acknowledged, no body.
		return &jsonRPCResponse{JSONRPC: "2.0"}, nil
	}

	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", httpResp.StatusCode, httpResp.Status)
	}

	contentType := httpResp.Header.Get("Content-Type")

	// Handle SSE response (server streams the response).
	if strings.HasPrefix(contentType, "text/event-stream") {
		return c.readSSEResponse(httpResp.Body, id)
	}

	// Handle JSON response (direct).
	respBody, err := io.ReadAll(io.LimitReader(httpResp.Body, 10*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	return &resp, nil
}

// postNotification sends a JSON-RPC notification (no response expected).
func (c *MCPHTTPClient) postNotification(ctx context.Context, method string, params any) error {
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
	}
	if params != nil {
		req.Params = mustMarshalRaw(params)
	}

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	c.setHeaders(httpReq)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	_, _ = io.Copy(io.Discard, httpResp.Body)

	if sid := httpResp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}
	return nil
}

// setHeaders adds common headers to an HTTP request.
func (c *MCPHTTPClient) setHeaders(req *http.Request) {
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}
}

// readSSEResponse reads a Server-Sent Events stream from a POST response,
// looking for the JSON-RPC response matching the given request ID.
func (c *MCPHTTPClient) readSSEResponse(r io.Reader, requestID int64) (*jsonRPCResponse, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
			continue
		}

		// Blank line = event boundary.
		if line == "" && len(dataLines) > 0 {
			data := strings.Join(dataLines, "\n")
			dataLines = nil

			var resp jsonRPCResponse
			if err := json.Unmarshal([]byte(data), &resp); err != nil {
				continue
			}

			// Check if this is our response.
			if len(resp.ID) > 0 {
				var id int64
				if json.Unmarshal(resp.ID, &id) == nil && id == requestID {
					return &resp, nil
				}
			}

			// Could be a server-initiated notification — handle it.
			var msg struct {
				Method string `json:"method,omitempty"`
			}
			_ = json.Unmarshal([]byte(data), &msg)
			c.handleNotification(msg.Method)
		}
	}
	return nil, fmt.Errorf("SSE stream ended without response for request %d", requestID)
}

// openSSEStream opens a long-lived GET connection for server-initiated
// messages (notifications, sampling requests). Runs in a goroutine.
func (c *MCPHTTPClient) openSSEStream() {
	if c.sessionID == "" {
		return // no session to subscribe to
	}

	ctx, cancel := context.WithCancel(context.Background())
	c.sseMu.Lock()
	c.sseCancel = cancel
	c.sseMu.Unlock()

	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint, nil)
	if err != nil {
		return
	}
	c.setHeaders(req)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.client.Do(req)
	if err != nil {
		return // server may not support GET (405)
	}

	c.sseMu.Lock()
	c.sseReader = resp.Body
	c.sseMu.Unlock()

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var dataLines []string

	for scanner.Scan() {
		if atomic.LoadInt32(&c.closed) != 0 {
			return
		}
		line := scanner.Text()

		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
			continue
		}

		if line == "" && len(dataLines) > 0 {
			data := strings.Join(dataLines, "\n")
			dataLines = nil

			var msg struct {
				Method string          `json:"method,omitempty"`
				ID     json.RawMessage `json:"id,omitempty"`
				Params json.RawMessage `json:"params,omitempty"`
			}
			if json.Unmarshal([]byte(data), &msg) != nil {
				continue
			}

			// Incoming request (has method + ID): e.g., sampling/createMessage.
			if msg.Method != "" && len(msg.ID) > 0 && string(msg.ID) != "null" {
				go c.handleIncomingRequest(msg.Method, msg.ID, msg.Params)
				continue
			}

			// Notification (has method, no ID).
			if msg.Method != "" {
				c.handleNotification(msg.Method)
			}
		}
	}
}

// handleNotification dispatches server-initiated notifications.
func (c *MCPHTTPClient) handleNotification(method string) {
	switch method {
	case "notifications/tools/list_changed":
		if c.OnToolsChanged != nil {
			go c.OnToolsChanged()
		}
	case "notifications/resources/list_changed":
		if c.OnResourcesChanged != nil {
			go c.OnResourcesChanged()
		}
	case "notifications/prompts/list_changed":
		if c.OnPromptsChanged != nil {
			go c.OnPromptsChanged()
		}
	}
}

// handleIncomingRequest processes server-initiated requests (sampling).
func (c *MCPHTTPClient) handleIncomingRequest(method string, id, params json.RawMessage) {
	var result any
	var rpcErr *rpcError

	switch method {
	case "sampling/createMessage":
		if c.OnSampling == nil {
			rpcErr = &rpcError{Code: -1, Message: "sampling not supported"}
		} else {
			var req MCPSamplingRequest
			if err := json.Unmarshal(params, &req); err != nil {
				rpcErr = &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), httpCallTimeout)
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

	// Send response back via POST.
	response := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
	}
	if rpcErr != nil {
		response.Error = rpcErr
	} else {
		response.Result = result
	}

	body, _ := json.Marshal(response)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if httpReq != nil {
		c.setHeaders(httpReq)
		httpReq.Header.Set("Content-Type", "application/json")
		resp, err := c.client.Do(httpReq)
		if err == nil {
			_ = resp.Body.Close()
		}
	}
}
