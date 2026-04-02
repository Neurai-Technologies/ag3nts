package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// HTTPAgent connects to local model servers (Ollama, OpenAI-compatible)
// via HTTP for streaming chat completions.
type HTTPAgent struct {
	name         string
	endpoint     string // e.g. "http://localhost:11434"
	model        string
	capabilities []string
	client       *http.Client

	mu       sync.Mutex     // B-2 fix: protects messages
	messages []chatMessage  // conversation history for multi-turn
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// HTTPConfig holds the configuration for creating an HTTPAgent.
type HTTPConfig struct {
	Name         string
	Endpoint     string
	Model        string
	Capabilities []string
}

// NewHTTPAgent creates an HTTP-backed agent.
// SR-11: Validates that the endpoint resolves to localhost.
func NewHTTPAgent(cfg HTTPConfig) (*HTTPAgent, error) {
	if err := validateLocalhost(cfg.Endpoint); err != nil {
		return nil, fmt.Errorf("SR-11: %w", err)
	}

	return &HTTPAgent{
		name:         cfg.Name,
		endpoint:     strings.TrimRight(cfg.Endpoint, "/"),
		model:        cfg.Model,
		capabilities: cfg.Capabilities,
		client: &http.Client{
			Timeout: 0, // no global timeout — context handles cancellation
		},
	}, nil
}

func (a *HTTPAgent) Name() string        { return a.name }
func (a *HTTPAgent) Capabilities() []string { return a.capabilities }

// Available checks if the HTTP server is reachable.
func (a *HTTPAgent) Available() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", a.endpoint, nil)
	if err != nil {
		return false
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

// Start sends a prompt to the model server and streams the response as AgentEvents.
func (a *HTTPAgent) Start(ctx context.Context, prompt string, opts *StartOpts) (*Session, error) {
	if opts == nil {
		opts = &StartOpts{}
	}

	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("%s-%d", a.name, time.Now().UnixNano())
	}

	fullPrompt := prompt
	if opts.Context != "" {
		fullPrompt = opts.Context + "\n\n" + prompt
	}

	model := a.model
	if opts.Model != "" {
		model = opts.Model
	}

	// B-2 fix: protect messages with mutex, snapshot for goroutine.
	a.mu.Lock()
	if opts.SessionID == "" {
		a.messages = nil
	}
	a.messages = append(a.messages, chatMessage{Role: "user", Content: fullPrompt})
	msgs := make([]chatMessage, len(a.messages))
	copy(msgs, a.messages)
	a.mu.Unlock()

	sessCtx, cancel := context.WithCancel(ctx)
	session := NewSession(sessionID, a.name, opts.TaskID, 256, cancel)

	// Determine API style based on endpoint.
	go func() {
		// B-3 fix: track status explicitly, don't use defer with hardcoded status.
		finalStatus := StatusStopped

		session.Emit(AgentEvent{
			Kind:      EventInit,
			Agent:     a.name,
			SessionID: sessionID,
			TaskID:    opts.TaskID,
			Content:   fmt.Sprintf("Connected to %s (%s)", a.name, model),
			Timestamp: time.Now(),
		})

		var err error
		if isOllamaEndpoint(a.endpoint) {
			err = a.streamOllama(sessCtx, session, model, opts.TaskID, msgs)
		} else {
			err = a.streamOpenAI(sessCtx, session, model, opts.TaskID, msgs)
		}

		if err != nil {
			finalStatus = StatusFailed
			session.Emit(AgentEvent{
				Kind:      EventError,
				Agent:     a.name,
				SessionID: sessionID,
				TaskID:    opts.TaskID,
				Content:   err.Error(),
				Timestamp: time.Now(),
			})
		}

		session.Emit(AgentEvent{
			Kind:      EventComplete,
			Agent:     a.name,
			SessionID: sessionID,
			TaskID:    opts.TaskID,
			Timestamp: time.Now(),
		})
		session.Close(finalStatus)
	}()

	return session, nil
}

// Send continues the conversation by appending a message and calling the API again.
func (a *HTTPAgent) Send(session *Session, message string) error {
	a.messages = append(a.messages, chatMessage{Role: "user", Content: message})
	// TODO: re-stream response into the existing session.
	return fmt.Errorf("%s: Send not yet fully implemented", a.name)
}

// Stop cancels the HTTP request via context.
func (a *HTTPAgent) Stop(session *Session) error {
	session.Cancel()
	return nil
}

// Events returns the event channel for the given session.
func (a *HTTPAgent) Events(session *Session) <-chan AgentEvent {
	return session.Events()
}

// streamOllama calls Ollama's native /api/chat endpoint with streaming.
func (a *HTTPAgent) streamOllama(ctx context.Context, session *Session, model, taskID string, msgs []chatMessage) error {
	body := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   true,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.endpoint+"/api/chat", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024)) // M-3 fix: cap error reads
		return fmt.Errorf("ollama %d: %s", resp.StatusCode, string(b))
	}

	// Ollama streams newline-delimited JSON objects.
	var fullResponse strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1*1024*1024)
	for scanner.Scan() {
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done          bool `json:"done"`
			TotalDuration int  `json:"total_duration"`
			EvalCount     int  `json:"eval_count"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
			continue
		}

		if chunk.Message.Content != "" {
			fullResponse.WriteString(chunk.Message.Content)
			session.Emit(AgentEvent{
				Kind:      EventProgress,
				Agent:     a.name,
				SessionID: session.ID,
				TaskID:    taskID,
				Content:   chunk.Message.Content,
				Timestamp: time.Now(),
			})
		}

		if chunk.Done {
			content := fullResponse.String()
			session.Emit(AgentEvent{
				Kind:      EventMessage,
				Agent:     a.name,
				SessionID: session.ID,
				TaskID:    taskID,
				Content:   content,
				Timestamp: time.Now(),
			})
			// B-2 fix: mutex-protected append for multi-turn.
			a.mu.Lock()
			a.messages = append(a.messages, chatMessage{Role: "assistant", Content: content})
			a.mu.Unlock()
		}
	}
	return scanner.Err()
}

// streamOpenAI calls an OpenAI-compatible /v1/chat/completions endpoint with SSE streaming.
func (a *HTTPAgent) streamOpenAI(ctx context.Context, session *Session, model, taskID string, msgs []chatMessage) error {
	body := map[string]any{
		"model":    model,
		"messages": msgs,
		"stream":   true,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", a.endpoint+"/v1/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("openai-compat request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024)) // M-3 fix
		return fmt.Errorf("openai-compat %d: %s", resp.StatusCode, string(b))
	}

	// SSE stream: lines starting with "data: ".
	var fullResponse strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1*1024*1024) // M-3 fix: explicit buffer
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}

		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			content := chunk.Choices[0].Delta.Content
			fullResponse.WriteString(content)
			session.Emit(AgentEvent{
				Kind:      EventProgress,
				Agent:     a.name,
				SessionID: session.ID,
				TaskID:    taskID,
				Content:   content,
				Timestamp: time.Now(),
			})
		}
	}

	if fullResponse.Len() > 0 {
		content := fullResponse.String()
		session.Emit(AgentEvent{
			Kind:      EventMessage,
			Agent:     a.name,
			SessionID: session.ID,
			TaskID:    taskID,
			Content:   content,
			Timestamp: time.Now(),
		})
		a.mu.Lock()
		a.messages = append(a.messages, chatMessage{Role: "assistant", Content: content})
		a.mu.Unlock()
	}

	return scanner.Err()
}

// isOllamaEndpoint heuristically detects Ollama by its default port.
func isOllamaEndpoint(endpoint string) bool {
	return strings.Contains(endpoint, ":11434")
}

// validateLocalhost ensures the endpoint URL resolves to a local address.
// SR-11: Prevents accidental exfiltration of prompts to external servers.
func validateLocalhost(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("invalid endpoint URL: %w", err)
	}

	host := u.Hostname()

	// Allow explicit localhost references.
	// H-2 fix: removed 0.0.0.0 — it's a bind address, not a valid client endpoint.
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return nil
	}

	// Resolve hostname and check if all addresses are local.
	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("cannot resolve %q: %w", host, err)
	}

	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("endpoint %q resolves to non-local address %s — only localhost is allowed in v1", endpoint, addr)
		}
	}

	return nil
}
