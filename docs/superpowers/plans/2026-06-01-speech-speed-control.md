# Speech Speed Control Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add speech speed control (0.25–4.0) to the TTS MCP server via a global `CLAUDE_TTS_SPEED` env var and a per-call `speed` parameter on the `speak` tool.

**Architecture:** Propagate speed through the `Provider` interface (`Synthesize` gains a `speed float64` param), through the `Job` struct and worker pool, into the OpenAI API payload. The `speak` tool validates speed, falls back to `provider.DefaultSpeed()` when omitted.

**Tech Stack:** Go 1.23, mcp-go library, OpenAI TTS API

---

## Files to Modify (in order)

1. `internal/tts/openai.go` — add speed constants, `defaultSpeed` field to `Client`, update `NewClient` to read env var, add `DefaultSpeed()` method, update `ttsRequest` struct and `Synthesize` signature
2. `internal/tts/provider.go` — update `Provider` interface: `Synthesize` gains `speed float64`, add `DefaultSpeed() float64` method
3. `internal/server/worker.go` — add `Speed float64` to `Job` struct, update `processJob` to pass `job.Speed` to `provider.Synthesize`, update `SubmitWithProvider` to accept speed
4. `internal/server/server.go` — add `speed` float parameter to speak tool definition, extract and validate in `handleSpeak`, pass to submit

### Files with tests to update (collateral changes)

- `internal/tts/openai_test.go` — update `synthesizeWithURL` helper + existing tests for new `Synthesize(text, voice, speed)` signature; add new speed-specific tests
- `internal/server/worker_registry_test.go` — update `recordingProvider.Synthesize` to accept speed param
- `internal/server/server_provider_test.go` — update `fakeServerProvider.Synthesize` to accept speed param; add speed parameter tests

---

## Tasks

### Task 1: Speed constants and Client field (~10 min)

**Goal:** Add speed constants, store `defaultSpeed` on `Client`, read `CLAUDE_TTS_SPEED` env var in `NewClient`.

**Acceptance criteria covered:** AC2 (env var), AC4 (API client infrastructure)

- [ ] In `internal/tts/openai.go`, add constants after the voice constants block:

```go
const (
	MinSpeed          = 0.25
	MaxSpeed          = 4.0
	DefaultSpeedValue = 1.0
)
```

- [ ] Add `defaultSpeed float64` field to the `Client` struct:

```go
type Client struct {
	apiKey       string
	httpClient   *http.Client
	model        string
	defaultSpeed float64
}
```

- [ ] Update `NewClient()` to read and validate `CLAUDE_TTS_SPEED`. Add `"strconv"` to imports:

```go
func NewClient() *Client {
	speed := DefaultSpeedValue
	if raw := os.Getenv("CLAUDE_TTS_SPEED"); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed >= MinSpeed && parsed <= MaxSpeed {
			speed = parsed
		} else {
			logging.Warn("CLAUDE_TTS_SPEED=%q is invalid (must be %.2f–%.2f); using default %.1f", raw, MinSpeed, MaxSpeed, DefaultSpeedValue)
		}
	}
	return &Client{
		apiKey: os.Getenv("OPENAI_API_KEY"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		model:        "gpt-4o-mini-tts",
		defaultSpeed: speed,
	}
}
```

- [ ] Add `DefaultSpeed()` method on `*Client`:

```go
func (c *Client) DefaultSpeed() float64 {
	return c.defaultSpeed
}
```

- [ ] Write tests in `internal/tts/openai_test.go`:

```go
func TestNewClient_SpeedDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "")
	client := NewClient()
	if client.defaultSpeed != DefaultSpeedValue {
		t.Errorf("expected defaultSpeed %.2f, got %.2f", DefaultSpeedValue, client.defaultSpeed)
	}
}

func TestNewClient_SpeedFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "1.5")
	client := NewClient()
	if client.defaultSpeed != 1.5 {
		t.Errorf("expected defaultSpeed 1.5, got %.2f", client.defaultSpeed)
	}
}

func TestNewClient_SpeedInvalidEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "not-a-number")
	client := NewClient()
	if client.defaultSpeed != DefaultSpeedValue {
		t.Errorf("invalid env should fall back to %.2f, got %.2f", DefaultSpeedValue, client.defaultSpeed)
	}
}

func TestNewClient_SpeedEnvOutOfRange(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "5.0") // > MaxSpeed
	client := NewClient()
	if client.defaultSpeed != DefaultSpeedValue {
		t.Errorf("out-of-range env should fall back to %.2f, got %.2f", DefaultSpeedValue, client.defaultSpeed)
	}
}

func TestDefaultSpeed(t *testing.T) {
	t.Setenv("CLAUDE_TTS_SPEED", "2.0")
	client := NewClient()
	if client.DefaultSpeed() != 2.0 {
		t.Errorf("DefaultSpeed() = %.2f, want 2.0", client.DefaultSpeed())
	}
}
```

