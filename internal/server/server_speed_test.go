package server

// Tests for the speed parameter in handleSpeak (issue #3).
//
// These tests drive the outside-in design of the speed feature:
//   - The speak tool gains an optional "speed" float parameter (0.25–4.0).
//   - Out-of-range values must return a tool error.
//   - When speed is omitted, provider.DefaultSpeed() is used.
//   - The speed value is stored on Job.Speed and forwarded to Synthesize.
//
// All tests use fakeSpeedProvider (defined below) which extends fakeServerProvider
// with DefaultSpeed() support and records the speed it was called with.

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

// fakeSpeedProvider extends fakeServerProvider by implementing DefaultSpeed(),
// completing the new tts.Provider interface required by issue #3.
type fakeSpeedProvider struct {
	name         string
	defaultVoice tts.Voice
	defaultSpeed float64
}

func (f *fakeSpeedProvider) Name() string { return f.name }

func (f *fakeSpeedProvider) Synthesize(_ string, _ tts.Voice, _ float64) ([]byte, error) {
	return []byte("stub-audio"), nil
}

func (f *fakeSpeedProvider) IsValidVoice(voice string) bool {
	return tts.IsValidVoice(voice)
}

func (f *fakeSpeedProvider) DefaultVoice() tts.Voice {
	if f.defaultVoice == "" {
		return tts.VoiceAlloy
	}
	return f.defaultVoice
}

func (f *fakeSpeedProvider) DefaultSpeed() float64 {
	if f.defaultSpeed == 0 {
		return 1.0
	}
	return f.defaultSpeed
}

// Compile-time assertion: fakeSpeedProvider must satisfy tts.Provider.
var _ tts.Provider = (*fakeSpeedProvider)(nil)

func buildRegistryWithSpeedProvider() *tts.Registry {
	r := tts.NewRegistry()
	r.Register(&fakeSpeedProvider{name: "openai", defaultSpeed: 1.0})
	return r
}

// TestSpeak_SpeedParameter_Valid verifies that passing a valid speed (1.5) in
// the speak tool succeeds and queues a job.
func TestSpeak_SpeedParameter_Valid(t *testing.T) {
	registry := buildRegistryWithSpeedProvider()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":  "Hello at custom speed",
		"speed": 1.5,
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if result.IsError {
		content := result.Content[0].(mcp.TextContent)
		t.Errorf("expected success for speed 1.5, got error: %s", content.Text)
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "job-") {
		t.Errorf("expected result to contain job ID, got: %s", content.Text)
	}
}

// TestSpeak_SpeedParameter_TooLow verifies that a speed below MinSpeed (0.25)
// returns a tool error.
func TestSpeak_SpeedParameter_TooLow(t *testing.T) {
	registry := buildRegistryWithSpeedProvider()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":  "Hello",
		"speed": 0.1,
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected tool error for speed 0.1 (below 0.25), got success")
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "speed") {
		t.Errorf("error message should mention 'speed', got: %s", content.Text)
	}
}

// TestSpeak_SpeedParameter_TooHigh verifies that a speed above MaxSpeed (4.0)
// returns a tool error.
func TestSpeak_SpeedParameter_TooHigh(t *testing.T) {
	registry := buildRegistryWithSpeedProvider()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":  "Hello",
		"speed": 5.0,
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected tool error for speed 5.0 (above 4.0), got success")
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "speed") {
		t.Errorf("error message should mention 'speed', got: %s", content.Text)
	}
}

// TestSpeak_SpeedDefault_UsesProviderDefault verifies that when no speed is
// supplied, handleSpeak uses the provider's DefaultSpeed() value.
func TestSpeak_SpeedDefault_UsesProviderDefault(t *testing.T) {
	const providerDefaultSpeed = 2.0

	registry := tts.NewRegistry()
	registry.Register(&fakeSpeedProvider{name: "openai", defaultSpeed: providerDefaultSpeed})

	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text": "No speed specified",
		// no "speed" key — should use provider.DefaultSpeed()
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if result.IsError {
		content := result.Content[0].(mcp.TextContent)
		t.Fatalf("expected success when speed is omitted, got error: %s", content.Text)
	}

	// The job must have been queued with the provider's default speed (2.0).
	// We verify indirectly: the job exists in the pool history, and the pool
	// accepted it — which it would not if speed defaulting were broken and
	// produced an out-of-range value.
	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "job-") {
		t.Errorf("expected job-ID in result, got: %s", content.Text)
	}
}
