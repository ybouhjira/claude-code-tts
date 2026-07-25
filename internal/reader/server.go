package reader

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ybouhjira/claude-code-tts/internal/logging"
	"github.com/ybouhjira/claude-code-tts/internal/tts"
)

//go:embed assets
var assetsFS embed.FS

// healthApp identifies this server in /api/health responses, so a second
// launch can tell "our reader already owns this port" apart from "some other
// program owns this port".
const healthApp = "claude-code-tts-reader"

// DefaultPort is where the reader listens unless TTS_READER_PORT or the -port
// flag says otherwise. If the port is taken by a foreign program, the reader
// falls back to a random free port.
const DefaultPort = 8898

// maxTTSChars bounds a single synthesis request. The page sends one sentence
// at a time, so real requests stay far below this; the bound matches the
// speak tool's limit.
const maxTTSChars = 4096

// Options configures a reader server.
type Options struct {
	Transcript      string                     // path to the initial session transcript (JSONL)
	Port            int                        // 0 means "any free port"
	Providers       map[string]tts.Synthesizer // nil means all built-in providers
	DefaultProvider string                     // "" means tts.DefaultProviderName()
	Speed           float64                    // initial playback speed; 0 means TTS_SPEED or 1.0
	ProjectsRoot    string                     // "" means ~/.claude/projects; tests override it
}

// session is one transcript the server follows. Each open page binds to one
// session through the ?s=<id> query parameter in its URL, so several tabs can
// show several sessions at the same time.
type session struct {
	id       string
	path     string
	lastSize int64     // watcher cursor: size at the last change event
	lastMod  time.Time // watcher cursor: mtime at the last change event
}

// Server is the local HTTP server behind the read-along page. It binds to the
// loopback interface only: the page shows conversation content and spends TTS
// API credits, so it must never be reachable from the network.
type Server struct {
	providers       map[string]tts.Synthesizer
	defaultProvider string
	speed           float64
	port            int
	projectsRoot    string

	mu       sync.Mutex // guards the registry and every session's watcher cursor
	sessions map[string]*session
	current  string // fallback session id for requests that name none

	hub      *sseHub
	httpSrv  *http.Server
	listener net.Listener
	stop     chan struct{}
	stopOnce sync.Once
}

// New creates a reader server. Zero-value options fall back to the same
// defaults the speak tool uses, so the read-along voice matches what the user
// already hears.
func New(opts Options) *Server {
	providers := opts.Providers
	if providers == nil {
		providers = tts.NewProviders()
	}
	defaultProvider := opts.DefaultProvider
	if defaultProvider == "" {
		defaultProvider = tts.DefaultProviderName()
	}
	speed := opts.Speed
	if speed == 0 {
		speed = speedFromEnv()
	}
	projectsRoot := opts.ProjectsRoot
	if projectsRoot == "" {
		if home, err := os.UserHomeDir(); err == nil {
			projectsRoot = filepath.Join(home, ".claude", "projects")
		}
	}
	s := &Server{
		providers:       providers,
		defaultProvider: defaultProvider,
		speed:           speed,
		port:            opts.Port,
		projectsRoot:    projectsRoot,
		sessions:        make(map[string]*session),
		hub:             newSSEHub(),
		stop:            make(chan struct{}),
	}
	if opts.Transcript != "" {
		s.register(opts.Transcript)
		s.setCurrent(sessionIDFor(opts.Transcript))
	}
	return s
}

// sessionIDFor derives a session id from a transcript path: the filename
// stem, which is the Claude Code session id. A document (any non-.jsonl
// file) instead gets a path-derived "doc-…" id, so several open documents
// never collide with each other or with session UUIDs.
func sessionIDFor(path string) string {
	if IsDocumentPath(path) {
		return documentSessionID(path)
	}
	return strings.TrimSuffix(filepath.Base(path), ".jsonl")
}

