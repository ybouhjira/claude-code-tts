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

	"github.com/ybouhjira/claude-code-tts/internal/logging"
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

const (
	MinSpeed          = 0.25
	MaxSpeed          = 4.0
	DefaultSpeedValue = 1.0
)

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
	apiKey       string
	httpClient   *http.Client
	model        string
	defaultSpeed float64
}

// NewClient creates a new TTS client
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

// DefaultSpeed returns the default speech speed for this client.
func (c *Client) DefaultSpeed() float64 {
	return c.defaultSpeed
}

// ttsRequest represents the API request payload
type ttsRequest struct {
	Model string  `json:"model"`
	Input string  `json:"input"`
	Voice string  `json:"voice"`
	Speed float64 `json:"speed"`
}

// Synthesize converts text to speech and returns MP3 audio data
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

// Name returns the registry key for the OpenAI provider.
func (c *Client) Name() string {
	return DefaultProviderName
}

// DefaultVoice returns the default voice for the OpenAI provider.
func (c *Client) DefaultVoice() Voice {
	return VoiceAlloy
}

// IsValidVoice reports whether the given voice string is valid for the OpenAI provider.
// Delegates to the package-level IsValidVoice function.
func (c *Client) IsValidVoice(voice string) bool {
	return IsValidVoice(voice)
}
