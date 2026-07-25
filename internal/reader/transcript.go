// Package reader serves the read-along view: a local web page that shows a
// Claude Code conversation and reads it aloud, keeping the sentence and word
// being spoken highlighted. Unlike the speak tool, the audio is played by the
// browser, not the native player, because only the page itself can keep a
// visual highlight in step with playback.
package reader

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Message is one visible chat turn extracted from a session transcript.
type Message struct {
	Role string `json:"role"` // "user" or "assistant"
	Text string `json:"text"` // Markdown-ish plain text of the turn
	// Parts splits an assistant turn's text at the places where tool activity
	// interrupted it. Every part except the last is a "working note" the model
	// wrote before running a tool; the last part is the final answer. The
	// field is only set when a turn actually has more than one part, so most
	// messages omit it.
	Parts     []string `json:"parts,omitempty"`
	Timestamp string   `json:"timestamp,omitempty"`
}

// rawEntry mirrors just the fields we need from one transcript line. The
// transcript format is internal to Claude Code and can change between
// releases, so parsing is deliberately tolerant: a line that does not look
// like a chat turn is skipped, never treated as an error.
type rawEntry struct {
	Type             string `json:"type"`
	IsMeta           bool   `json:"isMeta"`
	IsSidechain      bool   `json:"isSidechain"`
	IsCompactSummary bool   `json:"isCompactSummary"`
	Timestamp        string `json:"timestamp"`
	Message          struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

// contentBlock is one element of a structured message content array.
type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// systemReminderRe matches the <system-reminder> blocks Claude Code injects
// into user turns. They are context plumbing, not something the user typed,
// so the reader strips them out.
var systemReminderRe = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// noiseUserPrefixes mark user entries that are local-command plumbing (slash
// command echoes and their captured output), not real chat turns.
var noiseUserPrefixes = []string{
	"<command-name>",
	"<command-message>",
	"<local-command-stdout>",
	"<local-command-stderr>",
	"<local-command-caveat>",
	"<ide_opened_file>",
	"<ide_selection>",
	"<task-notification>",
	"[Request interrupted",
	"Caveat: The messages below were generated",
}

// ParseTranscript reads a Claude Code session transcript (a JSONL file) and
// returns the visible chat turns in order. Tool calls, tool results, thinking
// blocks, meta entries, and sidechain (subagent) entries are skipped. All text
// an assistant turn produces, including text between tool calls, is merged
// into a single message, because that is how the turn reads in the chat
// window. The individual chunks are additionally kept in Message.Parts so the
// page can style the working notes differently from the final answer.
func ParseTranscript(path string) ([]Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open transcript: %w", err)
	}
	defer f.Close()

	messages := []Message{}
	// sawGap remembers that at least one non-visible line (a tool call, a tool
	// result, a thinking block) sat between the previous visible text and the
	// next one. That gap is what separates a "working note" from the text that
	// follows it, so it decides whether merged assistant text opens a new part.
	sawGap := false
	// A plain Reader, not a Scanner: single transcript lines can hold huge
	// tool results, far beyond any fixed Scanner buffer.
	r := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			if msg, ok := parseLine(line); ok {
				last := len(messages) - 1
				if msg.Role == "assistant" && last >= 0 && messages[last].Role == "assistant" {
					// Continuation of the same turn (tool results in between
					// were skipped): merge instead of starting a new bubble.
					messages[last].Text += "\n\n" + msg.Text
					if sawGap {
						// Tool activity interrupted the turn here, so this
						// text starts a new part.
						messages[last].Parts = append(messages[last].Parts, msg.Text)
					} else {
						// Adjacent text chunks with nothing between them are
						// one continuous piece of writing.
						messages[last].Parts[len(messages[last].Parts)-1] += "\n\n" + msg.Text
					}
				} else {
					msg.Parts = []string{msg.Text}
					messages = append(messages, msg)
				}
				sawGap = false
			} else if strings.TrimSpace(line) != "" {
				sawGap = true
			}
		}
		if err != nil {
			// io.EOF, or a truncated tail while the session is still being
			// written; either way the turns parsed so far are what we show.
			break
		}
	}
	// A single-part turn carries no extra information in Parts; drop the field
	// so the JSON payload stays as small as before for the common case.
	for i := range messages {
		if len(messages[i].Parts) <= 1 {
			messages[i].Parts = nil
		}
	}
	return messages, nil
}