- [ ] Run `safe-test go test ./internal/tts/...` — all existing tests plus new ones must pass.

---

### Task 2: Update Provider interface and Synthesize signature (~10 min)

**Goal:** Add `speed float64` to `Synthesize` and `DefaultSpeed()` to the `Provider` interface; update `Client.Synthesize` and `ttsRequest` accordingly.

**Acceptance criteria covered:** AC4 (API payload), AC1 (interface contract)

- [ ] In `internal/tts/provider.go`, update the `Provider` interface:

```go
type Provider interface {
	Name() string
	Synthesize(text string, voice Voice, speed float64) ([]byte, error)
	IsValidVoice(voice string) bool
	DefaultVoice() Voice
	DefaultSpeed() float64
}
```

- [ ] In `internal/tts/openai.go`, add `Speed float64` to `ttsRequest`:

```go
type ttsRequest struct {
	Model string  `json:"model"`
	Input string  `json:"input"`
	Voice string  `json:"voice"`
	Speed float64 `json:"speed"`
}
```

- [ ] Update `Client.Synthesize` to accept speed and pass it in the payload:

```go
func (c *Client) Synthesize(text string, voice Voice, speed float64) ([]byte, error) {
	reqBody := ttsRequest{
		Model: c.model,
		Input: text,
		Voice: string(voice),
		Speed: speed,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/audio/speech", bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
	}

	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return audioData, nil
}
```

- [ ] Update `synthesizeWithURL` test helper in `internal/tts/openai_test.go` to accept and forward speed:

```go
func synthesizeWithURL(c *Client, text string, voice Voice, speed float64, url string) ([]byte, error) {
	reqBody := ttsRequest{
		Model: c.model,
		Input: text,
		Voice: string(voice),
		Speed: speed,
	}
	// ... rest unchanged
}
```

- [ ] Update all existing callers of `synthesizeWithURL` in `openai_test.go` to pass a speed value (use `DefaultSpeedValue`):

```go
// In TestSynthesize_Success:
audio, err := synthesizeWithURL(client, "Hello, world!", VoiceNova, DefaultSpeedValue, server.URL)

// In TestSynthesize_APIError:
_, err := synthesizeWithURL(client, "Hello", VoiceAlloy, DefaultSpeedValue, server.URL)
```

- [ ] Add a test asserting that `speed` appears in the JSON payload sent to the API:

```go
func TestSynthesize_SpeedInPayload(t *testing.T) {
	var capturedSpeed float64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ttsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		capturedSpeed = req.Speed
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-audio"))
	}))
	defer srv.Close()

	client := &Client{
		apiKey:     "test-key",
		httpClient: srv.Client(),
		model:      "tts-1",
	}

	_, err := synthesizeWithURL(client, "hello", VoiceAlloy, 1.5, srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if capturedSpeed != 1.5 {
		t.Errorf("expected speed 1.5 in payload, got %.2f", capturedSpeed)
	}
}
```

- [ ] Run `safe-test go test ./internal/tts/...` — must compile and pass.

---

### Task 3: Propagate speed through worker pool (~10 min)

**Goal:** Add `Speed` to `Job`, thread it through `SubmitWithProvider`, and pass it to `provider.Synthesize` in `processJob`.

**Acceptance criteria covered:** AC3 (per-call override propagation)

- [ ] In `internal/server/worker.go`, add `Speed float64` to the `Job` struct:

```go
type Job struct {
	ID           string    `json:"id"`
	Text         string    `json:"text"`
	Voice        tts.Voice `json:"voice"`
	Speed        float64   `json:"speed"`
	ProviderName string    `json:"provider,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	Status       string    `json:"status"`
	Error        string    `json:"error,omitempty"`
	mu           sync.RWMutex
}
```

- [ ] Update `SubmitWithProvider` to accept a `speed float64` parameter and store it on the job:

```go
func (wp *WorkerPool) SubmitWithProvider(text string, voice tts.Voice, providerName string, speed float64) (*Job, error) {
	job := &Job{
		ID:           fmt.Sprintf("job-%d", time.Now().UnixNano()),
		Text:         text,
		Voice:        voice,
		Speed:        speed,
		ProviderName: providerName,
		CreatedAt:    time.Now(),
		Status:       "pending",
	}
	return wp.enqueue(job)
}
```

- [ ] Update `processJob` to pass `job.Speed` to `provider.Synthesize` (registry path) and `wp.ttsClient.Synthesize` (legacy path):

```go
if wp.registry != nil {
	provider, pErr := wp.registry.Get(job.ProviderName)
	if pErr != nil {
		// ... error handling unchanged
	}
	logging.Debug("Job %s: calling %.64s TTS API...", job.ID, job.ProviderName)
	audioData, err = provider.Synthesize(job.Text, job.Voice, job.Speed)
} else {
	logging.Debug("Job %s: calling OpenAI TTS API...", job.ID)
	audioData, err = wp.ttsClient.Synthesize(job.Text, job.Voice, job.Speed)
}
```

- [ ] Update `GetStatus` deep-copy in `worker.go` to copy the `Speed` field:

```go
jobCopy := &Job{
	ID:           job.ID,
	Text:         job.Text,
	Voice:        job.Voice,
	Speed:        job.Speed,
	ProviderName: job.ProviderName,
	CreatedAt:    job.CreatedAt,
	Status:       job.Status,
	Error:        job.Error,
}
```

- [ ] Fix `recordingProvider.Synthesize` in `internal/server/worker_registry_test.go` to match the new interface signature:

```go
func (p *recordingProvider) Synthesize(text string, voice tts.Voice, speed float64) ([]byte, error) {
	p.mu.Lock()
	p.callCount++
	p.mu.Unlock()
	if p.synthErr != nil {
		return nil, p.synthErr
	}
	return []byte("stub-audio-bytes"), nil
}

func (p *recordingProvider) DefaultSpeed() float64 { return tts.DefaultSpeedValue }
```

- [ ] Fix all `SubmitWithProvider` call sites in `worker_registry_test.go` to pass a speed argument (use `tts.DefaultSpeedValue`):

```go
// Every wp.SubmitWithProvider(...) call in worker_registry_test.go gets a speed param:
job, err := wp.SubmitWithProvider("hello world", tts.VoiceAlloy, "fakeprovider", tts.DefaultSpeedValue)
```

- [ ] Run `safe-test go test ./internal/server/...` — must compile and pass.

---

### Task 4: Update server speak handler (~10 min)

**Goal:** Register `speed` as an optional number parameter on the `speak` tool, validate the range, fall back to `provider.DefaultSpeed()`, and pass it to `SubmitWithProvider`.

**Acceptance criteria covered:** AC1 (parameter + validation), AC2 (env default fallback), AC3 (per-call override)

- [ ] In `internal/server/server.go`, add a `mcp.WithNumber` parameter for `speed` to the speak tool definition. The `mcp-go` library uses `mcp.WithNumber` for float parameters:

```go
speakTool := mcp.NewTool("speak",
	mcp.WithDescription("Convert text to speech and play it aloud. Use this to provide audio feedback to the user."),
	mcp.WithString("text",
		mcp.Required(),
		mcp.Description("The text to convert to speech (max 4096 characters)"),
	),
	mcp.WithString("voice",
		mcp.Description("Voice to use: alloy, echo, fable, onyx, nova, shimmer (default: alloy)"),
	),
	mcp.WithString("provider",
		mcp.Description("TTS provider to use (default: openai)"),
	),
	mcp.WithNumber("speed",
		mcp.Description("Speech speed multiplier (0.25–4.0, default: 1.0). Overrides CLAUDE_TTS_SPEED env var."),
	),
)
```

- [ ] In `handleSpeak`, after the voice validation block, add speed extraction, validation, and fallback:

```go
// Extract speed parameter; fall back to provider default when omitted.
speed := provider.DefaultSpeed()
if rawSpeed, ok := request.Params.Arguments["speed"]; ok && rawSpeed != nil {
	switch v := rawSpeed.(type) {
	case float64:
		if v < tts.MinSpeed || v > tts.MaxSpeed {
			logging.Warn("speak: speed %.2f out of range (%.2f–%.2f)", v, tts.MinSpeed, tts.MaxSpeed)
			return mcp.NewToolResultError(fmt.Sprintf(
				"speed %.2f is out of range; must be between %.2f and %.2f", v, tts.MinSpeed, tts.MaxSpeed,
			)), nil
		}
		speed = v
	}
}
```

- [ ] Update the `SubmitWithProvider` call in `handleSpeak` to pass `speed`:

```go
job, err := s.workerPool.SubmitWithProvider(text, tts.Voice(voice), providerName, speed)
```

- [ ] Update the logging line to include speed:

```go
logging.Info("speak: queueing job (provider=%s, voice=%s, speed=%.2f, text_len=%d, preview='%.50s...')",
	providerName, voice, speed, len(text), text)
