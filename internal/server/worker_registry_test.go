package server

// Integration tests for processJob using the registry path (issue #2).
//
// These tests use NewWorkerPoolWithRegistry + SubmitWithProvider to exercise
// processJob without any real network calls or API keys.  A recordingProvider
// (defined below) allows assertions on whether Synthesize was invoked.
//
// Audio note: after a successful Synthesize call, processJob forwards the bytes
// to wp.audioPlayer.Play().  Because the stub bytes are not valid MP3, Play()
// will fail on any platform, leaving the job in "failed" status.  This is
// expected and acceptable for tests (a) and (c): the important assertion is
// what caused the failure, verified by checking the error message and whether
// the provider was invoked.

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

// --- helpers ---

// recordingProvider is a mock tts.Provider that records Synthesize invocations
// and can be configured to return an error.
type recordingProvider struct {
	name      string
	synthErr  error // if non-nil, Synthesize returns this error
	callCount int   // how many times Synthesize was called
	mu        sync.Mutex
}

func (p *recordingProvider) Name() string { return p.name }

func (p *recordingProvider) Synthesize(text string, voice tts.Voice) ([]byte, error) {
	p.mu.Lock()
	p.callCount++
	p.mu.Unlock()
	if p.synthErr != nil {
		return nil, p.synthErr
	}
	return []byte("stub-audio-bytes"), nil
}

func (p *recordingProvider) IsValidVoice(v string) bool { return tts.IsValidVoice(v) }
func (p *recordingProvider) DefaultVoice() tts.Voice    { return tts.VoiceAlloy }

func (p *recordingProvider) CallCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

// Compile-time assertion: recordingProvider must satisfy tts.Provider.
var _ tts.Provider = (*recordingProvider)(nil)

