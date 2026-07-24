package tts

import (
	"fmt"
	"os"
)

// Provider names accepted by NewProvider and the speak tool.
const (
	ProviderOpenAI     = "openai"
	ProviderElevenLabs = "elevenlabs"
)

// Synthesizer converts text into playable MP3 audio bytes.
// Each TTS provider (OpenAI, ElevenLabs) implements this interface,
// including its own voice validation, because voice names are
// provider-specific.
type Synthesizer interface {
	// Name returns the provider identifier, e.g. "openai".
	Name() string
	// Synthesize converts text to MP3 audio using the given voice.
	Synthesize(text string, voice string) ([]byte, error)
	// IsValidVoice reports whether this provider accepts the voice.
	IsValidVoice(voice string) bool
	// DefaultVoice returns the voice used when none is specified.
	DefaultVoice() string
	// Voices returns the human-friendly voice names this provider accepts.
	Voices() []string
}

// ProviderNames returns all supported provider identifiers.
func ProviderNames() []string {
	return []string{ProviderOpenAI, ProviderElevenLabs}
}

// NewProvider creates the synthesizer for the given provider name.
func NewProvider(name string) (Synthesizer, error) {
	switch name {
	case ProviderOpenAI:
		return NewOpenAIClient(), nil
	case ProviderElevenLabs:
		return NewElevenLabsClient(), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (valid providers: openai, elevenlabs)", name)
	}
}

// NewProviders creates one synthesizer per supported provider,
// keyed by provider name.
func NewProviders() map[string]Synthesizer {
	return map[string]Synthesizer{
		ProviderOpenAI:     NewOpenAIClient(),
		ProviderElevenLabs: NewElevenLabsClient(),
	}
}

// DefaultProviderName picks the provider to use when a request does not
// name one. The TTS_PROVIDER environment variable wins if set to a valid
// provider. Otherwise the choice follows which API keys are configured,
// preferring OpenAI to match the plugin's original behavior.
func DefaultProviderName() string {
	if p := os.Getenv("TTS_PROVIDER"); p != "" {
		if _, err := NewProvider(p); err == nil {
			return p
		}
	}
	if os.Getenv("OPENAI_API_KEY") != "" {
		return ProviderOpenAI
	}
	if os.Getenv("ELEVENLABS_API_KEY") != "" {
		return ProviderElevenLabs
	}
	return ProviderOpenAI
}