// parseLine converts one transcript line into a Message. ok is false for
// every line that is not a visible chat turn.
func parseLine(line string) (Message, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Message{}, false
	}
	var entry rawEntry
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return Message{}, false
	}
	if entry.IsMeta || entry.IsSidechain || entry.IsCompactSummary {
		// Compact summaries are the long "this session is being continued"
		// wall of text Claude Code writes into the transcript when it
		// compacts; nobody typed it, so it is not a visible turn.
		return Message{}, false
	}
	if entry.Type != "user" && entry.Type != "assistant" {
		return Message{}, false
	}

	text := extractText(entry.Message.Content)
	if entry.Type == "user" {
		text = systemReminderRe.ReplaceAllString(text, "")
		text = strings.TrimSpace(text)
		for _, p := range noiseUserPrefixes {
			if strings.HasPrefix(text, p) {
				return Message{}, false
			}
		}
	} else {
		text = strings.TrimSpace(text)
	}
	if text == "" {
		return Message{}, false
	}
	return Message{Role: entry.Type, Text: text, Timestamp: entry.Timestamp}, true
}

// extractText pulls the plain text out of a message content field, which is
// either a bare string or an array of typed blocks. Only "text" blocks count:
// tool_use, tool_result, and thinking blocks are not part of the visible chat.
func extractText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(content, &s); err == nil {
		return s
	}
	var blocks []contentBlock
	if err := json.Unmarshal(content, &blocks); err != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// nonProjectChar matches every character Claude Code replaces with a dash
// when it turns a project path into a directory name under ~/.claude/projects.
var nonProjectChar = regexp.MustCompile(`[^A-Za-z0-9-]`)

// MungeProjectPath converts an absolute project path into the directory name
// Claude Code uses for it under ~/.claude/projects.
func MungeProjectPath(projectDir string) string {
	return nonProjectChar.ReplaceAllString(projectDir, "-")
}

// FindLatestTranscript returns the most recently modified session transcript
// for the given project directory. The newest file is almost always the
// session the user is sitting in, because Claude Code appends to it on every
// turn.
func FindLatestTranscript(projectDir string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return findLatestTranscriptIn(filepath.Join(home, ".claude", "projects"), projectDir)
}

// findLatestTranscriptIn is FindLatestTranscript with the projects root made
// explicit so tests can point it at a temporary directory.
func findLatestTranscriptIn(projectsRoot, projectDir string) (string, error) {
	dir := filepath.Join(projectsRoot, MungeProjectPath(projectDir))
	if _, err := os.Stat(dir); err != nil {
		// Some Claude Code versions munge only the path separators. Try that
		// spelling before giving up.
		alt := filepath.Join(projectsRoot, strings.ReplaceAll(projectDir, string(os.PathSeparator), "-"))
		if _, altErr := os.Stat(alt); altErr == nil {
			dir = alt
		} else {
			return "", fmt.Errorf("no Claude Code session directory for %s (looked in %s)", projectDir, dir)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var newest string
	var newestMod int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if mod := info.ModTime().UnixNano(); newest == "" || mod > newestMod {
			newest = filepath.Join(dir, e.Name())
			newestMod = mod
		}
	}
	if newest == "" {
		return "", fmt.Errorf("no session transcripts (*.jsonl) in %s", dir)
	}
	return newest, nil
}

/* ---------------- session enumeration for the navigator ---------------- */

// SessionInfo describes one session transcript in the navigator listing.
type SessionInfo struct {
	ID      string `json:"id"`      // filename stem; equals the Claude Code session id
	Path    string `json:"path"`    // absolute transcript path
	Title   string `json:"title"`   // human-readable title; "" when none could be found
	Updated int64  `json:"updated"` // last write time, in Unix milliseconds
	Size    int64  `json:"size"`
}

// ProjectInfo groups the sessions that belong to one project directory.
type ProjectInfo struct {
	Name     string        `json:"name"` // last segment of the project's real path
	Dir      string        `json:"dir"`  // the project's real path, when known
	Sessions []SessionInfo `json:"sessions"`
}

// ListProjects enumerates every Claude Code project on this machine with its
// session transcripts, newest first.
func ListProjects() ([]ProjectInfo, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return listProjectsIn(filepath.Join(home, ".claude", "projects"))
}

// listProjectsIn is ListProjects with the projects root made explicit so
// tests can point it at a temporary directory.
func listProjectsIn(projectsRoot string) ([]ProjectInfo, error) {
	dirs, err := os.ReadDir(projectsRoot)
	if err != nil {
		return nil, err
	}
	var projects []ProjectInfo
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(projectsRoot, d.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var sessions []SessionInfo
		for _, e := range entries {
			// Only depth-1 *.jsonl files are sessions; subdirectories hold
			// subagent transcripts, tool results, and memory files.
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			path := filepath.Join(dir, e.Name())
			title, _ := SessionMeta(path)
			sessions = append(sessions, SessionInfo{
				ID:      strings.TrimSuffix(e.Name(), ".jsonl"),
				Path:    path,
				Title:   title,
				Updated: info.ModTime().UnixMilli(),
				Size:    info.Size(),
			})
		}
		if len(sessions) == 0 {
			continue
		}
		sort.Slice(sessions, func(i, j int) bool { return sessions[i].Updated > sessions[j].Updated })
		// The directory name is the munged project path, which is lossy ("/",
		// "_", and "." all became "-"), so the readable name comes from the
		// cwd recorded inside the newest transcript instead.
		name, projDir := d.Name(), ""
		if _, cwd := SessionMeta(sessions[0].Path); cwd != "" {
			name, projDir = filepath.Base(cwd), cwd
		}
		projects = append(projects, ProjectInfo{Name: name, Dir: projDir, Sessions: sessions})
	}
	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Sessions[0].Updated > projects[j].Sessions[0].Updated
	})
	return projects, nil
}

