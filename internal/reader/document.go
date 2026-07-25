package reader

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The read-along page does not care where its text came from: it renders
// "messages" and reads their sentences. That makes any Markdown or plain-text
// file readable with the full follow experience — sentence and word
// highlighting, follow scrolling, click-to-read, live reload on save — as
// long as the server presents the file as one message. This file is that
// adapter.

// maxDocumentBytes bounds how much of a file the reader will load. A page
// holding several megabytes of text would be unusable long before this limit
// matters; the cap just keeps an accidental `tts-ctl read huge.log` sane.
const maxDocumentBytes = 4 << 20 // 4 MiB

// IsDocumentPath reports whether a path names a plain document rather than a
// session transcript. Transcripts are always .jsonl files; everything else
// (.md, .txt, extensionless notes) is treated as a document.
func IsDocumentPath(path string) bool {
	return !strings.EqualFold(filepath.Ext(path), ".jsonl")
}

// documentSessionID derives a stable page id for a document from its absolute
// path. The "doc-" prefix keeps these ids from ever colliding with Claude
// Code session UUIDs, and the hash keeps the id URL-safe no matter what the
// path contains.
func documentSessionID(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	sum := sha256.Sum256([]byte(abs))
	return "doc-" + hex.EncodeToString(sum[:8])
}

// ParseDocument reads a Markdown or plain-text file and returns it as a
// single message with the "doc" role, which the page renders without the
// chat-turn chrome. The page's own Markdown renderer does the rest: headings,
// lists, inline styling, and code blocks that continuous reading skips.
func ParseDocument(path string) ([]Message, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read document: %w", err)
	}
	if fi.Size() > maxDocumentBytes {
		return nil, fmt.Errorf("document is too large to read aloud (%d bytes, limit %d)", fi.Size(), maxDocumentBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read document: %w", err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return []Message{}, nil
	}
	return []Message{{
		Role:      "doc",
		Text:      text,
		Timestamp: fi.ModTime().UTC().Format("2006-01-02T15:04:05Z07:00"),
	}}, nil
}

// DocumentTitle names a document for the page header and tab title: the first
// Markdown heading if one appears near the top, else the filename.
func DocumentTitle(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return filepath.Base(path)
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, _ := f.Read(buf)
	for _, line := range strings.Split(string(buf[:n]), "\n") {
		trimmed := strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(trimmed, "#"); ok {
			title := strings.TrimSpace(strings.TrimLeft(after, "#"))
			if title != "" {
				return title
			}
		}
	}
	return filepath.Base(path)
}
