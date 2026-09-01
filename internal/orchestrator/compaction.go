package orchestrator

import (
	"context"
	"fmt"
	"strings"

	"github.com/rohanrgit/ag3nts/internal/logging"
)

// CompactionLevel controls how aggressively context is compacted.
type CompactionLevel int

const (
	CompactNone       CompactionLevel = iota // 0%: keep all
	CompactLight                             // 10%: remove progress/init events
	CompactModerate                          // 20%: remove tool results, keep messages
	CompactHeavy                             // 50%: keep only task outputs
	CompactAggressive                        // 100%: single summary via LLM
)

// String returns the level name.
func (l CompactionLevel) String() string {
	switch l {
	case CompactNone:
		return "none"
	case CompactLight:
		return "light"
	case CompactModerate:
		return "moderate"
	case CompactHeavy:
		return "heavy"
	case CompactAggressive:
		return "aggressive"
	default:
		return "unknown"
	}
}

// CompactAgent is the interface for an agent that can summarize text.
type CompactAgent interface {
	Query(ctx context.Context, prompt string) (string, error)
}

// Compactor manages progressive context compaction.
type Compactor struct {
	summarizer CompactAgent    // cheap model for summarization (nil = no LLM compaction)
	threshold  float64         // trigger at this fraction of maxTokens (default: 0.80)
	maxTokens  int             // estimated max context window
	logger     *logging.Logger // optional logger
}

// NewCompactor creates a compactor. If summarizer is nil, only non-LLM
// compaction levels are available (Light, Moderate, Heavy).
func NewCompactor(summarizer CompactAgent, maxTokens int, logger *logging.Logger) *Compactor {
	if maxTokens <= 0 {
		maxTokens = 128000 // Claude default
	}
	return &Compactor{
		summarizer: summarizer,
		threshold:  0.80,
		maxTokens:  maxTokens,
		logger:     logger,
	}
}

// CheckAndCompact evaluates current token usage and applies progressive
// compaction if the context exceeds the threshold.
//
// Returns the (possibly compacted) context string.
func (c *Compactor) CheckAndCompact(ctx context.Context, currentContext string) string {
	tokenEstimate := estimateTokens(currentContext)
	ratio := float64(tokenEstimate) / float64(c.maxTokens)

	if ratio < c.threshold {
		return currentContext
	}

	// Try each level progressively until we're under 70% of max.
	target := int(float64(c.maxTokens) * 0.70)
	levels := []CompactionLevel{CompactLight, CompactModerate, CompactHeavy, CompactAggressive}

	for _, level := range levels {
		compacted := c.compact(ctx, currentContext, level)
		newEstimate := estimateTokens(compacted)

		if c.logger != nil {
			c.logger.LogWith(logging.LevelInfo, "compaction",
				fmt.Sprintf("level=%s: %d→%d tokens (target=%d)", level, tokenEstimate, newEstimate, target),
				map[string]any{"level": level.String(), "before": tokenEstimate, "after": newEstimate})
		}

		if newEstimate <= target {
			return compacted
		}
		currentContext = compacted
		tokenEstimate = newEstimate
	}

	// If still over target after all levels, return whatever we have.
	return currentContext
}

// compact applies a specific compaction level to the context string.
func (c *Compactor) compact(ctx context.Context, text string, level CompactionLevel) string {
	switch level {
	case CompactLight:
		return removeByEventKind(text, "progress", "init")
	case CompactModerate:
		return removeByEventKind(text, "progress", "init", "tool_result", "reasoning")
	case CompactHeavy:
		return keepOnlyTaskOutputs(text)
	case CompactAggressive:
		return c.summarize(ctx, text)
	default:
		return text
	}
}

// summarize uses the LLM to produce a condensed version of the context.
func (c *Compactor) summarize(ctx context.Context, text string) string {
	if c.summarizer == nil {
		// No LLM available — fall back to heavy truncation.
		return keepOnlyTaskOutputs(text)
	}

	// Cap the input to the summarizer to avoid sending enormous prompts.
	if len(text) > 100000 {
		text = text[:100000] + "\n[TRUNCATED FOR SUMMARIZATION]"
	}

	prompt := "Summarize the following multi-agent conversation context. Preserve all file paths, decisions made, task results, and actionable information. Remove redundant tool output and intermediate progress. Be concise.\n\n" + text

	result, err := c.summarizer.Query(ctx, prompt)
	if err != nil {
		// Summarization failed — fall back to heavy.
		return keepOnlyTaskOutputs(text)
	}
	return "[COMPACTED SUMMARY]\n" + result
}

// removeByEventKind removes lines that look like event markers of the given kinds.
// This is a heuristic based on the format used by buildContext and drainEvents.
func removeByEventKind(text string, kinds ...string) string {
	kindSet := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		kindSet[k] = true
	}

	lines := strings.Split(text, "\n")
	var kept []string
	skip := false
	for _, line := range lines {
		// Check if this line starts an event block we want to remove.
		shouldSkip := false
		for kind := range kindSet {
			if strings.Contains(line, "["+kind+"]") || strings.HasPrefix(line, kind+":") {
				shouldSkip = true
				break
			}
		}
		if shouldSkip {
			skip = true
			continue
		}
		// Reset skip on new section markers.
		if strings.HasPrefix(line, "=== ") || strings.HasPrefix(line, "---") {
			skip = false
		}
		if !skip {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// keepOnlyTaskOutputs extracts only the "=== Result from task" sections,
// discarding everything else.
func keepOnlyTaskOutputs(text string) string {
	sections := strings.Split(text, "=== Result from task ")
	if len(sections) <= 1 {
		// No task output sections — truncate to first 20K chars.
		if len(text) > 20000 {
			return text[:20000] + "\n[HEAVY TRUNCATION]"
		}
		return text
	}

	var kept []string
	for _, section := range sections[1:] { // skip preamble before first ===
		// Truncate each section to 10KB.
		if len(section) > 10240 {
			section = section[:10240] + "\n[TRUNCATED]"
		}
		kept = append(kept, "=== Result from task "+section)
	}
	return strings.Join(kept, "\n\n")
}

// estimateTokens gives a rough token count. Uses the ~4 chars/token heuristic.
func estimateTokens(text string) int {
	return len(text) / 4
}

