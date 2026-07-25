package audio

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// playbackSpeed reads the TTS_SPEED environment variable and returns the
// playback rate to use. A rate of 1.0 means "play at normal speed". The second
// return value is false when no adjustment should be made — that is, when the
// variable is empty, unparseable, not positive, or exactly 1.0. The rate is
// clamped to the range the pitch-preserving filters accept: 0.5x to 2.0x.
func playbackSpeed() (rate float64, adjusted bool) {
	raw := strings.TrimSpace(os.Getenv("TTS_SPEED"))
	if raw == "" {
		return 1.0, false
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		return 1.0, false
	}
	if v < 0.5 {
		v = 0.5
	}
	if v > 2.0 {
		v = 2.0
	}
	if v == 1.0 {
		return 1.0, false
	}
	return v, true
}

// Player handles audio playback with mutex protection
type Player struct {
	mu        sync.Mutex
	isPlaying bool

	// procMu guards cmd, the audio player process currently running. It is a
	// separate lock from mu so Stop() can kill the process from another
	// goroutine without waiting for playback (which holds mu) to finish.
	procMu sync.Mutex
	cmd    *exec.Cmd
}

// NewPlayer creates a new audio player
func NewPlayer() *Player {
	return &Player{}
}

// Play plays the given audio data.
// Only one audio can play at a time (mutex protected).
func (p *Player) Play(audioData []byte) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.isPlaying = true
	defer func() { p.isPlaying = false }()

	return p.playFileLocked(audioData)
}

// PlayStream plays audio read from r, beginning before synthesis has finished
// when the platform's player can read from standard input (on Linux: mpv,
// ffplay, or mpg123). This is what makes the first sound arrive quickly. On
// platforms whose player needs a real file (macOS afplay, Windows), it buffers
// the stream to a temp file first, so behavior there matches Play.
// Only one audio can play at a time (mutex protected).
func (p *Player) PlayStream(r io.Reader) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.isPlaying = true
	defer func() { p.isPlaying = false }()

	speed, speedAdjusted := playbackSpeed()
	speedStr := strconv.FormatFloat(speed, 'f', -1, 64)

	cmd, canStream, err := streamCmd(speedStr, speedAdjusted)
	if err != nil {
		return err
	}
	if !canStream {
		// This platform's player needs a file: buffer the whole stream, then
		// play it the same way Play does.
		data, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("failed to read audio stream: %w", err)
		}
		return p.playFileLocked(data)
	}

	cmd.Stdin = r
	return p.runLocked(cmd)
}

// playFileLocked writes audio to a temp .mp3 file and plays it with the
// platform player. The caller must hold p.mu.
func (p *Player) playFileLocked(audioData []byte) error {
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

	speed, speedAdjusted := playbackSpeed()
	speedStr := strconv.FormatFloat(speed, 'f', -1, 64)

	cmd, err := fileCmd(tmpFile.Name(), speedStr, speedAdjusted)
	if err != nil {
		return err
	}
	return p.runLocked(cmd)
}

// runLocked starts cmd, records it so Stop() can kill it, waits for it to
// finish, then clears the record. The caller must hold p.mu.
func (p *Player) runLocked(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start audio player: %w", err)
	}

	p.procMu.Lock()
	p.cmd = cmd
	p.procMu.Unlock()

	err := cmd.Wait()

	p.procMu.Lock()
	p.cmd = nil
	p.procMu.Unlock()

	if err != nil {
		return fmt.Errorf("audio playback failed: %w", err)
	}
	return nil
}

// Stop kills the audio player process that is currently playing, if any. It is
// safe to call from another goroutine, for example a signal handler.
func (p *Player) Stop() {
	p.procMu.Lock()
	defer p.procMu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// fileCmd builds the platform command to play the audio file at path.
func fileCmd(path, speedStr string, speedAdjusted bool) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		// afplay's -r sets the playback rate; only pass it when a speed was
		// asked for, so default behavior is unchanged.
		if speedAdjusted {
			return exec.Command("afplay", "-r", speedStr, path), nil
		}
		return exec.Command("afplay", path), nil
	case "linux":
		if _, err := exec.LookPath("mpv"); err == nil {
			args := []string{"--no-video"}
			if speedAdjusted {
				// mpv keeps pitch steady when changing speed.
				args = append(args, "--speed="+speedStr)
			}
			args = append(args, path)
			return exec.Command("mpv", args...), nil
		}
		if _, err := exec.LookPath("ffplay"); err == nil {
			args := []string{"-nodisp", "-autoexit"}
			if speedAdjusted {
				// atempo changes tempo without changing pitch (valid 0.5x–2.0x).
				args = append(args, "-af", "atempo="+speedStr)
			}
			args = append(args, path)
			return exec.Command("ffplay", args...), nil
		}
		if _, err := exec.LookPath("mpg123"); err == nil {
			// mpg123 has no clean pitch-preserving tempo control, so speed is
			// not applied on this player.
			return exec.Command("mpg123", "-q", path), nil
		}
		return nil, fmt.Errorf("no suitable audio player found on Linux (install mpv, ffplay, or mpg123)")
	case "windows":
		return exec.Command("powershell", "-c",
			fmt.Sprintf(`(New-Object Media.SoundPlayer '%s').PlaySync()`, path)), nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// streamCmd builds a platform command that plays MP3 read from standard input.
// The boolean is false (with a nil error) when the platform's player cannot
// read from stdin, signaling the caller to buffer to a file instead. It returns
// an error only when no player is available or the platform is unsupported.
func streamCmd(speedStr string, speedAdjusted bool) (*exec.Cmd, bool, error) {
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("mpv"); err == nil {
			args := []string{"--no-video"}
			if speedAdjusted {
				args = append(args, "--speed="+speedStr)
			}
			args = append(args, "-") // "-" means read from stdin
			return exec.Command("mpv", args...), true, nil
		}
		if _, err := exec.LookPath("ffplay"); err == nil {
			args := []string{"-nodisp", "-autoexit"}
			if speedAdjusted {
				args = append(args, "-af", "atempo="+speedStr)
			}
			args = append(args, "pipe:0") // pipe:0 means read from stdin
			return exec.Command("ffplay", args...), true, nil
		}
		if _, err := exec.LookPath("mpg123"); err == nil {
			return exec.Command("mpg123", "-q", "-"), true, nil
		}
		return nil, false, fmt.Errorf("no suitable audio player found on Linux (install mpv, ffplay, or mpg123)")
	case "darwin", "windows":
		// afplay and PowerShell's SoundPlayer need a real file, so the caller
		// buffers the stream and plays it from a temp file instead.
		return nil, false, nil
	default:
		return nil, false, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

// IsPlaying returns whether audio is currently playing
func (p *Player) IsPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.isPlaying
}
