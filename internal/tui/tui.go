// Package tui implements a plain terminal interface for the ag3nts orchestrator.
// No TUI framework — just stdin/stdout with goroutines for concurrent output.
package tui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
	"github.com/rohanrgit/ag3nts/internal/llm"
	"github.com/rohanrgit/ag3nts/internal/orchestrator"
	"github.com/rohanrgit/ag3nts/internal/task"
)

// dimStyle for timestamps.
var dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#616161"))

// App is the main terminal application. No Bubbletea — just goroutines.
type App struct {
	orch      *orchestrator.Orchestrator
	localOrch *llm.LocalOrchestrator
	eventCh   <-chan bus.Event
	stream    *streamBuffer
	mu        sync.Mutex // protects stdout writes
	active    string     // active agent for fallback routing
}

// New creates the terminal app.
func New(orch *orchestrator.Orchestrator, localOrch *llm.LocalOrchestrator) *App {
	return &App{
		orch:      orch,
		localOrch: localOrch,
		eventCh:   orch.Bus().Subscribe(512, "system"),
		stream:    newStreamBuffer(),
	}
}

// Run starts the terminal app. Blocks until the user quits.
func (a *App) Run(ctx context.Context) error {
	// Start event listener in background.
	go a.eventLoop(ctx)

	// Read input from stdin.
	scanner := bufio.NewScanner(os.Stdin)
	a.printPrompt()

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			a.printPrompt()
			continue
		}

		if line == "/quit" || line == "/exit" {
			a.shutdown()
			return nil
		}

		a.handleInput(ctx, line)
		a.printPrompt()
	}

	a.shutdown()
	return scanner.Err()
}

// shutdown unloads all models from VRAM on exit.
func (a *App) shutdown() {
	if a.localOrch != nil {
		fmt.Fprintln(os.Stderr, "Unloading models...")
		a.localOrch.Shutdown()
		fmt.Fprintln(os.Stderr, "Done.")
	}
}

// printPrompt writes the input prompt and updates the terminal title.
func (a *App) printPrompt() {
	a.updateTitle()
	fmt.Print("> ")
}

// updateTitle sets the terminal window title to show current status.
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
	// ANSI escape to set terminal title.
	fmt.Fprintf(os.Stderr, "\033]0;%s\007", status)
}

// println writes a line to stdout, thread-safe.
func (a *App) println(s string) {
	a.mu.Lock()
	fmt.Println(s)
	a.mu.Unlock()
}

// printLine writes a timestamped, colored line.
func (a *App) printLine(source, text string) {
	ts := dimStyle.Render(time.Now().Format("15:04:05"))
	styled := ts + " " +
		lipgloss.NewStyle().Foreground(agentColor(source)).Render(source) +
		" " + text
	a.println(styled)
}

// printLines writes multiple timestamped lines.
func (a *App) printLines(source, content string) {
	for _, line := range strings.Split(content, "\n") {
		a.printLine(source, line)
	}
}

// eventLoop listens for bus events and prints them.
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

// handleInput processes a line of user input.
func (a *App) handleInput(ctx context.Context, input string) {
	if strings.HasPrefix(input, "/") {
		a.handleSlash(ctx, input)
		return
	}

	// Local LLM orchestrator: send everything to Qwen 3.5.
	if a.localOrch != nil && a.localOrch.Available() {
		a.printLine("you", input)
		if err := a.localOrch.Send(ctx, input); err != nil {
			a.printLine("error", err.Error())
		}
		// Wait for the response to complete before showing next prompt.
		a.waitForCompletion()
		return
	}

	// Fallback: keyword-based routing.
	a.handleFallback(ctx, input)
}

// waitForCompletion blocks until the local orchestrator finishes processing.
func (a *App) waitForCompletion() {
	if a.localOrch == nil {
		return
	}
	for a.localOrch.IsRunning() {
		time.Sleep(100 * time.Millisecond)
	}
}

// handleFallback is the keyword-based routing for when Ollama is unavailable.
func (a *App) handleFallback(ctx context.Context, input string) {
	// Check for agent name prefix.
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
				a.printLine("system", fmt.Sprintf("Switched to %s.", agentName))
				return
			}
			a.printLine("you→"+agentName, message)
			if err := a.orch.SendTo(agentName, message); err != nil {
				a.printLine("error", err.Error())
			}
			return
		}
	}

	// Research routing.
	if isResearchQuery(input) && a.orch.Agents().Get("gemini") != nil {
		a.printLine("system", "researching: "+input)
		if err := a.orch.Research(input); err != nil {
			a.printLine("error", err.Error())
		}
		return
	}

	// Send to active or primary.
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

