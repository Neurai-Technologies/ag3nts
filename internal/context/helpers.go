package context

import (
	"strings"
)

// estimateTokens approximates token count from character count (4 chars/token).
func estimateTokens(text string) int {
	return len(text) / 4
}

// extractKeywords pulls simple keywords from text for retrieval matching.
// Strips punctuation, lowercases, filters words < 4 chars, dedupes, caps at 10.
func extractKeywords(text string) []string {
	if text == "" {
		return nil
	}
	words := strings.Fields(strings.ToLower(text))
	seen := make(map[string]bool)
	var out []string
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?\"'()[]{}—-")
		if len(w) < 4 || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= 10 {
			break
		}
	}
	return out
}

// splitKeywords parses a space-delimited keyword string back into a slice.
func splitKeywords(kw string) []string {
	if kw == "" {
		return nil
	}
	return strings.Fields(kw)
}
