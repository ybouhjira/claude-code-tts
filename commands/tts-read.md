---
description: Open the read-along view — this conversation, or any Markdown/text file you name, read aloud with live sentence and word highlighting (opens inside VS Code by default; add --browser for the system browser; select any text on the page to read just that part)
argument-hint: [transcript-or-file-path] [--browser]
allowed-tools: Bash(~/.claude/plugins/claude-code-tts/bin/tts-ctl:*)
---

The read-along view already launched before you were invoked. Command output:

!`~/.claude/plugins/claude-code-tts/bin/tts-ctl "read $ARGUMENTS"`

Reply with one short sentence telling the user the read-along view is ready,
based on the output above. Always include the URL from the output written as
a markdown link, for example [Open the read-along view](http://127.0.0.1:8898/).
When the output says to click the link, say that clicking it opens the view
inside VS Code as an editor tab. Do not run any tools and take no other action.
