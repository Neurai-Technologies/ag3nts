package recipe

import (
	"strings"
	"testing"
)

func TestValidateParams_RequiredMissing(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "objective", Type: "string", Required: true, Description: "What to build"},
		},
	}
	_, err := r.ValidateParams(map[string]string{})
	if err == nil {
		t.Fatal("expected error for missing required param")
	}
	if !strings.Contains(err.Error(), "objective") || !strings.Contains(err.Error(), "required") {
		t.Errorf("error %q should mention objective and required", err)
	}
}

func TestValidateParams_RequiredProvided(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "objective", Type: "string", Required: true},
		},
	}
	out, err := r.ValidateParams(map[string]string{"objective": "build a new feature"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["objective"] != "build a new feature" {
		t.Errorf("value not preserved: %q", out["objective"])
	}
}

func TestValidateParams_DefaultApplied(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "motivation", Type: "string", Required: false, Default: "unspecified"},
		},
	}
	out, err := r.ValidateParams(map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out["motivation"] != "unspecified" {
		t.Errorf("default not applied: %q", out["motivation"])
	}
}

func TestValidateParams_MinWordsFailure(t *testing.T) {
	r := &Recipe{
		Name: "repair-lite",
		Parameters: []Parameter{
			{
				Key:         "objective",
				Type:        "string",
				Required:    true,
				MinWords:    5,
				Description: "What to build",
			},
		},
	}
	_, err := r.ValidateParams(map[string]string{"objective": "add"})
	if err == nil {
		t.Fatal("expected min_words error for 'add'")
	}
	if !strings.Contains(err.Error(), "too vague") {
		t.Errorf("error should say 'too vague': %q", err)
	}
	if !strings.Contains(err.Error(), "minimum 5 words") {
		t.Errorf("error should state minimum: %q", err)
	}
}

func TestValidateParams_MinWordsPass(t *testing.T) {
	r := &Recipe{
		Name: "repair-lite",
		Parameters: []Parameter{
			{Key: "objective", Type: "string", Required: true, MinWords: 5},
		},
	}
	_, err := r.ValidateParams(map[string]string{
		"objective": "add a --verbose flag to cmd/orchestrate.go",
	})
	if err != nil {
		t.Errorf("expected 7-word objective to pass, got: %v", err)
	}
}

func TestValidateParams_MinChars(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "query", Type: "string", Required: true, MinChars: 10},
		},
	}
	if _, err := r.ValidateParams(map[string]string{"query": "hi"}); err == nil {
		t.Error("expected min_chars error for 'hi'")
	}
	if _, err := r.ValidateParams(map[string]string{"query": "what is sqlite WAL mode"}); err != nil {
		t.Errorf("expected pass for long query: %v", err)
	}
}

func TestValidateParams_Pattern(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "version", Type: "string", Required: true, Pattern: `^v\d+\.\d+\.\d+$`},
		},
	}
	if _, err := r.ValidateParams(map[string]string{"version": "not-a-version"}); err == nil {
		t.Error("expected pattern error")
	}
	if _, err := r.ValidateParams(map[string]string{"version": "v1.2.3"}); err != nil {
		t.Errorf("expected pass for v1.2.3: %v", err)
	}
}

func TestValidateParams_SelectEnum(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{
				Key:      "model",
				Type:     "select",
				Required: true,
				Options:  []string{"sonnet", "opus", "haiku"},
			},
		},
	}
	if _, err := r.ValidateParams(map[string]string{"model": "gpt-4"}); err == nil {
		t.Error("expected enum error")
	}
	if _, err := r.ValidateParams(map[string]string{"model": "sonnet"}); err != nil {
		t.Errorf("expected pass: %v", err)
	}
}

func TestValidateParams_DoesNotMutateInput(t *testing.T) {
	r := &Recipe{
		Name: "test",
		Parameters: []Parameter{
			{Key: "motivation", Type: "string", Default: "unspecified"},
		},
	}
	input := map[string]string{}
	out, _ := r.ValidateParams(input)
	if _, ok := input["motivation"]; ok {
		t.Error("input map was mutated")
	}
	if out["motivation"] != "unspecified" {
		t.Error("output map missing default")
	}
}
