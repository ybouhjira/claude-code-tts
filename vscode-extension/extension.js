// Companion extension for the claude-code-tts plugin.
//
// It exists because two things are impossible from outside the editor:
//
// 1. VS Code offers no command-line way to run a workbench command in an
//    already-running window, so a shell cannot open the read-along view by
//    itself. This extension watches a flag file that `tts-ctl read` writes
//    and opens the view in response — a file-based bridge in the style of
//    pokey/command-server, protected by ordinary filesystem permissions
//    instead of an open localhost socket.
//
// 2. The Claude Code chat panel is a closed webview. No external process can
//    add UI to it — but a VS Code extension can contribute right-click menu
//    items to another extension's webview via the `webview/context` menu
//    point, scoped by that webview's view type. That is how "Read selection
//    aloud" appears in the actual chat panel. The selected text itself is
//    not exposed by the menu API, so the command reads the X11 primary
//    selection, which the highlight already populated.
//
// The read-along view opens in this extension's own webview panel (an iframe
// around the local reader page) rather than VS Code's Simple Browser. Simple
// Browser tabs are always titled "Simple Browser"; an owned panel can carry
// the real name — "<project> — <session title>" — which is what makes several
// read-along tabs tellable apart. The page sends its title up to the wrapper
// with postMessage, and the wrapper forwards it here to rename the tab.

const vscode = require('vscode');
const cp = require('child_process');
const fs = require('fs');
const path = require('path');
const os = require('os');

const PLUGIN_ROOT = path.join(os.homedir(), '.claude', 'plugins', 'claude-code-tts');
const TTS_CTL = path.join(PLUGIN_ROOT, 'bin', 'tts-ctl');
const OPEN_FLAG = path.join(PLUGIN_ROOT, 'reader.open');
const URL_FILE = path.join(PLUGIN_ROOT, 'reader.url');
const POLL_MS = 700;

// A flag whose project directory is open in no window at all may be consumed
// by any window after this long, so the view still appears somewhere.
const UNCLAIMED_MS = 5000;

function runTtsCtl(args) {
  cp.execFile(TTS_CTL, args, { env: process.env }, (err, stdout, stderr) => {
    if (err) {
      vscode.window.showWarningMessage(
        'claude-code-tts: ' + (String(stderr || '').trim() || err.message)
      );
    }
  });
}

function readerUrl() {
  try {
    const url = fs.readFileSync(URL_FILE, 'utf8').trim().split('\n')[0];
    if (url) return url;
  } catch (e) {
    // No reader has run yet; fall through to the default address.
  }
  return 'http://127.0.0.1:8898/';
}

// canonDir puts a directory path into a comparable form: symlinks resolved
// (a shell's logical $PWD and VS Code's folder path can name the same place
// through different links), and case folded on the case-insensitive
// platforms (Windows drive letters arrive in both spellings; macOS default
// filesystems ignore case).
function canonDir(p) {
  let out;
  try {
    out = fs.realpathSync.native ? fs.realpathSync.native(p) : fs.realpathSync(p);
  } catch (e) {
    out = path.resolve(p);
  }
  if (process.platform === 'win32' || process.platform === 'darwin') out = out.toLowerCase();
  return out + path.sep;
}

// workspaceOwns reports whether this window has the given directory open:
// the directory sits inside one of the workspace folders, or a workspace
// folder sits inside it.
function workspaceOwns(dir) {
  const folders = vscode.workspace.workspaceFolders || [];
  const target = canonDir(dir);
  return folders.some((f) => {
    const folder = canonDir(f.uri.fsPath);
    return target.startsWith(folder) || folder.startsWith(target);
  });
}

// One panel per page URL: reopening a session reveals its existing tab
// instead of stacking duplicates.
const panels = new Map();

function openReader(url) {
  const target = url || readerUrl();
  const existing = panels.get(target);
  if (existing) {
    existing.reveal(undefined, false);
    return;
  }
  let port = 8898;
  try { port = Number(new URL(target).port) || 80; } catch (e) { /* keep the default */ }
  const panel = vscode.window.createWebviewPanel(
    'claudeTtsReader',
    'TTS Read Along',
    vscode.ViewColumn.Active,
    {
      enableScripts: true,
      // Keep the iframe (and any playing audio) alive when the tab is hidden.
      retainContextWhenHidden: true,
      portMapping: [{ webviewPort: port, extensionHostPort: port }],
    }
  );
  panels.set(target, panel);
  panel.onDidDispose(() => panels.delete(target));
  panel.webview.onDidReceiveMessage((msg) => {
    if (msg && msg.type === 'title' && typeof msg.title === 'string' && msg.title.trim()) {
      panel.title = msg.title.slice(0, 100);
    }
  });
  panel.webview.html = wrapperHtml(target);
}

