package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// Tier represents a subscription tier.
type Tier string

const (
	TierFree    Tier = "free"
	TierPaid    Tier = "paid"
	TierPremium Tier = "premium"
)

// Config is the top-level ag3nts configuration.
type Config struct {
	General      GeneralConfig            `toml:"general"`
	Node         NodeConfig               `toml:"node"`
	Workflows    WorkflowsConfig          `toml:"workflows"`
	Orchestrator OrchestratorConfig       `toml:"orchestrator"`
	Agents       map[string]AgentConfig   `toml:"agents"`
	Routing      RoutingConfig            `toml:"routing"`
	LLM          LLMConfig                `toml:"llm"`
}

// LLMConfig holds settings for the local LLM orchestrator (Ollama).
type LLMConfig struct {
	Enabled       bool   `toml:"enabled"`
	Endpoint      string `toml:"endpoint"`
	ModelsPath    string `toml:"models_path"`
	HeadModel     string `toml:"head_model"`
	SystemPrompt  string `toml:"system_prompt"`
	MaxContext    int    `toml:"max_context"`
}

// OrchestratorConfig holds settings for the multi-agent orchestrator.
type OrchestratorConfig struct {
	Primary        string `toml:"primary"`         // default primary agent name
	MaxConcurrency int    `toml:"max_concurrency"` // max parallel agent executions
	PersistSessions bool  `toml:"persist_sessions"` // save session IDs for resume
}

// AgentConfig defines a single agent backend.
type AgentConfig struct {
	Type         string   `toml:"type"`         // "subprocess" or "http"
	Model        string   `toml:"model"`        // model override
	Endpoint     string   `toml:"endpoint"`     // HTTP endpoint (for type=http)
	Flags        []string `toml:"flags"`        // extra CLI flags (for type=subprocess)
	Capabilities []string `toml:"capabilities"` // declared capabilities for routing
}

// RoutingConfig holds task routing rules.
type RoutingConfig struct {
	Rules []RouteRule `toml:"rules"`
}

// RouteRule maps a task type pattern to an agent with optional fallback.
type RouteRule struct {
	Pattern  string `toml:"pattern"`  // regex matched against task type
	Agent    string `toml:"agent"`    // target agent name
	Fallback string `toml:"fallback"` // fallback agent if primary unavailable
	Priority int    `toml:"priority"` // lower = higher priority
}

// GeneralConfig holds general settings.
type GeneralConfig struct {
	Tier Tier `toml:"tier"`
}

// NodeConfig holds Node.js version settings.
type NodeConfig struct {
	Version string `toml:"version"`
}

// WorkflowsConfig holds workflow management settings.
type WorkflowsConfig struct {
	Active    string                     `toml:"active"`
	Workflows map[string]WorkflowEntry   `toml:"workflow"`
}

// WorkflowEntry represents a single workflow registration.
type WorkflowEntry struct {
	Repo        string    `toml:"repo"`
	Branch      string    `toml:"branch"`
	InstalledAt time.Time `toml:"installed_at"`
}

// Load reads config from the given TOML file path.
// If the file doesn't exist, returns a default config.
func Load(path string) (*Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return cfg, nil
}

// Save writes the config to the given path with owner-only permissions (SR-7).
func (c *Config) Save(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("create config file: %w", err)
	}
	defer f.Close()

	enc := toml.NewEncoder(f)
	if err := enc.Encode(c); err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	// Ensure owner-only permissions on existing files
	_ = os.Chmod(path, 0600)
	return nil
}

// Default returns a config with sensible defaults.
func Default() *Config {
	return &Config{
		General: GeneralConfig{
			Tier: TierFree,
		},
		Node: NodeConfig{
			Version: "22.14.0",
		},
		Workflows: WorkflowsConfig{
			Workflows: make(map[string]WorkflowEntry),
		},
		Orchestrator: OrchestratorConfig{
			MaxConcurrency: 3,
			PersistSessions: true,
		},
		Agents: make(map[string]AgentConfig),
		LLM: LLMConfig{
			Enabled:       false,
			Endpoint:      "http://localhost:11434",
			ModelsPath:    "/Volumes/S990Pro4TB/LLMs/Ollama",
			HeadModel:     "gemma4:31b",
			MaxContext:     256000,
		},
	}
}

// ToolAllowed checks if a tool name is permitted under the current tier.
func (c *Config) ToolAllowed(tool string) bool {
	switch tool {
	case "gemini":
		return true // available on all tiers
	case "codex":
		return c.General.Tier == TierPaid || c.General.Tier == TierPremium
	case "claude":
		return c.General.Tier == TierPremium
	default:
		return false
	}
}

// AllowedTools returns the list of tool names available for the current tier.
func (c *Config) AllowedTools() []string {
	tools := []string{"gemini"}
	if c.General.Tier == TierPaid || c.General.Tier == TierPremium {
		tools = append(tools, "codex")
	}
	if c.General.Tier == TierPremium {
		tools = append(tools, "claude")
	}
	return tools
}

func (c *Config) validate() error {
	switch c.General.Tier {
	case TierFree, TierPaid, TierPremium:
		// valid
	default:
		return fmt.Errorf("unknown tier %q (must be free, paid, or premium)", c.General.Tier)
	}
	return nil
}
