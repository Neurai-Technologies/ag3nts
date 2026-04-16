// Package orchestrator is the central coordinator that wires agents, router,
// task queue, and event bus together. It manages the dispatch loop, primary
// agent conversation, and inter-agent communication.
package orchestrator

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rohanrgit/ag3nts/internal/agent"
	"github.com/rohanrgit/ag3nts/internal/bus"
	m3m0ry "github.com/rohanrgit/ag3nts/internal/context"
	"github.com/rohanrgit/ag3nts/internal/logging"
	"github.com/rohanrgit/ag3nts/internal/recipe"
	"github.com/rohanrgit/ag3nts/internal/router"
	"github.com/rohanrgit/ag3nts/internal/security"
	"github.com/rohanrgit/ag3nts/internal/store"
	"github.com/rohanrgit/ag3nts/internal/task"
)

// Default session timeout for automated task dispatch (not interactive).
const taskSessionTimeout = 300 * time.Second

// Config holds orchestrator initialization parameters.
type Config struct {
	Primary        string         // default primary agent name
	MaxConcurrency int            // max parallel agent executions
	PersistDir     string         // directory for task/result persistence
	Routes         []router.Route // routing rules
	StoreDB        *store.DB             // SQLite store (nil = JSON-only fallback)
	SessionID      string               // current session identifier for SQLite
	Reviewer       *security.Reviewer    // pre-dispatch security reviewer (nil = disabled)
	Logger         *logging.Logger       // structured logger (nil = disabled)
	Compactor      *Compactor            // context compactor (nil = disabled)
	Memory         *store.MemoryStore    // cross-agent memory (nil = disabled)
	Context        *m3m0ry.RollingStore  // rolling context window (nil = disabled)
	BaseDir        string                // ag3nts install root for recipe file: resolution
	AgentWorkDir   string                // cwd pinned for every subprocess agent (user's launch dir)
	ResumeIDs      map[string]string     // agent → provider session ID (restored from SQLite on --resume)
}

// Orchestrator coordinates agent dispatch, task management, and message flow.
type Orchestrator struct {
	agents  *agent.Registry
	router  *router.Router
	queue   *task.Queue
	store   *Store
	storeDB  *store.DB          // SQLite persistence (nil = disabled)
	sessID   string             // current session ID for SQLite
	reviewer *security.Reviewer // pre-dispatch security review (nil = disabled)
	logger    *logging.Logger   // structured session logger (nil = disabled)
	compactor *Compactor          // progressive context compaction (nil = disabled)
	memory    *store.MemoryStore  // cross-agent memory (nil = disabled)
	rollingCtx *m3m0ry.RollingStore // rolling context window (nil = disabled)
	recorder  *m3m0ry.Recorder    // bus recorder feeding rollingCtx
	baseDir   string              // ag3nts install root for recipe file: resolution
	agentWorkDir string            // cwd pinned for every subprocess agent dispatch
	taskDir   string              // task persistence directory (empty = disabled)
	bus       *bus.Bus
	primary string
	maxConc int

	resumeIDs map[string]string           // agent → provider session ID (for cross-restart resume)

	mu         sync.Mutex
	running    map[string]*agent.Session // taskID → active session
	mainSess   *agent.Session            // primary agent interactive session
	directSess map[string]*agent.Session // agentName → direct send session (for resume)
	retryCount map[string]int            // taskID → retry attempts so far

	taskDone chan struct{} // signalled when any task completes, triggers immediate re-dispatch

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates an orchestrator from the given config and agent registry.
func New(cfg Config, agents *agent.Registry) (*Orchestrator, error) {
	r, err := router.New(cfg.Routes, cfg.Primary, agents)
	if err != nil {
		return nil, fmt.Errorf("create router: %w", err)
	}

	maxConc := cfg.MaxConcurrency
	if maxConc <= 0 {
		maxConc = 3
	}

	persistDir := cfg.PersistDir
	taskDir := ""
	resultDir := ""
	if persistDir != "" {
		taskDir = persistDir + "/tasks"
		resultDir = persistDir + "/results"
	}

	q := task.NewQueue(taskDir)
	q.SetSessionID(cfg.SessionID)
	return &Orchestrator{
		agents:     agents,
		router:     r,
		queue:      q,
		store:      NewStore(resultDir),
		storeDB:    cfg.StoreDB,
		sessID:     cfg.SessionID,
		reviewer:   cfg.Reviewer,
		logger:     cfg.Logger,
		compactor:  cfg.Compactor,
		memory:     cfg.Memory,
		rollingCtx:   cfg.Context,
		baseDir:      cfg.BaseDir,
		agentWorkDir: cfg.AgentWorkDir,
		taskDir:      taskDir,
		bus:          bus.New(),
		primary:    cfg.Primary,
		maxConc:    maxConc,
		resumeIDs:  cfg.ResumeIDs,
		running:    make(map[string]*agent.Session),
		directSess: make(map[string]*agent.Session),
		retryCount: make(map[string]int),
		taskDone:   make(chan struct{}, 16), // buffered to avoid blocking executeTask
	}, nil
}

// Bus returns the event bus for external consumers to subscribe to events.
func (o *Orchestrator) Bus() *bus.Bus {
	return o.bus
}

// StoreDB returns the SQLite store, or nil if not configured.
func (o *Orchestrator) StoreDB() *store.DB {
	return o.storeDB
}

// RollingContext returns the rolling context store (m3m0ry), or nil if not configured.
func (o *Orchestrator) RollingContext() *m3m0ry.RollingStore {
	return o.rollingCtx
}

// Start begins the orchestrator dispatch loop in a background goroutine.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.ctx, o.cancel = context.WithCancel(ctx)

	// Restore persisted tasks if any.
	if err := o.queue.Load(); err != nil {
		return fmt.Errorf("load tasks: %w", err)
	}

	// Start rolling context recorder if configured.
	if o.rollingCtx != nil {
		o.recorder = m3m0ry.NewRecorder(o.rollingCtx, o.bus)
		o.recorder.Start(o.ctx)
	}

	// Start the dispatch loop.
	go o.dispatchLoop()

	return nil
}

