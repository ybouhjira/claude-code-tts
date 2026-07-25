# Global setup: use TTS in every project, in the VS Code GUI

This guide sets up the TTS plugin so it is available in **every** project and **every** VS Code window, with your API keys available everywhere and the ability to switch providers whenever you want.

It is aimed at Linux users who run Claude Code as the VS Code extension (the chat panel inside VS Code) and launch VS Code from the desktop, not from a terminal. That launch method is the source of the most common problem, explained below.

## How provider selection works

The plugin supports three text-to-speech providers: **OpenAI**, **ElevenLabs**, and **Kokoro**. OpenAI and ElevenLabs are cloud services that need an API key. Kokoro is different: it is a free, open-weight model that runs on your own machine through a small local server, so it needs no key and costs nothing per request. Each `speak` call decides which provider to use in this order:

1. If the call sets the `provider` parameter (`openai`, `elevenlabs`, or `kokoro`), that wins.
2. Otherwise, if the `TTS_PROVIDER` environment variable is set to a valid provider, that is the default.
3. Otherwise, the default is whichever API key is present, preferring OpenAI when both are set.

So you set a sensible default once, and override it per request when you want another provider. You never have to restart anything to switch.

Kokoro is the one exception to step 3: it has no API key to detect, so it is never picked automatically. You opt into it explicitly, either by setting `TTS_PROVIDER=kokoro` or by passing `provider=kokoro` on a single call.

The cloud API keys come from environment variables: `OPENAI_API_KEY` for OpenAI, and `ELEVENLABS_API_KEY` for ElevenLabs. The server starts as long as at least one provider is usable — one of those keys is set, or Kokoro is enabled (`TTS_PROVIDER=kokoro`, or `KOKORO_BASE_URL` points at a running server). For Kokoro setup, see [the Kokoro section in the README](../README.md#free-offline-tts-with-kokoro).

## Step 1 — Make your API keys available to every app and session

Here is the problem this step solves. When you launch VS Code from its desktop icon, it does **not** inherit the environment variables you export in your shell startup files (`~/.bashrc`, `~/.profile`) or in a `.env` file you `source`. Those are only loaded by interactive shells. A GUI-launched app gets a minimal environment. So if your `.mcp.json` passes a key as `${ELEVENLABS_API_KEY}`, it expands to an empty string, and the server cannot authenticate.

The fix that covers every GUI window and every session is the **systemd user environment**, which your desktop session loads once when you log in.

Create the directory:

```bash
mkdir -p ~/.config/environment.d
```

Create the file `~/.config/environment.d/claude-tts.conf` with your real keys. `OPENAI_API_KEY` is optional — include it only if you want to use OpenAI as well.

```ini
ELEVENLABS_API_KEY=your_elevenlabs_key_here
OPENAI_API_KEY=your_openai_key_here
TTS_PROVIDER=elevenlabs
```

The format is `KEY=value`, one per line. Do not add `export`, and do not put quotes around the value — systemd reads the value literally to the end of the line.

Now **log out and log back in**. This is required: the systemd user session reads `environment.d` only at login. This approach works on desktops that use a systemd user session, which includes standard Ubuntu/GNOME and KDE.

After you log back in, confirm the variables are present. Open a terminal and run:

```bash
echo "${ELEVENLABS_API_KEY:+ELEVENLABS set}  provider=${TTS_PROVIDER}"
```

You should see `ELEVENLABS set  provider=elevenlabs`.

If you also want the keys in plain login shells that do not read `environment.d` (an SSH session, for example), add the same three variables as `export` lines to your `~/.profile`.

## Step 2 — Register the server once, for every project (user scope)

Claude Code has three configuration scopes for MCP servers. A **project** server lives in a project's `.mcp.json` and only works in that project. A **user** server lives in your `~/.claude.json` and works in every project. For a global setup you want user scope.

If you previously added a project-scoped server (a `tts` entry in a repo's `.mcp.json`), remove it first so you do not end up with two servers of the same name:

```bash
rm /path/to/your/project/.mcp.json   # only if it exists and only contains this server
```

Then add the server at user scope. This assumes you installed the plugin with `make install`, which places the binary at `~/.claude/plugins/claude-code-tts/bin/tts-server`:

```bash
claude mcp add --scope user tts ~/.claude/plugins/claude-code-tts/bin/tts-server
```

Because you added this server yourself at user scope, Claude Code trusts it. You will not get the per-project approval prompt that project-scoped servers require. The server inherits the keys from your session environment (Step 1), so you do not need to embed any secret in the registration.

## Step 3 — Reload VS Code and verify

Fully quit VS Code (all windows) and reopen it. Two things must now be true: you logged out and back in, so VS Code has the keys; and the user-scope server is registered, so every project sees it.

In any project, type `/mcp` in the Claude chat. The `tts` server should show as **connected**.

If `/mcp` still reports a missing key, the extension is not inheriting your environment for some reason. Pass the key to the server explicitly instead. Run this in a terminal where `echo $ELEVENLABS_API_KEY` prints your key:

```bash
claude mcp remove --scope user tts
claude mcp add --scope user tts \
  -e ELEVENLABS_API_KEY="$ELEVENLABS_API_KEY" \
  -e OPENAI_API_KEY="$OPENAI_API_KEY" \
  -e TTS_PROVIDER=elevenlabs \
  -- ~/.claude/plugins/claude-code-tts/bin/tts-server
```

This stores the values in your `~/.claude.json`, which is a private file in your home directory and is never committed to any repository.

## Step 4 — Switch providers whenever you want

The default provider is whatever `TTS_PROVIDER` is set to. To change the default everywhere, edit that line in `~/.config/environment.d/claude-tts.conf` and log out and back in.

To switch for a single request, just tell Claude which provider to use — no restart, no config change. For example:

- "Use the speak tool with the OpenAI provider and the nova voice."
- "Say that again with the elevenlabs provider."

To use OpenAI at all, `OPENAI_API_KEY` must be set (Step 1). OpenAI voices are the fixed set: `alloy`, `echo`, `fable`, `onyx`, `nova`, `shimmer`. ElevenLabs voices are discovered from your account, so you can name any voice your account has, or pass a raw voice ID.

## Alternative: launch VS Code from a terminal

If you would rather not use the systemd approach, you can open VS Code from a terminal that already has the keys exported. From your project directory:

```bash
source .env        # a file that exports your keys
code .
```

Everything VS Code launches then inherits those keys. The catch is that it only works when you launch VS Code this way, not from the desktop icon.

## Troubleshooting

**`/mcp` shows the server but a missing-key warning.** Your environment does not have the key where VS Code can see it. Redo Step 1 and confirm with the `echo` check, or use the explicit `-e` registration shown in Step 3.

**ElevenLabs returns `402 paid_plan_required` / "Free users cannot use library voices via the API."** This happens when you request one of ElevenLabs' older premade voices (such as "Rachel") whose IDs were reclassified as Voice Library voices that free-tier API keys cannot use. The plugin avoids this by discovering the voices your account is actually allowed to use, so let it use the default, name a voice your account has, or pass a current default voice such as Aria. To see exactly which voices your key can use:

```bash
curl -s -H "xi-api-key: $ELEVENLABS_API_KEY" https://api.elevenlabs.io/v1/voices \
  | grep -oE '"(name|voice_id)": *"[^"]*"'
```
