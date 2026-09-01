# ag3nts

A multi-agent AI orchestrator that coordinates Claude Code, Gemini CLI, Codex CLI, and a local Gemma 31B model from a single terminal interface. Ask a question, and ag3nts routes it to the right agent. Run a recipe, and ag3nts orchestrates a multi-stage pipeline across agents with live progress tracking.

## Status

Internal Neurai Technologies project. Not under active development since May 2026 — the codebase works as documented below, but there are no support or compatibility guarantees and no roadmap.

## What this does

- **Gemma 4 31B** runs locally via Ollama as the coordinator — routes requests, manages memory, breaks down complex tasks
- **Claude Code** handles deep reasoning, code review, architecture decisions
- **Gemini CLI** handles web research, large-context analysis, Google ecosystem tasks
- **Codex CLI** handles focused implementation, single-file changes, quick code generation
- **Recipes** define multi-stage pipelines (research, plan, implement, review) that execute across agents automatically
- **m3m0ry** provides a rolling context window so agents share knowledge across the session
- **Custom tools** let you extend Gemma's capabilities with YAML-defined shell scripts

## Quick start

```bash
git clone https://github.com/rohanrgit/ag3nts.git
cd ag3nts
go build -o bin/ag3nts .
ln -sf $(pwd)/bin/ag3nts /usr/local/bin/ag3nts
ag3nts install   # install Claude Code, Gemini CLI, Codex CLI
```

Then from any project directory:
```bash
cd /path/to/your/project
ag3nts
```

## Usage

### Chat
```
> research how to implement health check endpoints in Go
> implement a --debug flag in cmd/main.go
> what was the last commit?
```

### Shell escape
```
> !git status
> !go test ./...
```

### Recipes
```
> /recipe                                              # list recipes
> /recipe repair-lite --dry-run objective=add a flag    # preview pipeline
> /recipe repair-lite objective=add a health check endpoint to the orchestrator
```

### Pipeline visibility
During recipe execution, live banners show stage status, agent assignments, and running cost:
```
╭─────────────────────────────────────────────────────────╮
│ recipe R222485000                                       │
│ ✓ research(gemini)  ✓ plan(claude)  ⠋ implement(codex)  │
╰─────────────────────────────────────────────────────────╯
```

## Documentation

- [Getting Started](docs/getting-started.md) — installation, first run, basic usage
- [Recipes](docs/recipes.md) — recipe authoring, parameters, evaluator loop, dry-run
- [Custom Tools](docs/custom-tools.md) — YAML tool definitions, security model
- [MCP Servers](docs/mcp.md) — connect to external MCP servers (Postgres, GitHub, Slack, etc.)
- [Commands Reference](docs/commands.md) — all TUI slash commands

## Architecture

```
User ──► TUI (readline) ──► Gemma 4 (Ollama, local)
                                │
                    ┌───────────┼───────────┐
                    ▼           ▼           ▼
              Claude Code   Gemini CLI   Codex CLI
              (subprocess)  (subprocess) (subprocess)
```

Gemma acts as the coordinator. It decides which agent handles each request, manages the m3m0ry rolling context, and orchestrates multi-stage recipe pipelines. All agent communication flows through a typed event bus; the TUI subscribes for real-time display.

### Key packages

| Package | Purpose |
|---|---|
| `internal/orchestrator/` | Dispatch loop, task execution, evaluator loop, health endpoint |
| `internal/llm/` | Ollama integration, AgentLoop, routing/system/custom tools |
| `internal/tui/` | Terminal interface, pipeline banners, in-place streaming |
| `internal/context/` | m3m0ry rolling context (SQLite + JSONL) |
| `internal/recipe/` | YAML recipe system with DAG expansion |
| `internal/agent/` | Agent interface, subprocess management, parsers |
| `internal/store/` | SQLite persistence for sessions, tasks, events |

## Configuration

Edit `config/ag3nts.toml`:

```toml
[orchestrator]
primary = "claude"
max_concurrency = 3

[llm]
enabled = true
head_model = "gemma4:31b"
endpoint = "http://localhost:11434"

[security]
enabled = true
block_on_critical = true

[logging]
enabled = true
level = "info"
```

## Portable SSD setup

ag3nts can live entirely on an external drive. The project directory contains everything: binaries, config, agent home directories (via symlinks), state, and cache. Plug into any macOS machine, run the setup script, and start coding. See the `shared/` directory for cross-platform config templates.

## License

Copyright © 2025–2026 Neurai Technologies Private Limited.

Licensed under the [PolyForm Noncommercial License 1.0.0](LICENSE.md): use, copying, modification, and distribution are permitted for noncommercial purposes only. Any commercial use requires a separate license from Neurai Technologies.
