// Package tui implements a readline-based terminal interface for ag3nts.
// Simple and stable: readline for input, fmt.Println for output,
// status line printed after each response. No scroll regions or TUI tricks.
package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/chzyer/readline"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
	"github.com/rohanrgit/ag3nts/internal/config"
	"github.com/rohanrgit/ag3nts/internal/llm"
	"github.com/rohanrgit/ag3nts/internal/orchestrator"
	"github.com/rohanrgit/ag3nts/internal/recipe"
	"github.com/rohanrgit/ag3nts/internal/router"
	"github.com/rohanrgit/ag3nts/internal/task"
)

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
var dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#616161"))
var errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF5350")).Bold(true)

// PermissionRequest is sent from tools to the TUI for user approval.
type PermissionRequest struct {
	Tool   string // tool name
	Action string // what it wants to do
	Reply  chan bool
}

// App is the main terminal application.
type App struct {
	orch       *orchestrator.Orchestrator
	localOrch  *llm.LocalOrchestrator
	configPath string
	currentCfg *config.Config
	cfgMu      sync.RWMutex
	eventCh    <-chan bus.Event
	stream     *streamBuffer
	rl         *readline.Instance
	mu         sync.Mutex
	active     string
	permCh       chan PermissionRequest // tools send permission requests here
	allowedTools map[string]bool       // tools approved via "always allow"
	lastTool   map[string]string      // last tool use line by agent (for formatting tool results)

	// Spinner.
	spinning  bool
	spinStop  chan struct{}
	spinMu    sync.Mutex
	spinStart time.Time
	spinLabel string
	tokenIn   int64
	tokenOut  int64

	// Session stats.
	sessionStart  time.Time
	totalTokenIn  int64
	totalTokenOut int64
	agentTokens   map[string][3]int64 // [input, output, cost_microdollars]
	agentTokensMu sync.RWMutex
	totalCostUSD  float64

	// Recipe pipeline tracking — drives inline banners (B.2) and the
	// sticky one-line status above the prompt (B.3). Updated by
	// handleEvent on repair.* task transitions.
	pipeline      *pipelineTracker
	stickyStatus  string // current sticky status line above the prompt (empty = no status shown)
	stickyMu      sync.Mutex
}

// newSlashCompleter creates a tab-completion handler for slash commands.
func newSlashCompleter() *readline.PrefixCompleter {
	return readline.NewPrefixCompleter(
		readline.PcItem("/help"),
		readline.PcItem("/quit"),
		readline.PcItem("/exit"),
		readline.PcItem("/cancel"),
		readline.PcItem("/compact"),
		readline.PcItem("/agents"),
		readline.PcItem("/tasks"),
		readline.PcItem("/status"),
		readline.PcItem("/reload"),
		readline.PcItem("/cost"),
		readline.PcItem("/recipe"),
		readline.PcItem("/schedule"),
		readline.PcItem("/m3m0ry",
			readline.PcItem("stats"),
			readline.PcItem("search"),
			readline.PcItem("tail"),
		),
		readline.PcItem("/memory"),
		readline.PcItem("/local",
			readline.PcItem("status"),
			readline.PcItem("reset"),
		),
	)
}

func New(orch *orchestrator.Orchestrator, localOrch *llm.LocalOrchestrator, configPath string) *App {
	currentCfg := config.Default()
	if configPath != "" {
		if loaded, err := config.Load(configPath); err == nil {
			currentCfg = loaded
		}
	}

	return &App{
		orch:        orch,
		localOrch:   localOrch,
		configPath:  configPath,
		currentCfg:  currentCfg,
		eventCh:     orch.Bus().Subscribe(512, "system"),
		stream:      newStreamBuffer(),
		permCh:       make(chan PermissionRequest),
		allowedTools: make(map[string]bool),
		lastTool:    make(map[string]string),
		agentTokens: make(map[string][3]int64),
		pipeline:    newPipelineTracker(),
	}
}

func cloneConfig(cfg *config.Config) *config.Config {
	if cfg == nil {
		return nil
	}
	clone := *cfg
	return &clone
}

func effectiveMaxConcurrency(cfg *config.Config) int {
	if cfg == nil || cfg.Orchestrator.MaxConcurrency <= 0 {
		return 3
	}
	return cfg.Orchestrator.MaxConcurrency
}

func defaultRoutes() []router.Route {
	return []router.Route{
		{Pattern: "research|explore|analyze", Agent: "gemini", Fallback: "claude", Priority: 1},
		{Pattern: "implement|fix|refactor|code", Agent: "codex", Fallback: "claude", Priority: 2},
		{Pattern: "review|audit|security", Agent: "claude", Priority: 3},
	}
}

func routesFromConfig(cfg *config.Config) []router.Route {
	if cfg != nil && len(cfg.Routing.Rules) > 0 {
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
	return defaultRoutes()
}

func routesEqual(a, b []router.Route) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Pattern != b[i].Pattern ||
			a[i].Agent != b[i].Agent ||
			a[i].Fallback != b[i].Fallback ||
			a[i].Priority != b[i].Priority {
			return false
		}
	}
	return true
}

func hasRestartRequiredChanges(current, next *config.Config) bool {
	if current == nil || next == nil {
		return false
	}

	if current.Orchestrator.Primary != next.Orchestrator.Primary {
		return true
	}
	if !reflect.DeepEqual(current.LLM, next.LLM) {
		return true
	}
	return !reflect.DeepEqual(current.Agents, next.Agents)
}

func (a *App) Run(ctx context.Context) error {
	a.sessionStart = time.Now()

	// Enable bracketed paste so we can detect pasted text and join newlines.
	EnableBracketedPaste()

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          "> ",
		HistoryFile:     historyPath(),
		InterruptPrompt: "^C",
		EOFPrompt:       "/quit",
		HistoryLimit:    1000,
		Stdin:           NewPasteReader(os.Stdin),
		AutoComplete:    newSlashCompleter(),
	})
	if err != nil {
		return fmt.Errorf("readline init: %w", err)
	}
	defer rl.Close()
	a.rl = rl

	go a.eventLoop(ctx)
	a.updateTitle()

	// Get the PasteReader for multi-line terminator tracking.
	pasteReader, _ := rl.Config.Stdin.(*PasteReader)

	for {
		fmt.Println(dimStyle.Render("─────────────────────────────────────────────────────────"))

		// Multi-line accumulation: Ctrl+J (LF) continues, Enter (CR) submits.
		var accumulated strings.Builder
		prompt := "> "

		for {
			rl.SetPrompt(prompt)
			if pasteReader != nil {
				pasteReader.LastTerminator = 0
			}
			line, err := rl.Readline()
			if err != nil {
				if err == readline.ErrInterrupt {
					if accumulated.Len() > 0 {
						// Cancel multi-line input, start over.
						accumulated.Reset()
						break
					}
					if a.localOrch != nil && a.localOrch.IsRunning() {
						_ = a.localOrch.Cancel()
						a.stopSpinner()
						fmt.Println(dimStyle.Render("  cancelled"))
						break
					}
					DisableBracketedPaste()
					a.shutdown()
					return nil
				}
				if err == io.EOF {
					DisableBracketedPaste()
					a.shutdown()
					return nil
				}
				return err
			}

			accumulated.WriteString(line)

			// Check if Alt+Enter (ESC+CR=27) was the terminator → continue input.
			if pasteReader != nil && pasteReader.LastTerminator == 27 {
				accumulated.WriteString("\n")
				prompt = "... > "
				continue
			}

			// Enter (CR=13) or unknown → submit.
			break
		}

		input := strings.TrimSpace(accumulated.String())
		if input == "" {
			continue
		}

		fmt.Println(dimStyle.Render("─────────────────────────────────────────────────────────"))

		if input == "/quit" || input == "/exit" {
			DisableBracketedPaste()
			a.shutdown()
			return nil
		}

		a.handleInput(ctx, input)
	}
}

