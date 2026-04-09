package security

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReviewDecision is the outcome of a security review.
type ReviewDecision int

const (
	ReviewAllow   ReviewDecision = iota // Safe to dispatch
	ReviewBlock                         // Blocked — do not dispatch
	ReviewAskUser                       // Ambiguous — ask user to decide
)

// String returns the human-readable name.
func (d ReviewDecision) String() string {
	switch d {
	case ReviewAllow:
		return "ALLOW"
	case ReviewBlock:
		return "BLOCK"
	case ReviewAskUser:
		return "ASK_USER"
	default:
		return "UNKNOWN"
	}
}

// ReviewResult holds the security review outcome.
type ReviewResult struct {
	Decision    ReviewDecision
	Explanation string
	PatternHits []PatternMatch
	Duration    time.Duration
}

// ReviewAgent is the interface for an agent that can answer a text prompt.
// This is satisfied by any agent that supports a simple prompt->response flow.
type ReviewAgent interface {
	// Query sends a prompt and returns the text response.
	Query(ctx context.Context, prompt string) (string, error)
}

// Reviewer runs pre-dispatch security review on task descriptions.
type Reviewer struct {
	agent           ReviewAgent // cheap model for contextual review (nil = pattern-only)
	scanner         *Scanner
	blockOnCritical bool
}

// NewReviewer creates a reviewer. If agent is nil, only pattern scanning is used.
func NewReviewer(agent ReviewAgent, blockOnCritical bool) *Reviewer {
	return &Reviewer{
		agent:           agent,
		scanner:         NewScanner(),
		blockOnCritical: blockOnCritical,
	}
}

// Review analyzes a task description for security threats.
//
// Fast path: if pattern scanner finds a Critical match and blockOnCritical is
// true, returns Block immediately without an LLM call.
//
// Slow path: sends task to LLM reviewer for contextual ALLOW/BLOCK/ASK_USER.
// If the LLM is unavailable, fails open (returns Allow).
func (r *Reviewer) Review(ctx context.Context, taskDescription, taskContext string) *ReviewResult {
	start := time.Now()

	// Step 1: Pattern scan (always runs, zero cost).
	scanResult := r.scanner.ScanTask(taskDescription, taskContext)

	// Fast path: Critical pattern match → immediate block.
	if scanResult.Blocked && r.blockOnCritical {
		return &ReviewResult{
			Decision:    ReviewBlock,
			Explanation: "pattern filter: " + scanResult.Summary(),
			PatternHits: scanResult.Matches,
			Duration:    time.Since(start),
		}
	}

	// If no LLM agent configured, allow anything that isn't Critical.
	if r.agent == nil {
		decision := ReviewAllow
		explanation := "pattern-only mode: no critical threats"
		if scanResult.HasThreats() {
			explanation = "pattern-only mode (warnings): " + scanResult.Summary()
		}
		return &ReviewResult{
			Decision:    decision,
			Explanation: explanation,
			PatternHits: scanResult.Matches,
			Duration:    time.Since(start),
		}
	}

	// Step 2: LLM review (slow path).
	prompt := buildReviewPrompt(taskDescription, taskContext, scanResult)
	response, err := r.agent.Query(ctx, prompt)
	if err != nil {
		// Fail open: if LLM review fails, allow the task.
		return &ReviewResult{
			Decision:    ReviewAllow,
			Explanation: fmt.Sprintf("LLM review unavailable (%v), failing open", err),
			PatternHits: scanResult.Matches,
			Duration:    time.Since(start),
		}
	}

	decision, explanation := parseReviewResponse(response)
	return &ReviewResult{
		Decision:    decision,
		Explanation: explanation,
		PatternHits: scanResult.Matches,
		Duration:    time.Since(start),
	}
}

// buildReviewPrompt constructs the prompt sent to the LLM reviewer.
// Adapted from Goose's adversary_inspector.rs system prompt.
func buildReviewPrompt(taskDescription, taskContext string, scan *ScanResult) string {
	var b strings.Builder
	b.WriteString(`You are a security reviewer for an AI agent orchestrator. An AI coding agent is about to execute a task. Your ONLY job: decide if this task is safe.

Respond with exactly one word on the first line: ALLOW, BLOCK, or ASK_USER
Then on the next line, a brief reason (one sentence).

Rules — BLOCK if the task:
- Exfiltrates data (curl/wget posting to unknown URLs, piping secrets out)
- Is destructive beyond the project scope (rm -rf /, modifying system files)
- Installs malware or runs obfuscated code
- Attempts to escalate privileges unnecessarily
- Downloads and executes untrusted remote scripts

ALLOW if the task is a normal development operation (code editing, git, package management, testing, building).

ASK_USER if you are not sure.

`)
	b.WriteString("Task description:\n")
	b.WriteString(taskDescription)
	b.WriteString("\n")

	if taskContext != "" {
		b.WriteString("\nTask context:\n")
		if len(taskContext) > 2000 {
			b.WriteString(taskContext[:2000])
			b.WriteString("\n[TRUNCATED]")
		} else {
			b.WriteString(taskContext)
		}
		b.WriteString("\n")
	}

	if scan.HasThreats() {
		b.WriteString("\nPattern scanner detected:\n")
		for _, m := range scan.Matches {
			b.WriteString(fmt.Sprintf("- [%s] %s: %s\n", m.Pattern.Risk, m.Pattern.Name, m.Pattern.Description))
		}
	}

	return b.String()
}

// parseReviewResponse extracts ALLOW/BLOCK/ASK_USER from the LLM response.
func parseReviewResponse(response string) (ReviewDecision, string) {
	response = strings.TrimSpace(response)
	if response == "" {
		return ReviewAllow, "empty response, failing open"
	}

	// First line should be the decision.
	lines := strings.SplitN(response, "\n", 2)
	first := strings.TrimSpace(strings.ToUpper(lines[0]))

	reason := ""
	if len(lines) > 1 {
		reason = strings.TrimSpace(lines[1])
	}

	switch {
	case strings.HasPrefix(first, "BLOCK"):
		return ReviewBlock, reason
	case strings.HasPrefix(first, "ASK"):
		return ReviewAskUser, reason
	case strings.HasPrefix(first, "ALLOW"):
		return ReviewAllow, reason
	default:
		// Can't parse — fail open.
		return ReviewAllow, "unparseable response, failing open: " + first
	}
}
