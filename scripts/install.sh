#!/bin/bash
set -euo pipefail

# ag3nts installer
# Usage: curl -fsSL <url>/install.sh | bash

REPO="rohanrgit/ag3nts"
BINARY="ag3nts"
INSTALL_DIR="${AG3NTS_INSTALL_DIR:-$HOME/.local/bin}"

# Colors
GREEN='\033[32m'
RED='\033[31m'
CYAN='\033[36m'
RESET='\033[0m'

step() { echo -e "${CYAN}→${RESET} $1"; }
ok() { echo -e "${GREEN}✓${RESET} $1"; }
fail() { echo -e "${RED}✗${RESET} $1"; exit 1; }

# Check architecture
ARCH=$(uname -m)
if [ "$ARCH" != "arm64" ]; then
    fail "ag3nts v0.1 requires Apple Silicon (arm64). Detected: $ARCH"
fi

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
if [ "$OS" != "darwin" ]; then
    fail "ag3nts v0.1 requires macOS. Detected: $OS"
fi

# Check for git
if ! command -v git &>/dev/null; then
    fail "git is required. Install it with: xcode-select --install"
fi

# Get latest release
step "Fetching latest release..."
RELEASE_URL="https://api.github.com/repos/${REPO}/releases/latest"
RELEASE_JSON=$(curl -fsSL "$RELEASE_URL" 2>/dev/null) || fail "Could not fetch release info from GitHub"

TAG=$(echo "$RELEASE_JSON" | grep '"tag_name"' | head -1 | cut -d'"' -f4)
if [ -z "$TAG" ]; then
    fail "Could not determine latest version"
fi

# Find darwin-arm64 asset
ASSET_URL=$(echo "$RELEASE_JSON" | grep '"browser_download_url"' | grep "darwin_arm64" | head -1 | cut -d'"' -f4)
if [ -z "$ASSET_URL" ]; then
    fail "No darwin-arm64 asset found in release $TAG"
fi

# Download and extract
step "Downloading ag3nts ${TAG}..."
TMP=$(mktemp -d)
trap "rm -rf $TMP" EXIT

curl -fsSL "$ASSET_URL" -o "$TMP/ag3nts.tar.gz" || fail "Download failed"
tar -xzf "$TMP/ag3nts.tar.gz" -C "$TMP" || fail "Extract failed"

# Install binary
step "Installing to ${INSTALL_DIR}..."
mkdir -p "$INSTALL_DIR"
cp "$TMP/$BINARY" "$INSTALL_DIR/$BINARY"
chmod +x "$INSTALL_DIR/$BINARY"

# Check if install dir is in PATH
if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
    echo ""
    echo "Add this to your shell profile (~/.zshrc or ~/.bashrc):"
    echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
    echo ""
fi

ok "ag3nts ${TAG} installed to ${INSTALL_DIR}/${BINARY}"
echo ""
echo "Next steps:"
echo "  1. ag3nts install                           # install AI coding tools"
echo "  2. ag3nts workflow install <name> --repo <url>  # install your workflow"
echo "  3. ag3nts                                    # launch master agent"
