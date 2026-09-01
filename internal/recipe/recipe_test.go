package recipe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Single-task validation (backward compat) ---

func TestRecipeSingleTaskValid(t *testing.T) {
	r := &Recipe{
		Name:         "test",
		SystemPrompt: "you are a test agent",
		Agent:        "claude",
	}
	if r.IsMultiTask() {
		t.Error("expected !IsMultiTask")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestRecipeSingleTaskMissingPrompt(t *testing.T) {
	r := &Recipe{Name: "test"}
	if err := r.Validate(); err == nil {
		t.Error("expected error for missing prompt")
	}
}

// --- Multi-task validation ---

func TestRecipeMultiTaskValid(t *testing.T) {
	r := &Recipe{
		Name: "repair",
		Tasks: []SubTask{
			{ID: "research", Description: "do research", Agent: "gemini"},
			{ID: "plan", Description: "make plan", Agent: "claude", DependsOn: []string{"research"}, ContextFrom: []string{"research"}},
			{ID: "implement", Description: "write code", Agent: "codex", DependsOn: []string{"plan"}, ContextFrom: []string{"plan"}},
		},
	}
	if !r.IsMultiTask() {
		t.Error("expected IsMultiTask")
	}
	if err := r.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestRecipeMultiTaskDuplicateID(t *testing.T) {
	r := &Recipe{
		Name: "bad",
		Tasks: []SubTask{
			{ID: "a", Description: "first"},
			{ID: "a", Description: "duplicate"},
		},
	}
	if err := r.Validate(); err == nil {
		t.Error("expected error for duplicate ID")
	} else if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error = %v, want duplicate", err)
	}
}

func TestRecipeMultiTaskUnknownDep(t *testing.T) {
	r := &Recipe{
		Name: "bad",
		Tasks: []SubTask{
			{ID: "a", Description: "first"},
			{ID: "b", Description: "second", DependsOn: []string{"nonexistent"}},
		},
	}
	if err := r.Validate(); err == nil {
		t.Error("expected error for unknown dep")
	}
}

func TestRecipeMultiTaskUnknownContext(t *testing.T) {
	r := &Recipe{
		Name: "bad",
		Tasks: []SubTask{
			{ID: "a", Description: "first", ContextFrom: []string{"nonexistent"}},
		},
	}
	if err := r.Validate(); err == nil {
		t.Error("expected error for unknown context_from")
	}
}

func TestRecipeMultiTaskMissingContent(t *testing.T) {
	r := &Recipe{
		Name: "bad",
		Tasks: []SubTask{
			{ID: "a"}, // no description, no prompt_template
		},
	}
	if err := r.Validate(); err == nil {
		t.Error("expected error for missing content")
	}
}

func TestRecipeMultiTaskCycleDetection(t *testing.T) {
	r := &Recipe{
		Name: "cyclic",
		Tasks: []SubTask{
			{ID: "a", Description: "a", DependsOn: []string{"b"}},
			{ID: "b", Description: "b", DependsOn: []string{"a"}},
		},
	}
	if err := r.Validate(); err == nil {
		t.Error("expected cycle error")
	} else if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error = %v, want cycle", err)
	}
}

func TestRecipeMultiTaskEvaluatorOf(t *testing.T) {
	r := &Recipe{
		Name: "eval",
		Tasks: []SubTask{
			{ID: "impl", Description: "implement"},
			{ID: "review", Description: "review", DependsOn: []string{"impl"}, EvaluatorOf: "impl", EvaluatorRetries: 3},
		},
	}
	if err := r.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestRecipeMultiTaskEvaluatorOfUnknown(t *testing.T) {
	r := &Recipe{
		Name: "bad",
		Tasks: []SubTask{
			{ID: "review", Description: "review", EvaluatorOf: "nothing"},
		},
	}
	if err := r.Validate(); err == nil {
		t.Error("expected error for unknown evaluator_of")
	}
}

// --- Template rendering ---

func TestRenderSubTaskInline(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "target", Type: "string", Required: true},
		},
		Tasks: []SubTask{
			{ID: "a", PromptTemplate: "review {{target}}"},
		},
	}
	rendered, err := r.RenderSubTask(&r.Tasks[0], map[string]string{"target": "./src"}, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if rendered != "review ./src" {
		t.Errorf("rendered = %q", rendered)
	}
}

