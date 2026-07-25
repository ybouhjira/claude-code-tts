#!/bin/bash
# Shared helpers for the Claude Code TTS hook and the on-demand commands.
#
# This file is meant to be sourced, not executed. It defines three things:
#   tts_load_config   - load API keys and tuning knobs from config files
#   tts_clean         - filter stdin to stdout, removing code and Markdown
#   tts_speak_stream  - read stdin, split it into chunks, and speak each one
#
# Every knob is a plain environment variable, so you can set it in a config
# file, export it in your shell, or prefix it on a single command line.

# Where the plugin lives. Claude Code sets CLAUDE_PLUGIN_ROOT when it runs the
# hook; on-demand commands fall back to the standard install path.
: "${PLUGIN_ROOT:=${CLAUDE_PLUGIN_ROOT:-$HOME/.claude/plugins/claude-code-tts}}"
: "${SPEAK_BIN:=$PLUGIN_ROOT/bin/speak-text}"

# A byte that will not appear in normal text (ASCII "record separator"). It is
# used to mark the boundaries between spoken chunks.
TTS_SEP=$'\x1e'

# tts_load_env_file FILE
# Read KEY=value lines from FILE and export each one, but only if that variable
# is not already set. This means a value you set by hand (in your shell, or on
# the command line) always wins over the file. Lines starting with '#' and
# blank lines are ignored. One layer of surrounding quotes is removed. The file
# is parsed, not sourced, so it cannot run arbitrary shell code.
tts_load_env_file() {
    local file="$1" force="${2:-}" line key value
    [ -f "$file" ] || return 0
    while IFS= read -r line || [ -n "$line" ]; do
        # Trim leading whitespace.
        line="${line#"${line%%[![:space:]]*}"}"
        # Skip blank lines and comment lines.
        [ -z "$line" ] && continue
        [ "${line#\#}" != "$line" ] && continue
        # Must look like KEY=VALUE.
        case "$line" in
            *=*) ;;
            *) continue ;;
        esac
        key="${line%%=*}"
        value="${line#*=}"
        # Trim trailing whitespace from the key name.
        key="${key%"${key##*[![:space:]]}"}"
        # Accept only valid shell variable names.
        case "$key" in
            [!A-Za-z_]* | *[!A-Za-z0-9_]*) continue ;;
        esac
        # Remove one layer of surrounding double or single quotes.
        case "$value" in
            \"*\") value="${value#\"}"; value="${value%\"}" ;;
            \'*\') value="${value#\'}"; value="${value%\'}" ;;
        esac
        # In force mode always set it; otherwise only if it is not already set.
        # "Not already set" lets a value from your shell win over the file.
        if [ -n "$force" ] || [ -z "${!key+x}" ]; then
            export "$key=$value"
        fi
    done < "$file"
}

# tts_load_config
# Load keys and knobs from the two well-known config files. The plugin-local
# .env is read first (it holds API keys). The environment.d file is read second
# (it holds tuning knobs, and may also hold keys). Values already in the
# environment are never overwritten.
tts_load_config() {
    tts_load_env_file "$PLUGIN_ROOT/.env"
    tts_load_env_file "$HOME/.config/environment.d/claude-code-tts.conf"
}

# The runtime override file holds on-the-fly settings changed from inside Claude
# Code with the /tts-* slash commands. It is separate from the config files so
# quick changes never touch your hand-edited config.
: "${TTS_RUNTIME_FILE:=$PLUGIN_ROOT/runtime.env}"

# tts_apply_runtime_overrides
# Force-load the runtime file so its values win over everything else, including
# the config files and any variables already in the environment. The hook calls
# this so a /tts-* change takes effect on the very next response.
tts_apply_runtime_overrides() {
    tts_load_env_file "$TTS_RUNTIME_FILE" force
}

