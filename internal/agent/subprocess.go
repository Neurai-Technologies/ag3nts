package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rohanrgit/ag3nts/internal/paths"
)

// EventParser converts a raw JSON line from a CLI subprocess into an AgentEvent.
// Each CLI tool (Claude, Gemini, Codex) provides its own parser implementation.
type EventParser func(line []byte, agentName, sessionID, taskID string) *AgentEvent

// ResumeFunc returns CLI flags to resume a previous session given the provider session ID.
type ResumeFunc func(sessionID string) []string

// SubprocessAgent wraps a CLI tool as an Agent, managing its lifecycle
// as a subprocess with streaming JSON output.
type SubprocessAgent struct {
	name         string
	binaryPath   string       // absolute path to CLI binary
	baseFlags    []string     // default CLI flags (e.g. "--output-format", "stream-json")
	promptFlag   string       // flag for prompt ("-p" or "" for positional)
	parser       EventParser  // JSON line → AgentEvent
	resumeFlags  ResumeFunc   // returns CLI flags for session resume (nil = not supported)
	systemPrompt string       // prepended to every prompt
	capabilities []string
	layout       *paths.Layout
	extraPaths   []string // directories to prepend to PATH (e.g. node/bin)

	mu       sync.Mutex
	sessions map[string]*subprocessSession
}

// subprocessSession tracks the OS process and goroutine for one session.
type subprocessSession struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	done   chan struct{}
}

// SubprocessConfig holds the configuration for creating a SubprocessAgent.
type SubprocessConfig struct {
	Name         string
	BinaryPath   string
	BaseFlags    []string
	PromptFlag   string // flag for prompt (e.g. "-p"); empty = positional argument
	Parser       EventParser
	ResumeFlags  ResumeFunc // returns CLI flags for session resume (nil = not supported)
	SystemPrompt string     // prepended to every prompt (agent-level instructions)
	Capabilities []string
	Layout       *paths.Layout
	ExtraPaths   []string
}

// NewSubprocessAgent creates a subprocess-backed agent from the given config.
func NewSubprocessAgent(cfg SubprocessConfig) *SubprocessAgent {
	promptFlag := cfg.PromptFlag
	if promptFlag == "" {
		promptFlag = "-p" // default for most CLIs
	}
	return &SubprocessAgent{
		name:         cfg.Name,
		binaryPath:   cfg.BinaryPath,
		baseFlags:    cfg.BaseFlags,
		promptFlag:   promptFlag,
		parser:       cfg.Parser,
		resumeFlags:  cfg.ResumeFlags,
		systemPrompt: cfg.SystemPrompt,
		capabilities: cfg.Capabilities,
		layout:       cfg.Layout,
		extraPaths:   cfg.ExtraPaths,
		sessions:     make(map[string]*subprocessSession),
	}
}

func (a *SubprocessAgent) Name() string { return a.name }

func (a *SubprocessAgent) Capabilities() []string { return a.capabilities }

func (a *SubprocessAgent) Available() bool {
	_, err := os.Stat(a.binaryPath)
	return err == nil
}

// Start launches the CLI subprocess with the given prompt and streams events
// through the returned Session's event channel.
func (a *SubprocessAgent) Start(ctx context.Context, prompt string, opts *StartOpts) (*Session, error) {
	if opts == nil {
		opts = &StartOpts{}
	}

	sessionID := opts.SessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("%s-%d", a.name, time.Now().UnixNano())
	}

	// Build the full prompt with optional context prepended.
	fullPrompt := prompt
	if opts.Context != "" {
		fullPrompt = opts.Context + "\n\n" + prompt
	}

	// Build command arguments.
	args := a.buildArgs(fullPrompt, opts)

	// Create a cancellable context for this session.
	sessCtx, cancel := context.WithCancel(ctx)

	session := NewSession(sessionID, a.name, opts.TaskID, 256, cancel)

	cmd := exec.CommandContext(sessCtx, a.binaryPath, args...)
	cmd.Env = filteredEnv(a.extraPaths...)
	if opts.WorkDir != "" {
		cmd.Dir = opts.WorkDir
	}
	// H-1 fix: apply opts.Env through the same allowlist as filteredEnv (SR-8).
	if opts.Env != nil {
		for k, v := range opts.Env {
			if envAllowed[k] {
				cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
			}
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start %s: %w", a.name, err)
	}

	done := make(chan struct{})

	// Store the subprocess session for lifecycle management.
	a.mu.Lock()
	a.sessions[sessionID] = &subprocessSession{cmd: cmd, cancel: cancel, done: done}
	a.mu.Unlock()

	// Stream stdout in a goroutine — parse each JSON line into AgentEvent.
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 10*1024*1024) // 64KB default, 10MB max
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			if a.parser != nil {
				if event := a.parser(line, a.name, sessionID, opts.TaskID); event != nil {
					// Capture provider session ID from init events for resume.
					if event.Kind == EventInit && event.SessionID != "" && event.SessionID != sessionID {
						session.SetResumeID(event.SessionID)
					}
					session.Emit(*event)
				}
			}
		}
	}()

	// B-5 fix: capture stderr and surface in error events.
	stderrCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		var buf strings.Builder
		for scanner.Scan() {
			if buf.Len() < 4096 { // cap at 4KB
				buf.WriteString(scanner.Text())
				buf.WriteByte('\n')
			}
		}
		stderrCh <- buf.String()
	}()

	// Wait for process exit and close the session.
	go func() {
		defer close(done)
		err := cmd.Wait()
		stderrContent := <-stderrCh // wait for stderr goroutine
		status := StatusStopped
		if err != nil {
			status = StatusFailed
			exitCode := 1
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}
			agentErr := ClassifyError(a.name, exitCode, stderrContent)
			session.Emit(AgentEvent{
				Kind:      EventError,
				Agent:     a.name,
				SessionID: sessionID,
				TaskID:    opts.TaskID,
				Content:   agentErr.Error(),
				Metadata: map[string]any{
					"error_type":  agentErr.Type.String(),
					"retryable":   agentErr.Retryable,
					"exit_code":   exitCode,
					"retry_after": agentErr.RetryAfter.String(),
				},
				Timestamp: time.Now(),
			})
		}
		session.Emit(AgentEvent{
			Kind:      EventComplete,
			Agent:     a.name,
			SessionID: sessionID,
			TaskID:    opts.TaskID,
			Timestamp: time.Now(),
		})
		session.Close(status)

		// Clean up session tracking.
		a.mu.Lock()
		delete(a.sessions, sessionID)
		a.mu.Unlock()
	}()

	return session, nil
}

