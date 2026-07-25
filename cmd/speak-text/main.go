package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

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
	provider := flag.String("provider", "", "TTS provider: openai, elevenlabs, or kokoro (default: TTS_PROVIDER env var, else based on configured API keys)")
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
		fmt.Fprintf(os.Stderr, "  kokoro:     a Kokoro voice, e.g. af_bella, af_heart, am_michael\n")
		fmt.Fprintf(os.Stderr, "              (needs a local Kokoro server; no API key; default af_bella)\n")
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

	// Validate environment for the chosen provider. Kokoro runs locally and
	// needs no API key, so it is exempt from this check.
	if providerName != tts.ProviderKokoro {
		keyVar := apiKeyEnvVar(providerName)
		if os.Getenv(keyVar) == "" {
			fmt.Fprintf(os.Stderr, "Error: %s environment variable is required for provider %s\n", keyVar, providerName)
			os.Exit(1)
		}
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

	player := audio.NewPlayer()

	// Stop playback promptly when asked to. `tts-ctl stop` sends this process a
	// termination signal; killing the player process here makes the read-out
	// end right away instead of playing to the end of the current clip.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		player.Stop()
		os.Exit(130)
	}()

	// Prefer streaming: begin playback as audio arrives so the first sound is
	// heard almost immediately. Providers that cannot stream fall back to the
	// buffered path, which synthesizes the whole clip before playing.
	if streamer, ok := client.(tts.StreamSynthesizer); ok {
		stream, err := streamer.SynthesizeStream(text, voiceName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error synthesizing speech: %v\n", err)
			os.Exit(1)
		}
		defer stream.Close()
		if err := player.PlayStream(stream); err != nil {
			fmt.Fprintf(os.Stderr, "Error playing audio: %v\n", err)
			os.Exit(1)
		}
		return
	}

	audioData, err := client.Synthesize(text, voiceName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error synthesizing speech: %v\n", err)
		os.Exit(1)
	}
	if err := player.Play(audioData); err != nil {
		fmt.Fprintf(os.Stderr, "Error playing audio: %v\n", err)
		os.Exit(1)
	}
}
