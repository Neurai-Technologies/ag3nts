package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/rohanrgit/ag3nts/internal/paths"
)

// jsonRPCRequest represents an incoming JSON-RPC 2.0 request.
type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// jsonRPCResponse represents an outgoing JSON-RPC 2.0 response.
type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	maxRequestSize = 10 * 1024 * 1024 // SR-3: 10 MB max request size
	maxPromptLen   = 1024 * 1024       // SR-3: 1 MB max prompt length
)

// Serve runs the MCP server on stdin/stdout using JSON-RPC over stdio.
func Serve(layout *paths.Layout) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), maxRequestSize)
	writer := os.Stdout

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			writeError(writer, nil, -32700, "Parse error")
			continue
		}

		resp := handleRequest(req, layout)
		writeResponse(writer, resp)
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	return nil
}

func handleRequest(req jsonRPCRequest, layout *paths.Layout) jsonRPCResponse {
	switch req.Method {
	case "initialize":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities": map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				"serverInfo": map[string]interface{}{
					"name":    "ag3nts",
					"version": "0.1.0",
				},
			},
		}

	case "notifications/initialized":
		// Client acknowledgment — no response needed for notifications
		return jsonRPCResponse{}

	case "tools/list":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"tools": getToolDefinitions(),
			},
		}

	case "tools/call":
		return handleToolCall(req, layout)

	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

func getToolDefinitions() []map[string]interface{} {
	return []map[string]interface{}{
		{
			"name":        "gemini_query",
			"description": "Send a prompt to Gemini CLI and get a response. Best for: research, exploration, large-context analysis, Google ecosystem tasks. Uses your Gemini Pro subscription (free tier: 1000 req/day).",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "The prompt to send to Gemini",
					},
				},
				"required": []string{"prompt"},
			},
		},
		{
			"name":        "codex_query",
			"description": "Send a prompt to Codex CLI (OpenAI) and get a response. Best for: alternative code perspectives, OpenAI model strengths, second opinions. Uses your ChatGPT Plus/Pro subscription.",
			"inputSchema": map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"prompt": map[string]interface{}{
						"type":        "string",
						"description": "The prompt to send to Codex CLI",
					},
				},
				"required": []string{"prompt"},
			},
		},
	}
}

func handleToolCall(req jsonRPCRequest, layout *paths.Layout) jsonRPCResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "Invalid params"},
		}
	}

	var args struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(params.Arguments, &args); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: "Invalid arguments: prompt required"},
		}
	}

	// SR-3: Validate prompt length
	if len(args.Prompt) > maxPromptLen {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: fmt.Sprintf("Prompt too large (%d bytes, max %d)", len(args.Prompt), maxPromptLen)},
		}
	}

	var result string
	var err error

	switch params.Name {
	case "gemini_query":
		result, err = queryGemini(args.Prompt, layout)
	case "codex_query":
		result, err = queryCodex(args.Prompt, layout)
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32602, Message: fmt.Sprintf("Unknown tool: %s", params.Name)},
		}
	}

	if err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]interface{}{
				"content": []map[string]interface{}{
					{"type": "text", "text": fmt.Sprintf("Error: %v", err)},
				},
				"isError": true,
			},
		}
	}

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{"type": "text", "text": result},
			},
		},
	}
}

func writeResponse(w io.Writer, resp jsonRPCResponse) {
	// Don't write empty responses (for notifications)
	if resp.JSONRPC == "" && resp.Result == nil && resp.Error == nil {
		return
	}
	data, _ := json.Marshal(resp)
	fmt.Fprintf(w, "%s\n", data)
}

func writeError(w io.Writer, id json.RawMessage, code int, msg string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
	data, _ := json.Marshal(resp)
	fmt.Fprintf(w, "%s\n", data)
}
