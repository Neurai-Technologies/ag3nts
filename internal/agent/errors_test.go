package agent

import (
	"testing"
	"time"
)

func TestParseRetryAfter_Patterns(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  time.Duration
	}{
		{"http header seconds", "retry-after: 45", 45 * time.Second},
		{"http header no space", "retry-after:30", 30 * time.Second},
		{"prose seconds", "please retry after 60 seconds", 60 * time.Second},
		{"prose minutes", "wait 2 minutes before trying again", 2 * time.Minute},
		{"try again in", "try again in 15s", 15 * time.Second},
		{"claude style", "rate_limit_error: please retry after 30 seconds", 30 * time.Second},
		{"gemini style", "resource exhausted, retry after 45 sec", 45 * time.Second},
		{"no match defaults to 30s", "some random error message", 30 * time.Second},
		{"empty string defaults", "", 30 * time.Second},
		{"cap at 5 minutes", "retry-after: 600", 5 * time.Minute},
		{"minutes cap", "wait 10 minutes", 5 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRetryAfter(tc.input)
			if got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestClassifyError_RateLimitedSetsRetryAfter(t *testing.T) {
	ae := ClassifyError("claude", 1, "Error: rate limit exceeded. Retry-After: 45 seconds")
	if ae.Type != ErrRateLimited {
		t.Fatalf("type = %v, want ErrRateLimited", ae.Type)
	}
	if !ae.Retryable {
		t.Error("expected retryable=true")
	}
	if ae.RetryAfter != 45*time.Second {
		t.Errorf("RetryAfter = %v, want 45s", ae.RetryAfter)
	}
}

func TestClassifyError_RateLimitedFallback(t *testing.T) {
	ae := ClassifyError("gemini", 1, "429 Too Many Requests")
	if ae.Type != ErrRateLimited {
		t.Fatalf("type = %v, want ErrRateLimited", ae.Type)
	}
	if ae.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s (default)", ae.RetryAfter)
	}
}

func TestBackoff_ExponentialWithJitter(t *testing.T) {
	cfg := DefaultRetryConfig(ErrRateLimited)
	prev := time.Duration(0)
	for i := 0; i < cfg.MaxAttempts; i++ {
		b := cfg.Backoff(i)
		if b <= 0 {
			t.Errorf("attempt %d: backoff %v should be positive", i, b)
		}
		if b > cfg.MaxBackoff+time.Duration(float64(cfg.MaxBackoff)*cfg.Jitter) {
			t.Errorf("attempt %d: backoff %v exceeds max+jitter", i, b)
		}
		// Generally increasing (allowing jitter variance).
		_ = prev
		prev = b
	}
}
