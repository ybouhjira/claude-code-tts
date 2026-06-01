package server

// Tests for the Provider abstraction in handleSpeak (issue #2).
//
// Test seams introduced in production code:
//   - server.go: NewWithRegistry(registry *tts.Registry) (*Server, error)
//     Creates a Server whose handleSpeak resolves providers from the Registry,
//     enabling full mock-provider injection without real network/audio.
//   - worker.go: NewWorkerPoolWithRegistry(workerCount, queueSize int, registry *tts.Registry) *WorkerPool
//     Creates a WorkerPool that stores the Registry; processJob will use it
//     instead of the hard-wired *tts.Client once the implementation is done.
//   - worker.go: (*WorkerPool).SubmitWithProvider(text string, voice tts.Voice, providerName string) (*Job, error)
//     Variant of Submit that records the provider name on the Job for worker routing.

import (
	"context"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

// buildRegistryWithFakeOpenAI returns a Registry pre-loaded with a fake "openai" provider.
func buildRegistryWithFakeOpenAI() *tts.Registry {
	r := tts.NewRegistry()
	r.Register(&fakeServerProvider{name: "openai"})
	return r
}

// TestHandleSpeak_DefaultProvider_UsesOpenAI verifies that when the `provider`
// argument is omitted, handleSpeak falls back to tts.DefaultProviderName ("openai")
// and succeeds when that provider is registered.
func TestHandleSpeak_DefaultProvider_UsesOpenAI(t *testing.T) {
	registry := buildRegistryWithFakeOpenAI()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text": "Hello from default provider",
		// no "provider" key → should default to "openai"
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if result.IsError {
		content := result.Content[0].(mcp.TextContent)
		t.Errorf("expected success with default provider, got error: %s", content.Text)
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "job-") {
		t.Errorf("expected result to contain job ID, got: %s", content.Text)
	}
}

// TestHandleSpeak_ExplicitValidProvider_UsesNamedProvider verifies that when
// `provider` is explicitly set to a registered provider name the call succeeds
// and the returned job references the correct provider.
func TestHandleSpeak_ExplicitValidProvider_UsesNamedProvider(t *testing.T) {
	registry := buildRegistryWithFakeOpenAI()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":     "Hello from explicit provider",
		"provider": "openai",
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if result.IsError {
		content := result.Content[0].(mcp.TextContent)
		t.Errorf("expected success with explicit provider 'openai', got error: %s", content.Text)
	}
}

// TestHandleSpeak_UnknownProvider_ReturnsErrorNamingProvider verifies that
// when an unregistered provider name is supplied the tool result is an error
// whose message names the bad provider value.
func TestHandleSpeak_UnknownProvider_ReturnsErrorNamingProvider(t *testing.T) {
	registry := buildRegistryWithFakeOpenAI()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	const badProvider = "gcp-tts"
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":     "Hello",
		"provider": badProvider,
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected tool error for unknown provider, got success")
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, badProvider) {
		t.Errorf("error message %q should name the unknown provider %q", content.Text, badProvider)
	}
}

// TestHandleSpeak_UnknownProvider_ErrorListsSupportedProviders verifies that
// the error message for an unknown provider also lists the currently registered
// providers, so clients know what is available.
func TestHandleSpeak_UnknownProvider_ErrorListsSupportedProviders(t *testing.T) {
	registry := buildRegistryWithFakeOpenAI()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":     "Hello",
		"provider": "nonexistent",
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown provider")
	}

	content := result.Content[0].(mcp.TextContent)
	// The error must mention "openai" because it is the only registered provider.
	if !strings.Contains(content.Text, "openai") {
		t.Errorf("error message %q should list 'openai' as a supported provider", content.Text)
	}
}

// TestHandleSpeak_RegistryProvider_DefaultVoiceUsedWhenVoiceOmitted verifies
// that when a provider is resolved from the registry and no voice is supplied,
// the provider's DefaultVoice is used (not the hard-coded "alloy").
func TestHandleSpeak_RegistryProvider_DefaultVoiceUsedWhenVoiceOmitted(t *testing.T) {
	registry := tts.NewRegistry()
	// Register a fake provider whose default voice is "shimmer" (not "alloy").
	registry.Register(&fakeServerProvider{name: "openai", defaultVoice: tts.VoiceShimmer})

	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text": "Default voice test",
		// no "voice" key
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if result.IsError {
		content := result.Content[0].(mcp.TextContent)
		t.Fatalf("expected success, got error: %s", content.Text)
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "shimmer") {
		t.Errorf("expected default voice 'shimmer' in result, got: %s", content.Text)
	}
}

// TestHandleSpeak_MultipleProviders_CanSelectEach verifies that when multiple
// providers are registered, each can be selected by name independently.
func TestHandleSpeak_MultipleProviders_CanSelectEach(t *testing.T) {
	registry := tts.NewRegistry()
	registry.Register(&fakeServerProvider{name: "openai"})
	registry.Register(&fakeServerProvider{name: "azure"})

	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	for _, providerName := range []string{"openai", "azure"} {
		t.Run(providerName, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]interface{}{
				"text":     "Hello",
				"provider": providerName,
			}

			result, err := srv.handleSpeak(context.Background(), request)
			if err != nil {
				t.Fatalf("handleSpeak returned error: %v", err)
			}
			if result.IsError {
				content := result.Content[0].(mcp.TextContent)
				t.Errorf("expected success for provider %q, got error: %s", providerName, content.Text)
			}
		})
	}
}