# tts_set_runtime KEY VALUE
# Write (or replace) one KEY=VALUE line in the runtime override file.
tts_set_runtime() {
    local key="$1" value="$2" tmp
    case "$key" in
        [!A-Za-z_]* | *[!A-Za-z0-9_]*)
            echo "tts_set_runtime: invalid key '$key'" >&2
            return 2
            ;;
    esac
    tmp="$(mktemp)" || return 1
    # Keep every existing line except a previous setting of this key.
    if [ -f "$TTS_RUNTIME_FILE" ]; then
        grep -vE "^[[:space:]]*${key}=" "$TTS_RUNTIME_FILE" > "$tmp" 2>/dev/null || true
    fi
    printf '%s=%s\n' "$key" "$value" >> "$tmp"
    mv "$tmp" "$TTS_RUNTIME_FILE"
}

# tts_reset_runtime
# Remove the runtime override file so settings fall back to the config files.
tts_reset_runtime() {
    rm -f "$TTS_RUNTIME_FILE"
}

# tts_clean
# Read text on stdin and write a more speakable version to stdout. Two knobs
# control it:
#   TTS_STRIP_CODE=1      remove fenced code blocks (``` or ~~~). Default 1.
#   TTS_STRIP_MARKDOWN=1  remove Markdown markup (headings, links, emphasis).
#                         Default 1.
tts_clean() {
    local strip_code="${TTS_STRIP_CODE:-1}" strip_md="${TTS_STRIP_MARKDOWN:-1}"

    # Step 1: optionally drop fenced code blocks, including the fence lines.
    if [ "$strip_code" = "1" ]; then
        awk '
            /^[[:space:]]*(```|~~~)/ { infence = !infence; next }
            infence { next }
            { print }
        '
    else
        cat
    fi | {
        # Step 2: optionally strip Markdown markup so it is not read aloud.
        if [ "$strip_md" = "1" ]; then
            sed -E '
                s/!\[[^]]*\]\([^)]*\)//g
                s/\[([^]]*)\]\([^)]*\)/\1/g
                s/^[[:space:]]*#{1,6}[[:space:]]*//
                s/^[[:space:]]*>[[:space:]]?//
                s/^[[:space:]]*[-*+][[:space:]]+//
                s/^[[:space:]]*[0-9]+\.[[:space:]]+//
                s/^[[:space:]]*[-*_=]{3,}[[:space:]]*$//
                s/`+//g
                s/\*+//g
            '
        else
            cat
        fi
    } | sed -E 's/[[:space:]]+$//' | cat -s
}

# tts_speak_stream
# Read text on stdin and speak it. Long text is split into chunks on sentence
# boundaries so no single request is too large for the provider. Knobs:
#   TTS_PROVIDER      passed to speak-text as -provider (optional)
#   TTS_VOICE         passed to speak-text as -voice (optional)
#   TTS_CHUNK_CHARS   maximum characters per chunk. Default 1500.
tts_speak_stream() {
    local max="${TTS_CHUNK_CHARS:-1500}"
    local speak_args=()
    [ -n "${TTS_PROVIDER:-}" ] && speak_args+=(-provider "$TTS_PROVIDER")
    [ -n "${TTS_VOICE:-}" ] && speak_args+=(-voice "$TTS_VOICE")

    # First put each sentence on its own line, then pack sentences back together
    # into chunks no longer than $max characters. Chunks are separated by the
    # record-separator byte so the read loop below can split on it reliably.
    sed -E 's/([.!?])[[:space:]]+/\1\n/g' \
        | awk -v max="$max" -v sep="$TTS_SEP" '
            function flush() { if (length(buf) > 0) { printf "%s%s", buf, sep; buf = "" } }
            {
                s = $0
                if (s ~ /^[[:space:]]*$/) next
                cand = (buf == "" ? s : buf " " s)
                if (length(cand) > max && buf != "") { flush(); buf = s }
                else buf = cand
            }
            END { flush() }
        ' \
        | while IFS= read -r -d "$TTS_SEP" chunk; do
            # Skip a chunk that is only whitespace.
            [ -n "${chunk//[[:space:]]/}" ] || continue
            "$SPEAK_BIN" "${speak_args[@]}" "$chunk"
        done
}
