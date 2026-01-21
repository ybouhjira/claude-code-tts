# TTS Reminder Hook

You have access to text-to-speech via the `mcp__tts__speak` tool. You should use it to provide audio feedback to the user for:
- Task completions ("Build completed successfully")
- Errors/failures ("Build failed - check the logs")
- Important milestones ("PR created")
- Waiting periods ("Installing dependencies")
- Questions you ask the user
- Brief summaries after multi-step tasks

If you haven't used TTS in this response and it would have been appropriate, consider if you should add a `mcp__tts__speak` call.

Keep TTS messages SHORT (1-2 sentences). Use voice "nova" for friendly updates, "onyx" for errors.
