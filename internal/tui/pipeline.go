package tui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
)

// stageStatus is the lifecycle of a single recipe stage.
type stageStatus int

const (
	stagePending stageStatus = iota
	stageRunning
	stageCompleted
	stageFailed
	stageBlocked
)

// stageState holds the live state of one stage in a recipe run.
type stageState struct {
	name   string // "research", "plan", "implement", etc. (without the "repair." prefix)
	status stageStatus
	taskID string
}

// stageSummary is per-stage data collected for the final summary banner
// when a recipe run completes. Built from the queue's task records at
// the moment the recipe reaches a terminal state.
type stageSummary struct {
	name         string
	status       stageStatus
	inputTokens  int
	outputTokens int
	costUSD      float64
}

// recipeRunState tracks the live state of a single recipe execution.
// Stages are added dynamically as tasks of type "repair.*" appear in
// the queue, so we don't need a hardcoded list of stages — repair-lite
// (4 stages) and repair (7 stages) both work without configuration.
type recipeRunState struct {
	runID     string
	stages    []*stageState // ordered by first-seen
	startedAt time.Time     // when the run was first observed (dispatch or first event)
	finalized bool          // true once the final summary banner has been emitted
}

// pipelineTracker keeps state for all in-flight recipe runs and renders
// the inline banner + sticky status line on transitions.
type pipelineTracker struct {
	mu   sync.Mutex
	runs map[string]*recipeRunState // runID -> state
}

func newPipelineTracker() *pipelineTracker {
	return &pipelineTracker{
		runs: make(map[string]*recipeRunState),
	}
}

// updateStage records a stage transition and returns the affected run
// state for rendering. runID is derived from the task ID (everything
// before the first "-"). Returns nil if the task ID has no runID prefix.
func (p *pipelineTracker) updateStage(taskID, taskType string, status stageStatus) *recipeRunState {
	stage := strings.TrimPrefix(taskType, "repair.")
	if stage == taskType {
		// Not a repair task.
		return nil
	}
	runID := runIDFromTaskID(taskID)
	if runID == "" {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	run, ok := p.runs[runID]
	if !ok {
		run = &recipeRunState{runID: runID}
		p.runs[runID] = run
	}

	// Find or create the stage entry. Stages are dynamic — we add them
	// the first time we see a task with that stage name.
	var st *stageState
	for _, s := range run.stages {
		if s.taskID == taskID {
			st = s
			break
		}
	}
	if st == nil {
		st = &stageState{name: stage, taskID: taskID, status: status}
		run.stages = append(run.stages, st)
	} else {
		st.status = status
	}

	// Return a snapshot so the caller can render outside the lock.
	snapshot := &recipeRunState{runID: run.runID, stages: make([]*stageState, len(run.stages))}
	for i, s := range run.stages {
		copy := *s
		snapshot.stages[i] = &copy
	}
	return snapshot
}

// hasRun returns true if the tracker is already aware of the given run.
func (p *pipelineTracker) hasRun(runID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.runs[runID]
	return ok
}

// stageStatusFor returns the current stored status for a specific
// (runID, taskID) pair, or stagePending if not found.
func (p *pipelineTracker) stageStatusFor(runID, taskID string) stageStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	run, ok := p.runs[runID]
	if !ok {
		return stagePending
	}
	for _, s := range run.stages {
		if s.taskID == taskID {
			return s.status
		}
	}
	return stagePending
}

// discoverStages scans the provided task list for all tasks belonging
// to runID and adds them as pending stages. Used on first-contact with
// a new recipe run so the banner shows the full pipeline shape from
// the start, not just the stages that have begun executing. Returns
// a snapshot of the discovered run, or nil if no stages were found.
func (p *pipelineTracker) discoverStages(runID string, tasksByID func() []taskMeta) *recipeRunState {
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing, ok := p.runs[runID]; ok {
		return cloneRunStateLocked(existing)
	}
	all := tasksByID()
	run := &recipeRunState{
		runID:     runID,
		startedAt: time.Now(),
	}
	for _, t := range all {
		if runIDFromTaskID(t.id) != runID {
			continue
		}
		stage := strings.TrimPrefix(t.taskType, "repair.")
		if stage == t.taskType {
			continue // not a repair stage
		}
		run.stages = append(run.stages, &stageState{
			name:   stage,
			taskID: t.id,
			status: stagePending,
		})
	}
	if len(run.stages) > 0 {
		p.runs[runID] = run
		return cloneRunStateLocked(run)
	}
	return nil
}

// markFinalized records that the final summary banner has been emitted
// for a run, so subsequent transitions don't re-emit it. Returns true
// if this is the first time finalize was called (i.e. caller should
// emit the banner now); false if it was already finalized.
func (p *pipelineTracker) markFinalized(runID string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	run, ok := p.runs[runID]
	if !ok || run.finalized {
		return false
	}
	run.finalized = true
	return true
}

// cloneRunStateLocked returns a deep snapshot of a run state. Caller
// must hold p.mu.
func cloneRunStateLocked(src *recipeRunState) *recipeRunState {
	clone := &recipeRunState{
		runID:     src.runID,
		startedAt: src.startedAt,
		finalized: src.finalized,
		stages:    make([]*stageState, len(src.stages)),
	}
	for i, s := range src.stages {
		st := *s
		clone.stages[i] = &st
	}
	return clone
}

// taskMeta is a minimal projection of task fields used by discoverStages
// so the tracker doesn't need to import the task package directly.
type taskMeta struct {
	id       string
	taskType string
}

