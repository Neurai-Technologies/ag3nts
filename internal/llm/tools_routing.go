package llm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
)

// RoutingDeps holds dependencies for routing tools.
type RoutingDeps struct {
	Client       *OllamaClient
	Models       *ModelManager
	Registry     *agent.Registry
	Bus          *bus.Bus
	Conversation *ConversationManager
	Memory       *Memory
	WorkDir      string
}

// RegisterRoutingTools returns tool definitions and executors for
// cross-model and cross-agent delegation.
func RegisterRoutingTools(deps RoutingDeps) ([]ToolDef, map[string]ToolExecutor) {
	defs := []ToolDef{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "recall",
				Description: "Retrieve relevant context from long-term memory. Use when you need information from earlier in the conversation, previous tool results, or past decisions. Memory persists across sessions and stores distilled findings, not raw data.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"query": {Type: "string", Description: "What to recall — describe what you're looking for"},
					},
					Required: []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "store",
				Description: "Store an important finding, decision, or summary in long-term memory. Use after completing analysis, making decisions, or learning something that should be remembered for later. Store the distilled insight, not raw data.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"category": {Type: "string", Description: "Type of memory: finding, decision, file_summary, error, user_context", Enum: []string{"finding", "decision", "file_summary", "error", "user_context"}},
						"content":  {Type: "string", Description: "The distilled finding or summary to store"},
					},
					Required: []string{"category", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "web_research",
				Description: "Delegate to Gemini CLI for web search and current information. Use for any question about current events, documentation, APIs, or information not in your training data.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"query": {Type: "string", Description: "The search query or research question"},
					},
					Required: []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "code_task",
				Description: "Delegate to Claude Code for complex coding tasks requiring deep code understanding, multi-file edits, or sophisticated reasoning. Use for substantial implementation work.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"task":    {Type: "string", Description: "Description of the coding task"},
						"context": {Type: "string", Description: "Additional context (optional)"},
					},
					Required: []string{"task"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "implement",
				Description: "Delegate to Codex CLI for focused implementation tasks. Use for quick code generation, single-file changes, or straightforward implementation.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"task": {Type: "string", Description: "Description of what to implement"},
					},
					Required: []string{"task"},
				},
			},
		},
	}

	executors := map[string]ToolExecutor{
		"recall":       toolRecall(deps),
		"store":        toolStore(deps),
		"web_research": toolWebResearch(deps),
		"code_task":    toolCodeTask(deps),
		"implement":    toolImplement(deps),
	}

	return defs, executors
}

// toolRecall retrieves relevant context from long-term memory.
func toolRecall(deps RoutingDeps) ToolExecutor {
	return func(args map[string]any) (string, error) {
		query, _ := args["query"].(string)
		if query == "" {
			return "", fmt.Errorf("query is required")
		}

		if deps.Memory == nil {
			return "Memory not available.", nil
		}

		return deps.Memory.Recall(query), nil
	}
}

// toolStore saves a distilled finding to Scout's long-term memory.
func toolStore(deps RoutingDeps) ToolExecutor {
	return func(args map[string]any) (string, error) {
		category, _ := args["category"].(string)
		content, _ := args["content"].(string)
		if category == "" || content == "" {
			return "", fmt.Errorf("category and content are required")
		}

		if deps.Memory == nil {
			return "Memory not available.", nil
		}

		deps.Memory.Store("qwen3.5", category, content)

		return fmt.Sprintf("Stored %s (%d chars). Memory now has %d entries.", category, len(content), deps.Memory.Len()), nil
	}
}

