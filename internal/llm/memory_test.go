package llm

import (
	"strings"
	"testing"
)

func TestMemoryStoreAndRecall(t *testing.T) {
	m := NewMemory("") // no disk persistence in tests

	m.Store("agent-a", "finding", "Redis cache warm-up takes 120ms on average.")
	m.Store("agent-b", "decision", "PostgreSQL connection pool reduced timeout errors in staging.")
	m.Store("agent-c", "file_summary", "Setup script now validates symlink targets before linking.")

	matchQuery := "postgresql timeout errors"
	matchResult := m.Recall(matchQuery)
	expectedContent := "PostgreSQL connection pool reduced timeout errors in staging."
	if !strings.Contains(matchResult, expectedContent) {
		t.Errorf("Recall(%q) = %q, want result to contain %q", matchQuery, matchResult, expectedContent)
	}

	noMatchQuery := "quasarflux999"
	noMatchResult := m.Recall(noMatchQuery)
	expectedNoMatch := "No relevant findings found for: " + noMatchQuery
	if noMatchResult != expectedNoMatch {
		t.Errorf("Recall(%q) = %q, want %q", noMatchQuery, noMatchResult, expectedNoMatch)
	}
}
