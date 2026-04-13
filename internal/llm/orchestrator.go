package llm

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
	m3m0ry "github.com/rohanrgit/ag3nts/internal/context"
)

// OrchestratorConfig holds configuration for the local LLM orchestrator.
// PermissionFunc asks the user for approval before executing a dangerous action.
// Returns true if approved, false if denied.
type PermissionFunc func(tool, action string) bool

type OrchestratorConfig struct {
	Endpoint      string                // Ollama endpoint (default: http://localhost:11434)
	HeadModel     string                // Gemma 4 31B (with thinking mode)
	ModelsPath    string                // Path to Ollama models directory (for OLLAMA_MODELS env)
	SystemPrompt  string                // System prompt for head model (optional override)
	WorkDir       string                // Working directory for file operations
	MaxContext    int                   // Context window limit in tokens (default: 256000)
	AskPermission PermissionFunc        // callback to ask user for permission (nil = auto-approve)
	Rolling       *m3m0ry.RollingStore  // session-scoped rolling context (nil = recall falls back to llm.Memory)
}

const defaultSystemPrompt = `<|think|>
You are the coordinator of ag3nts, a multi-agent AI system. You are Gemma 4 31B running locally via Ollama.

YOUR ROLE IS STRICTLY COORDINATION. You do NOT:
- Answer questions yourself (delegate to the right agent)
- Write code (delegate to code_task or implement)
- Research topics (delegate to web_research)
- Generate reports, analysis, or content (delegate to the right agent)
- Fabricate or hallucinate tool outputs, data, or results
- Guess what a tool would return — you MUST actually call it

YOU DO:
- Route user requests to the right agent
- Manage memory (store/recall)
- Break complex requests into steps and coordinate execution
- Summarize results AFTER agents return them (never before)
- Read/search files when needed to determine which agent to route to

Decision tree for every user request:
1. Does it need current info or web search? → web_research (Gemini)
2. Does it need code changes, review, or complex reasoning? → code_task (Claude)
3. Does it need quick implementation or single-file work? → implement (Codex)
4. Does it need file reading to understand context? → read_file / search_files (yourself)
5. Does it need past context? → recall (memory)
6. Is it a simple greeting or question about the system? → answer directly (the ONLY case you respond without delegation)

Tools:
- read_file, write_file, run_command, search_files: Filesystem and shell access. Use for context gathering only, not for doing the user's actual work.
- recall: Retrieve from long-term memory.
- store: Save distilled insights to long-term memory. Always show the user what you're storing first.
- web_research: Delegate to Gemini CLI. Use for ANY question about current events, docs, APIs.
- code_task: Delegate to Claude Code. Use for complex coding, multi-file edits, architecture, review.
- implement: Delegate to Codex CLI. Use for focused single-file implementation.

CRITICAL RULES:
- NEVER fabricate tool outputs. If you haven't called a tool, you don't know what it returns.
- NEVER generate fake data, tables, reports, or statistics. All data must come from actual tool calls.
- When you delegate, wait for the actual result before responding to the user.
- Be concise. The user is a developer.
- When given multi-step work, execute each step via the appropriate agent — don't do it yourself.

Output: Use markdown. Be brief. Show agent results, then store key insights.`

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
	// Use a closure that reads loop.askPermission at call time, not construction time.
	// This allows SetPermission (called after TUI init) to take effect for routing tools.
	routeDeps := RoutingDeps{
		Client:       client,
		Models:       models,
		Registry:     registry,
		Bus:          eventBus,
		Conversation: conversation,
		Memory:       memory,
		Rolling:      cfg.Rolling,
		AskPermission: func(tool, action string) bool {
			if loop.askPermission != nil {
				return loop.askPermission(tool, action)
			}
			return true // auto-approve if no permission func set yet
		},
		WorkDir: cfg.WorkDir,
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
			KeepAlive: -1,
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

// SetMessageCallback configures a callback that fires when the agent loop
// produces an aggregated assistant message. Used by the m3m0ry rolling
// context to capture the full response content for persistence and retrieval,
// since streamed EventProgress chunks are not individually suitable for
// storage.
func (lo *LocalOrchestrator) SetMessageCallback(fn MessageCallback) {
	lo.loop.onMessage = fn
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

// ExportConversation returns the full conversation as plain text.
func (lo *LocalOrchestrator) ExportConversation() string {
	messages := lo.conversation.Messages()
	var sb strings.Builder

	for _, msg := range messages {
		sb.WriteString("[")
		sb.WriteString(msg.Role)
		sb.WriteString("]: ")
		sb.WriteString(msg.Content)
		sb.WriteString("\n")
	}

	return sb.String()
}

// CompactResult holds before/after metrics from a /compact operation.
type CompactResult struct {
	BeforeTokens   int
	AfterTokens    int
	BeforeMessages int
	AfterMessages  int
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
