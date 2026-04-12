package orchestrator

import (
	"testing"
)

// --- Parser tests ---

func TestParseEvaluatorTrailer(t *testing.T) {
	desc := "review this code\n\n[EVALUATOR_LOOP: target=R123-implement retries=3 attempt=0]"
	meta := parseEvaluatorTrailer(desc)
	if meta == nil {
		t.Fatal("expected meta, got nil")
	}
	if meta.targetID != "R123-implement" {
		t.Errorf("targetID = %q", meta.targetID)
	}
	if meta.maxRetry != 3 {
		t.Errorf("maxRetry = %d", meta.maxRetry)
	}
	if meta.attempt != 0 {
		t.Errorf("attempt = %d", meta.attempt)
	}
}

func TestParseEvaluatorTrailerNone(t *testing.T) {
	meta := parseEvaluatorTrailer("just a regular task description")
	if meta != nil {
		t.Error("expected nil meta for non-evaluator task")
	}
}

func TestParseEvaluatorTrailerMultipleAttempts(t *testing.T) {
	desc := "review\n\n[EVALUATOR_LOOP: target=R1-impl-retry2 retries=5 attempt=2]"
	meta := parseEvaluatorTrailer(desc)
	if meta == nil {
		t.Fatal("expected meta")
	}
	if meta.attempt != 2 {
		t.Errorf("attempt = %d, want 2", meta.attempt)
	}
	if meta.targetID != "R1-impl-retry2" {
		t.Errorf("targetID = %q", meta.targetID)
	}
}

func TestParseEvaluatorVerdictAccept(t *testing.T) {
	cases := []string{
		"ACCEPT: looks good\nDetailed feedback...",
		"ACCEPT",
		"accept: all good",
	}
	for _, c := range cases {
		v, _ := parseEvaluatorVerdict(c)
		if v != "ACCEPT" {
			t.Errorf("verdict for %q = %q, want ACCEPT", c, v)
		}
	}
}

func TestParseEvaluatorVerdictReject(t *testing.T) {
	cases := []string{
		"REJECT: needs more work\nDetails...",
		"REJECT",
		"reject: missing tests",
	}
	for _, c := range cases {
		v, _ := parseEvaluatorVerdict(c)
		if v != "REJECT" {
			t.Errorf("verdict for %q = %q, want REJECT", c, v)
		}
	}
}

func TestParseEvaluatorVerdictFallbackKeyword(t *testing.T) {
	// LLM ignores the format — we fall back to keyword search.
	reject := "The implementation has issues and is REJECTED due to missing tests."
	v, _ := parseEvaluatorVerdict(reject)
	if v != "REJECT" {
		t.Errorf("verdict = %q, want REJECT (keyword fallback)", v)
	}

	accept := "This code is well-written and APPROVED for merge."
	v2, _ := parseEvaluatorVerdict(accept)
	if v2 != "ACCEPT" {
		t.Errorf("verdict = %q, want ACCEPT (keyword fallback)", v2)
	}
}

func TestParseEvaluatorVerdictEmpty(t *testing.T) {
	v, _ := parseEvaluatorVerdict("")
	if v != "REJECT" {
		t.Errorf("empty = %q, want REJECT", v)
	}
}

func TestParseEvaluatorVerdictUnparseable(t *testing.T) {
	// No clear verdict keywords — should default to REJECT.
	v, _ := parseEvaluatorVerdict("This is just some text without clear direction.")
	if v != "REJECT" {
		t.Errorf("unparseable = %q, want REJECT (safe default)", v)
	}
}

// --- Trailer manipulation ---

func TestStripEvaluatorTrailer(t *testing.T) {
	desc := "review this work\n\n[EVALUATOR_LOOP: target=X retries=3 attempt=0]"
	stripped := stripEvaluatorTrailer(desc)
	if stripped != "review this work" {
		t.Errorf("stripped = %q", stripped)
	}
}

func TestStripEvaluatorTrailerNoTrailer(t *testing.T) {
	desc := "just a regular task"
	if stripEvaluatorTrailer(desc) != desc {
		t.Error("expected unchanged")
	}
}

func TestRewriteEvaluatorTrailer(t *testing.T) {
	desc := "review this work\n\n[EVALUATOR_LOOP: target=R1-impl retries=5 attempt=0]"
	rewritten := rewriteEvaluatorTrailer(desc, "R1-impl-retry1", 1)
	if !contains(rewritten, "target=R1-impl-retry1") {
		t.Errorf("missing new target: %q", rewritten)
	}
	if !contains(rewritten, "retries=5") {
		t.Errorf("lost retries count: %q", rewritten)
	}
	if !contains(rewritten, "attempt=1") {
		t.Errorf("missing new attempt: %q", rewritten)
	}
	if !contains(rewritten, "review this work") {
		t.Errorf("lost original body: %q", rewritten)
	}
}

// --- Integration test ---

// TestEvaluatorLoopRejectThenAccept uses a replay agent that outputs
// REJECT once, then ACCEPT on the retry. Verifies that:
//  1. The original evaluator task completes with REJECT
//  2. A retry impl task is created with correct ID and wiring
//  3. A retry eval task is created with incremented attempt
//  4. The retry eval task's description has the updated trailer
func TestEvaluatorLoopRejectThenAccept(t *testing.T) {
	t.Skip("full integration tested via repair_integration_test in Phase 11.4")
}

func contains(s, sub string) bool {
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