func (a *App) shutdown() {
	a.stopSpinner()
	if a.localOrch != nil {
		fmt.Fprintln(os.Stderr, "Unloading models...")
		a.localOrch.Shutdown()
		fmt.Fprintln(os.Stderr, "Done.")
	}
}

func historyPath() string {
	home, _ := os.UserHomeDir()
	return home + "/.ag3nts_history"
}

// --- Output ---

func (a *App) println(s string) {
	a.mu.Lock()
	fmt.Println(s)
	a.mu.Unlock()
}

func (a *App) printLine(source, text string) {
	a.stopSpinner()
	ts := dimStyle.Render(time.Now().Format("15:04:05"))
	styled := ts + " " +
		lipgloss.NewStyle().Foreground(agentColor(source)).Bold(true).Render(source) +
		" " + text
	a.println(styled)
}

func (a *App) printError(source, text string) {
	a.stopSpinner()
	ts := dimStyle.Render(time.Now().Format("15:04:05"))
	styled := ts + " " +
		errorStyle.Render("✘") + " " +
		errorStyle.Render(source) + " " +
		dimStyle.Render(text)
	a.println(styled)
}

func (a *App) printLines(source, content string) {
	for _, line := range strings.Split(content, "\n") {
		a.printLine(source, line)
	}
}

// firstLine returns the first non-empty line of s, trimmed.
// Used for compact task list rendering so multi-line descriptions
// (which include full prompt templates) collapse to a single line.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// printTaskDetails renders the full state of a single task, used by
// /task <id>. Shows status, agent, type, full description, dependencies,
// timing, and result summary.
func (a *App) printTaskDetails(t *task.Task) {
	var b strings.Builder
	fmt.Fprintf(&b, "Task: %s\n", t.ID)
	fmt.Fprintf(&b, "  status:  %s %s\n", taskIcon(t.Status.String()), t.Status.String())
	fmt.Fprintf(&b, "  type:    %s\n", t.Type)
	if t.Agent != "" {
		fmt.Fprintf(&b, "  agent:   %s\n", t.Agent)
	}
	if t.SessionID != "" {
		fmt.Fprintf(&b, "  session: %s\n", t.SessionID)
	}
	if !t.CreatedAt.IsZero() {
		fmt.Fprintf(&b, "  created: %s\n", t.CreatedAt.Format("15:04:05"))
	}
	if !t.StartedAt.IsZero() {
		fmt.Fprintf(&b, "  started: %s\n", t.StartedAt.Format("15:04:05"))
	}
	if !t.CompletedAt.IsZero() {
		fmt.Fprintf(&b, "  done:    %s\n", t.CompletedAt.Format("15:04:05"))
	}
	if len(t.DependsOn) > 0 {
		fmt.Fprintf(&b, "  deps:    %s\n", strings.Join(t.DependsOn, ", "))
	}
	if len(t.ContextFrom) > 0 {
		fmt.Fprintf(&b, "  context: %s\n", strings.Join(t.ContextFrom, ", "))
	}
	if t.Result != nil {
		if t.Result.Error != "" {
			fmt.Fprintf(&b, "  error:   %s\n", t.Result.Error)
		}
		if t.Result.Usage != nil {
			fmt.Fprintf(&b, "  tokens:  ↑%d ↓%d\n", t.Result.Usage.InputTokens, t.Result.Usage.OutputTokens)
		}
		if t.Result.Output != "" {
			out := t.Result.Output
			if len(out) > 800 {
				out = out[:800] + "\n[... truncated, see state/orchestrator/results/ for full output]"
			}
			fmt.Fprintf(&b, "  output:\n%s\n", indent(out, "    "))
		}
	}
	fmt.Fprintf(&b, "  description:\n%s", indent(t.Description, "    "))
	a.printLines("ag3nts", b.String())
}

// maybeUpdatePipeline checks whether the given agent event belongs
// to a repair recipe stage and updates the pipeline tracker if so.
// On state transitions, prints the inline banner (B.2) and updates
// the spinner label to the sticky pipeline status (B.3).
//
// The first event for a new runID triggers eager stage discovery —
// the tracker scans the queue for all sibling stages and adds them
// as pending so the banner shows the full pipeline shape from the
// start, not just the stages that have already started executing.
func (a *App) maybeUpdatePipeline(evt agent.AgentEvent) {
	t := a.orch.Tasks().Get(evt.TaskID)
	if t == nil || !strings.HasPrefix(t.Type, "repair.") {
		return
	}

	// Determine new status from event kind.
	var newStatus stageStatus
	switch evt.Kind {
	case agent.EventInit:
		newStatus = stageRunning
	case agent.EventComplete:
		// Don't downgrade a failed stage to completed if EventError
		// already arrived for this task.
		runID := runIDFromTaskID(evt.TaskID)
		if a.pipeline.stageStatusFor(runID, evt.TaskID) == stageFailed {
			return
		}
		newStatus = stageCompleted
	case agent.EventError:
		newStatus = stageFailed
	default:
		return
	}

	// Eager discovery: if this is a brand-new runID, scan the queue
	// for all sibling stages and add them as pending so the banner
	// shows the full pipeline shape.
	runID := runIDFromTaskID(evt.TaskID)
	if !a.pipeline.hasRun(runID) {
		a.pipeline.discoverStages(runID, func() []taskMeta {
			all := a.orch.Tasks().List()
			out := make([]taskMeta, 0, len(all))
			for _, qt := range all {
				out = append(out, taskMeta{id: qt.ID, taskType: qt.Type})
			}
			return out
		})
	}

	run := a.pipeline.updateStage(evt.TaskID, t.Type, newStatus)
	if run == nil {
		return
	}

	// Render the inline banner only on stage transitions worth
	// surfacing — running, completed, failed. Skip pending (set
	// during discovery) since that's a no-op visually.
	if newStatus == stageRunning || newStatus == stageCompleted || newStatus == stageFailed {
		a.printPipelineBanner(run)
	}

	// Update the spinner label to reflect current pipeline state
	// (sticky status, B.3). When the run reaches a terminal state,
	// clear the sticky label so the spinner reverts on its next
	// natural restart.
	if run.isTerminal() {
		a.stickyMu.Lock()
		a.stickyStatus = ""
		a.stickyMu.Unlock()
	} else {
		sticky := renderStickyStatus(run)
		a.stickyMu.Lock()
		a.stickyStatus = sticky
		a.stickyMu.Unlock()
		// Update the live spinner label so the user sees pipeline
		// state immediately, not just on the next spinner restart.
		a.spinMu.Lock()
		if a.spinning {
			a.spinLabel = sticky
		}
		a.spinMu.Unlock()
	}
}

