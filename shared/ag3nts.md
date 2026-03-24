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
| `code-reviewer` | Sonnet | - | Dual-mode: REPAIR Stage 6 sub-step + auto-invokes before commit/push/PR |
| `accessibility-auditor` | Sonnet | WCAG refs | WCAG 2.2 AA audits, screen reader testing, POUR checklist |
| `software-architect` | Opus | Patterns | Dual-mode: REPAIR Stage 4 sub-step (ADRs, domain modeling) + standalone |
| `reality-checker` | Sonnet | - | Production readiness gate, defaults to NEEDS WORK |
| `security-engineer` | Opus | CVEs | Tri-mode: Stage 4 threat model + Stage 6 OWASP audit + auto-invoke on auth/secrets |
| `ux-architect` | Sonnet | Tailwind | Design tokens, theme scaffolding, layout systems |
| `version` | Haiku | - | Agent inventory audit, consistency checks, drift detection |

Source: adapted from [agency-agents](https://github.com/msitarzewski/agency-agents) (engineering, design, testing divisions), trimmed and customized for this stack.

## Auto-Invoke Rules

**Before any `git commit` or `git push`**: Always invoke the `code-reviewer` agent first.
It reviews staged changes, fixes blockers automatically, and reports suggestions. Do not
commit or push until the code-reviewer pass completes. If blockers were fixed, re-stage
the affected files before proceeding with the commit.

**Before any `git push`**: After the code-reviewer pass, always invoke the `security-engineer`
agent. It scans the full diff being pushed (`git diff origin/<branch>...HEAD`) for
vulnerabilities — secrets, injection flaws, insecure dependencies, OWASP Top 10 issues,
and misconfigurations. **The push is blocked until the security-engineer returns a clean
report.** If any Critical or High severity findings are reported, the push MUST NOT proceed.
Fix the vulnerabilities, re-stage, commit the fix, then re-run the security-engineer scan.
Medium/Low findings are reported as warnings but do not block the push.

**Before creating a PR**: Invoke `code-reviewer` on the full branch diff (`git diff main...HEAD`).

**When touching security-sensitive files**: Invoke `security-engineer` when changes touch
files matching `*auth*`, `*login*`, `*session*`, `*token*`, `*secret*`, `*password*`,
`*.env*`, config files, CI/CD pipelines, or files importing crypto/auth/JWT libraries.

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