// wrapperHtml is the whole webview: an iframe filling the tab, plus a relay
// that forwards the page's title messages to the extension host. The iframe
// carries allow="autoplay" so the Auto-read feature can start clips without a
// fresh user gesture; the sandbox list matches what VS Code's own Simple
// Browser grants a framed page.
function wrapperHtml(target) {
  const src = target.replace(/"/g, '&quot;');
  return [
    '<!DOCTYPE html><html><head><meta charset="utf-8">',
    '<meta http-equiv="Content-Security-Policy" content="default-src \'none\'; ',
    'frame-src http://127.0.0.1:* http://localhost:*; script-src \'unsafe-inline\'; style-src \'unsafe-inline\'">',
    '<style>html,body{margin:0;padding:0;height:100%;overflow:hidden}iframe{border:0;width:100%;height:100%}</style>',
    '</head><body>',
    '<iframe src="' + src + '" allow="autoplay" ',
    'sandbox="allow-scripts allow-same-origin allow-forms allow-downloads allow-modals"></iframe>',
    '<script>',
    'const vscodeApi = acquireVsCodeApi();',
    'window.addEventListener("message", (e) => {',
    '  const d = e.data;',
    '  if (d && d.type === "ttsread-title" && typeof d.title === "string") {',
    '    vscodeApi.postMessage({ type: "title", title: d.title });',
    '  }',
    '});',
    '</script></body></html>',
  ].join('');
}

function activate(context) {
  context.subscriptions.push(
    vscode.commands.registerCommand('claudeTts.readSelection', () => {
      const editor = vscode.window.activeTextEditor;
      if (editor && !editor.selection.isEmpty) {
        // Real editors expose their selection through the API — use it
        // directly so this also works on Wayland and macOS.
        runTtsCtl(['say', editor.document.getText(editor.selection)]);
        return;
      }
      // Webviews (the Claude Code chat panel) do not expose selections, but
      // highlighting already placed the text in the X11 primary selection.
      runTtsCtl(['selection']);
    })
  );

  context.subscriptions.push(
    vscode.commands.registerCommand('claudeTts.stop', () => runTtsCtl(['stop']))
  );

  context.subscriptions.push(
    vscode.commands.registerCommand('claudeTts.openReader', () => openReader())
  );

  // Zero-click bridge, one flag file seen by EVERY open window. The launcher
  // writes two lines: the page URL and the project directory it ran in. Only
  // the window that has that directory open consumes the flag, so the view
  // appears where the user is working — without the scoping, whichever
  // window polled first would win and the tab would pop up in the wrong
  // window (that was v0.1.0's behavior, and its bug).
  let lastMtime = 0;

  // claimAndOpen takes the flag atomically: rename() succeeds in exactly one
  // window even when several decided to consume in the same poll phase, so
  // the view can never open twice.
  const claimAndOpen = (st) => {
    const claimed = OPEN_FLAG + '.' + process.pid;
    fs.rename(OPEN_FLAG, claimed, (err) => {
      if (err) return; // another window won the claim
      lastMtime = st.mtimeMs;
      fs.readFile(claimed, 'utf8', (readErr, data) => {
        fs.unlink(claimed, () => {});
        if (readErr) return;
        const lines = String(data).split('\n').map((l) => l.trim()).filter(Boolean);
        openReader(lines[0] || undefined);
      });
    });
  };

  // A flag that predates activation is normally stale — reloading a window
  // must never pop the view uninvited. The one exception: a FRESH flag for a
  // project THIS window owns means the window reloaded in the moment between
  // the launcher writing the flag and the poller seeing it (typical right
  // after installing the extension); the user asked for that view, so open
  // it.
  try {
    const st = fs.statSync(OPEN_FLAG);
    const lines = fs.readFileSync(OPEN_FLAG, 'utf8').split('\n').map((l) => l.trim()).filter(Boolean);
    const dir = lines[1] || '';
    const fresh = Date.now() - st.mtimeMs < 2 * 60 * 1000;
    if (fresh && dir && workspaceOwns(dir)) {
      claimAndOpen(st);
    } else {
      lastMtime = st.mtimeMs;
    }
  } catch (e) {
    lastMtime = 0;
  }

  const timer = setInterval(() => {
    fs.stat(OPEN_FLAG, (err, st) => {
      if (err || st.mtimeMs <= lastMtime) return;
      fs.readFile(OPEN_FLAG, 'utf8', (readErr, data) => {
        if (readErr) return;
        const lines = String(data).split('\n').map((l) => l.trim()).filter(Boolean);
        const dir = lines[1] || '';
        const mine = dir && workspaceOwns(dir);
        const unclaimed = Date.now() - st.mtimeMs > UNCLAIMED_MS;
        // A flag naming a directory some other window owns is left alone —
        // unless it has sat unclaimed long enough that no owner seems to
        // exist. A flag with no directory (an old launcher) is taken as-is.
        if (dir && !mine && !unclaimed) return;
        claimAndOpen(st);
      });
    });
  }, POLL_MS);
  context.subscriptions.push({ dispose: () => clearInterval(timer) });
}

function deactivate() {}

module.exports = { activate, deactivate };
