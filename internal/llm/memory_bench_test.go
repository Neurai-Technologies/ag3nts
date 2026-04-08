package llm

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type benchMemoryEntry struct {
	source   string
	category string
	content  string
}

var benchVocabulary = []string{
	"agent", "system", "memory", "context", "analysis", "update", "issue", "design",
	"service", "network", "request", "response", "latency", "cache", "database", "token",
	"stream", "parser", "worker", "queue", "result", "summary", "project", "module",
	"process", "failure", "success", "vector", "ranking", "search", "filter", "storage",
	"runtime", "package", "handler", "monitor", "signal", "trace", "client", "server",
}

func generateBenchmarkEntries(n int, seed int64) []benchMemoryEntry {
	sources := []string{"agent-alpha", "agent-beta", "agent-gamma", "tool-indexer", "tool-linter"}
	categories := []string{"finding", "decision", "file_summary", "error", "user_context"}
	rng := rand.New(rand.NewSource(seed))
	entries := make([]benchMemoryEntry, n)

	for i := 0; i < n; i++ {
		entries[i] = benchMemoryEntry{
			source:   sources[rng.Intn(len(sources))],
			category: categories[rng.Intn(len(categories))],
			content:  randomSentence(rng, 12, 20),
		}
	}

	return entries
}

func randomSentence(rng *rand.Rand, minWords, maxWords int) string {
	wordCount := minWords
	if maxWords > minWords {
		wordCount += rng.Intn(maxWords - minWords + 1)
	}
	words := randomWords(rng, wordCount)
	if len(words) == 0 {
		return ""
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ") + "."
}

func randomWords(rng *rand.Rand, count int) []string {
	words := make([]string, count)
	for i := range words {
		words[i] = benchVocabulary[rng.Intn(len(benchVocabulary))]
	}
	return words
}

func tenWordQuery(entries []benchMemoryEntry, seed int64) string {
	rng := rand.New(rand.NewSource(seed))
	if len(entries) == 0 {
		return strings.Join(randomWords(rng, 10), " ")
	}

	tokens := tokenize(entries[rng.Intn(len(entries))].content)
	if len(tokens) >= 10 {
		start := rng.Intn(len(tokens) - 9)
		return strings.Join(tokens[start:start+10], " ")
	}

	words := randomWords(rng, 10)
	copy(words, tokens)
	return strings.Join(words, " ")
}

func seedBenchmarkMemory(m *Memory, entries []benchMemoryEntry) {
	for _, e := range entries {
		m.Store(e.source, e.category, e.content)
	}
}

func BenchmarkMemoryStore(b *testing.B) {
	for _, size := range []int{100, 1000} {
		b.Run(fmt.Sprintf("entries_%d", size), func(b *testing.B) {
			b.ReportAllocs()

			entries := generateBenchmarkEntries(size+b.N, int64(size))
			m := NewMemory("")
			seedBenchmarkMemory(m, entries[:size])

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				e := entries[size+i]
				m.Store(e.source, e.category, e.content)
			}
		})
	}
}

func BenchmarkMemoryRecall(b *testing.B) {
	for _, size := range []int{100, 1000} {
		b.Run(fmt.Sprintf("entries_%d", size), func(b *testing.B) {
			b.ReportAllocs()

			entries := generateBenchmarkEntries(size, int64(size*10))
			m := NewMemory("")
			seedBenchmarkMemory(m, entries)

			query := tenWordQuery(entries, int64(size*100))
			var result string

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				result = m.Recall(query)
			}
			_ = result
		})
	}
}