// printPipelineBanner prints the inline pipeline banner above the
// current cursor position. Stops the spinner first to avoid drawing
// over its line, then restarts it after.
func (a *App) printPipelineBanner(run *recipeRunState) {
	banner := renderInlineBanner(run)
	if banner == "" {
		return
	}
	a.spinMu.Lock()
	wasSpinning := a.spinning
	label := a.spinLabel
	a.spinMu.Unlock()

	if wasSpinning {
		a.stopSpinner()
	}
	a.println("")
	for _, line := range strings.Split(banner, "\n") {
		a.println(line)
	}
	if wasSpinning {
		a.startSpinner(label)
	}
}

// indent prepends prefix to each line of s.
func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

func (a *App) printErrors(source, content string) {
	for _, line := range strings.Split(content, "\n") {
		a.printError(source, line)
	}
}

// printDiff shows git diff in Claude Code format:
//
//	Update(file.go)
//	⎿  Added N lines, removed M lines
//	    12 - old line
//	    12 + new line
func (a *App) printDiff(rawDiff string) {
	addStyle := lipgloss.NewStyle().Background(lipgloss.Color("#2E7D32"))
	delStyle := lipgloss.NewStyle().Background(lipgloss.Color("#C62828"))
	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#42A5F5")).Bold(true)

	lines := strings.Split(rawDiff, "\n")
	var currentFile string
	var added, removed int
	var hunks []string

	flushFile := func() {
		if currentFile == "" {
			return
		}
		a.println("")
		a.println(headerStyle.Render("  Update(" + currentFile + ")"))
		a.println(dimStyle.Render(fmt.Sprintf("  ⎿  Added %d lines, removed %d lines", added, removed)))
		// Show up to 30 lines of context.
		limit := 30
		for i, h := range hunks {
			if i >= limit {
				a.println(dimStyle.Render(fmt.Sprintf("      ... %d more lines", len(hunks)-limit)))
				break
			}
			a.println(h)
		}
		currentFile = ""
		added = 0
		removed = 0
		hunks = nil
	}

	lineNum := 0
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "diff --git"):
			flushFile()
		case strings.HasPrefix(line, "+++ b/"):
			currentFile = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "--- a/"):
			// skip, we use +++ for the filename
		case strings.HasPrefix(line, "@@"):
			// Parse line number from @@ -X,Y +Z,W @@
			if idx := strings.Index(line, "+"); idx >= 0 {
				numStr := line[idx+1:]
				if commaIdx := strings.Index(numStr, ","); commaIdx > 0 {
					numStr = numStr[:commaIdx]
				} else if spaceIdx := strings.Index(numStr, " "); spaceIdx > 0 {
					numStr = numStr[:spaceIdx]
				}
				fmt.Sscanf(numStr, "%d", &lineNum)
			}
		case strings.HasPrefix(line, "+"):
			added++
			hunks = append(hunks, fmt.Sprintf("      %s", addStyle.Render(fmt.Sprintf("%d + %s", lineNum, line[1:]))))
			lineNum++
		case strings.HasPrefix(line, "-"):
			removed++
			hunks = append(hunks, fmt.Sprintf("      %s", delStyle.Render(fmt.Sprintf("%d - %s", lineNum, line[1:]))))
		default:
			if currentFile != "" && len(line) > 0 {
				hunks = append(hunks, dimStyle.Render(fmt.Sprintf("      %d   %s", lineNum, line)))
				lineNum++
			}
		}
	}
	flushFile()
	a.println("")
}

// --- Status line (printed after each response) ---

func (a *App) printStatusLine() {
	var parts []string

	if a.localOrch != nil {
		model := a.headModel()
		if model != "" {
			parts = append(parts, model)
		}

		convLen := a.localOrch.ConversationLen()
		estTokens := convLen * 200
		maxCtx := 256000
		pct := estTokens * 100 / maxCtx
		if pct > 100 {
			pct = 100
		}
		filled := pct / 10
		bar := strings.Repeat("#", filled) + strings.Repeat("-", 10-filled)
		parts = append(parts, fmt.Sprintf("[%s] %d%%", bar, pct))
	}

	tokIn := atomic.LoadInt64(&a.totalTokenIn)
	tokOut := atomic.LoadInt64(&a.totalTokenOut)
	if tokIn > 0 || tokOut > 0 {
		parts = append(parts, fmt.Sprintf("↑%s ↓%s", formatTokens(tokIn), formatTokens(tokOut)))
	}

	a.agentTokensMu.RLock()
	cost := a.totalCostUSD
	a.agentTokensMu.RUnlock()
	if cost > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", cost))
	}

	elapsed := time.Since(a.sessionStart).Round(time.Second)
	parts = append(parts, formatDuration(elapsed))

	if a.localOrch != nil {
		parts = append(parts, fmt.Sprintf("msgs: %d", a.localOrch.ConversationLen()))
	}

	a.println(dimStyle.Render("  " + strings.Join(parts, " | ")))
}

// --- Spinner ---

