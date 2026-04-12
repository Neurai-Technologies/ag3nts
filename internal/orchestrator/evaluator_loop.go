package orchestrator

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	m3m0ry "github.com/rohanrgit/ag3nts/internal/context"
	"github.com/rohanrgit/ag3nts/internal/logging"
	"github.com/rohanrgit/ag3nts/internal/task"
)

// evaluatorTrailerRE matches "[EVALUATOR_LOOP: target=<id> retries=N attempt=N]"
// which is appended to evaluator task descriptions by recipe.Expand.
var evaluatorTrailerRE = regexp.MustCompile(
	`\[EVALUATOR_LOOP:\s*target=(\S+)\s+retries=(\d+)\s+attempt=(\d+)\]`,
)

// retrySuffixRE matches a trailing "-retryN" for stripping.
var retrySuffixRE = regexp.MustCompile(`-retry\d+$`)

// stripRetrySuffix removes a trailing "-retryN" from an ID, returning the
// base (original) ID. Used to derive clean retry IDs across multiple rounds.
func stripRetrySuffix(id string) string {
	return retrySuffixRE.ReplaceAllString(id, "")
}

// evaluatorMeta holds parsed evaluator trailer values.
type evaluatorMeta struct {
	targetID string // task ID being evaluated (prefixed)
	maxRetry int
	attempt  int
}

// parseEvaluatorTrailer extracts evaluator metadata from a task description.
// Returns nil if the task is not an evaluator.
func parseEvaluatorTrailer(description string) *evaluatorMeta {
	m := evaluatorTrailerRE.FindStringSubmatch(description)
	if m == nil {
		return nil
	}
	retries, _ := strconv.Atoi(m[2])
	attempt, _ := strconv.Atoi(m[3])
	return &evaluatorMeta{
		targetID: m[1],
		maxRetry: retries,
		attempt:  attempt,
	}
}

// parseEvaluatorVerdict extracts ACCEPT / REJECT / ASK_USER from the first
// line of an evaluator's output. Falls back to keyword search if first-line
// parse fails (LLM didn't follow instructions).
func parseEvaluatorVerdict(output string) (verdict string, reason string) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return "REJECT", "empty output"
	}

	lines := strings.SplitN(trimmed, "\n", 2)
	first := strings.TrimSpace(lines[0])
	upper := strings.ToUpper(first)

	switch {
	case strings.HasPrefix(upper, "ACCEPT"):
		return "ACCEPT", strings.TrimPrefix(first, first[:6])
	case strings.HasPrefix(upper, "REJECT"):
		return "REJECT", strings.TrimPrefix(first, first[:6])
	}

	// Fallback: keyword search in the first 500 chars.
	head := trimmed
	if len(head) > 500 {
		head = head[:500]
	}
	upperHead := strings.ToUpper(head)
	if strings.Contains(upperHead, "APPROVED") || strings.Contains(upperHead, "ACCEPT") {
		return "ACCEPT", "inferred from keyword"
	}
	if strings.Contains(upperHead, "REJECTED") || strings.Contains(upperHead, "REJECT") {
		return "REJECT", "inferred from keyword"
	}

	// Default: treat unparseable output as REJECT to be safe.
	return "REJECT", "unparseable verdict"
}

