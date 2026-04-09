package recipe

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Loader discovers and loads recipes from configured search paths.
type Loader struct {
	paths []string // directories to search for .yaml/.yml files
}

// NewLoader creates a recipe loader with the given search paths.
// Paths that don't exist are silently skipped.
func NewLoader(searchPaths ...string) *Loader {
	return &Loader{paths: searchPaths}
}

// List returns all valid recipes found across search paths.
// Recipes are deduplicated by name (first found wins).
func (l *Loader) List() []*Recipe {
	seen := make(map[string]bool)
	var recipes []*Recipe

	for _, dir := range l.paths {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // skip inaccessible directories
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !isYAML(name) {
				continue
			}

			path := filepath.Join(dir, name)
			r, err := LoadRecipe(path)
			if err != nil {
				continue // skip malformed recipes
			}
			if r.Name == "" {
				r.Name = strings.TrimSuffix(name, filepath.Ext(name))
			}
			if seen[r.Name] {
				continue // first found wins
			}
			seen[r.Name] = true
			recipes = append(recipes, r)
		}
	}

	return recipes
}

// Get loads a specific recipe by name. Searches all paths.
func (l *Loader) Get(name string) (*Recipe, error) {
	for _, dir := range l.paths {
		for _, ext := range []string{".yaml", ".yml"} {
			path := filepath.Join(dir, name+ext)
			if _, err := os.Stat(path); err != nil {
				continue
			}
			r, err := LoadRecipe(path)
			if err != nil {
				return nil, err
			}
			if r.Name == "" {
				r.Name = name
			}
			return r, nil
		}
	}
	return nil, fmt.Errorf("recipe %q not found in search paths", name)
}

func isYAML(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".yaml" || ext == ".yml"
}
