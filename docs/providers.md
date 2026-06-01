# TTS Providers

## Overview

The `tts.Provider` interface decouples synthesis logic from the rest of the system.
All provider implementations live in `internal/tts/`. The `tts.Registry` maps string
keys (e.g. `"openai"`) to `Provider` implementations and is initialized in
`server.New()` where the built-in OpenAI backend is registered.

## The Provider Interface

```go
type Provider interface {
    Name() string
    Synthesize(text string, voice Voice) ([]byte, error)
    IsValidVoice(voice string) bool
    DefaultVoice() Voice
}
```

- `Name()` — the registry key, e.g. `"openai"`. Must be unique across all registered providers.
- `Synthesize()` — makes the network or subprocess call and returns raw MP3 bytes.
  Must **not** play audio; that is the audio player's responsibility.
- `IsValidVoice()` — validates a voice name before accepting a job.
- `DefaultVoice()` — the voice used when the caller omits a preference.

## Registered Providers

| Key | Implementation | Notes |
|-----|----------------|-------|
| `openai` | `*tts.Client` (`internal/tts/openai.go`) | Default; requires `OPENAI_API_KEY` |

## Adding a New Provider (Step-by-Step Recipe)

This example shows how to add a fictional `"piper"` local-TTS backend.

### 1. Create the implementation file

Create `internal/tts/piper.go`:

```go
package tts

import (
    "fmt"
    "os/exec"
)

// PiperClient implements Provider using the local Piper binary.
type PiperClient struct{}

func NewPiperClient() *PiperClient { return &PiperClient{} }

func (p *PiperClient) Name() string { return "piper" }

func (p *PiperClient) DefaultVoice() Voice { return VoiceAlloy } // map to nearest equivalent

func (p *PiperClient) IsValidVoice(v string) bool {
    // Piper uses model files, not named voices — accept any non-empty string.
    return v != ""
}

func (p *PiperClient) Synthesize(text string, voice Voice) ([]byte, error) {
    out, err := exec.Command("piper", "--text", text).Output()
    if err != nil {
        return nil, fmt.Errorf("piper: %w", err)
    }
    return out, nil
}
```

### 2. Write unit tests

Create `internal/tts/piper_test.go`. Mock `exec.Command` or use a test binary —
do NOT call the real `piper` binary in unit tests. Run via `safe-test go test ./internal/tts/...`.

### 3. Register in server.New()

In `internal/server/server.go`, add the new provider to the registry built inside `New()`:

```go
func New() (*Server, error) {
    reg := tts.NewRegistry()
    reg.Register(tts.NewClient())      // "openai" — existing
    reg.Register(tts.NewPiperClient()) // "piper"  — new backend
    ...
}
```

The registration point is the `New()` function in `internal/server/server.go`.
For advanced use-cases (e.g. test injection or dynamic registration), pass a
pre-built registry to `NewWithRegistry(registry)` instead.

### 4. Update this file

Add a row to the Registered Providers table above and document any required
environment variables or runtime dependencies.

## Backward Compatibility

When the `provider` parameter is omitted from the `speak` MCP tool call, the system
uses `tts.DefaultProviderName` (`"openai"`). Existing integrations that do not pass
`provider` are unaffected by this change.
