package llm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempTool(t *testing.T, dir, name, yaml string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(yaml), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCustomTools_EmptyDirReturnsNil(t *testing.T) {
	dir := t.TempDir()
	defs, execs, err := LoadCustomTools(dir, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(defs) != 0 || len(execs) != 0 {
		t.Errorf("expected no tools from empty dir, got %d defs, %d execs", len(defs), len(execs))
	}
}

func TestLoadCustomTools_NonexistentDirNotAnError(t *testing.T) {
	defs, execs, err := LoadCustomTools("/path/that/does/not/exist", nil)
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if defs != nil || execs != nil {
		t.Errorf("expected nil returns for missing dir")
	}
}

func TestLoadCustomTools_BasicValid(t *testing.T) {
	dir := t.TempDir()
	writeTempTool(t, dir, "echo.yaml", `
name: echo_test
description: Echoes a message back to the caller.
parameters:
  - key: message
    type: string
    required: true
    description: The message to echo
executor:
  type: shell
  command: ["sh", "-c", "echo $MESSAGE"]
  timeout: 5s
`)

	defs, execs, err := LoadCustomTools(dir, &CustomToolDeps{})
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(defs) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(defs))
	}
	if defs[0].Function.Name != "echo_test" {
		t.Errorf("name = %q, want echo_test", defs[0].Function.Name)
	}
	if !contains(defs[0].Function.Parameters.Required, "message") {
		t.Errorf("missing 'message' in required params")
	}

	// Execute it.
	exec, ok := execs["echo_test"]
	if !ok {
		t.Fatal("executor missing")
	}
	out, err := exec(map[string]any{"message": "hello world"})
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("output %q should contain 'hello world'", out)
	}
}

func TestLoadCustomTools_ReservedNameSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTempTool(t, dir, "bad.yaml", `
name: read_file
description: Shadow of the built-in read_file.
executor:
  type: shell
  command: ["echo", "hi"]
`)

	defs, _, err := LoadCustomTools(dir, nil)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(defs) != 0 {
		t.Errorf("reserved name should be skipped, got %d tools", len(defs))
	}
}

func TestLoadCustomTools_DuplicateNameSkipped(t *testing.T) {
	dir := t.TempDir()
	writeTempTool(t, dir, "a.yaml", `
name: dupe
description: First.
executor:
  type: shell
  command: ["echo", "a"]
`)
	writeTempTool(t, dir, "b.yaml", `
name: dupe
description: Second.
executor:
  type: shell
  command: ["echo", "b"]
`)

	defs, _, err := LoadCustomTools(dir, nil)
	if err != nil {
		t.Fatalf("load error: %v", err)
	}
	if len(defs) != 1 {
		t.Errorf("duplicate name should drop the second, got %d tools", len(defs))
	}
}

func TestLoadCustomTools_MalformedSkipsButContinues(t *testing.T) {
	dir := t.TempDir()
	writeTempTool(t, dir, "bad.yaml", `this is not valid yaml: [[[`)
	writeTempTool(t, dir, "good.yaml", `
name: good_tool
description: Valid tool.
executor:
  type: shell
  command: ["echo", "ok"]
`)

	defs, _, err := LoadCustomTools(dir, nil)
	if err != nil {
		t.Fatalf("load should not fail on one malformed file: %v", err)
	}
	if len(defs) != 1 {
		t.Errorf("expected 1 valid tool, got %d", len(defs))
	}
	if defs[0].Function.Name != "good_tool" {
		t.Errorf("name = %q, want good_tool", defs[0].Function.Name)
	}
}

func TestCustomTool_RequiredParamMissing(t *testing.T) {
	dir := t.TempDir()
	writeTempTool(t, dir, "req.yaml", `
name: req_tool
description: Requires a param.
parameters:
  - key: needed
    type: string
    required: true
executor:
  type: shell
  command: ["echo", "$NEEDED"]
`)

	_, execs, err := LoadCustomTools(dir, &CustomToolDeps{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = execs["req_tool"](map[string]any{})
	if err == nil {
		t.Error("expected error for missing required param")
	}
	if !strings.Contains(err.Error(), "needed") {
		t.Errorf("error should mention param name: %v", err)
	}
}

func TestCustomTool_TimeoutKillsRunaway(t *testing.T) {
	dir := t.TempDir()
	writeTempTool(t, dir, "slow.yaml", `
name: slow_tool
description: Sleeps longer than its timeout.
executor:
  type: shell
  command: ["sh", "-c", "sleep 10"]
  timeout: 100ms
`)

	_, execs, err := LoadCustomTools(dir, &CustomToolDeps{})
	if err != nil {
		t.Fatal(err)
	}
	out, err := execs["slow_tool"](map[string]any{})
	if err != nil {
		t.Fatalf("exec returned hard error: %v", err)
	}
	if !strings.Contains(out, "timed out") {
		t.Errorf("expected timeout message, got: %q", out)
	}
}

func TestCustomTool_PermissionDenied(t *testing.T) {
	dir := t.TempDir()
	writeTempTool(t, dir, "perm.yaml", `
name: perm_tool
description: Requires permission.
executor:
  type: shell
  command: ["echo", "should-not-run"]
  permission_required: true
`)

	deps := &CustomToolDeps{
		AskPermission: func(tool, action string) bool {
			return false // always deny
		},
	}
	_, execs, err := LoadCustomTools(dir, deps)
	if err != nil {
		t.Fatal(err)
	}
	out, err := execs["perm_tool"](map[string]any{})
	if err != nil {
		t.Fatalf("exec returned hard error: %v", err)
	}
	if !strings.Contains(out, "denied") {
		t.Errorf("expected denial message, got: %q", out)
	}
}

func TestCustomTool_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "missing name",
			yaml: `
description: No name.
executor:
  type: shell
  command: ["echo"]
`,
			want: "name",
		},
		{
			name: "missing description",
			yaml: `
name: nodesc
executor:
  type: shell
  command: ["echo"]
`,
			want: "description",
		},
		{
			name: "missing command",
			yaml: `
name: nocmd
description: x
executor:
  type: shell
`,
			want: "command",
		},
		{
			name: "bad param type",
			yaml: `
name: badparam
description: x
parameters:
  - key: n
    type: magic
executor:
  type: shell
  command: ["echo"]
`,
			want: "unsupported type",
		},
		{
			name: "unknown executor type",
			yaml: `
name: badexec
description: x
executor:
  type: python
  command: ["python", "-c", "print(1)"]
`,
			want: "unsupported executor",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeTempTool(t, dir, "t.yaml", tc.yaml)
			defs, _, _ := LoadCustomTools(dir, nil)
			if len(defs) != 0 {
				t.Errorf("expected malformed tool to be skipped, got %d", len(defs))
			}
		})
	}
}

// contains is a helper for slice membership checks.
func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
