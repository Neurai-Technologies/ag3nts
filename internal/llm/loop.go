package llm

import (
	"context"
	"fmt"
	"time"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
)

const (
	// maxIterations prevents infinite tool-calling loops.
	maxIterations = 25
	// maxToolRepeat warns if the same tool is called too many times.
	maxToolRepeat = 3
)

// AgentLoop manages the iterative send → stream → tool → resend cycle.
type AgentLoop struct {
	client       *OllamaClient
	conversation *ConversationManager
	models       *ModelManager
	bus          *bus.Bus
	tools        map[string]ToolExecutor
	toolDefs     []ToolDef
	headModel    string
}

// NewAgentLoop creates the agent loop.
func NewAgentLoop(
	client *OllamaClient,
	conversation *ConversationManager,
	models *ModelManager,
	eventBus *bus.Bus,
	headModel string,
) *AgentLoop {
	return &AgentLoop{
		client:       client,
		conversation: conversation,
		models:       models,
		bus:          eventBus,
		tools:        make(map[string]ToolExecutor),
		headModel:    headModel,
	}
}

// RegisterTools adds tool definitions and their executors.
func (al *AgentLoop) RegisterTools(defs []ToolDef, executors map[string]ToolExecutor) {
	al.toolDefs = append(al.toolDefs, defs...)
	for name, exec := range executors {
		al.tools[name] = exec
	}
}

// Run executes the full agent loop for a user message.
// Streams tokens to the bus as they arrive. Executes tool calls
// and re-sends until the model produces a final response.
func (al *AgentLoop) Run(ctx context.Context, userMessage string) error {
	// Append user message.
	al.conversation.Append(Message{
		Role:    RoleUser,
		Content: userMessage,
	})

	// Check if summarization is needed.
	if al.conversation.NeedsSummarization() {
		al.emitSystem("Compressing conversation history...")
		if err := al.conversation.Summarize(ctx); err != nil {
			al.emitSystem(fmt.Sprintf("Summarization failed: %v (continuing)", err))
		}
	}

	toolCounts := make(map[string]int)

	for iteration := 0; iteration < maxIterations; iteration++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Build and send request.
		msg, resp, err := al.client.StreamChat(ctx, ChatRequest{
			Model:    al.headModel,
			Messages: al.conversation.Messages(),
			Tools:    al.toolDefs,
			Options: &ModelOptions{
				NumCtx: al.models.Config(ModelHead).ContextLen,
			},
		}, func(chunk ChatResponse) {
			// Stream text tokens to TUI as they arrive.
			if chunk.Message.Content != "" {
				al.emitProgress(chunk.Message.Content)
			}
		})
		if err != nil {
			al.emitError(fmt.Sprintf("Ollama error: %v", err))
			return err
		}

		// Append assistant response to conversation.
		al.conversation.Append(msg)

		// No tool calls — final response. Emit completion and return.
		if len(msg.ToolCalls) == 0 {
			al.emitComplete(resp)
			return nil
		}

		// Process tool calls.
		for _, tc := range msg.ToolCalls {
			toolName := tc.Function.Name

			// Track call counts and warn on repetition.
			toolCounts[toolName]++
			if toolCounts[toolName] > maxToolRepeat {
				al.conversation.Append(Message{
					Role:    RoleSystem,
					Content: fmt.Sprintf("You have called %s %d times. Synthesize what you have and respond to the user.", toolName, toolCounts[toolName]),
				})
			}

			// Emit tool use for TUI visibility.
			al.emitToolUse(tc)

			// Execute the tool.
			executor, ok := al.tools[toolName]
			if !ok {
				result := fmt.Sprintf("Unknown tool: %s", toolName)
				al.conversation.Append(Message{Role: RoleTool, Content: result})
				continue
			}

			result, err := executor(tc.Function.Arguments)
			if err != nil {
				result = fmt.Sprintf("Tool error: %v", err)
			}

			// Truncate large results.
			if len(result) > maxOutputSize {
				result = result[:maxOutputSize] + "\n[TRUNCATED]"
			}

			// Append tool result to conversation.
			al.conversation.Append(Message{
				Role:    RoleTool,
				Content: result,
			})
		}
	}

	al.emitError("Max iterations reached. Stopping.")
	return fmt.Errorf("max iterations (%d) reached", maxIterations)
}

// emitProgress publishes streaming text to the bus.
func (al *AgentLoop) emitProgress(content string) {
	al.bus.Publish("system", "qwen3.5", agent.AgentEvent{
		Kind:      agent.EventProgress,
		Agent:     "qwen3.5",
		Content:   content,
		Timestamp: time.Now(),
	})
}

// emitToolUse publishes a tool call event to the bus.
func (al *AgentLoop) emitToolUse(tc ToolCall) {
	content := tc.Function.Name
	// Format key arguments for display.
	if args := formatToolArgs(tc); args != "" {
		content += ": " + args
	}
	al.bus.Publish("system", "qwen3.5", agent.AgentEvent{
		Kind:      agent.EventToolUse,
		Agent:     "qwen3.5",
		Content:   content,
		Timestamp: time.Now(),
	})
}

// emitComplete publishes a completion event with usage stats.
func (al *AgentLoop) emitComplete(resp ChatResponse) {
	al.bus.Publish("system", "qwen3.5", agent.AgentEvent{
		Kind:  agent.EventComplete,
		Agent: "qwen3.5",
		Usage: &agent.TokenUsage{
			InputTokens:  resp.PromptEvalCount,
			OutputTokens: resp.EvalCount,
		},
		Timestamp: time.Now(),
	})
}

// emitError publishes an error event.
func (al *AgentLoop) emitError(content string) {
	al.bus.Publish("system", "qwen3.5", agent.AgentEvent{
		Kind:      agent.EventError,
		Agent:     "qwen3.5",
		Content:   content,
		Timestamp: time.Now(),
	})
}

// emitSystem publishes a system info event.
func (al *AgentLoop) emitSystem(content string) {
	al.bus.Publish("system", "system", agent.AgentEvent{
		Kind:      agent.EventInit,
		Agent:     "system",
		Content:   content,
		Timestamp: time.Now(),
	})
}

// formatToolArgs extracts the most relevant argument for display.
func formatToolArgs(tc ToolCall) string {
	args := tc.Function.Arguments
	// Try common parameter names in priority order.
	for _, key := range []string{"command", "query", "path", "question", "task", "pattern"} {
		if v, ok := args[key].(string); ok && v != "" {
			if len(v) > 120 {
				return v[:117] + "..."
			}
			return v
		}
	}
	return ""
}