// Stop gracefully shuts down the orchestrator: stops all running agents,
// persists queue state, and cancels the dispatch loop.
func (o *Orchestrator) Stop() error {
	if o.cancel != nil {
		o.cancel()
	}

	o.mu.Lock()
	sessions := make([]*agent.Session, 0, len(o.running))
	for _, sess := range o.running {
		sessions = append(sessions, sess)
	}
	o.mu.Unlock()

	// Stop all running agent sessions.
	for _, sess := range sessions {
		a := o.agents.Get(sess.Agent)
		if a != nil {
			_ = a.Stop(sess)
		}
	}

	// B-4 fix: read mainSess under lock.
	o.mu.Lock()
	mainSess := o.mainSess
	o.mu.Unlock()
	if mainSess != nil {
		a := o.agents.Get(o.primary)
		if a != nil {
			_ = a.Stop(mainSess)
		}
	}

	// Stop the rolling context recorder before closing the bus.
	if o.recorder != nil {
		o.recorder.Stop()
	}

	// Close the event bus.
	o.bus.Close()

	// Persist final queue state.
	return o.queue.Save()
}

// Send sends a message to the primary agent. If a session is already active,
// it resumes the conversation using the provider's session ID (e.g. Claude's
// --resume --session-id) so context is preserved across messages.
func (o *Orchestrator) Send(message string) error {
	a := o.agents.Get(o.primary)
	if a == nil {
		return fmt.Errorf("primary agent %q not found", o.primary)
	}

	// Capture resume ID from existing session before stopping it.
	o.mu.Lock()
	oldSess := o.mainSess
	o.mainSess = nil
	o.mu.Unlock()

	var resumeID string
	if oldSess != nil {
		resumeID = oldSess.ResumeID()
		// Persist resume ID before stopping, so --resume can restore it.
		if resumeID != "" && o.storeDB != nil && o.sessID != "" {
			_ = o.storeDB.SetResumeID(o.sessID, o.primary, resumeID)
		}
		_ = a.Stop(oldSess)
	} else if o.resumeIDs != nil {
		// First message after --resume: use stored provider session ID.
		resumeID = o.resumeIDs[o.primary]
	}

	newSess, err := a.Start(o.ctx, message, &agent.StartOpts{
		TaskID:          "_primary",
		ResumeSessionID: resumeID,
		WorkDir:         o.agentWorkDir,
	})
	if err != nil {
		return fmt.Errorf("start primary agent: %w", err)
	}

	o.mu.Lock()
	o.mainSess = newSess
	o.mu.Unlock()
	go o.drainEvents(newSess)
	return nil
}

