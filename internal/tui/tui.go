// Package tui implements the terminal interface for the ag3nts orchestrator.
// Uses Bubbletea in inline mode — output goes to native terminal scrollback,
// only the input prompt and status bar are re-rendered in place.
package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
	"github.com/rohanrgit/ag3nts/internal/orchestrator"
	"github.com/rohanrgit/ag3nts/internal/task"
)

// Model is the root Bubbletea model for the orchestrator TUI.
type Model struct {
	orch *orchestrator.Orchestrator

	input       textarea.Model
	width       int
	ready       bool
	eventCh     <-chan bus.Event
	activeAgent string // persists routing to last-used agent; empty = primary
	lastErr     string
}

// New creates the TUI model wired to an orchestrator.
func New(orch *orchestrator.Orchestrator) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message or /help..."
	ta.ShowLineNumbers = false
	ta.DynamicHeight = true
	ta.MinHeight = 1
	ta.MaxHeight = maxInputLines
	ta.SetHeight(1)
	ta.Prompt = ""

	// Clear all default backgrounds so it uses the terminal's native background.
	clear := lipgloss.NewStyle()
	styles := ta.Styles()
	styles.Focused.Base = clear
	styles.Focused.Text = clear
	styles.Focused.CursorLine = clear
	styles.Focused.EndOfBuffer = clear
	styles.Focused.Placeholder = clear.Foreground(colorBorder)
	styles.Blurred.Base = clear
	styles.Blurred.Text = clear
	styles.Blurred.CursorLine = clear
	styles.Blurred.EndOfBuffer = clear
	styles.Blurred.Placeholder = clear.Foreground(colorBorder)
	ta.SetStyles(styles)

	ta.Focus()

	return Model{
		orch:    orch,
		input:   ta,
		eventCh: orch.Bus().Subscribe(512, "system"),
	}
}

// eventMsg wraps a bus event for delivery into Bubbletea's Update loop.
type eventMsg bus.Event

// Init starts listening for orchestrator events.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		m.waitForEvent(),
	)
}

// waitForEvent returns a Cmd that blocks until the next bus event arrives.
func (m Model) waitForEvent() tea.Cmd {
	return func() tea.Msg {
		event, ok := <-m.eventCh
		if !ok {
			return nil
		}
		return eventMsg(event)
	}
}

// dimStyle for timestamps.
var dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#616161"))

// printLine sends a timestamped styled line to the terminal's native scrollback.
func printLine(source, text string) tea.Cmd {
	ts := dimStyle.Render(time.Now().Format("15:04:05"))
	styled := ts + " " +
		lipgloss.NewStyle().Foreground(agentColor(source)).Render(source) +
		" " + text
	return tea.Println(styled)
}

// printLines sends multiple lines to scrollback.
func printLines(source, content string) tea.Cmd {
	var cmds []tea.Cmd
	for _, line := range strings.Split(content, "\n") {
		cmds = append(cmds, printLine(source, line))
	}
	return tea.Batch(cmds...)
}

// Update handles all messages.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.input.SetWidth(m.width - borderSize)
		m.ready = true
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "enter":
			value := strings.TrimSpace(m.input.Value())
			if value != "" {
				cmd := m.handleCommand(value)
				m.input.Reset()
				if cmd != nil {
					cmds = append(cmds, tea.Sequence(tea.Println(""), cmd))
				}
			}
			return m, tea.Batch(cmds...)
		}

		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	case eventMsg:
		cmd := m.handleEvent(bus.Event(msg))
		cmds = append(cmds, cmd, m.waitForEvent())
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

// View renders only the status bar and input prompt inline.
// All output goes to native scrollback via tea.Println.
func (m Model) View() tea.View {
	if !m.ready {
		return tea.NewView("Initializing ag3nts orchestrator...")
	}

	inputPanel := borderFocused.
		Width(m.width).
		Render(m.input.View())
	statusBar := m.renderStatusBar()

	content := lipgloss.JoinVertical(lipgloss.Left, inputPanel, statusBar)
	return tea.NewView(content)
}

// renderStatusBar creates the inline status line.
func (m Model) renderStatusBar() string {
	// Agents inline.
	var agentParts []string
	for _, a := range m.orch.Agents().List() {
		icon := statusIcon("idle")
		if !a.Available() {
			icon = statusIcon("failed")
		}
		name := a.Name()
		if name == m.orch.Primary() {
			name += "*"
		}
		agentParts = append(agentParts,
			lipgloss.NewStyle().Foreground(agentColor(a.Name())).
				Render(icon+" "+name))
	}
	agents := strings.Join(agentParts, "  ")

	tasks := m.orch.Tasks().List()
	running := m.orch.RunningCount()

	active := m.activeAgent
	if active == "" {
		active = m.orch.Primary()
	}
	status := fmt.Sprintf(" ag3nts | talking to: %s | %s | tasks: %d | running: %d",
		active, agents, len(tasks), running)

	return statusBarStyle.Width(m.width).Render(status)
}

