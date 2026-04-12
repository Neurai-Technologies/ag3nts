// Package recipe provides declarative YAML recipes for ag3nts.
// A recipe bundles a persona (system prompt), preferred agent, model override,
// required tool-sets, typed parameters, and execution constraints into a
// single YAML file. Extracted from Goose's recipe/mod.rs pattern.
package recipe

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Recipe is a declarative task definition loaded from YAML.
//
// Recipes come in two flavors:
//   - Single-task: SystemPrompt + Agent + Parameters (existing behavior)
//   - Multi-task: Tasks []SubTask with a DAG of stages routed to different agents
//
// A recipe with a non-empty Tasks field is treated as multi-task and must be
// dispatched via Orchestrator.RunRecipe (which calls Expand). Single-task
// recipes continue to work via Resolve.
type Recipe struct {
	Name         string      `yaml:"name"`
	Description  string      `yaml:"description"`
	SystemPrompt string      `yaml:"system_prompt"`
	Agent        string      `yaml:"agent"`      // preferred agent (claude, gemini, codex, etc.)
	Model        string      `yaml:"model"`       // model override (empty = agent default)
	Tools        []string    `yaml:"tools"`       // required tool-set names
	Parameters   []Parameter `yaml:"parameters"`  // typed user inputs
	MaxTurns     int         `yaml:"max_turns"`   // turn limit (0 = unlimited)
	Timeout      string      `yaml:"timeout"`     // duration string (e.g. "5m", "1h")
	Tags         []string    `yaml:"tags"`        // for routing pattern matching

	// Multi-task recipes: a DAG of sub-tasks routed to different agents.
	// When present, the orchestrator expands this into a list of *task.Task
	// with dependencies wired. Empty means single-task recipe.
	Tasks []SubTask `yaml:"tasks"`
}

// SubTask is one stage in a multi-task recipe. Each sub-task is dispatched
// as its own orchestrator Task with DAG dependencies derived from DependsOn
// and ContextFrom rewritten to prefixed run IDs.
type SubTask struct {
	ID               string            `yaml:"id"`               // stable within recipe (e.g. "research", "plan")
	Description      string            `yaml:"description"`       // short label (optional if prompt_template is set)
	Agent            string            `yaml:"agent"`             // empty = let router decide
	Model            string            `yaml:"model"`
	Type             string            `yaml:"type"`              // routing hint (e.g. "repair.research")
	DependsOn        []string          `yaml:"depends_on"`        // sub-task IDs (not yet rewritten)
	ContextFrom      []string          `yaml:"context_from"`      // sub-task IDs whose results to inject
	PromptTemplate   string            `yaml:"prompt_template"`   // inline text or "file:<path>"
	Timeout          string            `yaml:"timeout"`
	MaxRetries       int               `yaml:"max_retries"`
	EvaluatorOf      string            `yaml:"evaluator_of"`      // sub-task ID being evaluated (enables GAN loop)
	EvaluatorRetries int               `yaml:"evaluator_retries"` // max I⇄R loops (default 3)
	Params           map[string]string `yaml:"params"`            // per-sub-task param overrides
}

// Parameter is a typed input for a recipe.
type Parameter struct {
	Key         string   `yaml:"key"`
	Type        string   `yaml:"type"`        // string, file, select, number, boolean
	Required    bool     `yaml:"required"`
	Description string   `yaml:"description"`
	Default     string   `yaml:"default"`
	Options     []string `yaml:"options"`     // for select type
}

// LoadRecipe reads and parses a recipe from a YAML file.
func LoadRecipe(path string) (*Recipe, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read recipe %s: %w", path, err)
	}
	return ParseRecipe(data)
}

// ParseRecipe parses recipe YAML bytes.
func ParseRecipe(data []byte) (*Recipe, error) {
	var r Recipe
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse recipe: %w", err)
	}
	return &r, nil
}

// IsMultiTask reports whether this recipe defines a DAG of sub-tasks.
// Multi-task recipes must be dispatched via Orchestrator.RunRecipe.
func (r *Recipe) IsMultiTask() bool {
	return len(r.Tasks) > 0
}