// SendTo sends a message directly to a specific agent (not through routing).
// Resumes the previous session if one exists for this agent.
func (o *Orchestrator) SendTo(agentName string, message string) error {
	a := o.agents.Get(agentName)
	if a == nil {
		return fmt.Errorf("agent %q not found", agentName)
	}

	// Capture resume ID from existing session before stopping it.
	o.mu.Lock()
	oldSess := o.directSess[agentName]
	delete(o.directSess, agentName)
	o.mu.Unlock()

	var resumeID string
	if oldSess != nil {
		resumeID = oldSess.ResumeID()
		if resumeID != "" && o.storeDB != nil && o.sessID != "" {
			_ = o.storeDB.SetResumeID(o.sessID, agentName, resumeID)
		}
		_ = a.Stop(oldSess)
	} else if o.resumeIDs != nil {
		resumeID = o.resumeIDs[agentName]
	}

	sess, err := a.Start(o.ctx, message, &agent.StartOpts{
		TaskID:          fmt.Sprintf("_direct-%s-%d", agentName, time.Now().UnixNano()),
		ResumeSessionID: resumeID,
		WorkDir:         o.agentWorkDir,
	})
	if err != nil {
		return fmt.Errorf("start %s: %w", agentName, err)
	}

	o.mu.Lock()
	o.directSess[agentName] = sess
	o.mu.Unlock()

	go o.drainEvents(sess)
	return nil
}

// Research runs a two-stage pipeline: Gemini researches (fresh session, no
// resume to avoid context explosion), then Claude synthesizes the findings.
// Gemini's tool_use/init/complete events are published for progress visibility,
// but message content is captured silently and fed to Claude as context.
func (o *Orchestrator) Research(query string) error {
	gemini := o.agents.Get("gemini")
	if gemini == nil {
		return fmt.Errorf("gemini agent not available")
	}
	claude := o.agents.Get(o.primary)
	if claude == nil {
		return fmt.Errorf("primary agent %q not available", o.primary)
	}

	// Stage 1: Gemini researches (fresh session — no resume).
	sess, err := gemini.Start(o.ctx, query, &agent.StartOpts{
		TaskID:  fmt.Sprintf("_research-%d", time.Now().UnixNano()),
		WorkDir: o.agentWorkDir,
	})
	if err != nil {
		return fmt.Errorf("start gemini research: %w", err)
	}

	// Drain Gemini events: publish tool/progress events for visibility,
	// capture message text silently for Claude.
	go func() {
		var research strings.Builder
		synthesized := false
		for event := range sess.Events() {
			switch event.Kind {
			case agent.EventMessage, agent.EventProgress:
				// Capture silently — don't publish to TUI.
				research.WriteString(event.Content)
			case agent.EventComplete:
				// Publish completion.
				o.publish(event)

				// Only synthesize once (parser + subprocess both emit EventComplete).
				if synthesized {
					continue
				}
				synthesized = true

				// Stage 2: Send research to Claude for synthesis (always fresh — no resume).
				researchText := research.String()
				if researchText == "" {
					return
				}

				// If research is too thin (<200 chars), retry with a more specific prompt.
				if len(researchText) < 200 {
					o.publish(agent.AgentEvent{
						Kind:      agent.EventProgress,
						Agent:     "gemini",
						Content:   "research too brief, retrying with more detail...",
						Timestamp: time.Now(),
					})
					retrySess, retryErr := gemini.Start(o.ctx, "The previous research was too brief. Please provide a much more detailed and thorough answer with specific details, examples, and steps. Original query: "+query, &agent.StartOpts{
						TaskID:  fmt.Sprintf("_research-retry-%d", time.Now().UnixNano()),
						WorkDir: o.agentWorkDir,
					})
					if retryErr == nil {
						var retry strings.Builder
						for ev := range retrySess.Events() {
							if ev.Kind == agent.EventMessage || ev.Kind == agent.EventProgress {
								retry.WriteString(ev.Content)
							} else if ev.Kind != agent.EventComplete {
								o.publish(ev)
							}
						}
						if retry.Len() > 0 {
							researchText = retry.String()
						}
					}
				}

				// Stop any existing primary session.
				o.mu.Lock()
				oldMain := o.mainSess
				o.mainSess = nil
				o.mu.Unlock()
				if oldMain != nil {
					_ = claude.Stop(oldMain)
				}

				synthesisPrompt := "IMPORTANT: Do NOT use any tools. Do NOT read files, search, or fetch anything. ONLY synthesize and present the following research findings clearly and concisely. All the information you need is below:\n\n" + researchText
				newSess, err := claude.Start(o.ctx, synthesisPrompt, &agent.StartOpts{
					TaskID:  "_primary",
					WorkDir: o.agentWorkDir,
				})
				if err != nil {
					o.publish(agent.AgentEvent{
						Kind:      agent.EventError,
						Agent:     o.primary,
						Content:   fmt.Sprintf("synthesis failed: %v", err),
						Timestamp: time.Now(),
					})
					return
				}

				o.mu.Lock()
				o.mainSess = newSess
				o.mu.Unlock()
				o.drainEvents(newSess)
			default:
				// Publish tool_use, init, error, etc. for visibility.
				o.publish(event)
			}
		}
	}()

	return nil
}

