package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// mockMCPServer simulates an MCP server for testing. Reads JSON-RPC
// requests from the reader, dispatches via the handler, writes
// responses to the writer. Runs as a goroutine.
type mockMCPServer struct {
	handler func(req jsonRPCRequest) *jsonRPCResponse
}

// newTestClientWithHandler creates an MCPClient connected to a mock
// server via in-memory pipes. The handler is called for each request;
// return nil to skip the response (simulate a notification).
func newTestClientWithHandler(t *testing.T, handler func(req jsonRPCRequest) *jsonRPCResponse) *MCPClient {
	t.Helper()

	// Client writes to serverIn, reads from serverOut.
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()

	server := &mockMCPServer{handler: handler}
	go server.run(serverInR, serverOutW)

	client := newMCPClientFromPipes("test", serverInW, serverOutR)
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverInR.Close()
		_ = serverOutW.Close()
	})
	return client
}

func (s *mockMCPServer) run(reader io.Reader, writer io.WriteCloser) {
	defer writer.Close()
	scanner := newLineScanner(reader)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		var req jsonRPCRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		resp := s.handler(req)
		if resp == nil {
			continue // notification or no-response
		}
		data, _ := json.Marshal(resp)
		fmt.Fprintf(writer, "%s\n", data)
	}
}

func newLineScanner(r io.Reader) *lineScanner {
	return &lineScanner{r: r, buf: make([]byte, 0, 4096)}
}

// lineScanner is a minimal line reader that doesn't buffer ahead
// (unlike bufio.Scanner which may read past the first line).
type lineScanner struct {
	r    io.Reader
	buf  []byte
	line string
	err  error
}

func (s *lineScanner) Scan() bool {
	for {
		// Check if we have a complete line in the buffer.
		if idx := indexOf(s.buf, '\n'); idx >= 0 {
			s.line = string(s.buf[:idx])
			s.buf = s.buf[idx+1:]
			return true
		}
		// Read more.
		tmp := make([]byte, 4096)
		n, err := s.r.Read(tmp)
		if n > 0 {
			s.buf = append(s.buf, tmp[:n]...)
		}
		if err != nil {
			s.err = err
			if len(s.buf) > 0 {
				s.line = string(s.buf)
				s.buf = nil
				return true
			}
			return false
		}
	}
}

func (s *lineScanner) Text() string { return s.line }

func indexOf(b []byte, c byte) int {
	for i, v := range b {
		if v == c {
			return i
		}
	}
	return -1
}

// --- Tests ---

func TestMCPClient_InitializeAndListTools(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
					"serverInfo":      map[string]any{"name": "test-server"},
				},
			}
		case "tools/list":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"tools": []any{
						map[string]any{
							"name":        "query",
							"description": "Run a SQL query",
							"inputSchema": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"sql": map[string]any{
										"type":        "string",
										"description": "The SQL query",
									},
								},
								"required": []any{"sql"},
							},
						},
						map[string]any{
							"name":        "list_tables",
							"description": "List all tables",
							"inputSchema": map[string]any{
								"type":       "object",
								"properties": map[string]any{},
							},
						},
					},
				},
			}
		}
		return nil
	})

	// Initialize.
	ctx := context.Background()
	if err := client.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// List tools.
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	if tools[0].Name != "query" {
		t.Errorf("tool[0].Name = %q, want query", tools[0].Name)
	}
	if tools[1].Name != "list_tables" {
		t.Errorf("tool[1].Name = %q, want list_tables", tools[1].Name)
	}
	// Verify inputSchema is preserved as raw JSON.
	if len(tools[0].InputSchema) == 0 {
		t.Error("tool[0].InputSchema should be non-empty raw JSON")
	}
}

func TestMCPClient_CallTool(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
		case "tools/call":
			var params struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"content": []any{
						map[string]any{"type": "text", "text": "result for " + params.Name},
					},
				},
			}
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	args, _ := json.Marshal(map[string]string{"sql": "SELECT 1"})
	result, err := client.CallTool(ctx, "query", args)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	text := result.ResultText()
	if !strings.Contains(text, "result for query") {
		t.Errorf("result text = %q, want 'result for query'", text)
	}
}

