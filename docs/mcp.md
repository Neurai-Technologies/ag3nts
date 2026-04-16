# MCP Server Integration

ag3nts can connect to external [MCP (Model Context Protocol)](https://modelcontextprotocol.io/) servers, automatically discover their tools, and register them for the local LLM to call. This gives Gemma access to databases, APIs, file systems, and any other service that exposes an MCP interface.

## Quick start

1. Add to `config/ag3nts.toml`:

```toml
[toolsets.filesystem]
type = "mcp"
command = "npx"
args = ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/dir"]
```

2. Restart ag3nts. You'll see:
```
✓ MCP: filesystem (3 tools)
```

3. Gemma can now call filesystem tools naturally:
```
> list all Go files in the project
gemma4 ⚙️ filesystem__list_directory ...
```

## Configuration

Each MCP server is declared as a `[toolsets.<name>]` entry with `type = "mcp"`:

```toml
[toolsets.postgres]
type = "mcp"
command = "npx"
args = ["-y", "@modelcontextprotocol/server-postgres"]
description = "PostgreSQL database"
[toolsets.postgres.env]
POSTGRES_CONNECTION_STRING = "postgresql://user:pass@localhost/mydb"

[toolsets.github]
type = "mcp"
command = "npx"
args = ["-y", "@modelcontextprotocol/server-github"]
description = "GitHub integration"
[toolsets.github.env]
GITHUB_PERSONAL_ACCESS_TOKEN = "ghp_..."
```

### Fields

| Field | Required | Description |
|---|---|---|
| `type` | Yes | Must be `"mcp"` |
| `command` | Yes | Binary to run (e.g., `npx`, `python`, a compiled binary) |
| `args` | No | Arguments to the command |
| `description` | No | Human-readable description (for docs, not shown to model) |
| `env` | No | Environment variables for the subprocess |

## How it works

1. **Startup**: ag3nts spawns each MCP server as a subprocess and connects via JSON-RPC 2.0 over stdio
2. **Handshake**: sends `initialize` + `notifications/initialized` to establish the connection
3. **Discovery**: sends `tools/list` to discover available tools with their full JSON schemas
4. **Registration**: converts MCP tools to the Ollama tool format and registers them alongside built-in tools
5. **Execution**: when Gemma calls an MCP tool, ag3nts forwards the call via `tools/call` and returns the result
6. **Lifecycle**: servers stay alive for the session. If one crashes, ag3nts auto-restarts it on the next tool call.

## Tool naming

MCP tool names are qualified as `servername__toolname` (double underscore) to avoid collisions when multiple servers expose tools with the same name. For example:

- `postgres__query` (from the postgres server)
- `github__search_issues` (from the github server)
- `filesystem__read_file` (from the filesystem server)

## TUI commands

```
/mcp                    Show connected servers and their tools
```

## Auto-restart

If an MCP server crashes mid-session (OOM, segfault, etc.), ag3nts detects the failure on the next tool call and automatically attempts to restart the server. If the restart succeeds, the tool call is retried transparently. If it fails, the error is surfaced to Gemma.

## Dynamic tool updates

If an MCP server adds or removes tools after startup (signaled via `notifications/tools/list_changed`), ag3nts automatically re-discovers the server's tool catalog.

## Security

- **Environment filtering**: MCP server subprocesses receive a filtered environment (SR-5) with only safe variables, plus any explicitly declared in the `env` config
- **Permission prompts**: MCP tool calls go through the same 1/2/3 permission prompt as other tools
- **Output cap**: tool responses are capped at 100KB
- **Local binding**: MCP servers run as local subprocesses; no network exposure

## Available MCP servers

The [MCP server registry](https://modelcontextprotocol.io/) includes 70+ servers:

| Server | Package | What it does |
|---|---|---|
| Filesystem | `@modelcontextprotocol/server-filesystem` | Sandboxed file operations |
| PostgreSQL | `@modelcontextprotocol/server-postgres` | SQL queries |
| GitHub | `@modelcontextprotocol/server-github` | Issues, PRs, code search |
| Slack | `@modelcontextprotocol/server-slack` | Read/send messages |
| Memory | `@modelcontextprotocol/server-memory` | Persistent knowledge graph |
| Brave Search | `@modelcontextprotocol/server-brave-search` | Web search |
| SQLite | `@modelcontextprotocol/server-sqlite` | SQLite database queries |

Any server that speaks JSON-RPC 2.0 over stdio works — including custom servers you write in any language.

## Troubleshooting

**Server fails to start**: Check that the command is installed and accessible. For npx-based servers, ensure Node.js is installed (`node --version`).

**Tools not appearing**: Run `/mcp` to see connected servers and their tools. If a server shows 0 tools, the `tools/list` response may have failed — check stderr for `[mcp:servername]` log lines.

**Permission denied on every call**: MCP tools require permission by default. Use option `2) Always allow` at the permission prompt to auto-approve for the session.

**Server crashes repeatedly**: Check `[mcp:servername]` stderr lines for error messages. Common causes: missing environment variables (check `env` config), incompatible Node.js version, server-specific configuration issues.
