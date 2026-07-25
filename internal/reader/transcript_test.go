package reader

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixture covers every line shape the parser must handle: summaries, meta
// entries, plain and structured content, thinking-only lines, tool results,
// slash-command noise, system-reminder plumbing, sidechains, and garbage.
const fixture = `{"type":"summary","summary":"Old chat"}
{"type":"user","isMeta":true,"message":{"role":"user","content":"meta noise"}}
{"type":"user","message":{"role":"user","content":"Hello there."},"timestamp":"2026-07-25T10:00:00Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"thinking","thinking":"hmm"}]},"timestamp":"2026-07-25T10:00:05Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hi! How can I help?"}]},"timestamp":"2026-07-25T10:00:06Z"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"tool output"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Second part."}]},"timestamp":"2026-07-25T10:00:08Z"}
{"type":"user","message":{"role":"user","content":"<command-name>/model</command-name>"}}
{"type":"user","message":{"role":"user","content":"<local-command-stdout>ok</local-command-stdout>"}}
{"type":"user","message":{"role":"user","content":"Thanks!<system-reminder>context plumbing</system-reminder>"},"timestamp":"2026-07-25T10:01:00Z"}
{"type":"assistant","isSidechain":true,"message":{"role":"assistant","content":[{"type":"text","text":"subagent text"}]}}
this line is not JSON at all
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"You are welcome."}]},"timestamp":"2026-07-25T10:01:05Z"}
`

func writeTranscript(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestParseTranscript(t *testing.T) {
	msgs, err := ParseTranscript(writeTranscript(t, fixture))
	if err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}

	want := []Message{
		{Role: "user", Text: "Hello there."},
		{Role: "assistant", Text: "Hi! How can I help?\n\nSecond part."},
		{Role: "user", Text: "Thanks!"},
		{Role: "assistant", Text: "You are welcome."},
	}
	if len(msgs) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(msgs), len(want), msgs)
	}
	for i, w := range want {
		if msgs[i].Role != w.Role {
			t.Errorf("message %d: role %q, want %q", i, msgs[i].Role, w.Role)
		}
		if msgs[i].Text != w.Text {
			t.Errorf("message %d: text %q, want %q", i, msgs[i].Text, w.Text)
		}
	}
	if msgs[0].Timestamp == "" {
		t.Error("expected the first user message to keep its timestamp")
	}
}

// A turn interrupted by a tool run keeps its chunks in Parts (working notes,
// then the final answer); uninterrupted turns and user turns omit the field.
func TestParseTranscriptParts(t *testing.T) {
	msgs, err := ParseTranscript(writeTranscript(t, fixture))
	if err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}
	interrupted := msgs[1] // "Hi! How can I help?" + tool result + "Second part."
	if len(interrupted.Parts) != 2 {
		t.Fatalf("interrupted turn: got parts %q, want 2 of them", interrupted.Parts)
	}
	if interrupted.Parts[0] != "Hi! How can I help?" || interrupted.Parts[1] != "Second part." {
		t.Errorf("interrupted turn: wrong parts %q", interrupted.Parts)
	}
	if msgs[0].Parts != nil || msgs[2].Parts != nil || msgs[3].Parts != nil {
		t.Errorf("single-part turns must omit Parts, got %+v", msgs)
	}
}

// Adjacent assistant text lines with nothing between them are one continuous
// piece of writing, not a working note plus an answer.
func TestParseTranscriptAdjacentTextIsOnePart(t *testing.T) {
	transcript := `{"type":"user","message":{"role":"user","content":"Hi."}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"First chunk."}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Same breath."}]}}
`
	msgs, err := ParseTranscript(writeTranscript(t, transcript))
	if err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(msgs), msgs)
	}
	if msgs[1].Parts != nil {
		t.Errorf("adjacent text chunks should stay one part, got %q", msgs[1].Parts)
	}
	if msgs[1].Text != "First chunk.\n\nSame breath." {
		t.Errorf("merged text wrong: %q", msgs[1].Text)
	}
}

func TestParseTranscriptMissingFile(t *testing.T) {
	if _, err := ParseTranscript(filepath.Join(t.TempDir(), "nope.jsonl")); err == nil {
		t.Fatal("expected an error for a missing transcript")
	}
}

func TestMungeProjectPath(t *testing.T) {
	got := MungeProjectPath("/home/a b/c.d_e")
	want := "-home-a-b-c-d-e"
	if got != want {
		t.Fatalf("MungeProjectPath = %q, want %q", got, want)
	}
}

