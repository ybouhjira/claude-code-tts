// Read-along view: renders the conversation, plays TTS audio in the browser,
// and highlights the sentence and word being spoken.
//
// How the karaoke timing works: the page asks the server for audio one
// sentence at a time, so the sentence highlight is exact by construction.
// Word timing inside a sentence is estimated by sharing the clip's duration
// across the words in proportion to their length. No provider returns word
// timestamps through the plain speech endpoint, and the estimate tracks real
// speech closely enough to follow with the eye.
'use strict';

const $ = (sel) => document.querySelector(sel);
const messagesEl = $('#messages');

// The page is bound to one session through the ?s=<id> parameter in its URL,
// so several tabs can read several sessions from the same server. A URL with
// no session id shows the navigator instead: every project on this machine
// with its sessions, newest first.
const SESSION = new URLSearchParams(location.search).get('s') || '';

// api() stamps the session id onto a server call so the server knows which
// transcript the request is about.
function api(path) {
  return SESSION ? path + '?s=' + encodeURIComponent(SESSION) : path;
}

// setPageTitle names this view everywhere a name is shown: the browser tab
// via document.title, and — when the page runs inside the VS Code bridge
// extension's editor tab (an iframe in a webview) — the tab label, which the
// wrapper renames when it receives this message.
function setPageTitle(title) {
  document.title = title;
  try {
    if (window.parent !== window) window.parent.postMessage({ type: 'ttsread-title', title }, '*');
  } catch (e) { /* sandboxed parent; the in-page name still shows */ }
}

// applyMeta labels the page as "<project> — <session title>" from the fields
// the server adds to /api/messages. Titles grow with the session, so this
// runs on every refresh, not just at boot.
function applyMeta(data) {
  const label = [data.project, data.title].filter(Boolean).join(' — ');
  if (!label) return; // never clobber a good label with an empty one
  setPageTitle(label);
  $('#session-name').textContent = label;
  $('#session-name').title = label;
}

const SPEEDS = [0.5, 0.75, 0.9, 1.0, 1.1, 1.25, 1.5, 1.75, 2.0];
const MAX_SENTENCE_CHARS = 300; // one TTS request per sentence; keep them small
const CACHE_MAX_CLIPS = 60;

const state = {
  cfg: null,
  messages: [],
  queue: [],          // [{el, text}] every readable sentence, in document order
  qIndex: 0,          // current/next sentence to read (the resume point)
  endIndex: Infinity, // playRange stops after this queue index
  playing: false,
  paused: false,
  selReading: false,
  skipDelta: 0,       // set by prev/next while a clip plays
  audio: null,
  stopClip: null,     // ends the current clip early; set while a clip plays
  playGen: 0,         // bumping this cancels any in-flight play loop
  cache: new Map(),   // ttsKey -> Promise<blob URL>
  cacheOrder: [],
  pendingReplace: null, // full message list to re-render once playback stops
};

const settings = {
  provider: '',
  voice: '',
  speed: 1.0,
  follow: true,
  autoread: false,
  interim: true, // style Claude's working notes as italic bullets
};

/* ---------------- settings persistence ---------------- */

function loadSettings() {
  try {
    const raw = localStorage.getItem('ttsread.settings');
    if (raw) Object.assign(settings, JSON.parse(raw));
  } catch (e) { /* ignore a corrupt store */ }
}

function saveSettings() {
  try { localStorage.setItem('ttsread.settings', JSON.stringify(settings)); } catch (e) { /* ignore */ }
}

function savedVoiceFor(provider) {
  try { return localStorage.getItem('ttsread.voice.' + provider) || ''; } catch (e) { return ''; }
}

function saveVoiceFor(provider, voice) {
  try { localStorage.setItem('ttsread.voice.' + provider, voice); } catch (e) { /* ignore */ }
}

/* ---------------- small DOM helpers ---------------- */

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

let toastTimer = 0;
function toast(msg) {
  const t = $('#toast');
  t.textContent = msg;
  t.classList.add('show');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.remove('show'), 5000);
}

function setStatus(msg) {
  $('#status-text').textContent = msg;
}

function fmtTime(iso) {
  try {
    const d = new Date(iso);
    if (isNaN(d.getTime())) return '';
    return d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
  } catch (e) { return ''; }
}

/* ---------------- text processing ---------------- */

