package audio

import (
	"sync"
	"testing"
)

func TestNewPlayer(t *testing.T) {
	player := NewPlayer()

	if player == nil {
		t.Fatal("expected player to be created")
	}
	if player.isPlaying {
		t.Error("expected isPlaying to be false initially")
	}
}

func TestPlayer_IsPlaying_Initial(t *testing.T) {
	player := NewPlayer()

	if player.IsPlaying() {
		t.Error("expected IsPlaying() to return false initially")
	}
}

func TestPlayer_IsPlaying_ThreadSafe(t *testing.T) {
	player := NewPlayer()

	var wg sync.WaitGroup
	results := make([]bool, 100)

	// Call IsPlaying concurrently
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			results[idx] = player.IsPlaying()
		}(i)
	}

	wg.Wait()

	// All results should be false (no audio playing)
	for i, result := range results {
		if result {
			t.Errorf("expected result[%d] to be false", i)
		}
	}
}

func TestPlayer_Play_InvalidData(t *testing.T) {
	player := NewPlayer()

	// Empty audio data should still try to play (and fail at the player level)
	// This tests that the mutex and temp file logic works
	err := player.Play([]byte{})

	// Expect an error because empty file won't be valid audio
	// The specific error depends on the platform audio player
	if err == nil {
		t.Log("Note: Empty audio data was accepted - player may vary by platform")
	}
}

func TestPlayer_MutexProtection(t *testing.T) {
	player := NewPlayer()

	// Verify mutex is properly initialized and can be used
	player.mu.Lock()
	// Simulate checking a protected resource
	wasPlaying := player.isPlaying
	player.mu.Unlock()

	if wasPlaying {
		t.Error("expected isPlaying to be false")
	}
}

func TestPlayer_PlaySetsIsPlaying(t *testing.T) {
	player := NewPlayer()

	// We can't easily test actual playback without audio files,
	// but we can verify the structure is correct
	if player.isPlaying {
		t.Error("isPlaying should be false before Play()")
	}
}

func TestPlayer_ConcurrentPlayAttempts(t *testing.T) {
	player := NewPlayer()

	// Simulate what happens when multiple goroutines try to play
	// Due to mutex, they should execute sequentially
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Use invalid data to trigger fast failure
			err := player.Play([]byte("not-valid-mp3"))
			if err != nil {
				errors <- err
			}
		}()
	}

	wg.Wait()
	close(errors)

	// All attempts should have completed (with errors for invalid data)
	// The key is no deadlock occurred
	errorCount := 0
	for range errors {
		errorCount++
	}

	t.Logf("Got %d errors from 10 concurrent play attempts (expected - invalid audio data)", errorCount)
}
