package llm

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
)

// OrchestratorConfig holds configuration for the local LLM orchestrator.
type OrchestratorConfig struct {
	Endpoint      string // Ollama endpoint (default: http://localhost:11434)
	HeadModel     string // Qwen 3.5 122B model name in Ollama
	ReasonerModel string // Gemma 4 31B model name
	ModelsPath    string // Path to Ollama models directory (for OLLAMA_MODELS env)
	SystemPrompt  string // System prompt for head model (optional override)
	WorkDir       string // Working directory for file operations
	MaxContext    int    // Context window limit in tokens (default: 256000)
}

const defaultSystemPrompt = `You are the head orchestrator of ag3nts, a multi-agent AI system running locally. You manage conversations with the user and dispatch work to specialized agents via tool calls.

You are highly capable — handle most tasks directly. Only delegate when genuinely needed:

Tools available:
- read_file, write_file, run_command, search_files: Direct filesystem and shell access.
- deep_reason: Delegate to Gemma 4 31B for complex reasoning, evaluation, architecture decisions.
- recall: Retrieve relevant context from long-term memory. Use when you need information from earlier in the conversation or past findings. Memory persists across sessions.
- store: Save an important finding, decision, or summary to long-term memory. Use after completing analysis or making decisions. Store the distilled insight, not raw data.
- web_research: Delegate to Gemini CLI for current information from the internet.
- code_task: Delegate to Claude Code for complex multi-file coding tasks.
- implement: Delegate to Codex CLI for focused implementation tasks.

Guidelines:
- For simple questions, answer directly without tools.
- Always explain briefly what you're doing before calling a tool.
- After receiving tool results, synthesize and present them clearly.
- After completing significant analysis, use store() to save key findings to long-term memory.
- Use recall() when you need context from earlier in the session or past decisions.
- Be concise and direct. The user is a developer — no hand-holding.`

// LocalOrchestrator wraps the agent loop, conversation, and model management
// into a single entry point for the TUI.
type LocalOrchestrator struct {
	client       *OllamaClient
	models       *ModelManager
	conversation *ConversationManager
	memory       *Memory
	loop         *AgentLoop
	bus          *bus.Bus
	workDir      string

	mu      sync.Mutex
	cancel  context.CancelFunc // cancels the current Run
	running bool
}

// NewLocalOrchestrator creates and initializes the local LLM orchestrator.
func NewLocalOrchestrator(
	cfg OrchestratorConfig,
	registry *agent.Registry,
	eventBus *bus.Bus,
) (*LocalOrchestrator, error) {
	// Defaults.
	if cfg.Endpoint == "" {
		cfg.Endpoint = "http://localhost:11434"
	}
	if cfg.MaxContext <= 0 {
		cfg.MaxContext = 256000
	}
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = defaultSystemPrompt
	}

	// Create Ollama client (auto-starts Ollama if needed).
	client, err := NewOllamaClient(cfg.Endpoint, cfg.ModelsPath)
	if err != nil {
		return nil, fmt.Errorf("create ollama client: %w", err)
	}

	// Configure models (Head + Reasoner only — Scout removed from active config).
	modelConfigs := map[ModelRole]*ModelConfig{
		ModelHead: {
			Name:       cfg.HeadModel,
			Role:       ModelHead,
			ContextLen: cfg.MaxContext,
			KeepAlive:  -1,
		},
		ModelReasoner: {
			Name:       cfg.ReasonerModel,
			Role:       ModelReasoner,
			ContextLen: 128000,
			KeepAlive:  -1,
		},
	}

	models := NewModelManager(client, modelConfigs)
	conversation := NewConversationManager(cfg.SystemPrompt, cfg.MaxContext)

	// Memory: Go-native with disk persistence. No LLM needed.
	persistPath := ""
	if cfg.WorkDir != "" {
		persistPath = filepath.Join(cfg.WorkDir, "state", "memory.json")
	}
	memory := NewMemory(persistPath)

	// Create agent loop.
	loop := NewAgentLoop(client, conversation, models, eventBus, cfg.HeadModel)
	loop.memory = memory // auto-store findings after tool calls

	// Register system tools.
	sysDefs, sysExecs := RegisterSystemTools(cfg.WorkDir)
	loop.RegisterTools(sysDefs, sysExecs)

	// Register routing tools.
	routeDeps := RoutingDeps{
		Client:       client,
		Models:       models,
		Registry:     registry,
		Bus:          eventBus,
		Conversation: conversation,
		Memory:       memory,
		WorkDir:      cfg.WorkDir,
	}
	routeDefs, routeExecs := RegisterRoutingTools(routeDeps)
	loop.RegisterTools(routeDefs, routeExecs)

	// Set up conversation summarizer using the head model.
	conversation.SetSummarizer(func(ctx context.Context, text string) (string, error) {
		msg, _, err := client.Chat(ctx, ChatRequest{
			Model: cfg.HeadModel,
			Messages: []Message{
				{Role: RoleSystem, Content: "Summarize the following conversation concisely, preserving key decisions, facts, and context needed for continuation. Be brief but complete."},
				{Role: RoleUser, Content: text},
			},
		})
		if err != nil {
			return "", err
		}
		return msg.Content, nil
	})

	lo := &LocalOrchestrator{
		client:       client,
		models:       models,
		conversation: conversation,
		memory:       memory,
		loop:         loop,
		bus:          eventBus,
		workDir:      cfg.WorkDir,
	}

	return lo, nil
}