// searchKeywords triggers auto-routing to Gemini for web search/research.
var searchKeywords = []string{
	"search", "look up", "lookup", "find out", "research",
	"what is", "what are", "who is", "who are", "when did", "when was",
	"how to", "how do", "how does", "why is", "why do", "why does",
	"latest", "news", "current", "recent", "today",
	"compare", "difference between", "versus", "vs ",
	"explain", "define", "meaning of",
}

// isSearchQuery returns true if the input looks like a web search request.
func isSearchQuery(input string) bool {
	lower := strings.ToLower(input)
	for _, kw := range searchKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// handleCommand processes user input, returns a Cmd for output.
// If the message starts with an agent name (e.g. "gemini research X"),
// it switches to that agent. Subsequent messages continue to the same
// agent until another agent name is used.
// Search/research queries are always routed to Gemini.
func (m *Model) handleCommand(input string) tea.Cmd {
	if strings.HasPrefix(input, "/") {
		return m.handleSlashCommand(input)
	}

	// Check if first word is a known agent name → switch active agent.
	parts := strings.SplitN(input, " ", 2)
	if len(parts) >= 1 {
		agentName := strings.ToLower(parts[0])
		if m.orch.Agents().Get(agentName) != nil {
			m.activeAgent = agentName
			message := ""
			if len(parts) == 2 {
				message = parts[1]
			}
			if message == "" {
				return printLine("system", fmt.Sprintf("Switched to %s. Messages now go to %s.", agentName, agentName))
			}
			cmds := []tea.Cmd{printLine("you→"+agentName, message)}
			if err := m.orch.SendTo(agentName, message); err != nil {
				cmds = append(cmds, printLine("error", err.Error()))
			}
			return tea.Batch(cmds...)
		}
	}

	// Auto-route search queries to Gemini (regardless of active agent).
	if isSearchQuery(input) && m.orch.Agents().Get("gemini") != nil {
		cmds := []tea.Cmd{printLine("you→gemini", input)}
		if err := m.orch.SendTo("gemini", input); err != nil {
			cmds = append(cmds, printLine("error", err.Error()))
		}
		return tea.Batch(cmds...)
	}

	// Send to active agent (or primary if none set).
	target := m.activeAgent
	if target == "" {
		target = m.orch.Primary()
	}

	if target == m.orch.Primary() {
		cmds := []tea.Cmd{printLine("you", input)}
		if err := m.orch.Send(input); err != nil {
			m.lastErr = err.Error()
			cmds = append(cmds, printLine("error", err.Error()))
		}
		return tea.Batch(cmds...)
	}

	cmds := []tea.Cmd{printLine("you→"+target, input)}
	if err := m.orch.SendTo(target, input); err != nil {
		cmds = append(cmds, printLine("error", err.Error()))
	}
	return tea.Batch(cmds...)
}

// handleSlashCommand parses and executes TUI commands.
func (m *Model) handleSlashCommand(input string) tea.Cmd {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/help":
		return printLines("system", strings.Join([]string{
			"Commands:",
			"  /to <agent> <msg>   — send directly to an agent",
			"  /cancel             — stop the active agent's session",
			"  /task <type> <desc> — create a routed task",
			"  /primary <agent>    — switch primary agent",
			"  /agents             — list agents",
			"  /tasks              — list tasks",
			"  /status             — show overview",
			"  /quit               — exit",
			"",
			"Type an agent name to switch (e.g. 'gemini hello').",
			"Sessions auto-timeout after 2 minutes.",
		}, "\n"))

	case "/quit":
		return tea.Quit

	case "/cancel":
		target := m.activeAgent
		if target == "" {
			target = m.orch.Primary()
		}
		if err := m.orch.Cancel(target); err != nil {
			return printLine("system", err.Error())
		}
		return printLine("system", fmt.Sprintf("Cancelled %s session.", target))

	case "/to":
		if len(parts) < 3 {
			return printLine("error", "Usage: /to <agent> <message>")
		}
		agentName := parts[1]
		message := strings.Join(parts[2:], " ")
		cmds := []tea.Cmd{printLine("you→"+agentName, message)}
		if err := m.orch.SendTo(agentName, message); err != nil {
			cmds = append(cmds, printLine("error", err.Error()))
		}
		return tea.Batch(cmds...)

	case "/task":
		if len(parts) < 3 {
			return printLine("error", "Usage: /task <type> <description>")
		}
		taskType := parts[1]
		desc := strings.Join(parts[2:], " ")
		t := &task.Task{
			Type:        taskType,
			Description: desc,
		}
		if err := m.orch.CreateTask(t); err != nil {
			return printLine("error", err.Error())
		}
		return printLine("system", fmt.Sprintf("Task created: [%s] %s", taskType, desc))

	case "/primary":
		if len(parts) < 2 {
			return printLine("error", "Usage: /primary <agent>")
		}
		if err := m.orch.SetPrimary(parts[1]); err != nil {
			return printLine("error", err.Error())
		}
		return printLine("system", fmt.Sprintf("Primary agent → %s", parts[1]))

	case "/agents":
		return m.showAgents()

	case "/tasks":
		return m.showTasks()

	case "/status":
		return m.showStatus()

	default:
		return printLine("error", fmt.Sprintf("Unknown command: %s (try /help)", cmd))
	}
}

