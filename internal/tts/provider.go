package tts

import (
	"fmt"
	"sort"
	"strings"
)

// Provider is implemented by every TTS backend.
type Provider interface {
	// Name returns the registry key for this provider, e.g. "openai".
	Name() string
	// Synthesize converts text to speech and returns MP3 audio data.
	Synthesize(text string, voice Voice) ([]byte, error)
	// IsValidVoice reports whether the given voice string is accepted by this provider.
	IsValidVoice(voice string) bool
	// DefaultVoice returns the voice used when no voice is specified by the caller.
	DefaultVoice() Voice
}

// DefaultProviderName is the fallback provider name when the caller omits the provider param.
const DefaultProviderName = "openai"

// Registry maps provider names to Provider implementations.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[string]Provider),
	}
}

// Register adds p to the registry under p.Name().
// Calling Register with a name that already exists overwrites the previous entry.
func (r *Registry) Register(p Provider) {
	r.providers[p.Name()] = p
}

// Get returns the Provider registered under name, or a non-nil error if not found.
// The error message includes the unknown name and lists all registered providers.
func (r *Registry) Get(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf(
			"unknown provider %q; supported providers: %s",
			name,
			strings.Join(r.Names(), ", "),
		)
	}
	return p, nil
}

// Names returns a sorted slice of all registered provider names.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.providers))
	for k := range r.providers {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
