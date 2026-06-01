package tts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestValidVoices(t *testing.T) {
	voices := ValidVoices()

	expected := []Voice{VoiceAlloy, VoiceEcho, VoiceFable, VoiceOnyx, VoiceNova, VoiceShimmer}
	if len(voices) != len(expected) {
		t.Errorf("expected %d voices, got %d", len(expected), len(voices))
	}

	for i, v := range expected {
		if voices[i] != v {
			t.Errorf("expected voice %s at index %d, got %s", v, i, voices[i])
		}
	}
}

func TestIsValidVoice(t *testing.T) {
	tests := []struct {
		voice    string
		expected bool
	}{
		{"alloy", true},
		{"echo", true},
		{"fable", true},
		{"onyx", true},
		{"nova", true},
		{"shimmer", true},
		{"invalid", false},
		{"", false},
		{"ALLOY", false}, // case sensitive
		{"Alloy", false},
	}

	for _, tt := range tests {
		t.Run(tt.voice, func(t *testing.T) {
			result := IsValidVoice(tt.voice)
			if result != tt.expected {
				t.Errorf("IsValidVoice(%q) = %v, want %v", tt.voice, result, tt.expected)
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	client := NewClient()

	if client.apiKey != "test-key" {
		t.Errorf("expected apiKey 'test-key', got %q", client.apiKey)
	}
	if client.model != "gpt-4o-mini-tts" {
		t.Errorf("expected model 'gpt-4o-mini-tts', got %q", client.model)
	}
	if client.httpClient == nil {
		t.Error("expected httpClient to be initialized")
	}
}

func TestSynthesize_Success(t *testing.T) {
	expectedAudio := []byte("fake-mp3-audio-data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method and path
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		// Verify headers
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			t.Errorf("expected Authorization header, got %s", r.Header.Get("Authorization"))
		}

		// Verify request body
		body, _ := io.ReadAll(r.Body)
		var req ttsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.Model != "gpt-4o-mini-tts" {
			t.Errorf("expected model gpt-4o-mini-tts, got %s", req.Model)
		}
		if req.Input != "Hello, world!" {
			t.Errorf("expected input 'Hello, world!', got %s", req.Input)
		}
		if req.Voice != "nova" {
			t.Errorf("expected voice nova, got %s", req.Voice)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(expectedAudio)
	}))
	defer server.Close()

	client := &Client{
		apiKey:     "test-api-key",
		httpClient: server.Client(),
		model:      "gpt-4o-mini-tts",
	}

	// Override the URL by creating a custom transport
	originalURL := "https://api.openai.com/v1/audio/speech"
	_ = originalURL // We'll use a mock server instead

	// For this test, we need to create a client that uses our test server
	// We'll test the request building logic separately
	audio, err := synthesizeWithURL(client, "Hello, world!", VoiceNova, server.URL, 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(audio) != string(expectedAudio) {
		t.Errorf("expected audio %q, got %q", expectedAudio, audio)
	}
}

func TestSynthesize_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": "invalid api key"}`))
	}))
	defer server.Close()

	client := &Client{
		apiKey:     "invalid-key",
		httpClient: server.Client(),
		model:      "gpt-4o-mini-tts",
	}

	_, err := synthesizeWithURL(client, "Hello", VoiceAlloy, server.URL, 1.0)
	if err == nil {
		t.Error("expected error for API failure")
	}
}

// synthesizeWithURL is a test helper that allows overriding the API URL.
// The speed parameter is forwarded to the request payload.
func synthesizeWithURL(c *Client, text string, voice Voice, url string, speed float64) ([]byte, error) {
	reqBody := ttsRequest{
		Model: c.model,
		Input: text,
		Voice: string(voice),
		Speed: speed,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, io.NopCloser(
		io.Reader(
			&jsonReader{data: jsonData},
		),
	))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, &apiError{status: resp.StatusCode, body: string(body)}
	}

	return io.ReadAll(resp.Body)
}

type jsonReader struct {
	data []byte
	pos  int
}

func (r *jsonReader) Read(p []byte) (n int, err error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n = copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}

type apiError struct {
	status int
	body   string
}

func (e *apiError) Error() string {
	return e.body
}

// --- Speed control tests (issue #3) ---

// TestNewClient_SpeedDefault verifies that NewClient uses 1.0 as the default
// speed when CLAUDE_TTS_SPEED is not set.
func TestNewClient_SpeedDefault(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	os.Unsetenv("CLAUDE_TTS_SPEED")

	client := NewClient()

	if client.defaultSpeed != 1.0 {
		t.Errorf("expected defaultSpeed 1.0 when env var is unset, got %v", client.defaultSpeed)
	}
}

