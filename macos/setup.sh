#!/bin/bash
# =============================================================================
# ag3nts Setup Script (macOS)
# Auto-detects SSD mount point, creates symlinks, syncs shared configs.
# Run with: bash /Volumes/<SSD_NAME>/ag3nts/setup.sh
# =============================================================================

set -e

# --- Helper Functions ---
step()  { echo -e "\n\033[36m>> $1\033[0m"; }
ok()    { echo -e "   \033[32mOK: $1\033[0m"; }
skip()  { echo -e "   \033[33mSKIP: $1\033[0m"; }
fail()  { echo -e "   \033[31mFAIL: $1\033[0m"; }

# --- Pre-flight ---
echo "============================================="
echo "  ag3nts Setup Script (macOS)"
echo "============================================="

# --- Auto-detect SSD ---
step "Scanning for ag3nts folder (up to 5 levels deep)..."

# Search /Volumes (external drives) and $HOME for ag3nts project
BASE_PATH=$(find /Volumes -maxdepth 5 -type d -name "ag3nts" -exec test -f "{}/shared/ag3nts.md" \; -print -quit 2>/dev/null)
if [ -z "$BASE_PATH" ]; then
    BASE_PATH=$(find "$HOME" -maxdepth 4 -type d -name "ag3nts" -exec test -f "{}/shared/ag3nts.md" \; -print -quit 2>/dev/null)
fi

if [ -n "$BASE_PATH" ]; then
    ok "Found ag3nts at $BASE_PATH"
else
    fail "ag3nts folder not found."
    echo "   Searched /Volumes/ and $HOME/ (up to 5 levels deep)"
    echo "   Make sure your SSD is connected and contains the ag3nts folder."
    exit 1
fi

# --- Derived Paths ---
PLATFORM="$BASE_PATH/macos"
SHARED="$BASE_PATH/shared"

CLAUDE_BIN_SSD="$PLATFORM/claude-code/bin"
CLAUDE_CONFIG_SSD="$PLATFORM/claude-code/config"
GEMINI_CONFIG_SSD="$PLATFORM/gemini-cli/config"
CODEX_CONFIG_SSD="$PLATFORM/codex-cli/config"

CLAUDE_BIN_LOCAL="$HOME/.local/bin"
CLAUDE_CONFIG_LOCAL="$HOME/.claude"
GEMINI_CONFIG_LOCAL="$HOME/.gemini"
CODEX_CONFIG_LOCAL="$HOME/.codex"

# --- Validate SSD Contents ---
step "Validating ag3nts folder structure..."
MISSING=()
[ ! -d "$CLAUDE_BIN_SSD" ] && MISSING+=("macos/claude-code/bin")
[ ! -d "$CLAUDE_CONFIG_SSD" ] && MISSING+=("macos/claude-code/config")
[ ! -d "$GEMINI_CONFIG_SSD" ] && MISSING+=("macos/gemini-cli/config")
[ ! -d "$CODEX_CONFIG_SSD" ] && MISSING+=("macos/codex-cli/config")