// CreateTask adds a task to the queue. It will be dispatched by the
// dispatch loop when its dependencies are satisfied.
func (o *Orchestrator) CreateTask(t *task.Task) error {
	if t.ID == "" {
		t.ID = fmt.Sprintf("T%d", time.Now().UnixNano())
	}
	return o.queue.Add(t)
}

// RunRecipe dispatches a recipe. Single-task recipes create one task via
// Resolve; multi-task recipes expand into a DAG via Expand and add all
// tasks to the queue under a shared run ID.
//
// Returns the run ID (the prefix used for expanded task IDs). For
// single-task recipes, the run ID equals the generated task ID.
func (o *Orchestrator) RunRecipe(r *recipe.Recipe, params map[string]string) (string, error) {
	if r == nil {
		return "", fmt.Errorf("nil recipe")
	}

	if !r.IsMultiTask() {
		// Single-task path — preserves existing behavior.
		t, err := r.Resolve(params)
		if err != nil {
			return "", err
		}
		if err := o.CreateTask(t); err != nil {
			return "", err
		}
		return t.ID, nil
	}

	// Multi-task path.
	runID := fmt.Sprintf("R%d", time.Now().UnixNano()%1_000_000_000)
	tasks, err := r.Expand(recipe.ExpansionContext{
		RecipeRunID: runID,
		Params:      params,
		BaseDir:     o.baseDir,
	})
	if err != nil {
		return "", fmt.Errorf("expand recipe: %w", err)
	}

	// Apply per-recipe concurrency override if specified.
	if r.MaxConcurrency > 0 {
		o.mu.Lock()
		o.maxConc = r.MaxConcurrency
		o.mu.Unlock()
	}

	for _, t := range tasks {
		if err := o.queue.Add(t); err != nil {
			return runID, fmt.Errorf("add task %s: %w", t.ID, err)
		}
	}

	// Record the recipe start in m3m0ry if enabled.
	if o.rollingCtx != nil {
		_ = o.rollingCtx.Append(&m3m0ry.Chunk{
			SessionID: o.sessID,
			Kind:      "recipe_start",
			Content:   fmt.Sprintf("run=%s recipe=%s tasks=%d", runID, r.Name, len(tasks)),
			CreatedAt: time.Now(),
		})
	}

	if o.logger != nil {
		o.logger.Infof("orchestrator", "started recipe run %s (%s) with %d tasks", runID, r.Name, len(tasks))
	}

	return runID, nil
}

// SetPrimary changes the primary agent. Existing sessions are not affected.
func (o *Orchestrator) SetPrimary(name string) error {
	if err := o.router.SetPrimary(name); err != nil {
		return err
	}
	o.primary = name
	return nil
}

// UpdateMaxConcurrency changes the maximum number of tasks dispatched in
// parallel. Non-positive values fall back to the default of 3.
func (o *Orchestrator) UpdateMaxConcurrency(n int) {
	if n <= 0 {
		n = 3
	}
	o.mu.Lock()
	o.maxConc = n
	o.mu.Unlock()
}

// UpdateRouting replaces routing rules in the underlying router.
func (o *Orchestrator) UpdateRouting(routes []router.Route) error {
	return o.router.UpdateRoutes(routes)
}

// Primary returns the current primary agent name.
// SessionID returns the orchestrator's current session identifier.
// Used by the TUI to scope task views to the current session.
func (o *Orchestrator) SessionID() string {
	return o.sessID
}

// TaskPersistDir returns the directory where task JSON files are
// stored, or empty if task persistence is disabled. Used by the
// TUI's /task gc command to scan for legacy flat-layout files.
func (o *Orchestrator) TaskPersistDir() string {
	return o.taskDir
}