func (a *App) startSpinner(label string) {
	a.spinMu.Lock()
	defer a.spinMu.Unlock()

	if a.spinning {
		a.spinLabel = label
		return
	}
	a.spinning = true
	a.spinStop = make(chan struct{})
	a.spinStart = time.Now()
	a.spinLabel = label
	// Fresh idle→running transition: reset counters for a new task.
	// Mid-task restarts (after a paragraph flush) do NOT reset, because
	// the spinner stays marked "running" via its internal state and we
	// preserve tokenIn/tokenOut across flushes.
	atomic.StoreInt64(&a.tokenIn, 0)
	atomic.StoreInt64(&a.tokenOut, 0)

	go func() {
		i := 0
		for {
			select {
			case <-a.spinStop:
				fmt.Fprintf(os.Stderr, "\r\033[K")
				return
			default:
				frame := spinnerFrames[i%len(spinnerFrames)]
				elapsed := time.Since(a.spinStart).Round(time.Second)
				tokOut := atomic.LoadInt64(&a.tokenOut)

				status := a.spinLabel
				if elapsed >= time.Second {
					status += fmt.Sprintf(" (%s", formatDuration(elapsed))
					if tokOut > 0 {
						status += fmt.Sprintf(" · ↓ %s tokens", formatTokens(tokOut))
					}
					status += ")"
				}

				fmt.Fprintf(os.Stderr, "\r\033[K%s %s", dimStyle.Render(frame), dimStyle.Render(status))
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
}

func (a *App) stopSpinner() {
	a.spinMu.Lock()
	defer a.spinMu.Unlock()
	if !a.spinning {
		return
	}
	close(a.spinStop)
	a.spinning = false
	// Do NOT reset tokenIn/tokenOut here — stopSpinner is called in the
	// middle of a task whenever we need to print over the spinner line
	// (e.g. between paragraph flushes during streaming). Resetting the
	// counters here wipes the in-flight token count and the spinner
	// restart shows "0 tokens" which made it look like nothing was
	// happening. Counters reset on idle→running transition in startSpinner.
	time.Sleep(100 * time.Millisecond)
}

func (a *App) addTokens(chars int) {
	atomic.AddInt64(&a.tokenOut, int64(chars/4))
}

// --- Helpers ---

func (a *App) headModel() string {
	if a.localOrch != nil {
		return a.localOrch.HeadModelName()
	}
	return ""
}

func (a *App) updateTitle() {
	status := "ag3nts"
	if a.localOrch != nil {
		if a.localOrch.IsRunning() {
			status += " | thinking"
		} else {
			status += " | ready"
		}
		status += fmt.Sprintf(" | msgs: %d", a.localOrch.ConversationLen())
	}
	names := a.orch.Agents().Names()
	status += " | " + strings.Join(names, ", ")
	fmt.Fprintf(os.Stderr, "\033]0;%s\007", status)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm %ds", m, s)
}

func formatTokens(n int64) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

// --- Input handling ---

func (a *App) handleInput(ctx context.Context, input string) {
	if strings.HasPrefix(input, "/") {
		a.handleSlash(ctx, input)
		return
	}

	if a.localOrch != nil && a.localOrch.Available() {
		// If already processing (e.g. extra lines from paste), drop silently.
		if a.localOrch.IsRunning() {
			return
		}
		a.startSpinner("thinking...")
		if err := a.localOrch.Send(ctx, input); err != nil {
			a.stopSpinner()
			a.printError("error", err.Error())
			return
		}
		a.waitForCompletion()
		time.Sleep(200 * time.Millisecond)
		elapsed := time.Since(a.spinStart).Round(time.Second)
		tokIn := atomic.LoadInt64(&a.totalTokenIn)
		tokOut := atomic.LoadInt64(&a.totalTokenOut)
		a.println(lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784")).Bold(true).Render("✓") +
			dimStyle.Render(fmt.Sprintf(" complete (%s · ↑%s ↓%s)", formatDuration(elapsed), formatTokens(tokIn), formatTokens(tokOut))))
		a.printStatusLine()
		return
	}

	// If fallback routing is active but something is already running, drop.
	if a.localOrch != nil && a.localOrch.IsRunning() {
		return
	}

	a.handleFallback(ctx, input)
}

func (a *App) waitForCompletion() {
	if a.localOrch == nil {
		return
	}
	for a.localOrch.IsRunning() {
		time.Sleep(100 * time.Millisecond)
	}
	a.stopSpinner()
	a.updateTitle()
}

func (a *App) handleFallback(ctx context.Context, input string) {
	parts := strings.SplitN(input, " ", 2)
	if len(parts) >= 1 {
		agentName := strings.ToLower(strings.TrimRight(parts[0], ",.;:!?"))
		if a.orch.Agents().Get(agentName) != nil {
			a.active = agentName
			message := ""
			if len(parts) == 2 {
				message = parts[1]
			}
			if message == "" {
				a.printLine("ag3nts", fmt.Sprintf("Switched to %s.", agentName))
				return
			}
			a.printLine("you→"+agentName, message)
			if err := a.orch.SendTo(agentName, message); err != nil {
				a.printError("error", err.Error())
			}
			return
		}
	}

	if isResearchQuery(input) && a.orch.Agents().Get("gemini") != nil {
		a.printLine("ag3nts", "researching: "+input)
		if err := a.orch.Research(input); err != nil {
			a.printError("error", err.Error())
		}
		return
	}

	target := a.active
	if target == "" {
		target = a.orch.Primary()
	}
	a.printLine("you", input)
	if target == a.orch.Primary() {
		if err := a.orch.Send(input); err != nil {
			a.printError("error", err.Error())
		}
	} else {
		if err := a.orch.SendTo(target, input); err != nil {
			a.printError("error", err.Error())
		}
	}
}

// --- Slash commands ---

func (a *App) handleReload(ctx context.Context) {
	_ = ctx

	if strings.TrimSpace(a.configPath) == "" {
		a.printError("ag3nts", "Reload failed: config path is not set")
		return
	}

	nextCfg, err := config.Load(a.configPath)
	if err != nil {
		a.printError("ag3nts", fmt.Sprintf("Reload failed: %v", err))
		return
	}

	a.cfgMu.RLock()
	currentCfg := cloneConfig(a.currentCfg)
	a.cfgMu.RUnlock()
	if currentCfg == nil {
		currentCfg = config.Default()
	}

	currentRoutes := routesFromConfig(currentCfg)
	nextRoutes := routesFromConfig(nextCfg)
	currentMax := effectiveMaxConcurrency(currentCfg)
	nextMax := effectiveMaxConcurrency(nextCfg)

	var updates []string

	if !routesEqual(currentRoutes, nextRoutes) {
		if err := a.orch.UpdateRouting(nextRoutes); err != nil {
			a.printError("ag3nts", fmt.Sprintf("Reload failed: %v", err))
			return
		}
		updates = append(updates, fmt.Sprintf("routing rules updated (%d -> %d)", len(currentRoutes), len(nextRoutes)))
		currentCfg.Routing = nextCfg.Routing
	}

	if currentMax != nextMax {
		a.orch.UpdateMaxConcurrency(nextMax)
		updates = append(updates, fmt.Sprintf("max_concurrency updated (%d -> %d)", currentMax, nextMax))
		currentCfg.Orchestrator.MaxConcurrency = nextCfg.Orchestrator.MaxConcurrency
	}

	if len(updates) == 0 {
		a.printLine("ag3nts", fmt.Sprintf("Reloaded %s: no hot-reloadable changes.", a.configPath))
	} else {
		a.printLines("ag3nts", "Reloaded configuration:\n  - "+strings.Join(updates, "\n  - "))
	}

	if hasRestartRequiredChanges(currentCfg, nextCfg) {
		a.printLine("ag3nts", "⚠ Some settings changed (e.g. LLM model, endpoint, primary agent) that require a restart to take effect.")
	}

	a.cfgMu.Lock()
	a.currentCfg = currentCfg
	a.cfgMu.Unlock()
}

func (a *App) handleSlash(ctx context.Context, input string) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/help":
		lines := []string{
			"Commands:",
			"  /cancel   — cancel current operation (or Ctrl+C)",
			"  /compact  — compress conversation history to free context",
			"  /export   — export conversation to a timestamped file",
			"  /agents   — list agents",
			"  /tasks    — list current-session tasks (compact). /task <id> for details, /task list --all for cross-session",
			"  /status   — show overview",
			"  /reload   — reload config and apply hot settings",
			"  /cost    — show session cost breakdown",
			"  /recipe   — list or run a recipe (/recipe <name> [key=val...])",
			"  /schedule — list background schedules",
			"  /m3m0ry   — rolling context (/m3m0ry stats | search <q> | tail [n])",
			"  /quit     — exit",
			"Errors are prefixed with a red ✘ icon.",
		}
		if a.localOrch != nil {
			lines = append(lines, "",
				"Local LLM:",
				"  /local status  — show loaded models + memory",
				"  /local reset   — clear conversation + memory",
			)
		}
		a.printLines("ag3nts", strings.Join(lines, "\n"))

	case "/cancel":
		if a.localOrch != nil {
			if err := a.localOrch.Cancel(); err != nil {
				a.printLine("ag3nts", err.Error())
			} else {
				a.stopSpinner()
				a.printLine("ag3nts", "Cancelled.")
			}
			return
		}
		target := a.active
		if target == "" {
			target = a.orch.Primary()
		}
		if err := a.orch.Cancel(target); err != nil {
			a.printLine("ag3nts", err.Error())
		} else {
			a.printLine("ag3nts", "Cancelled "+target+".")
		}

	case "/local":
		if a.localOrch == nil {
			a.printLine("ag3nts", "Local LLM not configured.")
			return
		}
		sub := ""
		if len(parts) > 1 {
			sub = parts[1]
		}
		switch sub {
		case "status":
			a.printLines("ag3nts", a.localOrch.ModelStatus(context.Background()))
		case "reset":
			a.localOrch.Reset()
			a.printLine("ag3nts", "Conversation and memory cleared.")
		default:
			a.printLine("ag3nts", "Usage: /local status | /local reset")
		}

	case "/compact":
		if a.localOrch == nil {
			a.printLine("ag3nts", "Local LLM not configured.")
			return
		}
		result, err := a.localOrch.Compact(context.Background())
		if err != nil {
			a.printError("ag3nts", fmt.Sprintf("Compact failed: %v", err))
			return
		}
		saved := result.BeforeTokens - result.AfterTokens
		removed := result.BeforeMessages - result.AfterMessages
		a.printLine("ag3nts", fmt.Sprintf(
			"Context compacted: %d → %d tokens (saved %d). %d messages summarized into 1.",
			result.BeforeTokens, result.AfterTokens, saved, removed))

	case "/export":
		if a.localOrch == nil {
			a.printLine("ag3nts", "Local LLM not configured.")
			return
		}
		export := a.localOrch.ExportConversation()
		filename := filepath.Join("state", fmt.Sprintf("export-%s.txt", time.Now().Format("20060102-150405")))
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			a.printError("ag3nts", fmt.Sprintf("Export failed: %v", err))
			return
		}
		if err := os.WriteFile(filename, []byte(export), 0644); err != nil {
			a.printError("ag3nts", fmt.Sprintf("Export failed: %v", err))
			return
		}
		a.printLine("ag3nts", fmt.Sprintf("Session exported to %s", filename))

	case "/memory":
		if a.localOrch == nil {
			a.printLine("ag3nts", "Local LLM not configured.")
			return
		}
		a.printLines("ag3nts", a.localOrch.MemoryDump())

	case "/agents":
		var lines []string
		for _, ag := range a.orch.Agents().List() {
			avail := "ready"
			if !ag.Available() {
				avail = "unavailable"
			}
			lines = append(lines, fmt.Sprintf("  %s %s — %s", statusIcon(avail), ag.Name(), avail))
		}
		a.printLines("ag3nts", "Agents:\n"+strings.Join(lines, "\n"))

	case "/tasks", "/task":
		// /task <id>           — show full details for one task
		// /task list / /task   — compact list of current-session tasks
		// /task list --all     — include tasks from prior sessions
		var sub string
		var taskID string
		showAll := false
		if len(parts) > 1 {
			arg := parts[1]
			switch arg {
			case "list":
				sub = "list"
				for _, p := range parts[2:] {
					if p == "--all" {
						showAll = true
					}
				}
			case "--all":
				sub = "list"
				showAll = true
			default:
				// Single positional arg → treat as task ID for details.
				sub = "show"
				taskID = arg
			}
		} else {
			sub = "list"
		}

		if sub == "show" {
			t := a.orch.Tasks().Get(taskID)
			if t == nil {
				a.printError("ag3nts", fmt.Sprintf("task %q not found", taskID))
				return
			}
			a.printTaskDetails(t)
			return
		}

		// Compact list view.
		all := a.orch.Tasks().List()
		// Filter to current session unless --all requested. Tasks
		// added before SessionID was wired (legacy) have empty
		// SessionID and only show with --all.
		var visible []*task.Task
		for _, t := range all {
			if showAll || t.SessionID == a.orch.SessionID() {
				visible = append(visible, t)
			}
		}
		if len(visible) == 0 {
			if showAll {
				a.printLine("ag3nts", "No tasks.")
			} else {
				a.printLine("ag3nts", "No tasks in this session. Use /task list --all for cross-session view.")
			}
			return
		}
		var lines []string
		for _, t := range visible {
			desc := firstLine(t.Description)
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			lines = append(lines, fmt.Sprintf("  %s %s [%s] %s",
				taskIcon(t.Status.String()), t.ID, t.Type, desc))
		}
		header := fmt.Sprintf("Tasks (%d in session, /task <id> for details):", len(visible))
		if showAll {
			header = fmt.Sprintf("Tasks (%d total across sessions):", len(visible))
		}
		a.printLines("ag3nts", header+"\n"+strings.Join(lines, "\n"))

	case "/status":
		counts := a.orch.Tasks().Count()
		a.printLine("ag3nts", fmt.Sprintf(
			"primary=%s | agents=%d | pending=%d | completed=%d | failed=%d",
			a.orch.Primary(), a.orch.Agents().Count(),
			counts[task.StatusPending], counts[task.StatusCompleted], counts[task.StatusFailed]))

	case "/reload":
		a.handleReload(ctx)

	case "/cost":
		lines := []string{"Session Duration: " + formatDuration(time.Since(a.sessionStart))}
		if a.localOrch != nil {
			lines = append(lines, "Total Messages: "+strconv.Itoa(a.localOrch.ConversationLen()))
		}
		lines = append(lines, "Total Tokens: ↑"+formatTokens(atomic.LoadInt64(&a.totalTokenIn))+" ↓"+formatTokens(atomic.LoadInt64(&a.totalTokenOut)))

		a.agentTokensMu.RLock()
		agentNames := make([]string, 0, len(a.agentTokens))
		for name := range a.agentTokens {
			agentNames = append(agentNames, name)
		}
		sort.Strings(agentNames)
		for _, name := range agentNames {
			tokens := a.agentTokens[name]
			costUSD := float64(tokens[2]) / 1_000_000
			if costUSD > 0 {
				lines = append(lines, fmt.Sprintf("  %s: ↑%s ↓%s ($%.4f)", name, formatTokens(tokens[0]), formatTokens(tokens[1]), costUSD))
			} else {
				lines = append(lines, fmt.Sprintf("  %s: ↑%s ↓%s", name, formatTokens(tokens[0]), formatTokens(tokens[1])))
			}
		}
		totalCost := a.totalCostUSD
		a.agentTokensMu.RUnlock()

		if totalCost > 0 {
			lines = append(lines, fmt.Sprintf("Session Cost: $%.4f", totalCost))
		}

		a.printLines("ag3nts", strings.Join(lines, "\n"))

	case "/recipe":
		recipeArgs := ""
		if len(parts) > 1 {
			recipeArgs = strings.Join(parts[1:], " ")
		}
		a.handleRecipe(ctx, recipeArgs)

	case "/schedule":
		a.handleSchedule()

	case "/m3m0ry":
		m3Args := ""
		if len(parts) > 1 {
			m3Args = strings.Join(parts[1:], " ")
		}
		a.handleM3m0ry(m3Args)

	default:
		a.printError("error", fmt.Sprintf("Unknown: %s (try /help)", cmd))
	}
}

