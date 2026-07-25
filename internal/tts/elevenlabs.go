package tts

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// fallbackVoiceName and fallbackVoiceID identify "Aria", one of ElevenLabs'
// current default voices. Default voices are usable by free-tier accounts via
// the API, unlike Voice Library voices. We use Aria only as a last resort when
// we cannot discover the account's own voices (for example, no API key is set).
//
// The older premade voices (Rachel, Adam, and so on) were deliberately NOT
// hardcoded here: ElevenLabs reclassified them as Voice Library voices, which
// free-tier API keys are not allowed to synthesize with. Relying on them was
// the cause of a 402 "paid_plan_required" error for free users.
const (
	fallbackVoiceName = "aria"
	fallbackVoiceID   = "9BWtsMINqrJLrRacOk9x"
)

// ElevenLabsClient handles ElevenLabs TTS API requests.
type ElevenLabsClient struct {
	apiKey     string
	httpClient *http.Client
	modelID    string
	baseURL    string // text-to-speech endpoint, voice ID is appended
	voicesURL  string // list-voices endpoint used to discover account voices

	// Voice discovery is done once, lazily, and cached for the process
	// lifetime. It queries the account's own voices so that names resolve to
	// IDs the account is actually allowed to use, and so the default voice is
	// one that works on the caller's subscription tier.
	mu          sync.Mutex
	loaded      bool
	voiceByName map[string]string // lowercased name -> voice ID
	voiceOrder  []string          // voice names in the order the API returned them
}

// NewElevenLabsClient creates a new ElevenLabs TTS client.
func NewElevenLabsClient() *ElevenLabsClient {
	return &ElevenLabsClient{
		apiKey: os.Getenv("ELEVENLABS_API_KEY"),
		httpClient: &http.Client{
			// Synthesis of long text is slower than OpenAI's, so allow more time.
			Timeout: 60 * time.Second,
		},
		modelID:   "eleven_multilingual_v2",
		baseURL:   "https://api.elevenlabs.io/v1/text-to-speech",
		voicesURL: "https://api.elevenlabs.io/v1/voices",
	}
}

// Name returns the provider identifier.
func (c *ElevenLabsClient) Name() string {
	return ProviderElevenLabs
}

// ensureVoicesLocked discovers the account's voices once and caches them.
// The caller MUST hold c.mu. It never returns an error: if discovery is not
// possible (no API key, network failure, non-200 response), the cache stays
// empty and callers fall back to the built-in default voice.
func (c *ElevenLabsClient) ensureVoicesLocked() {
	if c.loaded {
		return
	}
	// Attempt discovery at most once per process, whatever the outcome.
	c.loaded = true

	// Without a key (for example, in unit tests) we cannot and must not call
	// the network. Callers fall back to the built-in default voice.
	if c.apiKey == "" || c.voicesURL == "" {
		return
	}

	req, err := http.NewRequest("GET", c.voicesURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("xi-api-key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	var payload struct {
		Voices []struct {
			VoiceID string `json:"voice_id"`
			Name    string `json:"name"`
		} `json:"voices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return
	}

	c.voiceByName = make(map[string]string, len(payload.Voices))
	for _, v := range payload.Voices {
		if v.Name == "" || v.VoiceID == "" {
			continue
		}
		c.voiceByName[strings.ToLower(v.Name)] = v.VoiceID
		c.voiceOrder = append(c.voiceOrder, v.Name)
	}
}

// DefaultVoice returns the voice used when none is specified. When the account's
// voices are known, it is the first one the account has (guaranteed usable on
// that tier). Otherwise it falls back to the built-in default voice.
func (c *ElevenLabsClient) DefaultVoice() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureVoicesLocked()
	if len(c.voiceOrder) > 0 {
		return c.voiceOrder[0]
	}
	return fallbackVoiceName
}

// Voices returns the human-friendly voice names this provider accepts. When the
// account's voices are known, it lists those; otherwise it lists the built-in
// default voice. Raw voice IDs are also accepted by Synthesize but not listed.
func (c *ElevenLabsClient) Voices() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureVoicesLocked()
	if len(c.voiceOrder) > 0 {
		out := make([]string, len(c.voiceOrder))
		copy(out, c.voiceOrder)
		return out
	}
	return []string{fallbackVoiceName}
}

// IsValidVoice reports whether the given voice can be used. A raw voice ID is
// always accepted. When the account's voices are known, a name is valid only if
// it matches one of them (or the built-in default). When discovery was not
// possible, any non-empty name is accepted and the API becomes the final judge.
func (c *ElevenLabsClient) IsValidVoice(voice string) bool {
	if voice == "" {
		return false
	}
	if looksLikeVoiceID(voice) {
		return true
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureVoicesLocked()

	if c.voiceByName != nil {
		if _, ok := c.voiceByName[strings.ToLower(voice)]; ok {
			return true
		}
		return strings.ToLower(voice) == fallbackVoiceName
	}
	// Discovery unavailable: be lenient and let the API decide.
	return true
}

// looksLikeVoiceID reports whether the string has the shape of an
// ElevenLabs voice ID: exactly 20 alphanumeric characters.
func looksLikeVoiceID(v string) bool {
	if len(v) != 20 {
		return false
	}
	for _, r := range v {
		isLower := r >= 'a' && r <= 'z'
		isUpper := r >= 'A' && r <= 'Z'
		isDigit := r >= '0' && r <= '9'
		if !isLower && !isUpper && !isDigit {
			return false
		}
	}
	return true
}

// resolveVoice converts a friendly name to its voice ID. A raw voice ID passes
// through unchanged. An empty voice becomes the built-in default. A name that
// cannot be resolved is passed through so the API can report a clear error.
func (c *ElevenLabsClient) resolveVoice(voice string) string {
	if voice == "" {
		voice = fallbackVoiceName
	}
	if looksLikeVoiceID(voice) {
		return voice
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensureVoicesLocked()

	if id, ok := c.voiceByName[strings.ToLower(voice)]; ok {
		return id
	}
	if strings.ToLower(voice) == fallbackVoiceName {
		return fallbackVoiceID
	}
	return voice
}

// elevenLabsRequest represents the API request payload
type elevenLabsRequest struct {
	Text    string `json:"text"`
	ModelID string `json:"model_id"`
}

// Synthesize converts text to speech and returns MP3 audio data
func (c *ElevenLabsClient) Synthesize(text string, voice string) ([]byte, error) {
	reqBody := elevenLabsRequest{
		Text:    text,
		ModelID: c.modelID,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Unlike OpenAI, ElevenLabs puts the voice in the URL path, not the body.
	url := c.baseURL + "/" + c.resolveVoice(voice)
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("xi-api-key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "audio/mpeg")

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