// waitForJobStatus polls job.Status until it reaches a terminal state
// ("completed" or "failed") or the timeout elapses, then returns the final
// status.  This avoids fixed sleeps which are brittle on slow CI machines.
func waitForJobStatus(job *Job, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		job.mu.RLock()
		s := job.Status
		job.mu.RUnlock()
		if s == "completed" || s == "failed" {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	job.mu.RLock()
	defer job.mu.RUnlock()
	return job.Status
}

// --- tests ---

// TestProcessJob_RegistryPath_SynthesizeIsCalled verifies that when a job with
// a known provider name is processed via the registry path, the provider's
// Synthesize method is actually invoked.  The job is expected to end up
// "failed" because the stub audio bytes are not valid MP3 (audio playback
// fails), but the key assertion is that Synthesize was called before that.
func TestProcessJob_RegistryPath_SynthesizeIsCalled(t *testing.T) {
	provider := &recordingProvider{name: "fakeprovider"}
	registry := tts.NewRegistry()
	registry.Register(provider)

	wp := NewWorkerPoolWithRegistry(1, 10, registry)
	wp.Start()
	defer wp.Stop()

	job, err := wp.SubmitWithProvider("hello world", tts.VoiceAlloy, "fakeprovider")
	if err != nil {
		t.Fatalf("SubmitWithProvider returned unexpected error: %v", err)
	}

	finalStatus := waitForJobStatus(job, 3*time.Second)

	// Synthesize must have been called regardless of whether audio succeeded.
	if provider.CallCount() == 0 {
		t.Error("expected provider.Synthesize to be called, but call count is 0")
	}

	// The job should be in a terminal state (not pending/processing) by now.
	if finalStatus == "pending" || finalStatus == "processing" {
		t.Errorf("job is still in non-terminal state %q after timeout", finalStatus)
	}
}

// TestProcessJob_RegistryPath_ProcessedCounterIncrements verifies that when
// synthesis succeeds AND audio playback succeeds, processJob increments the
// processed counter.  On Linux/CI, audio playback typically fails on stub
// data, so this test is marked to check the counter only when the job
// completes successfully.  If audio fails, we instead assert that the failure
// counter incremented (not zero).
//
// This documents the reachable assertion boundary: we can always confirm that
// exactly one of {processed, failed} is incremented after a job is processed.
func TestProcessJob_RegistryPath_ExactlyOneCounterIncrements(t *testing.T) {
	provider := &recordingProvider{name: "fakeprovider"}
	registry := tts.NewRegistry()
	registry.Register(provider)

	wp := NewWorkerPoolWithRegistry(1, 10, registry)
	wp.Start()
	defer wp.Stop()

	job, err := wp.SubmitWithProvider("counter test", tts.VoiceNova, "fakeprovider")
	if err != nil {
		t.Fatalf("SubmitWithProvider returned unexpected error: %v", err)
	}

	waitForJobStatus(job, 3*time.Second)

	processed := wp.processed.Load()
	failed := wp.failed.Load()
	total := processed + failed

	if total != 1 {
		t.Errorf("expected exactly one of {processed, failed} to be 1, got processed=%d failed=%d",
			processed, failed)
	}
}

// TestProcessJob_RegistryPath_UnknownProvider_MarksJobFailed verifies that
// when a job arrives with a provider name that is not in the registry,
// processJob marks the job "failed" and increments the failed counter without
// calling audio.
func TestProcessJob_RegistryPath_UnknownProvider_MarksJobFailed(t *testing.T) {
	// Register one known provider so the registry is not empty.
	provider := &recordingProvider{name: "knownprovider"}
	registry := tts.NewRegistry()
	registry.Register(provider)

	wp := NewWorkerPoolWithRegistry(1, 10, registry)
	wp.Start()
	defer wp.Stop()

	job, err := wp.SubmitWithProvider("should fail", tts.VoiceAlloy, "nonexistent-provider")
	if err != nil {
		t.Fatalf("SubmitWithProvider returned unexpected error: %v", err)
	}

	finalStatus := waitForJobStatus(job, 3*time.Second)

	if finalStatus != "failed" {
		t.Errorf("expected job status 'failed' for unknown provider, got %q", finalStatus)
	}

	job.mu.RLock()
	jobErr := job.Error
	job.mu.RUnlock()

	if !strings.Contains(jobErr, "nonexistent-provider") {
		t.Errorf("job error %q should mention the unknown provider name 'nonexistent-provider'", jobErr)
	}

	if wp.failed.Load() < 1 {
		t.Errorf("expected failed counter >= 1 after unknown-provider job, got %d", wp.failed.Load())
	}

	// Synthesize on the known provider must NOT have been called.
	if provider.CallCount() != 0 {
		t.Errorf("expected Synthesize NOT to be called for unknown provider, but got %d calls on knownprovider",
			provider.CallCount())
	}
}

// TestProcessJob_RegistryPath_SynthesizeError_MarksJobFailed verifies that
// when the resolved provider's Synthesize returns an error, processJob marks
// the job "failed" and increments the failed counter.
func TestProcessJob_RegistryPath_SynthesizeError_MarksJobFailed(t *testing.T) {
	synthErr := errors.New("upstream TTS service unavailable")
	provider := &recordingProvider{name: "errorprovider", synthErr: synthErr}
	registry := tts.NewRegistry()
	registry.Register(provider)

	wp := NewWorkerPoolWithRegistry(1, 10, registry)
	wp.Start()
	defer wp.Stop()

	job, err := wp.SubmitWithProvider("will error", tts.VoiceEcho, "errorprovider")
	if err != nil {
		t.Fatalf("SubmitWithProvider returned unexpected error: %v", err)
	}

	finalStatus := waitForJobStatus(job, 3*time.Second)

	if finalStatus != "failed" {
		t.Errorf("expected job status 'failed' after Synthesize error, got %q", finalStatus)
	}

	job.mu.RLock()
	jobErr := job.Error
	job.mu.RUnlock()

	if !strings.Contains(jobErr, synthErr.Error()) {
		t.Errorf("job error %q should contain synthesis error message %q", jobErr, synthErr.Error())
	}

	if wp.failed.Load() < 1 {
		t.Errorf("expected failed counter >= 1 after Synthesize error, got %d", wp.failed.Load())
	}

	// Synthesize must have been called once.
	if provider.CallCount() != 1 {
		t.Errorf("expected Synthesize to be called once, got %d", provider.CallCount())
	}
}

// TestSubmitWithProvider_JobHasProviderName verifies that SubmitWithProvider
// sets the ProviderName field on the returned Job, enabling processJob to
// route to the correct provider.
func TestSubmitWithProvider_JobHasProviderName(t *testing.T) {
	registry := tts.NewRegistry()
	registry.Register(&recordingProvider{name: "myprovider"})

	wp := NewWorkerPoolWithRegistry(1, 10, registry)
	// Workers not started — we only test the submission result.

	job, err := wp.SubmitWithProvider("test text", tts.VoiceShimmer, "myprovider")
	if err != nil {
		t.Fatalf("SubmitWithProvider returned unexpected error: %v", err)
	}
	if job.ProviderName != "myprovider" {
		t.Errorf("job.ProviderName = %q, want %q", job.ProviderName, "myprovider")
	}
	if job.Voice != tts.VoiceShimmer {
		t.Errorf("job.Voice = %q, want %q", job.Voice, tts.VoiceShimmer)
	}
	if job.Text != "test text" {
		t.Errorf("job.Text = %q, want %q", job.Text, "test text")
	}
	if job.Status != "pending" {
		t.Errorf("job.Status = %q, want %q", job.Status, "pending")
	}
}

// TestProcessJob_MultipleProviders_RoutesToCorrectOne verifies that when two
// providers are registered, a job is routed to exactly the named provider
// (not to the other one).
func TestProcessJob_MultipleProviders_RoutesToCorrectOne(t *testing.T) {
	providerA := &recordingProvider{name: "provider-a", synthErr: errors.New("a-error")}
	providerB := &recordingProvider{name: "provider-b", synthErr: errors.New("b-error")}

	registry := tts.NewRegistry()
	registry.Register(providerA)
	registry.Register(providerB)

	wp := NewWorkerPoolWithRegistry(1, 10, registry)
	wp.Start()
	defer wp.Stop()

	// Submit a job for provider-b only.
	job, err := wp.SubmitWithProvider("route test", tts.VoiceAlloy, "provider-b")
	if err != nil {
		t.Fatalf("SubmitWithProvider returned unexpected error: %v", err)
	}

	waitForJobStatus(job, 3*time.Second)

	if providerA.CallCount() != 0 {
		t.Errorf("provider-a.Synthesize was called %d time(s), expected 0 (job was for provider-b)",
			providerA.CallCount())
	}
	if providerB.CallCount() != 1 {
		t.Errorf("provider-b.Synthesize was called %d time(s), expected 1", providerB.CallCount())
	}
}
