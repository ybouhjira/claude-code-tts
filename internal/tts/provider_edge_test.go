package tts

// Edge-case and gap coverage for Registry (issue #2).
//
// These tests target cases not exercised by provider_test.go:
//   - Get on an empty registry (error message gracefully handles empty list)
//   - Names() always returns a non-nil slice
//   - Names() returns a copy (mutating it cannot corrupt internal state)
//   - Overwriting an entry keeps Names() deduplicated
//   - Compile-time interface conformance for the mock type used in server tests

import (
	"strings"
	"testing"
)

// --- Interface conformance ---

// Compile-time assertion: fakeProvider used in this file must implement Provider.
// If Provider gains a new method this line fails to compile, giving immediate feedback.
// Note: the *Client assertion lives in provider_test.go to avoid duplication.
var _ Provider = (*fakeProvider)(nil)

// --- Registry edge cases ---

// TestRegistry_Get_EmptyRegistry_ErrorMessageHasEmptyList verifies that the
// error message produced by Get on an empty registry gracefully formats the
// empty supported-provider list rather than crashing or producing a broken
// message.
func TestRegistry_Get_EmptyRegistry_ErrorMessageHasEmptyList(t *testing.T) {
	r := NewRegistry()

	_, err := r.Get("anything")
	if err == nil {
		t.Fatal("Get on empty registry should return error, got nil")
	}

	msg := err.Error()
	if !strings.Contains(msg, "anything") {
		t.Errorf("error message %q should contain the requested name %q", msg, "anything")
	}
	// The message should not panic or contain a placeholder like "<nil>"; a
	// rendered empty list (e.g. "supported providers: ") is acceptable.
	if strings.Contains(msg, "<nil>") || strings.Contains(msg, "%!") {
		t.Errorf("error message %q contains an unformatted placeholder, which indicates a formatting bug", msg)
	}
}

// TestRegistry_Names_EmptyRegistry_ReturnsNonNil verifies that Names() on an
// empty registry always returns a non-nil slice.  Callers that pass the result
// directly to strings.Join or range should not have to guard against nil.
func TestRegistry_Names_EmptyRegistry_ReturnsNonNil(t *testing.T) {
	r := NewRegistry()
	names := r.Names()
	if names == nil {
		t.Error("Names() on empty registry returned nil; expected non-nil empty slice so callers can safely range/join")
	}
	if len(names) != 0 {
		t.Errorf("Names() on empty registry returned %d names: %v", len(names), names)
	}
}

// TestRegistry_Names_ReturnsCopy verifies that mutating the slice returned by
// Names() does not corrupt the registry's internal state.  This guards against
// implementations that return a direct reference to the internal slice.
func TestRegistry_Names_ReturnsCopy(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeProvider{name: "alpha"})
	r.Register(&fakeProvider{name: "beta"})

	first := r.Names()
	if len(first) != 2 {
		t.Fatalf("expected 2 names before mutation, got %d: %v", len(first), first)
	}

	// Mutate the returned slice.
	first[0] = "MUTATED"

	// The registry must still report the original names.
	second := r.Names()
	for _, name := range second {
		if name == "MUTATED" {
			t.Error("mutating the slice returned by Names() corrupted internal registry state")
		}
	}
	if len(second) != 2 {
		t.Errorf("registry length changed after external mutation: got %d, want 2", len(second))
	}
}

// TestRegistry_Register_Overwrite_NamesDeduplicates verifies that registering
// two providers with the same Name() leaves exactly one entry in Names(),
// preventing duplicate keys in the sorted output.
func TestRegistry_Register_Overwrite_NamesDeduplicates(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeProvider{name: "dupe"})
	r.Register(&fakeProvider{name: "dupe"}) // second registration — same key

	names := r.Names()
	if len(names) != 1 {
		t.Errorf("Names() after two registrations of the same name should return 1 entry, got %d: %v", len(names), names)
	}
	if names[0] != "dupe" {
		t.Errorf("Names()[0] = %q, want %q", names[0], "dupe")
	}
}

// TestRegistry_Get_ErrorMessage_ListsAllRegisteredProvidersSorted verifies that
// the error message from Get on an unknown name enumerates all registered
// provider names and that they appear in sorted order, so callers get a
// consistent and useful message.
func TestRegistry_Get_ErrorMessage_ListsAllRegisteredProvidersSorted(t *testing.T) {
	r := NewRegistry()
	r.Register(&fakeProvider{name: "zebra"})
	r.Register(&fakeProvider{name: "alpha"})

	_, err := r.Get("unknown")
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}

	msg := err.Error()
	alphaIdx := strings.Index(msg, "alpha")
	zebraIdx := strings.Index(msg, "zebra")

	if alphaIdx == -1 {
		t.Errorf("error message %q should list registered provider 'alpha'", msg)
	}
	if zebraIdx == -1 {
		t.Errorf("error message %q should list registered provider 'zebra'", msg)
	}
	if alphaIdx != -1 && zebraIdx != -1 && alphaIdx > zebraIdx {
		t.Errorf("error message lists providers out of order: 'zebra' (%d) appears before 'alpha' (%d) in %q",
			zebraIdx, alphaIdx, msg)
	}
}
