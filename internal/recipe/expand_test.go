package recipe

import (
	"strings"
	"testing"
	"time"

	"github.com/rohanrgit/ag3nts/internal/task"
)

func makeRepairLike() *Recipe {
	return &Recipe{
		Name: "repair",
		Parameters: []Parameter{
			{Key: "objective", Type: "string", Required: true},
		},
		Tasks: []SubTask{
			{
				ID:             "research",
				Agent:          "gemini",
				Type:           "repair.research",
				PromptTemplate: "research {{objective}}",
			},
			{
				ID:             "plan",
				Agent:          "claude",
				Type:           "repair.plan",
				DependsOn:      []string{"research"},
				ContextFrom:    []string{"research"},
				PromptTemplate: "plan based on research",
				Timeout:        "10m",
			},
			{
				ID:             "implement",
				Agent:          "codex",
				Type:           "repair.implement",
				DependsOn:      []string{"plan"},
				ContextFrom:    []string{"plan"},
				PromptTemplate: "write the code",
			},
			{
				ID:               "review",
				Agent:            "claude",
				Type:             "repair.review",
				DependsOn:        []string{"implement"},
				ContextFrom:      []string{"implement"},
				PromptTemplate:   "review the implementation",
				EvaluatorOf:      "implement",
				EvaluatorRetries: 3,
			},
		},
	}
}

func TestExpandMultiTask(t *testing.T) {
	r := makeRepairLike()
	tasks, err := r.Expand(ExpansionContext{
		RecipeRunID: "R123",
		Params:      map[string]string{"objective": "add MCP support"},
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("len = %d, want 4", len(tasks))
	}

	// Verify ID rewriting.
	expectedIDs := []string{"R123-research", "R123-plan", "R123-implement", "R123-review"}
	for i, want := range expectedIDs {
		if tasks[i].ID != want {
			t.Errorf("task[%d].ID = %q, want %q", i, tasks[i].ID, want)
		}
	}
}

func TestExpandRewritesDependencies(t *testing.T) {
	r := makeRepairLike()
	tasks, err := r.Expand(ExpansionContext{
		RecipeRunID: "R456",
		Params:      map[string]string{"objective": "test"},
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	// plan depends on research — both should be prefixed.
	plan := findByID(tasks, "R456-plan")
	if plan == nil {
		t.Fatal("plan task not found")
	}
	if len(plan.DependsOn) != 1 || plan.DependsOn[0] != "R456-research" {
		t.Errorf("plan.DependsOn = %v, want [R456-research]", plan.DependsOn)
	}
	if len(plan.ContextFrom) != 1 || plan.ContextFrom[0] != "R456-research" {
		t.Errorf("plan.ContextFrom = %v", plan.ContextFrom)
	}
}

func TestExpandParamSubstitution(t *testing.T) {
	r := makeRepairLike()
	tasks, err := r.Expand(ExpansionContext{
		RecipeRunID: "R789",
		Params:      map[string]string{"objective": "add MCP support"},
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	research := findByID(tasks, "R789-research")
	if research == nil {
		t.Fatal("research task not found")
	}
	if !strings.Contains(research.Description, "add MCP support") {
		t.Errorf("research description missing substitution: %q", research.Description)
	}
}

func TestExpandEvaluatorTrailer(t *testing.T) {
	r := makeRepairLike()
	tasks, err := r.Expand(ExpansionContext{
		RecipeRunID: "R111",
		Params:      map[string]string{"objective": "test"},
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	review := findByID(tasks, "R111-review")
	if review == nil {
		t.Fatal("review task not found")
	}
	if !strings.Contains(review.Description, "[EVALUATOR_LOOP:") {
		t.Error("review missing evaluator trailer")
	}
	if !strings.Contains(review.Description, "target=R111-implement") {
		t.Error("evaluator trailer missing target")
	}
	if !strings.Contains(review.Description, "retries=3") {
		t.Error("evaluator trailer missing retries")
	}
}

func TestExpandTimeoutParsing(t *testing.T) {
	r := makeRepairLike()
	tasks, err := r.Expand(ExpansionContext{
		RecipeRunID: "R222",
		Params:      map[string]string{"objective": "test"},
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}

	plan := findByID(tasks, "R222-plan")
	if plan.Timeout != 10*time.Minute {
		t.Errorf("plan.Timeout = %v, want 10m", plan.Timeout)
	}
}

func TestExpandMissingRunID(t *testing.T) {
	r := makeRepairLike()
	_, err := r.Expand(ExpansionContext{
		Params: map[string]string{"objective": "test"},
	})
	if err == nil {
		t.Error("expected error for missing RecipeRunID")
	}
}

func TestExpandSingleTaskRejected(t *testing.T) {
	r := &Recipe{Name: "single", SystemPrompt: "do a thing"}
	_, err := r.Expand(ExpansionContext{RecipeRunID: "R1"})
	if err == nil {
		t.Error("expected error for single-task recipe")
	}
}

func TestExpandMissingRequiredParam(t *testing.T) {
	r := makeRepairLike()
	_, err := r.Expand(ExpansionContext{
		RecipeRunID: "R333",
		// missing objective
	})
	if err == nil {
		t.Error("expected error for missing required param")
	}
}

// --- Resolve (single-task backward compat) ---

func TestResolveSingleTask(t *testing.T) {
	r := &Recipe{
		Name:         "code-review",
		SystemPrompt: "review {{target}}",
		Agent:        "claude",
		Parameters: []Parameter{
			{Key: "target", Type: "string", Required: true},
		},
		Tags: []string{"review"},
	}
	tk, err := r.Resolve(map[string]string{"target": "./src"})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tk.Agent != "claude" {
		t.Errorf("agent = %q", tk.Agent)
	}
	if tk.Type != "review" {
		t.Errorf("type = %q", tk.Type)
	}
	if !strings.Contains(tk.Description, "./src") {
		t.Errorf("description missing substitution: %q", tk.Description)
	}
}

func TestResolveMultiTaskRejected(t *testing.T) {
	r := makeRepairLike()
	_, err := r.Resolve(map[string]string{"objective": "test"})
	if err == nil {
		t.Error("expected error for multi-task recipe")
	}
}

// --- Helper ---

func findByID(tasks []*task.Task, id string) *task.Task {
	for _, t := range tasks {
		if t.ID == id {
			return t
		}
	}
	return nil
}
