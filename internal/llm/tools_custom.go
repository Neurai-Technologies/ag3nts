package llm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// maxCustomToolOutput caps the amount of text a custom tool can return
// to the model. Matches the cap used by other tool outputs so a runaway
// command can't blow the context window.
const maxCustomToolOutput = 100 * 1024

// defaultCustomToolTimeout is applied when a custom tool's YAML doesn't
// specify one. Matches the default used by run_command.
const defaultCustomToolTimeout = 30 * time.Second

// maxCustomToolTimeout is the upper bound we enforce on user-declared
// timeouts to prevent a runaway or malicious tool from hanging
// indefinitely. Ten minutes is long enough for heavy work (a build,
// a database query) but short enough that the user will notice.
const maxCustomToolTimeout = 10 * time.Minute

// CustomTool is a user-defined tool loaded from YAML under
// config/tools/<name>.yaml. It is registered alongside the built-in
// system and routing tools and appears to the model as a first-class
// function call.
type CustomTool struct {
	Name        string              `yaml:"name"`
	Description string              `yaml:"description"`
	Parameters  []CustomToolParam   `yaml:"parameters"`
	Executor    CustomToolExecutor  `yaml:"executor"`
}

// CustomToolParam describes one input parameter of a custom tool.
// Mirrors the JSON-schema-lite used by the built-in tools so we can
// convert cleanly into ToolDef / ToolFunctionParams.
type CustomToolParam struct {
	Key         string `yaml:"key"`
	Type        string `yaml:"type"`        // string, integer, number, boolean
	Required    bool   `yaml:"required"`
	Description string `yaml:"description"`
}

// CustomToolExecutor declares how a custom tool is executed. Only
// "shell" is supported at v1. The command is an explicit argv slice
// — shell metacharacters are NOT interpreted, which prevents shell
// injection via tool arguments. Parameter values are passed to the
// command as environment variables (key name uppercased) rather
// than being string-interpolated into argv.
type CustomToolExecutor struct {
	Type               string            `yaml:"type"`     // only "shell" supported at v1
	Command            []string          `yaml:"command"`  // argv; empty means error
	Env                map[string]string `yaml:"env"`      // static env overlay
	TimeoutRaw         string            `yaml:"timeout"`  // "30s", "2m", "500ms"
	PermissionRequired bool              `yaml:"permission_required"`

	// Parsed after loading.
	timeout time.Duration
}

// LoadCustomTools scans a directory for *.yaml files, parses each as
// a CustomTool, validates them, and returns the resulting ToolDef +
// executor map ready to register with an AgentLoop. Non-existent or
// empty directories return (nil, nil, nil) — missing is not an error
// because custom tools are fully optional.
//
// Individual malformed files are skipped with a warning rather than
// aborting the whole load, so one broken YAML doesn't prevent the
// rest of the user's tools from being available.
func LoadCustomTools(dir string, deps *CustomToolDeps) ([]ToolDef, map[string]ToolExecutor, error) {
	if dir == "" {
		return nil, nil, nil
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("stat custom tools dir: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("custom tools path %s is not a directory", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("read custom tools dir: %w", err)
	}

	var defs []ToolDef
	execs := make(map[string]ToolExecutor)
	seenNames := make(map[string]bool)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		path := filepath.Join(dir, name)

		tool, err := loadCustomToolFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "⚠ custom tool %s: %v\n", name, err)
			continue
		}

		// Reject collisions with built-ins and with earlier custom tools.
		if isReservedToolName(tool.Name) {
			fmt.Fprintf(os.Stderr, "⚠ custom tool %s: name %q conflicts with built-in tool (skipped)\n", name, tool.Name)
			continue
		}
		if seenNames[tool.Name] {
			fmt.Fprintf(os.Stderr, "⚠ custom tool %s: duplicate name %q (skipped)\n", name, tool.Name)
			continue
		}
		seenNames[tool.Name] = true

		defs = append(defs, tool.toolDef())
		execs[tool.Name] = tool.executor(deps)
	}

	return defs, execs, nil
}

// CustomToolDeps are the runtime dependencies a custom tool needs at
// execution time (working directory for cmd.Dir, permission callback
// for user approval). Passed to LoadCustomTools so the returned
// executor closures carry the right context.
type CustomToolDeps struct {
	WorkDir       string
	AskPermission PermissionFunc // optional
}

// loadCustomToolFile reads a YAML file and returns a validated
// CustomTool. Returns an error if the file is malformed, the schema
// is invalid, or required fields are missing.
func loadCustomToolFile(path string) (*CustomTool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var tool CustomTool
	if err := yaml.Unmarshal(data, &tool); err != nil {
		return nil, fmt.Errorf("parse yaml: %w", err)
	}

	if err := tool.validate(); err != nil {
		return nil, err
	}
	return &tool, nil
}

