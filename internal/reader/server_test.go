package reader

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

// fakeSynth is a Synthesizer that returns fixed bytes without any network.
type fakeSynth struct {
	audio []byte
	fail  bool
}

func (f *fakeSynth) Name() string         { return "fake" }
func (f *fakeSynth) DefaultVoice() string { return "test-voice" }
func (f *fakeSynth) Voices() []string     { return []string{"test-voice", "other"} }
func (f *fakeSynth) IsValidVoice(v string) bool {
	return v == "test-voice" || v == "other"
}
func (f *fakeSynth) Synthesize(text, voice string) ([]byte, error) {
	if f.fail {
		return nil, errors.New("synthesis exploded")
	}
	return f.audio, nil
}

const miniTranscript = `{"type":"user","message":{"role":"user","content":"Hello."},"timestamp":"2026-07-25T10:00:00Z"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Hi there."}]},"timestamp":"2026-07-25T10:00:02Z"}
`

func newTestReader(t *testing.T, synth tts.Synthesizer) (*httptest.Server, string) {
	t.Helper()
	return newTestReaderWithRoot(t, synth, "")
}

func newTestReaderWithRoot(t *testing.T, synth tts.Synthesizer, projectsRoot string) (*httptest.Server, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(miniTranscript), 0o644); err != nil {
		t.Fatal(err)
	}
	if projectsRoot == "" {
		// Point session-id lookups at an empty root so tests never touch the
		// real ~/.claude/projects.
		projectsRoot = t.TempDir()
	}
	s := New(Options{
		Transcript:      path,
		Providers:       map[string]tts.Synthesizer{"fake": synth},
		DefaultProvider: "fake",
		Speed:           1.0,
		ProjectsRoot:    projectsRoot,
	})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, path
}

func getJSON(t *testing.T, url string, out any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status %d: %s", url, resp.StatusCode, body)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func TestMessagesEndpoint(t *testing.T) {
	ts, path := newTestReader(t, &fakeSynth{audio: []byte("MP3")})
	var got struct {
		Transcript string    `json:"transcript"`
		Messages   []Message `json:"messages"`
	}
	getJSON(t, ts.URL+"/api/messages", &got)
	if got.Transcript != path {
		t.Errorf("transcript = %q, want %q", got.Transcript, path)
	}
	if len(got.Messages) != 2 || got.Messages[0].Text != "Hello." || got.Messages[1].Text != "Hi there." {
		t.Fatalf("unexpected messages: %+v", got.Messages)
	}
}

func TestConfigEndpoint(t *testing.T) {
	ts, _ := newTestReader(t, &fakeSynth{audio: []byte("MP3")})
	var got struct {
		DefaultProvider string                    `json:"default_provider"`
		Providers       map[string]providerConfig `json:"providers"`
		Speed           float64                   `json:"speed"`
	}
	getJSON(t, ts.URL+"/api/config", &got)
	if got.DefaultProvider != "fake" {
		t.Errorf("default_provider = %q", got.DefaultProvider)
	}
	if pc, ok := got.Providers["fake"]; !ok || pc.DefaultVoice != "test-voice" || len(pc.Voices) != 2 {
		t.Errorf("unexpected provider config: %+v", got.Providers)
	}
	if got.Speed != 1.0 {
		t.Errorf("speed = %v, want 1.0", got.Speed)
	}
}