// sessionIDRe is the shape of an acceptable session id in a URL. Ids are
// UUIDs in practice; anything that could escape the projects directory when
// joined into a path (separators, "..", empty) is rejected by this pattern.
var sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// register adds a transcript to the registry (idempotently). It does NOT
// change the fallback session — only startup and /api/load mean "this is the
// session now"; a read-only request that happens to resolve a new id must
// not repoint what session-less requests see. Registering an id that already
// exists under a DIFFERENT path repoints that session to the new file (the
// only ways here are explicit loads, where the caller means "serve THIS
// file" — think a copied transcript that kept its UUID basename) and resets
// its cursor so following pages refetch.
func (s *Server) register(path string) *session {
	id := sessionIDFor(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[id]; ok {
		if sess.path != path {
			sess.path = path
			sess.lastSize, sess.lastMod = -1, time.Time{}
		}
		return sess
	}
	sess := &session{id: id, path: path}
	if fi, err := os.Stat(path); err == nil {
		sess.lastSize, sess.lastMod = fi.Size(), fi.ModTime()
	}
	s.sessions[id] = sess
	return sess
}

// setCurrent repoints the fallback session for requests that name none.
func (s *Server) setCurrent(id string) {
	s.mu.Lock()
	s.current = id
	s.mu.Unlock()
}

// sessionFor resolves the session a request is about: the ?s=<id> parameter,
// or the fallback session when the request names none. An id the server has
// not seen yet (a navigator link to an old session) is looked up on disk and
// registered on the spot.
func (s *Server) sessionFor(r *http.Request) (*session, error) {
	id := r.URL.Query().Get("s")
	s.mu.Lock()
	if id == "" {
		sess, ok := s.sessions[s.current]
		s.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("no session loaded")
		}
		return sess, nil
	}
	if sess, ok := s.sessions[id]; ok {
		s.mu.Unlock()
		return sess, nil
	}
	s.mu.Unlock()
	if !sessionIDRe.MatchString(id) {
		return nil, fmt.Errorf("invalid session id")
	}
	// Document ids only live in this server's memory — there is no on-disk
	// registry to look them up in after a restart.
	if strings.HasPrefix(id, "doc-") {
		return nil, fmt.Errorf("this document is no longer registered; run `tts-ctl read <file>` again")
	}
	path, err := findTranscriptByIDIn(s.projectsRoot, id)
	if err != nil {
		return nil, err
	}
	return s.register(path), nil
}

// pagePath returns the URL path (with query) of a session's page.
func pagePath(id string) string {
	if id == "" {
		return "/"
	}
	return "/?s=" + url.QueryEscape(id)
}

// speedFromEnv reads TTS_SPEED (the same knob the audio player honors) and
// clamps it to the 0.5–2.0 range the page offers.
func speedFromEnv() float64 {
	v := strings.TrimSpace(os.Getenv("TTS_SPEED"))
	if v == "" {
		return 1.0
	}
	speed, err := strconv.ParseFloat(v, 64)
	if err != nil || speed < 0.5 || speed > 2.0 {
		return 1.0
	}
	return speed
}

// Start binds the loopback listener and begins serving in the background.
// It returns the page URL.
func (s *Server) Start() (string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", s.port))
	if err != nil && s.port != 0 {
		// The launcher probes for an existing reader before starting a new
		// one, so a busy port here belongs to some other program.
		logging.Warn("reader: port %d is busy, using a random free port instead: %v", s.port, err)
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		return "", fmt.Errorf("cannot listen on loopback: %w", err)
	}
	s.listener = ln
	s.httpSrv = &http.Server{Handler: s.Handler()}
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logging.Error("reader: http server stopped: %v", err)
		}
	}()
	go s.watch()
	s.mu.Lock()
	id := s.current
	s.mu.Unlock()
	pageURL := fmt.Sprintf("http://127.0.0.1:%d%s", ln.Addr().(*net.TCPAddr).Port, pagePath(id))
	logging.Info("reader: read-along page at %s", pageURL)
	return pageURL, nil
}

// Shutdown stops the HTTP server and the transcript watcher.
func (s *Server) Shutdown() {
	s.stopOnce.Do(func() { close(s.stop) })
	if s.httpSrv != nil {
		s.httpSrv.Close()
	}
}

// Handler returns the complete HTTP handler, wrapped in the loopback-only
// checks. Exported so tests can drive the server through httptest.
func (s *Server) Handler() http.Handler {
	assets, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		panic(err) // the embedded tree is fixed at compile time
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("/api/health", s.handleHealth)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/messages", s.handleMessages)
	mux.HandleFunc("/api/sessions", s.handleSessions)
	mux.HandleFunc("/api/tts", s.handleTTS)
	mux.HandleFunc("/api/load", s.handleLoad)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/events", s.handleEvents)
	return s.guard(mux)
}

// guard rejects requests that did not come from this machine's own browser.
// Binding to loopback alone does not stop a malicious web page from making
// the browser send requests to 127.0.0.1 (DNS rebinding, or plain cross-site
// POSTs that would burn TTS credits). Requiring a loopback Host header
// defeats rebinding; requiring a loopback Origin, whenever the browser sends
// one, defeats cross-site calls.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isLoopbackHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "null" {
			u, err := url.Parse(origin)
			if err != nil || !isLoopbackHost(u.Host) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// isLoopbackHost reports whether a Host or Origin host (optionally with a
