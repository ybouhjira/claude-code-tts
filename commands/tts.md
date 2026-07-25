---
description: Control the text-to-speech experience — mode, voice, read the last response, read a file, and more
argument-hint: mode full|sentence|off | last | file PATH | read [PATH] | say TEXT | selection | stop | speed RATE | provider NAME | voice NAME | code include|exclude | markdown keep|strip | cap N|none | show | reset
allowed-tools: Bash(~/.claude/plugins/claude-code-tts/bin/tts-ctl:*)
---

The TTS control command already ran before you were invoked. Its output:

!`~/.claude/plugins/claude-code-tts/bin/tts-ctl "$ARGUMENTS"`

Reply with one short sentence telling the user what changed or what is now playing, based on the output above. Only if the output shows a shell quoting or syntax error (possible when free text after `say` contains double quotes, backticks, `$`, or backslashes), run `~/.claude/plugins/claude-code-tts/bin/tts-ctl say` yourself once, passing the text as one properly single-quoted argument — never wrap it in backticks. Otherwise do not run any tools and take no other action.

For reference, the subcommands are:
- `mode full|sentence|off` — how much of each response is auto-spoken
- `last` (add `--with-code` or `--raw`) — read the last response aloud now
- `file PATH` (add `--with-code`) — read a text or Markdown file aloud now
- `read [PATH] [--browser]` — open the read-along view: the conversation is read aloud with the current sentence and word highlighted, and selected text can be read via the right-click menu. Inside VS Code it opens as an editor tab (click the printed link); `--browser` forces the system web browser
- `say TEXT` — speak arbitrary text now
- `selection` — speak the text currently highlighted in any window (X11 primary selection; works on the Claude Code chat panel)
- `stop` — stop any read-out currently in progress
- `speed RATE` (0.5–2.0, or `default`) — set playback speed; pitch is preserved
- `provider openai|elevenlabs|kokoro` — switch the TTS provider
- `voice NAME` (or `default`) — change the voice; voice names are provider-specific, so set one that the current provider knows
- `code include|exclude` — read code blocks aloud, or skip them
- `markdown keep|strip` — keep Markdown markup, or strip it for cleaner speech
- `cap N|none` — limit spoken characters
- `show` — print the current settings
- `reset` — clear all on-the-fly overrides
