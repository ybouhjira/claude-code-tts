# Claude Code TTS Plugin

[![Go Version](https://img.shields.io/badge/Go-1.23+-00ADD8?style=flat&logo=go)](https://golang.org)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![CI](https://github.com/ybouhjira/claude-code-tts/actions/workflows/ci.yml/badge.svg)](https://github.com/ybouhjira/claude-code-tts/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/ybouhjira/claude-code-tts/branch/main/graph/badge.svg)](https://codecov.io/gh/ybouhjira/claude-code-tts)
[![MCP](https://img.shields.io/badge/MCP-Compatible-green.svg)](https://modelcontextprotocol.io)

A Text-to-Speech MCP server plugin for Claude Code that converts text to speech using OpenAI's TTS API, ElevenLabs, or Kokoro (a free, open-weight model that runs on your own machine). Get audio feedback from Claude as you work!

![Demo](demo.gif)

## Features

- **Deterministic Auto-Speak**: Every Claude response is automatically spoken (via Stop hook)
- **Three TTS Providers**, selectable per request: OpenAI (alloy, echo, fable, onyx, nova, shimmer); ElevenLabs (any voice on your account, discovered automatically, plus raw voice IDs); and Kokoro (free and offline via a local server — no API key, no per-use cost)
- **Worker Pool Architecture**: Non-blocking queue with concurrent processing
- **Mutex-Protected Playback**: One audio plays at a time, no overlapping
- **Cross-Platform**: macOS (afplay), Linux (mpv/ffplay/mpg123), Windows (PowerShell)
- **Standalone CLI**: `speak-text` binary for direct TTS without MCP
- **Read-Along View**: `/tts-read` opens the conversation in a browser page that reads it aloud and highlights each sentence and word as it is spoken; select any text and read just that part from the right-click menu. Each session gets its own tab, titled `project — session title`, and a built-in navigator lists every project and session on the machine

## Quick Install

```bash
# One-liner installation
curl -fsSL https://raw.githubusercontent.com/ybouhjira/claude-code-tts/main/install.sh | bash
```

Or install manually:

```bash
git clone https://github.com/ybouhjira/claude-code-tts.git ~/.claude/plugins/claude-code-tts
cd ~/.claude/plugins/claude-code-tts
make install
```

## Requirements

- **Go 1.21+** (for building from source)
- **A TTS provider**, one of:
  - An **API key** for OpenAI (with TTS access) or ElevenLabs, or
  - A local **Kokoro** server — free and keyless (see [Free, offline TTS with Kokoro](#free-offline-tts-with-kokoro))
- **Audio Player**:
  - macOS: `afplay` (built-in)
  - Linux: `mpv`, `ffplay`, or `mpg123`
  - Windows: PowerShell (built-in)

## Configuration

Set an API key for at least one provider:

```bash
export OPENAI_API_KEY="sk-..."        # for the openai provider
export ELEVENLABS_API_KEY="..."       # for the elevenlabs provider
```

Or add them to your shell profile (`~/.zshrc` or `~/.bashrc`).

When a request does not name a provider, the server picks a default. The `TTS_PROVIDER` environment variable wins if set to `openai`, `elevenlabs`, or `kokoro`. Otherwise the default follows which API keys are configured, preferring OpenAI when both are set. Kokoro has no API key to detect, so it is never auto-selected — you opt into it explicitly, either with `TTS_PROVIDER=kokoro` or by naming it on a single request.

```bash
export TTS_PROVIDER="elevenlabs"      # optional: make ElevenLabs the default
```

To use the plugin in every project and every VS Code window at once — including making your keys reach the VS Code GUI, which does not inherit your shell environment — see [docs/global-setup.md](docs/global-setup.md).

### Free, offline TTS with Kokoro

Kokoro is an open-weight text-to-speech model. "Open-weight" means the trained model file is published (under the Apache-2.0 license) for anyone to run, so there is no hosted service and no API key. It runs on your own machine, on CPU or GPU, at no per-use cost.

Because the model is not a cloud endpoint, something has to run it locally. The simplest way is **Kokoro-FastAPI**, a small local server that wraps the model and exposes the *same* HTTP shape OpenAI's TTS API uses. This plugin's `kokoro` provider talks to that server, so once it is running you get free speech with no other setup.

**1. Start a local Kokoro server.** The one-line Docker option (CPU build):

```bash
docker run -p 8880:8880 ghcr.io/remsky/kokoro-fastapi-cpu:latest
```

There is a GPU image (`kokoro-fastapi-gpu`) and a `pip`/`uv` install path too — see the [Kokoro-FastAPI project](https://github.com/remsky/Kokoro-FastAPI) for those and for the current list of voices.

**2. Point the plugin at it and select it.**

```bash
export TTS_PROVIDER="kokoro"                    # use Kokoro by default
export KOKORO_BASE_URL="http://localhost:8880"  # optional; this is the default
```

`KOKORO_BASE_URL` is the server's root address (host and port); the plugin appends the `/v1/audio/speech` path itself. Leave it unset to use `http://localhost:8880`.

**Voices.** Kokoro voice names look like `af_bella`, `af_heart`, `am_michael`, and `bf_emma`. The leading letters are a hint: `a` = American and `b` = British English, then `f` = female and `m` = male. The default is `af_bella`. The server also accepts weighted blends such as `af_bella(2)+af_sky(1)`. The plugin passes whatever voice you give straight through to the server, so any voice your server supports works; the server reports a clear error for an unknown one.

**Playback speed** is applied the same way as for the other providers, by the audio player from `TTS_SPEED` (see the controls below), so `speed` is not sent to the Kokoro server.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Claude Code                              │
│                         │                                    │
│                    MCP Protocol                              │
│                         │                                    │
│  ┌──────────────────────▼──────────────────────────────┐    │
│  │              TTS MCP Server (Go)                     │    │
│  │  ┌─────────────────────────────────────────────┐    │    │
│  │  │              Tool Handlers                   │    │    │
│  │  │  speak(text, provider, voice) │ tts_status() │    │    │
│  │  └─────────────┬─────────┴─────────────────────┘    │    │
│  │                │                                     │    │
│  │  ┌─────────────▼─────────────────────────────┐      │    │
│  │  │           Worker Pool (2 workers)          │      │    │
│  │  │  ┌─────────┐    ┌─────────────────────┐   │      │    │
│  │  │  │ Job     │───►│ Queue (50 slots)    │   │      │    │
│  │  │  │ Submit  │    └──────────┬──────────┘   │      │    │
│  │  │  └─────────┘               │              │      │    │
│  │  │                   ┌────────▼────────┐     │      │    │
│  │  │                   │ Worker 1 │ 2    │     │      │    │
│  │  │                   └────────┬────────┘     │      │    │
│  │  └────────────────────────────│──────────────┘      │    │
│  │                               │                      │    │
│  │  ┌────────────────────────────▼──────────────────┐  │    │
│  │  │            TTS Provider (per job)              │  │    │
│  │  │   OpenAI: POST /v1/audio/speech (tts-1)        │  │    │
│  │  │   ElevenLabs: POST /v1/text-to-speech/{voice}  │  │    │
│  │  │   Kokoro: POST /v1/audio/speech (local server) │  │    │
│  │  └───────────────────┬────────────────────────────┘  │    │
│  │                      │                               │    │
│  │  ┌───────────────────▼────────────────────────────┐  │    │
│  │  │         Audio Player (Mutex Protected)          │  │    │
│  │  │   macOS: afplay │ Linux: mpv │ Win: PowerShell  │  │    │
│  │  └─────────────────────────────────────────────────┘  │    │
│  └──────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────┘
```

## Usage

### speak(text, provider, voice)

Convert text to speech and play it aloud.

**Parameters:**
| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `text` | string | Yes | Text to speak (max 4096 chars) |
| `provider` | string | No | `openai`, `elevenlabs`, or `kokoro` (default: see Configuration) |
| `voice` | string | No | Voice to use (default: the provider's default voice) |

**OpenAI Voices** (default: `alloy`):
| Voice | Description |
|-------|-------------|
| `alloy` | Neutral, balanced |
| `echo` | Male, warm |
| `fable` | British accent |
| `onyx` | Deep male |
| `nova` | Female, friendly |
| `shimmer` | Soft female |

**ElevenLabs Voices**

ElevenLabs does not use a fixed list of names. Instead, the plugin asks your account which voices it has (through the List voices endpoint, `GET /v1/voices`) and accepts any of those by name. This matters for free-tier keys: the old premade voices like "Rachel" were reclassified as Voice Library voices, which free keys cannot use through the API, so hardcoding them caused a `402 paid_plan_required` error. Using your account's own voices avoids that.

You can pass a voice in three ways:

- **A voice name from your account**, such as `Aria` or a custom voice you created. Names are case-insensitive.
- **A raw voice ID**, the 20-character identifier shown next to a voice in your ElevenLabs voice library (for example, Aria is `9BWtsMINqrJLrRacOk9x`). This always works, even without voice discovery.
- **Nothing at all.** The default is the first voice on your account, or Aria (`9BWtsMINqrJLrRacOk9x`, a current default voice usable on the free tier) if the account's voices cannot be read.

To see the exact voices and IDs your key can use, run:

```bash
curl -s -H "xi-api-key: $ELEVENLABS_API_KEY" https://api.elevenlabs.io/v1/voices \
  | grep -oE '"(name|voice_id)": *"[^"]*"'
```

**Kokoro Voices** (default: `af_bella`)

Kokoro voice names look like `af_bella`, `af_heart`, `am_michael`, and `bf_emma`. The plugin does not keep a fixed list: it passes whatever voice you give straight to your local Kokoro server, which is the final judge. That way new voices and weighted blends such as `af_bella(2)+af_sky(1)` work without a plugin update. See [Free, offline TTS with Kokoro](#free-offline-tts-with-kokoro) for setup, and list your server's voices with:

```bash
curl -s http://localhost:8880/v1/audio/voices
```

**Example:**
```
Use the speak tool to say "Build completed successfully!" with the nova voice.
Use the speak tool with the elevenlabs provider to say "Deploy finished" with the Aria voice.
```

### tts_status()

Get the current status of the TTS system.

**Returns:**
```json
{
  "worker_count": 2,
  "queue_size": 50,
  "queue_pending": 0,
  "total_processed": 15,
  "total_failed": 0,
  "is_playing": false,
  "providers": ["elevenlabs", "openai"],
  "recent_jobs": [...]
}
```

## Automatic TTS

This plugin includes a **Stop hook** that speaks each Claude response out loud. A "Stop hook" is a script that Claude Code runs every time a response finishes. The hook reads the response text, tidies it up, and plays it.

By default it speaks only the **first sentence**, as a short spoken summary. You can change how much it reads, whether it skips code, and which voice it uses. All of that is controlled by one config file.

The hook runs in the background, so it never blocks Claude's responses. It also saves each response to `last-response.md` inside the plugin folder, so you can replay the whole thing on demand (see `speak-last` below).

### Enabling auto-speak

The Stop hook only runs when Claude Code has loaded this project as a **plugin** — a bundle that Claude Code registers and reads a manifest from. Copying the files into `~/.claude/plugins/` is not enough on its own. Claude Code loads plugins from its own registry, not by scanning that folder, so a plain copy gives you the `speak` tool and the `/tts` commands but leaves the hook inactive. If the commands work but auto-speak stays silent, this is why.

There are two supported ways to turn the hook on.

**Option 1 — install it as a real plugin.** Add the plugin through Claude Code's `/plugin` menu, pointing at this repository, then restart Claude Code or run `/reload-plugins`. Claude Code then reads `.claude-plugin/plugin.json` and auto-discovers `hooks/hooks.json`, so the Stop hook loads the way it is meant to.

**Option 2 — register the hook yourself.** Add a `Stop` hook to your own user settings at `~/.claude/settings.json`, pointing at the installed script. Use this if you installed with `make install` or the `curl` one-liner instead of through the plugin menu:

```json
{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "$HOME/.claude/plugins/claude-code-tts/hooks/auto-speak.sh"
          }
        ]
      }
    ]
  }
}
```

After editing `settings.json`, restart Claude Code, or approve the new hook when it prompts you, so the change takes effect. If you already have other hooks, keep them: add `Stop` alongside them rather than replacing the whole `hooks` block.

### Controls

Put your settings in `~/.config/environment.d/claude-code-tts.conf`. Each setting is a plain `KEY=value` line. The hook re-reads this file on every response, so edits take effect immediately — no restart or re-login needed. A value you export in your shell, or put in front of a single command, always overrides the file.

| Setting | Values | Default | What it does |
|---------|--------|---------|--------------|
| `TTS_SPEAK_MODE` | `sentence`, `full`, `off` | `sentence` | How much of each response to speak. `off` stops auto-speak but keeps the on-demand commands. |
| `TTS_MAX_CHARS` | a number; `0` = no cap | `200` in sentence mode, `0` in full mode | Upper limit on the number of spoken characters. |
| `TTS_STRIP_CODE` | `1`, `0` | `1` | `1` skips fenced code blocks; `0` reads them aloud. |
| `TTS_STRIP_MARKDOWN` | `1`, `0` | `1` | `1` removes Markdown markup (headings, links, emphasis) for cleaner speech. |
| `TTS_PROVIDER` | `openai`, `elevenlabs`, `kokoro` | your `.env` value | Which provider to use. |
| `TTS_VOICE` | a provider voice name or ID | provider default | Which voice to use. |
| `KOKORO_BASE_URL` | a URL | `http://localhost:8880` | Root address of your local Kokoro server (used only when the provider is `kokoro`). |
| `TTS_CHUNK_CHARS` | a number | `1500` | Max characters per request when reading long text; longer input is split on sentence boundaries. |

A starter file with all of these documented lives at `config/claude-code-tts.conf.example`.

Some common setups:

```bash
# Read the whole response, but skip code blocks (good for hands-free review)
TTS_SPEAK_MODE=full
TTS_STRIP_CODE=1

# Read everything, including code
TTS_SPEAK_MODE=full
TTS_STRIP_CODE=0

# Turn auto-speak off and use the on-demand commands instead
TTS_SPEAK_MODE=off
```

### Change it on the fly (slash commands)

Editing the config file is fine for your defaults, but for quick changes during a session use the slash commands. They change settings instantly. The change applies to the very next response, and it overrides the config file until you reset it.

| Command | What it does |
|---------|--------------|
| `/tts-full` | Speak the whole of every response from now on |
| `/tts-sentence` | Go back to speaking only the first sentence |
| `/tts-off` | Stop automatic speaking |
| `/tts-last` | Read the last response aloud now (add `--with-code` to include code) |
| `/tts-read` | Open the read-along browser view of this conversation (or of a Markdown/text file: `/tts-read PATH`) |
| `/tts mode full\|sentence\|off` | Set the auto-speak mode |
| `/tts file PATH` | Read a text or Markdown file aloud now |
| `/tts read [PATH]` | Same as `/tts-read`; optionally name a transcript or a Markdown/text file |
| `/tts say TEXT` | Speak some text right now |
| `/tts selection` | Speak the text currently highlighted in any window (see below) |
| `/tts stop` | Stop any read-out that is currently playing |
| `/tts speed RATE` | Set playback speed, `0.5`–`2.0` (`default` for normal); pitch is preserved |
| `/tts provider openai\|elevenlabs\|kokoro` | Switch the TTS provider |
| `/tts voice NAME` | Change the voice (or `default` to clear it); voice names are provider-specific |
| `/tts code include\|exclude` | Read code blocks aloud, or skip them |
| `/tts markdown keep\|strip` | Keep Markdown markup, or strip it for cleaner speech |
| `/tts cap N\|none` | Limit the number of spoken characters |
| `/tts show` | Print the current settings |
| `/tts reset` | Clear the on-the-fly changes and use the config file again |

Under the hood these run `tts-ctl`, a small command installed next to `speak-text`. You can run it directly in a terminal too, for example `tts-ctl mode full` or `tts-ctl show`.

The slash commands act instantly: the `tts-ctl` call is embedded in the command file with Claude Code's `!` pre-execution syntax, so it runs the moment you press Enter — before the model turn starts. Claude's reply afterward is only a confirmation; the audio or setting change never waits for it.

Two notes. A brand-new slash command may only appear after you restart Claude Code once. The on-the-fly settings are stored in a `runtime.env` file inside the plugin folder, and `/tts reset` deletes it.

### speak-text CLI

A standalone binary for direct TTS without going through MCP:

```bash
# Basic usage
speak-text "Hello world"

# With voice selection
speak-text -voice onyx "Error occurred"

# With the ElevenLabs provider (uses your account's default voice)
speak-text -provider elevenlabs "Error occurred"

# ...or name a voice / pass a raw voice ID
speak-text -provider elevenlabs -voice Aria "Error occurred"
speak-text -provider elevenlabs -voice 9BWtsMINqrJLrRacOk9x "Error occurred"

# With the free, local Kokoro provider (needs a running Kokoro server; no API key)
speak-text -provider kokoro "Build finished"
speak-text -provider kokoro -voice am_michael "Build finished"
```

Located at `~/.claude/plugins/claude-code-tts/bin/speak-text` after installation.

### speak-last — replay the last response

Speaks the most recent Claude response that the hook cached. By default it reads the whole response and skips code.

```bash
speak-last                 # whole last response, no code
speak-last --with-code     # include code blocks
speak-last --raw           # include code and Markdown markup, unchanged
speak-last --voice onyx    # override the voice
speak-last --provider openai
```

### speak-file — read a document aloud

Reads any text or Markdown file out loud. By default it strips code and Markdown so a document reads cleanly, and it splits long files into chunks so nothing is too large for the provider.

```bash
speak-file NOTES.md                # read the file, no code, no markup
speak-file --with-code README.md   # include code blocks
speak-file --raw CHANGELOG.md      # read it exactly as written
```

Both commands install to `~/.claude/plugins/claude-code-tts/bin/`, next to `speak-text`.

## Read-along mode (`/tts-read`)

Run `/tts-read` inside Claude Code (or `tts-ctl read` in a terminal) and the current conversation opens as a clean reading page. Press Play and the page reads the conversation aloud. The sentence being spoken gets a soft highlight, and inside it the word being spoken gets a stronger one, karaoke style. The page follows along by scrolling, and it live-updates as the session continues — new replies appear at the bottom as they arrive.

### Where it opens

- **Inside VS Code (default when you work in VS Code).** The command prints a link; clicking it opens the page as an editor tab, right next to your chat, using VS Code's built-in Simple Browser. This needs a one-time setting so VS Code knows to keep that address in the editor — add this to your VS Code `settings.json`:

  ```json
  "workbench.externalUriOpeners": {
    "127.0.0.1:8898": "simpleBrowser.open",
    "localhost:8898": "simpleBrowser.open"
  }
  ```

  Without the setting, the click falls back to your system browser. (If you change `TTS_READER_PORT`, use the same port here.)
- **System web browser.** Pass `--browser` (`/tts-read --browser`, or `tts-ctl read --browser`) to open the page in your regular browser instead.
- **Outside VS Code.** When the command runs in a plain terminal, the system browser opens automatically, as before.

Ways to control what is read:

- **Play / Pause / Stop** buttons, plus **Space** to toggle and **←/→** to jump a sentence back or forward.
- **Click any sentence** to start reading from that exact spot.
- **Select any text**, then right-click and choose **Read selection** (a small floating "Read selection" button also appears near the selection). Only the selected text is read.
- **Right-click → Read from here** starts continuous reading from the paragraph under the cursor.
- The **🔊 Read** button on a message reads just that one message.
- **Auto-read new** makes the page speak each new Claude reply as it lands — a hands-free mode with visual tracking, unlike the plain auto-speak hook.
- **Notes as bullets** (on by default) restyles Claude's *working notes* — the short passages it writes between tool runs, before the final answer — as quiet italic bullet points under a "working notes" label, so the answer itself stands out. Untick it to see the whole turn as one flat text. Markdown headings also render at their real sizes, stepping down from `#` to `######`.
- **Inline Markdown renders styled**, like the chat panel: `` `code` `` appears as a monospace chip, **bold** stays bold, *italic* stays italic, ~~strikethrough~~ is struck through, and links show as their colored text. None of this changes what is spoken — the markers were never read aloud, and still aren't.

Notes on how it works:

- The **browser** plays the audio in read-along mode (not the plugin's native player) because only the page itself can keep the highlight in step with playback. Audio is fetched one sentence at a time from the plugin's local server, so sentence highlighting is exact; word timing within a sentence is estimated from word lengths, which tracks real speech closely.
- It uses the same providers, voice, and speed settings as everything else (`/tts voice`, `/tts speed`, `/tts provider` are honored when the page opens; the page also has its own pickers).
- Code blocks are shown but skipped during continuous reading. To hear code, select it and use **Read selection**.
- `/tts stop` (or `tts-ctl stop`, or the Ctrl+Alt+X hotkey) also silences every open read-along page, not just the native player: page audio lives in the browser where process kills cannot reach, so the reader server relays the stop to all connected pages over its live event stream.
- The page is served on `127.0.0.1` only (default port `8898`, changeable with `TTS_READER_PORT`). Requests from other machines or foreign web pages are rejected.
- One reader server handles any number of sessions. Every session has its own page URL (`/?s=<session-id>`), so running `/tts-read` in another project opens another tab instead of hijacking the one you already have. Each page titles itself `project — session title`, which is what its tab shows.
- Opening the page without a session id (just `http://127.0.0.1:8898/`, or the **☰ Sessions** link in the top bar) shows the **navigator**: every Claude Code project on the machine with its sessions, newest first, each a link into the read-along view. Old sessions work the same as live ones.
- Running `/tts-read` again reuses the already-open reader server instead of starting a second one. Pass a path (`/tts-read ~/.claude/projects/<project>/<session>.jsonl`) to read a specific transcript file directly.
- **Any Markdown or plain-text file works too**: `/tts-read notes.md` (or `tts-ctl read notes.md`) opens the file in the same read-along page, with the identical follow experience — sentence and word highlighting, follow scrolling, click-to-read, selection reading, and code blocks that are shown but skipped by continuous reading. The page live-reloads when you save the file. Unlike `/tts file`, which plays audio through the native player with nothing to look at, this gives the full visual read-along. One note: the page URL for a file only survives as long as the reader server runs; after a restart, run the command again.
- VS Code offers no command-line way to run a workbench command in an already-running window, so a plain shell cannot open the Simple Browser by itself. Without the companion extension (next section) the printed link is a one-click open; with it, the open is fully automatic.

### The companion VS Code extension (right-click in the real chat + zero-click opens)

`make install-vscode-ext` installs a tiny local extension, `claude-code-tts-bridge` (about sixty lines, no marketplace, no network). It adds two things that are impossible from outside the editor:

- **"Read selection aloud (TTS)" in the right-click menu of the actual Claude Code chat panel.** VS Code lets an extension contribute items to another extension's webview menu (the `webview/context` contribution point, scoped to the Claude panel's view IDs). The menu API does not expose the selected text, so on Linux the command reads the X11 primary selection — which your highlight already filled. The same menu item appears in normal editors too, where the selection comes straight from the editor API (so that path also works on Wayland and macOS).
- **Zero-click `/tts-read`.** The extension watches `~/.claude/plugins/claude-code-tts/reader.open`; the `tts-read` launcher writes the page URL and its project directory there when it runs inside VS Code, and the extension opens the read-along view as an editor tab. This is a file-based bridge in the style of Talon's command-server: protected by ordinary filesystem permissions, with no localhost command socket that other processes or web pages could poke. The extension runs in every open VS Code window, so the flag names the project directory and **only the window that has that project open consumes it** — the tab appears where you are working, not in whichever window happened to poll first. If no open window has the project, any window takes the flag after a few seconds so the view still appears.

The view opens in the extension's own editor tab (a thin wrapper around the local reader page), not in VS Code's Simple Browser. The difference is the tab title: Simple Browser tabs are hard-wired to say "Simple Browser", while the extension's tab carries the session's real name — `project — session title` — and renames itself when the session title evolves. With several sessions open you can tell the tabs apart at a glance. Clicking a printed reader link by hand still goes through the Simple Browser rule above, so both paths work.

Reload the VS Code window once after installing (and after updating the extension). A "Stop TTS read-out" item is included in the chat panel's menu as well.

### Read the real chat panel: highlight + hotkey (Linux/X11)

The Claude Code chat panel itself is a closed webview: no extension can add buttons, menus, or highlighting inside it, and Ctrl+C on selected chat text is unreliable (a known Claude Code bug). But on Linux/X11 there is a clean hack: the moment you highlight text with the mouse — in the chat panel, an editor, anywhere — X11 places it in the *primary selection*, no copy needed. `tts-ctl selection` (or `/tts selection`) reads that and speaks it.

Bind it to keys and it becomes seamless. In your VS Code **keybindings.json**:

```json
{ "key": "ctrl+alt+s", "command": "workbench.action.tasks.runTask", "args": "TTS: Read selection" },
{ "key": "ctrl+alt+x", "command": "workbench.action.tasks.runTask", "args": "TTS: Stop reading" },
{ "key": "ctrl+alt+r", "command": "simpleBrowser.show", "args": "http://127.0.0.1:8898/" }
```

…with two matching entries in your user **tasks.json** (they run silently, no terminal pop-up):

```json
{
  "version": "2.0.0",
  "tasks": [
    {
      "label": "TTS: Read selection",
      "type": "shell",
      "command": "$HOME/.claude/plugins/claude-code-tts/bin/tts-ctl selection",
      "presentation": { "reveal": "never", "echo": false, "focus": false, "panel": "dedicated", "close": true },
      "problemMatcher": []
    },
    {
      "label": "TTS: Stop reading",
      "type": "shell",
      "command": "$HOME/.claude/plugins/claude-code-tts/bin/tts-ctl stop",
      "presentation": { "reveal": "never", "echo": false, "focus": false, "panel": "dedicated", "close": true },
      "problemMatcher": []
    }
  ]
}
```

The result:

- **Ctrl+Alt+S** — highlight anything in the Claude Code chat and hear it read aloud.
- **Ctrl+Alt+X** — instantly stop the read-out.
- **Ctrl+Alt+R** — open the read-along view as a VS Code editor tab with zero clicks.

The selection reader needs `python3` with tkinter (preinstalled on most desktop Linux distributions). On Wayland sessions, install `wl-clipboard` and adapt; on macOS there is no primary selection, so use the read-along view's own selection reading instead.
- The conversation is loaded from the session's transcript file on disk. That format is internal to Claude Code, so the parser is deliberately tolerant; if a future Claude Code release changes it, the page may show less until the plugin is updated.

## Project Structure

```
claude-code-tts/
├── cmd/
│   ├── tts-server/
│   │   └── main.go           # MCP server entry point
│   ├── speak-text/
│   │   └── main.go           # Standalone CLI binary
│   └── tts-reader/
│       └── main.go           # Read-along view launcher (/tts-read)
├── hooks/
│   ├── hooks.json            # Plugin hook declaration (Stop → auto-speak.sh)
│   ├── auto-speak.sh         # Stop hook: speaks each response per config
│   └── tts-common.sh         # Shared helpers: config, text cleaning, chunking
├── scripts/
│   ├── speak-last            # Replay the last response on demand
│   ├── speak-file            # Read a text/Markdown file aloud
│   ├── tts-read              # Launch the read-along view (backs /tts-read)
│   └── tts-ctl               # Change settings on the fly (backs the /tts commands)
├── commands/
│   ├── tts.md                # /tts dispatcher slash command
│   ├── tts-last.md           # /tts-last, and tts-full/sentence/off shortcuts
│   └── ...
├── config/
│   └── claude-code-tts.conf.example  # Documented config with all the knobs
├── internal/
│   ├── audio/
│   │   └── player.go         # Cross-platform audio playback
│   ├── reader/
│   │   ├── transcript.go     # Session transcript (JSONL) parser
│   │   ├── server.go         # Loopback web server for the read-along view
│   │   └── assets/           # The read-along page (HTML/CSS/JS, embedded)
│   ├── server/
│   │   ├── server.go         # MCP server & tool handlers
│   │   └── worker.go         # Worker pool implementation
│   └── tts/
│       ├── provider.go       # Synthesizer interface + provider registry
│       ├── openai.go         # OpenAI TTS client
│       ├── elevenlabs.go     # ElevenLabs TTS client
│       └── kokoro.go         # Kokoro TTS client (local, keyless)
├── .claude-plugin/
│   └── plugin.json            # Plugin manifest (name, version, metadata)
├── Makefile                   # Build automation
└── install.sh                 # One-liner installer
```

## Building from Source

```bash
# Clone the repository
git clone https://github.com/ybouhjira/claude-code-tts.git
cd claude-code-tts

# Build
make build

# Install to Claude Code plugins
make install

# Run tests
make test
```

## Troubleshooting

### "at least one API key is required"
The server needs an API key for at least one provider:
```bash
export OPENAI_API_KEY="sk-..."
# and/or
export ELEVENLABS_API_KEY="..."
```

### "No suitable audio player found on Linux"
Install one of: `mpv`, `ffplay`, or `mpg123`:
```bash
# Ubuntu/Debian
sudo apt install mpv

# Fedora
sudo dnf install mpv

# Arch
sudo pacman -S mpv
```

### Audio not playing on macOS
Check that `afplay` works:
```bash
# Test with a sample audio file
afplay /System/Library/Sounds/Ping.aiff
```

### Queue is full
The default queue size is 50. If you're hitting this limit:
1. Wait for current jobs to complete
2. Check `tts_status()` to see pending jobs
3. The queue will drain as jobs are processed

### High latency
- OpenAI TTS API typically takes 1-3 seconds per request
- Audio files must download completely before playing
- Consider keeping messages short for faster feedback

## API Costs

With the OpenAI provider, the plugin uses the `tts-1` model:
- **Cost**: ~$0.015 per 1,000 characters
- **Example**: "Hello, world!" (13 chars) = ~$0.0002

With the ElevenLabs provider, the plugin uses the `eleven_multilingual_v2` model. ElevenLabs bills characters against your plan's monthly quota rather than per request, so cost depends on your subscription tier.

With the Kokoro provider there is **no cost**: the model runs on your own machine, so you pay nothing per request and need no API key. The only trade-off is that you run a small local server, and synthesis speed depends on your hardware (a GPU is much faster than CPU).

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

MIT License - see [LICENSE](LICENSE) for details.

## Credits

- [OpenAI TTS API](https://platform.openai.com/docs/guides/text-to-speech)
- [ElevenLabs TTS API](https://elevenlabs.io/docs/api-reference/text-to-speech)
- [Kokoro-82M](https://huggingface.co/hexgrad/Kokoro-82M) - the open-weight TTS model
- [Kokoro-FastAPI](https://github.com/remsky/Kokoro-FastAPI) - local OpenAI-compatible Kokoro server
- [mcp-go](https://github.com/mark3labs/mcp-go) - Go MCP implementation
- [Model Context Protocol](https://modelcontextprotocol.io)
