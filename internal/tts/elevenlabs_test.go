package tts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewElevenLabsClient(t *testing.T) {
	t.Setenv("ELEVENLABS_API_KEY", "test-key")

	client := NewElevenLabsClient()

	if client.apiKey != "test-key" {
		t.Errorf("expected apiKey 'test-key', got %q", client.apiKey)
	}
	if client.modelID != "eleven_multilingual_v2" {
		t.Errorf("expected model 'eleven_multilingual_v2', got %q", client.modelID)
	}
	if client.httpClient == nil {
		t.Error("expected httpClient to be initialized")
	}
	if client.Name() != ProviderElevenLabs {
		t.Errorf("expected name %q, got %q", ProviderElevenLabs, client.Name())
	}
}

// newTestVoicesServer returns an httptest server that answers the list-voices
// endpoint with the given name -> voice_id pairs, in the given order.
func newTestVoicesServer(t *testing.T, names []string, ids map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("xi-api-key") == "" {
			t.Errorf("expected xi-api-key header on voices request")
		}
		type voice struct {
			VoiceID string `json:"voice_id"`
			Name    string `json:"name"`
		}
		out := struct {
			Voices []voice `json:"voices"`
		}{}
		for _, n := range names {
			out.Voices = append(out.Voices, voice{VoiceID: ids[n], Name: n})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	}))
}

func TestElevenLabsClient_NoKey_UsesFallback(t *testing.T) {
	// No API key: discovery is skipped entirely (no network), and the client
	// falls back to the built-in default voice.
	client := NewElevenLabsClient()
	client.apiKey = ""

	if got := client.DefaultVoice(); got != fallbackVoiceName {
		t.Errorf("expected default voice %q, got %q", fallbackVoiceName, got)
	}
	voices := client.Voices()
	if len(voices) != 1 || voices[0] != fallbackVoiceName {
		t.Errorf("expected voices [%q], got %v", fallbackVoiceName, voices)
	}
	// Without discovery, any non-empty name is accepted (API is the judge).
	if !client.IsValidVoice("anything") {
		t.Error("expected lenient acceptance when discovery is unavailable")
	}
	if client.IsValidVoice("") {
		t.Error("expected empty voice to be invalid")
	}
	// The fallback name resolves to the fallback ID.
	if id := client.resolveVoice(fallbackVoiceName); id != fallbackVoiceID {
		t.Errorf("expected %q to resolve to %q, got %q", fallbackVoiceName, fallbackVoiceID, id)
	}
	// An empty voice also resolves to the fallback ID.
	if id := client.resolveVoice(""); id != fallbackVoiceID {
		t.Errorf("expected empty voice to resolve to %q, got %q", fallbackVoiceID, id)
	}
}

func TestElevenLabsClient_Discovery(t *testing.T) {
	names := []string{"Aria", "Roger", "MyCustomVoice"}
	ids := map[string]string{
		"Aria":          "9BWtsMINqrJLrRacOk9x",
		"Roger":         "CwhRBWXzGAHq8TQ4Fs17",
		"MyCustomVoice": "aBcDeFgHiJkLmNoPqRsT",
	}
	srv := newTestVoicesServer(t, names, ids)
	defer srv.Close()

	client := NewElevenLabsClient()
	client.apiKey = "test-key"
	client.voicesURL = srv.URL

	// Default is the first voice the account has.
	if got := client.DefaultVoice(); got != "Aria" {
		t.Errorf("expected default voice 'Aria', got %q", got)
	}

	// Voices lists exactly the account's voices, in order.
	voices := client.Voices()
	if len(voices) != 3 || voices[0] != "Aria" || voices[2] != "MyCustomVoice" {
		t.Errorf("expected account voices in order, got %v", voices)
	}

	// A known account voice name is valid; an unknown one is not.
	if !client.IsValidVoice("roger") { // case insensitive
		t.Error("expected 'roger' to be valid")
	}
	if client.IsValidVoice("rachel") {
		t.Error("expected 'rachel' (not in account) to be invalid once discovery succeeds")
	}

	// Names resolve to the account's IDs; raw IDs pass through.
	if id := client.resolveVoice("MyCustomVoice"); id != ids["MyCustomVoice"] {
		t.Errorf("expected MyCustomVoice to resolve to its ID, got %q", id)
	}
	rawID := "ZZZZZZZZZZZZZZZZZZZZ"
	if id := client.resolveVoice(rawID); id != rawID {
		t.Errorf("expected raw ID to pass through, got %q", id)
	}
}