// BaseDir returns the ag3nts install root used for recipe file:
// template resolution. Used by the TUI dry-run renderer to expand
// templates without actually dispatching.
func (o *Orchestrator) BaseDir() string {
	return o.baseDir
}

func (o *Orchestrator) Primary() string {
	return o.primary
}

// Agents returns the agent registry for external consumers (e.g. TUI).
func (o *Orchestrator) Agents() *agent.Registry {
	return o.agents
}

// Tasks returns the task queue for external consumers (e.g. TUI).
func (o *Orchestrator) Tasks() *task.Queue {
	return o.queue
}

// Router returns the router for external consumers.
func (o *Orchestrator) Router() *router.Router {
	return o.router
}

// RunningCount returns the number of currently running agent sessions.
func (o *Orchestrator) RunningCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	count := len(o.running)
	if o.mainSess != nil && o.mainSess.Status == agent.StatusRunning {
		count++
	}
	for _, s := range o.directSess {
		if s != nil && s.Status == agent.StatusRunning {
			count++
		}
	}
	return count
}

// RunningAgents returns the names of agents with active sessions.
func (o *Orchestrator) RunningAgents() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	var names []string
	if o.mainSess != nil && o.mainSess.Status == agent.StatusRunning {
		names = append(names, o.mainSess.Agent)
	}
	for name, s := range o.directSess {
		if s != nil && s.Status == agent.StatusRunning {
			names = append(names, name)
		}
	}
	return names
}

// dispatchLoop runs in a goroutine. It triggers on two signals:
//   - taskDone channel: a task just completed, immediately check for
//     newly-unblocked tasks (event-driven, zero latency).
//   - 500ms ticker: fallback catch-all for tasks added externally,
//     evaluator retries, or missed signals.
func (o *Orchestrator) dispatchLoop() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-o.ctx.Done():
			return
		case <-o.taskDone:
			o.dispatchReady()
		case <-ticker.C:
			o.dispatchReady()
		}
	}
}

// dispatchReady finds tasks with satisfied dependencies and dispatches them.
func (o *Orchestrator) dispatchReady() {
	ready := o.queue.Ready()
	if len(ready) == 0 {
		return
	}

	o.mu.Lock()
	runCount := len(o.running)
	maxConc := o.maxConc
	o.mu.Unlock()

	for _, t := range ready {
		if runCount >= maxConc {
			break
		}

		// Resolve which agent handles this task.
		agentName, err := o.router.Resolve(t.Type, t.Agent)
		if err != nil {
			_ = o.queue.Update(t.ID, task.StatusFailed, &task.Result{
				Error: fmt.Sprintf("routing failed: %v", err),
			})
			continue
		}

		a := o.agents.Get(agentName)
		if a == nil {
			_ = o.queue.Update(t.ID, task.StatusFailed, &task.Result{
				Error: fmt.Sprintf("agent %q not in registry", agentName),
			})
			continue
		}

		// Pre-dispatch security review.
		if o.reviewer != nil {
			review := o.reviewer.Review(o.ctx, t.Description, "")
			if review.Decision == security.ReviewBlock {
				_ = o.queue.Update(t.ID, task.StatusFailed, &task.Result{
					Error: fmt.Sprintf("security blocked: %s", review.Explanation),
				})
				o.publish(agent.AgentEvent{
					Kind:      agent.EventError,
					Agent:     "security",
					TaskID:    t.ID,
					Content:   fmt.Sprintf("BLOCKED: %s", review.Explanation),
					Timestamp: time.Now(),
				})
				if o.logger != nil {
					o.logger.LogAgentWith(logging.LevelWarn, "security", "", t.ID,
						"task blocked by security review", map[string]any{"explanation": review.Explanation})
				}
				continue
			}
		}

		// Mark as running.
		_ = o.queue.Update(t.ID, task.StatusRunning, nil)
		if o.storeDB != nil {
			_ = o.storeDB.CreateTask(&store.TaskRecord{
				ID:          t.ID,
				SessionID:   o.sessID,
				Agent:       agentName,
				Type:        t.Type,
				Description: t.Description,
				Status:      "running",
				DependsOn:   t.DependsOn,
				ContextFrom: t.ContextFrom,
			})
		}
		if o.logger != nil {
			o.logger.LogAgentWith(logging.LevelInfo, "orchestrator", agentName, t.ID,
				"dispatching task", map[string]any{"type": t.Type, "description_len": len(t.Description)})
		}

		// Record repair stage start marker in m3m0ry for cross-run searchability.
		if o.rollingCtx != nil && strings.HasPrefix(t.Type, "repair.") {
			stage := strings.TrimPrefix(t.Type, "repair.")
			_ = o.rollingCtx.Append(&m3m0ry.Chunk{
				SessionID: o.sessID,
				TaskID:    t.ID,
				Agent:     agentName,
				Kind:      "repair_stage_start",
				Content:   fmt.Sprintf("stage=%s task=%s agent=%s", stage, t.ID, agentName),
				CreatedAt: time.Now(),
			})
		}

		// Build context from referenced task results.
		contextStr := o.buildContext(t.ContextFrom)

		go o.executeTask(t, a, contextStr)
		runCount++
	}
}

