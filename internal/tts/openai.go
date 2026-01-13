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

// Voice represents available OpenAI TTS voices
type Voice string

const (
	VoiceAlloy   Voice = "alloy"
	VoiceEcho    Voice = "echo"
	VoiceFable   Voice = "fable"
	VoiceOnyx    Voice = "onyx"
	VoiceNova    Voice = "nova"
	VoiceShimmer Voice = "shimmer"
)

// ValidVoices returns all valid voice options
func ValidVoices() []Voice {
	return []Voice{VoiceAlloy, VoiceEcho, VoiceFable, VoiceOnyx, VoiceNova, VoiceShimmer}
}

// IsValidVoice checks if the given voice is valid
func IsValidVoice(v string) bool {
	for _, valid := range ValidVoices() {
		if string(valid) == v {
			return true
		}
	}
	return false
}

// Client handles OpenAI TTS API requests
type Client struct {
	apiKey     string
	httpClient *http.Client
	model      string
}

// NewClient creates a new TTS client
func NewClient() *Client {
	return &Client{
		apiKey: os.Getenv("OPENAI_API_KEY"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		model: "tts-1",
	}
}

// ttsRequest represents the API request payload
type ttsRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
	Voice string `json:"voice"`
}

// Synthesize converts text to speech and returns MP3 audio data
func (c *Client) Synthesize(text string, voice Voice) ([]byte, error) {
	reqBody := ttsRequest{
		Model: c.model,
		Input: text,
		Voice: string(voice),
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
