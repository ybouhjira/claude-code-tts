package tts

import (
	"fmt"
	"io"
	"os"
)

// Provider names accepted by NewProvider and the speak tool.
const (
	ProviderOpenAI     = "openai"
	ProviderElevenLabs = "elevenlabs"
	ProviderKokoro     = "kokoro"
)

// Synthesizer converts text into playable MP3 audio bytes.
// Each TTS provider (OpenAI, ElevenLabs, Kokoro) implements this interface,
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

// StreamSynthesizer is an optional interface for providers that can deliver
// audio as a stream. When a provider implements it, callers can begin playback
// as the first bytes arrive instead of waiting for the whole file, which cuts
// the delay before the first sound is heard. The returned reader yields MP3
// bytes and must be closed by the caller. Providers that do not implement this
// interface are still played through the buffered Synthesize path.
type StreamSynthesizer interface {
	Synthesizer
	// SynthesizeStream starts synthesis and returns the audio as a stream.
	SynthesizeStream(text string, voice string) (io.ReadCloser, error)
}

// ProviderNames returns all supported provider identifiers.
func ProviderNames() []string {
	return []string{ProviderOpenAI, ProviderElevenLabs, ProviderKokoro}
}

// NewProvider creates the synthesizer for the given provider name.
func NewProvider(name string) (Synthesizer, error) {
	switch name {
	case ProviderOpenAI:
		return NewOpenAIClient(), nil
	case ProviderElevenLabs:
		return NewElevenLabsClient(), nil
	case ProviderKokoro:
		return NewKokoroClient(), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (valid providers: openai, elevenlabs, kokoro)", name)
	}
}

// NewProviders creates one synthesizer per supported provider,
// keyed by provider name.
func NewProviders() map[string]Synthesizer {
	return map[string]Synthesizer{
		ProviderOpenAI:     NewOpenAIClient(),
		ProviderElevenLabs: NewElevenLabsClient(),
		ProviderKokoro:     NewKokoroClient(),
	}
}

// DefaultProviderName picks the provider to use when a request does not
// name one. The TTS_PROVIDER environment variable wins if set to a valid
// provider. Otherwise the choice follows which API keys are configured,
// preferring OpenAI to match the plugin's original behavior.
//
// Kokoro has no API key to detect, so it is never auto-selected here. It is
// opt-in only: set TTS_PROVIDER=kokoro (or pass provider=kokoro per request).
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