```

- [ ] Fix `fakeServerProvider.Synthesize` in `internal/server/server_provider_test.go` to match the new interface:

```go
func (f *fakeServerProvider) Synthesize(_ string, _ tts.Voice, _ float64) ([]byte, error) {
	return []byte("stub-audio"), nil
}

func (f *fakeServerProvider) DefaultSpeed() float64 { return tts.DefaultSpeedValue }
```

- [ ] Add speed-specific tests in `internal/server/server_provider_test.go`:

```go
func TestHandleSpeak_SpeedParameter_Valid(t *testing.T) {
	registry := buildRegistryWithFakeOpenAI()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":  "Speed test",
		"speed": float64(1.5),
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if result.IsError {
		content := result.Content[0].(mcp.TextContent)
		t.Errorf("expected success for speed 1.5, got error: %s", content.Text)
	}
}

func TestHandleSpeak_SpeedParameter_TooLow(t *testing.T) {
	registry := buildRegistryWithFakeOpenAI()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":  "Speed test",
		"speed": float64(0.1), // below MinSpeed
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for speed below minimum (0.1 < 0.25)")
	}
}

func TestHandleSpeak_SpeedParameter_TooHigh(t *testing.T) {
	registry := buildRegistryWithFakeOpenAI()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":  "Speed test",
		"speed": float64(5.0), // above MaxSpeed
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for speed above maximum (5.0 > 4.0)")
	}
}

func TestHandleSpeak_SpeedDefault_UsesProviderDefault(t *testing.T) {
	// fakeServerProvider returns DefaultSpeedValue (1.0) from DefaultSpeed().
	// We verify the call succeeds when speed is omitted — the fallback path.
	registry := buildRegistryWithFakeOpenAI()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text": "No speed specified",
		// no "speed" key
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if result.IsError {
		content := result.Content[0].(mcp.TextContent)
		t.Errorf("expected success when speed omitted, got error: %s", content.Text)
	}
}

func TestHandleSpeak_SpeedBoundaryValues(t *testing.T) {
	registry := buildRegistryWithFakeOpenAI()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	for _, speed := range []float64{0.25, 1.0, 4.0} {
		t.Run(fmt.Sprintf("speed=%.2f", speed), func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]interface{}{
				"text":  "Boundary test",
				"speed": speed,
			}
			result, err := srv.handleSpeak(context.Background(), request)
			if err != nil {
				t.Fatalf("handleSpeak error: %v", err)
			}
			if result.IsError {
				content := result.Content[0].(mcp.TextContent)
				t.Errorf("speed %.2f is valid but got error: %s", speed, content.Text)
			}
		})
	}
}
```

- [ ] Run `safe-test go test ./internal/server/...` — must compile and pass.

---

### Task 5: Fix all callsites and ensure compilation (~5 min)

**Goal:** Confirm no remaining callers use the old `Synthesize` or `SubmitWithProvider` signatures.

- [ ] `cmd/speak-text/main.go` calls `client.Synthesize(text, tts.Voice(*voice))` on line 57. Update to pass speed and optionally add a `-speed` flag:

```go
// Add flag after the voice flag:
speedFlag := flag.Float64("speed", 0, "Speech speed (0.25–4.0, default: uses CLAUDE_TTS_SPEED or 1.0)")

// After flag.Parse(), resolve speed:
speed := client.DefaultSpeed()
if *speedFlag != 0 {
	if *speedFlag < tts.MinSpeed || *speedFlag > tts.MaxSpeed {
		fmt.Fprintf(os.Stderr, "Error: speed %.2f out of range (%.2f–%.2f)\n", *speedFlag, tts.MinSpeed, tts.MaxSpeed)
		os.Exit(1)
	}
	speed = *speedFlag
}