// toolWebResearch calls Gemini CLI subprocess for web search.
func toolWebResearch(deps RoutingDeps) ToolExecutor {
	return func(args map[string]any) (string, error) {
		query, _ := args["query"].(string)
		if query == "" {
			return "", fmt.Errorf("query is required")
		}

		gemini := deps.Registry.Get("gemini")
		if gemini == nil {
			return "", fmt.Errorf("gemini agent not available")
		}

		publishProgress(deps.Bus, "gemini", "Researching: "+query)

		sess, err := gemini.Start(context.Background(), query, &agent.StartOpts{
			TaskID: fmt.Sprintf("_research-%d", time.Now().UnixNano()),
		})
		if err != nil {
			return "", fmt.Errorf("start gemini: %w", err)
		}

		// Drain events: publish tool/init/complete for TUI visibility,
		// capture message content silently.
		var research strings.Builder
		for event := range sess.Events() {
			switch event.Kind {
			case agent.EventMessage, agent.EventProgress:
				research.WriteString(event.Content)
			case agent.EventToolUse, agent.EventInit:
				publishEvent(deps.Bus, event)
			case agent.EventComplete:
				publishEvent(deps.Bus, event)
			case agent.EventError:
				publishEvent(deps.Bus, event)
			}
		}

		result := research.String()
		if result == "" {
			return "No results found for: " + query, nil
		}
		return result, nil
	}
}

// toolCodeTask calls Claude Code subprocess for complex coding.
func toolCodeTask(deps RoutingDeps) ToolExecutor {
	return func(args map[string]any) (string, error) {
		task, _ := args["task"].(string)
		if task == "" {
			return "", fmt.Errorf("task is required")
		}
		extra, _ := args["context"].(string)

		prompt := task
		if extra != "" {
			prompt = extra + "\n\n" + task
		}

		claude := deps.Registry.Get("claude")
		if claude == nil {
			return "", fmt.Errorf("claude agent not available")
		}

		publishProgress(deps.Bus, "claude", "Working on: "+task)

		sess, err := claude.Start(context.Background(), prompt, &agent.StartOpts{
			TaskID: fmt.Sprintf("_code-%d", time.Now().UnixNano()),
		})
		if err != nil {
			return "", fmt.Errorf("start claude: %w", err)
		}

		var output strings.Builder
		for event := range sess.Events() {
			switch event.Kind {
			case agent.EventMessage, agent.EventProgress:
				output.WriteString(event.Content)
			case agent.EventToolUse, agent.EventInit, agent.EventComplete, agent.EventError:
				publishEvent(deps.Bus, event)
			}
		}

		return output.String(), nil
	}
}

// toolImplement calls Codex CLI subprocess for implementation.
func toolImplement(deps RoutingDeps) ToolExecutor {
	return func(args map[string]any) (string, error) {
		task, _ := args["task"].(string)
		if task == "" {
			return "", fmt.Errorf("task is required")
		}

		codex := deps.Registry.Get("codex")
		if codex == nil {
			return "", fmt.Errorf("codex agent not available")
		}

		publishProgress(deps.Bus, "codex", "Implementing: "+task)

		sess, err := codex.Start(context.Background(), task, &agent.StartOpts{
			TaskID: fmt.Sprintf("_impl-%d", time.Now().UnixNano()),
		})
		if err != nil {
			return "", fmt.Errorf("start codex: %w", err)
		}

		var output strings.Builder
		for event := range sess.Events() {
			switch event.Kind {
			case agent.EventMessage, agent.EventProgress:
				output.WriteString(event.Content)
			case agent.EventToolUse, agent.EventInit, agent.EventComplete, agent.EventError:
				publishEvent(deps.Bus, event)
			}
		}

		return output.String(), nil
	}
}

// publishProgress emits a progress event to the bus for TUI display.
func publishProgress(b *bus.Bus, agentName, content string) {
	b.Publish("system", agentName, agent.AgentEvent{
		Kind:      agent.EventProgress,
		Agent:     agentName,
		Content:   content,
		Timestamp: time.Now(),
	})
}

// publishComplete emits a completion event to the bus.
func publishComplete(b *bus.Bus, agentName string, tokens int) {
	b.Publish("system", agentName, agent.AgentEvent{
		Kind:  agent.EventComplete,
		Agent: agentName,
		Usage: &agent.TokenUsage{
			OutputTokens: tokens,
		},
		Timestamp: time.Now(),
	})
}

// publishEvent emits an agent event to the bus.
func publishEvent(b *bus.Bus, event agent.AgentEvent) {
	b.Publish("system", event.Agent, event)
}

