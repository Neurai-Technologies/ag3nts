#!/bin/bash
# pre-commit-review-gate.sh — Blocks git commit until code-reviewer and security-engineer
# agents have been run. Uses a marker file with staged-diff hash to validate.
# Harness-enforced via PreToolUse hook. Exit 2 = block, Exit 0 = allow.

INPUT=$(cat)

# Only intercept git commit commands (not git commit-graph, git commit-tree, etc.)
echo "$INPUT" | grep -qE '"command"[[:space:]]*:[[:space:]]*"[^"]*\bgit\b[^"]*\bcommit\b( |")' || exit 0

# Check if there are staged changes
git diff --cached --quiet 2>/dev/null && exit 0

REVIEW_MARKER="/tmp/.claude-pre-commit-reviewed"

# Compute hash of current staged changes (guard against git unavailable)
STAGED_HASH=$(git diff --cached 2>/dev/null | shasum | cut -d' ' -f1)
[ "$STAGED_HASH" = "da39a3ee5e6b4b0d3255bfef95601890afd80709" ] && exit 0

# Check if review marker exists with matching hash
if [ -f "$REVIEW_MARKER" ]; then
    MARKER_HASH=$(cat "$REVIEW_MARKER" 2>/dev/null)
    if [ "$STAGED_HASH" = "$MARKER_HASH" ]; then
        # Review was done for these exact changes — allow commit, clean up marker
        rm -f "$REVIEW_MARKER"
        exit 0
    fi
    # Staged changes differ from what was reviewed — re-review required
    rm -f "$REVIEW_MARKER"
fi

# No valid review marker — block the commit
cat >&2 << EOF
BLOCKED: Pre-commit review gate.

You must run code-reviewer and security-engineer agents before committing.

Steps:
1. Run the code-reviewer agent on staged changes (git diff --cached).
   Fix any blockers it reports, re-stage fixed files.
2. Run the security-engineer agent on staged changes (git diff --cached).
   Fix any Critical/High findings, re-stage fixed files.
   If you had to fix issues, re-run both agents.
3. After both agents pass clean, create the review marker:
   echo "\$(git diff --cached | shasum | cut -d' ' -f1)" > /tmp/.claude-pre-commit-reviewed
4. Then retry the git commit.

Staged diff hash: $STAGED_HASH
EOF
exit 2
