#!/bin/bash
# pre-pr-review-gate.sh — Blocks PR creation until code-reviewer has been run
# on the full branch diff. Uses a marker file with branch-diff hash.
# Harness-enforced via PreToolUse hook. Exit 2 = block, Exit 0 = allow.

INPUT=$(cat)

# Only intercept gh pr create commands
echo "$INPUT" | grep -q '"command".*"gh pr create' || exit 0

# Must be inside a git repo
git rev-parse --git-dir >/dev/null 2>&1 || exit 0

REVIEW_MARKER="/tmp/.claude-pre-pr-reviewed"

# Detect base branch
BASE_BRANCH=$(git symbolic-ref refs/remotes/origin/HEAD 2>/dev/null | sed 's@^refs/remotes/origin/@@')
[ -z "$BASE_BRANCH" ] && BASE_BRANCH="main"

# Compute hash of full branch diff
BRANCH_HASH=$(git diff "${BASE_BRANCH}...HEAD" 2>/dev/null | shasum | cut -d' ' -f1)

# Check if review marker exists with matching hash
if [ -f "$REVIEW_MARKER" ]; then
    MARKER_HASH=$(cat "$REVIEW_MARKER" 2>/dev/null)
    if [ "$BRANCH_HASH" = "$MARKER_HASH" ]; then
        rm -f "$REVIEW_MARKER"
        exit 0
    fi
    rm -f "$REVIEW_MARKER"
fi

# Block PR creation
cat >&2 << EOF
BLOCKED: Pre-PR review gate.

You must run code-reviewer on the full branch diff before creating a PR.

Steps:
1. Run the code-reviewer agent on the full branch diff:
   git diff ${BASE_BRANCH}...HEAD
2. Fix any blockers, commit the fixes.
3. After code-reviewer passes clean, create the review marker:
   echo "\$(git diff ${BASE_BRANCH}...HEAD | shasum | cut -d' ' -f1)" > /tmp/.claude-pre-pr-reviewed
4. Then retry gh pr create.

Branch diff hash: $BRANCH_HASH
EOF
exit 2
