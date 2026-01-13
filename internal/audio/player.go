package audio

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
)

// Player handles audio playback with mutex protection
type Player struct {
	mu        sync.Mutex
	isPlaying bool
}

// NewPlayer creates a new audio player
func NewPlayer() *Player {
	return &Player{}
}

// Play plays the given audio data
// Only one audio can play at a time (mutex protected)
func (p *Player) Play(audioData []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.isPlaying = true
	defer func() { p.isPlaying = false }()

	// Create temporary file
	tmpFile, err := os.CreateTemp("", "tts-*.mp3")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(audioData); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write audio data: %w", err)
	}
	tmpFile.Close()

	// Play audio based on platform
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("afplay", tmpFile.Name())
	case "linux":
		// Try common Linux audio players
		if _, err := exec.LookPath("mpv"); err == nil {
			cmd = exec.Command("mpv", "--no-video", tmpFile.Name())
		} else if _, err := exec.LookPath("ffplay"); err == nil {
			cmd = exec.Command("ffplay", "-nodisp", "-autoexit", tmpFile.Name())
		} else if _, err := exec.LookPath("aplay"); err == nil {
			// aplay requires WAV, so use mpg123 for MP3
			if _, err := exec.LookPath("mpg123"); err == nil {
				cmd = exec.Command("mpg123", "-q", tmpFile.Name())
			} else {
				return fmt.Errorf("no suitable audio player found on Linux (install mpv, ffplay, or mpg123)")
			}
		} else {
			return fmt.Errorf("no suitable audio player found on Linux (install mpv, ffplay, or mpg123)")
		}
	case "windows":
		// Windows Media Player via PowerShell
		cmd = exec.Command("powershell", "-c",
			fmt.Sprintf(`(New-Object Media.SoundPlayer '%s').PlaySync()`, tmpFile.Name()))
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("audio playback failed: %w", err)
	}

	return nil
}

// IsPlaying returns whether audio is currently playing
func (p *Player) IsPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.isPlaying
}
