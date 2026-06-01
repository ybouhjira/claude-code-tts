#!/bin/bash

# Auto-speak hook for Claude Code
# Speaks Claude's last response

# Only run if TTS is explicitly enabled for this session.
# Enable by launching Claude with: CLAUDE_TTS_ENABLED=true claude
if [ "${CLAUDE_TTS_ENABLED:-false}" != "true" ]; then
    exit 0
fi

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/claude-code-tts}"
SPEAK_BIN="$PLUGIN_ROOT/bin/speak-text"

json=$(cat)

{
    # Use last_assistant_message from the payload — it contains the current response
    # and is always present, avoiding the transcript-not-yet-flushed race condition
    msg=$(echo "$json" | jq -r '.last_assistant_message // ""' 2>/dev/null)

    # Fall back to transcript parsing if payload field is absent
    if [ -z "$msg" ]; then
        transcript=$(echo "$json" | jq -r '.transcript_path // ""' 2>/dev/null)
        if [ -n "$transcript" ] && [ -f "$transcript" ]; then
            msg=$(grep '"role":"assistant"' "$transcript" 2>/dev/null | while read -r line; do
                echo "$line" | jq -r '
                    if (.message.content | type == "array") then
                        [.message.content[] | select(.type == "text") | .text] | join(" ")
                    else "" end
                ' 2>/dev/null
            done | grep -v "^[[:space:]]*$" | tail -1)
        else
            msg=$(echo "$json" | jq -r '.stop_hook_message // .message // .content // ""' 2>/dev/null)
        fi
    fi

    # Skip if empty or too short
    [ -z "$msg" ] || [ ${#msg} -lt 10 ] && exit 0

    [ -x "$SPEAK_BIN" ] && "$SPEAK_BIN" "$msg" 2>/dev/null
} &

exit 0
