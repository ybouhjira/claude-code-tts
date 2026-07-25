package reader

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

func TestIsDocumentPath(t *testing.T) {
	if IsDocumentPath("/tmp/session.jsonl") {
		t.Error("a .jsonl path must be a transcript, not a document")
	}
	for _, p := range []string{"/tmp/notes.md", "/tmp/plain.txt", "/tmp/NOTES"} {
		if !IsDocumentPath(p) {
			t.Errorf("%s should be a document", p)
		}
	}
}

func TestParseDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.md")
	content := "# My Notes\n\nFirst paragraph.\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	msgs, err := ParseDocument(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].Role != "doc" {
		t.Fatalf("want one doc message, got %+v", msgs)
	}
	if !strings.Contains(msgs[0].Text, "First paragraph.") {
		t.Errorf("document text lost: %q", msgs[0].Text)
	}
	if title := DocumentTitle(path); title != "My Notes" {
		t.Errorf("title = %q, want the first heading", title)
	}
}

func TestDocumentTitleFallsBackToFilename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plain.txt")
	if err := os.WriteFile(path, []byte("no heading here\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if title := DocumentTitle(path); title != "plain.txt" {
		t.Errorf("title = %q, want the filename", title)
	}
}

// TestMessagesEndpointServesDocument drives the whole document path the way
// the page does: the server starts on an .md file and /api/messages must
// return it as a single "doc" message with document labels.
func TestMessagesEndpointServesDocument(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "guide.md")
	if err := os.WriteFile(path, []byte("# Reading Guide\n\nHello world.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Transcript:      path,
		Providers:       map[string]tts.Synthesizer{"fake": &fakeSynth{audio: []byte("MP3")}},
		DefaultProvider: "fake",
		Speed:           1.0,
		ProjectsRoot:    t.TempDir(),
	})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	var got struct {
		Session  string    `json:"session"`
		Title    string    `json:"title"`
		Project  string    `json:"project"`
		Messages []Message `json:"messages"`
	}
	getJSON(t, ts.URL+"/api/messages", &got)
	if !strings.HasPrefix(got.Session, "doc-") {
		t.Errorf("session id = %q, want a doc- id", got.Session)
	}
	if got.Title != "Reading Guide" {
		t.Errorf("title = %q, want the document's first heading", got.Title)
	}
	if got.Project != filepath.Base(dir) {
		t.Errorf("project = %q, want the parent directory name %q", got.Project, filepath.Base(dir))
	}
	if len(got.Messages) != 1 || got.Messages[0].Role != "doc" {
		t.Fatalf("want one doc message, got %+v", got.Messages)
	}

	// A stale doc id (server restarted, registry gone) must fail with 404,
	// never fall through to the transcript lookup.
	resp, err := http.Get(ts.URL + "/api/messages?s=doc-0011223344556677")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("stale doc id: status %d, want 404", resp.StatusCode)
	}
}