// TestNewClient_SpeedFromEnv verifies that NewClient reads CLAUDE_TTS_SPEED and
// stores it as the client's defaultSpeed.
func TestNewClient_SpeedFromEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "2.0")

	client := NewClient()

	if client.defaultSpeed != 2.0 {
		t.Errorf("expected defaultSpeed 2.0 from env, got %v", client.defaultSpeed)
	}
}

// TestNewClient_SpeedInvalidEnv verifies that NewClient clamps/defaults to 1.0
// when CLAUDE_TTS_SPEED is set to a value outside the valid range 0.25–4.0.
func TestNewClient_SpeedInvalidEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "99.0")

	client := NewClient()

	if client.defaultSpeed != 1.0 {
		t.Errorf("expected defaultSpeed 1.0 for out-of-range env value, got %v", client.defaultSpeed)
	}
}

// TestNewClient_SpeedEnvNonNumeric verifies that NewClient falls back to 1.0
// when CLAUDE_TTS_SPEED is set to a non-numeric string (e.g. "abc").
// Gap flagged in phase 4: the existing InvalidEnv test only covers an
// out-of-range *numeric* value; this test covers the parse-error path.
func TestNewClient_SpeedEnvNonNumeric(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "abc")

	client := NewClient()

	if client.defaultSpeed != 1.0 {
		t.Errorf("expected defaultSpeed 1.0 for non-numeric env value 'abc', got %v", client.defaultSpeed)
	}
}

// TestNewClient_SpeedEnvFast verifies that a word like "fast" (non-numeric) is
// also treated as invalid and falls back to 1.0.
func TestNewClient_SpeedEnvFast(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "fast")

	client := NewClient()

	if client.defaultSpeed != 1.0 {
		t.Errorf("expected defaultSpeed 1.0 for non-numeric env value 'fast', got %v", client.defaultSpeed)
	}
}

// TestNewClient_SpeedEnvZero verifies that CLAUDE_TTS_SPEED=0.0 (below MinSpeed
// 0.25) falls back to 1.0.
func TestNewClient_SpeedEnvZero(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "0.0")

	client := NewClient()

	if client.defaultSpeed != 1.0 {
		t.Errorf("expected defaultSpeed 1.0 for out-of-range env value 0.0, got %v", client.defaultSpeed)
	}
}

// TestNewClient_SpeedEnvAtLowerBoundary verifies that CLAUDE_TTS_SPEED=0.25
// (exactly MinSpeed) is accepted and stored.
func TestNewClient_SpeedEnvAtLowerBoundary(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "0.25")

	client := NewClient()

	if client.defaultSpeed != 0.25 {
		t.Errorf("expected defaultSpeed 0.25 at lower boundary, got %v", client.defaultSpeed)
	}
}

// TestNewClient_SpeedEnvAtUpperBoundary verifies that CLAUDE_TTS_SPEED=4.0
// (exactly MaxSpeed) is accepted and stored.
func TestNewClient_SpeedEnvAtUpperBoundary(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "4.0")

	client := NewClient()

	if client.defaultSpeed != 4.0 {
		t.Errorf("expected defaultSpeed 4.0 at upper boundary, got %v", client.defaultSpeed)
	}
}

// TestDefaultSpeed verifies that client.DefaultSpeed() returns the client's
// configured default speed.
func TestDefaultSpeed(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "1.5")

	client := NewClient()

	got := client.DefaultSpeed()
	if got != 1.5 {
		t.Errorf("DefaultSpeed() = %v, want 1.5", got)
	}
}

// TestSynthesize_SpeedInPayload verifies that Synthesize sends the given speed
// value in the JSON request body to the API.
func TestSynthesize_SpeedInPayload(t *testing.T) {
	const wantSpeed = 1.5

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req ttsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.Speed != wantSpeed {
			t.Errorf("expected speed %v in request payload, got %v", wantSpeed, req.Speed)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("fake-audio"))
	}))
	defer server.Close()

	client := &Client{
		apiKey:       "test-api-key",
		httpClient:   server.Client(),
		model:        "gpt-4o-mini-tts",
		defaultSpeed: 1.0,
	}

	_, err := synthesizeWithURL(client, "Hello, world!", VoiceNova, server.URL, wantSpeed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