// Inline Markdown is parsed, not stripped: the chat panel shows `code` as a
// chip and keeps **bold** bold, and the read-along view should look the same.
// tokenizeInline turns raw text into segments whose text is CLEAN (markers
// removed) — the clean text is what gets spoken and sentence-split — while
// each segment's classes carry the styling to the word spans.
const INLINE_TOKENS = [
  { re: /^!\[[^\]]*\]\([^)]*\)/, cls: null },                    // image: dropped
  { re: /^\[([^\]]*)\]\([^)]*\)/, cls: 'md-link', inner: 1 },    // link: keep its text
  { re: /^(`+)([^`]+)\1/, cls: 'md-code', inner: 2, flat: true }, // no nesting inside code
  { re: /^\*\*\*([^*]+)\*\*\*/, cls: 'md-b md-i', inner: 1 },
  { re: /^\*\*((?:[^*]|\*(?!\*))+)\*\*/, cls: 'md-b', inner: 1 },
  { re: /^__((?:[^_]|_(?!_))+)__/, cls: 'md-b', inner: 1 },
  { re: /^~~([^~]+)~~/, cls: 'md-s', inner: 1 },
];
// Single-marker emphasis only opens at a word boundary and closes before one,
// so identifiers like snake_case or a lone asterisk stay plain text.
const ITALIC_TOKENS = [
  { re: /^\*([^*\s](?:[^*]*[^*\s])?)\*/, cls: 'md-i', inner: 1 },
  { re: /^_([^_\s](?:[^_]*[^_\s])?)_/, cls: 'md-i', inner: 1 },
];
const ITALIC_BEFORE = /[\s(["'—-]/;
const ITALIC_AFTER = /[\s.,;:!?)\]"']/;

function tokenizeInline(src) {
  const out = [];
  let plain = '';
  const flush = () => { if (plain) { out.push({ text: plain, cls: '' }); plain = ''; } };
  let i = 0;
  let prev = ''; // the character before position i, for italic word boundaries
  while (i < src.length) {
    const rest = src.slice(i);
    let m = null;
    let tok = null;
    for (const t of INLINE_TOKENS) {
      m = rest.match(t.re);
      if (m) { tok = t; break; }
    }
    if (!m && (prev === '' || ITALIC_BEFORE.test(prev))) {
      for (const t of ITALIC_TOKENS) {
        const mm = rest.match(t.re);
        if (!mm) continue;
        const after = src[i + mm[0].length];
        if (after === undefined || ITALIC_AFTER.test(after)) { m = mm; tok = t; break; }
      }
    }
    if (!m) { plain += src[i]; prev = src[i]; i += 1; continue; }
    flush();
    if (tok.cls !== null) {
      const innerText = m[tok.inner];
      if (tok.flat) {
        out.push({ text: innerText, cls: tok.cls });
      } else {
        // Recurse so **bold with `code` inside** styles both.
        for (const seg of tokenizeInline(innerText)) {
          out.push({ text: seg.text, cls: seg.cls ? seg.cls + ' ' + tok.cls : tok.cls });
        }
      }
    }
    prev = m[0][m[0].length - 1];
    i += m[0].length;
  }
  flush();
  return out;
}

// inlineRuns flattens the styled segments into one whitespace-normalized
// clean string plus style ranges over it. Sentence splitting and speech run
// on the clean string exactly as before; the ranges re-attach the styling to
// the word spans afterwards.
function inlineRuns(raw) {
  let clean = '';
  const runs = [];
  for (const seg of tokenizeInline(raw)) {
    const start = clean.length;
    for (const ch of seg.text) {
      if (/\s/.test(ch)) {
        if (clean && !clean.endsWith(' ')) clean += ' ';
      } else {
        clean += ch;
      }
    }
    if (seg.cls && clean.length > start) runs.push({ start, end: clean.length, cls: seg.cls });
  }
  return { clean, runs };
}

// Words that end with a period but usually do not end a sentence.
const ABBREV_RE = /(?:\b(?:e\.g|i\.e|etc|vs|cf|Mr|Mrs|Ms|Dr|Prof|St|No|Fig|approx)|\b[A-Z])\.$/;

function splitSentences(text) {
  const out = [];
  let buf = '';
  for (let piece of text.split(/(?<=[.!?…])\s+/)) {
    piece = piece.trim();
    if (!piece) continue;
    buf = buf ? buf + ' ' + piece : piece;
    if (ABBREV_RE.test(buf)) continue; // likely an abbreviation; keep joining
    out.push(buf);
    buf = '';
  }
  if (buf) out.push(buf);

  // Cut any monster sentence so a single TTS request stays quick.
  const final = [];
  for (const s of out) {
    let rest = s;
    while (rest.length > MAX_SENTENCE_CHARS) {
      let cut = rest.lastIndexOf(', ', MAX_SENTENCE_CHARS);
      if (cut < 80) cut = rest.lastIndexOf(' ', MAX_SENTENCE_CHARS);
      if (cut < 80) cut = MAX_SENTENCE_CHARS;
      final.push(rest.slice(0, cut + 1).trim());
      rest = rest.slice(cut + 1).trim();
    }
    if (rest) final.push(rest);
  }
  return final;
}

/* ---------------- rendering ---------------- */

// makeSentenceSpan builds one clickable sentence. runs/base carry the inline
// styling: base is where this sentence starts in the paragraph's clean text,
// and any style range overlapping a word puts its classes on that word span.
function makeSentenceSpan(sentence, runs, base) {
  const span = el('span', 'sent');
  span.dataset.t = sentence;
  span.title = 'Click to read from here';
  let pos = 0;
  for (const token of sentence.split(/(\s+)/)) {
    if (!token) continue;
    if (/^\s+$/.test(token)) {
      span.append(token);
    } else {
      const w = el('span', 'w', token);
      if (runs && runs.length && base >= 0) {
        const a = base + pos;
        const b = a + token.length;
        const cls = new Set();
        for (const r of runs) {
          if (r.start < b && r.end > a) r.cls.split(' ').forEach((c) => cls.add(c));
        }
        if (cls.size) w.className = 'w ' + [...cls].join(' ');
      }
      span.append(w);
    }
    pos += token.length;
  }
  return span;
}

// appendSentences fills a block element with sentence spans separated by
// spaces. The raw Markdown is parsed once: the clean text drives sentence
// splitting and speech, the style ranges drive the display styling.
function appendSentences(block, raw) {
  const { clean, runs } = inlineRuns(raw);
  const sentences = splitSentences(clean);
  let cursor = 0;
  sentences.forEach((s, i) => {
    if (i > 0) block.append(' ');
    // Every sentence is a substring of the clean text, so a moving indexOf
    // recovers its offset — which is what anchors the style ranges.
    const at = clean.indexOf(s, cursor);
    if (at >= 0) cursor = at + s.length;
    block.append(makeSentenceSpan(s, runs, at));
  });
}

/* ---------------- code blocks: header, copy, highlighting ---------------- */

// Keyword sets for the languages that actually show up in Claude sessions.
// The aim is readable color, not a full grammar: comments, strings, numbers,
// and keywords cover most of what the eye scans for.
const CODE_KEYWORDS = {
  go: 'break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var nil true false iota',
  js: 'async await break case catch class const continue debugger default delete do else export extends finally for from function if import in instanceof interface let new of return static super switch this throw try type typeof var void while with yield null undefined true false',
  python: 'and as assert async await break class continue def del elif else except finally for from global if import in is lambda match nonlocal not or pass raise return try while with yield None True False self',
  bash: 'if then else elif fi for while until do done case esac function in select time echo exit return local export readonly shift source set unset trap sudo cd',
  rust: 'as async await break const continue crate dyn else enum extern fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait type unsafe use where while true false',
  clike: 'auto break case catch class const continue default delete do else enum explicit extern final finally for goto if implements import instanceof int char float double long short signed unsigned bool void namespace new operator private protected public return sizeof static struct switch template this throw throws try typedef union using virtual volatile while null nullptr true false',
  sql: 'select from where insert into update delete set values create table index view drop alter join left right inner outer on group by order having limit offset as and or not null in is distinct union all between like exists primary key foreign references default',
  json: 'true false null',
  yaml: 'true false null',
};

// codeLangDef maps a fence's info string to a highlighting definition:
// which line/block comments exist and which keyword set applies.
function codeLangDef(lang) {
  const alias = {
    javascript: 'js', typescript: 'js', ts: 'js', jsx: 'js', tsx: 'js', node: 'js',
    sh: 'bash', shell: 'bash', zsh: 'bash', console: 'bash', makefile: 'bash', make: 'bash',
    py: 'python', golang: 'go', yml: 'yaml', rs: 'rust',
    c: 'clike', cpp: 'clike', 'c++': 'clike', h: 'clike', java: 'clike', cs: 'clike', kotlin: 'clike', swift: 'clike',
  };
  const l = alias[lang] || lang;
  const defs = {
    go: { line: '//', block: true, kw: CODE_KEYWORDS.go },
    js: { line: '//', block: true, kw: CODE_KEYWORDS.js },
    rust: { line: '//', block: true, kw: CODE_KEYWORDS.rust },
    clike: { line: '//', block: true, kw: CODE_KEYWORDS.clike },
    css: { line: null, block: true, kw: '' },
    python: { line: '#', block: false, kw: CODE_KEYWORDS.python },
    bash: { line: '#', block: false, kw: CODE_KEYWORDS.bash },
    yaml: { line: '#', block: false, kw: CODE_KEYWORDS.yaml },
    toml: { line: '#', block: false, kw: CODE_KEYWORDS.yaml },
    json: { line: null, block: false, kw: CODE_KEYWORDS.json },
    sql: { line: '--', block: true, kw: CODE_KEYWORDS.sql },
    html: { line: null, block: false, kw: '', htmlComment: true },
    xml: { line: null, block: false, kw: '', htmlComment: true },
  };
  return defs[l] || null;
}

// highlightDiff colors a diff/patch per line: additions, deletions, hunk
// headers, and file headers.
function highlightDiff(text) {
  const frag = document.createDocumentFragment();
  text.split('\n').forEach((line, i) => {
    if (i) frag.append('\n');
    let cls = '';
    if (/^@@/.test(line)) cls = 'tok-hunk';
    else if (/^(diff |index |--- |\+\+\+ )/.test(line)) cls = 'tok-com';
    else if (/^\+/.test(line)) cls = 'tok-add';
    else if (/^-/.test(line)) cls = 'tok-del';
    if (cls) frag.append(el('span', cls, line));
    else frag.append(line);
  });
  return frag;
}

// highlightCode tokenizes code into colored spans. Everything is built with
// createElement/textContent — transcript content can never inject markup. An
// unknown language comes back as plain text.
function highlightCode(text, lang) {
  if (lang === 'diff' || lang === 'patch') return highlightDiff(text);
  const frag = document.createDocumentFragment();
  const def = codeLangDef(lang);
  if (!def) {
    frag.append(text);
    return frag;
  }
  const kw = new Set((def.kw || '').split(/\s+/).filter(Boolean));
  // SQL is conventionally written in either case; its keyword set is stored
  // lowercase, so fold tokens for the lookup.
  const foldCase = def.kw === CODE_KEYWORDS.sql;
  // Alternation order is the precedence: comments beat strings beat numbers
  // beat words. Strings tolerate a missing closing quote so one typo does
  // not discolor the rest of the block.
  const parts = [];
  if (def.htmlComment) parts.push('<!--[\\s\\S]*?(?:-->|$)');
  if (def.block) parts.push('\\/\\*[\\s\\S]*?(?:\\*\\/|$)');
  if (def.line === '//') parts.push('\\/\\/[^\\n]*');
  if (def.line === '--') parts.push('--[^\\n]*');
  if (def.line === '#') parts.push('#[^\\n]*');
  parts.push('"(?:\\\\.|[^"\\\\\\n])*"?');
  parts.push("'(?:\\\\.|[^'\\\\\\n])*'?");
  parts.push('`(?:\\\\.|[^`\\\\])*`?');
  parts.push('\\b\\d[\\w.]*\\b');
  parts.push('[A-Za-z_$][\\w$]*');
  const re = new RegExp(parts.join('|'), 'g');
  let last = 0;
  let m;
  while ((m = re.exec(text))) {
    if (m.index > last) frag.append(text.slice(last, m.index));
    const tok = m[0];
    const c = tok[0];
    let cls = '';
    if (tok.startsWith('/*') || tok.startsWith('<!--') ||
        (def.line === '//' && tok.startsWith('//')) ||
        (def.line === '--' && tok.startsWith('--')) ||
        (def.line === '#' && c === '#')) cls = 'tok-com';
    else if (c === '"' || c === "'" || c === '`') cls = 'tok-str';
    else if (c >= '0' && c <= '9') cls = 'tok-num';
    else if (kw.has(tok) || (foldCase && kw.has(tok.toLowerCase()))) cls = 'tok-kw';
    if (cls) frag.append(el('span', cls, tok));
    else frag.append(tok);
    last = m.index + tok.length;
  }
  if (last < text.length) frag.append(text.slice(last));
  return frag;
}

// renderCodeBlock builds the full code block: a header bar with the fence's
// language, the "not read aloud" hint, and a copy button, above the
// highlighted code itself. Continuous reading skips it because it contains
// no sentence spans.
function renderCodeBlock(text, lang) {
  const wrap = el('div', 'codeblock');
  const head = el('div', 'code-head');
  head.append(el('span', 'code-lang', lang || 'code'));
  head.append(el('span', 'code-note', 'not read aloud'));
  const copyBtn = el('button', 'code-copy', 'Copy');
  copyBtn.title = 'Copy this code block';
  copyBtn.addEventListener('click', () => {
    navigator.clipboard.writeText(text).then(
      () => {
        copyBtn.textContent = 'Copied ✓';
        setTimeout(() => { copyBtn.textContent = 'Copy'; }, 1500);
      },
      () => toast('Could not copy — the clipboard is unavailable here')
    );
  });
  head.append(copyBtn);
  const pre = el('pre', 'code');
  pre.append(highlightCode(text, (lang || '').toLowerCase()));
  wrap.append(head, pre);
  return wrap;
}

// renderBody converts one message's Markdown-ish text into readable blocks.
// Fenced code becomes a highlighted code block that continuous reading
// skips; everything else becomes paragraphs of clickable sentence spans.
// Built entirely with createElement/textContent so transcript content can
// never inject markup.
function renderBody(text) {
  const body = el('div', 'msg-body');
  const lines = text.split('\n');
  let para = [];
  let code = null; // {lines, lang, fence} while inside a fence

  const flushPara = () => {
    if (!para.length) return;
    const p = el('p');
    appendSentences(p, para.join(' '));
    body.append(p);
    para = [];
  };

  // The info string after the fence (```go) names the language. A block only
  // closes on a fence of the same character at least as long as the opener,
  // so a ``` inside a ~~~ block stays part of the code.
  const fenceRe = /^\s*(`{3,}|~{3,})\s*([\w+#.-]*)/;

  for (const rawLine of lines) {
    const line = rawLine;
    const m = line.match(fenceRe);
    if (code !== null) {
      if (m && m[1][0] === code.fence[0] && m[1].length >= code.fence.length) {
        body.append(renderCodeBlock(code.lines.join('\n'), code.lang));
        code = null;
      } else {
        code.lines.push(line);
      }
      continue;
    }
    if (m) { flushPara(); code = { lines: [], lang: (m[2] || '').toLowerCase(), fence: m[1] }; continue; }

    const trimmed = line.trim();
    if (trimmed === '') { flushPara(); continue; }

    const heading = trimmed.match(/^(#{1,6})\s+(.*)$/);
    if (heading) {
      flushPara();
      const level = heading[1].length; // 1..6, each with its own size in CSS
      const h = el('p', 'h h' + level);
      appendSentences(h, heading[2]);
      body.append(h);
      continue;
    }
    const bullet = trimmed.match(/^[-*+]\s+(.*)$/);
    const numbered = trimmed.match(/^(\d+)[.)]\s+(.*)$/);
    if (bullet || numbered) {
      flushPara();
      const li = el('p', numbered ? 'li num' : 'li');
      const content = bullet ? bullet[1] : numbered[1] + '. ' + numbered[2];
      appendSentences(li, content);
      body.append(li);
      continue;
    }
    para.push(trimmed);
  }
  if (code !== null && code.lines.length) { // unterminated fence at end of message
    body.append(renderCodeBlock(code.lines.join('\n'), code.lang));
  }
  flushPara();
  return body;
}

// renderTurnBody renders one turn's text. An assistant turn that was
// interrupted by tool runs arrives with msg.parts: every part but the last is
// a "working note" written before a tool ran, and the last part is the final
// answer. When the "Notes as bullets" toggle is on, the notes are grouped in
// a visually quieter block (italic, bulleted) above the final answer. With
// the toggle off — or for any single-part turn — the whole text renders the
// classic way.
function renderTurnBody(msg) {
  const split = msg.role === 'assistant' && settings.interim &&
    Array.isArray(msg.parts) && msg.parts.length > 1;
  if (!split) return renderBody(msg.text);
  const body = el('div', 'msg-body');
  const notes = el('div', 'interim');
  notes.append(el('div', 'interim-label', 'working notes'));
  for (const part of msg.parts.slice(0, -1)) notes.append(renderBody(part));
  body.append(notes, renderBody(msg.parts[msg.parts.length - 1]));
  return body;
}

// roleLabel names a card's author. The 'doc' role is a Markdown or text file
// served whole (tts-ctl read FILE); it is one "message" but not a chat turn.
function roleLabel(role) {
  if (role === 'user') return 'You';
  if (role === 'doc') return 'Document';
  return 'Claude';
}

function renderMessage(msg, index) {
  const card = el('article', 'message ' + msg.role);
  card.dataset.mi = index;
  const head = el('div', 'msg-head');
  head.append(el('span', 'role', roleLabel(msg.role)));
  // A document's timestamp is its file mtime; showing it as a clock time
  // would read like a chat time, so only real turns show one.
  const t = msg.role === 'doc' ? '' : fmtTime(msg.timestamp);
  if (t) head.append(el('span', 'time', t));
  const readBtn = el('button', 'msg-read', '🔊 Read');
  readBtn.title = 'Read this message aloud';
  readBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    readCard(card);
  });
  head.append(readBtn);
  card.append(head, renderTurnBody(msg));
  return card;
}

function renderAll(msgs) {
  messagesEl.textContent = '';
  if (!msgs.length) {
    const p = el('p', '', 'Nothing to read yet — say something in the session.');
    p.id = 'empty';
    messagesEl.append(p);
  }
  msgs.forEach((m, i) => messagesEl.append(renderMessage(m, i)));
  rebuildQueue();
}

// rebuildQueue re-collects every readable sentence. Cards are append-only on
// live updates, so the element the play loop is on keeps existing and we can
// re-find its position by identity.
function rebuildQueue() {
  const currentEl = state.queue[state.qIndex] ? state.queue[state.qIndex].el : null;
  state.queue = [...messagesEl.querySelectorAll('.sent')].map((s) => ({ el: s, text: s.dataset.t }));
  if (currentEl) {
    const i = state.queue.findIndex((q) => q.el === currentEl);
    if (i >= 0) state.qIndex = i;
  }
  if (state.qIndex >= state.queue.length) state.qIndex = 0;
  updateButtons();
}

function queueIndexOf(sentEl) {
  return state.queue.findIndex((q) => q.el === sentEl);
}

/* ---------------- audio: fetch + cache ---------------- */

function ttsKey(text) {
  return settings.provider + '|' + settings.voice + '|' + text;
}

function fetchClip(text) {
  const key = ttsKey(text);
  if (state.cache.has(key)) return state.cache.get(key);
  const p = fetch(api('/api/tts'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ text, provider: settings.provider, voice: settings.voice }),
  }).then(async (r) => {
    if (!r.ok) {
      let msg = 'TTS request failed (' + r.status + ')';
      try { msg = (await r.json()).error || msg; } catch (e) { /* keep default */ }
      throw new Error(msg);
    }
    return URL.createObjectURL(await r.blob());
  });
  // Do not cache failures, or one glitch would mute a sentence forever.
  p.catch(() => {
    state.cache.delete(key);
    const i = state.cacheOrder.indexOf(key);
    if (i >= 0) state.cacheOrder.splice(i, 1);
  });
  state.cache.set(key, p);
  state.cacheOrder.push(key);
  while (state.cacheOrder.length > CACHE_MAX_CLIPS) {
    const old = state.cacheOrder.shift();
    const q = state.cache.get(old);
    state.cache.delete(old);
    if (q) q.then((u) => URL.revokeObjectURL(u)).catch(() => {});
  }
  return p;
}

/* ---------------- audio: one shared, unlocked element ---------------- */

// Inside VS Code's Simple Browser the page lives in a cross-origin iframe,
// where a click grants only a short-lived permission to start audio. A fresh
// Audio element per sentence therefore worked for the first clip and was
// blocked on the next one, halting playback after every sentence. The fix is
// the standard unlock pattern: ONE persistent element, primed by the first
// real user gesture with a silent clip, and reused for every sentence.
// Chromium remembers the permission per element, so the shared element keeps
// playing with no further gestures.
const SILENT_CLIP =
  'data:audio/wav;base64,UklGRigAAABXQVZFZm10IBAAAAABAAEAQB8AAEAfAAABAAgAZGF0YQQAAACAgICA';
const player = new Audio();

function unlockAudio() {
  // If a clip is already playing (or paused mid-clip), the element is
  // necessarily unlocked; priming now would clobber its src.
  if (state.playing || state.selReading || !player.paused) return;
  player.src = SILENT_CLIP;
  const p = player.play();
  if (p) p.then(() => player.pause()).catch(() => { /* gesture too stale; the next one retries */ });
}
document.addEventListener('pointerdown', unlockAudio, true);
document.addEventListener('keydown', unlockAudio, true);

// armAutoResume makes a block self-healing: the next click both unlocks the
// element (via unlockAudio above) and restarts reading where it stopped.
function armAutoResume(start, end) {
  const once = () => {
    document.removeEventListener('pointerdown', once, true);
    // Defer so that if this click also targeted a sentence or a button, that
    // handler runs last and wins (it bumps playGen).
    setTimeout(() => { if (!state.playing && !state.selReading) playRange(start, end); }, 0);
  };
  document.addEventListener('pointerdown', once, true);
}

/* ---------------- audio: playback with karaoke highlight ---------------- */

// playClip plays one clip and keeps the word highlight moving. Resolves with
// 'ended', 'stopped' (user action), or 'blocked' (autoplay policy).
function playClip(url, sentEl) {
  return new Promise((resolve) => {
    const audio = player;
    audio.src = url;
    state.audio = audio;
    audio.playbackRate = settings.speed;

    let words = [];
    let starts = [];
    let raf = 0;
    if (sentEl) {
      sentEl.classList.add('s-active');
      if (settings.follow) sentEl.scrollIntoView({ behavior: 'smooth', block: 'center' });
      words = [...sentEl.querySelectorAll('.w')];
    }

    const computeStarts = () => {
      const d = audio.duration;
      if (!isFinite(d) || d <= 0 || !words.length) return;
      const weights = words.map((w) => w.textContent.length + 1);
      const total = weights.reduce((a, b) => a + b, 0);
      let acc = 0;
      starts = weights.map((wt) => {
        const at = (d * acc) / total;
        acc += wt;
        return at;
      });
    };
    // Property handlers, not addEventListener: each clip replaces the previous
    // clip's handlers on the shared element instead of stacking on them.
    audio.onloadedmetadata = computeStarts;

    const highlightNow = () => {
      if (!starts.length) return;
      const t = audio.currentTime;
      let idx = 0;
      for (let k = 0; k < starts.length; k++) {
        if (starts[k] <= t) idx = k; else break;
      }
      for (let k = 0; k < words.length; k++) {
        words[k].classList.toggle('w-active', k === idx);
      }
    };
    const tick = () => { highlightNow(); raf = requestAnimationFrame(tick); };
    raf = requestAnimationFrame(tick);
    // rAF stops in background tabs; timeupdate keeps the highlight roughly
    // moving there so the page is not frozen mid-sentence when you tab back.
    audio.ontimeupdate = highlightNow;

    let done = false;
    const finish = (how) => {
      if (done) return;
      done = true;
      cancelAnimationFrame(raf);
      if (sentEl) {
        sentEl.classList.remove('s-active');
        words.forEach((w) => w.classList.remove('w-active'));
      }
      audio.pause();
      audio.onloadedmetadata = audio.ontimeupdate = audio.onended = audio.onerror = null;
      state.audio = null;
      state.stopClip = null;
      resolve(how);
    };
    state.stopClip = () => finish('stopped');
    audio.onended = () => finish('ended');
    audio.onerror = () => finish('ended');
    audio.play().catch((err) => {
      if (err && err.name === 'NotAllowedError') {
        toast('The browser blocked audio. Click anywhere on the page and reading will resume.');
        finish('blocked');
      } else {
        // A decode or abort problem with this one clip; skip the sentence
        // instead of halting the whole read.
        finish('ended');
      }
    });
  });
}

// playRange reads queue entries start..end (inclusive). It is the single
// playback loop; bumping state.playGen cancels a running loop instantly.
async function playRange(start, end) {
  stopSelectionOnly();
  const gen = ++state.playGen;
  if (state.stopClip) state.stopClip();
  if (!state.queue.length) return;

  state.playing = true;
  state.paused = false;
  state.qIndex = Math.min(Math.max(start, 0), state.queue.length - 1);
  state.endIndex = end;
  updateButtons();

  while (state.playing && gen === state.playGen && state.qIndex < state.queue.length && state.qIndex <= state.endIndex) {
    const item = state.queue[state.qIndex];
    setStatus('Reading sentence ' + (state.qIndex + 1) + ' of ' + state.queue.length + '…');
    let url;
    try {
      url = await fetchClip(item.text);
    } catch (e) {
      toast(e.message);
      break;
    }
    if (!state.playing || gen !== state.playGen) return;
    const next = state.queue[state.qIndex + 1];
    if (next && state.qIndex + 1 <= state.endIndex) fetchClip(next.text).catch(() => {});
    const how = await playClip(url, item.el);
    if (gen !== state.playGen) return;
    if (how === 'blocked') { armAutoResume(state.qIndex, state.endIndex); break; }
    if (how === 'stopped' && state.skipDelta === 0) break; // explicit stop; qIndex stays as resume point
    const delta = state.skipDelta || 1;
    state.skipDelta = 0;
    state.qIndex = Math.max(0, state.qIndex + delta);
  }

  if (gen === state.playGen) {
    state.playing = false;
    state.paused = false;
    state.endIndex = Infinity;
    updateButtons();
    setStatus('Idle');
    applyPendingReplace();
  }
}

function playFrom(i) { playRange(i, Infinity); }

function readCard(card) {
  const sents = [...card.querySelectorAll('.sent')];
  if (!sents.length) return;
  const start = queueIndexOf(sents[0]);
  const end = queueIndexOf(sents[sents.length - 1]);
  if (start >= 0) playRange(start, end);
}

function stopPlayback() {
  state.playing = false;
  state.selReading = false;
  state.skipDelta = 0;
  if (state.stopClip) state.stopClip();
  updateButtons();
  setStatus('Idle');
  applyPendingReplace();
}

function stopSelectionOnly() {
  if (!state.selReading) return;
  state.selReading = false;
  if (state.stopClip) state.stopClip();
}

function skip(delta) {
  if (!state.playing || !state.stopClip) return;
  state.skipDelta = delta === 1 ? 1 : -1;
  state.stopClip();
}

function togglePlay() {
  if (state.playing && state.audio) {
    if (state.paused) {
      state.paused = false;
      state.audio.play().catch(() => {});
    } else {
      state.paused = true;
      state.audio.pause();
    }
    updateButtons();
    return;
  }
  playFrom(state.qIndex);
}

// readTextAloud speaks arbitrary text (the user's selection). There are no
// sentence spans to highlight; the user's own selection stays visible instead.
async function readTextAloud(text) {
  const gen = ++state.playGen;
  if (state.stopClip) state.stopClip();
  state.playing = false;
  state.selReading = true;
  updateButtons();
  setStatus('Reading selection…');
  for (const chunk of splitSentences(text)) {
    if (!state.selReading || gen !== state.playGen) break;
    let url;
    try {
      url = await fetchClip(chunk);
    } catch (e) {
      toast(e.message);
      break;
    }
    if (!state.selReading || gen !== state.playGen) break;
    const how = await playClip(url, null);
    if (how !== 'ended') break;
  }
  if (gen === state.playGen) {
    state.selReading = false;
    updateButtons();
    setStatus('Idle');
    applyPendingReplace();
  }
}

/* ---------------- controls ---------------- */

function updateButtons() {
  const busy = state.playing || state.selReading;
  $('#btn-play').textContent = state.playing && !state.paused ? '⏸ Pause' : (state.paused ? '▶ Resume' : '▶ Play');
  $('#btn-play').disabled = !state.queue.length && !state.playing;
  $('#btn-stop').disabled = !busy;
  $('#btn-prev').disabled = !state.playing;
  $('#btn-next').disabled = !state.playing;
}

function populateControls() {
  const speedSel = $('#sel-speed');
  speedSel.textContent = '';
  for (const s of SPEEDS) {
    const o = el('option', '', s + '×');
    o.value = String(s);
    speedSel.append(o);
  }
  const provSel = $('#sel-provider');
  provSel.textContent = '';
  for (const name of Object.keys(state.cfg.providers).sort()) {
    const o = el('option', '', name);
    o.value = name;
    provSel.append(o);
  }

  if (!state.cfg.providers[settings.provider]) settings.provider = state.cfg.default_provider;
  if (!SPEEDS.includes(settings.speed)) settings.speed = 1.0;
  if (!settings.voice) settings.voice = defaultVoiceFor(settings.provider);

  provSel.value = settings.provider;
  speedSel.value = String(settings.speed);
  $('#inp-voice').value = settings.voice;
  $('#chk-follow').checked = settings.follow;
  $('#chk-autoread').checked = settings.autoread;
  $('#chk-interim').checked = settings.interim;
  refreshVoiceOptions();

  provSel.addEventListener('change', () => {
    settings.provider = provSel.value;
    settings.voice = savedVoiceFor(settings.provider) || defaultVoiceFor(settings.provider);
    $('#inp-voice').value = settings.voice;
    refreshVoiceOptions();
    saveSettings();
  });
  speedSel.addEventListener('change', () => {
    settings.speed = parseFloat(speedSel.value);
    if (state.audio) state.audio.playbackRate = settings.speed;
    saveSettings();
  });
  $('#inp-voice').addEventListener('change', () => {
    settings.voice = $('#inp-voice').value.trim() || defaultVoiceFor(settings.provider);
    saveVoiceFor(settings.provider, settings.voice);
    saveSettings();
  });
  $('#chk-follow').addEventListener('change', () => { settings.follow = $('#chk-follow').checked; saveSettings(); });
  $('#chk-autoread').addEventListener('change', () => { settings.autoread = $('#chk-autoread').checked; saveSettings(); });
  $('#chk-interim').addEventListener('change', () => {
    settings.interim = $('#chk-interim').checked;
    saveSettings();
    rerenderMessages();
  });

  $('#btn-play').addEventListener('click', togglePlay);
  $('#btn-stop').addEventListener('click', stopPlayback);
  $('#btn-prev').addEventListener('click', () => skip(-1));
  $('#btn-next').addEventListener('click', () => skip(1));
}

function defaultVoiceFor(provider) {
  const pc = state.cfg.providers[provider];
  return pc ? pc.default_voice : '';
}

function refreshVoiceOptions() {
  const list = $('#voice-options');
  list.textContent = '';
  const pc = state.cfg.providers[settings.provider];
  if (!pc) return;
  for (const v of pc.voices) {
    const o = document.createElement('option');
    o.value = v;
    list.append(o);
  }
}

/* ---------------- selection chip + context menu ---------------- */

function selectionInMessages() {
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || !sel.rangeCount) return null;
  const range = sel.getRangeAt(0);
  const node = range.commonAncestorContainer;
  const host = node.nodeType === 1 ? node : node.parentElement;
  if (!host || !host.closest('#messages')) return null;
  const text = sel.toString().trim();
  return text ? { text, range } : null;
}

function positionChip() {
  const chip = $('#selchip');
  const found = selectionInMessages();
  if (!found) { chip.hidden = true; return; }
  const rect = found.range.getBoundingClientRect();
  chip.hidden = false;
  chip.style.left = Math.max(8, window.scrollX + rect.left) + 'px';
  chip.style.top = (window.scrollY + rect.bottom + 8) + 'px';
}

function hideMenu() { $('#ctxmenu').hidden = true; }

function showMenu(x, y, items) {
  const menu = $('#ctxmenu');
  menu.textContent = '';
  for (const item of items) {
    const b = el('button', '', item.label);
    b.addEventListener('click', () => { hideMenu(); item.fn(); });
    menu.append(b);
  }
  menu.hidden = false;
  // Clamp to the viewport once we know the menu's size.
  const rect = menu.getBoundingClientRect();
  menu.style.left = Math.min(x, window.innerWidth - rect.width - 8) + 'px';
  menu.style.top = Math.min(y, window.innerHeight - rect.height - 8) + 'px';
}

function wireSelectionAndMenu() {
  document.addEventListener('pointerup', () => setTimeout(positionChip, 1));
  document.addEventListener('selectionchange', () => {
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed) $('#selchip').hidden = true;
  });
  $('#selchip').addEventListener('click', () => {
    const found = selectionInMessages();
    $('#selchip').hidden = true;
    if (found) readTextAloud(found.text);
  });

  document.addEventListener('contextmenu', (e) => {
    if (!(e.target instanceof Element) || !e.target.closest('#messages')) return;
    const found = selectionInMessages();
    const sent = e.target.closest('.sent');
    if (!found && !sent) return; // nothing useful to offer; keep the native menu
    e.preventDefault();
    const items = [];
    if (found) {
      items.push({ label: '🔊 Read selection', fn: () => readTextAloud(found.text) });
      items.push({ label: '📋 Copy selection', fn: () => navigator.clipboard.writeText(found.text).catch(() => {}) });
    }
    if (sent) {
      const i = queueIndexOf(sent);
      if (i >= 0) items.push({ label: '▶ Read from here', fn: () => playFrom(i) });
    }
    showMenu(e.clientX, e.clientY, items);
  });
  document.addEventListener('click', (e) => {
    if (!(e.target instanceof Element) || !e.target.closest('#ctxmenu')) hideMenu();
  });
  window.addEventListener('scroll', hideMenu);
  window.addEventListener('keydown', (e) => { if (e.key === 'Escape') { hideMenu(); $('#selchip').hidden = true; } });
}

function wireSentenceClicks() {
  messagesEl.addEventListener('click', (e) => {
    if (!(e.target instanceof Element)) return;
    const sent = e.target.closest('.sent');
    if (!sent) return;
    const sel = window.getSelection();
    if (sel && !sel.isCollapsed) return; // the user is selecting, not clicking
    const i = queueIndexOf(sent);
    if (i >= 0) playFrom(i);
  });
}

function wireKeyboard() {
  window.addEventListener('keydown', (e) => {
    const target = e.target;
    if (target instanceof Element && target.closest('input, select, textarea, button')) return;
    if (e.code === 'Space') { e.preventDefault(); togglePlay(); }
    else if (e.key === 'ArrowRight') skip(1);
    else if (e.key === 'ArrowLeft') skip(-1);
  });
}

/* ---------------- live updates ---------------- */

function applyPendingReplace() {
  if (!state.pendingReplace || state.playing || state.selReading) return;
  const msgs = state.pendingReplace;
  state.pendingReplace = null;
  state.messages = msgs;
  renderAll(msgs);
}

// rerenderMessages redraws the conversation with the current settings (used
// by the "Notes as bullets" toggle). A redraw would yank the DOM out from
// under an active read-out, so during playback it is queued the same way a
// transcript rewrite is, and applies when playback stops.
function rerenderMessages() {
  if (state.playing || state.selReading) {
    state.pendingReplace = state.messages;
    return;
  }
  renderAll(state.messages);
}

async function refreshMessages() {
  let resp, data;
  try {
    resp = await fetch(api('/api/messages'));
    data = await resp.json();
  } catch (e) {
    return;
  }
  if (!resp.ok) {
    // The transcript may be gone (a pruned or moved session). Keep the
    // conversation on screen; just say why live updates stopped.
    setStatus(data.error || 'Cannot refresh this session');
    return;
  }
  applyMeta(data);
  const olds = state.messages;
  const news = data.messages || [];

  let appendOnly = news.length >= olds.length;
  if (appendOnly) {
    for (let i = 0; i < olds.length; i++) {
      if (olds[i].role !== news[i].role || olds[i].text !== news[i].text) { appendOnly = false; break; }
    }
  }

  if (appendOnly && news.length === olds.length) return; // nothing visible changed

  if (!appendOnly) {
    // The transcript was rewritten (edited turn, compacted session). Redraw,
    // but never yank the DOM out from under an active read-out.
    if (state.playing || state.selReading) {
      state.pendingReplace = news;
      return;
    }
    state.messages = news;
    renderAll(news);
    return;
  }

  const firstNewQueueIdx = state.queue.length;
  const empty = $('#empty');
  if (empty) empty.remove();
  for (let i = olds.length; i < news.length; i++) {
    messagesEl.append(renderMessage(news[i], i));
  }
  state.messages = news;
  rebuildQueue();

  if (settings.autoread && !state.playing && !state.selReading) {
    // Start at the first newly arrived assistant sentence, if any.
    for (let i = firstNewQueueIdx; i < state.queue.length; i++) {
      const card = state.queue[i].el.closest('.message');
      if (card && card.classList.contains('assistant')) { playFrom(i); break; }
    }
  }
}

function debounce(fn, ms) {
  let t = 0;
  return () => { clearTimeout(t); t = setTimeout(fn, ms); };
}

function wireEvents() {
  const es = new EventSource(api('/api/events'));
  const refresh = debounce(refreshMessages, 300);
  // Change events name the session they are about; this page only cares
  // about its own. (A payload that is not JSON comes from an older server,
  // which only ever watched one transcript — treat that as ours.)
  es.addEventListener('change', (ev) => {
    try {
      const d = JSON.parse(ev.data);
      if (d.session && d.session !== SESSION) return;
    } catch (e) { /* old single-session server */ }
    refresh();
  });
  // `tts-ctl stop` (and Ctrl+Alt+X) reach the page through this event —
  // nothing outside the browser can end an <audio> element directly.
  es.addEventListener('stop', () => stopPlayback());
  es.onerror = () => setStatus('Reconnecting…'); // EventSource retries by itself
}

/* ---------------- session navigator (a URL with no session id) ---------------- */

function fmtAgo(ms) {
  const s = Math.round((Date.now() - ms) / 1000);
  if (s < 60) return 'just now';
  const m = Math.round(s / 60);
  if (m < 60) return m + ' min ago';
  const h = Math.round(m / 60);
  if (h < 24) return h + ' h ago';
  const d = Math.round(h / 24);
  return d === 1 ? 'yesterday' : d + ' days ago';
}

function renderNavigator(projects) {
  messagesEl.textContent = '';
  if (!projects.length) {
    const p = el('p', '', 'No Claude Code sessions found on this machine.');
    p.id = 'empty';
    messagesEl.append(p);
    return;
  }
  for (const proj of projects) {
    const card = el('article', 'message nav-project');
    const head = el('div', 'msg-head');
    head.append(el('span', 'role', proj.name || 'unknown project'));
    if (proj.dir) head.append(el('span', 'time', proj.dir));
    card.append(head);
    const list = el('div', 'nav-sessions');
    for (const sess of proj.sessions) {
      const a = document.createElement('a');
      a.className = 'nav-session';
      a.href = '/?s=' + encodeURIComponent(sess.id);
      a.append(el('span', sess.title ? 'nav-title' : 'nav-title untitled', sess.title || 'untitled session'));
      a.append(el('span', 'nav-when', fmtAgo(sess.updated)));
      list.append(a);
    }
    card.append(list);
    messagesEl.append(card);
  }
}

// navKey captures everything the navigator DISPLAYS. Session files change
// every second while Claude is talking, but the visible list (names, titles,
// order, coarse relative times) changes rarely — and rebuilding the DOM on
// every change event would eat in-progress clicks on the session links.
let lastNavKey = '';
function navKey(projects) {
  return JSON.stringify(projects.map((p) => [
    p.name,
    p.sessions.map((sess) => [sess.id, sess.title, fmtAgo(sess.updated)]),
  ]));
}

async function refreshNavigator() {
  let data;
  try {
    data = await (await fetch('/api/sessions')).json();
  } catch (e) {
    setStatus('Cannot reach the reader server');
    return;
  }
  const projects = data.projects || [];
  const key = navKey(projects);
  if (key !== lastNavKey) {
    lastNavKey = key;
    renderNavigator(projects);
  }
  setStatus('Pick a session to read along');
}

async function initNavigator() {
  document.body.classList.add('nav');
  setPageTitle('Claude Code — Sessions');
  await refreshNavigator();
  // Keep the list roughly current: server events cover the sessions it
  // already follows, and the slow timer picks up everything else (session
  // files change constantly while Claude is talking).
  const refresh = debounce(refreshNavigator, 500);
  const es = new EventSource('/api/events');
  es.addEventListener('change', refresh);
  es.addEventListener('load', refresh);
  setInterval(refreshNavigator, 30000);
}

/* ---------------- boot ---------------- */

async function init() {
  if (!SESSION) {
    initNavigator();
    return;
  }
  loadSettings();
  try {
    state.cfg = await (await fetch(api('/api/config'))).json();
  } catch (e) {
    setStatus('Cannot reach the reader server');
    return;
  }
  populateControls();
  wireSelectionAndMenu();
  wireSentenceClicks();
  wireKeyboard();

  const resp = await fetch(api('/api/messages'));
  const data = await resp.json();
  if (!resp.ok) {
    setStatus(data.error || 'Cannot load this session');
    toast((data.error || 'Cannot load this session') + ' — pick another from the Sessions list.');
    return;
  }
  applyMeta(data);
  state.messages = data.messages || [];
  renderAll(state.messages);
  const parts = (data.transcript || '').split('/');
  $('#status-session').textContent = parts[parts.length - 1] || '';
  setStatus('Idle — press Play, click a sentence, or select some text');
  // Open at the end of the conversation, like the chat window does.
  window.scrollTo(0, document.body.scrollHeight);
  wireEvents();
}

init();