func TestMCPClient_CallToolError(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
		case "tools/call":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"content": []any{
						map[string]any{"type": "text", "text": "table not found"},
					},
					"isError": true,
				},
			}
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	args, _ := json.Marshal(map[string]string{})
	result, err := client.CallTool(ctx, "query", args)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !result.IsError {
		t.Error("expected isError=true")
	}
	if !strings.Contains(result.ResultText(), "table not found") {
		t.Errorf("error text = %q", result.ResultText())
	}
}

func TestMCPClient_ConcurrentCalls(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
		case "tools/call":
			// Simulate some processing time.
			time.Sleep(10 * time.Millisecond)
			var params struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(req.Params, &params)
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"content": []any{
						map[string]any{"type": "text", "text": "done:" + params.Name},
					},
				},
			}
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	var wg sync.WaitGroup
	errors := make([]error, 5)
	results := make([]string, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("tool_%d", idx)
			args, _ := json.Marshal(map[string]string{})
			result, err := client.CallTool(ctx, name, args)
			if err != nil {
				errors[idx] = err
				return
			}
			results[idx] = result.ResultText()
		}(i)
	}
	wg.Wait()

	for i, err := range errors {
		if err != nil {
			t.Errorf("call %d failed: %v", i, err)
		}
	}
	for i, r := range results {
		if r == "" {
			t.Errorf("call %d returned empty result", i)
		}
	}
}

func TestMCPClient_ServerCrash(t *testing.T) {
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()

	// Server that handles initialize + notification, then crashes.
	go func() {
		scanner := newLineScanner(serverInR)
		initialized := false
		for scanner.Scan() {
			var req jsonRPCRequest
			if err := json.Unmarshal([]byte(scanner.Text()), &req); err != nil {
				continue
			}
			if req.Method == "initialize" {
				resp := jsonRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result:  map[string]any{},
				}
				data, _ := json.Marshal(resp)
				fmt.Fprintf(serverOutW, "%s\n", data)
				initialized = true
				continue
			}
			if req.Method == "notifications/initialized" && initialized {
				// Simulate crash after handshake completes.
				time.Sleep(50 * time.Millisecond)
				serverOutW.Close()
				// Drain remaining writes so the pipe doesn't block.
				go func() { _, _ = io.Copy(io.Discard, serverInR) }()
				return
			}
		}
	}()

	client := newMCPClientFromPipes("crash-test", serverInW, serverOutR)
	defer func() {
		_ = client.Close()
		_ = serverInR.Close()
	}()

	ctx := context.Background()
	_ = client.initialize(ctx)

	// Wait for the server to crash.
	time.Sleep(100 * time.Millisecond)

	if client.Alive() {
		t.Error("expected client to detect server crash")
	}

	// Further calls should fail.
	args, _ := json.Marshal(map[string]string{})
	_, err := client.CallTool(ctx, "anything", args)
	if err == nil {
		t.Error("expected error after server crash")
	}
}

func TestMCPClient_CallTimeout(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}
		case "tools/call":
			// Hang forever — never respond.
			select {}
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	callCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	args, _ := json.Marshal(map[string]string{})
	_, err := client.CallTool(callCtx, "hanging", args)
	if err == nil {
		t.Error("expected timeout error")
	}
	if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "context") {
		t.Errorf("expected context deadline error, got: %v", err)
	}
}

// --- Resource tests ---

func TestMCPClient_ListResources(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities": map[string]any{
						"resources": map[string]any{},
					},
					"serverInfo": map[string]any{"name": "test-server"},
				},
			}
		case "resources/list":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"resources": []any{
						map[string]any{
							"uri":         "file:///etc/config.json",
							"name":        "Config",
							"description": "Application configuration",
							"mimeType":    "application/json",
						},
						map[string]any{
							"uri":  "db://users/schema",
							"name": "Users Schema",
						},
					},
				},
			}
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	resources, err := client.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("expected 2 resources, got %d", len(resources))
	}
	if resources[0].URI != "file:///etc/config.json" {
		t.Errorf("resource[0].URI = %q", resources[0].URI)
	}
	if resources[0].Name != "Config" {
		t.Errorf("resource[0].Name = %q", resources[0].Name)
	}
	if resources[0].MimeType != "application/json" {
		t.Errorf("resource[0].MimeType = %q", resources[0].MimeType)
	}
	if resources[1].URI != "db://users/schema" {
		t.Errorf("resource[1].URI = %q", resources[1].URI)
	}
}

func TestMCPClient_ListResources_NoCapability(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"tools": map[string]any{}},
				},
			}
		case "resources/list":
			t.Error("resources/list should not be called when server lacks resources capability")
			return nil
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	resources, err := client.ListResources(ctx)
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if resources != nil {
		t.Errorf("expected nil resources, got %v", resources)
	}
}

