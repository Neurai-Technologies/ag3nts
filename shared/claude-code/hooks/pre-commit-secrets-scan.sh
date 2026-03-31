#!/bin/bash
# pre-commit-secrets-scan.sh — Hard-blocks git commit if secrets are detected in staged changes.
# Harness-enforced via PreToolUse hook. Exit 2 = block, Exit 0 = allow.

INPUT=$(cat)

# Only intercept git commit commands (not git commit-graph, git commit-tree, etc.)
echo "$INPUT" | grep -qE '"command"[[:space:]]*:[[:space:]]*"[^"]*\bgit\b[^"]*\bcommit\b( |")' || exit 0

# Check if there are staged changes
git diff --cached --quiet 2>/dev/null && exit 0

STAGED_DIFF=$(git diff --cached 2>/dev/null)
[ -z "$STAGED_DIFF" ] && exit 0

FINDINGS=""

# Check for .env files being committed
ENV_FILES=$(git diff --cached --name-only 2>/dev/null | grep -E '\.env($|\.)')
if [ -n "$ENV_FILES" ]; then
    FINDINGS="${FINDINGS}\n- .env file(s) staged for commit: ${ENV_FILES}"
fi

# Scan staged diff for secret patterns
check_pattern() {
    local pattern="$1"
    local label="$2"
    local matches
    matches=$(echo "$STAGED_DIFF" | grep -nE "^\+" | grep -iE "$pattern" | grep -v 'not-a-secret' | head -5)
    if [ -n "$matches" ]; then
        FINDINGS="${FINDINGS}\n- ${label}:\n${matches}"
    fi
}

# AWS keys
check_pattern 'AKIA[0-9A-Z]{16}' "AWS Access Key ID"
check_pattern 'aws_secret_access_key\s*=' "AWS Secret Access Key assignment"

# API keys and tokens (generic)
check_pattern '(api[_-]?key|api[_-]?secret|api[_-]?token)\s*[:=]\s*["\x27][A-Za-z0-9/+=]{16,}' "API key/secret/token assignment"

# Private keys
check_pattern 'BEGIN (RSA |EC |DSA |OPENSSH )?PRIVATE KEY' "Private key"

# GitHub tokens
check_pattern 'gh[pousr]_[A-Za-z0-9_]{36,}' "GitHub token"

# OpenAI / Anthropic keys
check_pattern 'sk-[a-zA-Z0-9]{20,}' "OpenAI/Anthropic API key"

# Generic secret assignments (high-confidence patterns only)
check_pattern '(password|passwd|secret|credential)\s*[:=]\s*["\x27][^\s"'\'']{8,}' "Hardcoded password/secret"

# Connection strings with credentials
check_pattern '(mysql|postgres|mongodb|redis)://[^:]+:[^@]+@' "Database connection string with credentials"

# JWT tokens
check_pattern 'eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.' "JWT token"

if [ -n "$FINDINGS" ]; then
    echo "BLOCKED: Potential secrets detected in staged changes." >&2
    echo "" >&2
    echo "Findings:" >&2
    printf '%b\n' "$FINDINGS" >&2
    echo "" >&2
    echo "Actions:" >&2
    echo "1. Remove the secrets from the staged files" >&2
    echo "2. Use environment variables or a secrets manager instead" >&2
    echo "3. If these are false positives, add a comment '# not-a-secret' on the line" >&2
    exit 2
fi

exit 0