// handleEvaluatorCompletion is called after an evaluator task completes.
// If the verdict is REJECT and retries remain, it spawns retry tasks
// (a new implementer task + a new evaluator task) wired into the DAG.
// If the verdict is ACCEPT or retries are exhausted, the loop terminates.
//
// The trailer format evolves across attempts:
//
//	[EVALUATOR_LOOP: target=<id> retries=N attempt=0]    (first eval)
//	[EVALUATOR_LOOP: target=<id>-retry1 retries=N attempt=1] (first retry eval)
func (o *Orchestrator) handleEvaluatorCompletion(evalTask *task.Task, result *task.Result) {
	meta := parseEvaluatorTrailer(evalTask.Description)
	if meta == nil {
		return // not an evaluator task
	}

	verdict, reason := parseEvaluatorVerdict(result.Output)

	if o.logger != nil {
		o.logger.LogAgentWith(logging.LevelInfo, "evaluator", "", evalTask.ID,
			fmt.Sprintf("verdict=%s attempt=%d/%d", verdict, meta.attempt, meta.maxRetry),
			map[string]any{"reason": reason, "target": meta.targetID})
	}

	// Append to m3m0ry for audit trail.
	if o.rollingCtx != nil {
		_ = o.rollingCtx.Append(&m3m0ry.Chunk{
			SessionID: o.sessID,
			TaskID:    evalTask.ID,
			Kind:      "evaluator_verdict",
			Content:   fmt.Sprintf("%s: %s", verdict, reason),
			CreatedAt: time.Now(),
		})
	}

	// ACCEPT or max retries exhausted: loop terminates.
	if verdict == "ACCEPT" {
		return
	}
	if meta.attempt >= meta.maxRetry {
		if o.logger != nil {
			o.logger.Warn("evaluator", fmt.Sprintf("max retries exhausted for %s", meta.targetID))
		}
		// Mark the original target as failed so downstream tasks don't advance.
		if target := o.queue.Get(meta.targetID); target != nil {
			targetResult := &task.Result{
				Error: fmt.Sprintf("evaluator rejected after %d retries", meta.maxRetry),
			}
			_ = o.queue.Update(meta.targetID, task.StatusFailed, targetResult)
		}
		return
	}

	// REJECT: spawn retry impl + retry eval.
	// Derive retry IDs from the BASE IDs (stripped of any prior -retryN suffix)
	// so multiple rounds produce implID-retry1, implID-retry2, etc.
	nextAttempt := meta.attempt + 1
	baseTargetID := stripRetrySuffix(meta.targetID)
	baseEvalID := stripRetrySuffix(evalTask.ID)
	retryImplID := fmt.Sprintf("%s-retry%d", baseTargetID, nextAttempt)
	retryEvalID := fmt.Sprintf("%s-retry%d", baseEvalID, nextAttempt)

	// Retrieve the original implementer to copy its config.
	// Always use the BASE target (not the current retry chain) so we preserve
	// the original task's agent/type/timeout.
	origImpl := o.queue.GetSnapshot(baseTargetID)
	if origImpl == nil {
		if o.logger != nil {
			o.logger.Error("evaluator", fmt.Sprintf("original impl %s not found in queue", meta.targetID))
		}
		return
	}

	// Retry impl: same agent/type/timeout, new ID, depends on the evaluator
	// so it runs AFTER the eval (and has the feedback as context).
	retryImpl := &task.Task{
		ID:          retryImplID,
		Description: fmt.Sprintf("%s\n\n[RETRY %d/%d - evaluator feedback:]\n%s",
			stripEvaluatorTrailer(origImpl.Description), nextAttempt, meta.maxRetry, result.Output),
		Type:        origImpl.Type,
		Agent:       origImpl.Agent,
		Status:      task.StatusPending,
		DependsOn:   []string{evalTask.ID},
		ContextFrom: append([]string{meta.targetID, evalTask.ID}, origImpl.ContextFrom...),
		Timeout:     origImpl.Timeout,
		CreatedAt:   time.Now(),
	}

	// Retry eval: re-evaluates the retry impl, with incremented attempt.
	// Trailer keeps the BASE target so the next round's parse finds the
	// original implementer config for its own retry spawn.
	retryEval := &task.Task{
		ID:          retryEvalID,
		Description: rewriteEvaluatorTrailer(evalTask.Description, baseTargetID, nextAttempt),
		Type:        evalTask.Type,
		Agent:       evalTask.Agent,
		Status:      task.StatusPending,
		DependsOn:   []string{retryImplID},
		ContextFrom: []string{retryImplID, baseTargetID},
		Timeout:     evalTask.Timeout,
		CreatedAt:   time.Now(),
	}

	if err := o.queue.Add(retryImpl); err != nil {
		if o.logger != nil {
			o.logger.Error("evaluator", fmt.Sprintf("add retry impl: %v", err))
		}
		return
	}
	if err := o.queue.Add(retryEval); err != nil {
		if o.logger != nil {
			o.logger.Error("evaluator", fmt.Sprintf("add retry eval: %v", err))
		}
		return
	}

	if o.logger != nil {
		o.logger.Infof("evaluator", "spawned retry %s and eval %s", retryImplID, retryEvalID)
	}
}

// stripEvaluatorTrailer removes the [EVALUATOR_LOOP:] trailer from a description.
func stripEvaluatorTrailer(description string) string {
	loc := evaluatorTrailerRE.FindStringIndex(description)
	if loc == nil {
		return description
	}
	return strings.TrimSpace(description[:loc[0]])
}

// rewriteEvaluatorTrailer updates the trailer with a new target and attempt number.
func rewriteEvaluatorTrailer(description, newTarget string, attempt int) string {
	stripped := stripEvaluatorTrailer(description)
	// Try to recover the retries count from the original trailer.
	retries := 3
	if m := evaluatorTrailerRE.FindStringSubmatch(description); m != nil {
		if r, err := strconv.Atoi(m[2]); err == nil {
			retries = r
		}
	}
	return fmt.Sprintf("%s\n\n[EVALUATOR_LOOP: target=%s retries=%d attempt=%d]",
		stripped, newTarget, retries, attempt)
}