func TestFindLatestTranscriptIn(t *testing.T) {
	root := t.TempDir()
	projectDir := "/home/user/my.project"
	dir := filepath.Join(root, MungeProjectPath(projectDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	older := filepath.Join(dir, "older.jsonl")
	newer := filepath.Join(dir, "newer.jsonl")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}

	got, err := findLatestTranscriptIn(root, projectDir)
	if err != nil {
		t.Fatalf("findLatestTranscriptIn: %v", err)
	}
	if got != newer {
		t.Fatalf("got %q, want %q", got, newer)
	}
}

func TestFindLatestTranscriptSlashOnlyFallback(t *testing.T) {
	root := t.TempDir()
	projectDir := "/home/user/under_score"
	// Only the older munging style (slashes replaced, underscore kept) exists.
	dir := filepath.Join(root, "-home-user-under_score")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(session, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findLatestTranscriptIn(root, projectDir)
	if err != nil {
		t.Fatalf("findLatestTranscriptIn: %v", err)
	}
	if got != session {
		t.Fatalf("got %q, want %q", got, session)
	}
}

func TestFindLatestTranscriptNoSessions(t *testing.T) {
	root := t.TempDir()
	projectDir := "/home/user/empty"
	if err := os.MkdirAll(filepath.Join(root, MungeProjectPath(projectDir)), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := findLatestTranscriptIn(root, projectDir); err == nil {
		t.Fatal("expected an error when the project has no transcripts")
	}
}

func TestParseTranscriptSkipsIDEAndCompactNoise(t *testing.T) {
	content := `{"type":"user","message":{"role":"user","content":"<ide_opened_file>The user opened foo.go</ide_opened_file>"}}
{"type":"user","message":{"role":"user","content":"<ide_selection>some selected code</ide_selection>"}}
{"type":"user","isCompactSummary":true,"message":{"role":"user","content":"This session is being continued from a previous conversation..."}}
{"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>abc123</task-id>\n<status>completed</status>\n</task-notification>"}}
{"type":"user","message":{"role":"user","content":"<local-command-stderr>Error: something broke</local-command-stderr>"}}
{"type":"user","message":{"role":"user","content":"[Request interrupted by user]"}}
{"type":"user","message":{"role":"user","content":"A real question."}}
`
	msgs, err := ParseTranscript(writeTranscript(t, content))
	if err != nil {
		t.Fatalf("ParseTranscript: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "A real question." {
		t.Fatalf("expected only the real question, got %+v", msgs)
	}
}

func TestSessionMetaTitleAndCwd(t *testing.T) {
	content := `{"type":"queue-operation","operation":"enqueue","cwd":"/home/user/projects/demo"}
{"type":"ai-title","aiTitle":"First title","sessionId":"abc"}
{"type":"user","message":{"role":"user","content":"Hello."},"cwd":"/home/user/projects/demo"}
{"type":"ai-title","aiTitle":"Refined title","sessionId":"abc"}
`
	path := writeTranscript(t, content)
	title, cwd := SessionMeta(path)
	if title != "Refined title" {
		t.Errorf("title = %q, want the LAST ai-title", title)
	}
	if cwd != "/home/user/projects/demo" {
		t.Errorf("cwd = %q, want /home/user/projects/demo", cwd)
	}
}

func TestSessionMetaFallsBackToFirstPrompt(t *testing.T) {
	content := `{"type":"mode","mode":"normal"}
{"type":"user","message":{"role":"user","content":"<command-name>/tts</command-name>"}}
{"type":"user","message":{"role":"user","content":"Please fix the flaky test in the reader package."}}
`
	title, _ := SessionMeta(writeTranscript(t, content))
	if title != "Please fix the flaky test in the reader package." {
		t.Errorf("title = %q, want the first real prompt", title)
	}
}

func TestSessionMetaCacheRefreshesOnChange(t *testing.T) {
	path := writeTranscript(t, `{"type":"ai-title","aiTitle":"Old","sessionId":"x"}`+"\n")
	if title, _ := SessionMeta(path); title != "Old" {
		t.Fatalf("title = %q, want Old", title)
	}
	newer := `{"type":"ai-title","aiTitle":"Old","sessionId":"x"}
{"type":"ai-title","aiTitle":"New","sessionId":"x"}
`
	if err := os.WriteFile(path, []byte(newer), 0o644); err != nil {
		t.Fatal(err)
	}
	// The mtime may not tick between the two writes on coarse filesystems,
	// but the size did, and the cache keys on both.
	if title, _ := SessionMeta(path); title != "New" {
		t.Fatalf("title after change = %q, want New", title)
	}
}

func TestListProjectsIn(t *testing.T) {
	root := t.TempDir()
	projDir := filepath.Join(root, "-home-user-projects-demo")
	if err := os.MkdirAll(filepath.Join(projDir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	older := filepath.Join(projDir, "aaa.jsonl")
	newer := filepath.Join(projDir, "bbb.jsonl")
	olderContent := `{"type":"user","message":{"role":"user","content":"Old session prompt."},"cwd":"/home/user/projects/demo"}` + "\n"
	newerContent := `{"type":"ai-title","aiTitle":"Newer session","sessionId":"bbb"}
{"type":"user","message":{"role":"user","content":"hi"},"cwd":"/home/user/projects/demo"}
`
	if err := os.WriteFile(older, []byte(olderContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte(newerContent), 0o644); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-1 * time.Hour)
	if err := os.Chtimes(older, past, past); err != nil {
		t.Fatal(err)
	}

	projects, err := listProjectsIn(root)
	if err != nil {
		t.Fatalf("listProjectsIn: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1: %+v", len(projects), projects)
	}
	p := projects[0]
	if p.Name != "demo" || p.Dir != "/home/user/projects/demo" {
		t.Errorf("project name/dir = %q/%q, want demo//home/user/projects/demo", p.Name, p.Dir)
	}
	if len(p.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2", len(p.Sessions))
	}
	if p.Sessions[0].ID != "bbb" || p.Sessions[1].ID != "aaa" {
		t.Errorf("sessions not newest-first: %+v", p.Sessions)
	}
	if p.Sessions[0].Title != "Newer session" {
		t.Errorf("newest session title = %q", p.Sessions[0].Title)
	}
	if p.Sessions[1].Title != "Old session prompt." {
		t.Errorf("older session title = %q (want the first-prompt fallback)", p.Sessions[1].Title)
	}
}

func TestFindTranscriptByIDIn(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "xyz.jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findTranscriptByIDIn(root, "xyz")
	if err != nil {
		t.Fatalf("findTranscriptByIDIn: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := findTranscriptByIDIn(root, "missing"); err == nil {
		t.Fatal("expected an error for an unknown id")
	}
}