// runIDFromTaskID extracts the recipe run prefix from a task ID like
// "R12345-research" or "R12345-implement-retry1". Returns "" if the
// task ID has no recipe prefix.
func runIDFromTaskID(taskID string) string {
	idx := strings.Index(taskID, "-")
	if idx <= 0 {
		return ""
	}
	prefix := taskID[:idx]
	// Recipe run IDs start with "R" followed by digits.
	if len(prefix) < 2 || prefix[0] != 'R' {
		return ""
	}
	for _, c := range prefix[1:] {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return prefix
}

// stageIcon maps a stage status to a one-character indicator.
func stageIcon(s stageStatus) string {
	switch s {
	case stageRunning:
		return "⠋"
	case stageCompleted:
		return "✓"
	case stageFailed:
		return "✗"
	case stageBlocked:
		return "⊘"
	default:
		return "·"
	}
}

// stageColor returns the lipgloss style for a stage's icon based on status.
func stageColor(s stageStatus) lipgloss.Style {
	switch s {
	case stageRunning:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD54F")).Bold(true)
	case stageCompleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784")).Bold(true)
	case stageFailed, stageBlocked:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#EF5350")).Bold(true)
	default:
		return dimStyle
	}
}

// renderInlineBanner returns a multi-line styled box showing the
// current state of a recipe run. Used as a one-shot inline render
// (Option 3) that scrolls naturally with output — re-printed on
// every stage transition rather than pinned to a fixed location.
func renderInlineBanner(run *recipeRunState) string {
	if run == nil || len(run.stages) == 0 {
		return ""
	}

	var stageStrs []string
	for _, s := range run.stages {
		icon := stageColor(s.status).Render(stageIcon(s.status))
		name := s.name
		if s.status == stageRunning {
			name = lipgloss.NewStyle().Bold(true).Render(name)
		} else if s.status == stagePending {
			name = dimStyle.Render(name)
		}
		stageStrs = append(stageStrs, icon+" "+name)
	}
	body := strings.Join(stageStrs, "  ")

	header := fmt.Sprintf("recipe %s", run.runID)
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6E6E6E")).
		Padding(0, 1)
	content := lipgloss.NewStyle().Foreground(lipgloss.Color("#9E9E9E")).Render(header) +
		"\n" + body
	return border.Render(content)
}

// renderStickyStatus returns a one-line summary of a recipe run for
// the sticky status line above the prompt (Option 1). Compact form,
// no border, fits on one terminal row.
func renderStickyStatus(run *recipeRunState) string {
	if run == nil || len(run.stages) == 0 {
		return ""
	}
	var parts []string
	for _, s := range run.stages {
		icon := stageColor(s.status).Render(stageIcon(s.status))
		parts = append(parts, icon+" "+s.name)
	}
	prefix := dimStyle.Render("▶ " + run.runID + ": ")
	return prefix + strings.Join(parts, "  ")
}

// isTerminal returns true if every stage in the run has reached a
// terminal status (completed, failed, or blocked). Used to decide
// when to clear the sticky status.
func (run *recipeRunState) isTerminal() bool {
	for _, s := range run.stages {
		if s.status == stagePending || s.status == stageRunning {
			return false
		}
	}
	return true
}

// renderFinalSummary renders a styled completion box for a finished
// recipe run. Includes per-stage status + totals (wall-clock, tokens,
// cost). Stage data is provided by the caller — the tracker doesn't
// know the orchestrator's task records.
func renderFinalSummary(run *recipeRunState, summaries []stageSummary, totalCost float64, totalIn, totalOut int) string {
	if run == nil || len(run.stages) == 0 {
		return ""
	}

	// Header line: outcome verdict.
	var verdict string
	allOK := true
	for _, s := range run.stages {
		if s.status == stageFailed || s.status == stageBlocked {
			allOK = false
			break
		}
	}
	if allOK {
		verdict = lipgloss.NewStyle().Foreground(lipgloss.Color("#81C784")).Bold(true).Render("✓ recipe complete")
	} else {
		verdict = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF5350")).Bold(true).Render("✗ recipe ended with failures")
	}

	// Per-stage rows.
	var rows []string
	for _, s := range run.stages {
		icon := stageColor(s.status).Render(stageIcon(s.status))
		// Look up summary for this stage.
		var ts stageSummary
		for _, sum := range summaries {
			if sum.name == s.name {
				ts = sum
				break
			}
		}
		stageLabel := fmt.Sprintf("%s %s", icon, s.name)
		stats := ""
		if ts.inputTokens > 0 || ts.outputTokens > 0 {
			stats = dimStyle.Render(fmt.Sprintf("  ↑%s ↓%s",
				formatTokensShort(ts.inputTokens),
				formatTokensShort(ts.outputTokens)))
			if ts.costUSD > 0 {
				stats += dimStyle.Render(fmt.Sprintf("  $%.4f", ts.costUSD))
			}
		}
		rows = append(rows, stageLabel+stats)
	}

	// Total stats line.
	duration := time.Since(run.startedAt).Round(time.Second)
	totals := dimStyle.Render(fmt.Sprintf("total: ↑%s ↓%s",
		formatTokensShort(totalIn),
		formatTokensShort(totalOut)))
	if totalCost > 0 {
		totals += dimStyle.Render(fmt.Sprintf("  $%.4f", totalCost))
	}
	totals += dimStyle.Render(fmt.Sprintf("  %s", duration))

	header := dimStyle.Render(fmt.Sprintf("recipe %s", run.runID))

	body := header + "\n" +
		verdict + "\n" +
		strings.Join(rows, "\n") + "\n" +
		totals

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#6E6E6E")).
		Padding(0, 1)

	return border.Render(body)
}

// formatTokensShort formats a token count compactly: 1234 → "1.2k",
// 12345 → "12k", 1234567 → "1.2M". Used by the final summary banner
// to keep stage rows narrow.
func formatTokensShort(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}
