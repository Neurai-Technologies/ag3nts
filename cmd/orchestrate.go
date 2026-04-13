package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/rohanrgit/ag3nts/internal/agent"
	m3m0ry "github.com/rohanrgit/ag3nts/internal/context"
	"github.com/rohanrgit/ag3nts/internal/llm"
	"github.com/rohanrgit/ag3nts/internal/logging"
	"github.com/rohanrgit/ag3nts/internal/orchestrator"
	"github.com/rohanrgit/ag3nts/internal/paths"
	"github.com/rohanrgit/ag3nts/internal/router"
	"github.com/rohanrgit/ag3nts/internal/scheduler"
	"github.com/rohanrgit/ag3nts/internal/security"
	"github.com/rohanrgit/ag3nts/internal/store"
	"github.com/rohanrgit/ag3nts/internal/tui"
)

var (
	primaryFlag string
	resumeFlag  string
	forkFlag    string
)

var orchestrateCmd = &cobra.Command{
	Use:   "orchestrate",
	Short: "Launch the multi-agent orchestrator TUI",
	Long: `Starts the ag3nts orchestrator — a terminal interface for managing
multiple AI agents simultaneously. Chat with your primary agent, dispatch
tasks to others, and watch them work in parallel.

  ag3nts orchestrate                       # use config defaults
  ag3nts orchestrate --primary gemini      # start with Gemini as primary
  ag3nts orchestrate --resume <session-id> # resume a previous session
  ag3nts orchestrate --fork <session-id>   # fork from a previous session`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runOrchestrate()
	},
}

func init() {
	orchestrateCmd.Flags().StringVar(&primaryFlag, "primary", "", "override the primary agent")
	orchestrateCmd.Flags().StringVar(&resumeFlag, "resume", "", "resume a previous session by ID")
	orchestrateCmd.Flags().StringVar(&forkFlag, "fork", "", "fork from a previous session (new session, same context)")
	rootCmd.AddCommand(orchestrateCmd)
}

