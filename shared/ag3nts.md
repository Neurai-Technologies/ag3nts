# ag3nts.md â€” Shared Agent Instructions

## Context
Developer: Rohan. Works across Windows + macOS. All AI tools on portable SSD at `ag3nts/` with symlinks to `~/.claude`, `~/.gemini`, `~/.codex`. Setup scripts: `windows/setup.ps1`, `macos/setup.sh`.

## Tech Stack
- **Languages:** Python (primary), TypeScript
- **Web:** Astro 5, Tailwind CSS 4, pnpm, Node 22
- **Editor:** VS Code
- **CLI tools:** ImageMagick (`magick`), FFmpeg, ExifTool, jq

## Code Style
**Python:** type hints on all signatures, `pathlib.Path` over `os.path`, f-strings, Google-style docstrings, `snake_case` functions/vars, `PascalCase` classes, 4-space indent.

**TypeScript:** strict mode, `const` over `let`, arrow functions for callbacks, named exports, interfaces over type aliases, 2-space indent.

**General:** trailing newline, no trailing whitespace, UTF-8. Defer to linters (Ruff, Prettier, ESLint) for anything not listed.

## Git
Branches: `main` (stable), `dev` (integration), `feature/`, `fix/`, `hotfix/`. Conventional commits: `type(scope): description`, subject < 72 chars. Squash merge to main. One concern per PR.

## Testing
Python: `pytest`, files named `test_<module>.py`, mock external services. TypeScript: Vitest, files named `<module>.test.ts`. Tests must pass before committing. Bug fix = regression test.

## Commands
| Task | Command |
|---|---|
| Install deps (JS) | `pnpm install` |
| Dev server | `pnpm dev` |
| Build | `pnpm build` |
| Python tests | `pytest` |
| JS tests | `pnpm test` |
| Lint Python | `ruff check .` |
| Format Python | `ruff format .` |

## Agents

Sub-agents installed in `~/.claude/agents/`. Activate by name in any conversation.

| Agent | Model | Web | Purpose |
|---|---|---|---|
| `feedback` | Haiku | - | Captures user feedback and preferences across sessions |
| `code-reviewer` | Sonnet | - | Multi-agent dispatcher: 4 parallel specialists (correctness, security, convention, history) with confidence scoring. Dual-mode: REPAIR Stage 6 + auto-invoke before commit/push/PR |
| `accessibility-auditor` | Sonnet | WCAG refs | WCAG 2.2 AA audits, screen reader testing, POUR checklist |
| `software-architect` | Opus | Patterns | Dual-mode: REPAIR Stage 4 sub-step (ADRs, domain modeling) + standalone |
| `reality-checker` | Sonnet | - | Production readiness gate, defaults to NEEDS WORK |
| `security-engineer` | Opus | CVEs | Tri-mode: Stage 4 threat model + Stage 6 OWASP audit + auto-invoke on auth/secrets |
| `ux-architect` | Sonnet | Tailwind | Design tokens, theme scaffolding, layout systems |
| `version` | Haiku | - | Agent inventory audit, consistency checks, drift detection |
| `anthropic` | Sonnet | Heavy | Scans Anthropic research/news/docs daily, proposes ag3nts integrations |

