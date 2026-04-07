package llm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
)

// RoutingDeps holds dependencies for routing tools.
type RoutingDeps struct {
	Client   *OllamaClient
	Models   *ModelManager
	Registry *agent.Registry
	Bus      *bus.Bus
	WorkDir  string
}

// RegisterRoutingTools returns tool definitions and executors for
// cross-model and cross-agent delegation.
func RegisterRoutingTools(deps RoutingDeps) ([]ToolDef, map[string]ToolExecutor) {
	defs := []ToolDef{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "deep_reason",
				Description: "Delegate to Gemma 4 31B for deep reasoning, planning, evaluation, architecture decisions, or complex mathematical/logical analysis. Use when you need to think harder about a problem.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"question": {Type: "string", Description: "The question or problem to reason about"},
						"context":  {Type: "string", Description: "Additional context to include (optional)"},
					},
					Required: []string{"question"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "analyze_repo",
				Description: "Delegate to Llama 4 Scout (10M token context) for full-repository analysis, cross-file understanding, large codebase review, or architecture-level refactoring. Use when you need to see more code than fits in your context window.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"question": {Type: "string", Description: "What to analyze or review"},
						"files":    {Type: "string", Description: "Comma-separated file/directory paths to include (optional, defaults to working directory)"},
					},
					Required: []string{"question"},
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
		"deep_reason":  toolDeepReason(deps),
		"analyze_repo": toolAnalyzeRepo(deps),
		"web_research": toolWebResearch(deps),
		"code_task":    toolCodeTask(deps),
		"implement":    toolImplement(deps),
	}

	return defs, executors
}

// toolDeepReason calls Gemma 4 31B for planning/evaluation/reasoning.
func toolDeepReason(deps RoutingDeps) ToolExecutor {
	return func(args map[string]any) (string, error) {
		question, _ := args["question"].(string)
		if question == "" {
			return "", fmt.Errorf("question is required")
		}
		extra, _ := args["context"].(string)

		prompt := question
		if extra != "" {
			prompt = extra + "\n\n" + question
		}

		publishProgress(deps.Bus, "gemma4", "Loading Gemma 4 for deep reasoning...")

		if err := deps.Models.EnsureLoaded(context.Background(), ModelReasoner); err != nil {
			return "", fmt.Errorf("load reasoner: %w", err)
		}
		// No defer Unload — model stays loaded for follow-up calls.
		// Only evicted when the OTHER secondary model needs to load.

		publishProgress(deps.Bus, "gemma4", "Reasoning...")

		msg, resp, err := deps.Client.Chat(context.Background(), ChatRequest{
			Model: deps.Models.ModelName(ModelReasoner),
			Messages: []Message{
				{Role: RoleSystem, Content: "You are a deep reasoning specialist. Provide thorough, well-structured analysis. Consider trade-offs, edge cases, and alternatives."},
				{Role: RoleUser, Content: prompt},
			},
			Options: &ModelOptions{
				NumCtx: deps.Models.Config(ModelReasoner).ContextLen,
			},
		})
		if err != nil {
			return "", fmt.Errorf("deep_reason: %w", err)
		}

		publishComplete(deps.Bus, "gemma4", resp.EvalCount)
		return msg.Content, nil
	}
}

// toolAnalyzeRepo calls Llama 4 Scout for massive-context analysis.
func toolAnalyzeRepo(deps RoutingDeps) ToolExecutor {
	return func(args map[string]any) (string, error) {
		question, _ := args["question"].(string)
		if question == "" {
			return "", fmt.Errorf("question is required")
		}

		publishProgress(deps.Bus, "llama4-scout", "Loading Llama 4 Scout (10M context)...")

		if err := deps.Models.EnsureLoaded(context.Background(), ModelAnalyzer); err != nil {
			return "", fmt.Errorf("load analyzer: %w", err)
		}
		// No defer Unload — model stays loaded with repo context.
		// Only evicted when the OTHER secondary model needs to load.

		// Collect file contents if specified.
		var fileContent strings.Builder
		if files, _ := args["files"].(string); files != "" {
			for _, f := range strings.Split(files, ",") {
				f = strings.TrimSpace(f)
				path := resolvePath(deps.WorkDir, f)

				info, err := os.Stat(path)
				if err != nil {
					continue
				}

				if info.IsDir() {
					// Read all files in directory (up to reasonable limit).
					_ = filepath.Walk(path, func(p string, fi os.FileInfo, err error) error {
						if err != nil || fi.IsDir() || fi.Size() > maxFileSize {
							return nil
						}
						// Skip binary/hidden files.
						ext := filepath.Ext(p)
						if ext == "" || ext == ".exe" || ext == ".bin" || strings.HasPrefix(filepath.Base(p), ".") {
							return nil
						}
						data, err := os.ReadFile(p)
						if err != nil {
							return nil
						}
						rel, _ := filepath.Rel(deps.WorkDir, p)
						fileContent.WriteString(fmt.Sprintf("\n=== %s ===\n%s\n", rel, string(data)))
						return nil
					})
				} else {
					data, err := os.ReadFile(path)
					if err != nil {
						continue
					}
					rel, _ := filepath.Rel(deps.WorkDir, path)
					fileContent.WriteString(fmt.Sprintf("\n=== %s ===\n%s\n", rel, string(data)))
				}
			}
		}

		prompt := question
		if fileContent.Len() > 0 {
			prompt = question + "\n\nFiles for analysis:\n" + fileContent.String()
		}

		publishProgress(deps.Bus, "llama4-scout", "Analyzing...")

		msg, resp, err := deps.Client.Chat(context.Background(), ChatRequest{
			Model: deps.Models.ModelName(ModelAnalyzer),
			Messages: []Message{
				{Role: RoleSystem, Content: "You are a codebase analysis specialist with massive context capacity. Provide thorough, cross-cutting analysis across files and components."},
				{Role: RoleUser, Content: prompt},
			},
			Options: &ModelOptions{
				NumCtx: deps.Models.Config(ModelAnalyzer).ContextLen,
			},
		})
		if err != nil {
			return "", fmt.Errorf("analyze_repo: %w", err)
		}

		publishComplete(deps.Bus, "llama4-scout", resp.EvalCount)
		return msg.Content, nil
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
