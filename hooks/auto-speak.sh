#!/bin/bash

# Auto-speak hook for Claude Code
# This Stop hook automatically speaks a summary of Claude's response using TTS

set -euo pipefail

LOG_FILE="$HOME/.claude/tts-hook.log"
TIMEOUT=5
PLUGIN_ROOT="${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/claude-code-tts}"
SPEAK_BIN="$PLUGIN_ROOT/bin/speak-text"

# Function to log messages
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >> "$LOG_FILE"
}

# Function to extract assistant message from JSON
extract_message() {
    local json="$1"
    # Try multiple possible fields where the message might be
    echo "$json" | jq -r '.stop_hook_message // .assistant_message // .message // .content // ""' 2>/dev/null || echo ""
}

# Function to summarize using gemini
summarize_with_gemini() {
    local message="$1"
    local prompt="Summarize what Claude did in ONE sentence (max 15 words) for text-to-speech. Focus on: completions, errors, questions, status updates. Skip code details. Be concise like: \"Created the login component\" or \"Found 3 errors in tests\". Response: $message"

    timeout 3s gemini "$prompt" 2>/dev/null | head -n 1 || echo ""
}

# Main execution
log "Hook triggered"

# Read JSON from stdin
json=$(cat)

# Extract assistant message
message=$(extract_message "$json")

if [ -z "$message" ]; then
    log "No message found in JSON, skipping TTS"
    exit 0
fi

# Skip if message is too short (likely just a question or acknowledgment)
msg_length=${#message}
if [ "$msg_length" -lt 50 ]; then
    log "Message too short ($msg_length chars), skipping TTS"
    exit 0
fi

# Check if speak-text binary exists
if [ ! -f "$SPEAK_BIN" ]; then
    log "Error: speak-text binary not found at $SPEAK_BIN"
    exit 0
fi

# Run TTS in background to not block Claude
{
    # Try to generate summary with gemini
    if command -v gemini &> /dev/null; then
        summary=$(summarize_with_gemini "$message")
        log "Gemini summary: $summary"
    else
        log "Warning: gemini CLI not found, using first 100 chars of message"
        summary="${message:0:100}"
    fi

    # Skip if summary is empty
    if [ -z "$summary" ]; then
        log "Empty summary, skipping TTS"
        exit 0
    fi

    # Speak the summary (with timeout to prevent hanging)
    log "Speaking: $summary"
    timeout "$TIMEOUT" "$SPEAK_BIN" "$summary" 2>&1 | tee -a "$LOG_FILE" || {
        log "Error: TTS failed or timed out"
    }

    log "Hook completed"
} &

# Exit immediately (don't wait for background job)
exit 0
