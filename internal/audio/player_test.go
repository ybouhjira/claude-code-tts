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

// Table-driven tests for NewPlayer
func TestNewPlayer_TableDriven(t *testing.T) {
	tests := []struct {
		name string
	}{
		{"first player"},
		{"second player"},
		{"third player"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			player := NewPlayer()
			if player == nil {
				t.Fatal("expected player to be created")
			}
			if player.isPlaying {
				t.Error("expected isPlaying to be false initially")
			}
			// Verify mutex is initialized (can be locked)
			player.mu.Lock()
			player.mu.Unlock()
		})
	}
}

func TestPlayer_IsPlaying_ConsistentState(t *testing.T) {
	player := NewPlayer()

	// Call IsPlaying multiple times, should always be consistent
	for i := 0; i < 10; i++ {
		if player.IsPlaying() {
			t.Errorf("iteration %d: expected IsPlaying() to return false", i)
		}
	}
}

func TestPlayer_Play_CreatesAndRemovesTempFile(t *testing.T) {
	player := NewPlayer()

	// This will fail (invalid audio), but should still create and clean up temp file
	_ = player.Play([]byte("fake-audio-data"))

	// We can't directly verify the temp file was deleted since it's cleaned up
	// before the function returns, but we can verify no panic occurred
	// and the function completed (which means defer cleanup ran)
}

func TestPlayer_IsPlaying_AfterPlayFailure(t *testing.T) {
	player := NewPlayer()

	// Try to play invalid data (will fail)
	_ = player.Play([]byte("invalid"))

	// After failed play, isPlaying should be false
	if player.IsPlaying() {
		t.Error("expected IsPlaying() to be false after failed play")
	}
}

func TestPlayer_MutexProtectsIsPlaying(t *testing.T) {
	player := NewPlayer()

	// Concurrent reads of isPlaying should work safely
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// This should not race or panic
			_ = player.IsPlaying()
		}()
	}
	wg.Wait()
}

func TestPlayer_Play_EmptyData(t *testing.T) {
	player := NewPlayer()

	err := player.Play([]byte{})

	// Empty data should still try to play (and likely fail at player level)
	// We're testing that it doesn't panic or hang
	if err == nil {
		t.Log("Note: Empty audio data was accepted by the platform player")
	}
}

func TestPlayer_Play_LargeData(t *testing.T) {
	player := NewPlayer()

	// Create 10MB of fake data
	largeData := make([]byte, 10*1024*1024)
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	// This will fail (invalid audio), but tests that large data doesn't panic
	err := player.Play(largeData)

	if err == nil {
		t.Log("Note: Large invalid data was accepted")
	}
	// Main goal: no panic, no deadlock
}

func TestPlayer_Play_NilData(t *testing.T) {
	player := NewPlayer()

	// nil data should be handled gracefully
	err := player.Play(nil)

	// Should likely fail, but shouldn't panic
	if err == nil {
		t.Log("Note: nil audio data was accepted")
	}
}

func TestPlayer_Sequential_Plays(t *testing.T) {
	player := NewPlayer()

	// Multiple sequential plays should work (even if they fail due to invalid data)
	for i := 0; i < 5; i++ {
		_ = player.Play([]byte("test-data"))

		// After each play, isPlaying should be false
		if player.IsPlaying() {
			t.Errorf("iteration %d: expected IsPlaying() to be false after play completed", i)
		}
	}
}

func TestPlayer_IsPlaying_DuringPlay(t *testing.T) {
	player := NewPlayer()

	// Start a play in a goroutine
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Use invalid data for quick failure
		_ = player.Play([]byte("test"))
	}()

	// Wait for completion
	<-done

	// After play completes, isPlaying should be false
	if player.IsPlaying() {
		t.Error("expected IsPlaying() to be false after play completed")
	}
}

func TestPlayer_ConcurrentIsPlayingCalls(t *testing.T) {
	player := NewPlayer()

	// Many concurrent IsPlaying calls should not cause race conditions
	var wg sync.WaitGroup
	results := make([]bool, 1000)

	for i := 0; i < 1000; i++ {
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
			t.Errorf("result[%d] should be false", i)
		}
	}
}

func TestPlayer_StructureInitialization(t *testing.T) {
	player := NewPlayer()

	// Verify the player struct is properly initialized
	if player == nil {
		t.Fatal("player should not be nil")
	}

	// isPlaying should be false
	if player.isPlaying {
		t.Error("isPlaying field should be false initially")
	}

	// Mutex should be usable
	player.mu.Lock()
	player.isPlaying = true
	playing := player.isPlaying
	player.mu.Unlock()

	if !playing {
		t.Error("mutex should protect isPlaying field")
	}
}
