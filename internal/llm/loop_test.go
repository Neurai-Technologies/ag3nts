package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/rohanrgit/ag3nts/internal/bus"
)

// mockOllamaClient captures chat requests and serves scripted stream responses.
type mockOllamaClient struct {
	t         *testing.T
	mu        sync.Mutex
	requests  []ChatRequest
	responder func(call int, req ChatRequest) []ChatResponse
}

func newMockOllamaClient(t *testing.T, responder func(call int, req ChatRequest) []ChatResponse) (*OllamaClient, *mockOllamaClient) {
	t.Helper()

	mock := &mockOllamaClient{
		t:         t,
		responder: responder,
	}

	client := &OllamaClient{
		endpoint: "http://mock-ollama",
		client: &http.Client{
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/api/chat" {
					return &http.Response{
						StatusCode: http.StatusNotFound,
						Body:       io.NopCloser(strings.NewReader("not found")),
						Header:     make(http.Header),
						Request:    r,
					}, nil
				}

				var req ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					mock.t.Errorf("decode request: %v", err)
					return &http.Response{
						StatusCode: http.StatusBadRequest,
						Body:       io.NopCloser(strings.NewReader("bad request")),
						Header:     make(http.Header),
						Request:    r,
					}, nil
				}

				mock.mu.Lock()
				mock.requests = append(mock.requests, req)
				call := len(mock.requests)
				mock.mu.Unlock()

				var payload strings.Builder
				for _, chunk := range mock.responder(call, req) {
					data, err := json.Marshal(chunk)
					if err != nil {
						mock.t.Errorf("marshal chunk: %v", err)
						continue
					}
					payload.Write(data)
					payload.WriteByte('\n')
				}

				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(payload.String())),
					Header:     make(http.Header),
					Request:    r,
				}, nil
			}),
		},
	}

	return client, mock
}

func (m *mockOllamaClient) Close() {
	// no-op: in-memory transport only
}

func (m *mockOllamaClient) RequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

func (m *mockOllamaClient) Requests() []ChatRequest {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]ChatRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

// mockConversationManager creates a deterministic in-memory conversation for tests.
type mockConversationManager struct {
	instanceRef *ConversationManager
}

func newMockConversationManager(systemPrompt string) *mockConversationManager {
	return &mockConversationManager{
		instanceRef: NewConversationManager(systemPrompt, 1_000_000),
	}
}

func (m *mockConversationManager) Instance() *ConversationManager {
	return m.instanceRef
}

// mockModelManager provides a fixed head model config for loop tests.
type mockModelManager struct {
	instanceRef *ModelManager
}

func newMockModelManager(headModel string) *mockModelManager {
	return &mockModelManager{
		instanceRef: NewModelManager(nil, map[ModelRole]*ModelConfig{
			ModelHead: {
				Name:       headModel,
				ContextLen: 4096,
			},
		}),
	}
}