if [ ${#MISSING[@]} -gt 0 ]; then
    fail "Missing folders in $PLATFORM:"
    for m in "${MISSING[@]}"; do echo "   - $m"; done
    echo ""
    echo "   If this is your first macOS setup, install the tools first:"
    echo "   1. Install Claude Code: curl -fsSL https://claude.ai/install.sh | bash"
    echo "      Then move binary: mv ~/.local/bin/claude $CLAUDE_BIN_SSD/"
    echo "   2. Download portable Node (arm64): https://nodejs.org/en/download"
    echo "      Extract to $PLATFORM/node/"
    echo "   3. Install Gemini & Codex via portable Node's npm with --prefix"
    exit 1
fi
ok "All expected folders found."

# --- Symlink Helper ---
create_symlink() {
    local local_path="$1"
    local ssd_target="$2"
    local label="$3"

    step "Setting up $label symlink..."

    if [ -L "$local_path" ]; then
        current_target=$(readlink "$local_path")
        if [ "$current_target" = "$ssd_target" ]; then
            skip "Symlink already correct: $local_path -> $ssd_target"
        else
            echo "   Updating symlink: $current_target -> $ssd_target"
            rm "$local_path"
            ln -s "$ssd_target" "$local_path"
            ok "Updated symlink: $local_path -> $ssd_target"
        fi
    elif [ -e "$local_path" ]; then
        fail "$local_path already exists and is NOT a symlink."
        echo "   Back it up and remove it, then re-run this script."
        exit 1
    else
        parent_dir=$(dirname "$local_path")
        mkdir -p "$parent_dir"
        ln -s "$ssd_target" "$local_path"
        ok "Created symlink: $local_path -> $ssd_target"
    fi
}

# --- Create Symlinks ---
create_symlink "$CLAUDE_BIN_LOCAL" "$CLAUDE_BIN_SSD" "Claude Code binary"
create_symlink "$CLAUDE_CONFIG_LOCAL" "$CLAUDE_CONFIG_SSD" "Claude Code config"
create_symlink "$GEMINI_CONFIG_LOCAL" "$GEMINI_CONFIG_SSD" "Gemini CLI config"
create_symlink "$CODEX_CONFIG_LOCAL" "$CODEX_CONFIG_SSD" "Codex CLI config"

# --- PATH: Add Claude Code binary location ---
step "Checking PATH for Claude Code..."
CLAUDE_PATH="$HOME/.local/bin"

if echo "$PATH" | grep -q "$CLAUDE_PATH"; then
    skip "PATH already contains $CLAUDE_PATH"
else
    # Detect shell config file
    if [ -f "$HOME/.zshrc" ]; then
        SHELL_RC="$HOME/.zshrc"
    elif [ -f "$HOME/.bashrc" ]; then
        SHELL_RC="$HOME/.bashrc"
    else
        SHELL_RC="$HOME/.zshrc"
    fi

    if grep -q ".local/bin" "$SHELL_RC" 2>/dev/null; then
        skip "PATH entry already in $SHELL_RC (restart terminal to apply)"
    else
        echo "" >> "$SHELL_RC"
        echo "# ag3nts: Claude Code" >> "$SHELL_RC"
        echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$SHELL_RC"
        ok "Added $CLAUDE_PATH to $SHELL_RC"
    fi
fi

# --- Sync Shared Configs ---
step "Syncing shared configs to macOS platform..."

sync_shared() {
    local shared_dir="$1"
    local platform_dir="$2"

    if [ -d "$shared_dir" ]; then
        for file in "$shared_dir"/*; do
            [ -f "$file" ] || continue
            filename=$(basename "$file")
            dest="$platform_dir/$filename"
            # Skip files that are already symlinked
            [ -L "$dest" ] && continue
            if [ -f "$dest" ]; then
                if ! cmp -s "$file" "$dest"; then
                    cp "$file" "$dest"
                    ok "Updated: $filename"
                fi
            else
                cp "$file" "$dest"
                ok "Copied: $filename"
            fi
        done
    fi
}

# Symlink shared Claude Code files (single source of truth, no copies)
symlink_shared() {
    local target="$1"
    local link="$2"
    local label="$3"

    if [ -L "$link" ]; then
        current=$(readlink "$link")
        if [ "$current" = "$target" ]; then
            skip "Symlink already correct: $label"
        else
            rm "$link"
            ln -s "$target" "$link"
            ok "Updated symlink: $label"
        fi
    elif [ -e "$link" ]; then
        rm -rf "$link"
        ln -s "$target" "$link"
        ok "Replaced copy with symlink: $label"
    else
        ln -s "$target" "$link"
        ok "Created symlink: $label"
    fi
}

symlink_shared "../../../shared/ag3nts.md" "$CLAUDE_CONFIG_SSD/ag3nts.md" "ag3nts.md"
symlink_shared "../../../shared/claude-code/CLAUDE.md" "$CLAUDE_CONFIG_SSD/CLAUDE.md" "CLAUDE.md"
symlink_shared "../../../shared/claude-code/statusline.sh" "$CLAUDE_CONFIG_SSD/statusline.sh" "statusline.sh"
symlink_shared "../../../shared/claude-code/files/agents" "$CLAUDE_CONFIG_SSD/agents" "agents/"

sync_shared "$SHARED/claude-code" "$CLAUDE_CONFIG_SSD"
sync_shared "$SHARED/gemini-cli" "$GEMINI_CONFIG_SSD"
sync_shared "$SHARED/codex-cli" "$CODEX_CONFIG_SSD"
ok "Shared config sync complete."

# --- Verification ---
step "Verifying Claude Code..."
if [ -f "$CLAUDE_BIN_SSD/claude" ]; then
    claude_ver=$("$CLAUDE_BIN_SSD/claude" --version 2>&1) && ok "Claude Code $claude_ver" || fail "Claude Code execution failed"
else
    fail "Claude binary not found at $CLAUDE_BIN_SSD/claude"
fi

step "Verifying Gemini CLI..."
if [ -f "$PLATFORM/gemini-cli/gemini-launch.sh" ]; then
    gemini_ver=$("$PLATFORM/gemini-cli/gemini-launch.sh" --version 2>&1) && ok "Gemini CLI $gemini_ver" || fail "Gemini CLI execution failed"
else
    fail "Gemini launcher not found at $PLATFORM/gemini-cli/gemini-launch.sh"
fi

step "Verifying Codex CLI..."
if [ -f "$PLATFORM/codex-cli/codex-launch.sh" ]; then
    codex_ver=$("$PLATFORM/codex-cli/codex-launch.sh" --version 2>&1) && ok "Codex CLI $codex_ver" || fail "Codex CLI execution failed"
else
    fail "Codex launcher not found at $PLATFORM/codex-cli/codex-launch.sh"
fi

# --- Summary ---
echo ""
echo "============================================="
echo "  Setup Complete!"
echo "  SSD detected at: $BASE_PATH"
echo "============================================="
echo ""
echo "  Next steps:"
echo "  1. Restart your terminal (or run: source ~/.zshrc)"
echo "  2. Run 'claude' to authenticate (opens browser)"
echo "  3. Gemini: $PLATFORM/gemini-cli/gemini-launch.sh"
echo "  4. Codex:  $PLATFORM/codex-cli/codex-launch.sh"
echo ""
