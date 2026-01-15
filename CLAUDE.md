# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Text-to-Speech MCP server plugin for Claude Code written in Go. It converts text to speech using OpenAI's TTS API and plays audio via platform-native players.

## Commands

```bash
# Build
make build              # Creates bin/tts-server

# Run locally (requires OPENAI_API_KEY)
make run

# Test
make test               # Run all tests
make test-coverage      # Run tests with HTML coverage report
go test -v ./internal/server/...  # Run specific package tests
go test -v -run TestSubmit ./internal/server/...  # Run single test by name

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
│    Entry point - validates OPENAI_API_KEY, creates server   │
│                                                             │
│  internal/server/                                           │
│    server.go: MCP server setup, tool registration           │
│      - speak(text, voice) → queues TTS job                  │
│      - tts_status() → returns pool stats as JSON            │
│                                                             │
│    worker.go: Worker pool (2 workers, 50-slot queue)        │
│      - Concurrent job processing with goroutines            │
│      - Job history tracking (last 100 jobs)                 │
│      - Atomic counters for processed/failed stats           │
│                                                             │
│  internal/tts/                                              │
│    openai.go: OpenAI TTS API client                         │
│      - POST /v1/audio/speech with tts-1 model               │
│      - Returns MP3 audio bytes                              │
│                                                             │
│  internal/audio/                                            │
│    player.go: Cross-platform audio playback                 │
│      - Mutex-protected (one audio at a time)                │
│      - macOS: afplay, Linux: mpv/ffplay/mpg123              │
│      - Windows: PowerShell Media.SoundPlayer                │
└─────────────────────────────────────────────────────────────┘
```

## Key Design Decisions

- **Worker Pool Pattern**: Jobs are non-blocking; `speak()` returns immediately after queuing
- **Mutex-Protected Playback**: `audio.Player` ensures no overlapping audio
- **Job Queue**: Channel-based with 50 slots; returns error when full
- **MCP Protocol**: Uses `mcp-go` library for stdio-based communication with Claude Code

## Environment

- **Required**: `OPENAI_API_KEY` environment variable
- **Go Version**: 1.23 (per go.mod)

## MCP Tools

| Tool | Parameters | Description |
|------|------------|-------------|
| `speak` | `text` (required), `voice` (optional: alloy, echo, fable, onyx, nova, shimmer) | Queue TTS job |
| `tts_status` | none | Get queue/worker stats |
