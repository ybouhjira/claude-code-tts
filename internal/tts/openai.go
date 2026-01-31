package tts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
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

// Speed constants
const (
	MinSpeed     = 0.25
	MaxSpeed     = 4.0
	DefaultSpeed = 1.0
)

// IsValidSpeed checks if the given speed is within valid range
func IsValidSpeed(speed float64) bool {
	return speed >= MinSpeed && speed <= MaxSpeed
}

// Client handles OpenAI TTS API requests
type Client struct {
	apiKey       string
	httpClient   *http.Client
	model        string
	defaultSpeed float64
}

// NewClient creates a new TTS client
func NewClient() *Client {
	defaultSpeed := DefaultSpeed
	if envSpeed := os.Getenv("CLAUDE_TTS_SPEED"); envSpeed != "" {
		if parsed, err := strconv.ParseFloat(envSpeed, 64); err == nil && IsValidSpeed(parsed) {
			defaultSpeed = parsed
		}
	}

	return &Client{
		apiKey: os.Getenv("OPENAI_API_KEY"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		model:        "tts-1",
		defaultSpeed: defaultSpeed,
	}
}

// GetDefaultSpeed returns the client's default speed setting
func (c *Client) GetDefaultSpeed() float64 {
	return c.defaultSpeed
}

// ttsRequest represents the API request payload
type ttsRequest struct {
	Model string  `json:"model"`
	Input string  `json:"input"`
	Voice string  `json:"voice"`
	Speed float64 `json:"speed,omitempty"`
}

// Synthesize converts text to speech and returns MP3 audio data
// If speed is 0, the client's default speed is used
func (c *Client) Synthesize(text string, voice Voice, speed float64) ([]byte, error) {
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
