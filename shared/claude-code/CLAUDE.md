# CLAUDE.md — Claude Code Agent Config

@ag3nts.md

## Claude-Specific Notes

- This config is managed via the ag3nts portable SSD setup.
- Global config lives in `shared/claude-code/`, platform config in `<platform>/claude-code/config/`.
- Use `/compact` when context usage exceeds 80%. Each pipeline stage has a `## Compact Instructions` section — follow those preservation rules when compacting.
- Use `/init` in new project directories to generate project-level CLAUDE.md files.
- When editing ag3nts setup scripts, always verify with the setup command after changes.
