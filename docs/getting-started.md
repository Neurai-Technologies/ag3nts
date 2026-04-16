# Getting Started with ag3nts

## Prerequisites

- **macOS** (Apple Silicon)
- **Go 1.25+** (for building from source)
- **Ollama** installed with `gemma4:31b` pulled
- At least one of: Claude Code, Gemini CLI, Codex CLI installed

## Installation

```bash
# Clone the repo
git clone https://github.com/rohanrgit/ag3nts.git
cd ag3nts

# Build
go build -o bin/ag3nts .

# Symlink to PATH (one time)
ln -sf $(pwd)/bin/ag3nts /usr/local/bin/ag3nts

# Install agent CLIs
ag3nts install
```

## First run

```bash
cd /path/to/your/project
ag3nts
```

You'll see:
```
✓ Agent workdir: /path/to/your/project
✓ Security review enabled (pattern filter, block_on_critical=true)
✓ Ollama connected (gemma4:31b)
✓ Model loaded and ready
─────────────────────────────────────────────────────────
>
```

Gemma is your coordinator. It routes tasks to the right agent:
- **Research/web search** goes to Gemini
- **Complex coding/review** goes to Claude
- **Quick implementation** goes to Codex
- **File reading/memory** Gemma handles directly

## Basic usage

### Chat
Just type naturally. Gemma delegates to the right agent:
```
> what's the latest commit in this project?
> research how to implement health check endpoints in Go
> implement a --debug flag in cmd/main.go
```

### Shell commands
Prefix with `!` to run directly without LLM:
```
> !git status
> !go test ./...
> !ls -la
```

### Recipes
Run multi-stage workflows:
```
> /recipe                                    # list available recipes
> /recipe repair-lite --dry-run objective=... # preview without running
> /recipe repair-lite objective=add health check endpoint to the orchestrator
```

### Key commands
```
/help       — list all commands
/tasks      — show current session tasks
/cost       — token/cost breakdown
/m3m0ry     — rolling context stats/search/tail
/cancel     — cancel current operation
/quit       — exit
```

## Project targeting

ag3nts operates on whatever directory you launch it from. To target a different project:

```bash
# Option 1: cd into the project
cd /path/to/project && ag3nts

# Option 2: use --project flag
ag3nts orchestrate --project /path/to/project
```

## Permissions

When an agent wants to modify files or run commands, you'll see:
```
⚠ Permission required
  Tool:   write_file
  Action: /path/to/file.go
  1) Allow once
  2) Always allow write_file
  3) Deny
```

Press `1`, `2`, or `3` — no Enter needed.

## Configuration

Edit `config/ag3nts.toml` for:
- Agent settings (models, flags)
- Routing rules (which queries go to which agent)
- Security settings
- Logging levels
- m3m0ry context window size

See [recipes.md](recipes.md) for recipe authoring and [custom-tools.md](custom-tools.md) for extending Gemma's tool surface.
