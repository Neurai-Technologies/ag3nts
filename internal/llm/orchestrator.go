package llm

import (
	"context"
	"fmt"
	"sync"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
)

// OrchestratorConfig holds configuration for the local LLM orchestrator.
type OrchestratorConfig struct {
	Endpoint      string // Ollama endpoint (default: http://localhost:11434)
	HeadModel     string // Qwen 3.5 122B model name in Ollama
	ReasonerModel string // Gemma 4 31B model name
	AnalyzerModel string // Llama 4 Scout model name
	SystemPrompt  string // System prompt for head model (optional override)
	WorkDir       string // Working directory for file operations
	MaxContext    int    // Context window limit in tokens (default: 256000)
}

const defaultSystemPrompt = `You are the head orchestrator of ag3nts, a multi-agent AI system running locally. You manage conversations with the user and dispatch work to specialized agents via tool calls.

You are highly capable — handle most tasks directly. Only delegate when genuinely needed:

Tools available:
- read_file, write_file, run_command, search_files: Direct filesystem and shell access. Use for reading code, writing files, running builds/tests.
- deep_reason: Delegate to Gemma 4 31B for mathematical proofs, formal evaluation, architectural trade-off analysis, or problems requiring deeper reasoning than you can provide.
- analyze_repo: Delegate to Llama 4 Scout (10M token context) when you need to see more code than fits in your 256K context — full-repo reviews, cross-cutting refactors, architecture audits.
- web_research: Delegate to Gemini CLI for current information from the internet — documentation, latest releases, news, anything not in your training data.
- code_task: Delegate to Claude Code for complex multi-file coding tasks requiring deep code understanding.
- implement: Delegate to Codex CLI for focused, single-purpose implementation tasks.

Guidelines:
- For simple questions, answer directly without tools.
- Always explain briefly what you're doing before calling a tool.
- After receiving tool results, synthesize and present them clearly.
- Be concise and direct. The user is a developer — no hand-holding.`

// LocalOrchestrator wraps the agent loop, conversation, and model management
// into a single entry point for the TUI.
type LocalOrchestrator struct {
	client       *OllamaClient
	models       *ModelManager
	conversation *ConversationManager
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

	// Create Ollama client.
	client, err := NewOllamaClient(cfg.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("create ollama client: %w", err)
	}
	if !client.Available() {
		return nil, fmt.Errorf("ollama not reachable at %s", cfg.Endpoint)
	}

	// Configure models.
	modelConfigs := map[ModelRole]*ModelConfig{
		ModelHead: {
			Name:       cfg.HeadModel,
			Role:       ModelHead,
			ContextLen: cfg.MaxContext,
			KeepAlive:  "-1", // permanent
		},
		ModelReasoner: {
			Name:       cfg.ReasonerModel,
			Role:       ModelReasoner,
			ContextLen: 128000,
			KeepAlive:  "5m",
		},
		ModelAnalyzer: {
			Name:       cfg.AnalyzerModel,
			Role:       ModelAnalyzer,
			ContextLen: 131072, // Scout supports much more but practical limit
			KeepAlive:  "5m",
		},
	}

	models := NewModelManager(client, modelConfigs)
	conversation := NewConversationManager(cfg.SystemPrompt, cfg.MaxContext)

	// Create agent loop.
	loop := NewAgentLoop(client, conversation, models, eventBus, cfg.HeadModel)

	// Register system tools.
	sysDefs, sysExecs := RegisterSystemTools(cfg.WorkDir)
	loop.RegisterTools(sysDefs, sysExecs)

	// Register routing tools.
	routeDeps := RoutingDeps{
		Client:   client,
		Models:   models,
		Registry: registry,
		Bus:      eventBus,
		WorkDir:  cfg.WorkDir,
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

// ModelStatus returns a human-readable status of all managed models.
func (lo *LocalOrchestrator) ModelStatus(ctx context.Context) string {
	return lo.models.Status(ctx)
}

// ConversationLen returns the number of messages in the conversation.
func (lo *LocalOrchestrator) ConversationLen() int {
	return lo.conversation.Len()
}