func runOrchestrate() error {
	// Build the agent registry from installed tools.
	registry := agent.NewRegistry()

	// Register subprocess agents for installed CLI tools.
	claudeAgent := agent.NewClaudeAgent(layout)
	if claudeAgent.Available() {
		_ = registry.Register(claudeAgent)
	}

	geminiAgent := agent.NewGeminiAgent(layout)
	if geminiAgent.Available() {
		_ = registry.Register(geminiAgent)
	}

	codexAgent := agent.NewCodexAgent(layout)
	if codexAgent.Available() {
		_ = registry.Register(codexAgent)
	}

	// Register HTTP agents from config (e.g. Ollama).
	for name, acfg := range cfg.Agents {
		if acfg.Type == "http" && acfg.Endpoint != "" {
			httpAgent, err := agent.NewHTTPAgent(agent.HTTPConfig{
				Name:         name,
				Endpoint:     acfg.Endpoint,
				Model:        acfg.Model,
				Capabilities: acfg.Capabilities,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "⚠ skipping agent %q: %v\n", name, err)
				continue
			}
			_ = registry.Register(httpAgent)
		}
	}

	if registry.Count() == 0 {
		return fmt.Errorf("no agents available — run 'ag3nts install' first")
	}

	// Determine primary agent: CLI flag > config > auto-detect.
	primary := primaryFlag
	if primary == "" && cfg.Orchestrator.Primary != "" {
		primary = cfg.Orchestrator.Primary
	}
	if primary == "" {
		primary = detectPrimary(registry)
	}
	if registry.Get(primary) == nil {
		return fmt.Errorf("primary agent %q not available — installed agents: %v", primary, registry.Names())
	}

	// Load routing rules: config > defaults.
	routes := loadRoutes()

	// Build persistence directory.
	persistDir := ""
	if layout != nil {
		persistDir = layout.State + "/orchestrator"
	}

	// Pin the working directory that every subprocess agent (Claude, Gemini,
	// Codex) runs in. Without this, agents inherit ag3nts' own cwd and may
	// walk their own heuristics to find a "project", potentially editing
	// files in unrelated repos. Prefer the user's launch cwd; fall back to
	// ag3nts install root if Getwd fails.
	agentWorkDir, gwdErr := os.Getwd()
	if gwdErr != nil || agentWorkDir == "" {
		if layout != nil {
			agentWorkDir = layout.Base
		}
	}
	fmt.Fprintf(os.Stderr, "✓ Agent workdir: %s\n", agentWorkDir)

	maxConc := cfg.Orchestrator.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 3
	}

	// Open SQLite store for structured persistence.
	var storeDB *store.DB
	sessionID := fmt.Sprintf("%s_%d", time.Now().Format("20060102"), time.Now().UnixNano()%10000)
	if layout != nil {
		dbPath := layout.State + "/ag3nts.db"
		db, err := store.Open(store.Config{Path: dbPath})
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ SQLite unavailable: %v (falling back to JSON)\n", err)
		} else {
			storeDB = db
			defer storeDB.Close()

			switch {
			case resumeFlag != "":
				// Resume: reuse existing session.
				sess, err := storeDB.GetSession(resumeFlag)
				if err != nil || sess == nil {
					return fmt.Errorf("session %q not found", resumeFlag)
				}
				sessionID = sess.ID
				if sess.PrimaryAgent != "" {
					primary = sess.PrimaryAgent
				}
				_ = storeDB.UpdateSessionStatus(sessionID, "active")
				fmt.Fprintf(os.Stderr, "✓ Resuming session %s\n", sessionID)

			case forkFlag != "":
				// Fork: create new session, inherit context from source.
				srcSess, err := storeDB.GetSession(forkFlag)
				if err != nil || srcSess == nil {
					return fmt.Errorf("session %q not found for fork", forkFlag)
				}
				wd, _ := os.Getwd()
				_ = storeDB.CreateSession(&store.SessionRecord{
					ID:           sessionID,
					Name:         "fork of " + srcSess.ID,
					WorkingDir:   wd,
					PrimaryAgent: primary,
					Status:       "active",
				})
				// Copy completed tasks as context reference.
				srcTasks, _ := storeDB.ListTasks(forkFlag)
				for _, t := range srcTasks {
					if t.Status == "completed" && t.ResultOutput != "" {
						_ = storeDB.CreateTask(&store.TaskRecord{
							ID:           fmt.Sprintf("fork_%s_%d", t.ID, time.Now().UnixNano()%10000),
							SessionID:    sessionID,
							Agent:        t.Agent,
							Type:         t.Type,
							Description:  "[forked] " + t.Description,
							Status:       "completed",
							ResultOutput: t.ResultOutput,
							InputTokens:  t.InputTokens,
							OutputTokens: t.OutputTokens,
							CostUSD:      t.CostUSD,
						})
					}
				}
				fmt.Fprintf(os.Stderr, "✓ Forked from session %s → %s\n", forkFlag, sessionID)

			default:
				// New session.
				wd, _ := os.Getwd()
				_ = storeDB.CreateSession(&store.SessionRecord{
					ID:           sessionID,
					Name:         "orchestrate",
					WorkingDir:   wd,
					PrimaryAgent: primary,
					Status:       "active",
				})
			}
		}
	}

	// Create structured logger if enabled.
	var logger *logging.Logger
	if cfg.Logging.Enabled && layout != nil {
		logsDir := layout.State + "/logs"
		moduleLevels := make(map[string]logging.Level)
		for mod, lvl := range cfg.Logging.ModuleLevels {
			moduleLevels[mod] = logging.ParseLevel(lvl)
		}
		l, err := logging.Open(logsDir, sessionID, logging.ParseLevel(cfg.Logging.Level), moduleLevels)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ Logging unavailable: %v\n", err)
		} else {
			logger = l
			defer logger.Close()
		}
	}

	// Create memory store if SQLite is available.
	var memoryStore *store.MemoryStore
	if storeDB != nil {
		memoryStore = store.NewMemoryStore(storeDB)
	}

	// Create rolling context store (m3m0ry) if enabled and SQLite is available.
	var rollingCtx *m3m0ry.RollingStore
	if cfg.Context.Enabled && storeDB != nil && layout != nil {
		jsonlPath := cfg.Context.JSONLPath
		if jsonlPath == "" {
			jsonlPath = "m3m0ry.jsonl"
		}
		if !filepath.IsAbs(jsonlPath) {
			jsonlPath = filepath.Join(layout.State, jsonlPath)
		}
		rs, err := m3m0ry.Open(m3m0ry.Config{
			Enabled:         true,
			MaxTokens:       cfg.Context.MaxTokens,
			MaxChunkTokens:  cfg.Context.MaxChunkTokens,
			JSONLPath:       jsonlPath,
			EvictHeadroom:   cfg.Context.EvictHeadroom,
			RetrievalLimit:  cfg.Context.RetrievalLimit,
			RetrievalBudget: cfg.Context.RetrievalBudget,
		}, storeDB, sessionID, logger)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ m3m0ry unavailable: %v\n", err)
		} else {
			rollingCtx = rs
			defer rollingCtx.Close()
		}
	}

	// Create security reviewer if enabled.
	var reviewer *security.Reviewer
	if cfg.Security.Enabled {
		blockOnCritical := cfg.Security.BlockOnCritical
		// Pattern-only by default. LLM review requires an agent to be configured.
		reviewer = security.NewReviewer(nil, blockOnCritical)
		fmt.Fprintf(os.Stderr, "✓ Security review enabled (pattern filter, block_on_critical=%v)\n", blockOnCritical)
	}

	// Create orchestrator.
	orch, err := orchestrator.New(orchestrator.Config{
		Primary:        primary,
		MaxConcurrency: maxConc,
		PersistDir:     persistDir,
		Routes:         routes,
		StoreDB:        storeDB,
		SessionID:      sessionID,
		Reviewer:       reviewer,
		Logger:         logger,
		Memory:         memoryStore,
		Context:        rollingCtx,
		BaseDir:        baseDirOrEmpty(layout),
		AgentWorkDir:   agentWorkDir,
	}, registry)
	if err != nil {
		return fmt.Errorf("create orchestrator: %w", err)
	}

	// Handle graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create local LLM orchestrator if configured and Ollama is available.
	var localOrch *llm.LocalOrchestrator

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		if localOrch != nil {
			localOrch.Shutdown()
		}
		_ = orch.Stop()
		cancel()
	}()

	// Start the orchestrator dispatch loop.
	if err := orch.Start(ctx); err != nil {
		return fmt.Errorf("start orchestrator: %w", err)
	}

	// Start background scheduler if SQLite is available.
	if storeDB != nil {
		recipeLoader := buildRecipeLoader()
		sched := scheduler.New(storeDB, recipeLoader, orch, logger)
		if err := sched.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "⚠ Scheduler unavailable: %v\n", err)
		} else {
			defer sched.Stop()
		}
	}

	if cfg.LLM.Enabled {
		lo, err := llm.NewLocalOrchestrator(llm.OrchestratorConfig{
			Endpoint:     cfg.LLM.Endpoint,
			ModelsPath:   cfg.LLM.ModelsPath,
			HeadModel:    cfg.LLM.HeadModel,
			SystemPrompt: cfg.LLM.SystemPrompt,
			WorkDir:      agentWorkDir,
			MaxContext:   cfg.LLM.MaxContext,
			Rolling:      rollingCtx,
		}, registry, orch.Bus())
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ local LLM unavailable: %v (falling back to CLI agents)\n", err)
		} else {
			localOrch = lo
			fmt.Fprintf(os.Stderr, "✓ Ollama connected (%s)\n", cfg.LLM.HeadModel)
			fmt.Fprintf(os.Stderr, "⠋ Loading model into memory...")
			if err := lo.WarmUp(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "\r\033[K⚠ Model warm-up failed: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "\r\033[K✓ Model loaded and ready\n")
			}
			// Wire local LLM messages into m3m0ry for retrieval. The agent
			// loop emits streamed deltas as EventProgress which the recorder
			// filters; the callback receives the aggregated message for both
			// user prompts and assistant responses, so we can index both
			// sides of every turn. Indexing user prompts is critical because
			// users typically search by the action word they asked for,
			// which lives in the prompt not the response.
			if rollingCtx != nil {
				headName := lo.HeadModelName()
				lo.SetMessageCallback(func(role, content string) {
					if content == "" {
						return
					}
					kind := "task_result"
					if role == "user" {
						kind = "user_prompt"
					}
					_ = rollingCtx.Append(&m3m0ry.Chunk{
						SessionID: sessionID,
						Agent:     headName,
						Kind:      kind,
						Content:   content,
						CreatedAt: time.Now(),
					})
				})
			}
		}
	}

	// Launch the terminal app.
	app := tui.New(orch, localOrch, layout.ConfigFile())
	// Wire permission prompts from TUI to LLM orchestrator.
	if localOrch != nil {
		localOrch.SetPermission(app.GetPermissionFunc())
	}
	if err := app.Run(ctx); err != nil {
		_ = orch.Stop()
		return fmt.Errorf("app error: %w", err)
	}

	return orch.Stop()
}