func postTTS(t *testing.T, url string, body map[string]string) *http.Response {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(url+"/api/tts", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestTTSEndpoint(t *testing.T) {
	ts, _ := newTestReader(t, &fakeSynth{audio: []byte("MP3-BYTES")})

	resp := postTTS(t, ts.URL, map[string]string{"text": "Hello world", "voice": "test-voice"})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "MP3-BYTES" {
		t.Errorf("body = %q", body)
	}

	if resp := postTTS(t, ts.URL, map[string]string{"text": ""}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty text: status %d, want 400", resp.StatusCode)
	}
	if resp := postTTS(t, ts.URL, map[string]string{"text": "hi", "provider": "nope"}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown provider: status %d, want 400", resp.StatusCode)
	}
	if resp := postTTS(t, ts.URL, map[string]string{"text": "hi", "voice": "bogus"}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid voice: status %d, want 400", resp.StatusCode)
	}
	if resp := postTTS(t, ts.URL, map[string]string{"text": strings.Repeat("a", maxTTSChars+1)}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("oversized text: status %d, want 400", resp.StatusCode)
	}
}

func TestTTSEndpointSynthesisFailure(t *testing.T) {
	ts, _ := newTestReader(t, &fakeSynth{fail: true})
	resp := postTTS(t, ts.URL, map[string]string{"text": "hi"})
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", resp.StatusCode)
	}
}

func TestGuardRejectsForeignOriginAndHost(t *testing.T) {
	ts, _ := newTestReader(t, &fakeSynth{audio: []byte("MP3")})

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/messages", nil)
	req.Header.Set("Origin", "https://evil.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign origin: status %d, want 403", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/messages", nil)
	req.Host = "evil.example" // DNS-rebinding shape: loopback IP, foreign Host
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("foreign host: status %d, want 403", resp.StatusCode)
	}
}

func TestHealthAndLoad(t *testing.T) {
	ts, path := newTestReader(t, &fakeSynth{audio: []byte("MP3")})

	var health struct {
		App      string `json:"app"`
		Sessions int    `json:"sessions"`
	}
	getJSON(t, ts.URL+"/api/health", &health)
	if health.App != healthApp {
		t.Fatalf("app = %q, want %q", health.App, healthApp)
	}
	if health.Sessions != 1 {
		t.Errorf("sessions = %d, want 1", health.Sessions)
	}

	other := filepath.Join(t.TempDir(), "other.jsonl")
	otherContent := `{"type":"user","message":{"role":"user","content":"Different session."}}` + "\n"
	if err := os.WriteFile(other, []byte(otherContent), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"path": other})
	resp, err := http.Post(ts.URL+"/api/load", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var loaded struct {
		OK      bool   `json:"ok"`
		Session string `json:"session"`
		URL     string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&loaded); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("load: status %d", resp.StatusCode)
	}
	if !loaded.OK || loaded.Session != "other" || loaded.URL != "/?s=other" {
		t.Fatalf("load response = %+v, want ok/other//?s=other", loaded)
	}

	// The new session is readable through its own URL, and the first session
	// keeps working through its URL — loading no longer repoints every page.
	var got struct {
		Transcript string    `json:"transcript"`
		Messages   []Message `json:"messages"`
	}
	getJSON(t, ts.URL+"/api/messages?s=other", &got)
	if got.Transcript != other {
		t.Errorf("transcript for s=other = %q, want %q", got.Transcript, other)
	}
	if len(got.Messages) != 1 || got.Messages[0].Text != "Different session." {
		t.Errorf("messages for s=other: %+v", got.Messages)
	}
	getJSON(t, ts.URL+"/api/messages?s=session", &got)
	if got.Transcript != path {
		t.Errorf("transcript for s=session = %q, want %q", got.Transcript, path)
	}

	body, _ = json.Marshal(map[string]string{"path": filepath.Join(t.TempDir(), "missing.jsonl")})
	resp, err = http.Post(ts.URL+"/api/load", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("load missing file: status %d, want 400", resp.StatusCode)
	}
}