// handleM3m0ry exposes rolling context operations.
// Usage:
//
//	/m3m0ry stats
//	/m3m0ry search <query>
//	/m3m0ry tail [n]
func (a *App) handleM3m0ry(args string) {
	rs := a.orch.RollingContext()
	if rs == nil {
		a.printLine("ag3nts", "m3m0ry is not enabled")
		return
	}

	subParts := strings.Fields(args)
	if len(subParts) == 0 {
		a.printLine("ag3nts", "Usage: /m3m0ry stats | search <query> | tail [n]")
		return
	}

	sub := subParts[0]
	rest := strings.TrimSpace(strings.TrimPrefix(args, sub))

	switch sub {
	case "stats":
		stats, err := rs.Stats()
		if err != nil {
			a.printError("m3m0ry", err.Error())
			return
		}
		lines := []string{
			fmt.Sprintf("Total tokens: %d", stats.TotalTokens),
			fmt.Sprintf("Chunk count:  %d", stats.ChunkCount),
			fmt.Sprintf("Max seq:      %d", stats.MaxSeq),
			fmt.Sprintf("JSONL path:   %s", stats.JSONLPath),
			fmt.Sprintf("JSONL size:   %s", formatBytes(stats.JSONLBytes)),
		}
		a.printLines("ag3nts", strings.Join(lines, "\n"))

	case "search":
		if rest == "" {
			a.printLine("ag3nts", "Usage: /m3m0ry search <query>")
			return
		}
		chunks, err := rs.Retrieve(rest, time.Now())
		if err != nil {
			a.printError("m3m0ry", err.Error())
			return
		}
		if len(chunks) == 0 {
			a.printLine("ag3nts", "no matches")
			return
		}
		var out []string
		for i, c := range chunks {
			if i >= 10 {
				break
			}
			preview := c.Content
			if len(preview) > 200 {
				preview = preview[:200] + "..."
			}
			tag := c.TaskID
			if tag == "" {
				tag = c.Kind
			}
			out = append(out, fmt.Sprintf("  [%s %s] %s", tag, c.Agent, preview))
		}
		a.printLines("ag3nts", "Matches:\n"+strings.Join(out, "\n"))

	case "tail":
		n := 10
		if rest != "" {
			if v, err := strconv.Atoi(rest); err == nil && v > 0 {
				n = v
			}
		}
		db := a.orch.StoreDB()
		if db == nil {
			a.printLine("ag3nts", "SQLite not available")
			return
		}
		// Use the orchestrator's session ID indirectly via recent rows.
		stats, _ := rs.Stats()
		startSeq := stats.MaxSeq - int64(n)
		if startSeq < 0 {
			startSeq = 0
		}
		// We don't have a direct API to list by session from here —
		// use the Retrieve with empty query (recency-only) to get recent items.
		chunks, _ := rs.Retrieve("", time.Now())
		if len(chunks) > n {
			chunks = chunks[:n]
		}
		if len(chunks) == 0 {
			a.printLine("ag3nts", "m3m0ry is empty")
			return
		}
		var lines []string
		for _, c := range chunks {
			ts := c.CreatedAt.Format("15:04:05")
			preview := c.Content
			if len(preview) > 120 {
				preview = preview[:120] + "..."
			}
			lines = append(lines, fmt.Sprintf("  %s [%s] %s", ts, c.Kind, preview))
		}
		a.printLines("ag3nts", "Recent chunks:\n"+strings.Join(lines, "\n"))

	default:
		a.printLine("ag3nts", "Usage: /m3m0ry stats | search <query> | tail [n]")
	}
}

