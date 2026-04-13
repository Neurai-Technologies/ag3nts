package recipe

import (
	"reflect"
	"testing"
)

func TestParseInlineArgsEmpty(t *testing.T) {
	name, params := ParseInlineArgs("")
	if name != "" {
		t.Errorf("name = %q, want empty", name)
	}
	if len(params) != 0 {
		t.Errorf("params = %v, want empty", params)
	}
}

func TestParseInlineArgsWhitespaceOnly(t *testing.T) {
	name, params := ParseInlineArgs("   ")
	if name != "" || len(params) != 0 {
		t.Errorf("name=%q params=%v, want empty", name, params)
	}
}

func TestParseInlineArgsNameOnly(t *testing.T) {
	name, params := ParseInlineArgs("research")
	if name != "research" {
		t.Errorf("name = %q, want research", name)
	}
	if len(params) != 0 {
		t.Errorf("params = %v, want empty", params)
	}
}

func TestParseInlineArgsSingleParam(t *testing.T) {
	name, params := ParseInlineArgs("research query=MCP")
	if name != "research" {
		t.Errorf("name = %q", name)
	}
	want := map[string]string{"query": "MCP"}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %v, want %v", params, want)
	}
}

// The original bug: multi-word value gets split.
func TestParseInlineArgsMultiWordValue(t *testing.T) {
	name, params := ParseInlineArgs("research query=what is the MCP protocol")
	if name != "research" {
		t.Errorf("name = %q", name)
	}
	want := map[string]string{"query": "what is the MCP protocol"}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %v, want %v", params, want)
	}
}

func TestParseInlineArgsMultipleParams(t *testing.T) {
	name, params := ParseInlineArgs("repair objective=add a Hello command motivation=user asked for it")
	if name != "repair" {
		t.Errorf("name = %q", name)
	}
	want := map[string]string{
		"objective":  "add a Hello command",
		"motivation": "user asked for it",
	}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %v, want %v", params, want)
	}
}

func TestParseInlineArgsThreeParams(t *testing.T) {
	name, params := ParseInlineArgs("repair objective=fix bug motivation=it crashes assumptions=none")
	if name != "repair" {
		t.Errorf("name = %q", name)
	}
	want := map[string]string{
		"objective":   "fix bug",
		"motivation":  "it crashes",
		"assumptions": "none",
	}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %v, want %v", params, want)
	}
}

func TestParseInlineArgsSingleWordValues(t *testing.T) {
	// code-review target=./src focus=security
	name, params := ParseInlineArgs("code-review target=./src focus=security")
	if name != "code-review" {
		t.Errorf("name = %q", name)
	}
	want := map[string]string{
		"target": "./src",
		"focus":  "security",
	}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %v, want %v", params, want)
	}
}

func TestParseInlineArgsDoubleQuoted(t *testing.T) {
	name, params := ParseInlineArgs(`research query="what is MCP protocol"`)
	if name != "research" {
		t.Errorf("name = %q", name)
	}
	want := map[string]string{"query": "what is MCP protocol"}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %v, want %v", params, want)
	}
}

func TestParseInlineArgsSingleQuoted(t *testing.T) {
	name, params := ParseInlineArgs(`research query='what is MCP'`)
	want := map[string]string{"query": "what is MCP"}
	if name != "research" || !reflect.DeepEqual(params, want) {
		t.Errorf("name=%q params=%v", name, params)
	}
}

func TestParseInlineArgsEmptyValue(t *testing.T) {
	name, params := ParseInlineArgs("recipe key=")
	if name != "recipe" {
		t.Errorf("name = %q", name)
	}
	want := map[string]string{"key": ""}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %v, want %v", params, want)
	}
}

func TestParseInlineArgsValueWithEquals(t *testing.T) {
	// A value containing an equals sign should be preserved.
	name, params := ParseInlineArgs("recipe filter=a=b")
	want := map[string]string{"filter": "a=b"}
	if name != "recipe" || !reflect.DeepEqual(params, want) {
		t.Errorf("name=%q params=%v want %v", name, params, want)
	}
}

func TestParseInlineArgsUnderscoreKey(t *testing.T) {
	name, params := ParseInlineArgs("recipe my_key=value here")
	want := map[string]string{"my_key": "value here"}
	if name != "recipe" || !reflect.DeepEqual(params, want) {
		t.Errorf("name=%q params=%v", name, params)
	}
}

func TestParseInlineArgsPreservesCase(t *testing.T) {
	name, params := ParseInlineArgs("Recipe Key=Value")
	want := map[string]string{"Key": "Value"}
	if name != "Recipe" || !reflect.DeepEqual(params, want) {
		t.Errorf("name=%q params=%v", name, params)
	}
}

func TestParseInlineArgsMultiWordWithQuotedFollowup(t *testing.T) {
	// The previous multi-word value stops when a new key=value begins.
	name, params := ParseInlineArgs("r a=first value b=second value")
	want := map[string]string{
		"a": "first value",
		"b": "second value",
	}
	if name != "r" || !reflect.DeepEqual(params, want) {
		t.Errorf("name=%q params=%v want %v", name, params, want)
	}
}

func TestParseInlineArgsRealWorldRepair(t *testing.T) {
	// The exact case that exposed the bug.
	name, params := ParseInlineArgs("repair-lite objective=add a Hello command to an existing Go CLI")
	if name != "repair-lite" {
		t.Errorf("name = %q, want repair-lite", name)
	}
	want := map[string]string{
		"objective": "add a Hello command to an existing Go CLI",
	}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %v", params)
	}
}

func TestParseInlineArgsRealWorldResearch(t *testing.T) {
	// The other case that exposed the bug.
	name, params := ParseInlineArgs("research query=what is the MCP protocol")
	if name != "research" {
		t.Errorf("name = %q, want research", name)
	}
	want := map[string]string{"query": "what is the MCP protocol"}
	if !reflect.DeepEqual(params, want) {
		t.Errorf("params = %v", params)
	}
}

func TestParseInlineArgsBarewordsBeforeKey(t *testing.T) {
	// Tokens before any key= are silently dropped (no positional support).
	name, params := ParseInlineArgs("recipe bareword1 bareword2 key=value")
	want := map[string]string{"key": "value"}
	if name != "recipe" || !reflect.DeepEqual(params, want) {
		t.Errorf("name=%q params=%v", name, params)
	}
}

func TestStripQuotes(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{`"hello"`, "hello"},
		{`'hello'`, "hello"},
		{`hello`, "hello"},
		{`"mismatched'`, `"mismatched'`},
		{``, ``},
		{`"`, `"`},
		{`""`, ``},
	}
	for _, c := range cases {
		got := stripQuotes(c.in)
		if got != c.want {
			t.Errorf("stripQuotes(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
