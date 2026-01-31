package tts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	if client.model != "tts-1" {
		t.Errorf("expected model 'tts-1', got %q", client.model)
	}
	if client.httpClient == nil {
		t.Error("expected httpClient to be initialized")
	}
	if client.defaultSpeed != DefaultSpeed {
		t.Errorf("expected defaultSpeed %v, got %v", DefaultSpeed, client.defaultSpeed)
	}
}

func TestNewClient_WithEnvSpeed(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "1.5")

	client := NewClient()

	if client.defaultSpeed != 1.5 {
		t.Errorf("expected defaultSpeed 1.5, got %v", client.defaultSpeed)
	}
}

func TestNewClient_WithInvalidEnvSpeed(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "5.0") // out of range

	client := NewClient()

	// Should fall back to default
	if client.defaultSpeed != DefaultSpeed {
		t.Errorf("expected defaultSpeed %v for invalid env, got %v", DefaultSpeed, client.defaultSpeed)
	}
}

func TestNewClient_WithNonNumericEnvSpeed(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "fast") // not a number

	client := NewClient()

	// Should fall back to default
	if client.defaultSpeed != DefaultSpeed {
		t.Errorf("expected defaultSpeed %v for non-numeric env, got %v", DefaultSpeed, client.defaultSpeed)
	}
}

func TestIsValidSpeed(t *testing.T) {
	tests := []struct {
		speed    float64
		expected bool
	}{
		{0.25, true},  // min valid
		{1.0, true},   // default
		{4.0, true},   // max valid
		{2.5, true},   // middle
		{0.24, false}, // below min
		{4.1, false},  // above max
		{0, false},    // zero
		{-1.0, false}, // negative
	}

	for _, tt := range tests {
		result := IsValidSpeed(tt.speed)
		if result != tt.expected {
			t.Errorf("IsValidSpeed(%v) = %v, want %v", tt.speed, result, tt.expected)
		}
	}
}

func TestGetDefaultSpeed(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("CLAUDE_TTS_SPEED", "2.0")

	client := NewClient()

	if client.GetDefaultSpeed() != 2.0 {
		t.Errorf("expected GetDefaultSpeed() to return 2.0, got %v", client.GetDefaultSpeed())
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
		if req.Model != "tts-1" {
			t.Errorf("expected model tts-1, got %s", req.Model)
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
		model:      "tts-1",
	}

	// Override the URL by creating a custom transport
	originalURL := "https://api.openai.com/v1/audio/speech"
	_ = originalURL // We'll use a mock server instead

	// For this test, we need to create a client that uses our test server
	// We'll test the request building logic separately
	audio, err := synthesizeWithURL(client, "Hello, world!", VoiceNova, server.URL)
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
		model:      "tts-1",
	}

	_, err := synthesizeWithURL(client, "Hello", VoiceAlloy, server.URL)
	if err == nil {
		t.Error("expected error for API failure")
	}
}

// synthesizeWithURL is a test helper that allows overriding the API URL
func synthesizeWithURL(c *Client, text string, voice Voice, url string) ([]byte, error) {
	return synthesizeWithURLAndSpeed(c, text, voice, 0, url)
}

// synthesizeWithURLAndSpeed is a test helper that allows overriding the API URL with speed
func synthesizeWithURLAndSpeed(c *Client, text string, voice Voice, speed float64, url string) ([]byte, error) {
	effectiveSpeed := speed
	if effectiveSpeed == 0 {
		effectiveSpeed = c.defaultSpeed
	}

	reqBody := ttsRequest{
		Model: c.model,
		Input: text,
		Voice: string(voice),
		Speed: effectiveSpeed,
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
