package recipe

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// includeDirective matches {{#include:relative/path}} for prompt composition.
var includeDirective = regexp.MustCompile(`\{\{#include:([^}]+)\}\}`)

// RenderSubTask fills {{param}} placeholders in a sub-task's prompt template,
// resolving "file:<path>" references and {{#include:path}} directives relative
// to baseDir. Sub-task params override global params.
func (r *Recipe) RenderSubTask(st *SubTask, globalParams map[string]string, baseDir string) (string, error) {
	tmpl, err := loadPromptTemplate(st.PromptTemplate, baseDir)
	if err != nil {
		return "", fmt.Errorf("sub-task %q: %w", st.ID, err)
	}

	// Resolve includes recursively (one level — no nested includes).
	tmpl, err = resolveIncludes(tmpl, baseDir)
	if err != nil {
		return "", fmt.Errorf("sub-task %q: %w", st.ID, err)
	}

	// Merge params: globals first, sub-task overrides.
	merged := make(map[string]string, len(globalParams)+len(st.Params))
	for k, v := range globalParams {
		merged[k] = v
	}
	for k, v := range st.Params {
		merged[k] = v
	}

	// Substitute recipe parameters (apply defaults + enforce required).
	for _, p := range r.Parameters {
		placeholder := "{{" + p.Key + "}}"
		value, ok := merged[p.Key]
		if !ok || value == "" {
			if p.Default != "" {
				value = p.Default
			} else if p.Required {
				return "", fmt.Errorf("sub-task %q: required parameter %q not provided", st.ID, p.Key)
			}
		}
		tmpl = strings.ReplaceAll(tmpl, placeholder, value)
	}

	// Substitute any ad-hoc params from sub-task that aren't formal parameters.
	for k, v := range st.Params {
		tmpl = strings.ReplaceAll(tmpl, "{{"+k+"}}", v)
	}

	return tmpl, nil
}

// loadPromptTemplate returns the template body, resolving "file:<path>" prefix.
func loadPromptTemplate(template, baseDir string) (string, error) {
	if template == "" {
		return "", nil
	}
	if strings.HasPrefix(template, "file:") {
		path := strings.TrimPrefix(template, "file:")
		if !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("load prompt template %s: %w", path, err)
		}
		return string(data), nil
	}
	return template, nil
}

// resolveIncludes replaces {{#include:path}} directives with file contents.
// Paths are relative to baseDir. Only one pass — no recursive includes.
func resolveIncludes(tmpl, baseDir string) (string, error) {
	matches := includeDirective.FindAllStringSubmatchIndex(tmpl, -1)
	if len(matches) == 0 {
		return tmpl, nil
	}

	var b strings.Builder
	lastEnd := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		pathStart, pathEnd := m[2], m[3]
		includePath := strings.TrimSpace(tmpl[pathStart:pathEnd])

		if !filepath.IsAbs(includePath) {
			includePath = filepath.Join(baseDir, includePath)
		}
		data, err := os.ReadFile(includePath)
		if err != nil {
			return "", fmt.Errorf("include %s: %w", includePath, err)
		}

		b.WriteString(tmpl[lastEnd:start])
		b.Write(data)
		lastEnd = end
	}
	b.WriteString(tmpl[lastEnd:])
	return b.String(), nil
}
