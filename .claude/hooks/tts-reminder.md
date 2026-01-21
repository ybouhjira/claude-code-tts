# TTS Usage Check

Review your response and determine if audio feedback would benefit the user:

**USE TTS for:**
- ✅ Task completions: "Build done", "Tests passed", "Deployed successfully"
- ✅ Errors/failures: "Build failed", "3 tests failing" (use voice: onyx)
- ✅ Questions to user: Speak the question so they hear it
- ✅ Long waits starting: "Installing dependencies..."
- ✅ Important milestones: "PR created", "Committed changes"

**SKIP TTS for:**
- ❌ Code explanations or documentation
- ❌ Listing files or showing content
- ❌ Simple acknowledgments ("Got it", "Sure")
- ❌ Already used TTS in this response

If TTS is appropriate but wasn't used, add a `mcp__tts__speak` call now.
Keep messages SHORT (1-2 sentences). Voice: `nova` (friendly), `onyx` (errors).
