package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ModelManager handles loading, unloading, and lifecycle of Ollama models.
// Only one secondary model (Reasoner or Analyzer) is loaded at a time
// to manage VRAM. The Head model stays loaded permanently.
type ModelManager struct {
	client  *OllamaClient
	models  map[ModelRole]*ModelConfig
	mu      sync.Mutex
	loaded  map[string]bool // model name → currently loaded
}

// NewModelManager creates a manager for the given model configurations.
func NewModelManager(client *OllamaClient, models map[ModelRole]*ModelConfig) *ModelManager {
	return &ModelManager{
		client: client,
		models: models,
		loaded: make(map[string]bool),
	}
}

// EnsureLoaded loads a model into Ollama VRAM if not already loaded.
// For secondary models, unloads any other secondary model first.
func (mm *ModelManager) EnsureLoaded(ctx context.Context, role ModelRole) error {
	cfg, ok := mm.models[role]
	if !ok {
		return fmt.Errorf("no model configured for role %d", role)
	}

	mm.mu.Lock()
	if mm.loaded[cfg.Name] {
		mm.mu.Unlock()
		return nil
	}

	// For secondary models, unload the other secondary first.
	if role != ModelHead {
		for r, m := range mm.models {
			if r != ModelHead && r != role && mm.loaded[m.Name] {
				mm.mu.Unlock()
				_ = mm.Unload(ctx, r)
				mm.mu.Lock()
			}
		}
	}
	mm.mu.Unlock()

	// Send an empty chat request with keep_alive to warm the model.
	keepAlive := cfg.KeepAlive
	if keepAlive == "" {
		keepAlive = "30m"
	}

	_, _, err := mm.client.Chat(ctx, ChatRequest{
		Model:     cfg.Name,
		Messages:  []Message{{Role: RoleUser, Content: "hello"}},
		KeepAlive: keepAlive,
	})
	if err != nil {
		return fmt.Errorf("load model %s: %w", cfg.Name, err)
	}

	mm.mu.Lock()
	mm.loaded[cfg.Name] = true
	mm.mu.Unlock()

	return nil
}

// Unload removes a model from Ollama VRAM.
func (mm *ModelManager) Unload(ctx context.Context, role ModelRole) error {
	cfg, ok := mm.models[role]
	if !ok {
		return fmt.Errorf("no model configured for role %d", role)
	}

	mm.mu.Lock()
	if !mm.loaded[cfg.Name] {
		mm.mu.Unlock()
		return nil
	}
	mm.mu.Unlock()

	// Send request with keep_alive=0 to trigger unload.
	_, _, err := mm.client.Chat(ctx, ChatRequest{
		Model:     cfg.Name,
		Messages:  []Message{{Role: RoleUser, Content: "unload"}},
		KeepAlive: "0",
	})
	if err != nil {
		return fmt.Errorf("unload model %s: %w", cfg.Name, err)
	}

	mm.mu.Lock()
	delete(mm.loaded, cfg.Name)
	mm.mu.Unlock()

	return nil
}

// ModelName returns the Ollama model name for the given role.
func (mm *ModelManager) ModelName(role ModelRole) string {
	if cfg, ok := mm.models[role]; ok {
		return cfg.Name
	}
	return ""
}

// Config returns the ModelConfig for the given role.
func (mm *ModelManager) Config(role ModelRole) *ModelConfig {
	if cfg, ok := mm.models[role]; ok {
		return cfg
	}
	return nil
}

// IsLoaded checks if a model is currently loaded in Ollama VRAM.
func (mm *ModelManager) IsLoaded(ctx context.Context, role ModelRole) bool {
	cfg, ok := mm.models[role]
	if !ok {
		return false
	}

	// Check via Ollama API.
	models, err := mm.runningModels(ctx)
	if err != nil {
		// Fall back to local tracking.
		mm.mu.Lock()
		defer mm.mu.Unlock()
		return mm.loaded[cfg.Name]
	}

	for _, m := range models {
		if m == cfg.Name {
			return true
		}
	}
	return false
}

// WarmHead ensures the head model stays loaded permanently (keep_alive=-1).
func (mm *ModelManager) WarmHead(ctx context.Context) error {
	cfg, ok := mm.models[ModelHead]
	if !ok {
		return fmt.Errorf("no head model configured")
	}

	_, _, err := mm.client.Chat(ctx, ChatRequest{
		Model:     cfg.Name,
		Messages:  []Message{{Role: RoleUser, Content: "hello"}},
		KeepAlive: "-1",
	})
	if err != nil {
		return fmt.Errorf("warm head model: %w", err)
	}

	mm.mu.Lock()
	mm.loaded[cfg.Name] = true
	mm.mu.Unlock()

	return nil
}

// runningModels returns the names of models currently loaded in Ollama.
func (mm *ModelManager) runningModels(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", mm.client.endpoint+"/api/ps", nil)
	if err != nil {
		return nil, err
	}

	resp, err := mm.client.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	names := make([]string, len(result.Models))
	for i, m := range result.Models {
		names[i] = m.Name
	}
	return names, nil
}

// Status returns a human-readable status of all managed models.
func (mm *ModelManager) Status(ctx context.Context) string {
	running, _ := mm.runningModels(ctx)
	runningSet := make(map[string]bool)
	for _, n := range running {
		runningSet[n] = true
	}

	var lines []string
	roles := []ModelRole{ModelHead, ModelReasoner, ModelAnalyzer}
	roleNames := []string{"head", "reasoner", "analyzer"}

	for i, role := range roles {
		cfg, ok := mm.models[role]
		if !ok {
			continue
		}
		status := "unloaded"
		if runningSet[cfg.Name] {
			status = "loaded"
		}
		lines = append(lines, fmt.Sprintf("  %s: %s (%s)", roleNames[i], cfg.Name, status))
	}

	return "Local models:\n" + joinLines(lines)
}

func joinLines(lines []string) string {
	result := ""
	for i, l := range lines {
		if i > 0 {
			result += "\n"
		}
		result += l
	}
	return result
}
