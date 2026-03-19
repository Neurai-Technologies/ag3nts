#!/usr/bin/env bash
# Claude Code status line script
# Reads JSON from stdin, outputs a compact one-line status

input=$(cat)

# Extract fields
model=$(echo "$input" | jq -r '.model.display_name // "Unknown"')
used_pct=$(echo "$input" | jq -r '.context_window.used_percentage // empty')
cost=$(echo "$input" | jq -r '.cost.total_cost_usd // empty')
agent=$(echo "$input" | jq -r '.agent.name // empty')

# Build context progress bar (10 chars wide, ▓░ style)
if [ -n "$used_pct" ]; then
  pct_int=$(printf "%.0f" "$used_pct")
  filled=$(( pct_int * 10 / 100 ))
  [ "$filled" -gt 10 ] && filled=10
  empty=$(( 10 - filled ))
  bar=""
  for i in $(seq 1 "$filled"); do bar="${bar}▓"; done
  for i in $(seq 1 "$empty");  do bar="${bar}░"; done
  ctx_part="${bar} ${pct_int}%"
else
  ctx_part="░░░░░░░░░░ --%"
fi

# Format cost
if [ -n "$cost" ]; then
  cost_part="$(printf '$%.2f' "$cost")"
else
  cost_part='$-.--'
fi

# Build output
output="${model} ${ctx_part} | ${cost_part}"

if [ -n "$agent" ]; then
  output="${output} | Agent: ${agent}"
fi

printf '%s' "$output"
