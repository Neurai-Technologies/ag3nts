package llm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	maxFileSize     = 100 * 1024 // 100KB max file read
	maxOutputSize   = 100 * 1024 // 100KB max command output
	maxSearchResult = 200        // max glob results
	defaultTimeout  = 30         // default command timeout in seconds
)

// RegisterSystemTools returns tool definitions and executors for
// file I/O and shell operations.
func RegisterSystemTools(workDir string) ([]ToolDef, map[string]ToolExecutor) {
	defs := []ToolDef{
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "read_file",
				Description: "Read the contents of a file. Returns the file content as text.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"path":   {Type: "string", Description: "Absolute or relative file path"},
						"offset": {Type: "integer", Description: "Line number to start reading from (0-based, optional)"},
						"limit":  {Type: "integer", Description: "Maximum number of lines to read (optional, default: all)"},
					},
					Required: []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "write_file",
				Description: "Write content to a file. Creates parent directories if needed. Overwrites existing files.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"path":    {Type: "string", Description: "Absolute or relative file path"},
						"content": {Type: "string", Description: "Content to write to the file"},
					},
					Required: []string{"path", "content"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "run_command",
				Description: "Execute a shell command and return its stdout and stderr. Use for build, test, git, and other CLI operations.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"command":         {Type: "string", Description: "Shell command to execute"},
						"timeout_seconds": {Type: "integer", Description: "Timeout in seconds (optional, default: 30)"},
					},
					Required: []string{"command"},
				},
			},
		},
		{
			Type: "function",
			Function: ToolFunction{
				Name:        "search_files",
				Description: "Search for files matching a glob pattern. Returns a list of matching file paths.",
				Parameters: ToolFunctionParams{
					Type: "object",
					Properties: map[string]ToolParamProp{
						"pattern": {Type: "string", Description: "Glob pattern (e.g. '**/*.go', 'internal/**/*.go')"},
						"path":    {Type: "string", Description: "Directory to search in (optional, defaults to working directory)"},
					},
					Required: []string{"pattern"},
				},
			},
		},
	}

	executors := map[string]ToolExecutor{
		"read_file":    toolReadFile(workDir),
		"write_file":   toolWriteFile(workDir),
		"run_command":  toolRunCommand(workDir),
		"search_files": toolSearchFiles(workDir),
	}

	return defs, executors
}

// toolReadFile reads a file's contents, capped at maxFileSize.
func toolReadFile(workDir string) ToolExecutor {
	return func(args map[string]any) (string, error) {
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return "", fmt.Errorf("path is required")
		}
		path = resolvePath(workDir, path)

		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}

		if len(data) > maxFileSize {
			data = data[:maxFileSize]
			return string(data) + "\n[TRUNCATED at 100KB]", nil
		}

		// Handle offset/limit for line-based reading.
		offset, _ := toInt(args["offset"])
		limit, _ := toInt(args["limit"])

		if offset > 0 || limit > 0 {
			lines := strings.Split(string(data), "\n")
			if offset >= len(lines) {
				return "", fmt.Errorf("offset %d exceeds file length (%d lines)", offset, len(lines))
			}
			if offset > 0 {
				lines = lines[offset:]
			}
			if limit > 0 && limit < len(lines) {
				lines = lines[:limit]
			}
			return strings.Join(lines, "\n"), nil
		}

		return string(data), nil
	}
}

// toolWriteFile writes content to a file, creating directories as needed.
func toolWriteFile(workDir string) ToolExecutor {
	return func(args map[string]any) (string, error) {
		path, ok := args["path"].(string)
		if !ok || path == "" {
			return "", fmt.Errorf("path is required")
		}
		content, ok := args["content"].(string)
		if !ok {
			return "", fmt.Errorf("content is required")
		}

		path = resolvePath(workDir, path)

		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", fmt.Errorf("create directory: %w", err)
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return "", fmt.Errorf("write %s: %w", path, err)
		}

		return fmt.Sprintf("Written %d bytes to %s", len(content), path), nil
	}
}

// toolRunCommand executes a shell command with timeout and filtered env.
func toolRunCommand(workDir string) ToolExecutor {
	return func(args map[string]any) (string, error) {
		command, ok := args["command"].(string)
		if !ok || command == "" {
			return "", fmt.Errorf("command is required")
		}

		// Block interactive/privileged commands that hang in a subprocess.
		trimmed := strings.TrimSpace(command)
		if strings.HasPrefix(trimmed, "sudo ") {
			return "", fmt.Errorf("sudo is not supported — run without sudo or ask the user to run it manually")
		}

		timeout := defaultTimeout
		if t, ok := toInt(args["timeout_seconds"]); ok && t > 0 {
			timeout = t
		}
		if timeout > 300 {
			timeout = 300 // hard cap at 5 minutes
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = workDir
		cmd.Env = safeEnv()

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()

		out := stdout.String()
		errOut := stderr.String()

		if len(out) > maxOutputSize {
			out = out[:maxOutputSize] + "\n[TRUNCATED]"
		}
		if len(errOut) > maxOutputSize {
			errOut = errOut[:maxOutputSize] + "\n[TRUNCATED]"
		}

		result := out
		if errOut != "" {
			result += "\nSTDERR:\n" + errOut
		}
		if err != nil {
			result += fmt.Sprintf("\nExit: %v", err)
		}

		return result, nil
	}
}

// toolSearchFiles searches for files matching a glob pattern.
func toolSearchFiles(workDir string) ToolExecutor {
	return func(args map[string]any) (string, error) {
		pattern, ok := args["pattern"].(string)
		if !ok || pattern == "" {
			return "", fmt.Errorf("pattern is required")
		}

		searchDir := workDir
		if p, ok := args["path"].(string); ok && p != "" {
			searchDir = resolvePath(workDir, p)
		}

		fullPattern := filepath.Join(searchDir, pattern)
		matches, err := filepath.Glob(fullPattern)
		if err != nil {
			return "", fmt.Errorf("glob %s: %w", fullPattern, err)
		}

		if len(matches) > maxSearchResult {
			matches = matches[:maxSearchResult]
		}

		if len(matches) == 0 {
			return "No files found matching: " + pattern, nil
		}

		// Return paths relative to workDir for readability.
		var lines []string
		for _, m := range matches {
			rel, err := filepath.Rel(workDir, m)
			if err != nil {
				rel = m
			}
			lines = append(lines, rel)
		}

		return strings.Join(lines, "\n"), nil
	}
}

// resolvePath resolves a path relative to workDir. Absolute paths pass through.
func resolvePath(workDir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(workDir, path))
}

// safeEnv returns a filtered set of environment variables (SR-8 pattern).
func safeEnv() []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "USER": true, "TERM": true,
		"LANG": true, "LC_ALL": true, "SHELL": true, "TMPDIR": true,
		"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
	}
	var env []string
	for _, e := range os.Environ() {
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			if allowed[e[:idx]] {
				env = append(env, e)
			}
		}
	}
	return env
}

// toInt extracts an int from a map value (JSON numbers arrive as float64).
func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case nil:
		return 0, false
	default:
		return 0, false
	}
}
