package recipe

import (
	"fmt"
	"time"

	"github.com/rohanrgit/ag3nts/internal/task"
)

// ExpansionContext configures how a multi-task recipe expands into tasks.
type ExpansionContext struct {
	RecipeRunID string            // prefix for rewritten task IDs (e.g. "R123456")
	Params      map[string]string // top-level recipe params
	BaseDir     string            // for "file:" and {{#include:}} resolution
}

// Expand turns a multi-task recipe into concrete *task.Task instances with
// stable IDs of form "<RecipeRunID>-<SubTask.ID>". DependsOn and ContextFrom
// refs are rewritten to the prefixed IDs. Single-task recipes should call
// Resolve instead.
func (r *Recipe) Expand(ec ExpansionContext) ([]*task.Task, error) {
	if !r.IsMultiTask() {
		return nil, fmt.Errorf("recipe %q is not multi-task — use Resolve()", r.Name)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}
	if ec.RecipeRunID == "" {
		return nil, fmt.Errorf("RecipeRunID is required")
	}

	prefixed := func(id string) string {
		return ec.RecipeRunID + "-" + id
	}

	tasks := make([]*task.Task, 0, len(r.Tasks))
	for i := range r.Tasks {
		st := &r.Tasks[i]

		// Render prompt template with params and includes.
		description, err := r.RenderSubTask(st, ec.Params, ec.BaseDir)
		if err != nil {
			return nil, fmt.Errorf("render sub-task %q: %w", st.ID, err)
		}
		if description == "" {
			description = st.Description
		}

		// Evaluator tasks get a metadata trailer so the orchestrator
		// evaluator loop can parse it without extra storage.
		if st.EvaluatorOf != "" {
			retries := st.EvaluatorRetries
			if retries <= 0 {
				retries = 3
			}
			description += fmt.Sprintf(
				"\n\n[EVALUATOR_LOOP: target=%s retries=%d attempt=0]",
				prefixed(st.EvaluatorOf), retries,
			)
		}

		// Rewrite DependsOn and ContextFrom to prefixed IDs.
		depsOn := make([]string, len(st.DependsOn))
		for j, dep := range st.DependsOn {
			depsOn[j] = prefixed(dep)
		}
		ctxFrom := make([]string, len(st.ContextFrom))
		for j, cf := range st.ContextFrom {
			ctxFrom[j] = prefixed(cf)
		}

		// Parse timeout if specified.
		var timeout time.Duration
		if st.Timeout != "" {
			if d, err := time.ParseDuration(st.Timeout); err == nil {
				timeout = d
			}
		}

		// Task type for routing: prefer explicit Type, else recipe.<name>.<subtask>.
		taskType := st.Type
		if taskType == "" {
			taskType = r.Name + "." + st.ID
		}

		t := &task.Task{
			ID:          prefixed(st.ID),
			Description: description,
			Type:        taskType,
			Agent:       st.Agent,
			Status:      task.StatusPending,
			DependsOn:   depsOn,
			ContextFrom: ctxFrom,
			Timeout:     timeout,
			CreatedAt:   time.Now(),
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}

// Resolve converts a single-task recipe into one *task.Task. Returns an
// error if called on a multi-task recipe (use Expand instead).
func (r *Recipe) Resolve(params map[string]string) (*task.Task, error) {
	if r.IsMultiTask() {
		return nil, fmt.Errorf("recipe %q is multi-task — use Expand()", r.Name)
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}

	prompt, err := r.RenderPrompt(params)
	if err != nil {
		return nil, err
	}

	var timeout time.Duration
	if r.Timeout != "" {
		if d, err := time.ParseDuration(r.Timeout); err == nil {
			timeout = d
		}
	}

	return &task.Task{
		ID:          fmt.Sprintf("R%d", time.Now().UnixNano()%1_000_000_000),
		Description: prompt,
		Type:        r.TaskType(),
		Agent:       r.Agent,
		Status:      task.StatusPending,
		Timeout:     timeout,
		CreatedAt:   time.Now(),
	}, nil
}