// findTranscriptByIDIn locates a session transcript by its id (the filename
// stem) across every project directory. The caller validates the id shape,
// so joining it into a path here is safe.
func findTranscriptByIDIn(projectsRoot, id string) (string, error) {
	dirs, err := os.ReadDir(projectsRoot)
	if err != nil {
		return "", err
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		candidate := filepath.Join(projectsRoot, d.Name(), id+".jsonl")
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no session transcript with id %s", id)
}

/* ---------------- per-transcript metadata (title + project) ---------------- */

// metaScanBytes is how much of a transcript's head and tail the metadata scan
// reads. Titles are appended as the session grows, so the last one in the
// tail is current; 64 KiB of tail finds it in practice for all but huge
// files, and those fall back to the head scan.
const metaScanBytes = 64 * 1024

// aiTitleMarker identifies the transcript lines that carry a session title.
var aiTitleMarker = []byte(`"type":"ai-title"`)

// cwdMarker identifies lines that record the working directory.
var cwdMarker = []byte(`"cwd"`)

// sessionMetaEntry caches the computed metadata for one transcript, keyed by
// the file's size and mtime so an unchanged file is never re-read.
type sessionMetaEntry struct {
	size  int64
	mod   int64
	title string
	cwd   string
}

var sessionMetaCache sync.Map // path -> sessionMetaEntry

// SessionMeta returns the human-readable title and the project working
// directory of a session transcript. Both come from bounded reads of the
// file's head and tail, never a full parse, and results are cached until the
// file changes. Either value can be "" when the transcript does not record it.
func SessionMeta(path string) (title, cwd string) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", ""
	}
	if v, ok := sessionMetaCache.Load(path); ok {
		e := v.(sessionMetaEntry)
		if e.size == fi.Size() && e.mod == fi.ModTime().UnixNano() {
			return e.title, e.cwd
		}
	}
	head := readChunk(path, 0, metaScanBytes)
	tail := head
	if fi.Size() > metaScanBytes {
		tail = readChunk(path, fi.Size()-metaScanBytes, metaScanBytes)
	}

	// The newest title wins, so scan the tail backwards first. A session
	// whose title emission stopped early still gets its first title from the
	// head; a session with no title at all falls back to its first prompt.
	title = lastAITitle(tail)
	if title == "" {
		title = firstAITitle(head)
	}
	if title == "" {
		title = firstUserPrompt(head)
	}
	cwd = firstCwd(head)

	sessionMetaCache.Store(path, sessionMetaEntry{
		size: fi.Size(), mod: fi.ModTime().UnixNano(), title: title, cwd: cwd,
	})
	return title, cwd
}

