#!/bin/bash
# security-sensitive-file-check.sh — PostToolUse hook for Edit/Write.
# Detects writes to security-sensitive files and reminds Claude to invoke
# the security-engineer agent. Runs after the edit, cannot block.
# Outputs to stderr so the harness injects it as context.

INPUT=$(cat)

# Extract file_path from JSON input
FILE_PATH=$(echo "$INPUT" | grep -oE '"file_path"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | sed 's/.*"file_path"[[:space:]]*:[[:space:]]*"//;s/"$//')
[ -z "$FILE_PATH" ] && exit 0

FILENAME=$(basename "$FILE_PATH")
FILEPATH_LOWER=$(echo "$FILE_PATH" | tr '[:upper:]' '[:lower:]')

MATCHED=""

# Check filename patterns
case "$FILENAME" in
    *.env|*.env.*|.env*)           MATCHED="environment config file" ;;
    Dockerfile*|docker-compose*)   MATCHED="container config" ;;
    Jenkinsfile|Makefile)          MATCHED="CI/CD pipeline" ;;
esac

# Check path patterns (case-insensitive)
if [ -z "$MATCHED" ]; then
    if echo "$FILEPATH_LOWER" | grep -qE '(auth|login|session|token|secret|password|credential|api[_-]?key|oauth|saml|sso|permissions|rbac|acl)'; then
        MATCHED="security-sensitive path pattern"
    elif echo "$FILEPATH_LOWER" | grep -qE '\.(pem|key|cert|crt|p12|pfx|jks)$'; then
        MATCHED="certificate/key file"
    elif echo "$FILEPATH_LOWER" | grep -qE '\.github/workflows/|\.gitlab-ci|\.circleci/'; then
        MATCHED="CI/CD pipeline"
    elif echo "$FILEPATH_LOWER" | grep -qE 'cors|csp|security[_-]?headers|middleware'; then
        MATCHED="security middleware/headers"
    fi
fi

if [ -n "$MATCHED" ]; then
    echo "SECURITY NOTICE: You edited a security-sensitive file (${MATCHED}):" >&2
    echo "  ${FILE_PATH}" >&2
    echo "" >&2
    echo "Consider invoking the security-engineer agent to review this change" >&2
    echo "before your next commit." >&2
fi

exit 0
