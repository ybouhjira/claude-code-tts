package tts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewKokoroClient_DefaultBaseURL(t *testing.T) {
	t.Setenv("KOKORO_BASE_URL", "")

	client := NewKokoroClient()

	if client.Name() != ProviderKokoro {
		t.Errorf("expected name %q, got %q", ProviderKokoro, client.Name())
	}
	if client.model != "kokoro" {
		t.Errorf("expected model 'kokoro', got %q", client.model)
	}
	if client.DefaultVoice() != defaultKokoroVoice {
		t.Errorf("expected default voice %q, got %q", defaultKokoroVoice, client.DefaultVoice())
	}
	want := defaultKokoroBaseURL + kokoroSpeechPath
	if client.baseURL != want {
		t.Errorf("expected baseURL %q, got %q", want, client.baseURL)
	}
}

func TestNewKokoroClient_CustomBaseURL(t *testing.T) {
	t.Setenv("KOKORO_BASE_URL", "http://box:9000")

	client := NewKokoroClient()

	if client.baseURL != "http://box:9000"+kokoroSpeechPath {
		t.Errorf("unexpected baseURL %q", client.baseURL)
	}
}

func TestKokoroEndpoint(t *testing.T) {
	tests := []struct {
		root string
		want string
	}{
		{"http://localhost:8880", "http://localhost:8880/v1/audio/speech"},
		{"http://localhost:8880/", "http://localhost:8880/v1/audio/speech"},
		{"  http://localhost:8880  ", "http://localhost:8880/v1/audio/speech"},
		{"http://localhost:8880/v1/audio/speech", "http://localhost:8880/v1/audio/speech"},
	}

	for _, tt := range tests {
		t.Run(tt.root, func(t *testing.T) {
			if got := kokoroEndpoint(tt.root); got != tt.want {
				t.Errorf("kokoroEndpoint(%q) = %q, want %q", tt.root, got, tt.want)
			}
		})
	}
}

func TestKokoroClient_IsValidVoice(t *testing.T) {
	client := NewKokoroClient()

	tests := []struct {
		voice    string
		expected bool
	}{
		{"af_bella", true},
		{"am_michael", true},
		{"af_bella(2)+af_sky(1)", true}, // weighted blends pass through
		{"anything_new", true},          // unknown names are left to the server
		{"", false},
		{"   ", false},
	}

	for _, tt := range tests {
		t.Run(tt.voice, func(t *testing.T) {
			if got := client.IsValidVoice(tt.voice); got != tt.expected {
				t.Errorf("IsValidVoice(%q) = %v, want %v", tt.voice, got, tt.expected)
			}
		})
	}
}

func TestKokoroClient_Synthesize_Success(t *testing.T) {
	expectedAudio := []byte("fake-mp3-audio-data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		// Kokoro is keyless: no Authorization header should be sent.
		if auth := r.Header.Get("Authorization"); auth != "" {
			t.Errorf("expected no Authorization header, got %q", auth)
		}

		body, _ := io.ReadAll(r.Body)
		var req kokoroRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to unmarshal request: %v", err)
		}
		if req.Model != "kokoro" {
			t.Errorf("expected model kokoro, got %s", req.Model)
		}
		if req.Input != "Hello, world!" {
			t.Errorf("expected input 'Hello, world!', got %s", req.Input)
		}
		if req.Voice != "af_bella" {
			t.Errorf("expected voice af_bella, got %s", req.Voice)
		}
		if req.ResponseFormat != "mp3" {
			t.Errorf("expected response_format mp3, got %s", req.ResponseFormat)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(expectedAudio)
	}))
	defer server.Close()

	client := &KokoroClient{
		httpClient: server.Client(),
		model:      "kokoro",
		baseURL:    server.URL,
	}

	audio, err := client.Synthesize("Hello, world!", "af_bella")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(audio) != string(expectedAudio) {
		t.Errorf("expected audio %q, got %q", expectedAudio, audio)
	}
}

func TestKokoroClient_Synthesize_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail": "unknown voice"}`))
	}))
	defer server.Close()

	client := &KokoroClient{
		httpClient: server.Client(),
		model:      "kokoro",
		baseURL:    server.URL,
	}

	if _, err := client.Synthesize("Hello", "nope"); err == nil {
		t.Error("expected error for API failure")
	}
}

func TestKokoroClient_SynthesizeStream_Success(t *testing.T) {
	expectedAudio := []byte("streamed-mp3-bytes")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("expected no Authorization header, got %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(expectedAudio)
	}))
	defer server.Close()

	client := &KokoroClient{
		httpClient: server.Client(),
		model:      "kokoro",
		baseURL:    server.URL,
	}

	stream, err := client.SynthesizeStream("Hello, world!", "af_bella")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer stream.Close()

	got, err := io.ReadAll(stream)
	if err != nil {
		t.Fatalf("failed to read stream: %v", err)
	}
	if string(got) != string(expectedAudio) {
		t.Errorf("expected audio %q, got %q", expectedAudio, got)
	}
}

func TestKokoroClient_SynthesizeStream_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"detail": "unknown voice"}`))
	}))
	defer server.Close()

	client := &KokoroClient{
		httpClient: server.Client(),
		model:      "kokoro",
		baseURL:    server.URL,
	}

	stream, err := client.SynthesizeStream("Hello", "nope")
	if err == nil {
		if stream != nil {
			stream.Close()
		}
		t.Error("expected error for API failure")
	}
}
