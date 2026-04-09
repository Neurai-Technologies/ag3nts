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

// Validate checks that the recipe has required fields.
func (r *Recipe) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("recipe name is required")
	}
	if r.SystemPrompt == "" && r.Description == "" {
		return fmt.Errorf("recipe %q: system_prompt or description is required", r.Name)
	}
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