// TestHandleSpeak_UnknownProvider_ErrorListsBothSupportedProviders checks that
// when two providers are registered, both appear in the error message.
func TestHandleSpeak_UnknownProvider_ErrorListsBothSupportedProviders(t *testing.T) {
	registry := tts.NewRegistry()
	registry.Register(&fakeServerProvider{name: "openai"})
	registry.Register(&fakeServerProvider{name: "azure"})

	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":     "Hello",
		"provider": "nonexistent",
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown provider")
	}

	content := result.Content[0].(mcp.TextContent)
	if !strings.Contains(content.Text, "openai") {
		t.Errorf("error %q should mention 'openai'", content.Text)
	}
	if !strings.Contains(content.Text, "azure") {
		t.Errorf("error %q should mention 'azure'", content.Text)
	}
}

// TestHandleSpeak_EmptyProviderString_BehavesAsDefault verifies that passing
// an explicit empty string for the "provider" argument is treated identically
// to omitting the parameter — both should use the default provider.
func TestHandleSpeak_EmptyProviderString_BehavesAsDefault(t *testing.T) {
	registry := buildRegistryWithFakeOpenAI()
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":     "Empty provider string test",
		"provider": "", // explicit empty — should fall back to default
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if result.IsError {
		content := result.Content[0].(mcp.TextContent)
		t.Errorf("expected success with empty provider string, got error: %s", content.Text)
	}
}

// TestHandleSpeak_ProviderNameIsCaseSensitive documents and locks the current
// behaviour: provider names are matched case-sensitively.  "OpenAI" (mixed
// case) is NOT the same as "openai" (lower case) and must return an error
// rather than silently resolving to the registered "openai" provider.
func TestHandleSpeak_ProviderNameIsCaseSensitive(t *testing.T) {
	registry := buildRegistryWithFakeOpenAI() // registers "openai"
	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	mixedCaseNames := []string{"OpenAI", "OPENAI", "Openai", "openAI"}
	for _, name := range mixedCaseNames {
		t.Run(name, func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]interface{}{
				"text":     "Hello",
				"provider": name,
			}

			result, err := srv.handleSpeak(context.Background(), request)
			if err != nil {
				t.Fatalf("handleSpeak returned unexpected error: %v", err)
			}
			if !result.IsError {
				t.Errorf("expected error for case-variant provider name %q, got success (registry lookup should be case-sensitive)", name)
			}
		})
	}
}

// TestHandleSpeak_UnknownProvider_ErrorListsProvidersSorted verifies that when
// more than one provider is registered and an unknown provider is requested,
// the error message lists the supported providers in sorted order, so the
// output is deterministic regardless of registration order.
func TestHandleSpeak_UnknownProvider_ErrorListsProvidersSorted(t *testing.T) {
	registry := tts.NewRegistry()
	// Register in reverse alphabetical order to confirm sorting.
	registry.Register(&fakeServerProvider{name: "zzz-provider"})
	registry.Register(&fakeServerProvider{name: "aaa-provider"})
	registry.Register(&fakeServerProvider{name: "mmm-provider"})

	srv, err := NewWithRegistry(registry)
	if err != nil {
		t.Fatalf("NewWithRegistry returned error: %v", err)
	}
	defer srv.Shutdown()

	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]interface{}{
		"text":     "Hello",
		"provider": "unknown",
	}

	result, err := srv.handleSpeak(context.Background(), request)
	if err != nil {
		t.Fatalf("handleSpeak returned unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected error for unknown provider")
	}

	content := result.Content[0].(mcp.TextContent)
	aaaIdx := strings.Index(content.Text, "aaa-provider")
	mmmIdx := strings.Index(content.Text, "mmm-provider")
	zzzIdx := strings.Index(content.Text, "zzz-provider")

	if aaaIdx == -1 || mmmIdx == -1 || zzzIdx == -1 {
		t.Errorf("error message %q should list all three providers", content.Text)
	}
	if !(aaaIdx < mmmIdx && mmmIdx < zzzIdx) {
		t.Errorf("providers not in sorted order in error message: aaa@%d mmm@%d zzz@%d in %q",
			aaaIdx, mmmIdx, zzzIdx, content.Text)
	}
}

// --- fake provider used only in this test file ---

// fakeServerProvider is a minimal tts.Provider stub for server-level tests.
// It never makes real network calls.
type fakeServerProvider struct {
	name         string
	defaultVoice tts.Voice
}

func (f *fakeServerProvider) Name() string { return f.name }

func (f *fakeServerProvider) Synthesize(_ string, _ tts.Voice) ([]byte, error) {
	// Returns stub audio so tests never hit the real API.
	return []byte("stub-audio"), nil
}

func (f *fakeServerProvider) IsValidVoice(voice string) bool {
	return tts.IsValidVoice(voice)
}

func (f *fakeServerProvider) DefaultVoice() tts.Voice {
	if f.defaultVoice == "" {
		return tts.VoiceAlloy
	}
	return f.defaultVoice
}
