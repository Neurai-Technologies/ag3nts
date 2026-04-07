package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// OllamaClient handles HTTP communication with the Ollama API.
type OllamaClient struct {
	endpoint string
	client   *http.Client
}

// NewOllamaClient creates a client for the given Ollama endpoint.
// Validates the endpoint is localhost (security: SR-11).
func NewOllamaClient(endpoint string) (*OllamaClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil, fmt.Errorf("only localhost endpoints allowed (got %s)", host)
	}

	return &OllamaClient{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: 0, // no timeout — streaming can take minutes
		},
	}, nil
}

// ChatRequest is the request body for /api/chat.
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []Message     `json:"messages"`
	Stream   bool          `json:"stream"`
	Tools    []ToolDef     `json:"tools,omitempty"`
	Options  *ModelOptions `json:"options,omitempty"`
	KeepAlive string       `json:"keep_alive,omitempty"`
}

// ModelOptions controls model inference behavior.
type ModelOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumCtx      int     `json:"num_ctx,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

// ChatResponse is a single streaming chunk from /api/chat.
type ChatResponse struct {
	Model      string  `json:"model"`
	Message    Message `json:"message"`
	Done       bool    `json:"done"`
	DoneReason string  `json:"done_reason,omitempty"`

	// Usage (only populated when done=true).
	TotalDuration   int64 `json:"total_duration"`
	LoadDuration    int64 `json:"load_duration"`
	PromptEvalCount int   `json:"prompt_eval_count"`
	EvalCount       int   `json:"eval_count"`
	EvalDuration    int64 `json:"eval_duration"`
}

// StreamChat sends a chat request and streams responses.
// onChunk is called for each streaming chunk (text deltas).
// Returns the final complete assistant Message (which may contain ToolCalls).
func (c *OllamaClient) StreamChat(ctx context.Context, req ChatRequest, onChunk func(ChatResponse)) (Message, ChatResponse, error) {
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Message{}, ChatResponse{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return Message{}, ChatResponse{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return Message{}, ChatResponse{}, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(errBody))
	}

	// Accumulate the full assistant message from streaming chunks.
	var fullContent string
	var toolCalls []ToolCall
	var finalResp ChatResponse
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var chunk ChatResponse
		if err := json.Unmarshal(line, &chunk); err != nil {
			continue
		}

		if chunk.Message.Content != "" {
			fullContent += chunk.Message.Content
		}

		// Tool calls arrive on a non-done chunk, before the final done=true.
		if len(chunk.Message.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.Message.ToolCalls...)
		}

		if onChunk != nil && chunk.Message.Content != "" {
			onChunk(chunk)
		}

		if chunk.Done {
			finalResp = chunk
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return Message{}, ChatResponse{}, fmt.Errorf("read stream: %w", err)
	}

	// Build the complete assistant message.
	msg := Message{
		Role:      RoleAssistant,
		Content:   fullContent,
		ToolCalls: toolCalls,
	}

	return msg, finalResp, nil
}

// Chat sends a non-streaming chat request. Used for model-to-model calls
// (e.g., Qwen 3.5 calling Gemma 4 for deep reasoning).
func (c *OllamaClient) Chat(ctx context.Context, req ChatRequest) (Message, ChatResponse, error) {
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil {
		return Message{}, ChatResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.endpoint+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return Message{}, ChatResponse{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return Message{}, ChatResponse{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(resp.Body)
		return Message{}, ChatResponse{}, fmt.Errorf("ollama returned %d: %s", resp.StatusCode, string(errBody))
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return Message{}, ChatResponse{}, fmt.Errorf("decode response: %w", err)
	}

	msg := chatResp.Message
	msg.Role = RoleAssistant
	return msg, chatResp, nil
}

// Available checks if Ollama is reachable.
func (c *OllamaClient) Available() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.endpoint+"/api/tags", nil)
	if err != nil {
		return false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