// port) names this machine's loopback interface.
func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	page, err := assetsFS.ReadFile("assets/index.html")
	if err != nil {
		http.Error(w, "page missing from binary", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Everything the page needs is served from this origin; audio clips are
	// blob: URLs created from fetched MP3 bytes. frame-ancestors limits who
	// may EMBED the page: the VS Code surfaces that wrap it in an iframe
	// (the bridge extension's tab and Simple Browser run on vscode-webview:
	// or vscode-file: origins on desktop, vscode-cdn.net on web) and the
	// page's own origin. Without it, any public website could frame the
	// loopback address and listen for the title postMessage.
	w.Header().Set("Content-Security-Policy",
		"default-src 'self'; media-src 'self' blob:; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; "+
			"frame-ancestors 'self' vscode-webview: vscode-file: https://*.vscode-cdn.net")
	if _, err := w.Write(page); err != nil {
		logging.Debug("reader: writing index: %v", err)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	transcript := ""
	if sess, ok := s.sessions[s.current]; ok {
		transcript = sess.path
	}
	count := len(s.sessions)
	s.mu.Unlock()
	writeJSON(w, map[string]any{"app": healthApp, "transcript": transcript, "sessions": count})
}

// providerConfig is what the page needs to offer a provider in its pickers.
type providerConfig struct {
	Voices       []string `json:"voices"`
	DefaultVoice string   `json:"default_voice"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	provs := make(map[string]providerConfig, len(s.providers))
	for name, synth := range s.providers {
		provs[name] = providerConfig{Voices: synth.Voices(), DefaultVoice: synth.DefaultVoice()}
	}
	// An on-the-fly voice override (tts-ctl voice NAME) applies to the default
	// provider, mirroring how the speak path treats TTS_VOICE.
	if v := os.Getenv("TTS_VOICE"); v != "" {
		if synth, ok := s.providers[s.defaultProvider]; ok && synth.IsValidVoice(v) {
			pc := provs[s.defaultProvider]
			pc.DefaultVoice = v
			provs[s.defaultProvider] = pc
		}
	}
	out := map[string]any{
		"default_provider": s.defaultProvider,
		"providers":        provs,
		"speed":            s.speed,
	}
	// The transcript fields are decoration; a request with an unknown session
	// id still deserves the provider config.
	if sess, err := s.sessionFor(r); err == nil {
		out["transcript"] = sess.path
		out["session"] = sess.id
	}
	writeJSON(w, out)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	sess, err := s.sessionFor(r)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}
	// A document (a Markdown or plain-text file) goes through the same page
	// as a session; only the parsing and the labels differ.
	isDoc := IsDocumentPath(sess.path)
	var msgs []Message
	if isDoc {
		msgs, err = ParseDocument(sess.path)
	} else {
		msgs, err = ParseTranscript(sess.path)
	}
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var version int64
	if fi, err := os.Stat(sess.path); err == nil {
		version = fi.ModTime().UnixNano()
	}
	// Title and project name let the page label itself (tab title and header)
	// as "<project> — <session title>". A document is labeled by its parent
	// directory and its first heading (or filename).
	var title, project string
	if isDoc {
		title = DocumentTitle(sess.path)
		project = filepath.Base(filepath.Dir(sess.path))
	} else {
		var cwd string
		title, cwd = SessionMeta(sess.path)
		if cwd != "" {
			project = filepath.Base(cwd)
		}
	}
	writeJSON(w, map[string]any{
		"session":    sess.id,
		"transcript": sess.path,
		"version":    version,
		"messages":   msgs,
		"project":    project,
		"title":      title,
	})
}

// handleSessions lists every project and its sessions for the navigator page
// (the page served when the URL names no session).
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	projects, err := listProjectsIn(s.projectsRoot)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]any{"projects": projects})
}

func (s *Server) handleTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Text     string `json:"text"`
		Provider string `json:"provider"`
		Voice    string `json:"voice"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		writeJSONError(w, http.StatusBadRequest, "text is required")
		return
	}
	if len(text) > maxTTSChars {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("text exceeds %d characters", maxTTSChars))
		return
	}
	provider := req.Provider
	if provider == "" {
		provider = s.defaultProvider
	}
	synth, ok := s.providers[provider]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("unknown provider %q", provider))
		return
	}
	voice := req.Voice
	if voice == "" {
		voice = synth.DefaultVoice()
	}
	if !synth.IsValidVoice(voice) {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid voice %q for provider %s", voice, provider))
		return
	}
	audio, err := synth.Synthesize(text, voice)
	if err != nil {
		logging.Error("reader: synthesis failed (provider=%s, voice=%s): %v", provider, voice, err)
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Cache-Control", "no-store")
	if _, err := w.Write(audio); err != nil {
		logging.Debug("reader: writing audio: %v", err)
	}
}

