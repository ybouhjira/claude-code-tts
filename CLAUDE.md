# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Text-to-Speech MCP server plugin for Claude Code written in Go. It converts text to speech using OpenAI's TTS API, ElevenLabs, or Kokoro (a free, open-weight model run through a local server) and plays audio via platform-native players.

## Commands

```bash
# Build
make build              # Creates bin/tts-server

# Run locally (requires OPENAI_API_KEY)
make run

# Test
make test               # Run all tests
go test -v ./internal/server/...  # Run specific package tests

# Lint
make lint               # Runs golangci-lint (auto-installs if missing)

# Format
make fmt

# Install to Claude Code plugins
make install            # Installs to ~/.claude/plugins/claude-code-tts/
```

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  cmd/tts-server/main.go                                     │
│    Entry point - requires at least one usable provider:     │
│    an API key (OPENAI_API_KEY and/or ELEVENLABS_API_KEY),   │
│    or keyless Kokoro (TTS_PROVIDER=kokoro / KOKORO_BASE_URL) │
│                                                             │
│  internal/server/                                           │
│    server.go: MCP server setup, tool registration           │
│      - speak(text, provider, voice) → queues TTS job        │
│      - tts_status() → returns pool stats as JSON            │
│                                                             │
│    worker.go: Worker pool (2 workers, 50-slot queue)        │
│      - Holds a provider registry (name → tts.Synthesizer)   │
│      - Each Job records its provider and voice              │
│      - Concurrent job processing with goroutines            │
│      - Job history tracking (last 100 jobs)                 │
│      - Atomic counters for processed/failed stats           │
│                                                             │
│  internal/tts/                                              │
│    provider.go: Synthesizer interface + provider helpers    │
│      - Each provider validates its own voices               │
│      - DefaultProviderName(): TTS_PROVIDER env var, else    │
│        picked from configured API keys (OpenAI preferred).  │
│        Kokoro is never auto-picked (no key); opt-in only    │
│    openai.go: OpenAI TTS API client                         │
│      - POST /v1/audio/speech with tts-1 model               │
│      - Voices: alloy, echo, fable, onyx, nova, shimmer      │
│    elevenlabs.go: ElevenLabs TTS API client                 │
│      - POST /v1/text-to-speech/{voice_id}                   │
│      - Discovers account voices via GET /v1/voices (lazy,   │
│        cached); resolves names to IDs, raw IDs pass through │
│      - Free-tier safe: no hardcoded (deprecated) premade    │
│        voice IDs; falls back to Aria if discovery fails     │
│    kokoro.go: Kokoro TTS client (local, keyless)            │
│      - POST {KOKORO_BASE_URL}/v1/audio/speech, no auth      │
│      - OpenAI-compatible body; response_format=mp3          │
│      - Voice validation is pass-through (server decides)    │
│      - All clients return MP3 audio bytes                   │
│                                                             │
│  internal/audio/                                            │
│    player.go: Cross-platform audio playback                 │
│      - Mutex-protected (one audio at a time)                │
│      - macOS: afplay, Linux: mpv/ffplay/mpg123              │
│      - Windows: PowerShell Media.SoundPlayer                │
│                                                             │
│  cmd/tts-reader/main.go + internal/reader/                  │
│    Read-along view (launched by `tts-ctl read` / /tts-read) │
│      - transcript.go: parses the session's JSONL transcript │
│        into visible chat turns (skips tool calls, thinking, │
│        meta lines, sidechains, command/IDE noise, task      │
│        notifications, interrupt markers, compact            │
│        summaries); also enumerates ~/.claude/projects for   │
│        the navigator and extracts per-session titles (last  │
│        "ai-title" line, from bounded head/tail reads) and   │
│        the real project path (first "cwd" field — the dir   │
│        names under ~/.claude/projects are munged lossily).  │
│        Message.Parts records where tool runs interrupted an │
│        assistant turn: every part but the last is a         │
│        "working note", which the page styles as quiet       │
│        italic bullets ("Notes as bullets" toggle, default   │
│        on); headings render at real h1-h6 sizes, and inline │
│        markdown (code chips, bold, italic, links) renders   │
│        styled while the spoken text stays clean             │
│      - document.go: serves any Markdown/plain-text file     │
│        through the same page (`tts-ctl read notes.md`): the │
│        file becomes one "doc"-role message, id "doc-<hash>",│
│        titled by its first heading; highlighting, follow    │
│        scroll and live reload on save all work unchanged    │
│      - server.go: loopback-only HTTP server with embedded   │
│        web page (assets/), /api/tts synthesis endpoint,     │
│        SSE live updates as the transcript grows, and        │
│        single-instance reuse via /api/health + /api/load    │
│      - MULTI-SESSION: one server, many sessions. Each page  │
│        binds to a session via its /?s=<session-id> URL;     │
│        /api/load registers sessions (never repoints open    │
│        pages), SSE change events carry the session id, and  │
│        pages title themselves "project — session title".    │
│        A URL with no session id serves the navigator        │
│        (every project + session, via /api/sessions)         │
│      - The BROWSER plays the audio here (not player.go) so  │
│        the page can highlight each sentence/word in sync;   │
│        sentence timing is exact (one clip per sentence),    │
│        word timing is estimated proportionally to length    │
│      - Inside VS Code the launcher does NOT open the system │
│        browser: it prints a clickable URL that opens as a   │
│        Simple Browser editor tab via the user's             │
│        workbench.externalUriOpeners rule (see README).      │
│        --browser forces the system browser anywhere.        │
│        The bridge extension (vscode-extension/) instead     │
│        opens its own webview tab wrapping the page, because │
│        only an owned tab can carry a real title; the flag   │
│        file lists the project dir so only the VS Code       │
│        window with that project open consumes it (the       │
│        extension polls in EVERY window — unscoped, the      │
│        first poller wins and the tab lands in the wrong     │
│        window)                                              │
│                                                             │
│  scripts/tts-ctl `selection` (X11 only)                     │
│    Speaks the text currently highlighted in ANY window,     │
│    including the Claude Code chat panel, by reading the X11 │
│    primary selection via python3/tkinter. The chat panel is │
│    a closed webview (no extension API, no context-menu      │
│    injection), so highlight+hotkey is the only way to read  │
│    from it directly; README documents the VS Code           │
│    keybinding/task setup                                    │
└─────────────────────────────────────────────────────────────┘
```

## Key Design Decisions

- **Provider Interface**: `tts.Synthesizer` abstracts TTS providers; voice validation lives in each provider because voice names are provider-specific
- **Worker Pool Pattern**: Jobs are non-blocking; `speak()` returns immediately after queuing
- **Mutex-Protected Playback**: `audio.Player` ensures no overlapping audio
- **Job Queue**: Channel-based with 50 slots; returns error when full
- **MCP Protocol**: Uses `mcp-go` library for stdio-based communication with Claude Code

## Environment

- **Required**: at least one usable provider — an `OPENAI_API_KEY` or `ELEVENLABS_API_KEY`, or keyless Kokoro (set `TTS_PROVIDER=kokoro`, or `KOKORO_BASE_URL`)
- **Optional**: `TTS_PROVIDER` (`openai`, `elevenlabs`, or `kokoro`) to pick the default provider
- **Optional**: `KOKORO_BASE_URL` — root address of a local Kokoro-FastAPI server (default `http://localhost:8880`)
- **Optional**: `TTS_READER_PORT` — port for the read-along view's local server (default `8898`, loopback only)
- **Go Version**: 1.21+ (go.mod specifies 1.23)

## MCP Tools

| Tool | Parameters | Description |
|------|------------|-------------|
| `speak` | `text` (required), `provider` (optional: openai, elevenlabs, kokoro), `voice` (optional; OpenAI: alloy, echo, fable, onyx, nova, shimmer; ElevenLabs: a voice name from your account or a raw voice ID, default Aria; Kokoro: a voice name such as af_bella, default af_bella) | Queue TTS job |
| `tts_status` | none | Get queue/worker stats |
