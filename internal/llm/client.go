package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"time"
)

// OllamaClient handles HTTP communication with the Ollama API.
type OllamaClient struct {
	endpoint   string
	client     *http.Client
	managedPid int // PID of ollama serve process we started (0 = external)
}

// NewOllamaClient creates a client for the given Ollama endpoint.
// Kills any existing Ollama, starts a fresh instance with modelsPath set,
// and waits for it to be ready. Validates localhost only (SR-11).
func NewOllamaClient(endpoint, modelsPath string) (*OllamaClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	host := u.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil, fmt.Errorf("only localhost endpoints allowed (got %s)", host)
	}

	oc := &OllamaClient{
		endpoint: endpoint,
		client: &http.Client{
			Timeout: 0,
		},
	}

	// Kill any existing Ollama so we can start fresh with the right OLLAMA_MODELS.
	_ = exec.Command("pkill", "-f", "ollama").Run()
	time.Sleep(1 * time.Second)

	// Start Ollama with the correct models path.
	pid, err := startOllamaServe(modelsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to start ollama: %w", err)
	}
	oc.managedPid = pid

	// Wait for it to be ready (up to 15 seconds).
	ready := false
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		if oc.Available() {
			ready = true
			break
		}
	}
	if !ready {
		oc.StopOllama()
		return nil, fmt.Errorf("ollama started but not responding after 15s")
	}

	return oc, nil
}

// StopOllama kills all Ollama processes — whether we started them or not.
func (c *OllamaClient) StopOllama() {
	// Use pkill to stop all ollama processes cleanly.
	_ = exec.Command("pkill", "-f", "ollama").Run()
	c.managedPid = 0
}

// startOllamaServe launches `ollama serve` as a background process
// with OLLAMA_MODELS set to the given path.
func startOllamaServe(modelsPath string) (int, error) {
	ollamaPath, err := exec.LookPath("ollama")
	if err != nil {
		return 0, fmt.Errorf("ollama binary not found: %w", err)
	}

	cmd := exec.Command(ollamaPath, "serve")
	cmd.Stdout = nil
	cmd.Stderr = nil

	// Set environment with OLLAMA_MODELS pointing to the SSD.
	env := os.Environ()
	if modelsPath != "" {
		env = append(env, "OLLAMA_MODELS="+modelsPath)
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("start ollama serve: %w", err)
	}

	return cmd.Process.Pid, nil
}

// ChatRequest is the request body for /api/chat.
type ChatRequest struct {
	Model    string        `json:"model"`
	Messages []Message     `json:"messages"`
	Stream   bool          `json:"stream"`
	Tools    []ToolDef     `json:"tools,omitempty"`
	Options  *ModelOptions `json:"options,omitempty"`
	KeepAlive any           `json:"keep_alive,omitempty"`
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
	decoder := json.NewDecoder(resp.Body)
	for decoder.More() {
		var chunk ChatResponse
		if err := decoder.Decode(&chunk); err != nil {
			return Message{}, ChatResponse{}, fmt.Errorf("decode stream chunk: %w", err)
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

	// Build the complete assistant message.
	msg := Message{
		Role:      RoleAssistant,
		Content:   fullContent,
		ToolCalls: toolCalls,
	}

	if !finalResp.Done {
		return Message{}, ChatResponse{}, fmt.Errorf("stream ended without done signal (partial content: %d bytes)", len(fullContent))
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