func TestMessagesCarryProjectAndTitle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "abc.jsonl")
	content := `{"type":"ai-title","aiTitle":"Fix the widget","sessionId":"abc"}
{"type":"user","message":{"role":"user","content":"Hello."},"cwd":"/home/user/widgets"}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{
		Transcript:      path,
		Providers:       map[string]tts.Synthesizer{"fake": &fakeSynth{audio: []byte("MP3")}},
		DefaultProvider: "fake",
		Speed:           1.0,
	})
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)

	var got struct {
		Session string `json:"session"`
		Project string `json:"project"`
		Title   string `json:"title"`
	}
	getJSON(t, ts.URL+"/api/messages?s=abc", &got)
	if got.Session != "abc" || got.Project != "widgets" || got.Title != "Fix the widget" {
		t.Fatalf("session/project/title = %q/%q/%q", got.Session, got.Project, got.Title)
	}
}

func TestSessionResolvedFromProjectsRoot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-some-project")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "old-session.jsonl")
	if err := os.WriteFile(old, []byte(`{"type":"user","message":{"role":"user","content":"From the archive."}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, _ := newTestReaderWithRoot(t, &fakeSynth{audio: []byte("MP3")}, root)

	// A session id the server never saw is found on disk and served.
	var got struct {
		Messages []Message `json:"messages"`
	}
	getJSON(t, ts.URL+"/api/messages?s=old-session", &got)
	if len(got.Messages) != 1 || got.Messages[0].Text != "From the archive." {
		t.Fatalf("lazy-resolved session: %+v", got.Messages)
	}

	// An unknown or malformed id is a clean 404, never a file probe outside
	// the projects root.
	for _, bad := range []string{"nope", "..%2F..%2Fetc%2Fpasswd", "a%2Fb"} {
		resp, err := http.Get(ts.URL + "/api/messages?s=" + bad)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("s=%s: status %d, want 404", bad, resp.StatusCode)
		}
	}

	// Lazy resolution is read-only with respect to the fallback: a request
	// without ?s= still resolves to the session the server started with.
	var fallback struct {
		Messages []Message `json:"messages"`
	}
	getJSON(t, ts.URL+"/api/messages", &fallback)
	if len(fallback.Messages) == 0 || fallback.Messages[0].Text != "Hello." {
		t.Errorf("fallback session flipped after a lazy lookup: %+v", fallback.Messages)
	}
}

func TestLoadRepointsCollidingBasename(t *testing.T) {
	ts, _ := newTestReader(t, &fakeSynth{audio: []byte("MP3")})

	// A different file with the SAME basename as the running session: loading
	// it must actually serve it, not silently keep the old file.
	other := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(other, []byte(`{"type":"user","message":{"role":"user","content":"The copied transcript."}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"path": other})
	resp, err := http.Post(ts.URL+"/api/load", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	var got struct {
		Transcript string    `json:"transcript"`
		Messages   []Message `json:"messages"`
	}
	getJSON(t, ts.URL+"/api/messages?s=session", &got)
	if got.Transcript != other {
		t.Errorf("transcript = %q, want the newly loaded %q", got.Transcript, other)
	}
	if len(got.Messages) != 1 || got.Messages[0].Text != "The copied transcript." {
		t.Errorf("messages = %+v", got.Messages)
	}
}

func TestSessionsEndpoint(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "-home-user-demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sess := filepath.Join(dir, "s1.jsonl")
	content := `{"type":"ai-title","aiTitle":"Demo work","sessionId":"s1"}
{"type":"user","message":{"role":"user","content":"hi"},"cwd":"/home/user/demo"}
`
	if err := os.WriteFile(sess, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ts, _ := newTestReaderWithRoot(t, &fakeSynth{audio: []byte("MP3")}, root)

	var got struct {
		Projects []ProjectInfo `json:"projects"`
	}
	getJSON(t, ts.URL+"/api/sessions", &got)
	if len(got.Projects) != 1 {
		t.Fatalf("projects: %+v", got.Projects)
	}
	p := got.Projects[0]
	if p.Name != "demo" || len(p.Sessions) != 1 || p.Sessions[0].ID != "s1" || p.Sessions[0].Title != "Demo work" {
		t.Fatalf("unexpected listing: %+v", p)
	}
}

func TestStopEndpoint(t *testing.T) {
	ts, _ := newTestReader(t, &fakeSynth{audio: []byte("MP3")})
	resp, err := http.Post(ts.URL+"/api/stop", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop: status %d, want 200", resp.StatusCode)
	}
	getResp, err := http.Get(ts.URL + "/api/stop")
	if err != nil {
		t.Fatal(err)
	}
	getResp.Body.Close()
	if getResp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("stop via GET: status %d, want 405", getResp.StatusCode)
	}
}

func TestIndexServesPageWithCSP(t *testing.T) {
	ts, _ := newTestReader(t, &fakeSynth{audio: []byte("MP3")})
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Errorf("missing CSP header, got %q", csp)
	}
	page, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(page, []byte("Read along")) {
		t.Error("index page does not look like the read-along view")
	}
}