// executeTask runs a single task on the given agent and collects results.
func (o *Orchestrator) executeTask(t *task.Task, a agent.Agent, contextStr string) {
	ctx := o.ctx
	if t.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.Timeout)
		defer cancel()
	}

	sess, err := a.Start(ctx, t.Description, &agent.StartOpts{
		TaskID:  t.ID,
		Model:   t.Model,
		Context: contextStr,
		WorkDir: o.agentWorkDir,
	})
	if err != nil {
		_ = o.queue.Update(t.ID, task.StatusFailed, &task.Result{
			Error: fmt.Sprintf("start failed: %v", err),
		})
		return
	}

	o.mu.Lock()
	o.running[t.ID] = sess
	o.mu.Unlock()

	// Collect events until the session closes.
	var output strings.Builder
	// progressive accumulates EventProgress content as a fallback for
	// agents that stream their final response via delta updates without
	// emitting a non-delta EventMessage (e.g., Gemini CLI). Only used if
	// output (EventMessage-sourced) is empty at the end.
	var progressive strings.Builder
	var events []agent.AgentEvent
	var usage *agent.TokenUsage
	start := time.Now()

	for event := range sess.Events() {
		events = append(events, event)

		// Publish to event bus for TUI and other subscribers.
		o.publish(event)

		switch event.Kind {
		case agent.EventMessage:
			output.WriteString(event.Content)
		case agent.EventProgress:
			// Capture streaming delta content for agents that don't emit
			// a final aggregated EventMessage. Bounded safety check: skip
			// status-only progress events with trivial content (e.g. Codex
			// "Turn started") so the fallback doesn't pollute task results.
			if len(event.Content) > 0 && event.Content != "Turn started" {
				progressive.WriteString(event.Content)
			}
		case agent.EventComplete:
			if event.Usage != nil {
				usage = event.Usage
			}
		}
	}

	// Fallback: if no EventMessage content was captured but we saw progressive
	// deltas, use the accumulated deltas as the task output. Gemini CLI
	// triggers this path — it streams the response via delta messages.
	if output.Len() == 0 && progressive.Len() > 0 {
		output.WriteString(progressive.String())
	}

	duration := time.Since(start)

	// Determine final status.
	status := task.StatusCompleted
	var errStr string
	if sess.Status == agent.StatusFailed {
		status = task.StatusFailed
		errStr = "agent session failed"

		// Check if the error is retryable.
		if lastErr := findLastError(events); lastErr != nil {
			if retryable, _ := lastErr.Metadata["retryable"].(bool); retryable {
				errTypeStr, _ := lastErr.Metadata["error_type"].(string)
				errType := parseErrorType(errTypeStr)
				cfg := agent.DefaultRetryConfig(errType)

				o.mu.Lock()
				attempts := o.retryCount[t.ID]
				o.mu.Unlock()

				if cfg.MaxAttempts > 0 && attempts < cfg.MaxAttempts {
					backoff := cfg.Backoff(attempts)
					// If the error includes a parsed Retry-After duration
					// (from the API's 429 response), prefer it over the
					// exponential backoff. This is more precise — the API
					// knows exactly when its rate-limit window resets.
					retrySource := "backoff"
					if raStr, ok := lastErr.Metadata["retry_after"].(string); ok {
						if ra, err := time.ParseDuration(raStr); err == nil && ra > 0 {
							backoff = ra
							retrySource = "Retry-After"
						}
					}
					o.publish(agent.AgentEvent{
						Kind:      agent.EventProgress,
						Agent:     "orchestrator",
						TaskID:    t.ID,
						Content:   fmt.Sprintf("retrying in %s (attempt %d/%d, %s, %s)", backoff.Round(time.Second), attempts+1, cfg.MaxAttempts, errTypeStr, retrySource),
						Timestamp: time.Now(),
					})
					if o.logger != nil {
						o.logger.LogAgentWith(logging.LevelWarn, "orchestrator", a.Name(), t.ID,
							fmt.Sprintf("retrying task (attempt %d/%d, %s)", attempts+1, cfg.MaxAttempts, errTypeStr),
							map[string]any{"backoff_ms": backoff.Milliseconds()})
					}
					time.Sleep(backoff)
					o.mu.Lock()
					o.retryCount[t.ID] = attempts + 1
					delete(o.running, t.ID)
					o.mu.Unlock()
					_ = o.queue.Update(t.ID, task.StatusPending, nil)
					return // will be re-dispatched by dispatchLoop
				}
			}
		}
	}

	result := &task.Result{
		Output:   output.String(),
		Events:   events,
		Usage:    usage,
		Duration: duration,
		Error:    errStr,
	}

	// Persist to SQLite BEFORE queue update — queue update signals completion
	// to waiters, so SQLite must be written first to avoid read-before-write races.
	if o.storeDB != nil {
		var tokens store.TokenRecord
		var costUSD float64
		if usage != nil {
			tokens = store.TokenRecord{
				InputTokens:  usage.InputTokens,
				OutputTokens: usage.OutputTokens,
				CachedTokens: usage.CachedTokens,
				CostUSD:      usage.TotalCost,
			}
			costUSD = usage.TotalCost
		}
		_ = o.storeDB.UpdateTaskResult(t.ID, a.Name(), output.String(), errStr, tokens, duration.Milliseconds())
		_ = o.storeDB.AddTokenUsage(o.sessID, tokens.InputTokens, tokens.OutputTokens, tokens.CachedTokens, costUSD)
	}

	_ = o.queue.Update(t.ID, status, result)

	// Log task completion.
	if o.logger != nil {
		logLevel := logging.LevelInfo
		if status == task.StatusFailed {
			logLevel = logging.LevelError
		}
		o.logger.LogAgentWith(logLevel, "orchestrator", a.Name(), t.ID, "task "+status.String(),
			map[string]any{"duration_ms": duration.Milliseconds(), "error": errStr})
	}

	// Save result to context store for downstream tasks.
	o.store.SaveResult(t.ID, result)

	// Append task result to rolling context window if enabled.
	if o.rollingCtx != nil && result.Output != "" {
		_ = o.rollingCtx.Append(&m3m0ry.Chunk{
			SessionID: o.sessID,
			TaskID:    t.ID,
			Agent:     a.Name(),
			Kind:      "task_result",
			Content:   result.Output,
			CreatedAt: time.Now(),
		})
	}

	// Record repair stage end marker for cross-run searchability.
	if o.rollingCtx != nil && strings.HasPrefix(t.Type, "repair.") {
		stage := strings.TrimPrefix(t.Type, "repair.")
		_ = o.rollingCtx.Append(&m3m0ry.Chunk{
			SessionID: o.sessID,
			TaskID:    t.ID,
			Agent:     a.Name(),
			Kind:      "repair_stage_end",
			Content:   fmt.Sprintf("stage=%s task=%s status=%s duration_ms=%d", stage, t.ID, status, duration.Milliseconds()),
			CreatedAt: time.Now(),
		})
	}

	// If this was an evaluator task, process its verdict and spawn retries
	// (or terminate the loop). No-op for non-evaluator tasks.
	if status == task.StatusCompleted {
		o.handleEvaluatorCompletion(t, result)
	}

	o.mu.Lock()
	delete(o.running, t.ID)
	o.mu.Unlock()

	// Signal dispatch loop to immediately check for newly-unblocked tasks.
	select {
	case o.taskDone <- struct{}{}:
	default:
		// Channel full — dispatchLoop will catch up via ticker.
	}
}

