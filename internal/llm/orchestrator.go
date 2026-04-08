package llm

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
)

// OrchestratorConfig holds configuration for the local LLM orchestrator.
// PermissionFunc asks the user for approval before executing a dangerous action.
// Returns true if approved, false if denied.
type PermissionFunc func(tool, action string) bool

type OrchestratorConfig struct {
	Endpoint      string // Ollama endpoint (default: http://localhost:11434)
	HeadModel     string // Gemma 4 31B (with thinking mode)
	ModelsPath    string // Path to Ollama models directory (for OLLAMA_MODELS env)
	SystemPrompt  string // System prompt for head model (optional override)
	WorkDir       string // Working directory for file operations
	MaxContext    int    // Context window limit in tokens (default: 256000)
	AskPermission PermissionFunc // callback to ask user for permission (nil = auto-approve)
}

const defaultSystemPrompt = `<|think|>
You are Gemma 4 31B, running locally via Ollama on the user's Mac Studio (M3 Ultra, 256GB RAM). You are the head orchestrator of ag3nts, a multi-agent AI system.

When asked what model you are, say "Gemma 4 31B running locally via Ollama with thinking enabled." Never claim to be Claude, GPT, Qwen, or any other model.

The ag3nts system architecture:
- Head orchestrator: You (Gemma 4 31B dense, local via Ollama, 256K context, thinking enabled)
- Web research: Gemini CLI (Google, subprocess, searches the internet)
- Complex coding: Claude Code (Anthropic, subprocess, multi-file edits)
- Implementation: Codex CLI (OpenAI, subprocess, focused single-file tasks)
- Memory: Go-native persistent storage with TF-IDF search (survives across sessions)

You have thinking mode enabled — use it for complex reasoning. You don't need to delegate reasoning to another model. Handle most tasks directly, only delegate for internet access and complex coding.

Tools available:
- read_file, write_file, run_command, search_files: Direct filesystem and shell access.
- recall: Retrieve relevant context from long-term memory. Memory persists across sessions.
- store: Save an important finding, decision, or summary to long-term memory. Store distilled insights, not raw data.
- web_research: Delegate to Gemini CLI for current information from the internet.
- code_task: Delegate to Claude Code for complex multi-file coding tasks.
- implement: Delegate to Codex CLI for focused implementation tasks.

Guidelines:
- For simple questions about yourself or the system, answer directly from this prompt — don't read files.
- Always explain briefly what you're doing before calling a tool.
- IMPORTANT: Always present your findings, analysis, and results IN FULL to the user before storing them. Never just say "I stored it" — show the complete content first, then store. The user needs to see everything.
- After presenting findings, use store() to save key distilled insights to long-term memory.
- Use recall() when you need context from earlier in the session or past decisions.
- Be concise and direct. The user is a developer — no hand-holding.
- CRITICAL: When given a multi-step plan, you MUST execute ALL steps in order. Never skip a step. Never say "I will do X" without actually calling the tool. If a step says "use code_task", you MUST call code_task. If a step says "recall everything", you MUST call recall. Complete every single step before presenting the final summary.

Output formatting:
- Use markdown formatting in your responses: **bold** for key terms, *italic* for emphasis, headers with ## for sections.
- Use tables where comparing options or listing structured data.
- Use bullet lists for findings, numbered lists for steps.
- Use code blocks with language tags for code snippets.
- Separate major sections with --- horizontal rules.

Git commits:
- When the user asks you to commit and push, always include these co-author lines at the end of the commit message:
  Co-Authored-By: ag3nts (Gemma 4 + Codex + Claude + Gemini) <ag3nts@local>
  Include only the agents that actually contributed to the changes in that commit.`

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

	// Single model: Gemma 4 31B is both head and reasoner (thinking mode enabled).
	modelConfigs := map[ModelRole]*ModelConfig{
		ModelHead: {
			Name:       cfg.HeadModel,
			Role:       ModelHead,
			ContextLen: cfg.MaxContext,
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
	loop := NewAgentLoop(client, conversation, models, eventBus, cfg.HeadModel, memory)
	loop.askPermission = cfg.AskPermission

	// Register system tools.
	sysDefs, sysExecs := RegisterSystemTools(cfg.WorkDir)
	loop.RegisterTools(sysDefs, sysExecs)

	// Register routing tools.
	routeDeps := RoutingDeps{
		Client:        client,
		Models:        models,
		Registry:      registry,
		Bus:           eventBus,
		Conversation:  conversation,
		Memory:        memory,
		AskPermission: cfg.AskPermission,
		WorkDir:       cfg.WorkDir,
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

// SetPermission configures the permission callback after creation.
// Called by the TUI after both orchestrator and app are initialized.
func (lo *LocalOrchestrator) SetPermission(fn PermissionFunc) {
	lo.loop.askPermission = fn
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

// HeadModelName returns the display name of the head model (without tag).
func (lo *LocalOrchestrator) HeadModelName() string {
	name := lo.models.ModelName(ModelHead)
	if idx := strings.Index(name, ":"); idx > 0 {
		return name[:idx]
	}
	return name
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

// CompactResult holds before/after metrics from a /compact operation.
type CompactResult struct {
	BeforeTokens    int
	AfterTokens     int
	BeforeMessages  int
	AfterMessages   int
}

// Compact manually triggers conversation summarization and returns metrics.
func (lo *LocalOrchestrator) Compact(ctx context.Context) (*CompactResult, error) {
	before := lo.conversation.EstimateTokens()
	beforeMsgs := lo.conversation.Len()

	if err := lo.conversation.Summarize(ctx); err != nil {
		return nil, err
	}

	return &CompactResult{
		BeforeTokens:   before,
		AfterTokens:    lo.conversation.EstimateTokens(),
		BeforeMessages: beforeMsgs,
		AfterMessages:  lo.conversation.Len(),
	}, nil
}

// MemoryDump returns all persisted in-memory entries in full detail.
func (lo *LocalOrchestrator) MemoryDump() string {
	return lo.memory.FullDump()
}