func TestRenderSubTaskFile(t *testing.T) {
	dir := t.TempDir()
	promptFile := filepath.Join(dir, "prompt.md")
	if err := os.WriteFile(promptFile, []byte("Research {{topic}} thoroughly."), 0644); err != nil {
		t.Fatal(err)
	}

	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "topic", Type: "string", Required: true},
		},
		Tasks: []SubTask{
			{ID: "research", PromptTemplate: "file:prompt.md"},
		},
	}
	rendered, err := r.RenderSubTask(&r.Tasks[0], map[string]string{"topic": "MCP protocol"}, dir)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(rendered, "MCP protocol") {
		t.Errorf("rendered missing substitution: %q", rendered)
	}
}

func TestRenderSubTaskInclude(t *testing.T) {
	dir := t.TempDir()
	prefix := filepath.Join(dir, "prefix.md")
	if err := os.WriteFile(prefix, []byte("=== PREFIX ===\n"), 0644); err != nil {
		t.Fatal(err)
	}

	r := &Recipe{
		Name: "test",
		Tasks: []SubTask{
			{ID: "a", PromptTemplate: "{{#include:prefix.md}}\nThe actual work goes here."},
		},
	}
	rendered, err := r.RenderSubTask(&r.Tasks[0], nil, dir)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(rendered, "=== PREFIX ===") {
		t.Errorf("missing include content: %q", rendered)
	}
	if !strings.Contains(rendered, "actual work") {
		t.Errorf("missing body: %q", rendered)
	}
}

func TestRenderSubTaskParamOverride(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "mode", Type: "string", Default: "safe"},
		},
		Tasks: []SubTask{
			{
				ID:             "a",
				PromptTemplate: "mode={{mode}}",
				Params:         map[string]string{"mode": "aggressive"},
			},
		},
	}
	rendered, err := r.RenderSubTask(&r.Tasks[0], map[string]string{"mode": "default"}, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Sub-task params override both global and default.
	if rendered != "mode=aggressive" {
		t.Errorf("rendered = %q, want mode=aggressive", rendered)
	}
}

func TestRenderSubTaskMissingRequired(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "target", Type: "string", Required: true},
		},
		Tasks: []SubTask{
			{ID: "a", PromptTemplate: "review {{target}}"},
		},
	}
	_, err := r.RenderSubTask(&r.Tasks[0], nil, "")
	if err == nil {
		t.Error("expected error for missing required param")
	}
}

func TestRenderSubTaskDefault(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "depth", Type: "string", Default: "thorough"},
		},
		Tasks: []SubTask{
			{ID: "a", PromptTemplate: "depth={{depth}}"},
		},
	}
	rendered, err := r.RenderSubTask(&r.Tasks[0], nil, "")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if rendered != "depth=thorough" {
		t.Errorf("rendered = %q", rendered)
	}
}

// --- YAML parsing ---

func TestParseRecipeYAMLMultiTask(t *testing.T) {
	yamlData := `
name: test-repair
description: A test pipeline
parameters:
  - key: objective
    type: string
    required: true
tasks:
  - id: research
    description: research the topic
    agent: gemini
    prompt_template: "research {{objective}}"
  - id: plan
    description: make a plan
    agent: claude
    depends_on: [research]
    context_from: [research]
    prompt_template: "plan based on research"
  - id: review
    description: review the plan
    agent: claude
    depends_on: [plan]
    evaluator_of: plan
    evaluator_retries: 3
    prompt_template: "review this work"
`
	r, err := ParseRecipe([]byte(yamlData))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !r.IsMultiTask() {
		t.Error("expected multi-task")
	}
	if len(r.Tasks) != 3 {
		t.Errorf("tasks = %d, want 3", len(r.Tasks))
	}
	if r.Tasks[2].EvaluatorOf != "plan" {
		t.Errorf("evaluator_of = %q", r.Tasks[2].EvaluatorOf)
	}
	if r.Tasks[2].EvaluatorRetries != 3 {
		t.Errorf("evaluator_retries = %d", r.Tasks[2].EvaluatorRetries)
	}
	if err := r.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}