// buildContext assembles context from cross-agent memory and completed task results.
// SR-12: Truncates individual results at 100KB.
func (o *Orchestrator) buildContext(taskIDs []string) string {
	const maxResultSize = 100 * 1024   // SR-12: 100KB per result
	const maxTotalContext = 512 * 1024 // M-1 fix: 512KB total context cap

	var parts []string

	// Prepend relevant memories if available.
	if o.memory != nil {
		wd := ""
		if o.storeDB != nil {
			if sess, _ := o.storeDB.GetSession(o.sessID); sess != nil {
				wd = sess.WorkingDir
			}
		}
		if memCtx := o.memory.InjectRelevant(wd, ""); memCtx != "" {
			parts = append(parts, memCtx)
		}
	}

	// Inject relevant chunks from the rolling context window (m3m0ry).
	// Query is derived from the concatenated descriptions of referenced tasks.
	if o.rollingCtx != nil && len(taskIDs) > 0 {
		var descriptions []string
		for _, id := range taskIDs {
			if t := o.queue.Get(id); t != nil {
				descriptions = append(descriptions, t.Description)
			}
		}
		if len(descriptions) > 0 {
			query := strings.Join(descriptions, " ")
			if rendered := o.rollingCtx.RenderRelevant(query); rendered != "" {
				parts = append(parts, rendered)
			}
		}
	}

	if len(taskIDs) == 0 {
		if len(parts) == 0 {
			return ""
		}
		return strings.Join(parts, "\n\n")
	}
	var totalSize int
	for _, id := range taskIDs {
		result := o.store.GetResult(id)
		if result == nil {
			continue
		}
		output := result.Output
		if len(output) > maxResultSize {
			output = output[:maxResultSize] + "\n[TRUNCATED]"
		}
		totalSize += len(output)
		if totalSize > maxTotalContext {
			parts = append(parts, "[CONTEXT LIMIT REACHED — further results omitted]")
			break
		}
		parts = append(parts, fmt.Sprintf("=== Result from task %s ===\n%s", id, output))
	}

	if len(parts) == 0 {
		return ""
	}
	assembled := strings.Join(parts, "\n\n")

	// Run through compactor if available.
	if o.compactor != nil {
		assembled = o.compactor.CheckAndCompact(o.ctx, assembled)
	}

	return assembled
}