// formatBytes returns a human-readable byte size.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// handleRecipe lists or runs a recipe.
// Usage: /recipe (list all) or /recipe <name> [key=val ...]
func (a *App) handleRecipe(ctx context.Context, args string) {
	// Build recipe loader from config paths.
	var searchPaths []string
	a.cfgMu.RLock()
	currentCfg := a.currentCfg
	a.cfgMu.RUnlock()

	configDir := filepath.Dir(a.configPath)
	searchPaths = append(searchPaths, filepath.Join(configDir, "recipes"))
	if currentCfg != nil && currentCfg.Workflows.Active != "" {
		searchPaths = append(searchPaths, filepath.Join(configDir, "workflows", currentCfg.Workflows.Active, "recipes"))
	}
	loader := recipe.NewLoader(searchPaths...)

	if args == "" {
		// List recipes.
		recipes := loader.List()
		if len(recipes) == 0 {
			a.printLine("ag3nts", "No recipes found. Add .yaml files to config/recipes/")
			return
		}
		var lines []string
		for _, r := range recipes {
			agentStr := r.Agent
			if agentStr == "" {
				agentStr = "any"
			}
			lines = append(lines, fmt.Sprintf("  %-18s %-8s %s", r.Name, agentStr, r.Description))
		}
		a.printLines("ag3nts", "Recipes:\n"+strings.Join(lines, "\n"))
		return
	}

	// Parse: /recipe <name> [key=val ...]
	// Uses recipe.ParseInlineArgs which handles multi-word values so
	// "/recipe research query=what is MCP protocol" correctly yields
	// query="what is MCP protocol" instead of query="what".
	name, params := recipe.ParseInlineArgs(args)
	if name == "" {
		a.printLine("ag3nts", "Usage: /recipe <name> [key=val ...]")
		return
	}

	r, err := loader.Get(name)
	if err != nil {
		a.printError("recipe", err.Error())
		return
	}
	if err := r.Validate(); err != nil {
		a.printError("recipe", err.Error())
		return
	}

	// Dispatch via orchestrator.RunRecipe — handles single + multi-task
	// recipes uniformly, expands DAG, adds all tasks to the queue, and
	// sets up the evaluator loop if present.
	runID, err := a.orch.RunRecipe(r, params)
	if err != nil {
		a.printError("recipe", fmt.Sprintf("dispatch failed: %v", err))
		return
	}

	if r.IsMultiTask() {
		a.printLine("ag3nts", fmt.Sprintf("Recipe %q dispatched (run=%s, %d sub-tasks)", r.Name, runID, len(r.Tasks)))
	} else {
		a.printLine("ag3nts", fmt.Sprintf("Recipe %q dispatched as task %s → %s", r.Name, runID, r.Agent))
	}
}

// handleSchedule lists background schedules.
func (a *App) handleSchedule() {
	db := a.orch.StoreDB()
	if db == nil {
		a.printLine("ag3nts", "Schedules require SQLite (not available).")
		return
	}

	schedules, err := db.ListSchedules()
	if err != nil {
		a.printError("schedule", err.Error())
		return
	}
	if len(schedules) == 0 {
		a.printLine("ag3nts", "No schedules. Use 'ag3nts schedule add' to create one.")
		return
	}

	var lines []string
	for _, s := range schedules {
		enabled := "on"
		if !s.Enabled {
			enabled = "off"
		}
		lastRun := "never"
		if !s.LastRun.IsZero() {
			lastRun = s.LastRun.Format("01-02 15:04")
		}
		nextRun := "—"
		if !s.NextRun.IsZero() {
			nextRun = s.NextRun.Format("01-02 15:04")
		}
		lines = append(lines, fmt.Sprintf("  %-16s %-4s %-15s recipe=%-12s last=%s next=%s",
			s.ID, enabled, s.Cron, s.Recipe, lastRun, nextRun))
	}
	a.printLines("ag3nts", "Schedules:\n"+strings.Join(lines, "\n"))
}

// --- Event handling ---

