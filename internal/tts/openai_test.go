package tts

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAIClient_Voices(t *testing.T) {
	client := NewOpenAIClient()
	voices := client.Voices()

	expected := []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}
	if len(voices) != len(expected) {
		t.Errorf("expected %d voices, got %d", len(expected), len(voices))
	}

	for i, v := range expected {
		if voices[i] != v {
			t.Errorf("expected voice %s at index %d, got %s", v, i, voices[i])
		}
	}
}

func TestOpenAIClient_IsValidVoice(t *testing.T) {
	client := NewOpenAIClient()

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
		{"rachel", false}, // ElevenLabs voice, not valid for OpenAI
	}

	for _, tt := range tests {
		t.Run(tt.voice, func(t *testing.T) {
			result := client.IsValidVoice(tt.voice)
			if result != tt.expected {
				t.Errorf("IsValidVoice(%q) = %v, want %v", tt.voice, result, tt.expected)
			}
		})
	}
}

func TestNewOpenAIClient(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")

	client := NewOpenAIClient()

	if client.apiKey != "test-key" {
		t.Errorf("expected apiKey 'test-key', got %q", client.apiKey)
	}
	if client.model != "tts-1" {
		t.Errorf("expected model 'tts-1', got %q", client.model)
	}
	if client.httpClient == nil {
		t.Error("expected httpClient to be initialized")
	}
	if client.Name() != ProviderOpenAI {
		t.Errorf("expected name %q, got %q", ProviderOpenAI, client.Name())
	}
	if client.DefaultVoice() != "alloy" {
		t.Errorf("expected default voice 'alloy', got %q", client.DefaultVoice())
	}
}

func TestOpenAIClient_Synthesize_Success(t *testing.T) {
	expectedAudio := []byte("fake-mp3-audio-data")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request method
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

	client := &OpenAIClient{
		apiKey:     "test-api-key",
		httpClient: server.Client(),
		model:      "tts-1",
		baseURL:    server.URL,
	}

	audio, err := client.Synthesize("Hello, world!", "nova")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(audio) != string(expectedAudio) {
		t.Errorf("expected audio %q, got %q", expectedAudio, audio)
	}
}

func TestOpenAIClient_Synthesize_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": "invalid api key"}`))
	}))
	defer server.Close()

	client := &OpenAIClient{
		apiKey:     "invalid-key",
		httpClient: server.Client(),
		model:      "tts-1",
		baseURL:    server.URL,
	}

	_, err := client.Synthesize("Hello", "alloy")
	if err == nil {
		t.Error("expected error for API failure")
	}
}
