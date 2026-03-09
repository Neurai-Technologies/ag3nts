# ag3nts

A portable, cross-platform AI coding agent toolkit. Run **Claude Code**, **Gemini CLI**, and **OpenAI Codex CLI** from a single external SSD — plug into any Windows or macOS machine, run one setup script, and start coding.

## What this is

A pre-configured, portable setup for three AI terminal agents that:

- Lives entirely on an external SSD (or any portable drive)
- Uses symlinks so each tool thinks its config is in the default location
- Auto-syncs a shared instruction file (`ag3nts.md`) across all three agents
- Provides a central `bin/` folder — one PATH entry for all tools
- Works on both Windows and macOS with platform-specific binaries and scripts

## Architecture

```
ag3nts/
├── shared/                          # Cross-platform configs (single source of truth)
│   ├── ag3nts.md                    # Canonical instructions for all agents
│   ├── claude-code/
│   │   ├── CLAUDE.md                # Claude Code stub (imports ag3nts.md)
│   │   └── settings.json            # Claude Code global settings
│   ├── gemini-cli/
│   │   ├── GEMINI.md                # Gemini CLI stub (imports ag3nts.md)
│   │   └── settings.json            # Gemini global settings
│   └── codex-cli/
│       ├── AGENTS.md                # Codex CLI stub (imports ag3nts.md)
│       └── config.toml              # Codex global config
│
├── windows/
│   ├── setup.ps1                    # Windows bootstrap (run as Admin)
│   ├── bin/                         # Central launchers (symlinked to ~/.local/bin)
│   │   ├── claude.cmd               # Auto-detects SSD, syncs configs, launches Claude
│   │   ├── gemini.cmd               # Auto-detects SSD, syncs configs, launches Gemini
│   │   └── codex.cmd                # Auto-detects SSD, syncs + concatenates, launches Codex
│   ├── claude-code/
│   │   ├── bin/                     # claude.exe (Windows binary)
│   │   └── config/                  # Symlinked from ~/.claude
│   ├── gemini-cli/
│   │   ├── config/                  # Symlinked from ~/.gemini
│   │   ├── node_modules/            # Gemini npm package
│   │   └── gemini-launch.cmd        # Portable Node launcher
│   ├── codex-cli/
│   │   ├── config/                  # Symlinked from ~/.codex
│   │   ├── node_modules/            # Codex npm package
│   │   └── codex-launch.cmd         # Portable Node launcher
│   └── node/                        # Portable Node.js (win-x64)
│
├── macos/
│   ├── setup.sh                     # macOS bootstrap
│   ├── bin/                         # Central launchers
│   │   ├── claude                   # macOS launcher
│   │   ├── gemini                   # macOS launcher
│   │   └── codex                    # macOS launcher
│   ├── claude-code/
│   │   ├── bin/                     # claude (macOS arm64 binary)
│   │   └── config/                  # Symlinked from ~/.claude
│   ├── gemini-cli/
│   │   ├── config/                  # Symlinked from ~/.gemini
│   │   ├── node_modules/            # Gemini npm package
│   │   └── gemini-launch.sh         # Portable Node launcher
│   ├── codex-cli/
│   │   ├── config/                  # Symlinked from ~/.codex
│   │   ├── node_modules/            # Codex npm package
│   │   └── codex-launch.sh          # Portable Node launcher
│   └── node/                        # Portable Node.js (darwin-arm64)
│
└── .gitignore
```

## How it works

### Shared instructions via `ag3nts.md`

All three agents read from the same canonical instruction file (`shared/ag3nts.md`). Each tool has its own stub file that imports `ag3nts.md`:

| Tool | Reads | Stub imports |
|---|---|---|
| Claude Code | `CLAUDE.md` | `@ag3nts.md` (native import) |
| Gemini CLI | `GEMINI.md` | `@ag3nts.md` (native import) |
| Codex CLI | `AGENTS.md` | Concatenated by launcher (Codex doesn't support `@` imports) |

Edit `shared/ag3nts.md` once → all three agents pick up the changes on next launch.

### Auto-sync launchers

The `bin/*.cmd` launchers do three things every time you run a tool:

1. **Scan all drives** for the `Terminal-AI/` folder (auto-detects drive letter)
2. **Sync** `ag3nts.md` and the tool's stub file from `shared/` to the platform config
3. **Launch** the tool

No manual copying, no remembering to re-run scripts.

### Symlink architecture

Each machine has symlinks pointing from the tool's expected config location to the SSD:

```
~/.claude    →  <SSD>/ag3nts/windows/claude-code/config/
~/.gemini    →  <SSD>/ag3nts/windows/gemini-cli/config/
~/.codex     →  <SSD>/ag3nts/windows/codex-cli/config/
~/.local/bin →  <SSD>/ag3nts/windows/bin/
```

The setup scripts create these automatically.

## Quick start

### Prerequisites

- **Claude Code**: Claude Pro, Max, Teams, or Enterprise account
- **Gemini CLI**: Google account
- **Codex CLI**: ChatGPT Plus, Pro, or Enterprise account
- **Git for Windows** (Claude Code requires Git Bash internally)

### Windows

1. Clone this repo to your portable SSD:
   ```powershell
   git clone https://github.com/rohanrgit/ag3nts.git D:\ag3nts
   ```
   Or rename the folder to `Terminal-AI` if you prefer:
   ```powershell
   git clone https://github.com/rohanrgit/ag3nts.git D:\Terminal-AI
   ```

2. Install the tool binaries (one-time per platform):
   ```powershell
   # Claude Code — native installer, then move binary
   irm https://claude.ai/install.ps1 | iex
   Move-Item "$env:USERPROFILE\.local\bin\claude.exe" "D:\Terminal-AI\windows\claude-code\bin\"

   # Portable Node.js — download zip, extract
   Invoke-WebRequest -Uri "https://nodejs.org/dist/v22.x.x/node-v22.x.x-win-x64.zip" -OutFile node.zip
   Expand-Archive node.zip -DestinationPath "D:\Terminal-AI\windows\node\"
   Remove-Item node.zip

   # Gemini CLI — install via portable npm
   & "D:\Terminal-AI\windows\node\node-v22.x.x-win-x64\npm.cmd" install -g @google/gemini-cli --prefix "D:\Terminal-AI\windows\gemini-cli"

   # Codex CLI — install via portable npm
   & "D:\Terminal-AI\windows\node\node-v22.x.x-win-x64\npm.cmd" install -g @openai/codex --prefix "D:\Terminal-AI\windows\codex-cli"
   ```

3. Run the setup script (PowerShell as Administrator):
   ```powershell
   & "D:\Terminal-AI\windows\setup.ps1"
   ```

4. Open a new terminal and start using:
   ```powershell
   claude    # launches Claude Code
   gemini    # launches Gemini CLI
   codex     # launches Codex CLI
   ```

### macOS

1. Clone this repo to your SSD (mounted at `/Volumes/<SSD_NAME>/`):
   ```bash
   git clone https://github.com/rohanrgit/ag3nts.git /Volumes/<SSD_NAME>/Terminal-AI
   ```

2. Install tool binaries and portable Node (arm64 for Apple Silicon):
   ```bash
   # Claude Code
   curl -fsSL https://claude.ai/install.sh | bash
   mv ~/.local/bin/claude /Volumes/<SSD_NAME>/Terminal-AI/macos/claude-code/bin/

   # Portable Node.js (arm64)
   curl -o node.tar.gz https://nodejs.org/dist/v22.x.x/node-v22.x.x-darwin-arm64.tar.gz
   tar -xzf node.tar.gz -C /Volumes/<SSD_NAME>/Terminal-AI/macos/node/
   rm node.tar.gz

   # Gemini CLI
   /Volumes/<SSD_NAME>/Terminal-AI/macos/node/node-v22.x.x-darwin-arm64/bin/npm install -g @google/gemini-cli --prefix /Volumes/<SSD_NAME>/Terminal-AI/macos/gemini-cli

   # Codex CLI
   /Volumes/<SSD_NAME>/Terminal-AI/macos/node/node-v22.x.x-darwin-arm64/bin/npm install -g @openai/codex --prefix /Volumes/<SSD_NAME>/Terminal-AI/macos/codex-cli
   ```

3. Run the setup script:
   ```bash
   bash /Volumes/<SSD_NAME>/Terminal-AI/macos/setup.sh
   ```

4. Restart terminal and authenticate each tool.

## Customization

### Editing shared instructions

Edit `shared/ag3nts.md` — changes sync automatically on next tool launch.

### Tool-specific instructions

Edit the stubs in `shared/<tool>/`:
- `shared/claude-code/CLAUDE.md` — Claude-specific notes
- `shared/gemini-cli/GEMINI.md` — Gemini-specific notes
- `shared/codex-cli/AGENTS.md` — Codex-specific notes

### Adding a new tool

1. Create `<platform>/<tool-name>/` with `bin/` and `config/` subdirectories
2. Add a launcher in `<platform>/bin/`
3. Add sync logic for any shared configs
4. Update the setup script

### Sub-agents (Claude Code)

Custom sub-agents live in `~/.claude/agents/`. This repo includes a pre-built `web-researcher` agent at `shared/claude-code/agents/web-researcher.md` that:
- Searches the web using WebSearch
- Fetches and cross-references multiple sources
- Returns structured briefings with source URLs
- Runs on Sonnet to save tokens (main agent stays on Opus)

## What's NOT in this repo

The repo contains **scripts, configs, and instructions only** — not the actual tool binaries or npm packages. These are large, platform-specific, and change frequently. You install them once using the commands above, and the setup script handles the rest.

Files excluded via `.gitignore`:
- Tool binaries (`claude.exe`, `node.exe`, etc.)
- npm `node_modules/`
- Auth tokens and credentials
- Session data, caches, logs
- SQLite databases

## Requirements

| Tool | Subscription needed |
|---|---|
| Claude Code | Claude Pro ($20/mo), Max ($100-200/mo), or API key |
| Gemini CLI | Google account (free tier available) |
| Codex CLI | ChatGPT Plus ($20/mo), Pro, or Enterprise |

| Platform | Requirements |
|---|---|
| Windows | Windows 10+, Git for Windows, PowerShell (Admin for setup) |
| macOS | macOS 12+, Apple Silicon (M1/M2/M3/M4) |

## License

MIT

## Author

Rohan ([@rohanrgit](https://github.com/rohanrgit))