// WarmUp loads the head model into VRAM. Call during startup.
func (lo *LocalOrchestrator) WarmUp(ctx context.Context) error {
	return lo.models.WarmHead(ctx)
}

// Shutdown unloads all models and stops Ollama.
func (lo *LocalOrchestrator) Shutdown() {
	ctx := context.Background()
	for _, role := range []ModelRole{ModelHead, ModelReasoner} {
		_ = lo.models.Unload(ctx, role)
	}
	lo.client.StopOllama()
}

// Send processes a user message through the LLM pipeline.
// Non-blocking — runs the agent loop in a goroutine.
// Events are published to the bus for TUI consumption.
func (lo *LocalOrchestrator) Send(ctx context.Context, message string) error {
	lo.mu.Lock()
	if lo.running {
		lo.mu.Unlock()
		return fmt.Errorf("already processing a message — use /cancel first")
	}

	loopCtx, cancel := context.WithCancel(ctx)
	lo.cancel = cancel
	lo.running = true
	lo.mu.Unlock()

	go func() {
		defer func() {
			lo.mu.Lock()
			lo.running = false
			lo.cancel = nil
			lo.mu.Unlock()
		}()

		if err := lo.loop.Run(loopCtx, message); err != nil && loopCtx.Err() == nil {
			lo.loop.emitError(fmt.Sprintf("Error: %v", err))
		}
	}()

	return nil
}

// Cancel stops the current agent loop execution.
func (lo *LocalOrchestrator) Cancel() error {
	lo.mu.Lock()
	defer lo.mu.Unlock()

	if lo.cancel == nil {
		return fmt.Errorf("nothing to cancel")
	}
	lo.cancel()
	return nil
}

// Available checks if Ollama and the head model are reachable.
func (lo *LocalOrchestrator) Available() bool {
	return lo.client.Available()
}

// Reset clears conversation history.
func (lo *LocalOrchestrator) Reset() {
	lo.conversation.Reset()
}

// IsRunning returns true if the agent loop is currently processing.
func (lo *LocalOrchestrator) IsRunning() bool {
	lo.mu.Lock()
	defer lo.mu.Unlock()
	return lo.running
}

// ModelStatus returns a human-readable status of all managed models and memory.
func (lo *LocalOrchestrator) ModelStatus(ctx context.Context) string {
	status := lo.models.Status(ctx)
	if lo.memory != nil {
		status += "\n" + lo.memory.Summary()
	}
	return status
}

// ConversationLen returns the number of messages in the conversation.
func (lo *LocalOrchestrator) ConversationLen() int {
	return lo.conversation.Len()
}
