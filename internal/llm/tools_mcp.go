package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/rohanrgit/ag3nts/internal/mcp"
)

// MCPServerConfig describes one MCP server to connect to. Populated
// from config.ToolSetConfig entries with type="mcp" in cmd/orchestrate.go.
type MCPServerConfig struct {
	Name    string
	Command string
	Args    []string
	Env     []string // pre-formatted "KEY=VALUE" strings
}

// LoadMCPTools connects to the configured MCP servers via the manager,
// converts their discovered tools into ToolDef + ToolExecutor pairs
// ready for registration with an AgentLoop, and returns them.
//
// Each MCP tool is qualified as "servername__toolname" to avoid
// cross-server collisions. Complex parameter types (object, array)
// are flattened to "string" with a description hint since Gemma passes
// simple key-value args through Ollama.
func LoadMCPTools(manager *mcp.MCPManager, askPermission PermissionFunc) ([]ToolDef, map[string]ToolExecutor) {
	allTools := manager.AllTools()
	if len(allTools) == 0 {
		return nil, nil
	}

	defs := make([]ToolDef, 0, len(allTools))
	execs := make(map[string]ToolExecutor, len(allTools))

	for qualName, tool := range allTools {
		// Skip collisions with built-in tools (unlikely since qualified
		// names include the server prefix, but defensive).
		if isReservedToolName(qualName) {
			fmt.Fprintf(os.Stderr, "⚠ MCP tool %q conflicts with built-in (skipped)\n", qualName)
			continue
		}

		def := mcpSchemaToToolDef(qualName, tool)
		defs = append(defs, def)
		execs[qualName] = mcpToolExecutor(manager, qualName, askPermission)
	}

	return defs, execs
}

// mcpSchemaToToolDef converts an MCP tool's JSON Schema into a ToolDef
// compatible with the Ollama API's tool format. Now preserves array
// and object structure via ToolParamProp.Items and .Properties so
// models that understand JSON Schema get accurate type info.
func mcpSchemaToToolDef(qualName string, tool mcp.MCPTool) ToolDef {
	props := make(map[string]ToolParamProp)
	var required []string

	if len(tool.InputSchema) > 0 {
		var schema struct {
			Type       string                     `json:"type"`
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(tool.InputSchema, &schema); err == nil {
			required = schema.Required
			for pName, pRaw := range schema.Properties {
				props[pName] = parseMCPParamProp(pRaw)
			}
		}
	}

	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        qualName,
			Description: tool.Description,
			Parameters: ToolFunctionParams{
				Type:       "object",
				Properties: props,
				Required:   required,
			},
		},
	}
}

// parseMCPParamProp recursively converts a JSON Schema property into
// a ToolParamProp. Handles string/number/integer/boolean directly;
// arrays get an Items sub-schema; objects get Properties + Required.
// Unknown or missing types default to "string".
func parseMCPParamProp(raw json.RawMessage) ToolParamProp {
	var s struct {
		Type        string                     `json:"type"`
		Description string                     `json:"description"`
		Enum        []string                   `json:"enum"`
		Items       json.RawMessage            `json:"items"`
		Properties  map[string]json.RawMessage `json:"properties"`
		Required    []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return ToolParamProp{Type: "string", Description: "(unparseable schema)"}
	}

	prop := ToolParamProp{
		Type:        s.Type,
		Description: s.Description,
		Enum:        s.Enum,
	}
	if prop.Type == "" {
		prop.Type = "string"
	}

	// Recurse into array items.
	if prop.Type == "array" && len(s.Items) > 0 {
		items := parseMCPParamProp(s.Items)
		prop.Items = &items
	}

	// Recurse into object properties.
	if prop.Type == "object" && len(s.Properties) > 0 {
		prop.Properties = make(map[string]ToolParamProp, len(s.Properties))
		for k, v := range s.Properties {
			prop.Properties[k] = parseMCPParamProp(v)
		}
		prop.Required = s.Required
	}

	return prop
}

// mcpToolExecutor returns a ToolExecutor closure that calls the MCP
// tool through the manager. Includes permission prompt and timeout.
func mcpToolExecutor(manager *mcp.MCPManager, qualName string, askPermission PermissionFunc) ToolExecutor {
	return func(args map[string]any) (string, error) {
		// Permission check.
		if askPermission != nil {
			preview := qualName + " " + formatMCPArgs(args)
			if !askPermission(qualName, preview) {
				return "Permission denied by user.", nil
			}
		}

		argsJSON, err := json.Marshal(args)
		if err != nil {
			return "", fmt.Errorf("marshal args: %w", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		result, err := manager.CallTool(ctx, qualName, argsJSON)
		if err != nil {
			return "", fmt.Errorf("MCP tool %s: %w", qualName, err)
		}

		output := result.ResultText()
		if result.IsError {
			return "MCP tool error: " + output, nil
		}
		if output == "" {
			return "(no output)", nil
		}
		// Cap output size like other tools.
		if len(output) > 100*1024 {
			output = output[:100*1024] + "\n[TRUNCATED]"
		}
		return output, nil
	}
}

// formatMCPArgs builds a compact preview of tool arguments for the
// permission prompt.
func formatMCPArgs(args map[string]any) string {
	var parts []string
	for k, v := range args {
		s := fmt.Sprint(v)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%q", k, s))
	}
	return strings.Join(parts, " ")
}