func TestElevenLabsClient_DiscoveryFetchedOnce(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"voices":[{"voice_id":"9BWtsMINqrJLrRacOk9x","name":"Aria"}]}`))
	}))
	defer srv.Close()

	client := NewElevenLabsClient()
	client.apiKey = "test-key"
	client.voicesURL = srv.URL

	// Several calls that would each need the voice list.
	_ = client.DefaultVoice()
	_ = client.Voices()
	_ = client.IsValidVoice("aria")
	_ = client.resolveVoice("aria")

	if calls != 1 {
		t.Errorf("expected voices endpoint to be called once, got %d calls", calls)
	}
}

func TestLooksLikeVoiceID(t *testing.T) {
	tests := []struct {
		voice    string
		expected bool
	}{
		{"21m00Tcm4TlvDq8ikWAM", true}, // 20 alphanumeric chars
		{"9BWtsMINqrJLrRacOk9x", true}, // Aria's ID
		{"", false},
		{"tooshort123", false},          // 11 chars
		{"21m00Tcm4TlvDq8ikWA!", false}, // 20 chars but not alphanumeric
		{"rachel", false},               // a name, not an ID
	}

	for _, tt := range tests {
		t.Run(tt.voice, func(t *testing.T) {
			if got := looksLikeVoiceID(tt.voice); got != tt.expected {
				t.Errorf("looksLikeVoiceID(%q) = %v, want %v", tt.voice, got, tt.expected)
			}
		})
	}
}

func TestElevenLabsClient_Synthesize_Success(t *testing.T) {
	expectedAudio := []byte("fake-mp3-audio-data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		// A raw voice ID goes straight into the URL path unchanged.
		if !strings.HasSuffix(r.URL.Path, "/9BWtsMINqrJLrRacOk9x") {
			t.Errorf("expected path to end with the voice ID, got %s", r.URL.Path)
		}

		// Verify headers
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("xi-api-key") != "test-api-key" {
			t.Errorf("expected xi-api-key header, got %s", r.Header.Get("xi-api-key"))
		}

		// Verify request body
		body, _ := io.ReadAll(r.Body)
		var req elevenLabsRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.Text != "Hello, world!" {
			t.Errorf("expected text 'Hello, world!', got %s", req.Text)
		}
		if req.ModelID != "eleven_multilingual_v2" {
			t.Errorf("expected model eleven_multilingual_v2, got %s", req.ModelID)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(expectedAudio)
	}))
	defer server.Close()

	client := &ElevenLabsClient{
		apiKey:     "test-api-key",
		httpClient: server.Client(),
		modelID:    "eleven_multilingual_v2",
		baseURL:    server.URL,
	}

	// Pass Aria's raw ID so no discovery is needed for this synthesis test.
	audio, err := client.Synthesize("Hello, world!", "9BWtsMINqrJLrRacOk9x")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(audio) != string(expectedAudio) {
		t.Errorf("expected audio %q, got %q", expectedAudio, audio)
	}
}

func TestElevenLabsClient_Synthesize_DefaultVoiceUsesFallbackID(t *testing.T) {
	// With no discovery available, an empty voice must resolve to the fallback
	// voice ID in the request path.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/"+fallbackVoiceID) {
			t.Errorf("expected path to end with fallback voice ID %q, got %s", fallbackVoiceID, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("audio"))
	}))
	defer server.Close()

	client := &ElevenLabsClient{
		apiKey:     "test-api-key",
		httpClient: server.Client(),
		modelID:    "eleven_multilingual_v2",
		baseURL:    server.URL,
		// voicesURL left empty: discovery is skipped, fallback is used.
	}

	if _, err := client.Synthesize("Hello", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestElevenLabsClient_Synthesize_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail": "invalid api key"}`))
	}))
	defer server.Close()

	client := &ElevenLabsClient{
		apiKey:     "invalid-key",
		httpClient: server.Client(),
		modelID:    "eleven_multilingual_v2",
		baseURL:    server.URL,
	}

	_, err := client.Synthesize("Hello", "9BWtsMINqrJLrRacOk9x")
	if err == nil {
		t.Error("expected error for API failure")
	}
}
