// Package tui implements the Bubbletea v2 terminal interface for the
// ag3nts orchestrator. It displays agent status, task progress, streaming
// output, and accepts user commands.
package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
	"github.com/rohanrgit/ag3nts/internal/orchestrator"
	"github.com/rohanrgit/ag3nts/internal/task"
)

// panel tracks which UI panel has focus.
type panel int

const (
	panelInput panel = iota
	panelOutput
	panelSidebar
)

// Model is the root Bubbletea model for the orchestrator TUI.
type Model struct {
	orch *orchestrator.Orchestrator

	// Sub-models.
	output viewport.Model
	input  textarea.Model

	// Layout state.
	layout      layoutDimensions
	activePanel panel
	ready       bool

	// Content buffers.
	outputLines []string // accumulated output lines
	eventCh     <-chan bus.Event

	// Error display.
	lastErr string
}

// New creates the TUI model wired to an orchestrator.
func New(orch *orchestrator.Orchestrator) Model {
	ta := textarea.New()
	ta.Placeholder = "Type a message or /help for commands..."
	ta.ShowLineNumbers = false
	ta.SetHeight(1)
	ta.Focus()

	return Model{
		orch:        orch,
		input:       ta,
		activePanel: panelInput,
		eventCh:     orch.Bus().Subscribe(512, "system"), // subscribe to system topic only (wildcard causes duplicates)
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

// Update handles all messages: key presses, window resize, bus events.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.layout = calcLayout(msg.Width, msg.Height)
		m.output = viewport.New(
			viewport.WithWidth(m.layout.outputWidth-borderSize),
			viewport.WithHeight(m.layout.outputHeight),
		)
		m.input.SetWidth(m.layout.inputWidth - borderSize)
		m.ready = true
		m.refreshOutput()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return m, tea.Quit

		case "tab":
			switch m.activePanel {
			case panelInput:
				m.activePanel = panelOutput
				m.input.Blur()
			case panelOutput:
				m.activePanel = panelSidebar
			case panelSidebar:
				m.activePanel = panelInput
				m.input.Focus()
			}
			return m, nil

		case "enter":
			if m.activePanel == panelInput {
				value := strings.TrimSpace(m.input.Value())
				if value != "" {
					cmd := m.handleCommand(value)
					m.input.Reset()
					if cmd != nil {
						cmds = append(cmds, cmd)
					}
				}
				return m, tea.Batch(cmds...)
			}
		}

		switch m.activePanel {
		case panelInput:
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			cmds = append(cmds, cmd)
		case panelOutput:
			var cmd tea.Cmd
			m.output, cmd = m.output.Update(msg)
			cmds = append(cmds, cmd)
		}

		return m, tea.Batch(cmds...)

	case eventMsg:
		m.handleEvent(bus.Event(msg))
		cmds = append(cmds, m.waitForEvent())
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

// View renders the complete TUI layout.
func (m Model) View() tea.View {
	if !m.ready {
		v := tea.NewView("Initializing ag3nts orchestrator...")
		v.AltScreen = true
		return v
	}

	// Status bar.
	statusBar := m.renderStatusBar()

	// Output viewport with border.
	outputBorder := borderNormal
	if m.activePanel == panelOutput {
		outputBorder = borderFocused
	}
	outputPanel := outputBorder.
		Width(m.layout.outputWidth - borderSize).
		Height(m.layout.outputHeight).
		Render(m.output.View())

	// Sidebar: agents + tasks.
	sidebarPanel := m.renderSidebar()

	// Main content: output | sidebar.
	mainContent := lipgloss.JoinHorizontal(lipgloss.Top, outputPanel, sidebarPanel)

	// Input area with border.
	inputBorder := borderNormal
	if m.activePanel == panelInput {
		inputBorder = borderFocused
	}
	inputPanel := inputBorder.
		Width(m.layout.inputWidth - borderSize).
		Render(m.input.View())

	// Stack vertically: status → main → input.
	content := lipgloss.JoinVertical(lipgloss.Left, statusBar, mainContent, inputPanel)

	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// renderStatusBar creates the top status line.
func (m Model) renderStatusBar() string {
	primary := m.orch.Primary()
	agentCount := m.orch.Agents().Count()
	running := m.orch.RunningCount()

	status := fmt.Sprintf(" ag3nts | primary: %s | agents: %d | running: %d",
		primary, agentCount, running)

	return statusBarStyle.Width(m.layout.statusWidth).Render(status)
}

// renderSidebar creates the right panel with agents and tasks.
func (m Model) renderSidebar() string {
	sidebarBorder := borderNormal
	if m.activePanel == panelSidebar {
		sidebarBorder = borderFocused
	}

	// Agent list.
	var agentLines []string
	agentLines = append(agentLines, lipgloss.NewStyle().Bold(true).Render("Agents"))
	for _, a := range m.orch.Agents().List() {
		icon := statusIcon("idle")
		if !a.Available() {
			icon = statusIcon("failed")
		}
		name := a.Name()
		if name == m.orch.Primary() {
			name += "*"
		}
		line := lipgloss.NewStyle().Foreground(agentColor(a.Name())).
			Render(fmt.Sprintf(" %s %s", icon, name))
		agentLines = append(agentLines, line)
	}

	// Task list.
	var taskLines []string
	taskLines = append(taskLines, lipgloss.NewStyle().Bold(true).Render("Tasks"))
	tasks := m.orch.Tasks().List()
	if len(tasks) == 0 {
		taskLines = append(taskLines, " (none)")
	}
	for _, t := range tasks {
		icon := taskIcon(t.Status.String())
		line := fmt.Sprintf(" %s %s", icon, truncate(t.Description, m.layout.sidebarWidth-8))
		taskLines = append(taskLines, line)
	}

	agentContent := strings.Join(agentLines, "\n")
	taskContent := strings.Join(taskLines, "\n")
	combined := agentContent + "\n\n" + taskContent

	return sidebarBorder.
		Width(m.layout.sidebarWidth - borderSize).
		Height(m.layout.sidebarHeight).
		Render(combined)
}

// handleCommand processes user input.
func (m *Model) handleCommand(input string) tea.Cmd {
	if strings.HasPrefix(input, "/") {
		return m.handleSlashCommand(input)
	}

	m.appendOutput("you", input)
	if err := m.orch.Send(input); err != nil {
		m.lastErr = err.Error()
		m.appendOutput("error", err.Error())
	}
	return nil
}

// handleSlashCommand parses and executes TUI commands.
func (m *Model) handleSlashCommand(input string) tea.Cmd {
	parts := strings.Fields(input)
	cmd := parts[0]

	switch cmd {
	case "/help":
		m.appendOutput("system", strings.Join([]string{
			"Commands:",
			"  /to <agent> <msg>   — send directly to an agent",
			"  /task <type> <desc> — create a routed task",
			"  /primary <agent>    — switch primary agent",
			"  /agents             — list agents",
			"  /tasks              — list tasks",
			"  /status             — show overview",
			"  /quit               — exit",
			"",
			"Tab to switch panels. Plain text goes to the primary agent.",
		}, "\n"))

	case "/quit":
		return tea.Quit

	case "/to":
		if len(parts) < 3 {
			m.appendOutput("error", "Usage: /to <agent> <message>")
			return nil
		}
		agentName := parts[1]
		message := strings.Join(parts[2:], " ")
		m.appendOutput("you→"+agentName, message)
		if err := m.orch.SendTo(agentName, message); err != nil {
			m.appendOutput("error", err.Error())
		}

	case "/task":
		if len(parts) < 3 {
			m.appendOutput("error", "Usage: /task <type> <description>")
			return nil
		}
		taskType := parts[1]
		desc := strings.Join(parts[2:], " ")
		t := &task.Task{
			Type:        taskType,
			Description: desc,
		}
		if err := m.orch.CreateTask(t); err != nil {
			m.appendOutput("error", err.Error())
		} else {
			m.appendOutput("system", fmt.Sprintf("Task created: [%s] %s", taskType, desc))
		}

	case "/primary":
		if len(parts) < 2 {
			m.appendOutput("error", "Usage: /primary <agent>")
			return nil
		}
		if err := m.orch.SetPrimary(parts[1]); err != nil {
			m.appendOutput("error", err.Error())
		} else {
			m.appendOutput("system", fmt.Sprintf("Primary agent → %s", parts[1]))
		}

	case "/agents":
		m.showAgents()

	case "/tasks":
		m.showTasks()

	case "/status":
		m.showStatus()

	default:
		m.appendOutput("error", fmt.Sprintf("Unknown command: %s (try /help)", cmd))
	}

	return nil
}

// handleEvent processes a bus event and updates the output viewport.
func (m *Model) handleEvent(event bus.Event) {
	agentEvt, ok := event.Payload.(agent.AgentEvent)
	if !ok {
		return
	}

	switch agentEvt.Kind {
	case agent.EventMessage:
		m.appendOutput(agentEvt.Agent, agentEvt.Content)
	case agent.EventError:
		m.appendOutput(agentEvt.Agent+"[err]", agentEvt.Content)
	case agent.EventCommand:
		m.appendOutput(agentEvt.Agent+"[cmd]", agentEvt.Content)
	case agent.EventToolUse:
		m.appendOutput(agentEvt.Agent+"[tool]", agentEvt.Content)
	case agent.EventProgress:
		if agentEvt.Content != "" {
			m.appendOutput(agentEvt.Agent, agentEvt.Content)
		}
	case agent.EventInit:
		m.appendOutput("system", fmt.Sprintf("[%s] %s", agentEvt.Agent, agentEvt.Content))
	case agent.EventComplete:
		if agentEvt.Usage != nil {
			m.appendOutput("system", fmt.Sprintf("[%s] done — %d in / %d out tokens",
				agentEvt.Agent, agentEvt.Usage.InputTokens, agentEvt.Usage.OutputTokens))
		}
	}
}

// appendOutput adds a line to the output buffer and refreshes the viewport.
func (m *Model) appendOutput(source, content string) {
	for _, line := range strings.Split(content, "\n") {
		styled := lipgloss.NewStyle().Foreground(agentColor(source)).Render(source) +
			" " + line
		m.outputLines = append(m.outputLines, styled)
	}

	if len(m.outputLines) > 10000 {
		m.outputLines = m.outputLines[len(m.outputLines)-10000:]
	}

	m.refreshOutput()
}

// refreshOutput updates the viewport content from the output buffer.
func (m *Model) refreshOutput() {
	m.output.SetContent(strings.Join(m.outputLines, "\n"))
	m.output.GotoBottom()
}

// showAgents displays agent status in the output panel.
func (m *Model) showAgents() {
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
	m.appendOutput("system", strings.Join(lines, "\n"))
}

// showTasks displays task list in the output panel.
func (m *Model) showTasks() {
	tasks := m.orch.Tasks().List()
	if len(tasks) == 0 {
		m.appendOutput("system", "No tasks.")
		return
	}
	var lines []string
	lines = append(lines, "Tasks:")
	for _, t := range tasks {
		lines = append(lines, fmt.Sprintf("  %s %s [%s] — %s",
			taskIcon(t.Status.String()), t.ID, t.Type, t.Description))
	}
	m.appendOutput("system", strings.Join(lines, "\n"))
}

// showStatus displays orchestrator overview.
func (m *Model) showStatus() {
	counts := m.orch.Tasks().Count()
	m.appendOutput("system", fmt.Sprintf(
		"Status: primary=%s | agents=%d | running=%d | pending=%d | completed=%d | failed=%d",
		m.orch.Primary(),
		m.orch.Agents().Count(),
		m.orch.RunningCount(),
		counts[task.StatusPending],
		counts[task.StatusCompleted],
		counts[task.StatusFailed],
	))
}

// truncate shortens a string to maxLen, adding "…" if truncated.
func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return s
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 1 {
		return "…"
	}
	return s[:maxLen-1] + "…"
}