// handleSlash processes slash commands.
func (a *App) handleSlash(ctx context.Context, input string) {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/help":
		lines := []string{
			"Commands:",
			"  /cancel   — stop the active session",
			"  /agents   — list agents",
			"  /tasks    — list tasks",
			"  /status   — show overview",
			"  /quit     — exit",
		}
		if a.localOrch != nil {
			lines = append(lines, "",
				"Local LLM:",
				"  /local status  — show loaded models",
				"  /local reset   — clear conversation",
			)
		}
		a.printLines("system", strings.Join(lines, "\n"))

	case "/cancel":
		if a.localOrch != nil {
			if err := a.localOrch.Cancel(); err != nil {
				a.printLine("system", err.Error())
			} else {
				a.printLine("system", "Cancelled.")
			}
			return
		}
		target := a.active
		if target == "" {
			target = a.orch.Primary()
		}
		if err := a.orch.Cancel(target); err != nil {
			a.printLine("system", err.Error())
		} else {
			a.printLine("system", "Cancelled "+target+".")
		}

	case "/local":
		if a.localOrch == nil {
			a.printLine("system", "Local LLM not configured.")
			return
		}
		sub := ""
		if len(parts) > 1 {
			sub = parts[1]
		}
		switch sub {
		case "status":
			a.printLines("system", a.localOrch.ModelStatus(ctx))
		case "reset":
			a.localOrch.Reset()
			a.printLine("system", "Conversation cleared.")
		default:
			a.printLine("system", "Usage: /local status | /local reset")
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
		a.printLines("system", "Agents:\n"+strings.Join(lines, "\n"))

	case "/tasks":
		tasks := a.orch.Tasks().List()
		if len(tasks) == 0 {
			a.printLine("system", "No tasks.")
			return
		}
		var lines []string
		for _, t := range tasks {
			lines = append(lines, fmt.Sprintf("  %s %s [%s] — %s",
				taskIcon(t.Status.String()), t.ID, t.Type, t.Description))
		}
		a.printLines("system", "Tasks:\n"+strings.Join(lines, "\n"))

	case "/status":
		counts := a.orch.Tasks().Count()
		a.printLine("system", fmt.Sprintf(
			"primary=%s | agents=%d | pending=%d | completed=%d | failed=%d",
			a.orch.Primary(), a.orch.Agents().Count(),
			counts[task.StatusPending], counts[task.StatusCompleted], counts[task.StatusFailed]))

	default:
		a.printLine("error", fmt.Sprintf("Unknown: %s (try /help)", cmd))
	}
}

// handleEvent processes a single bus event.
func (a *App) handleEvent(event bus.Event) {
	agentEvt, ok := event.Payload.(agent.AgentEvent)
	if !ok {
		return
	}

	switch agentEvt.Kind {
	case agent.EventMessage:
		a.stream.Append(agentEvt.Agent, agentEvt.Content)

	case agent.EventProgress:
		if agentEvt.Content != "" {
			a.stream.Append(agentEvt.Agent, agentEvt.Content)
		}

	case agent.EventToolUse:
		a.flushAgent(agentEvt.Agent)
		a.printLine(agentEvt.Agent, formatToolLine(agentEvt.Content))

	case agent.EventReasoning:
		a.printLine(agentEvt.Agent, dimStyle.Render("thinking..."))

	case agent.EventError:
		a.flushAgent(agentEvt.Agent)
		a.printLines(agentEvt.Agent+"[err]", agentEvt.Content)

	case agent.EventToolResult:
		// suppress

	case agent.EventInit:
		a.updateTitle()
		a.printLine("system", fmt.Sprintf("[%s] connected", agentEvt.Agent))

	case agent.EventComplete:
		a.updateTitle()
		a.flushAgent(agentEvt.Agent)
		if agentEvt.Usage != nil {
			cost := ""
			if agentEvt.Usage.TotalCost > 0 {
				cost = fmt.Sprintf(" | $%.4f", agentEvt.Usage.TotalCost)
			}
			a.printLine("system", fmt.Sprintf("[%s] done — %d in / %d out tokens%s",
				agentEvt.Agent, agentEvt.Usage.InputTokens, agentEvt.Usage.OutputTokens, cost))
		}
		// If a secondary model completed but the orchestrator is still running,
		// show that the head model is now synthesizing.
		if a.localOrch != nil && a.localOrch.IsRunning() && agentEvt.Agent != "qwen3.5" {
			a.printLine("qwen3.5", dimStyle.Render("synthesizing..."))
		}
	}
}

// flushAgent renders buffered text as markdown and prints it.
func (a *App) flushAgent(agentName string) {
	text := a.stream.Flush(agentName)
	if text == "" {
		return
	}

	rendered := renderMarkdown(text)
	if rendered == "" {
		return
	}

	ts := dimStyle.Render(time.Now().Format("15:04:05"))
	label := lipgloss.NewStyle().Foreground(agentColor(agentName)).Render(agentName)
	a.println(ts + " " + label)
	for _, line := range strings.Split(rendered, "\n") {
		a.println(line)
	}
}

// Research keywords.
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
