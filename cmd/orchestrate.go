package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/llm"
	"github.com/rohanrgit/ag3nts/internal/orchestrator"
	"github.com/rohanrgit/ag3nts/internal/router"
	"github.com/rohanrgit/ag3nts/internal/tui"
)

var primaryFlag string

var orchestrateCmd = &cobra.Command{
	Use:   "orchestrate",
	Short: "Launch the multi-agent orchestrator TUI",
	Long: `Starts the ag3nts orchestrator — a terminal interface for managing
multiple AI agents simultaneously. Chat with your primary agent, dispatch
tasks to others, and watch them work in parallel.

  ag3nts orchestrate                 # use config defaults
  ag3nts orchestrate --primary gemini  # start with Gemini as primary`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runOrchestrate()
	},
}

func init() {
	orchestrateCmd.Flags().StringVar(&primaryFlag, "primary", "", "override the primary agent")
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

	maxConc := cfg.Orchestrator.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 3
	}

	// Create orchestrator.
	orch, err := orchestrator.New(orchestrator.Config{
		Primary:        primary,
		MaxConcurrency: maxConc,
		PersistDir:     persistDir,
		Routes:         routes,
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
	if cfg.LLM.Enabled {
		workDir := layout.Base
		lo, err := llm.NewLocalOrchestrator(llm.OrchestratorConfig{
			Endpoint:      cfg.LLM.Endpoint,
			ModelsPath:    cfg.LLM.ModelsPath,
			HeadModel:     cfg.LLM.HeadModel,
			ReasonerModel: cfg.LLM.ReasonerModel,
			SystemPrompt:  cfg.LLM.SystemPrompt,
			WorkDir:       workDir,
			MaxContext:     cfg.LLM.MaxContext,
		}, registry, orch.Bus())
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ local LLM unavailable: %v (falling back to CLI agents)\n", err)
		} else {
			localOrch = lo
			fmt.Fprintf(os.Stderr, "✓ Local LLM orchestrator ready (%s)\n", cfg.LLM.HeadModel)
		}
	}

	// Launch the terminal app.
	app := tui.New(orch, localOrch)
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
