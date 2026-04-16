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
				go io.Copy(io.Discard, serverInR)
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
