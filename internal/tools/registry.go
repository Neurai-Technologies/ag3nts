package tools

// Registry maps tool names to their implementations.
var Registry = map[string]Tool{
	"claude": &Claude{},
	"codex":  &Codex{},
	"gemini": &Gemini{},
}

// Get returns a tool by name, or nil if not found.
func Get(name string) Tool {
	return Registry[name]
}

// All returns all registered tools in install order.
func All() []Tool {
	return []Tool{Registry["gemini"], Registry["codex"], Registry["claude"]}
}
