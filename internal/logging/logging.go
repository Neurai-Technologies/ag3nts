// Package logging provides structured JSON logging for ag3nts sessions.
// Each session gets its own log file at state/logs/<session-id>.jsonl.
// Per-module log levels are configurable via ag3nts.toml.
package logging

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Level represents a log severity.
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String returns the level name.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "unknown"
	}
}

// ParseLevel converts a string to a Level. Defaults to LevelInfo.
func ParseLevel(s string) Level {
	switch s {
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

// LogEntry is a single structured log line written as JSON.
type LogEntry struct {
	Timestamp string `json:"ts"`
	Level     string `json:"level"`
	Module    string `json:"module"`
	Agent     string `json:"agent,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Message   string `json:"msg"`
	Data      any    `json:"data,omitempty"`
}

// Logger writes structured JSON lines to a session-specific log file.
type Logger struct {
	f            *os.File
	enc          *json.Encoder
	mu           sync.Mutex
	defaultLevel Level
	moduleLevels map[string]Level
	sessionID    string
}

// Open creates a logger that writes to state/logs/<sessionID>.jsonl.
// Creates the log directory if it doesn't exist.
func Open(logsDir, sessionID string, defaultLevel Level, moduleLevels map[string]Level) (*Logger, error) {
	if err := os.MkdirAll(logsDir, 0700); err != nil {
		return nil, fmt.Errorf("create logs dir: %w", err)
	}

	path := filepath.Join(logsDir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}

	if moduleLevels == nil {
		moduleLevels = make(map[string]Level)
	}

	return &Logger{
		f:            f,
		enc:          json.NewEncoder(f),
		defaultLevel: defaultLevel,
		moduleLevels: moduleLevels,
		sessionID:    sessionID,
	}, nil
}

// Close flushes and closes the log file.
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.f.Close()
}

// Log writes a log entry if the level meets the threshold for the module.
func (l *Logger) Log(level Level, module, msg string) {
	l.log(level, module, "", "", msg, nil)
}

// LogWith writes a log entry with additional structured data.
func (l *Logger) LogWith(level Level, module, msg string, data any) {
	l.log(level, module, "", "", msg, data)
}

// LogAgent writes a log entry tagged with an agent and task.
func (l *Logger) LogAgent(level Level, module, agentName, taskID, msg string) {
	l.log(level, module, agentName, taskID, msg, nil)
}

// LogAgentWith writes a log entry tagged with agent, task, and data.
func (l *Logger) LogAgentWith(level Level, module, agentName, taskID, msg string, data any) {
	l.log(level, module, agentName, taskID, msg, data)
}

// Convenience methods.

func (l *Logger) Debug(module, msg string)               { l.Log(LevelDebug, module, msg) }
func (l *Logger) Info(module, msg string)                 { l.Log(LevelInfo, module, msg) }
func (l *Logger) Warn(module, msg string)                 { l.Log(LevelWarn, module, msg) }
func (l *Logger) Error(module, msg string)                { l.Log(LevelError, module, msg) }
func (l *Logger) Infof(module, format string, args ...any) {
	l.Log(LevelInfo, module, fmt.Sprintf(format, args...))
}
func (l *Logger) Errorf(module, format string, args ...any) {
	l.Log(LevelError, module, fmt.Sprintf(format, args...))
}

func (l *Logger) log(level Level, module, agentName, taskID, msg string, data any) {
	// Check level threshold for this module.
	threshold := l.defaultLevel
	if moduleLevel, ok := l.moduleLevels[module]; ok {
		threshold = moduleLevel
	}
	if level < threshold {
		return
	}

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level.String(),
		Module:    module,
		Agent:     agentName,
		TaskID:    taskID,
		SessionID: l.sessionID,
		Message:   msg,
		Data:      data,
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.enc.Encode(entry) // best-effort logging, don't propagate errors
}