func TestMCPClient_ReadResource(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"resources": map[string]any{}},
				},
			}
		case "resources/read":
			var params struct {
				URI string `json:"uri"`
			}
			_ = json.Unmarshal(req.Params, &params)
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"contents": []any{
						map[string]any{
							"uri":      params.URI,
							"mimeType": "application/json",
							"text":     `{"key": "value"}`,
						},
					},
				},
			}
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	contents, err := client.ReadResource(ctx, "file:///etc/config.json")
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(contents))
	}
	if contents[0].Text != `{"key": "value"}` {
		t.Errorf("content text = %q", contents[0].Text)
	}
	if contents[0].MimeType != "application/json" {
		t.Errorf("content mimeType = %q", contents[0].MimeType)
	}
}

// --- Prompt tests ---

func TestMCPClient_ListPrompts(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"prompts": map[string]any{}},
				},
			}
		case "prompts/list":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"prompts": []any{
						map[string]any{
							"name":        "review-pr",
							"description": "Review a pull request",
							"arguments": []any{
								map[string]any{
									"name":        "pr_number",
									"description": "The PR number to review",
									"required":    true,
								},
							},
						},
						map[string]any{
							"name":        "summarize",
							"description": "Summarize a document",
						},
					},
				},
			}
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got %d", len(prompts))
	}
	if prompts[0].Name != "review-pr" {
		t.Errorf("prompt[0].Name = %q", prompts[0].Name)
	}
	if prompts[0].Description != "Review a pull request" {
		t.Errorf("prompt[0].Description = %q", prompts[0].Description)
	}
	if len(prompts[0].Arguments) != 1 {
		t.Fatalf("expected 1 argument, got %d", len(prompts[0].Arguments))
	}
	if prompts[0].Arguments[0].Name != "pr_number" {
		t.Errorf("arg.Name = %q", prompts[0].Arguments[0].Name)
	}
	if !prompts[0].Arguments[0].Required {
		t.Error("arg.Required should be true")
	}
}

func TestMCPClient_GetPrompt(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{"prompts": map[string]any{}},
				},
			}
		case "prompts/get":
			var params struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"description": "Review PR #" + params.Arguments["pr_number"],
					"messages": []any{
						map[string]any{
							"role": "user",
							"content": map[string]any{
								"type": "text",
								"text": "Please review PR #" + params.Arguments["pr_number"],
							},
						},
					},
				},
			}
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	messages, desc, err := client.GetPrompt(ctx, "review-pr", map[string]string{"pr_number": "42"})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	if desc != "Review PR #42" {
		t.Errorf("description = %q", desc)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Role != "user" {
		t.Errorf("message.Role = %q", messages[0].Role)
	}
	if !strings.Contains(messages[0].Content.Text, "PR #42") {
		t.Errorf("message text = %q", messages[0].Content.Text)
	}
}

func TestMCPClient_ListPrompts_NoCapability(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		switch req.Method {
		case "initialize":
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"capabilities": map[string]any{},
				},
			}
		case "prompts/list":
			t.Error("prompts/list should not be called when server lacks prompts capability")
			return nil
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	prompts, err := client.ListPrompts(ctx)
	if err != nil {
		t.Fatalf("ListPrompts: %v", err)
	}
	if prompts != nil {
		t.Errorf("expected nil prompts, got %v", prompts)
	}
}

// --- Sampling tests ---

