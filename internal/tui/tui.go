// Package tui implements a readline-based terminal interface for ag3nts.
// Simple and stable: readline for input, fmt.Println for output,
// status line printed after each response. No scroll regions or TUI tricks.
package tui

import (
	"context"
	"fmt"
	"io"
	"os"
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

// App is the main terminal application.
type App struct {
	orch      *orchestrator.Orchestrator
	localOrch *llm.LocalOrchestrator
	eventCh   <-chan bus.Event
	stream    *streamBuffer
	rl        *readline.Instance
	mu        sync.Mutex
	active    string

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
}

func New(orch *orchestrator.Orchestrator, localOrch *llm.LocalOrchestrator) *App {
	return &App{
		orch:      orch,
		localOrch: localOrch,
		eventCh:   orch.Bus().Subscribe(512, "system"),
		stream:    newStreamBuffer(),
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
	})
	if err != nil {
		return fmt.Errorf("readline init: %w", err)
	}
	defer rl.Close()
	a.rl = rl

	go a.eventLoop(ctx)
	a.updateTitle()

	for {
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

func (a *App) printLines(source, content string) {
	for _, line := range strings.Split(content, "\n") {
		a.printLine(source, line)
	}
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
			a.printLine("error", err.Error())
			return
		}
		a.waitForCompletion()
		time.Sleep(200 * time.Millisecond)
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
				a.printLine("error", err.Error())
			}
			return
		}
	}

	if isResearchQuery(input) && a.orch.Agents().Get("gemini") != nil {
		a.printLine("ag3nts", "researching: "+input)
		if err := a.orch.Research(input); err != nil {
			a.printLine("error", err.Error())
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
			a.printLine("error", err.Error())
		}
	} else {
		if err := a.orch.SendTo(target, input); err != nil {
			a.printLine("error", err.Error())
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
			"  /agents   — list agents",
			"  /tasks    — list tasks",
			"  /status   — show overview",
			"  /quit     — exit",
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

	default:
		a.printLine("error", fmt.Sprintf("Unknown: %s (try /help)", cmd))
	}
}

// --- Event handling ---

func (a *App) eventLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-a.eventCh:
			if !ok {
				return
			}
			a.handleEvent(event)
		}
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
			// Empty message = flush signal from agent loop.
			a.flushAgent(agentEvt.Agent)
		} else {
			a.stream.Append(agentEvt.Agent, agentEvt.Content)
			a.addTokens(len(agentEvt.Content))
		}

	case agent.EventProgress:
		if agentEvt.Content != "" {
			if strings.HasSuffix(agentEvt.Agent, "[result]") {
				a.printLine(agentEvt.Agent, dimStyle.Render(agentEvt.Content))
			} else {
				a.stream.Append(agentEvt.Agent, agentEvt.Content)
				a.addTokens(len(agentEvt.Content))
			}
		}

	case agent.EventToolUse:
		a.flushAgent(agentEvt.Agent)
		a.printLine(agentEvt.Agent, formatToolLine(agentEvt.Content))
		a.startSpinner("working...")

	case agent.EventReasoning:
		a.startSpinner("thinking...")

	case agent.EventError:
		a.flushAgent(agentEvt.Agent)
		a.printLines(agentEvt.Agent+"[err]", agentEvt.Content)

	case agent.EventToolResult:
		// suppress

	case agent.EventInit:
		a.updateTitle()
		a.printLine("ag3nts", fmt.Sprintf("[%s] connected", agentEvt.Agent))
		a.startSpinner("waiting for response...")

	case agent.EventComplete:
		a.updateTitle()
		a.flushAgent(agentEvt.Agent)
		if agentEvt.Usage != nil {
			atomic.AddInt64(&a.totalTokenIn, int64(agentEvt.Usage.InputTokens))
			atomic.AddInt64(&a.totalTokenOut, int64(agentEvt.Usage.OutputTokens))
			cost := ""
			if agentEvt.Usage.TotalCost > 0 {
				cost = fmt.Sprintf(" | $%.4f", agentEvt.Usage.TotalCost)
			}
			a.printLine("ag3nts", fmt.Sprintf("[%s] done — %d in / %d out tokens%s",
				agentEvt.Agent, agentEvt.Usage.InputTokens, agentEvt.Usage.OutputTokens, cost))
		}
		if a.localOrch != nil && a.localOrch.IsRunning() && agentEvt.Agent != a.headModel() {
			a.startSpinner("synthesizing...")
		}
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
