#!/usr/bin/env bash
input=$(cat)
python3 -c "
import sys, json
try:
    data = json.loads(sys.argv[1])
    model = data.get('model', {}).get('display_name', 'Unknown')
    used = data.get('context_window', {}).get('used_percentage')
    if used is not None:
        print('{} | ctx: {:.0f}% used'.format(model, used))
    else:
        print(model)
except Exception:
    print('Claude Code')
" "$input" 2>/dev/null || python -c "
import sys, json
try:
    data = json.loads(sys.argv[1])
    model = data.get('model', {}).get('display_name', 'Unknown')
    used = data.get('context_window', {}).get('used_percentage')
    if used is not None:
        print('{} | ctx: {:.0f}% used'.format(model, used))
    else:
        print(model)
except Exception:
    print('Claude Code')
" "$input" 2>/dev/null || echo "Claude Code"