// Validate checks that the recipe has required fields.
// Single-task recipes require Name + (SystemPrompt | Description).
// Multi-task recipes additionally validate every SubTask and the DAG.
func (r *Recipe) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("recipe name is required")
	}

	// Validate parameters (applies to both flavors).
	for i, p := range r.Parameters {
		if p.Key == "" {
			return fmt.Errorf("recipe %q: parameter %d has no key", r.Name, i)
		}
		if p.Type == "" {
			return fmt.Errorf("recipe %q: parameter %q has no type", r.Name, p.Key)
		}
		switch p.Type {
		case "string", "file", "select", "number", "boolean":
			// valid
		default:
			return fmt.Errorf("recipe %q: parameter %q has unknown type %q", r.Name, p.Key, p.Type)
		}
		if p.Type == "select" && len(p.Options) == 0 {
			return fmt.Errorf("recipe %q: select parameter %q has no options", r.Name, p.Key)
		}
	}

	if r.IsMultiTask() {
		return r.validateMultiTask()
	}

	// Single-task validation.
	if r.SystemPrompt == "" && r.Description == "" {
		return fmt.Errorf("recipe %q: system_prompt or description is required", r.Name)
	}
	return nil
}

// validateMultiTask checks SubTask fields, unique IDs, and DAG integrity.
func (r *Recipe) validateMultiTask() error {
	if len(r.Tasks) == 0 {
		return nil
	}

	// Unique IDs and required fields.
	ids := make(map[string]bool, len(r.Tasks))
	for i, st := range r.Tasks {
		if st.ID == "" {
			return fmt.Errorf("recipe %q: sub-task %d has no id", r.Name, i)
		}
		if ids[st.ID] {
			return fmt.Errorf("recipe %q: duplicate sub-task id %q", r.Name, st.ID)
		}
		ids[st.ID] = true

		if st.PromptTemplate == "" && st.Description == "" {
			return fmt.Errorf("recipe %q: sub-task %q needs description or prompt_template", r.Name, st.ID)
		}
	}

	// Validate refs: depends_on, context_from, evaluator_of all point to existing IDs.
	for _, st := range r.Tasks {
		for _, dep := range st.DependsOn {
			if !ids[dep] {
				return fmt.Errorf("recipe %q: sub-task %q depends_on unknown id %q", r.Name, st.ID, dep)
			}
		}
		for _, cf := range st.ContextFrom {
			if !ids[cf] {
				return fmt.Errorf("recipe %q: sub-task %q context_from unknown id %q", r.Name, st.ID, cf)
			}
		}
		if st.EvaluatorOf != "" && !ids[st.EvaluatorOf] {
			return fmt.Errorf("recipe %q: sub-task %q evaluator_of unknown id %q", r.Name, st.ID, st.EvaluatorOf)
		}
	}

	// Cycle detection via topological sort.
	if err := r.topoSort(); err != nil {
		return fmt.Errorf("recipe %q: %w", r.Name, err)
	}

	return nil
}

// topoSort returns the sub-tasks in dependency order. Returns error if a
// cycle is detected.
func (r *Recipe) topoSort() error {
	inDegree := make(map[string]int, len(r.Tasks))
	deps := make(map[string][]string, len(r.Tasks))

	for _, st := range r.Tasks {
		if _, ok := inDegree[st.ID]; !ok {
			inDegree[st.ID] = 0
		}
		for _, dep := range st.DependsOn {
			deps[dep] = append(deps[dep], st.ID)
			inDegree[st.ID]++
		}
	}

	var queue []string
	for id, d := range inDegree {
		if d == 0 {
			queue = append(queue, id)
		}
	}

	visited := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		visited++
		for _, child := range deps[id] {
			inDegree[child]--
			if inDegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	if visited != len(r.Tasks) {
		return fmt.Errorf("cycle detected in sub-task dependencies")
	}
	return nil
}

// RenderPrompt substitutes {{param}} placeholders in the system prompt
// with values from the params map. Missing required params return an error.
func (r *Recipe) RenderPrompt(params map[string]string) (string, error) {
	prompt := r.SystemPrompt

	for _, p := range r.Parameters {
		placeholder := "{{" + p.Key + "}}"
		value, ok := params[p.Key]
		if !ok || value == "" {
			if p.Default != "" {
				value = p.Default
			} else if p.Required {
				return "", fmt.Errorf("required parameter %q not provided", p.Key)
			}
		}
		prompt = strings.ReplaceAll(prompt, placeholder, value)
	}

	return prompt, nil
}

// TaskType returns a routing-compatible type string for this recipe.
// Uses the first tag if available, otherwise the recipe name.
func (r *Recipe) TaskType() string {
	if len(r.Tags) > 0 {
		return r.Tags[0]
	}
	return r.Name
}