// validate checks that the custom tool has the required fields and
// a supported executor. Also parses and clamps the timeout.
func (c *CustomTool) validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.Description == "" {
		return fmt.Errorf("description is required")
	}

	// Parameter type allowlist.
	for _, p := range c.Parameters {
		if p.Key == "" {
			return fmt.Errorf("parameter has no key")
		}
		switch p.Type {
		case "string", "integer", "number", "boolean":
			// valid
		case "":
			return fmt.Errorf("parameter %q has no type", p.Key)
		default:
			return fmt.Errorf("parameter %q has unsupported type %q", p.Key, p.Type)
		}
	}

	// Executor.
	switch c.Executor.Type {
	case "shell", "":
		c.Executor.Type = "shell" // default
	default:
		return fmt.Errorf("unsupported executor type %q (only 'shell' is supported)", c.Executor.Type)
	}
	if len(c.Executor.Command) == 0 {
		return fmt.Errorf("executor.command is required (argv array)")
	}

	// Timeout parse + clamp.
	if c.Executor.TimeoutRaw == "" {
		c.Executor.timeout = defaultCustomToolTimeout
	} else {
		d, err := time.ParseDuration(c.Executor.TimeoutRaw)
		if err != nil {
			return fmt.Errorf("executor.timeout %q invalid: %w", c.Executor.TimeoutRaw, err)
		}
		c.Executor.timeout = d
	}
	if c.Executor.timeout > maxCustomToolTimeout {
		c.Executor.timeout = maxCustomToolTimeout
	}
	if c.Executor.timeout <= 0 {
		c.Executor.timeout = defaultCustomToolTimeout
	}

	return nil
}

// toolDef converts the custom tool into a ToolDef the AgentLoop can
// register and expose to the model.
func (c *CustomTool) toolDef() ToolDef {
	props := make(map[string]ToolParamProp, len(c.Parameters))
	var required []string
	for _, p := range c.Parameters {
		props[p.Key] = ToolParamProp{
			Type:        p.Type,
			Description: p.Description,
		}
		if p.Required {
			required = append(required, p.Key)
		}
	}
	return ToolDef{
		Type: "function",
		Function: ToolFunction{
			Name:        c.Name,
			Description: c.Description,
			Parameters: ToolFunctionParams{
				Type:       "object",
				Properties: props,
				Required:   required,
			},
		},
	}
}

// executor returns a ToolExecutor closure that runs the tool's
// configured command. The closure captures the deps for permission
// prompts and working directory.
func (c *CustomTool) executor(deps *CustomToolDeps) ToolExecutor {
	tool := *c // snapshot; closure must not mutate loader state
	return func(args map[string]any) (string, error) {
		// Required-param check.
		for _, p := range tool.Parameters {
			if !p.Required {
				continue
			}
			if _, ok := args[p.Key]; !ok {
				return "", fmt.Errorf("missing required parameter %q", p.Key)
			}
		}

		// Permission prompt.
		if tool.Executor.PermissionRequired && deps != nil && deps.AskPermission != nil {
			preview := tool.callPreview(args)
			if !deps.AskPermission(tool.Name, preview) {
				return "Permission denied by user.", nil
			}
		}

		// Build environment: parent env + parameter-derived env + static
		// env overlay. Parameters become uppercase env vars so shell
		// commands can reference them via $PARAM_NAME without risk of
		// shell injection (the values are never interpolated into argv).
		env := os.Environ()
		for key, value := range args {
			env = append(env, fmt.Sprintf("%s=%s", strings.ToUpper(key), fmt.Sprint(value)))
		}
		for k, v := range tool.Executor.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}

		// Execute with timeout.
		ctx, cancel := context.WithTimeout(context.Background(), tool.Executor.timeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, tool.Executor.Command[0], tool.Executor.Command[1:]...)
		cmd.Env = env
		if deps != nil && deps.WorkDir != "" {
			cmd.Dir = deps.WorkDir
		}

		out, err := cmd.CombinedOutput()
		output := string(out)
		if len(output) > maxCustomToolOutput {
			output = output[:maxCustomToolOutput] + "\n[TRUNCATED]"
		}
		if ctx.Err() == context.DeadlineExceeded {
			return "Tool timed out after " + tool.Executor.timeout.String() + "\n" + output, nil
		}
		if err != nil {
			return fmt.Sprintf("Tool error: %v\n%s", err, output), nil
		}
		if output == "" {
			return "(no output)", nil
		}
		return output, nil
	}
}

// callPreview returns a one-line human-readable summary of a tool
// invocation used in the permission prompt. Example:
//   query_db sql="SELECT * FROM users"
func (c *CustomTool) callPreview(args map[string]any) string {
	var parts []string
	parts = append(parts, c.Name)
	for _, p := range c.Parameters {
		v, ok := args[p.Key]
		if !ok {
			continue
		}
		s := fmt.Sprint(v)
		if len(s) > 60 {
			s = s[:57] + "..."
		}
		parts = append(parts, fmt.Sprintf("%s=%q", p.Key, s))
	}
	return strings.Join(parts, " ")
}

// isReservedToolName returns true if the given name collides with
// a built-in system or routing tool. Custom tools with reserved
// names are skipped at load time.
func isReservedToolName(name string) bool {
	switch name {
	case "read_file", "write_file", "run_command", "search_files":
		return true // system tools
	case "recall", "store", "web_research", "code_task", "implement":
		return true // routing tools
	case "ask_model": // reserved for future model-routing tool
		return true
	}
	return false
}
