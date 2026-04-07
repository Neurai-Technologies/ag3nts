// Package llm implements a local LLM orchestrator using Ollama.
// Qwen 3.5 122B runs as the head orchestrator with native tool calling,
// delegating to Gemma 4 (reasoning), Llama 4 Scout (large context),
// and external CLI agents (Gemini, Claude, Codex) as needed.
package llm

// Role constants for Ollama API messages.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is an Ollama chat message with optional tool calls.
type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall represents a function call requested by the model.
type ToolCall struct {
	ID       string           `json:"id,omitempty"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction holds the name and arguments of a tool call.
type ToolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToolDef defines a tool for the Ollama API's "tools" parameter.
type ToolDef struct {
	Type     string       `json:"type"` // always "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction describes a callable function.
type ToolFunction struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Parameters  ToolFunctionParams `json:"parameters"`
}

// ToolFunctionParams describes the parameters schema (JSON Schema subset).
type ToolFunctionParams struct {
	Type       string                   `json:"type"` // "object"
	Properties map[string]ToolParamProp `json:"properties"`
	Required   []string                 `json:"required"`
}

// ToolParamProp describes a single parameter.
type ToolParamProp struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
}

// ModelRole identifies a model's purpose in the orchestration.
type ModelRole int

const (
	ModelHead     ModelRole = iota // Qwen 3.5 122B: orchestrator brain
	ModelReasoner                  // Gemma 4 31B: deep reasoning
	ModelAnalyzer                  // Llama 4 Scout: large context analysis
)

// ModelConfig describes a local model managed through Ollama.
type ModelConfig struct {
	Name       string    // Ollama model name (e.g. "qwen3.5:122b")
	Role       ModelRole
	ContextLen int       // Max context window in tokens
	KeepAlive  string    // Ollama keep_alive parameter (e.g. "30m", "-1" for permanent)
}

// ToolExecutor is the function signature for tool implementations.
type ToolExecutor func(args map[string]any) (string, error)
