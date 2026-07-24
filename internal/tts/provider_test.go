package tts

import (
	"testing"
)

func TestNewProvider(t *testing.T) {
	openai, err := NewProvider(ProviderOpenAI)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if openai.Name() != ProviderOpenAI {
		t.Errorf("expected name %q, got %q", ProviderOpenAI, openai.Name())
	}

	elevenlabs, err := NewProvider(ProviderElevenLabs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elevenlabs.Name() != ProviderElevenLabs {
		t.Errorf("expected name %q, got %q", ProviderElevenLabs, elevenlabs.Name())
	}

	if _, err := NewProvider("unknown"); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func TestNewProviders(t *testing.T) {
	providers := NewProviders()

	if len(providers) != 2 {
		t.Errorf("expected 2 providers, got %d", len(providers))
	}
	for _, name := range ProviderNames() {
		synth, ok := providers[name]
		if !ok {
			t.Errorf("expected provider %q to be registered", name)
			continue
		}
		if synth.Name() != name {
			t.Errorf("provider registered under %q reports name %q", name, synth.Name())
		}
	}
}

func TestDefaultProviderName(t *testing.T) {
	tests := []struct {
		name          string
		ttsProvider   string
		openAIKey     string
		elevenLabsKey string
		expected      string
	}{
		{"explicit openai", "openai", "", "", ProviderOpenAI},
		{"explicit elevenlabs", "elevenlabs", "sk-x", "", ProviderElevenLabs},
		{"invalid TTS_PROVIDER falls back to keys", "bogus", "", "el-x", ProviderElevenLabs},
		{"openai key only", "", "sk-x", "", ProviderOpenAI},
		{"elevenlabs key only", "", "", "el-x", ProviderElevenLabs},
		{"both keys prefer openai", "", "sk-x", "el-x", ProviderOpenAI},
		{"no keys default openai", "", "", "", ProviderOpenAI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TTS_PROVIDER", tt.ttsProvider)
			t.Setenv("OPENAI_API_KEY", tt.openAIKey)
			t.Setenv("ELEVENLABS_API_KEY", tt.elevenLabsKey)

			if got := DefaultProviderName(); got != tt.expected {
				t.Errorf("DefaultProviderName() = %q, want %q", got, tt.expected)
			}
		})
	}
}
