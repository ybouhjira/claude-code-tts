package tts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// openAIVoices are the voice names accepted by the OpenAI TTS API.
var openAIVoices = []string{"alloy", "echo", "fable", "onyx", "nova", "shimmer"}

// OpenAIClient handles OpenAI TTS API requests.
type OpenAIClient struct {
	apiKey     string
	httpClient *http.Client
	model      string
	baseURL    string
}

// NewOpenAIClient creates a new OpenAI TTS client.
func NewOpenAIClient() *OpenAIClient {
	return &OpenAIClient{
		apiKey: os.Getenv("OPENAI_API_KEY"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		model:   "tts-1",
		baseURL: "https://api.openai.com/v1/audio/speech",
	}
}

// Name returns the provider identifier.
func (c *OpenAIClient) Name() string {
	return ProviderOpenAI
}

// DefaultVoice returns the voice used when none is specified.
func (c *OpenAIClient) DefaultVoice() string {
	return "alloy"
}

// Voices returns the voice names this provider accepts.
func (c *OpenAIClient) Voices() []string {
	voices := make([]string, len(openAIVoices))
	copy(voices, openAIVoices)
	return voices
}

// IsValidVoice checks if the given voice is valid for OpenAI.
func (c *OpenAIClient) IsValidVoice(voice string) bool {
	for _, valid := range openAIVoices {
		if valid == voice {
			return true
		}
	}
	return false
}

// ttsRequest represents the API request payload
type ttsRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
	Voice string `json:"voice"`
}

// Synthesize converts text to speech and returns MP3 audio data
func (c *OpenAIClient) Synthesize(text string, voice string) ([]byte, error) {
	reqBody := ttsRequest{
		Model: c.model,
		Input: text,
		Voice: voice,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL, bytes.NewReader(jsonData))
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