// readChunk reads up to n bytes of a file starting at offset. Errors collapse
// to an empty slice: metadata is best-effort decoration, never a failure.
func readChunk(path string, offset int64, n int) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, n)
	read, _ := f.ReadAt(buf, offset)
	return buf[:read]
}

// aiTitleLine mirrors one {"type":"ai-title",...} transcript line.
type aiTitleLine struct {
	AiTitle string `json:"aiTitle"`
}

// lastAITitle returns the newest parseable title in the chunk, searching
// backwards. A line cut off at the chunk boundary fails to parse and the
// search simply moves on to the previous occurrence.
func lastAITitle(chunk []byte) string {
	for len(chunk) > 0 {
		idx := bytes.LastIndex(chunk, aiTitleMarker)
		if idx < 0 {
			return ""
		}
		lineStart := bytes.LastIndexByte(chunk[:idx], '\n') + 1
		if t := parseAITitle(lineAt(chunk, lineStart)); t != "" {
			return t
		}
		if lineStart == 0 {
			return ""
		}
		chunk = chunk[:lineStart-1]
	}
	return ""
}

// firstAITitle returns the oldest parseable title in the chunk, searching
// forwards.
func firstAITitle(chunk []byte) string {
	offset := 0
	for offset < len(chunk) {
		idx := bytes.Index(chunk[offset:], aiTitleMarker)
		if idx < 0 {
			return ""
		}
		abs := offset + idx
		lineStart := bytes.LastIndexByte(chunk[:abs], '\n') + 1
		if t := parseAITitle(lineAt(chunk, lineStart)); t != "" {
			return t
		}
		lineEnd := bytes.IndexByte(chunk[abs:], '\n')
		if lineEnd < 0 {
			return ""
		}
		offset = abs + lineEnd + 1
	}
	return ""
}

// lineAt returns the full line that starts at the given offset.
func lineAt(chunk []byte, start int) []byte {
	rest := chunk[start:]
	if end := bytes.IndexByte(rest, '\n'); end >= 0 {
		return rest[:end]
	}
	return rest
}

func parseAITitle(line []byte) string {
	var t aiTitleLine
	if json.Unmarshal(line, &t) != nil {
		return ""
	}
	return strings.TrimSpace(t.AiTitle)
}

// firstUserPrompt returns the first real user turn in the chunk, shortened to
// a title-sized string. It reuses parseLine, so plumbing turns (slash-command
// echoes, IDE context, compact summaries) are already filtered out.
func firstUserPrompt(chunk []byte) string {
	for _, line := range strings.Split(string(chunk), "\n") {
		msg, ok := parseLine(line)
		if !ok || msg.Role != "user" {
			continue
		}
		text := strings.Join(strings.Fields(msg.Text), " ")
		const max = 80
		runes := []rune(text)
		if len(runes) <= max {
			return text
		}
		// Truncate in runes, not bytes: a byte cut can split a multi-byte
		// character and put invalid UTF-8 into every title listing.
		head := string(runes[:max])
		if cut := strings.LastIndex(head, " "); cut >= len(head)/2 {
			head = head[:cut]
		}
		return head + "…"
	}
	return ""
}

// cwdLine mirrors the "cwd" field present on most transcript lines.
type cwdLine struct {
	Cwd string `json:"cwd"`
}

// firstCwd returns the first working directory recorded in the chunk. Only
// the first one identifies the project: later lines can carry subdirectories
// the session cd-ed into.
func firstCwd(chunk []byte) string {
	offset := 0
	for offset < len(chunk) {
		idx := bytes.Index(chunk[offset:], cwdMarker)
		if idx < 0 {
			return ""
		}
		abs := offset + idx
		lineStart := bytes.LastIndexByte(chunk[:abs], '\n') + 1
		var c cwdLine
		if json.Unmarshal(lineAt(chunk, lineStart), &c) == nil && filepath.IsAbs(c.Cwd) {
			return c.Cwd
		}
		lineEnd := bytes.IndexByte(chunk[abs:], '\n')
		if lineEnd < 0 {
			return ""
		}
		offset = abs + lineEnd + 1
	}
	return ""
}
