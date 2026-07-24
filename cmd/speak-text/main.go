package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ybouhjira/claude-code-tts/internal/audio"
	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

// apiKeyEnvVar returns the environment variable holding the API key
// for the given provider.
func apiKeyEnvVar(provider string) string {
	if provider == tts.ProviderElevenLabs {
		return "ELEVENLABS_API_KEY"
	}
	return "OPENAI_API_KEY"
}

func main() {
	// Parse flags
	provider := flag.String("provider", "", "TTS provider: openai or elevenlabs (default: TTS_PROVIDER env var, else based on configured API keys)")
	voice := flag.String("voice", "", "Voice to use (default: the provider's default voice)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS] TEXT\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Converts text to speech and plays it.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nVoices:\n")
		fmt.Fprintf(os.Stderr, "  openai:     alloy, echo, fable, onyx, nova, shimmer\n")
		fmt.Fprintf(os.Stderr, "  elevenlabs: a voice name from your account or a raw voice ID\n")
		fmt.Fprintf(os.Stderr, "              (default: your account's first voice, or Aria)\n")
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s \"Build completed\"\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -voice onyx \"Error occurred\"\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -provider elevenlabs \"Error occurred\"\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -provider elevenlabs -voice Aria \"Error occurred\"\n", os.Args[0])
	}
	flag.Parse()

	// Check for text argument
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(1)
	}

	text := flag.Arg(0)

	// Resolve provider
	providerName := *provider
	if providerName == "" {
		providerName = tts.DefaultProviderName()
	}

	client, err := tts.NewProvider(providerName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Validate environment for the chosen provider
	keyVar := apiKeyEnvVar(providerName)
	if os.Getenv(keyVar) == "" {
		fmt.Fprintf(os.Stderr, "Error: %s environment variable is required for provider %s\n", keyVar, providerName)
		os.Exit(1)
	}

	// Resolve and validate voice
	voiceName := *voice
	if voiceName == "" {
		voiceName = client.DefaultVoice()
	}
	if !client.IsValidVoice(voiceName) {
		fmt.Fprintf(os.Stderr, "Error: invalid voice '%s' for provider %s. Valid voices: %s\n",
			voiceName, providerName, strings.Join(client.Voices(), ", "))
		os.Exit(1)
	}

	// Synthesize speech
	audioData, err := client.Synthesize(text, voiceName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error synthesizing speech: %v\n", err)
		os.Exit(1)
	}

	// Play audio
	player := audio.NewPlayer()
	if err := player.Play(audioData); err != nil {
		fmt.Fprintf(os.Stderr, "Error playing audio: %v\n", err)
		os.Exit(1)
	}
}
