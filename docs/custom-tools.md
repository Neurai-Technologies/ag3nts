# Custom Tools

Extend Gemma's capabilities by dropping YAML files in `config/tools/`. Each file defines a tool that registers alongside built-in tools and appears to the model as a first-class function call.

## Quick start

1. Copy the example:
```bash
cp config/tools/example-echo.yaml.disabled config/tools/my-tool.yaml
```

2. Edit `my-tool.yaml` with your tool definition

3. Restart ag3nts — you'll see:
```
✓ 1 custom tool(s) loaded from .../config/tools
```

4. Gemma can now call your tool naturally:
```
> use my-tool to do the thing
```

## YAML schema

```yaml
name: query_db                    # unique identifier (shown to the model)
description: >                    # one-sentence description (shown to the model)
  Run a read-only SQL query against the project database.

parameters:                       # inputs the model provides
  - key: sql
    type: string                  # string | integer | number | boolean
    required: true
    description: The SQL query to execute (SELECT only)
  - key: limit
    type: integer
    required: false
    description: Max rows to return

executor:
  type: shell                     # only "shell" supported at v1
  command: ["psql", "-h", "localhost", "-d", "mydb", "-c", "$SQL"]
  env:                            # static environment overlay
    PGPASSWORD: "my-password"
  timeout: 30s                    # max runtime (default 30s, max 10m)
  permission_required: true       # ask user before every call
```

## How parameters are passed

Parameter values become **uppercase environment variables**. A parameter with `key: sql` becomes `$SQL` in the command's environment. Values are never interpolated into the `command` array — they're passed purely via env vars, which prevents shell injection.

Example: if the model calls `query_db(sql="SELECT * FROM users", limit=10)`, the command runs with:
```
SQL="SELECT * FROM users"
LIMIT="10"
```

## Executor types

### `shell` (only type at v1)

The `command` field is an **argv array**, not a shell string. Shell metacharacters are NOT interpreted:

```yaml
# CORRECT — explicit argv
command: ["psql", "-h", "localhost", "-c", "$SQL"]

# CORRECT — if you need shell features (pipes, redirects)
command: ["sh", "-c", "echo $SQL | psql -h localhost"]

# WRONG — this is NOT a shell string, it's one argv element
command: "psql -h localhost -c $SQL"
```

## Security model

| Feature | How it works |
|---|---|
| **No shell injection via params** | Values are env vars, not argv-interpolated |
| **Timeout enforcement** | `exec.CommandContext` with deadline; hard cap at 10 minutes |
| **Output size cap** | 100KB max (matching built-in tools) |
| **Permission prompt** | `permission_required: true` routes through the 1/2/3 TUI prompt |
| **Working directory** | Pinned to `agentWorkDir` (the project you launched ag3nts from) |
| **Reserved name protection** | Can't shadow built-ins (`read_file`, `recall`, `web_research`, etc.) |
| **Duplicate detection** | Two YAML files with the same `name` — second is skipped with a warning |
| **Malformed YAML recovery** | Bad files are skipped; good files still load |

## Tips

- **Start with `permission_required: true`** until you trust the tool. You can always set it to `false` later.
- **Use `timeout`** generously for slow commands (database queries, API calls). Default is 30s.
- **Keep descriptions concise** — the model sees them in its tool list and long descriptions waste context.
- **Test with the echo tool first** — rename `example-echo.yaml.disabled` to `echo.yaml`, restart, verify Gemma can call it.
- **gitignore real tools** — `config/tools/*.yaml` is gitignored by default. Only `.yaml.disabled` example templates are tracked.

## Example: Git commit helper

```yaml
name: git_commit
description: Stage and commit all changes with a generated message.
parameters:
  - key: message
    type: string
    required: true
    description: The commit message
executor:
  type: shell
  command: ["sh", "-c", "git add -A && git commit -m \"$MESSAGE\""]
  timeout: 30s
  permission_required: true
```

## Example: HTTP API call

```yaml
name: check_api
description: Check if the production API is responding.
executor:
  type: shell
  command: ["curl", "-s", "-o", "/dev/null", "-w", "%{http_code}", "https://api.example.com/health"]
  timeout: 10s
  permission_required: false
```
