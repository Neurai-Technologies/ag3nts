package security

import (
	"time"
)

// ScanResult holds all matches from a scan.
type ScanResult struct {
	Matches   []PatternMatch
	MaxRisk   RiskLevel
	Blocked   bool // true if any Critical match found
	Timestamp time.Time
}

// Scanner runs pattern matching against task descriptions and agent tool calls.
type Scanner struct{}

// NewScanner creates a scanner using the global compiled patterns.
func NewScanner() *Scanner {
	return &Scanner{}
}

// ScanText checks text against all loaded patterns.
func (s *Scanner) ScanText(text string) *ScanResult {
	result := &ScanResult{
		Timestamp: time.Now(),
	}

	for _, p := range patterns {
		if loc := p.Compiled.FindStringIndex(text); loc != nil {
			matched := text[loc[0]:loc[1]]
			if len(matched) > 200 {
				matched = matched[:200] + "..."
			}
			result.Matches = append(result.Matches, PatternMatch{
				Pattern:     p,
				MatchedText: matched,
			})
			if p.Risk > result.MaxRisk {
				result.MaxRisk = p.Risk
			}
			if p.Risk == RiskCritical {
				result.Blocked = true
			}
		}
	}

	return result
}

// ScanTask checks a task description and optional context together.
func (s *Scanner) ScanTask(description, context string) *ScanResult {
	combined := description
	if context != "" {
		combined += "\n" + context
	}
	return s.ScanText(combined)
}

// HasThreats returns true if any matches were found.
func (r *ScanResult) HasThreats() bool {
	return len(r.Matches) > 0
}

// Summary returns a human-readable summary of findings.
func (r *ScanResult) Summary() string {
	if !r.HasThreats() {
		return "no threats detected"
	}
	s := ""
	for i, m := range r.Matches {
		if i > 0 {
			s += "; "
		}
		s += m.Pattern.Risk.String() + ": " + m.Pattern.Description
	}
	return s
}
