package context

import (
	"strings"
)

// estimateTokens approximates token count from character count (4 chars/token).
func estimateTokens(text string) int {
	return len(text) / 4
}

// maxKeywordsPerChunk caps how many unique keywords are indexed per
// chunk. 10 was far too aggressive — a 2000-char assistant response
// covers ~300 unique words; indexing only the first 10 means 97% of
// the content is invisible to LIKE-based retrieval. 200 is a
// pragmatic middle ground: one keyword per ~80 chars of content,
// enough to represent the full chunk without bloating the keywords
// column.
const maxKeywordsPerChunk = 200

// minKeywordLen filters out short common words ("the", "and", "is")
// that add noise without improving recall.
const minKeywordLen = 4

// extractKeywords pulls keywords from text for retrieval matching.
// Strips punctuation, lowercases, dedupes, filters words shorter than
// minKeywordLen, caps at maxKeywordsPerChunk. Walks the full text
// (not just the beginning) so keywords sampled from anywhere in the
// content all have a chance to be indexed.
func extractKeywords(text string) []string {
	if text == "" {
		return nil
	}
	words := strings.Fields(strings.ToLower(text))
	seen := make(map[string]bool, len(words))
	out := make([]string, 0, 64)
	for _, w := range words {
		w = strings.Trim(w, ".,;:!?\"'()[]{}—-")
		if len(w) < minKeywordLen || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
		if len(out) >= maxKeywordsPerChunk {
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