func (m *mockModelManager) Instance() *ModelManager {
	return m.instanceRef
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestAgentLoop_GracefullyHandlesToolErrorsAndFeedsBackToLLM(t *testing.T) {
	ollama, ollamaMock := newMockOllamaClient(t, func(call int, _ ChatRequest) []ChatResponse {
		switch call {
		case 1:
			return []ChatResponse{
				{
					Model: "head:test",
					Message: Message{
						Role: RoleAssistant,
						ToolCalls: []ToolCall{
							{
								Function: ToolCallFunction{
									Name:      "failing_tool",
									Arguments: map[string]any{"query": "example"},
								},
							},
						},
					},
				},
				{Model: "head:test", Done: true, PromptEvalCount: 12, EvalCount: 1},
			}
		case 2:
			return []ChatResponse{
				{
					Model: "head:test",
					Message: Message{
						Role:    RoleAssistant,
						Content: "Recovered after tool error.",
					},
				},
				{Model: "head:test", Done: true, PromptEvalCount: 24, EvalCount: 8},
			}
		default:
			return []ChatResponse{{Model: "head:test", Done: true}}
		}
	})
	defer ollamaMock.Close()

	conversationMock := newMockConversationManager("You are a test assistant.")
	modelsMock := newMockModelManager("head:test")

	loop := NewAgentLoop(
		ollama,
		conversationMock.Instance(),
		modelsMock.Instance(),
		bus.New(),
		"head:test",
		nil,
	)

	toolCalls := 0
	loop.RegisterTools(
		[]ToolDef{
			{
				Type: "function",
				Function: ToolFunction{
					Name:        "failing_tool",
					Description: "Always fails in tests.",
					Parameters: ToolFunctionParams{
						Type:       "object",
						Properties: map[string]ToolParamProp{"query": {Type: "string", Description: "query"}},
						Required:   []string{"query"},
					},
				},
			},
		},
		map[string]ToolExecutor{
			"failing_tool": func(_ map[string]any) (string, error) {
				toolCalls++
				return "", errors.New("boom")
			},
		},
	)

	if err := loop.Run(context.Background(), "please run the failing tool"); err != nil {
		t.Fatalf("Run returned unexpected error: %v", err)
	}

	if toolCalls != 1 {
		t.Fatalf("tool executor called %d times, want 1", toolCalls)
	}

	requests := ollamaMock.Requests()
	if len(requests) != 2 {
		t.Fatalf("StreamChat called %d times, want 2", len(requests))
	}

	secondReq := requests[1]
	foundToolError := false
	for _, msg := range secondReq.Messages {
		if msg.Role == RoleTool && strings.Contains(msg.Content, "Tool error: boom") {
			foundToolError = true
			break
		}
	}
	if !foundToolError {
		t.Fatalf("second LLM request missing tool error feedback; messages=%+v", secondReq.Messages)
	}
}

func TestAgentLoop_MaxIterationsReached(t *testing.T) {
	ollama, ollamaMock := newMockOllamaClient(t, func(call int, _ ChatRequest) []ChatResponse {
		return []ChatResponse{
			{
				Model: "head:test",
				Message: Message{
					Role: RoleAssistant,
					ToolCalls: []ToolCall{
						{
							Function: ToolCallFunction{
								Name:      "loop_tool",
								Arguments: map[string]any{"call": call},
							},
						},
					},
				},
			},
			{Model: "head:test", Done: true, PromptEvalCount: 10, EvalCount: 1},
		}
	})
	defer ollamaMock.Close()

	conversationMock := newMockConversationManager("You are a test assistant.")
	modelsMock := newMockModelManager("head:test")

	loop := NewAgentLoop(
		ollama,
		conversationMock.Instance(),
		modelsMock.Instance(),
		bus.New(),
		"head:test",
		nil,
	)

	toolCalls := 0
	loop.RegisterTools(
		[]ToolDef{
			{
				Type: "function",
				Function: ToolFunction{
					Name:        "loop_tool",
					Description: "Always returns success in tests.",
					Parameters: ToolFunctionParams{
						Type:       "object",
						Properties: map[string]ToolParamProp{"call": {Type: "integer", Description: "call count"}},
						Required:   []string{"call"},
					},
				},
			},
		},
		map[string]ToolExecutor{
			"loop_tool": func(_ map[string]any) (string, error) {
				toolCalls++
				return "ok", nil
			},
		},
	)

	err := loop.Run(context.Background(), "keep calling tools")
	if err == nil {
		t.Fatal("Run returned nil error, want max iterations error")
	}

	wantErr := fmt.Sprintf("max iterations (%d) reached", maxIterations)
	if err.Error() != wantErr {
		t.Fatalf("Run error = %q, want %q", err.Error(), wantErr)
	}

	if ollamaMock.RequestCount() != maxIterations {
		t.Fatalf("StreamChat called %d times, want %d", ollamaMock.RequestCount(), maxIterations)
	}

	if toolCalls != maxIterations {
		t.Fatalf("tool executor called %d times, want %d", toolCalls, maxIterations)
	}
}

// TestAgentLoop_MessageCallbackFiresOnFinalResponse verifies that onMessage
// is invoked with the aggregated assistant content when the loop produces
// a final response. This is the mechanism used by m3m0ry to capture local
// LLM responses, since streamed EventProgress chunks are filtered by the
// recorder.
func TestAgentLoop_MessageCallbackFiresOnFinalResponse(t *testing.T) {
	ollama, ollamaMock := newMockOllamaClient(t, func(call int, _ ChatRequest) []ChatResponse {
		return []ChatResponse{
			{
				Model: "head:test",
				Message: Message{
					Role:    RoleAssistant,
					Content: "Hello! How can I help you today?",
				},
			},
			{Model: "head:test", Done: true, PromptEvalCount: 5, EvalCount: 9},
		}
	})
	defer ollamaMock.Close()

	conversationMock := newMockConversationManager("You are a test assistant.")
	modelsMock := newMockModelManager("head:test")

	loop := NewAgentLoop(
		ollama,
		conversationMock.Instance(),
		modelsMock.Instance(),
		bus.New(),
		"head:test",
		nil,
	)

	type captured struct {
		role, content string
	}
	var calls []captured
	loop.onMessage = func(role, content string) {
		calls = append(calls, captured{role, content})
	}

	if err := loop.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Expect two callbacks: user prompt then assistant response.
	if len(calls) != 2 {
		t.Fatalf("callback called %d times, want 2 (user + assistant)", len(calls))
	}
	if calls[0].role != "user" || calls[0].content != "hi" {
		t.Errorf("calls[0] = %+v, want {user hi}", calls[0])
	}
	if calls[1].role != "assistant" || calls[1].content != "Hello! How can I help you today?" {
		t.Errorf("calls[1] = %+v, want {assistant Hello!...}", calls[1])
	}
}

// TestAgentLoop_MessageCallbackFiresOnIntermediateNarration verifies that
// onMessage also fires for intermediate narration before tool calls, not
// just the final response.
func TestAgentLoop_MessageCallbackFiresOnIntermediateNarration(t *testing.T) {
	ollama, ollamaMock := newMockOllamaClient(t, func(call int, _ ChatRequest) []ChatResponse {
		switch call {
		case 1:
			return []ChatResponse{
				{
					Model: "head:test",
					Message: Message{
						Role:    RoleAssistant,
						Content: "I will search for that.",
						ToolCalls: []ToolCall{
							{
								Function: ToolCallFunction{
									Name:      "lookup",
									Arguments: map[string]any{"q": "thing"},
								},
							},
						},
					},
				},
				{Model: "head:test", Done: true, PromptEvalCount: 10, EvalCount: 6},
			}
		case 2:
			return []ChatResponse{
				{
					Model: "head:test",
					Message: Message{
						Role:    RoleAssistant,
						Content: "Based on the lookup, the answer is 42.",
					},
				},
				{Model: "head:test", Done: true, PromptEvalCount: 20, EvalCount: 10},
			}
		default:
			return []ChatResponse{{Model: "head:test", Done: true}}
		}
	})
	defer ollamaMock.Close()

	conversationMock := newMockConversationManager("You are a test assistant.")
	modelsMock := newMockModelManager("head:test")

	loop := NewAgentLoop(
		ollama,
		conversationMock.Instance(),
		modelsMock.Instance(),
		bus.New(),
		"head:test",
		nil,
	)

	loop.RegisterTools(
		[]ToolDef{
			{
				Type: "function",
				Function: ToolFunction{
					Name:        "lookup",
					Description: "A lookup tool.",
					Parameters: ToolFunctionParams{
						Type: "object",
						Properties: map[string]ToolParamProp{
							"q": {Type: "string", Description: "query"},
						},
						Required: []string{"q"},
					},
				},
			},
		},
		map[string]ToolExecutor{
			"lookup": func(_ map[string]any) (string, error) {
				return "result", nil
			},
		},
	)

	// Collect only assistant-role callbacks — the user-prompt callback
	// always fires once and is out of scope for this test.
	var assistantMessages []string
	loop.onMessage = func(role, content string) {
		if role == "assistant" {
			assistantMessages = append(assistantMessages, content)
		}
	}

	if err := loop.Run(context.Background(), "look up that thing"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(assistantMessages) != 2 {
		t.Fatalf("assistant callback called %d times, want 2 (narration + final)", len(assistantMessages))
	}
	if assistantMessages[0] != "I will search for that." {
		t.Errorf("assistantMessages[0] = %q, want narration", assistantMessages[0])
	}
	if assistantMessages[1] != "Based on the lookup, the answer is 42." {
		t.Errorf("assistantMessages[1] = %q, want final response", assistantMessages[1])
	}
}

// TestAgentLoop_MessageCallbackNotFiredForEmptyContent verifies the callback
// is only invoked when there's actual content to capture.
func TestAgentLoop_MessageCallbackNotFiredForEmptyContent(t *testing.T) {
	ollama, ollamaMock := newMockOllamaClient(t, func(call int, _ ChatRequest) []ChatResponse {
		return []ChatResponse{
			{
				Model: "head:test",
				Message: Message{
					Role:    RoleAssistant,
					Content: "", // empty content
				},
			},
			{Model: "head:test", Done: true, PromptEvalCount: 5, EvalCount: 1},
		}
	})
	defer ollamaMock.Close()

	conversationMock := newMockConversationManager("You are a test assistant.")
	modelsMock := newMockModelManager("head:test")

	loop := NewAgentLoop(
		ollama,
		conversationMock.Instance(),
		modelsMock.Instance(),
		bus.New(),
		"head:test",
		nil,
	)

	// Count only assistant-role callbacks. The user-prompt callback
	// always fires; this test checks the empty-assistant-content path.
	assistantCount := 0
	loop.onMessage = func(role, content string) {
		if role == "assistant" {
			assistantCount++
		}
	}

	if err := loop.Run(context.Background(), "test"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if assistantCount != 0 {
		t.Errorf("assistant callback called %d times for empty content, want 0", assistantCount)
	}
}
