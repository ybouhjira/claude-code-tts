#!/bin/bash

# Auto-speak hook for Claude Code
# Speaks the first sentence of Claude's response

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/claude-code-tts}"
SPEAK_BIN="$PLUGIN_ROOT/bin/speak-text"

# Read JSON from stdin, extract message, get first sentence, speak it
{
    json=$(cat)
    msg=$(echo "$json" | jq -r '.stop_hook_message // .message // .content // ""' 2>/dev/null)

    # Skip if empty or too short
    [ -z "$msg" ] || [ ${#msg} -lt 30 ] && exit 0

    # Get first sentence (up to first period, max 200 chars)
    summary=$(echo "$msg" | sed 's/\..*/./' | head -c 200)

    # Speak it
    [ -x "$SPEAK_BIN" ] && timeout 5 "$SPEAK_BIN" "$summary" 2>/dev/null
} &

exit 0
