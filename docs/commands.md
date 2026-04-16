# TUI Commands Reference

## Chat
Type naturally. Gemma routes to the right agent:
```
> research how to implement graceful shutdown in Go
> implement a --debug flag in cmd/main.go
> review the changes I just made
```

## Shell escape
Prefix with `!` to run directly in the shell (no LLM, no permission prompt):
```
> !git status
> !go test ./...
> !ls -la internal/
```

## Slash commands

### `/help`
List all available commands.

### `/tasks` or `/task list`
Compact list of tasks in the current session:
```
Tasks (4 in session, /task <id> for details):
  ✓ R222-research [repair.research] (gemini) You are the research stage...
  ✓ R222-plan [repair.plan] (claude) You are the plan stage...
  → R222-implement [repair.implement] (codex) You are the implementation...
  ○ R222-review [repair.review] (claude) You are the adversarial reviewer...
```

### `/task <id>`
Full details for one task — status, agent, timing, dependencies, context, output preview.

### `/task list --all`
Show tasks from all sessions (including prior runs).

### `/task gc [--dry-run]`
Clean up legacy task JSON files from before session-scoping was added.

### `/recipe`
List all available recipes with agent and description.

### `/recipe <name> [--dry-run] [key=val ...]`
Dispatch (or preview) a recipe:
```
/recipe repair-lite objective=add a health check endpoint
/recipe repair-lite --dry-run objective=add a health check endpoint
/recipe research query=what is the MCP protocol
```

Multi-word values work naturally — everything after `key=` until the next `key=` is the value.

### `/cost`
Session cost breakdown by agent:
```
Session Duration: 12m 30s
Total Tokens: ↑258k ↓7.8k
  gemini: ↑52.3k ↓2.2k ($0.0102)
  claude: ↑8.2k ↓4.5k ($0.5375)
  codex: ↑204k ↓3.5k ($0.2402)
Session Cost: $0.7879
```

### `/m3m0ry stats`
Rolling context window statistics:
```
Total tokens: 45000
Chunk count: 128
Max seq: 256
JSONL path: state/m3m0ry.jsonl
JSONL size: 1.2 MB
```

### `/m3m0ry search <query>`
Search the rolling context for matching chunks:
```
/m3m0ry search health check endpoint
```

### `/m3m0ry tail [n]`
Show the most recent N chunks (default 10).

### `/status`
One-line overview: primary agent, agent count, task counts by status.

### `/cancel`
Cancel the current operation (also Ctrl+C).

### `/local status`
Show loaded models and memory stats for the local LLM.

### `/local reset`
Clear conversation history and in-memory knowledge.

### `/compact`
Compress conversation history to free context window space.

### `/export`
Export the full conversation to a timestamped file in `state/`.

### `/schedule`
List background cron schedules (if any configured).

### `/reload`
Hot-reload configuration from `ag3nts.toml` without restarting.

### `/quit` or `/exit`
Exit ag3nts. Unloads local models from VRAM.

## Pipeline visibility

During recipe execution, the TUI shows:
- **Dispatch banner**: all stages with status icons
- **Spinner label**: live pipeline state with running cost
- **Stage banners**: updated on each stage completion
- **Summary banner**: final box with per-stage tokens, cost, duration
- **Completion signal**: bold green "Pipeline complete" or red "Pipeline failed"

## Permissions

When a tool needs approval:
```
⚠ Permission required
  Tool:   write_file
  Action: /path/to/file.go
  1) Allow once
  2) Always allow write_file
  3) Deny
```
Press the number key — no Enter needed. "Always allow" persists for the session.