// Cancel stops the active session for a given agent.
func (o *Orchestrator) Cancel(agentName string) error {
	a := o.agents.Get(agentName)
	if a == nil {
		return fmt.Errorf("agent %q not found", agentName)
	}

	o.mu.Lock()
	// Check direct sessions first.
	if sess, ok := o.directSess[agentName]; ok {
		delete(o.directSess, agentName)
		o.mu.Unlock()
		return a.Stop(sess)
	}
	// Check primary session.
	if agentName == o.primary && o.mainSess != nil {
		sess := o.mainSess
		o.mainSess = nil
		o.mu.Unlock()
		return a.Stop(sess)
	}
	o.mu.Unlock()
	return fmt.Errorf("no active session for %s", agentName)
}

// drainEvents reads all events from a session and publishes them to the bus.
// Emits a heartbeat every 30s if no events arrive so the user knows it's alive.
func (o *Orchestrator) drainEvents(sess *agent.Session) {
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case event, ok := <-sess.Events():
			if !ok {
				return
			}
			o.publish(event)
			heartbeat.Reset(30 * time.Second)
		case <-heartbeat.C:
			o.publish(agent.AgentEvent{
				Kind:      agent.EventProgress,
				Agent:     sess.Agent,
				SessionID: sess.ID,
				TaskID:    sess.TaskID,
				Content:   "still working...",
				Timestamp: time.Now(),
			})
		}
	}
}

// publish sends an agent event to the bus on both agent-specific and
// task-specific topics so subscribers can filter by either dimension.
func (o *Orchestrator) publish(event agent.AgentEvent) {
	// Publish on agent topic: "agent.claude", "agent.gemini", etc.
	o.bus.Publish("agent."+event.Agent, event.Agent, event)

	// Publish on task topic if tagged.
	if event.TaskID != "" {
		o.bus.Publish("task."+event.TaskID, event.Agent, event)
	}

	// Publish on the global system topic.
	o.bus.Publish("system", event.Agent, event)
}

// findLastError returns the last EventError from an event slice, or nil.
func findLastError(events []agent.AgentEvent) *agent.AgentEvent {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == agent.EventError {
			return &events[i]
		}
	}
	return nil
}

// parseErrorType converts a string back to an ErrorType.
func parseErrorType(s string) agent.ErrorType {
	switch s {
	case "auth_failed":
		return agent.ErrAuthFailed
	case "rate_limited":
		return agent.ErrRateLimited
	case "context_exceeded":
		return agent.ErrContextExceeded
	case "crashed":
		return agent.ErrCrashed
	case "timed_out":
		return agent.ErrTimedOut
	case "network_error":
		return agent.ErrNetworkError
	case "security_blocked":
		return agent.ErrSecurityBlocked
	default:
		return agent.ErrUnknown
	}
}