// detectPrimary picks the best available agent as primary, following the
// existing tier-based fallback: claude → codex → gemini.
func detectPrimary(registry *agent.Registry) string {
	for _, name := range []string{"claude", "codex", "gemini"} {
		if a := registry.Get(name); a != nil && a.Available() {
			return name
		}
	}
	// Fall back to first available.
	agents := registry.Available()
	if len(agents) > 0 {
		return agents[0].Name()
	}
	return ""
}

// loadRoutes reads routing rules from the config, falling back to
// sensible defaults if none are configured.
func loadRoutes() []router.Route {
	if len(cfg.Routing.Rules) > 0 {
		routes := make([]router.Route, len(cfg.Routing.Rules))
		for i, r := range cfg.Routing.Rules {
			routes[i] = router.Route{
				Pattern:  r.Pattern,
				Agent:    r.Agent,
				Fallback: r.Fallback,
				Priority: r.Priority,
			}
		}
		return routes
	}

	// Defaults when no routing rules configured.
	return []router.Route{
		{Pattern: "research|explore|analyze", Agent: "gemini", Fallback: "claude", Priority: 1},
		{Pattern: "implement|fix|refactor|code", Agent: "codex", Fallback: "claude", Priority: 2},
		{Pattern: "review|audit|security", Agent: "claude", Priority: 3},
	}
}

// baseDirOrEmpty returns the project root for recipe file: resolution.
func baseDirOrEmpty(layout *paths.Layout) string {
	if layout == nil {
		return ""
	}
	return layout.Base
}
