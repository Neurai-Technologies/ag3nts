package llm

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
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

// MessageCallback is invoked when the agent loop produces an aggregated
// assistant message (either narration before tools or the final response).
// Used by external systems (e.g. m3m0ry rolling context) to capture the
// full message content, since the streamed EventProgress chunks are not
// individually suitable for persistence.
type MessageCallback func(role, content string)

// AgentLoop manages the iterative send → stream → tool → resend cycle.
type AgentLoop struct {
	client       *OllamaClient
	conversation *ConversationManager
	models       *ModelManager
	bus          *bus.Bus
	tools        map[string]ToolExecutor
	toolDefs     []ToolDef
	headModel     string // full model name for API (e.g. "gemma4:31b")
	headDisplay   string // short name for display (e.g. "gemma4")
	memory        *Memory
	askPermission PermissionFunc
	onMessage     MessageCallback // optional: fired on aggregated messages
}

// NewAgentLoop creates the agent loop.
func NewAgentLoop(
	client *OllamaClient,
	conversation *ConversationManager,
	models *ModelManager,
	eventBus *bus.Bus,
	headModel string,
	memory *Memory,
) *AgentLoop {
	// Strip tag for display name: "gemma4:31b" → "gemma4"
	display := headModel
	if idx := strings.Index(headModel, ":"); idx > 0 {
		display = headModel[:idx]
	}

	return &AgentLoop{
		client:       client,
		conversation: conversation,
		models:       models,
		bus:          eventBus,
		tools:        make(map[string]ToolExecutor),
		headModel:    headModel,
		headDisplay:  display,
		memory:       memory,
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

		// Build and send request. KeepAlive=-1 pins the model in VRAM
		// indefinitely — every request must carry it, otherwise Ollama
		// resets to its default (5 min) and unloads during idle gaps.
		msg, resp, err := al.client.StreamChat(ctx, ChatRequest{
			Model:    al.headModel,
			Messages: al.conversation.Messages(),
			Tools:    al.toolDefs,
			Options: &ModelOptions{
				NumCtx: al.models.Config(ModelHead).ContextLen,
			},
			KeepAlive: -1,
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

		// Fire message callback with the aggregated content for external
		// capture (m3m0ry). This fires for both final responses and
		// intermediate narration before tool execution.
		if al.onMessage != nil && msg.Content != "" {
			al.onMessage("assistant", msg.Content)
		}

		// No tool calls — final response. Emit completion and return.
		if len(msg.ToolCalls) == 0 {
			al.emitComplete(resp)
			return nil
		}

		// Flush any streamed text so the user sees intermediate narration
		// (e.g. "I will now search for...") before tool execution starts.
		if msg.Content != "" {
			al.emitFlush()
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

			// Permission check for file-modifying and system tools.
			if al.askPermission != nil {
				var needsPermission bool
				var permAction string
				switch toolName {
				case "write_file":
					permAction, _ = tc.Function.Arguments["path"].(string)
					needsPermission = true
				case "run_command":
					permAction, _ = tc.Function.Arguments["command"].(string)
					needsPermission = true
				}
				if needsPermission && !al.askPermission(toolName, permAction) {
					al.conversation.Append(Message{Role: RoleTool, Content: "Permission denied by user."})
					continue
				}
			}

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

			// Show brief tool result summary in TUI.
			resultPreview := result
			if len(resultPreview) > 200 {
				resultPreview = resultPreview[:197] + "..."
			}
			al.emitToolResult(toolName, resultPreview)

			// Show git diff after file-modifying tools.
			if toolName == "implement" || toolName == "code_task" || toolName == "write_file" {
				al.emitDiff()
			}

			// Auto-store routing tool results in memory (already distilled).
			// System tool results are raw — they'll be stored when the head
			// model synthesizes findings via the explicit store() tool.
			if al.memory != nil && err == nil {
				switch toolName {
				case "web_research":
					al.memory.Store("gemini", "finding", result)
				case "code_task":
					al.memory.Store("claude", "finding", result)
				case "implement":
					al.memory.Store("codex", "finding", result)
				}
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
	al.bus.Publish("system", al.headDisplay, agent.AgentEvent{
		Kind:      agent.EventProgress,
		Agent:     al.headDisplay,
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
	al.bus.Publish("system", al.headDisplay, agent.AgentEvent{
		Kind:      agent.EventToolUse,
		Agent:     al.headDisplay,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// emitComplete publishes a completion event with usage stats.
func (al *AgentLoop) emitComplete(resp ChatResponse) {
	al.bus.Publish("system", al.headDisplay, agent.AgentEvent{
		Kind:  agent.EventComplete,
		Agent: al.headDisplay,
		Usage: &agent.TokenUsage{
			InputTokens:  resp.PromptEvalCount,
			OutputTokens: resp.EvalCount,
		},
		Timestamp: time.Now(),
	})
}

// emitError publishes an error event.
func (al *AgentLoop) emitError(content string) {
	al.bus.Publish("system", al.headDisplay, agent.AgentEvent{
		Kind:      agent.EventError,
		Agent:     al.headDisplay,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// emitToolResult publishes a brief summary of a tool's output.
func (al *AgentLoop) emitToolResult(toolName, preview string) {
	al.bus.Publish("system", al.headDisplay, agent.AgentEvent{
		Kind:      agent.EventProgress,
		Agent:     al.headDisplay + "[result]",
		Content:   toolName + ": " + preview,
		Timestamp: time.Now(),
	})
}

// emitFlush triggers a flush of the streamed text in the TUI.
// Uses EventMessage which the TUI renders immediately (unlike EventProgress which buffers).
func (al *AgentLoop) emitFlush() {
	al.bus.Publish("system", al.headDisplay, agent.AgentEvent{
		Kind:      agent.EventMessage,
		Agent:     al.headDisplay,
		Content:   "", // empty content — the TUI will flush the buffer and render
		Timestamp: time.Now(),
	})
	// Small delay to let the TUI process the flush.
	time.Sleep(50 * time.Millisecond)
}

// emitDiff runs git diff and publishes formatted per-file diffs.
func (al *AgentLoop) emitDiff() {
	diffCmd := exec.Command("git", "diff")
	diffOut, err := diffCmd.Output()
	if err != nil || len(diffOut) == 0 {
		return
	}

	al.bus.Publish("system", al.headDisplay, agent.AgentEvent{
		Kind:      agent.EventProgress,
		Agent:     "ag3nts[diff]",
		Content:   string(diffOut),
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
