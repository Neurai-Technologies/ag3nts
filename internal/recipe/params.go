package recipe

import (
	"regexp"
	"strings"
)

// keyTokenRE matches a bare "key=" or "key=value" token. A key is a word
// starting with a letter or underscore, followed by letters/digits/underscores,
// then a literal "=". The value (if any) is captured separately.
var keyTokenRE = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)=(.*)$`)

// ParseInlineArgs parses a TUI-style inline recipe invocation like:
//
//	research query=what is the MCP protocol
//	repair objective=add a Hello command motivation=user asked
//	code-review target=./src focus=security
//
// It returns the recipe name and a map of params. Multi-word values are
// supported: any tokens not matching `key=...` are appended to the current
// parameter's value. This allows natural multi-word values without requiring
// quotes.
//
// Quoted values (double or single) are also honored: tokens starting with
// a quote are accumulated until the closing quote, and the quotes are
// stripped from the result.
//
// Returns name="" if args is empty.
func ParseInlineArgs(args string) (name string, params map[string]string) {
	params = make(map[string]string)

	args = strings.TrimSpace(args)
	if args == "" {
		return "", params
	}

	tokens := strings.Fields(args)
	if len(tokens) == 0 {
		return "", params
	}

	name = tokens[0]
	if len(tokens) == 1 {
		return name, params
	}

	var currentKey string
	var currentValue strings.Builder
	inQuote := false
	quoteChar := byte(0)

	flush := func() {
		if currentKey != "" {
			v := currentValue.String()
			// Strip matching outer quotes if still present.
			v = stripQuotes(v)
			params[currentKey] = v
		}
		currentKey = ""
		currentValue.Reset()
	}

	for _, tok := range tokens[1:] {
		// If we're inside a quoted string, keep appending until closing quote.
		if inQuote {
			currentValue.WriteByte(' ')
			currentValue.WriteString(tok)
			if strings.HasSuffix(tok, string(quoteChar)) {
				inQuote = false
				quoteChar = 0
			}
			continue
		}

		// Check if this token starts a new key.
		if m := keyTokenRE.FindStringSubmatch(tok); m != nil {
			flush()
			currentKey = m[1]
			val := m[2]
			// If the value starts with a quote that isn't closed in the same
			// token, enter quote-accumulation mode.
			if len(val) > 0 && (val[0] == '"' || val[0] == '\'') {
				qc := val[0]
				if len(val) == 1 || !strings.HasSuffix(val[1:], string(qc)) {
					inQuote = true
					quoteChar = qc
				}
			}
			currentValue.WriteString(val)
			continue
		}

		// Not a new key — append to current value.
		if currentKey != "" {
			if currentValue.Len() > 0 {
				currentValue.WriteByte(' ')
			}
			currentValue.WriteString(tok)
		}
		// Tokens before any key= are dropped silently (positional args not supported).
	}
	flush()

	return name, params
}

// stripQuotes removes matching outer single or double quotes from a string.
// Returns the input unchanged if quotes don't match.
func stripQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	first, last := s[0], s[len(s)-1]
	if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}