func TestMCPClient_SamplingRequest(t *testing.T) {
	// For sampling, the SERVER sends a request to the CLIENT.
	// We need a special test setup: the mock server sends a
	// sampling/createMessage request after initialize.

	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()

	// Track what the server receives back.
	var samplingResponse jsonRPCResponse
	var gotResponse bool
	var responseMu sync.Mutex

	go func() {
		scanner := newLineScanner(serverInR)
		for scanner.Scan() {
			var msg struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id,omitempty"`
				Method  string          `json:"method,omitempty"`
				Result  json.RawMessage `json:"result,omitempty"`
				Error   json.RawMessage `json:"error,omitempty"`
			}
			if err := json.Unmarshal([]byte(scanner.Text()), &msg); err != nil {
				continue
			}

			if msg.Method == "initialize" {
				resp := jsonRPCResponse{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Result: map[string]any{
						"protocolVersion": "2024-11-05",
						"capabilities":    map[string]any{},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Fprintf(serverOutW, "%s\n", data)
				continue
			}
			if msg.Method == "notifications/initialized" {
				// After handshake, server sends a sampling request.
				sampReq := map[string]any{
					"jsonrpc": "2.0",
					"id":      99,
					"method":  "sampling/createMessage",
					"params": map[string]any{
						"messages": []any{
							map[string]any{
								"role":    "user",
								"content": map[string]any{"type": "text", "text": "What is 2+2?"},
							},
						},
						"maxTokens": 100,
					},
				}
				data, _ := json.Marshal(sampReq)
				fmt.Fprintf(serverOutW, "%s\n", data)
				continue
			}

			// This should be the sampling response from the client.
			if msg.Method == "" && len(msg.ID) > 0 {
				responseMu.Lock()
				_ = json.Unmarshal([]byte(scanner.Text()), &samplingResponse)
				gotResponse = true
				responseMu.Unlock()
				// Close after receiving response.
				serverOutW.Close()
				go func() { _, _ = io.Copy(io.Discard, serverInR) }()
				return
			}
		}
	}()

	client := newMCPClientFromPipes("sampling-test", serverInW, serverOutR)
	defer func() {
		_ = client.Close()
		_ = serverInR.Close()
	}()

	// Set up sampling handler.
	client.OnSampling = func(ctx context.Context, req *MCPSamplingRequest) (*MCPSamplingResponse, error) {
		if len(req.Messages) == 0 {
			return nil, fmt.Errorf("no messages")
		}
		return &MCPSamplingResponse{
			Role:       "assistant",
			Content:    MCPContent{Type: "text", Text: "4"},
			Model:      "test-model",
			StopReason: "endTurn",
		}, nil
	}

	ctx := context.Background()
	if err := client.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	// Wait for the server to receive the sampling response.
	deadline := time.After(5 * time.Second)
	for {
		responseMu.Lock()
		done := gotResponse
		responseMu.Unlock()
		if done {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for sampling response")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Verify the response.
	resultBytes, _ := json.Marshal(samplingResponse.Result)
	var result MCPSamplingResponse
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("parse sampling response: %v", err)
	}
	if result.Content.Text != "4" {
		t.Errorf("sampling result text = %q, want '4'", result.Content.Text)
	}
	if result.Model != "test-model" {
		t.Errorf("sampling result model = %q", result.Model)
	}
}

func TestMCPClient_SamplingNoHandler(t *testing.T) {
	// Server sends a sampling request but client has no handler.
	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()

	var gotError bool
	var responseMu sync.Mutex

	go func() {
		scanner := newLineScanner(serverInR)
		for scanner.Scan() {
			var msg struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id,omitempty"`
				Method  string          `json:"method,omitempty"`
				Error   *rpcError       `json:"error,omitempty"`
			}
			if err := json.Unmarshal([]byte(scanner.Text()), &msg); err != nil {
				continue
			}

			if msg.Method == "initialize" {
				resp := jsonRPCResponse{
					JSONRPC: "2.0",
					ID:      msg.ID,
					Result:  map[string]any{},
				}
				data, _ := json.Marshal(resp)
				fmt.Fprintf(serverOutW, "%s\n", data)
				continue
			}
			if msg.Method == "notifications/initialized" {
				// Send sampling request.
				sampReq := map[string]any{
					"jsonrpc": "2.0",
					"id":      50,
					"method":  "sampling/createMessage",
					"params": map[string]any{
						"messages":  []any{},
						"maxTokens": 10,
					},
				}
				data, _ := json.Marshal(sampReq)
				fmt.Fprintf(serverOutW, "%s\n", data)
				continue
			}
			// Response from client (should be error).
			if msg.Method == "" && msg.Error != nil {
				responseMu.Lock()
				gotError = true
				responseMu.Unlock()
				serverOutW.Close()
				go func() { _, _ = io.Copy(io.Discard, serverInR) }()
				return
			}
			// Also check for error in full parse
			if msg.Method == "" {
				var full struct {
					Error *rpcError `json:"error"`
				}
				_ = json.Unmarshal([]byte(scanner.Text()), &full)
				if full.Error != nil {
					responseMu.Lock()
					gotError = true
					responseMu.Unlock()
					serverOutW.Close()
					go io.Copy(io.Discard, serverInR)
					return
				}
			}
		}
	}()

	client := newMCPClientFromPipes("sampling-no-handler", serverInW, serverOutR)
	// Deliberately do NOT set OnSampling.
	defer func() {
		_ = client.Close()
		_ = serverInR.Close()
	}()

	ctx := context.Background()
	_ = client.initialize(ctx)

	deadline := time.After(5 * time.Second)
	for {
		responseMu.Lock()
		done := gotError
		responseMu.Unlock()
		if done {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for error response")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	// If we get here, the client correctly returned an error.
}

// --- Notification tests ---

func TestMCPClient_ResourcesChangedNotification(t *testing.T) {
	notified := make(chan struct{}, 1)

	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()

	go func() {
		scanner := newLineScanner(serverInR)
		for scanner.Scan() {
			var req jsonRPCRequest
			if err := json.Unmarshal([]byte(scanner.Text()), &req); err != nil {
				continue
			}
			if req.Method == "initialize" {
				resp := jsonRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]any{
						"capabilities": map[string]any{"resources": map[string]any{}},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Fprintf(serverOutW, "%s\n", data)
			}
			if req.Method == "notifications/initialized" {
				// Send a resources-changed notification.
				notif := map[string]any{
					"jsonrpc": "2.0",
					"method":  "notifications/resources/list_changed",
				}
				data, _ := json.Marshal(notif)
				fmt.Fprintf(serverOutW, "%s\n", data)
				time.Sleep(100 * time.Millisecond)
				serverOutW.Close()
				go io.Copy(io.Discard, serverInR)
				return
			}
		}
	}()

	client := newMCPClientFromPipes("notif-test", serverInW, serverOutR)
	client.OnResourcesChanged = func() {
		notified <- struct{}{}
	}
	defer func() {
		_ = client.Close()
		_ = serverInR.Close()
	}()

	ctx := context.Background()
	_ = client.initialize(ctx)

	select {
	case <-notified:
		// Success.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for resources-changed notification")
	}
}

func TestMCPClient_PromptsChangedNotification(t *testing.T) {
	notified := make(chan struct{}, 1)

	serverInR, serverInW := io.Pipe()
	serverOutR, serverOutW := io.Pipe()

	go func() {
		scanner := newLineScanner(serverInR)
		for scanner.Scan() {
			var req jsonRPCRequest
			if err := json.Unmarshal([]byte(scanner.Text()), &req); err != nil {
				continue
			}
			if req.Method == "initialize" {
				resp := jsonRPCResponse{
					JSONRPC: "2.0",
					ID:      req.ID,
					Result: map[string]any{
						"capabilities": map[string]any{"prompts": map[string]any{}},
					},
				}
				data, _ := json.Marshal(resp)
				fmt.Fprintf(serverOutW, "%s\n", data)
			}
			if req.Method == "notifications/initialized" {
				notif := map[string]any{
					"jsonrpc": "2.0",
					"method":  "notifications/prompts/list_changed",
				}
				data, _ := json.Marshal(notif)
				fmt.Fprintf(serverOutW, "%s\n", data)
				time.Sleep(100 * time.Millisecond)
				serverOutW.Close()
				go io.Copy(io.Discard, serverInR)
				return
			}
		}
	}()

	client := newMCPClientFromPipes("prompt-notif-test", serverInW, serverOutR)
	client.OnPromptsChanged = func() {
		notified <- struct{}{}
	}
	defer func() {
		_ = client.Close()
		_ = serverInR.Close()
	}()

	ctx := context.Background()
	_ = client.initialize(ctx)

	select {
	case <-notified:
		// Success.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for prompts-changed notification")
	}
}

func TestMCPClient_HasCapability(t *testing.T) {
	client := newTestClientWithHandler(t, func(req jsonRPCRequest) *jsonRPCResponse {
		if req.Method == "initialize" {
			return &jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"capabilities": map[string]any{
						"tools":     map[string]any{},
						"resources": map[string]any{"subscribe": true},
					},
				},
			}
		}
		return nil
	})

	ctx := context.Background()
	_ = client.initialize(ctx)

	if !client.HasCapability("tools") {
		t.Error("expected tools capability")
	}
	if !client.HasCapability("resources") {
		t.Error("expected resources capability")
	}
	if client.HasCapability("prompts") {
		t.Error("should not have prompts capability")
	}
	if client.HasCapability("sampling") {
		t.Error("should not have sampling capability (it's a client capability)")
	}
}