func (s *Server) handleLoad(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		writeJSONError(w, http.StatusBadRequest, "path is required")
		return
	}
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("not a readable file: %s", path))
		return
	}
	sess := s.register(path)
	s.setCurrent(sess.id)
	s.mu.Lock()
	// Force the watcher to report this session changed on its next tick, so a
	// page already showing it refetches even if the file itself is untouched.
	sess.lastSize, sess.lastMod = -1, time.Time{}
	s.mu.Unlock()
	logging.Info("reader: loaded session %s (%s)", sess.id, path)
	s.hub.broadcast(sseEvent{Name: "load", Data: sseJSON(map[string]any{"session": sess.id, "path": path})})
	writeJSON(w, map[string]any{"ok": true, "session": sess.id, "url": pagePath(sess.id)})
}

// handleStop tells every connected page to stop its playback. The page's
// audio lives in the browser, out of reach of any process kill, so this SSE
// relay is the only way `tts-ctl stop` can silence a read-along tab.
func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}
	s.hub.broadcast(sseEvent{Name: "stop", Data: "now"})
	writeJSON(w, map[string]any{"ok": true})
}

// handleEvents is the server-sent-events stream. The page listens here and
// refetches messages whenever the transcript changes, which is what makes the
// view follow the live session.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)
	fmt.Fprint(w, "event: hello\ndata: ok\n\n")
	fl.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stop:
			return
		case ev := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Name, ev.Data)
			fl.Flush()
		case <-ping.C:
			// Comment line: keeps proxies and the browser from timing out.
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// watch polls every registered session's transcript once a second and tells
// connected pages which one changed. Polling is deliberate: it needs no extra
// dependency, and a one-second delay is invisible next to speech-synthesis
// time. Events carry the session id so each page reacts only to its own.
func (s *Server) watch() {
	t := time.NewTicker(1 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
		}
		// Snapshot under the lock, stat outside it: disk I/O must not block
		// request handlers.
		s.mu.Lock()
		snapshot := make([]session, 0, len(s.sessions))
		for _, sess := range s.sessions {
			snapshot = append(snapshot, *sess)
		}
		s.mu.Unlock()
		for _, c := range snapshot {
			fi, err := os.Stat(c.path)
			if err != nil {
				continue // deleted or unreadable; keep quiet, it may come back
			}
			if fi.Size() == c.lastSize && fi.ModTime().Equal(c.lastMod) {
				continue
			}
			s.mu.Lock()
			if sess, ok := s.sessions[c.id]; ok {
				sess.lastSize, sess.lastMod = fi.Size(), fi.ModTime()
			}
			s.mu.Unlock()
			s.hub.broadcast(sseEvent{Name: "change", Data: sseJSON(map[string]any{
				"session": c.id,
				"version": fi.ModTime().UnixNano(),
			})})
		}
	}
}

// sseJSON renders an event payload as a single-line JSON string.
func sseJSON(v map[string]any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// sseEvent is one named server-sent event.
type sseEvent struct {
	Name string
	Data string
}

// sseHub fans events out to every connected page.
type sseHub struct {
	mu   sync.Mutex
	subs map[chan sseEvent]struct{}
}

func newSSEHub() *sseHub {
	return &sseHub{subs: make(map[chan sseEvent]struct{})}
}

func (h *sseHub) subscribe() chan sseEvent {
	ch := make(chan sseEvent, 8)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *sseHub) unsubscribe(ch chan sseEvent) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// broadcast never blocks: a page that has fallen behind misses an event and
// simply catches up on the next one it does receive.
func (h *sseHub) broadcast(ev sseEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logging.Debug("reader: writing JSON: %v", err)
	}
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		logging.Debug("reader: writing JSON error: %v", err)
	}
}

// ProbeInstance reports whether a reader from this plugin is already
// listening on the given port.
func ProbeInstance(port int) bool {
	client := &http.Client{Timeout: 700 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/health", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var h struct {
		App string `json:"app"`
	}
	if json.NewDecoder(resp.Body).Decode(&h) != nil {
		return false
	}
	return h.App == healthApp
}

// SwitchTranscript registers a transcript with an already-running reader, so
// a second `tts-ctl read` reuses the running server instead of starting
// another. It returns the session's page URL on that server.
func SwitchTranscript(port int, path string) (string, error) {
	body, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(fmt.Sprintf("http://127.0.0.1:%d/api/load", port), "application/json", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("reader refused the transcript switch: %s", strings.TrimSpace(string(b)))
	}
	var out struct {
		URL string `json:"url"`
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	// An older reader binary answers without a url field; its bare page still
	// works, so fall back to that instead of failing.
	if json.NewDecoder(resp.Body).Decode(&out) != nil || out.URL == "" {
		return base + "/", nil
	}
	return base + out.URL, nil
}