func (a *App) eventLoop(ctx context.Context) {
	heartbeat := time.NewTicker(2 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-a.eventCh:
			if !ok {
				return
			}
			a.handleEvent(event)
		case req := <-a.permCh:
			a.handlePermission(req)
		case <-heartbeat.C:
			// Restart spinner if orchestrator is working but spinner died.
			if a.localOrch != nil && a.localOrch.IsRunning() {
				a.spinMu.Lock()
				running := a.spinning
				a.spinMu.Unlock()
				if !running {
					a.startSpinner(a.headModel() + " working...")
				}
			}
		}
	}
}

// handlePermission asks the user to approve or deny a tool action.
// Offers Claude Code-style options: allow once, always allow, or deny.
func (a *App) handlePermission(req PermissionRequest) {
	// Auto-approve if this tool was previously set to "always allow".
	if a.allowedTools[req.Tool] {
		a.println(dimStyle.Render(fmt.Sprintf("  ✓ Auto-approved: %s", req.Tool)))
		req.Reply <- true
		return
	}

	a.stopSpinner()

	promptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD54F")).Bold(true)
	greenStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784"))
	a.println("")
	a.println(promptStyle.Render("⚠ Permission required"))
	a.println(fmt.Sprintf("  Tool:   %s", req.Tool))
	a.println(fmt.Sprintf("  Action: %s", req.Action))
	a.println(dimStyle.Render("  1) Allow once"))
	a.println(dimStyle.Render("  2) Always allow " + req.Tool))
	a.println(dimStyle.Render("  3) Deny"))
	fmt.Print(dimStyle.Render("  Choice [1/2/3]: "))

	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(response)

	switch response {
	case "1", "y", "yes":
		a.println(greenStyle.Render("  ✓ Approved"))
		req.Reply <- true
	case "2":
		a.allowedTools[req.Tool] = true
		a.println(greenStyle.Render("  ✓ Always allowed: " + req.Tool))
		req.Reply <- true
	default:
		a.println(errorStyle.Render("  ✘ Denied"))
		req.Reply <- false
	}
	a.println("")
	a.startSpinner(a.headModel() + " processing...")
}

// GetPermissionFunc returns a function that tools can call to request permission.
func (a *App) GetPermissionFunc() func(tool, action string) bool {
	return func(tool, action string) bool {
		reply := make(chan bool, 1)
		a.permCh <- PermissionRequest{
			Tool:   tool,
			Action: action,
			Reply:  reply,
		}
		return <-reply
	}
}

func (a *App) handleEvent(event bus.Event) {
	agentEvt, ok := event.Payload.(agent.AgentEvent)
	if !ok {
		return
	}

	// Recipe pipeline tracking — update inline banner and sticky
	// status when a repair.* task transitions states. Runs before
	// the main switch so banner rendering isn't gated on which
	// branch handles the event.
	if agentEvt.TaskID != "" {
		a.maybeUpdatePipeline(agentEvt)
	}

	switch agentEvt.Kind {
	case agent.EventMessage:
		if agentEvt.Content == "" {
			// Empty message = flush signal.
			a.flushStream(agentEvt.Agent)
		} else {
			a.stopSpinner()
			a.appendAndFlushParagraphs(agentEvt.Agent, agentEvt.Content)
		}

	case agent.EventProgress:
		if agentEvt.Content != "" {
			if strings.HasSuffix(agentEvt.Agent, "[diff]") {
				a.stopSpinner()
				a.printDiff(agentEvt.Content)
				a.startSpinner(a.headModel() + " processing...")
			} else if strings.HasSuffix(agentEvt.Agent, "[result]") {
				a.stopSpinner()
				baseAgent := strings.TrimSuffix(agentEvt.Agent, "[result]")
				toolSpec := a.lastTool[baseAgent]
				content := agentEvt.Content
				if toolName, body, ok := splitToolResultPreview(agentEvt.Content); ok {
					content = body
					if toolSpec == "" {
						toolSpec = toolName
					} else if seenName, _ := splitToolDescriptor(toolSpec); seenName != toolName {
						toolSpec = toolName
					}
				}
				formatted := a.formatToolResult(toolSpec, content)
				a.println(formatted)
				a.startSpinner(a.headModel() + " processing...")
			} else {
				// Streaming delta from the local LLM. Do NOT stop the spinner
				// here — let it keep running with a live token counter so the
				// user can see work is happening. appendAndFlushLines handles
				// spinner stop/start only when a complete line is ready to
				// render, so the spinner survives across streaming chunks.
				a.appendAndFlushLines(agentEvt.Agent, agentEvt.Content)
			}
		}

	case agent.EventToolUse:
		a.flushStream(agentEvt.Agent)
		a.lastTool[agentEvt.Agent] = agentEvt.Content
		a.printLine(agentEvt.Agent, formatToolLine(agentEvt.Content))
		// Contextual spinner based on tool type.
		switch {
		case strings.Contains(agentEvt.Content, "web_research"):
			a.startSpinner("researching (gemini)...")
		case strings.Contains(agentEvt.Content, "code_task"):
			a.startSpinner("coding (claude)...")
		case strings.Contains(agentEvt.Content, "implement"):
			a.startSpinner("implementing (codex)...")
		case strings.Contains(agentEvt.Content, "recall"):
			a.startSpinner("searching memory...")
		case strings.Contains(agentEvt.Content, "store"):
			a.startSpinner("storing to memory...")
		case strings.Contains(agentEvt.Content, "read_file"):
			a.startSpinner("reading file...")
		case strings.Contains(agentEvt.Content, "run_command"):
			a.startSpinner("running command...")
		default:
			a.startSpinner("working...")
		}

	case agent.EventReasoning:
		a.startSpinner("reasoning...")

	case agent.EventError:
		a.flushStream(agentEvt.Agent)
		a.printErrors(agentEvt.Agent, agentEvt.Content)

	case agent.EventToolResult:
		a.stopSpinner()
		toolSpec := a.lastTool[agentEvt.Agent]
		content := agentEvt.Content
		if toolName, body, ok := splitToolResultPreview(agentEvt.Content); ok {
			content = body
			if toolSpec == "" {
				toolSpec = toolName
			} else if seenName, _ := splitToolDescriptor(toolSpec); seenName != toolName {
				toolSpec = toolName
			}
		}
		if toolSpec == "" {
			toolSpec = agentEvt.Agent
		}
		formatted := a.formatToolResult(toolSpec, content)
		a.println(formatted)
		a.startSpinner(a.headModel() + " processing...")

	case agent.EventInit:
		a.updateTitle()
		a.printLine("ag3nts", fmt.Sprintf("[%s] connected", agentEvt.Agent))
		a.startSpinner("waiting for " + agentEvt.Agent + "...")

	case agent.EventComplete:
		a.updateTitle()
		a.flushStream(agentEvt.Agent)
		if agentEvt.Usage != nil {
			in := int64(agentEvt.Usage.InputTokens)
			out := int64(agentEvt.Usage.OutputTokens)
			atomic.AddInt64(&a.totalTokenIn, in)
			atomic.AddInt64(&a.totalTokenOut, out)
			costMicro := int64(agentEvt.Usage.TotalCost * 1_000_000) // store as microdollars for atomic int ops
			a.agentTokensMu.Lock()
			tokens := a.agentTokens[agentEvt.Agent]
			tokens[0] += in
			tokens[1] += out
			tokens[2] += costMicro
			a.agentTokens[agentEvt.Agent] = tokens
			a.totalCostUSD += agentEvt.Usage.TotalCost
			a.agentTokensMu.Unlock()
			cost := ""
			if agentEvt.Usage.TotalCost > 0 {
				cost = fmt.Sprintf(" | $%.4f", agentEvt.Usage.TotalCost)
			}
			a.printLine("ag3nts", fmt.Sprintf("[%s] done — %d in / %d out tokens%s",
				agentEvt.Agent, agentEvt.Usage.InputTokens, agentEvt.Usage.OutputTokens, cost))
		}
		// If the orchestrator is still running, show what's happening next.
		if a.localOrch != nil && a.localOrch.IsRunning() {
			head := a.headModel()
			if agentEvt.Agent != head {
				a.startSpinner(head + " synthesizing " + agentEvt.Agent + " results...")
			} else {
				a.startSpinner(head + " processing...")
			}
		}
	}
}