Source: adapted from [agency-agents](https://github.com/msitarzewski/agency-agents) (engineering, design, testing divisions), trimmed and customized for this stack.

## Permission Mode

Auto mode is the default permission mode (`permissions.defaultMode: "auto"` in settings.json).
An AI classifier (Sonnet, two-stage pipeline) reviews each tool call in real-time:

- **Read-only actions + in-project file edits** → auto-approved (no classifier call)
- **Shell commands, web fetches, external operations** → classifier-reviewed
- **Destructive/dangerous actions** → blocked (force push, exfiltration, `curl | bash`, etc.)

This eliminates approval fatigue during auto-invoke flows (code-reviewer dispatches 4
parallel sub-agents, security-engineer runs audits — dozens of tool calls per commit).
You only see a prompt when the classifier is genuinely uncertain or blocks a dangerous action.

**Fallback**: 3 consecutive denials or 20 total denials → falls back to manual prompting.
Approve one action to resume auto mode.

**Override**: Cycle modes with `Shift+Tab`: `default → acceptEdits → plan → auto`.

**Environment context** is configured in platform settings.json under `autoMode.environment`
— tells the classifier which repos and infrastructure are trusted.

## Auto-Invoke Rules (Harness-Enforced)

The following rules are enforced by PreToolUse/PostToolUse hooks in `settings.json`.
The harness runs these checks automatically — they cannot be skipped or forgotten.
Hook scripts live in `shared/claude-code/hooks/`, symlinked to `~/.claude/hooks/`.

**Before any `git commit`** (PreToolUse hooks on Bash):
1. **Secrets scan** (`pre-commit-secrets-scan.sh`) — hard-blocks the commit if hardcoded
   credentials, API keys, private keys, or connection strings are detected in the staged diff.
2. **Review gate** (`pre-commit-review-gate.sh`) — blocks the commit until the 3-step
   pre-commit protocol has been completed:
   - **Step 1 — LINT**: Invoke the `lint` agent (Sonnet) on staged files. It detects
     project-specific linters, runs them, auto-fixes issues, and re-stages fixed files.
   - **Step 2 — SECURITY**: Invoke the `security-engineer` agent (Sonnet) on staged
     changes (`git diff --cached`). Fix any Critical/High findings, re-stage.
   - **Step 3 — MARKER**: After both agents pass clean, create the review marker:
     `echo "$(git diff --cached | shasum | cut -d' ' -f1)" > /tmp/.claude-pre-commit-reviewed`
   If either agent requires fixes that change staged files, re-run both agents from Step 1.
   The marker hash ensures this — modified staged files invalidate the marker.

**Before creating a PR** (PreToolUse hook on Bash):
- **PR review gate** (`pre-pr-review-gate.sh`) — blocks `gh pr create` until you have
  invoked `code-reviewer` on the full branch diff and created the marker:
  `echo "$(git diff main...HEAD | shasum | cut -d' ' -f1)" > /tmp/.claude-pre-pr-reviewed`

**When editing security-sensitive files** (PostToolUse hook on Edit/Write):
- **File check** (`security-sensitive-file-check.sh`) — detects writes to files matching
  `*auth*`, `*login*`, `*session*`, `*token*`, `*secret*`, `*password*`, `*.env*`,
  CI/CD pipelines, certificate/key files, or security middleware. Injects a context
  reminder to invoke the `security-engineer` agent before the next commit.

**REPAIR pipeline modes** (Stages 4 and 6):
- NOT hook-enforced — orchestrated by RepairBoss at defined pipeline stages.
- Stage 4 threat model and Stage 6 OWASP audit remain as agent dispatch instructions.

## Scripted / Automated Runs

When running Claude Code non-interactively (scripts, CI/CD, cron, automation), always use
`--bare -p` for clean, isolated execution:

```bash
claude --bare -p "your prompt here"
```

**What `--bare` does**: Skips hooks, LSP, plugin sync, skill walks, MCP auto-discovery,
CLAUDE.md loading, and auto-memory. Only built-in tools (Bash, Read, Write, Edit, Glob,
Grep) are available by default. ~14% faster startup.

**Auth**: Requires `ANTHROPIC_API_KEY` env var (OAuth/keychain is skipped in bare mode).

**To add context explicitly** (since CLAUDE.md is skipped):
```bash
claude --bare -p "prompt" --append-system-prompt-file ./review-rules.txt
claude --bare -p "prompt" --mcp-config ./mcp-servers.json
claude --bare -p "prompt" --allowedTools "Bash,Read,WebSearch"
```

**When NOT to use `--bare`**: Interactive sessions, tasks needing hooks or auto-invoke
rules, tasks depending on CLAUDE.md instructions or MCP servers from `.mcp.json`.

## Interaction Rules
- Be concise. No over-explaining.
- Show diffs/snippets, not full files.
- Ask before large refactors or multi-file changes.
- Verify each step before proceeding.
- State uncertainty rather than guess.
- Fix root causes, not symptoms.
- Follow existing project conventions.
- If something is not in context, say so. Never hallucinate or fabricate information.
- When showing code that requires elevated permissions or system changes, include a one-line explanation of what it does.

