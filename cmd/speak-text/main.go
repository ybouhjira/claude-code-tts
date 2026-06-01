package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/ybouhjira/claude-code-tts/internal/audio"
	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

func main() {
	// Parse flags
	voice := flag.String("voice", "nova", "Voice to use (alloy, echo, fable, onyx, nova, shimmer)")
	speedFlag := flag.Float64("speed", -1, "Speech speed (0.25–4.0, default: uses CLAUDE_TTS_SPEED or 1.0)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [OPTIONS] TEXT\n\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "Converts text to speech using OpenAI TTS API and plays it.\n\n")
		fmt.Fprintf(os.Stderr, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  %s \"Build completed\"\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "  %s -voice onyx \"Error occurred\"\n", os.Args[0])
	}
	flag.Parse()

	// Check for text argument
	if flag.NArg() == 0 {
		flag.Usage()
		os.Exit(1)
	}

	text := flag.Arg(0)

	// Validate environment
	if os.Getenv("OPENAI_API_KEY") == "" {
		fmt.Fprintf(os.Stderr, "Error: OPENAI_API_KEY environment variable is required\n")
		os.Exit(1)
	}

	// Validate voice
	if !tts.IsValidVoice(*voice) {
		fmt.Fprintf(os.Stderr, "Error: invalid voice '%s'. Valid voices: ", *voice)
		for i, v := range tts.ValidVoices() {
			if i > 0 {
				fmt.Fprintf(os.Stderr, ", ")
			}
			fmt.Fprintf(os.Stderr, "%s", v)
		}
		fmt.Fprintf(os.Stderr, "\n")
		os.Exit(1)
	}

	// Create TTS client
	client := tts.NewClient()

	// Resolve speed: flag overrides env-based default from client.
	speed := client.DefaultSpeed()
	if *speedFlag >= 0 {
		if *speedFlag < tts.MinSpeed || *speedFlag > tts.MaxSpeed {
			fmt.Fprintf(os.Stderr, "Error: speed %.2f out of range (%.2f–%.2f)\n", *speedFlag, tts.MinSpeed, tts.MaxSpeed)
			os.Exit(1)
		}
		speed = *speedFlag
	}

	// Synthesize speech
	audioData, err := client.Synthesize(text, tts.Voice(*voice), speed)
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