// formatToolResult styles tool output using tool-specific formatting.
func (a *App) formatToolResult(toolName, content string) string {
	name, details := splitToolDescriptor(toolName)

	switch name {
	case "read_file":
		path := details
		if path == "" {
			path = "file"
		}
		lines := strings.Split(content, "\n")
		preview := content
		footer := ""
		if len(lines) > 15 {
			preview = strings.Join(lines[:15], "\n")
			footer = "\n" + dimStyle.Render(fmt.Sprintf("... %d more lines", len(lines)-15))
		}
		return "📄 " + path + "\n" + wrapCodeFence(codeFenceLanguage(path), preview) + footer
	case "run_command":
		cmd := details
		if cmd == "" {
			cmd = "command"
		}
		return "$ " + cmd + "\n" + wrapCodeFence("bash", content)
	case "search_files":
		return wrapCodeFence("", content)
	case "web_research", "code_task", "implement":
		return renderMarkdown(content)
	default:
		return content
	}
}

func splitToolDescriptor(value string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", ""
	}
	if i := strings.Index(value, ": "); i >= 0 {
		return strings.TrimSpace(value[:i]), strings.TrimSpace(value[i+2:])
	}
	return value, ""
}

func splitToolResultPreview(content string) (string, string, bool) {
	name, body := splitToolDescriptor(content)
	if name == "" || body == "" {
		return "", "", false
	}
	if _, ok := toolIcons[name]; !ok {
		return "", "", false
	}
	return name, body, true
}

func wrapCodeFence(lang, content string) string {
	body := strings.TrimRight(content, "\n")
	if lang == "" {
		return "```\n" + body + "\n```"
	}
	return "```" + lang + "\n" + body + "\n```"
}

func codeFenceLanguage(path string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch ext {
	case "":
		return ""
	case "md":
		return "markdown"
	case "yml":
		return "yaml"
	case "sh", "bash", "zsh":
		return "bash"
	default:
		return ext
	}
}

// appendAndFlushParagraphs adds text to the stream buffer and flushes
// completed paragraphs (separated by double newline) through glamour.
// This gives progressive rendering — paragraph by paragraph, fully styled.
func (a *App) appendAndFlushParagraphs(agentName, content string) {
	a.stream.Append(agentName, content)
	a.addTokens(len(content))

	// Check if the buffer contains a paragraph break (double newline).
	buf := a.stream.Peek(agentName)
	for {
		idx := strings.Index(buf, "\n\n")
		if idx < 0 {
			break
		}
		// Render the paragraph up to the break.
		paragraph := buf[:idx]
		buf = buf[idx+2:]

		if strings.TrimSpace(paragraph) != "" {
			rendered := renderMarkdown(paragraph)
			if rendered != "" {
				a.println(rendered)
			}
		}
	}
	// Keep the remainder in the buffer.
	a.stream.Set(agentName, buf)
}

// appendAndFlushLines is the streaming variant: it flushes on every
// single newline (not just double), so multi-line responses render
// line-by-line as they arrive. Preserves the spinner across chunks
// and only stops/restarts it when a line is actually being printed.
// Used for local LLM streaming deltas.
func (a *App) appendAndFlushLines(agentName, content string) {
	a.stream.Append(agentName, content)
	a.addTokens(len(content))

	buf := a.stream.Peek(agentName)
	// Fast path: nothing to flush yet. Don't touch the spinner — the
	// live token counter in its status line shows streaming progress.
	if !strings.Contains(buf, "\n") {
		a.stream.Set(agentName, buf)
		return
	}

	// A line boundary exists — we're about to print. Capture the
	// current spinner label AND the live counters so we can restart
	// without wiping the in-flight token count.
	a.spinMu.Lock()
	spinnerLabel := a.spinLabel
	a.spinMu.Unlock()
	savedIn := atomic.LoadInt64(&a.tokenIn)
	savedOut := atomic.LoadInt64(&a.tokenOut)

	a.stopSpinner()

	for {
		idx := strings.Index(buf, "\n")
		if idx < 0 {
			break
		}
		line := buf[:idx]
		buf = buf[idx+1:]

		if strings.TrimSpace(line) != "" {
			rendered := renderMarkdown(line)
			if rendered != "" {
				a.println(rendered)
			}
		} else {
			// Blank line — preserve vertical spacing.
			a.println("")
		}
	}
	a.stream.Set(agentName, buf)

	// Restart the spinner and restore the counters so the live ↓ tokens
	// display continues from where it was, not from zero.
	if spinnerLabel != "" {
		a.startSpinner(spinnerLabel)
		atomic.StoreInt64(&a.tokenIn, savedIn)
		atomic.StoreInt64(&a.tokenOut, savedOut)
	}
}

// flushStream renders any remaining buffered text and clears the buffer.
func (a *App) flushStream(agentName string) {
	text := a.stream.Flush(agentName)
	if text == "" {
		return
	}
	rendered := renderMarkdown(text)
	if rendered != "" {
		a.println(rendered)
	}
}

func (a *App) flushAgent(agentName string) {
	text := a.stream.Flush(agentName)
	if text == "" {
		return
	}

	a.stopSpinner()

	rendered := renderMarkdown(text)
	if rendered == "" {
		return
	}

	ts := dimStyle.Render(time.Now().Format("15:04:05"))
	label := lipgloss.NewStyle().Foreground(agentColor(agentName)).Bold(true).Render(agentName)
	a.println("")
	a.println(ts + " " + label)
	for _, line := range strings.Split(rendered, "\n") {
		a.println(line)
	}
	a.println("")
}

// --- Routing keywords ---

var researchKeywords = []string{
	"what", "who", "when", "where", "which", "how", "why",
	"search", "research", "find", "look up", "lookup", "look at",
	"check", "explore", "investigate", "examine", "inspect",
	"tell me", "show me", "list", "give me", "can you",
	"explain", "define", "meaning of", "describe",
	"compare", "difference between", "versus", "vs ",
	"latest", "news", "current", "recent", "today", "update",
}

var actionKeywords = []string{
	"fix", "implement", "create", "write", "edit", "modify", "change",
	"update", "add", "remove", "delete", "refactor", "build", "deploy",
	"commit", "push", "merge", "install", "run", "test", "debug",
}

func isResearchQuery(input string) bool {
	lower := strings.ToLower(input)
	for _, kw := range actionKeywords {
		if strings.Contains(lower, kw) {
			return false
		}
	}
	for _, kw := range researchKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
