# ag3nts.md â€” Shared Agent Instructions

## Context
Developer: Rohan. Works across Windows + macOS. All AI tools on portable SSD at `Terminal-AI/` with symlinks to `~/.claude`, `~/.gemini`, `~/.codex`. Setup scripts: `windows/setup.ps1`, `macos/setup.sh`.

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