// Send is not yet implemented for subprocess agents. Multi-turn requires
// stopping the current process and relaunching with --resume.
func (a *SubprocessAgent) Send(session *Session, message string) error {
	// TODO: Implement session resume via --resume flag.
	return fmt.Errorf("%s: Send not yet implemented (use Start with SessionID for resume)", a.name)
}

// Stop terminates a running subprocess session gracefully.
// Sends SIGINT first, then SIGKILL after 5 seconds if still running.
func (a *SubprocessAgent) Stop(session *Session) error {
	a.mu.Lock()
	sub, ok := a.sessions[session.ID]
	a.mu.Unlock()
	if !ok {
		return nil // already stopped
	}

	// Send SIGINT for graceful shutdown.
	if sub.cmd.Process != nil {
		_ = sub.cmd.Process.Signal(syscall.SIGINT)
	}

	// Wait up to 5 seconds, then force kill.
	select {
	case <-sub.done:
		return nil
	case <-time.After(5 * time.Second):
		if sub.cmd.Process != nil {
			_ = sub.cmd.Process.Kill()
		}
		<-sub.done
		return nil
	}
}

// Events returns the event channel for the given session.
func (a *SubprocessAgent) Events(session *Session) <-chan AgentEvent {
	return session.Events()
}

// buildArgs constructs the CLI arguments for launching the subprocess.
func (a *SubprocessAgent) buildArgs(prompt string, opts *StartOpts) []string {
	var args []string
	args = append(args, a.baseFlags...)

	// Insert resume flags if continuing a previous session.
	if opts.ResumeSessionID != "" && a.resumeFlags != nil {
		args = append(args, a.resumeFlags(opts.ResumeSessionID)...)
	}

	// Append model override if specified.
	if opts.Model != "" {
		args = append(args, "-m", opts.Model)
	}

	// Prepend system prompt if configured.
	if a.systemPrompt != "" {
		prompt = a.systemPrompt + "\n\n" + prompt
	}

	// Append prompt: some CLIs use -p, others take it as positional arg.
	if a.promptFlag == "_positional" {
		args = append(args, prompt)
	} else {
		args = append(args, a.promptFlag, prompt)
	}

	return args
}

// envAllowed is the SR-8 allowlist for subprocess environment variables.
var envAllowed = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "TERM": true,
	"LANG": true, "LC_ALL": true, "SHELL": true, "TMPDIR": true,
	"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
}

// filteredEnv returns a safe set of environment variables for subprocess agents.
// SR-8: Reuses the same allowlist pattern from internal/mcp/tools.go.
func filteredEnv(extraPaths ...string) []string {

	var env []string
	for _, e := range os.Environ() {
		idx := strings.IndexByte(e, '=')
		if idx < 0 {
			continue
		}
		key := e[:idx]
		if envAllowed[key] {
			if key == "PATH" && len(extraPaths) > 0 {
				env = append(env, fmt.Sprintf("PATH=%s:%s",
					strings.Join(extraPaths, ":"), os.Getenv("PATH")))
			} else {
				env = append(env, e)
			}
		}
	}

	// Ensure PATH is set even if not in original env.
	hasPath := false
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			hasPath = true
			break
		}
	}
	if !hasPath && len(extraPaths) > 0 {
		env = append(env, fmt.Sprintf("PATH=%s", strings.Join(extraPaths, ":")))
	}

	return env
}
