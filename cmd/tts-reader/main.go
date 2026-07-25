// Command tts-reader opens the read-along view for a Claude Code session:
// the conversation rendered in a local web page that reads it aloud while
// highlighting the sentence and word being spoken, with read-on-select.
//
// It is normally started through `tts-ctl read` (the /tts-read slash
// command), which loads the plugin configuration first so provider API keys
// and defaults are present in the environment.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"

	"github.com/ybouhjira/claude-code-tts/internal/logging"
	"github.com/ybouhjira/claude-code-tts/internal/reader"
)

func main() {
	transcript := flag.String("transcript", "", "path to a session transcript (.jsonl) or a Markdown/text file to read; default: the newest session of the project directory")
	projectDir := flag.String("project-dir", "", "project directory used to locate the session (default: $CLAUDE_PROJECT_DIR, else the working directory)")
	port := flag.Int("port", portFromEnv(), "port to listen on (127.0.0.1 only); a busy port falls back to a random free one")
	noOpen := flag.Bool("no-open", false, "do not open the page anywhere")
	browser := flag.Bool("browser", false, "open the system web browser even when running inside VS Code")
	urlFile := flag.String("url-file", "", "write the page URL to this file once the server is ready")
	flag.Parse()

	if err := logging.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: file logging unavailable: %v\n", err)
	}

	path := *transcript
	if path == "" {
		dir := *projectDir
		if dir == "" {
			dir = os.Getenv("CLAUDE_PROJECT_DIR")
		}
		if dir == "" {
			wd, err := os.Getwd()
			if err != nil {
				fatal("cannot determine the working directory: %v", err)
			}
			dir = wd
		}
		found, err := reader.FindLatestTranscript(dir)
		if err != nil {
			fatal("%v\nPass the transcript directly: tts-reader -transcript /path/to/session.jsonl", err)
		}
		path = found
	}
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		fatal("not a readable file: %s", path)
	}

	// If a reader from this plugin is already running, register the session
	// with it and reuse the running server instead of starting a second one.
	// Each session has its own page URL, so other open tabs are undisturbed.
	if reader.ProbeInstance(*port) {
		pageURL, err := reader.SwitchTranscript(*port, path)
		if err != nil {
			fatal("a reader already runs on port %d but did not accept the transcript: %v", *port, err)
		}
		finish(pageURL, *urlFile, *noOpen, *browser)
		return
	}

	srv := reader.New(reader.Options{Transcript: path, Port: *port})
	pageURL, err := srv.Start()
	if err != nil {
		fatal("%v", err)
	}
	finish(pageURL, *urlFile, *noOpen, *browser)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	srv.Shutdown()
}

// portFromEnv honors TTS_READER_PORT so the port can be set once in the
// plugin config instead of on every launch.
func portFromEnv() int {
	if v := os.Getenv("TTS_READER_PORT"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	return reader.DefaultPort
}

// finish reports the page URL: on stdout always, into the URL file when
// asked (the tts-read launcher polls that file), and then opens it — unless
// the process runs inside VS Code, where the URL is left as a clickable link
// instead. A workbench.externalUriOpeners rule (see the README) makes that
// click open the page in VS Code's built-in Simple Browser tab, so the user
// stays in the editor; launching the system browser would pull them out of
// it, which is why that path needs an explicit -browser.
func finish(pageURL, urlFile string, noOpen, forceBrowser bool) {
	fmt.Println(pageURL)
	if urlFile != "" {
		if err := os.WriteFile(urlFile, []byte(pageURL+"\n"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot write %s: %v\n", urlFile, err)
		}
	}
	if noOpen {
		return
	}
	if inVSCode() && !forceBrowser {
		fmt.Println("running inside VS Code: click the URL above to open the page as an editor tab (pass --browser for the system browser)")
		return
	}
	if err := openBrowser(pageURL); err != nil {
		fmt.Fprintf(os.Stderr, "open %s yourself (auto-open failed: %v)\n", pageURL, err)
	}
}

// inVSCode reports whether this process was started from within VS Code —
// either an integrated terminal or the Claude Code extension's shell.
func inVSCode() bool {
	return os.Getenv("TERM_PROGRAM") == "vscode" ||
		os.Getenv("VSCODE_PID") != "" ||
		os.Getenv("VSCODE_IPC_HOOK") != ""
}

func openBrowser(pageURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", pageURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", pageURL)
	default:
		cmd = exec.Command("xdg-open", pageURL)
	}
	return cmd.Start()
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
