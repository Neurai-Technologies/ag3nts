package agent

import (
	"math"
	"math/rand"
	"strings"
	"time"
)

// ErrorType classifies agent failures for recovery decisions.
type ErrorType int

const (
	ErrUnknown         ErrorType = iota
	ErrAuthFailed                // API key invalid/expired
	ErrRateLimited               // 429 or rate limit message
	ErrContextExceeded           // Token limit exceeded
	ErrCrashed                   // Process exited unexpectedly
	ErrTimedOut                  // Context deadline exceeded
	ErrNetworkError              // Connection refused/timeout
	ErrSecurityBlocked           // Blocked by security review
)

// String returns the human-readable name.
func (e ErrorType) String() string {
	switch e {
	case ErrAuthFailed:
		return "auth_failed"
	case ErrRateLimited:
		return "rate_limited"
	case ErrContextExceeded:
		return "context_exceeded"
	case ErrCrashed:
		return "crashed"
	case ErrTimedOut:
		return "timed_out"
	case ErrNetworkError:
		return "network_error"
	case ErrSecurityBlocked:
		return "security_blocked"
	default:
		return "unknown"
	}
}

// AgentError wraps an error with classification and retry metadata.
type AgentError struct {
	Type       ErrorType
	Agent      string
	Message    string
	Retryable  bool
	RetryAfter time.Duration // hint for rate limits
}

func (e *AgentError) Error() string {
	return e.Agent + ": " + e.Type.String() + ": " + e.Message
}

// ClassifyError examines exit code and stderr to determine error type.
func ClassifyError(agentName string, exitCode int, stderr string) *AgentError {
	lower := strings.ToLower(stderr)

	ae := &AgentError{
		Agent:   agentName,
		Message: stderr,
	}

	// Truncate message for storage.
	if len(ae.Message) > 1024 {
		ae.Message = ae.Message[:1024] + "..."
	}

	switch {
	// Rate limiting.
	case containsAny(lower, "rate limit", "429", "too many requests", "quota exceeded", "resource exhausted"):
		ae.Type = ErrRateLimited
		ae.Retryable = true
		ae.RetryAfter = 30 * time.Second

	// Auth failures.
	case containsAny(lower, "unauthorized", "invalid api key", "401", "authentication failed", "forbidden", "403"):
		ae.Type = ErrAuthFailed
		ae.Retryable = false

	// Context/token limit.
	case containsAny(lower, "context length", "token limit", "maximum context", "too long", "context window"):
		ae.Type = ErrContextExceeded
		ae.Retryable = false // needs context compaction, not a simple retry

	// Network errors.
	case containsAny(lower, "connection refused", "connection reset", "no such host", "network unreachable", "dns"):
		ae.Type = ErrNetworkError
		ae.Retryable = true
		ae.RetryAfter = 5 * time.Second

	// Timeout.
	case containsAny(lower, "deadline exceeded", "timed out", "timeout"):
		ae.Type = ErrTimedOut
		ae.Retryable = true
		ae.RetryAfter = 10 * time.Second

	// Process crash (signal-based exits).
	case exitCode == 137 || exitCode == 143: // SIGKILL, SIGTERM
		ae.Type = ErrCrashed
		ae.Retryable = true
		ae.RetryAfter = 5 * time.Second

	default:
		ae.Type = ErrUnknown
		ae.Retryable = false
	}

	return ae
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// RetryConfig holds parameters for retry with exponential backoff.
type RetryConfig struct {
	MaxAttempts    int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	Jitter         float64 // 0.0-1.0, fraction of randomness
}

// DefaultRetryConfig returns sensible retry parameters per error type.
func DefaultRetryConfig(errType ErrorType) RetryConfig {
	switch errType {
	case ErrRateLimited:
		return RetryConfig{MaxAttempts: 5, InitialBackoff: 30 * time.Second, MaxBackoff: 5 * time.Minute, Jitter: 0.4}
	case ErrNetworkError:
		return RetryConfig{MaxAttempts: 3, InitialBackoff: 5 * time.Second, MaxBackoff: 60 * time.Second, Jitter: 0.3}
	case ErrTimedOut:
		return RetryConfig{MaxAttempts: 2, InitialBackoff: 10 * time.Second, MaxBackoff: 60 * time.Second, Jitter: 0.2}
	case ErrCrashed:
		return RetryConfig{MaxAttempts: 2, InitialBackoff: 5 * time.Second, MaxBackoff: 30 * time.Second, Jitter: 0.2}
	default:
		return RetryConfig{MaxAttempts: 0} // no retry
	}
}

// Backoff calculates the delay for a given attempt using exponential backoff with jitter.
// attempt is 0-indexed (first retry = attempt 0).
func (rc RetryConfig) Backoff(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	base := float64(rc.InitialBackoff) * math.Pow(2, float64(attempt))
	if base > float64(rc.MaxBackoff) {
		base = float64(rc.MaxBackoff)
	}
	// Apply jitter: base * (1 - jitter/2 + rand*jitter)
	jitterRange := base * rc.Jitter
	jittered := base - jitterRange/2 + rand.Float64()*jitterRange
	return time.Duration(jittered)
}
