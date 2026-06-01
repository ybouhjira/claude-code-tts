package tts

import (
	"strings"
	"testing"
)

// --- Compile-time interface check ---

// var _ Provider = (*Client)(nil) asserts that *Client implements Provider.
// If Provider gains a new method that Client doesn't implement this line fails
// to compile, giving immediate feedback during the RED phase.
var _ Provider = (*Client)(nil)

// --- Registry tests ---

func TestNewRegistry_IsEmpty(t *testing.T) {
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry() returned nil")
	}
	names := r.Names()
	if len(names) != 0 {
		t.Errorf("expected empty registry, got names: %v", names)
	}
}

func TestRegistry_Register_And_Get_ReturnsSameProvider(t *testing.T) {
	r := NewRegistry()
	p := &fakeProvider{name: "fakeprovider"}

	r.Register(p)

	got, err := r.Get("fakeprovider")
	if err != nil {
		t.Fatalf("Get(%q) returned unexpected error: %v", "fakeprovider", err)
	}
	if got != p {
		t.Errorf("Get(%q) returned %v, want %v", "fakeprovider", got, p)
	}
}

func TestRegistry_Get_UnknownName_ReturnsError(t *testing.T) {
	r := NewRegistry()

	_, err := r.Get("nonexistent")
	if err == nil {
		t.Fatal("Get(\"nonexistent\") expected an error, got nil")
	}
}

func TestRegistry_Names_ReturnsSortedList(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeProvider{name: "zebra"})
	r.Register(&fakeProvider{name: "alpha"})
	r.Register(&fakeProvider{name: "middle"})

	names := r.Names()

	if len(names) != 3 {
		t.Fatalf("expected 3 names, got %d: %v", len(names), names)
	}
	expected := []string{"alpha", "middle", "zebra"}
	for i, want := range expected {
		if names[i] != want {
			t.Errorf("Names()[%d] = %q, want %q (full list: %v)", i, names[i], want, names)
		}
	}
}

func TestRegistry_Register_OverwritesPreviousEntry(t *testing.T) {
	r := NewRegistry()
	first := &fakeProvider{name: "prov"}
	second := &fakeProvider{name: "prov"}

	r.Register(first)
	r.Register(second)

	got, err := r.Get("prov")
	if err != nil {
		t.Fatalf("Get returned error after overwrite: %v", err)
	}
	if got != second {
		t.Errorf("expected second provider after overwrite, got %v", got)
	}

	names := r.Names()
	if len(names) != 1 {
		t.Errorf("expected 1 name after overwrite, got %d: %v", len(names), names)
	}
}

func TestRegistry_Names_EmptyWhenNoProviders(t *testing.T) {
	r := NewRegistry()
	names := r.Names()
	if names == nil {
		// nil slice is acceptable for an empty registry, but length must be 0
		return
	}
	if len(names) != 0 {
		t.Errorf("expected 0 names, got %d", len(names))
	}
}

// --- OpenAI Client Provider-interface behaviour tests ---

func TestClient_Name_ReturnsOpenAI(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	c := NewClient()

	if c.Name() != "openai" {
		t.Errorf("Client.Name() = %q, want %q", c.Name(), "openai")
	}
}

func TestClient_DefaultVoice_IsValid(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	c := NewClient()

	dv := c.DefaultVoice()
	if !IsValidVoice(string(dv)) {
		t.Errorf("Client.DefaultVoice() = %q, which is not a valid voice", dv)
	}
}

func TestClient_IsValidVoice_DelegatesToPackageFunc(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "test-key")
	c := NewClient()

	// Valid voices must all pass
	for _, v := range ValidVoices() {
		if !c.IsValidVoice(string(v)) {
			t.Errorf("Client.IsValidVoice(%q) = false, want true", v)
		}
	}

	// An unknown voice must fail
	if c.IsValidVoice("not-a-voice") {
		t.Error("Client.IsValidVoice(\"not-a-voice\") = true, want false")
	}
}

func TestDefaultProviderName_IsOpenAI(t *testing.T) {
	if DefaultProviderName != "openai" {
		t.Errorf("DefaultProviderName = %q, want %q", DefaultProviderName, "openai")
	}
}

func TestRegistry_Get_ErrorMessage_ContainsProviderName(t *testing.T) {
	r := NewRegistry()
	_, err := r.Get("mystery")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "mystery") {
		t.Errorf("error message %q should mention the unknown provider name %q", err.Error(), "mystery")
	}
}

// --- helpers ---

// fakeProvider is a minimal Provider stub used in registry tests.
type fakeProvider struct {
	name string
}

func (f *fakeProvider) Name() string                                 { return f.name }
func (f *fakeProvider) Synthesize(_ string, _ Voice) ([]byte, error) { return nil, nil }
func (f *fakeProvider) IsValidVoice(_ string) bool                   { return true }
func (f *fakeProvider) DefaultVoice() Voice                          { return VoiceAlloy }
