#!/bin/bash
# Auto-speak Stop hook for Claude Code.
#
# Claude Code runs this after every response and passes a JSON object on stdin.
# The response text is in the .last_assistant_message field on current versions;
# the other field names are kept as fallbacks for older versions.
#
# What it speaks is controlled by config (see tts_load_config). The knobs:
#   TTS_SPEAK_MODE   sentence | full | off       (default: sentence)
#   TTS_MAX_CHARS    character cap, 0 = no cap    (default: 200 sentence, 0 full)
#   TTS_STRIP_CODE   1 = skip code blocks         (default: 1)
#   TTS_STRIP_MARKDOWN 1 = strip Markdown markup  (default: 1)
#   TTS_PROVIDER / TTS_VOICE                       (default: your .env values)

PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/claude-code-tts}"
# shellcheck source=/dev/null
. "$PLUGIN_ROOT/hooks/tts-common.sh"
tts_load_config
tts_apply_runtime_overrides   # on-the-fly /tts-* changes win over the config files

# Read the whole hook payload and pull out the response text.
json=$(cat)
msg=$(printf '%s' "$json" | jq -r '.last_assistant_message // .stop_hook_message // .message // .content // ""' 2>/dev/null)
[ -z "$msg" ] && exit 0

# Always cache the full response so `speak-last` can replay it later, even when
# auto-speak is off or only the first sentence is spoken.
printf '%s' "$msg" > "$PLUGIN_ROOT/last-response.md" 2>/dev/null

mode="${TTS_SPEAK_MODE:-sentence}"
[ "$mode" = "off" ] && exit 0

# Do the speaking in the background so Claude is never blocked.
{
    cleaned=$(printf '%s' "$msg" | tts_clean)

    case "$mode" in
        full)
            cap="${TTS_MAX_CHARS:-0}"
            text="$cleaned"
            ;;
        *)
            # "sentence" mode: take up to the first sentence terminator.
            cap="${TTS_MAX_CHARS:-200}"
            text=$(printf '%s' "$cleaned" | tr '\n' ' ' | sed -E 's/([.!?]).*/\1/')
            ;;
    esac

    # Apply the character cap. A cap of 0 (or unset) means no cap.
    if [ "${cap:-0}" -gt 0 ] 2>/dev/null; then
        text=$(printf '%s' "$text" | head -c "$cap")
    fi

    # Skip anything too short to be worth speaking.
    [ "${#text}" -lt 2 ] && exit 0

    printf '%s' "$text" | tts_speak_stream
} >/dev/null 2>&1 &

exit 0
