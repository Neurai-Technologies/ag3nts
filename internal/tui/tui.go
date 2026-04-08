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
	"github.com/rohanrgit/ag3nts/internal/llm"
	"github.com/rohanrgit/ag3nts/internal/orchestrator"
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
	orch      *orchestrator.Orchestrator
	localOrch *llm.LocalOrchestrator
	eventCh   <-chan bus.Event
	stream    *streamBuffer
	rl        *readline.Instance
	mu        sync.Mutex
	active    string
	permCh    chan PermissionRequest // tools send permission requests here
	lastTool  map[string]string      // last tool use line by agent (for formatting tool results)

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
	agentTokens   map[string][2]int64
	agentTokensMu sync.RWMutex
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
		readline.PcItem("/cost"),
		readline.PcItem("/memory"),
		readline.PcItem("/local",
			readline.PcItem("status"),
			readline.PcItem("reset"),
		),
	)
}

func New(orch *orchestrator.Orchestrator, localOrch *llm.LocalOrchestrator) *App {
	return &App{
		orch:        orch,
		localOrch:   localOrch,
		eventCh:     orch.Bus().Subscribe(512, "system"),
		stream:      newStreamBuffer(),
		permCh:      make(chan PermissionRequest),
		lastTool:    make(map[string]string),
		agentTokens: make(map[string][2]int64),
	}
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

	for {
		fmt.Println(dimStyle.Render("─────────────────────────────────────────────────────────"))
		rl.SetPrompt("> ")
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				if a.localOrch != nil && a.localOrch.IsRunning() {
					_ = a.localOrch.Cancel()
					a.stopSpinner()
					fmt.Println(dimStyle.Render("  cancelled"))
					continue
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

		input := strings.TrimSpace(line)
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
	addStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784"))
	delStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#EF5350"))
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
	atomic.StoreInt64(&a.tokenIn, 0)
	atomic.StoreInt64(&a.tokenOut, 0)
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

func (a *App) handleSlash(_ context.Context, input string) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/help":
		lines := []string{
			"Commands:",
			"  /cancel   — cancel current operation (or Ctrl+C)",
			"  /compact  — compress conversation history to free context",
			"  /agents   — list agents",
			"  /tasks    — list tasks",
			"  /status   — show overview",
			"  /cost    — show session cost breakdown",
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

	case "/tasks":
		tasks := a.orch.Tasks().List()
		if len(tasks) == 0 {
			a.printLine("ag3nts", "No tasks.")
			return
		}
		var lines []string
		for _, t := range tasks {
			lines = append(lines, fmt.Sprintf("  %s %s [%s] — %s",
				taskIcon(t.Status.String()), t.ID, t.Type, t.Description))
		}
		a.printLines("ag3nts", "Tasks:\n"+strings.Join(lines, "\n"))

	case "/status":
		counts := a.orch.Tasks().Count()
		a.printLine("ag3nts", fmt.Sprintf(
			"primary=%s | agents=%d | pending=%d | completed=%d | failed=%d",
			a.orch.Primary(), a.orch.Agents().Count(),
			counts[task.StatusPending], counts[task.StatusCompleted], counts[task.StatusFailed]))

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
			lines = append(lines, fmt.Sprintf("%s: ↑%s ↓%s", name, formatTokens(tokens[0]), formatTokens(tokens[1])))
		}
		a.agentTokensMu.RUnlock()

		a.printLines("ag3nts", strings.Join(lines, "\n"))

	default:
		a.printError("error", fmt.Sprintf("Unknown: %s (try /help)", cmd))
	}
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
					a.startSpinner("working...")
				}
			}
		}
	}
}

// handlePermission asks the user to approve or deny a tool action.
func (a *App) handlePermission(req PermissionRequest) {
	a.stopSpinner()
	// Show the permission prompt.
	promptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD54F")).Bold(true)
	a.println("")
	a.println(promptStyle.Render("⚠ Permission required"))
	a.println(fmt.Sprintf("  Tool:   %s", req.Tool))
	a.println(fmt.Sprintf("  Action: %s", req.Action))
	fmt.Print(dimStyle.Render("  Allow? [y/n] "))

	// Read a single character response.
	var response string
	fmt.Scanln(&response)
	response = strings.ToLower(strings.TrimSpace(response))

	approved := response == "y" || response == "yes"
	if approved {
		a.println(lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784")).Render("  ✓ Approved"))
	} else {
		a.println(errorStyle.Render("  ✘ Denied"))
	}
	a.println("")
	a.startSpinner("processing...")

	req.Reply <- approved
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
				a.startSpinner("processing...")
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
				a.startSpinner("processing...")
			} else {
				a.stopSpinner()
				a.appendAndFlushParagraphs(agentEvt.Agent, agentEvt.Content)
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
		a.startSpinner("processing...")

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
			a.agentTokensMu.Lock()
			tokens := a.agentTokens[agentEvt.Agent]
			tokens[0] += in
			tokens[1] += out
			a.agentTokens[agentEvt.Agent] = tokens
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
			if agentEvt.Agent != a.headModel() {
				a.startSpinner("synthesizing results...")
			} else {
				a.startSpinner("processing...")
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
		return "📄 " + path + "\n" + wrapCodeFence(codeFenceLanguage(path), content)
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