// handleEvent processes a bus event and prints to scrollback.
func (m *Model) handleEvent(event bus.Event) tea.Cmd {
	agentEvt, ok := event.Payload.(agent.AgentEvent)
	if !ok {
		return nil
	}

	switch agentEvt.Kind {
	case agent.EventMessage:
		return printLines(agentEvt.Agent, agentEvt.Content)
	case agent.EventError:
		return printLines(agentEvt.Agent+"[err]", agentEvt.Content)
	case agent.EventCommand:
		return printLines(agentEvt.Agent+"[cmd]", agentEvt.Content)
	case agent.EventToolUse:
		return printLines(agentEvt.Agent+"[tool]", agentEvt.Content)
	case agent.EventToolResult:
		// Don't dump full tool output — just confirm completion.
		return nil
	case agent.EventReasoning:
		// Show a brief thinking indicator, not the full reasoning.
		return printLine(agentEvt.Agent+"[thinking]", "...")
	case agent.EventProgress:
		if agentEvt.Content != "" {
			return printLines(agentEvt.Agent, agentEvt.Content)
		}
	case agent.EventInit:
		return printLine("system", fmt.Sprintf("[%s] connected — waiting for response...", agentEvt.Agent))
	case agent.EventComplete:
		if agentEvt.Usage != nil {
			cost := ""
			if agentEvt.Usage.TotalCost > 0 {
				cost = fmt.Sprintf(" | $%.4f", agentEvt.Usage.TotalCost)
			}
			return printLine("system", fmt.Sprintf("[%s] done — %d in / %d out tokens%s",
				agentEvt.Agent, agentEvt.Usage.InputTokens, agentEvt.Usage.OutputTokens, cost))
		}
	}
	return nil
}

// showAgents displays agent status.
func (m *Model) showAgents() tea.Cmd {
	var lines []string
	lines = append(lines, "Agents:")
	for _, a := range m.orch.Agents().List() {
		avail := "unavailable"
		if a.Available() {
			avail = "ready"
		}
		primary := ""
		if a.Name() == m.orch.Primary() {
			primary = " (primary)"
		}
		lines = append(lines, fmt.Sprintf("  %s %s — %s%s",
			statusIcon(avail), a.Name(), avail, primary))
	}
	return printLines("system", strings.Join(lines, "\n"))
}

// showTasks displays task list.
func (m *Model) showTasks() tea.Cmd {
	tasks := m.orch.Tasks().List()
	if len(tasks) == 0 {
		return printLine("system", "No tasks.")
	}
	var lines []string
	lines = append(lines, "Tasks:")
	for _, t := range tasks {
		lines = append(lines, fmt.Sprintf("  %s %s [%s] — %s",
			taskIcon(t.Status.String()), t.ID, t.Type, t.Description))
	}
	return printLines("system", strings.Join(lines, "\n"))
}

// showStatus displays orchestrator overview.
func (m *Model) showStatus() tea.Cmd {
	counts := m.orch.Tasks().Count()
	return printLine("system", fmt.Sprintf(
		"Status: primary=%s | agents=%d | running=%d | pending=%d | completed=%d | failed=%d",
		m.orch.Primary(),
		m.orch.Agents().Count(),
		m.orch.RunningCount(),
		counts[task.StatusPending],
		counts[task.StatusCompleted],
		counts[task.StatusFailed],
	))
}