// Change the Synthesize call to:
audioData, err := client.Synthesize(text, tts.Voice(*voice), speed)
```

- [ ] Check `cmd/tts-server/main.go` for any direct `Synthesize` calls (likely none — it delegates to the server).

- [ ] Run `safe-test go test ./...` — the full suite must compile and pass with no errors.

- [ ] Run `make build` to confirm both binaries compile cleanly.

---

### Task 6: Final commit (~2 min)

**Goal:** Commit all changes on the issue branch with a clear message.

- [ ] Stage changed files:

```
internal/tts/openai.go
internal/tts/openai_test.go
internal/tts/provider.go
internal/server/worker.go
internal/server/worker_registry_test.go
internal/server/server.go
internal/server/server_provider_test.go
cmd/speak-text/main.go  (if modified)
```

- [ ] Commit with message:

```
feat: add speech speed control (0.25-4.0)

- CLAUDE_TTS_SPEED env var sets global default speed (default 1.0)
- Per-call speed parameter on speak tool overrides global default
- Speed validated to 0.25–4.0 range; out-of-range returns tool error
- Speed propagated through Provider interface, Job struct, and OpenAI API payload
- Comprehensive tests: env loading, validation, boundary values, API payload
```

---

## Effort Estimate

| Task | Estimated Time |
|------|----------------|
| Task 1: Speed constants and Client field | ~10 min |
| Task 2: Provider interface + Synthesize signature | ~10 min |
| Task 3: Propagate through worker pool | ~10 min |
| Task 4: Server speak handler | ~10 min |
| Task 5: Fix callsites + verify compilation | ~5 min |
| Task 6: Final commit | ~2 min |
| **Total** | **~47 min** |

---

## Acceptance Criteria Coverage

| AC | Task(s) |
|----|---------|
| AC1: `speed` param on speak tool, 0.25–4.0, validates range, returns error | Task 4 |
| AC2: `CLAUDE_TTS_SPEED` env var sets global default, default 1.0 | Task 1 |
| AC3: Per-call speed overrides global default | Task 4 (extraction + fallback logic) |
| AC4: OpenAI API client passes `speed` in payload | Task 2 |
| AC5: Comprehensive tests | Tasks 1, 2, 3, 4 |

---

## Key Design Decisions

1. **Interface-first change**: `Provider.Synthesize` gains `speed float64` as the 3rd positional argument — simple, no option struct needed at this scope.
2. **`DefaultSpeed()` on Provider**: Allows each backend to declare its own default; the server handler calls it as the fallback when the caller omits `speed`, keeping the fallback logic co-located with the provider.
3. **Env var validation in `NewClient`**: Invalid or out-of-range values warn via logging and silently fall back to `1.0` rather than hard-failing startup — matches the existing pattern for `OPENAI_API_KEY` (which also doesn't fail construction).
4. **`SubmitWithProvider` signature extended**: Adds `speed float64` as a 4th parameter rather than introducing a new `SubmitWithProviderAndSpeed` variant — keeps the single call path clean and avoids accumulating overloaded methods.
5. **Speed type is `float64` throughout**: No wrapper type is introduced since speed is a scalar measurement with no domain-specific behavior beyond range validation. The `MinSpeed`/`MaxSpeed`/`DefaultSpeedValue` constants serve as the validation contract.

---

## Risks and Mitigations

1. **`mcp.WithNumber` availability**: The `mcp-go` library may use a different function name for number parameters (e.g., `mcp.WithFloat`). Risk: low — the existing code uses `mcp.WithString`, so check the library's API surface before registering the parameter. Mitigation: inspect `go doc github.com/mark3labs/mcp-go/mcp` or scan existing usage to confirm the exact function name.

2. **`synthesizeWithURL` helper signature change**: The test helper in `openai_test.go` has its own `ttsRequest` construction inline — updating its signature breaks callers within the same file. Mitigation: update all internal callers in Task 2 at the same time as the helper signature, keeping the change atomic.

3. **Legacy `Submit` path in `worker.go`**: The `Submit` method (non-provider variant used by `worker_test.go`) calls `wp.ttsClient.Synthesize` via `processJob`. Since `wp.ttsClient` is a `*tts.Client` (not a `Provider`), it needs its `Synthesize` updated in Task 2 and `processJob` updated in Task 3 to pass a speed. The legacy path should use `DefaultSpeedValue` or `wp.ttsClient.DefaultSpeed()`. Mitigation: Task 3 explicitly handles both the registry and legacy branches of `processJob`.
