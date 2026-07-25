---
description: Read Claude's last response aloud (add --with-code to include code blocks, --raw to include everything)
argument-hint: [--with-code | --raw]
allowed-tools: Bash(~/.claude/plugins/claude-code-tts/bin/tts-ctl:*)
---

The read-out already started before you were invoked. Command output:

!`~/.claude/plugins/claude-code-tts/bin/tts-ctl "last $ARGUMENTS"`

Reply with one short sentence telling the user the last response is being read
aloud (or what went wrong, based on the output above). Do not run any tools
and take no other action.
