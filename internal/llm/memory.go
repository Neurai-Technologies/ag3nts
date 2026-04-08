package llm

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Memory stores distilled findings in Go memory with disk persistence.
// No LLM needed — uses TF-IDF keyword search for retrieval.
type Memory struct {
	mu       sync.Mutex
	entries  []MemoryEntry
	filePath string // disk persistence path (empty = no persistence)
}

// MemoryEntry is a single stored finding.
type MemoryEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"`   // which model/tool produced this
	Category string    `json:"category"` // finding, decision, file_summary, error, user_context
	Content  string    `json:"content"`
}

// NewMemory creates the memory layer with optional disk persistence.
func NewMemory(persistPath string) *Memory {
	m := &Memory{
		filePath: persistPath,
	}
	// Load from disk if exists.
	if persistPath != "" {
		_ = m.loadFromDisk()
	}
	return m
}

// Store adds a finding to memory and persists to disk.
func (m *Memory) Store(source, category, content string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.entries = append(m.entries, MemoryEntry{
		Timestamp: time.Now(),
		Source:    source,
		Category: category,
		Content:  content,
	})

	// Persist to disk.
	if m.filePath != "" {
		_ = m.saveToDiskLocked()
	}
}

// Recall searches memory for entries relevant to the query.
// Uses TF-IDF scoring to rank results by relevance.
func (m *Memory) Recall(query string) string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) == 0 {
		return "No findings stored yet."
	}

	// Score each entry against the query.
	type scored struct {
		entry MemoryEntry
		score float64
	}

	queryTerms := tokenize(query)
	if len(queryTerms) == 0 {
		return m.lastN(10)
	}

	// Build document frequency for IDF.
	df := make(map[string]int)
	for _, e := range m.entries {
		seen := make(map[string]bool)
		for _, t := range tokenize(e.Content + " " + e.Category + " " + e.Source) {
			if !seen[t] {
				df[t]++
				seen[t] = true
			}
		}
	}

	numDocs := float64(len(m.entries))
	var results []scored

	for _, e := range m.entries {
		entryTerms := tokenize(e.Content + " " + e.Category + " " + e.Source)
		termFreq := make(map[string]int)
		for _, t := range entryTerms {
			termFreq[t]++
		}

		var score float64
		for _, qt := range queryTerms {
			tf := float64(termFreq[qt])
			if tf > 0 {
				idf := math.Log(numDocs / float64(df[qt]+1))
				score += tf * idf
			}
		}

		if score > 0 {
			results = append(results, scored{entry: e, score: score})
		}
	}

	if len(results) == 0 {
		return "No relevant findings found for: " + query
	}

	// Sort by score descending.
	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	// Return top 10 results.
	limit := 10
	if len(results) < limit {
		limit = len(results)
	}

	var sb strings.Builder
	for i := 0; i < limit; i++ {
		e := results[i].entry
		sb.WriteString(fmt.Sprintf("[%s] [%s] [%s]\n%s\n\n",
			e.Timestamp.Format("15:04:05"), e.Source, e.Category, e.Content))
	}
	return sb.String()
}

// Summary returns a brief overview of memory contents.
func (m *Memory) Summary() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.entries) == 0 {
		return "Memory: empty"
	}

	counts := make(map[string]int)
	totalChars := 0
	for _, e := range m.entries {
		counts[e.Category]++
		totalChars += len(e.Content)
	}

	var parts []string
	for cat, count := range counts {
		parts = append(parts, fmt.Sprintf("%s: %d", cat, count))
	}

	return fmt.Sprintf("Memory: %d entries (~%d tokens) — %s",
		len(m.entries), totalChars/4, strings.Join(parts, ", "))
}

// Len returns the number of stored entries.
func (m *Memory) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// Clear resets memory and removes the disk file.
func (m *Memory) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = nil
	if m.filePath != "" {
		_ = os.Remove(m.filePath)
	}
}

// lastN returns the last N entries formatted as text.
func (m *Memory) lastN(n int) string {
	start := 0
	if len(m.entries) > n {
		start = len(m.entries) - n
	}
	var sb strings.Builder
	for _, e := range m.entries[start:] {
		sb.WriteString(fmt.Sprintf("[%s] [%s] [%s]\n%s\n\n",
			e.Timestamp.Format("15:04:05"), e.Source, e.Category, e.Content))
	}
	return sb.String()
}

// tokenize splits text into lowercase tokens for search.
func tokenize(text string) []string {
	words := strings.Fields(strings.ToLower(text))
	var tokens []string
	for _, w := range words {
		// Strip punctuation.
		w = strings.TrimFunc(w, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9')
		})
		if len(w) > 1 { // skip single chars
			tokens = append(tokens, w)
		}
	}
	return tokens
}

// saveToDiskLocked writes entries to disk. Must hold mu.
func (m *Memory) saveToDiskLocked() error {
	if m.filePath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.filePath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m.entries, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(m.filePath, data, 0644)
}

// loadFromDisk reads entries from disk.
func (m *Memory) loadFromDisk() error {
	data, err := os.ReadFile(m.filePath)
	if err != nil {
		return err // file doesn't exist yet, that's fine
	}
	return json.Unmarshal(data, &m.entries)
}
