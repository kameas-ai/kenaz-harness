<script setup lang="ts">
/**
 * MarkdownBlock — single canonical markdown renderer (markdown-rendering-polish WP01).
 *
 * Consolidates the scattered marked+DOMPurify pipeline into one component.
 * All markdown rendering in the chat surface should flow through this component.
 *
 * Features:
 *   - Renders `:source` (markdown string) via marked (GFM) + DOMPurify.
 *   - Detects fenced code blocks and wraps each in a `.code-block-wrap` div
 *     with a header bar: language label, copy-to-clipboard, save-as-artifact.
 *   - Copy: copies raw code text (not rendered HTML) to the clipboard.
 *   - Save: silently calls `client.sessions.saveAsArtifact(...)` using the
 *     provided sessionId+messageId context. Falls back to a no-op when context
 *     is absent so the component is safe to use in contexts without a session.
 *   - Undo toast: 5-second inline toast with "Saved as <title>. Undo." that
 *     calls `client.artifacts.remove(id)` on click.
 *
 * Save RPC: uses `client.sessions.saveAsArtifact` — the only artifact-create
 * surface in ArtifactsClient v1 (there is no artifacts.create endpoint).
 *
 * Typography: code-block header matches KaneazToolsPanel small-caps register
 * (font-ui, text-[10px] uppercase tracking-[0.16em]).
 *
 * CSS note: code block headers are rendered via a custom marked renderer that
 * wraps each <pre> in a .code-block-wrap container, with the header bar
 * injected as a preceding sibling. This keeps the Vue template clean and
 * avoids the v-html/Vue-island isolation problem.
 * Interactive buttons (copy/save) are handled via event delegation on the
 * root element — this is safe because DOMPurify strips event handlers from
 * v-html, but our wrapper element catches delegated clicks outside v-html.
 */
import { computed, ref, nextTick, onMounted, onUpdated, useTemplateRef, inject } from 'vue';
import { Marked } from 'marked';
import DOMPurify from 'dompurify';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';

const props = defineProps<{
  /** Raw markdown source string. */
  source: string;
  /**
   * When true, renders a brass streaming caret beside the rendered text
   * (matches the privacy CI #4 brass-for-live-state convention).
   * Code-block headers are suppressed while streaming to avoid layout
   * thrash on every token.
   */
  streaming?: boolean;
  /**
   * Session id — required for save-as-artifact. When absent, the save
   * button is not rendered.
   */
  sessionId?: string;
  /**
   * Message id within the session — required for save-as-artifact.
   * When absent, save button is not rendered.
   */
  messageId?: string;
}>();

// ── marked setup ──────────────────────────────────────────────────────────

/**
 * Module-level shared Marked instance for lexer-only operations (codeBlocks
 * computed). A fresh instance is created per html-render to allow a stateful
 * code-block-index counter in the renderer without mutating the shared instance.
 */
const markedBase = new Marked({ gfm: true, breaks: false });

// ── helpers ───────────────────────────────────────────────────────────────

/** Map a language identifier to a file extension. */
function langToExt(lang: string): string {
  const map: Record<string, string> = {
    typescript: 'ts', javascript: 'js', python: 'py', rust: 'rs',
    go: 'go', java: 'java', csharp: 'cs', cpp: 'cpp', c: 'c',
    ruby: 'rb', shell: 'sh', bash: 'sh', zsh: 'sh', fish: 'fish',
    sql: 'sql', json: 'json', yaml: 'yml', toml: 'toml',
    html: 'html', css: 'css', markdown: 'md', diff: 'diff',
    dockerfile: 'dockerfile', makefile: 'mk', swift: 'swift',
    kotlin: 'kt', scala: 'scala', r: 'r', tex: 'tex',
    xml: 'xml', graphql: 'graphql', proto: 'proto',
    haskell: 'hs', elixir: 'ex', erlang: 'erl',
  };
  return map[lang.toLowerCase()] ?? (lang.toLowerCase() || 'txt');
}

/** Stable 6-char hash of a string (djb2). Not cryptographic — title only. */
function hash6(s: string): string {
  let h = 5381;
  for (let i = 0; i < s.length; i++) {
    h = ((h << 5) + h) ^ s.charCodeAt(i);
    h = h & 0xffffffff;
  }
  return (h >>> 0).toString(16).padStart(8, '0').slice(0, 6);
}

// ── code-block state ──────────────────────────────────────────────────────

interface CodeBlock {
  index: number;
  lang: string;
  text: string;
  title: string;
}

/** Parsed code blocks from the source (lexer-level, stable ordering). */
const codeBlocks = computed<CodeBlock[]>(() => {
  const tokens = markedBase.lexer(props.source);
  const blocks: CodeBlock[] = [];
  let idx = 0;
  for (const tok of tokens) {
    if (tok.type === 'code') {
      const lang = ((tok.lang ?? '').split(/\s/)[0] ?? '').toLowerCase();
      const text = tok.text;
      const ext = langToExt(lang);
      const slug = lang || 'code';
      const title = `${slug}-snippet-${hash6(text)}.${ext}`;
      blocks.push({ index: idx++, lang, text, title });
    }
  }
  return blocks;
});

// ── rendered HTML ─────────────────────────────────────────────────────────

/**
 * Build a fresh Marked instance with a code-block renderer that wraps each
 * fenced block in a .code-block-wrap container with data-block-idx.
 * A fresh instance per compute call gives us a per-invocation blockIdx counter
 * without mutating the shared base instance.
 */
function buildMarkedWithCodeHeaders(): Marked {
  let blockIdx = 0;
  const m = new Marked({ gfm: true, breaks: false });
  m.use({
    renderer: {
      code({ text, lang }: { text: string; lang?: string; escaped?: boolean }) {
        const language = ((lang ?? '').split(/\s/)[0] ?? '');
        const idx = blockIdx++;
        const escaped = text
          .replace(/&/g, '&amp;')
          .replace(/</g, '&lt;')
          .replace(/>/g, '&gt;')
          .replace(/"/g, '&quot;');
        const cls = language ? ` class="language-${language}"` : '';
        return (
          `<div class="code-block-wrap" data-block-idx="${idx}">` +
          `<pre><code${cls}>${escaped}</code></pre>` +
          `</div>\n`
        );
      },
    },
  });
  return m;
}

const html = computed(() => {
  if (!props.source) return '';
  const m = buildMarkedWithCodeHeaders();
  const raw = m.parse(props.source, { async: false }) as string;
  return DOMPurify.sanitize(raw, {
    ADD_ATTR: ['target', 'rel', 'data-block-idx'],
    ADD_TAGS: [],
  });
});

// ── header injection ──────────────────────────────────────────────────────

const rootRef = useTemplateRef<HTMLElement>('root');

/** Insert / update header bars above each .code-block-wrap. */
function mountHeaders() {
  const root = rootRef.value;
  if (!root) return;
  // Suppress headers while streaming — avoids layout thrash on each token.
  if (props.streaming) return;
  const wraps = root.querySelectorAll<HTMLElement>('.code-block-wrap');
  wraps.forEach((wrap) => {
    // Idempotent: skip if header already mounted.
    if (wrap.querySelector('.code-header-bar')) return;
    const idxStr = wrap.getAttribute('data-block-idx') ?? '0';
    const idx = parseInt(idxStr, 10);
    const block = codeBlocks.value[idx];
    if (!block) return;

    const header = document.createElement('div');
    header.className = 'code-header-bar';
    header.setAttribute('data-testid', `code-block-header-${idx}`);

    // Language label
    const langSpan = document.createElement('span');
    langSpan.className = 'code-header-lang';
    langSpan.setAttribute('data-testid', `code-block-lang-${idx}`);
    langSpan.textContent = block.lang || 'code';
    header.appendChild(langSpan);

    // Button group
    const btnGroup = document.createElement('span');
    btnGroup.className = 'code-header-actions';

    // Copy button
    const copyBtn = document.createElement('button');
    copyBtn.type = 'button';
    copyBtn.className = 'code-header-btn';
    copyBtn.setAttribute('aria-label', `Copy ${block.lang || 'code'} block`);
    copyBtn.setAttribute('data-testid', `code-block-copy-${idx}`);
    copyBtn.setAttribute('data-action', 'copy');
    copyBtn.setAttribute('data-block-idx', idxStr);
    copyBtn.textContent = 'copy';
    btnGroup.appendChild(copyBtn);

    // Save button (only when session context and client are present)
    if (props.sessionId && props.messageId && client) {
      const saveBtn = document.createElement('button');
      saveBtn.type = 'button';
      saveBtn.className = 'code-header-btn';
      saveBtn.setAttribute('aria-label', `Save ${block.lang || 'code'} block as artifact`);
      saveBtn.setAttribute('data-testid', `code-block-save-${idx}`);
      saveBtn.setAttribute('data-action', 'save');
      saveBtn.setAttribute('data-block-idx', idxStr);
      saveBtn.textContent = 'save';
      btnGroup.appendChild(saveBtn);
    }

    header.appendChild(btnGroup);
    wrap.insertBefore(header, wrap.firstChild);
  });
}

onMounted(mountHeaders);
onUpdated(mountHeaders);

// ── event delegation ──────────────────────────────────────────────────────

/** Copy flash state: block index → true while showing "copied". */
const copyFlash = ref<Set<number>>(new Set());

async function copyCode(blockIdx: number, btn: HTMLElement): Promise<void> {
  const block = codeBlocks.value[blockIdx];
  if (!block) return;
  try {
    await navigator.clipboard.writeText(block.text);
    btn.textContent = 'copied';
    copyFlash.value = new Set([...copyFlash.value, blockIdx]);
    setTimeout(() => {
      btn.textContent = 'copy';
      const next = new Set(copyFlash.value);
      next.delete(blockIdx);
      copyFlash.value = next;
    }, 1500);
  } catch {
    // clipboard denied — silent
  }
}

// ── artifact save + undo toast ────────────────────────────────────────────

interface UndoToast {
  artifactId: string;
  title: string;
  timer: ReturnType<typeof setTimeout>;
}

/**
 * Optional client injection — MarkdownBlock degrades gracefully when no
 * HarnessClient is provided (e.g. in unit tests that only test rendering).
 * The save button is only mounted when both sessionId+messageId AND a client
 * are present, so the fallback path never reaches the save RPC.
 */
const client = inject<HarnessClient>(HarnessClientKey);
const undoToast = ref<UndoToast | null>(null);

async function saveCode(blockIdx: number, btn: HTMLElement): Promise<void> {
  if (!props.sessionId || !props.messageId || !client) return;
  const block = codeBlocks.value[blockIdx];
  if (!block) return;

  btn.textContent = 'saving…';
  btn.setAttribute('disabled', '');
  try {
    // sessions.saveAsArtifact is the only artifact-create surface in v1.
    // We save with range (0, 0) meaning the full message; the title
    // identifies which code block this artifact came from.
    const artifact = await client.sessions.saveAsArtifact(
      props.sessionId,
      props.messageId,
      block.title,
      0,
      0,
    );

    // Show undo toast after save resolves (async, non-blocking).
    await nextTick();
    if (undoToast.value) {
      clearTimeout(undoToast.value.timer);
    }
    const timer = setTimeout(() => {
      undoToast.value = null;
    }, 5000);
    undoToast.value = { artifactId: artifact.id, title: block.title, timer };
  } catch {
    // Save failed — silent per spec.
  } finally {
    btn.textContent = 'save';
    btn.removeAttribute('disabled');
  }
}

function onDelegatedClick(event: Event): void {
  const target = (event.target as HTMLElement).closest('[data-action]') as HTMLElement | null;
  if (!target) return;
  const action = target.getAttribute('data-action');
  const idxStr = target.getAttribute('data-block-idx') ?? '0';
  const idx = parseInt(idxStr, 10);
  if (action === 'copy') {
    copyCode(idx, target);
  } else if (action === 'save') {
    saveCode(idx, target);
  }
}

async function undoSave(): Promise<void> {
  if (!undoToast.value) return;
  const { artifactId, timer } = undoToast.value;
  clearTimeout(timer);
  undoToast.value = null;
  if (!client) return;
  try {
    await client.artifacts.remove(artifactId);
  } catch {
    // Delete failed — silent.
  }
}
</script>

<template>
  <span ref="root" class="markdown-block" role="text" @click="onDelegatedClick">
    <!-- Rendered markdown body with injected code-block-wrap containers -->
    <span class="md-body" v-html="html"></span>

    <!-- Streaming caret — brass per privacy CI #4 (brass strictly for live state) -->
    <span
      v-if="streaming"
      aria-hidden="true"
      class="streaming-caret inline-block w-[2px] h-[1em] align-text-bottom ml-0.5 bg-accent"
    ></span>

    <!-- Undo toast (rendered outside v-html so Vue controls it) -->
    <Transition name="undo-fade">
      <div
        v-if="undoToast"
        class="undo-toast"
        role="status"
        aria-live="polite"
        data-testid="undo-toast"
      >
        <span class="font-ui text-sm text-ink">
          Saved as
          <code class="font-mono text-[12px] text-ink-muted">{{ undoToast.title }}</code>.
        </span>
        <button
          type="button"
          class="font-ui text-[12px] uppercase tracking-[0.14em] text-accent hover:underline ml-3"
          data-testid="undo-toast-button"
          @click.stop="undoSave"
        >
          Undo
        </button>
      </div>
    </Transition>
  </span>
</template>

<style scoped>
/* ── markdown body ─────────────────────────────────────────────────────── */
.md-body {
  display: block;
}
.md-body :deep(p) {
  margin: 0 0 0.65em 0;
  line-height: 1.55;
}
.md-body :deep(p:last-child) {
  margin-bottom: 0;
}
.md-body :deep(ul),
.md-body :deep(ol) {
  margin: 0 0 0.65em 1.25em;
  padding: 0;
  line-height: 1.55;
}
.md-body :deep(li) {
  margin-bottom: 0.15em;
}
.md-body :deep(ul) {
  list-style: disc;
}
.md-body :deep(ol) {
  list-style: decimal;
}
.md-body :deep(h1),
.md-body :deep(h2),
.md-body :deep(h3),
.md-body :deep(h4) {
  font-weight: 600;
  margin: 0.6em 0 0.3em 0;
  line-height: 1.25;
}
.md-body :deep(h1) {
  font-size: 1.2em;
}
.md-body :deep(h2) {
  font-size: 1.1em;
}
.md-body :deep(h3),
.md-body :deep(h4) {
  font-size: 1em;
  color: var(--ink-muted);
}
.md-body :deep(strong) {
  color: var(--ink);
  font-weight: 600;
}
.md-body :deep(em) {
  font-style: italic;
}
.md-body :deep(code) {
  font-family: var(--font-mono);
  font-size: 0.9em;
  background: var(--surface-2);
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-sm);
  padding: 0.05em 0.35em;
  color: var(--ink);
}
.md-body :deep(pre) {
  font-family: var(--font-mono);
  font-size: 0.88em;
  background: var(--surface-2);
  border: 1px solid var(--border-muted);
  border-bottom-left-radius: var(--radius-sm);
  border-bottom-right-radius: var(--radius-sm);
  border-top-left-radius: 0;
  border-top-right-radius: 0;
  border-top: none;
  padding: 0.65em 0.85em;
  margin: 0;
  overflow-x: auto;
  line-height: 1.45;
}
.md-body :deep(pre code) {
  background: transparent;
  border: 0;
  padding: 0;
  color: var(--ink);
}
.md-body :deep(.code-block-wrap) {
  margin: 0.5em 0 0.75em 0;
}
.md-body :deep(blockquote) {
  border-left: 2px solid var(--border-strong);
  padding-left: 0.75em;
  margin: 0.5em 0;
  color: var(--ink-muted);
}
.md-body :deep(a) {
  color: var(--accent);
  text-decoration: underline;
  text-underline-offset: 2px;
}
.md-body :deep(a:hover) {
  color: var(--ink);
}
.md-body :deep(hr) {
  border: 0;
  border-top: 1px solid var(--border-muted);
  margin: 0.85em 0;
}
.md-body :deep(table) {
  border-collapse: collapse;
  margin: 0.5em 0;
  font-size: 0.95em;
}
.md-body :deep(th),
.md-body :deep(td) {
  border: 1px solid var(--border-muted);
  padding: 0.3em 0.6em;
  text-align: left;
}
.md-body :deep(th) {
  background: var(--surface-2);
  font-weight: 600;
}

/* ── code-block header (injected DOM) ─────────────────────────────────── */
.md-body :deep(.code-header-bar) {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  background: var(--surface-2);
  border: 1px solid var(--border-muted);
  border-bottom: none;
  border-top-left-radius: var(--radius-sm);
  border-top-right-radius: var(--radius-sm);
  padding: 0.2em 0.6em;
}

.md-body :deep(.code-header-lang) {
  font-family: var(--font-ui);
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.16em;
  color: var(--ink-subtle);
}

.md-body :deep(.code-header-actions) {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  margin-left: auto;
}

.md-body :deep(.code-header-btn) {
  font-family: var(--font-ui);
  font-size: 10px;
  text-transform: uppercase;
  letter-spacing: 0.16em;
  padding: 0.15em 0.5em;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-muted);
  color: var(--ink-dim);
  background: transparent;
  cursor: pointer;
  transition: color 0.1s, background 0.1s;
}

.md-body :deep(.code-header-btn:hover) {
  color: var(--accent);
  background: var(--surface-3);
}

.md-body :deep(.code-header-btn:disabled) {
  opacity: 0.5;
  cursor: not-allowed;
}

/* ── undo toast ───────────────────────────────────────────────────────── */
.undo-toast {
  position: fixed;
  bottom: 1.5rem;
  right: 1.5rem;
  z-index: 50;
  display: flex;
  align-items: center;
  padding: 0.6rem 1rem;
  border-radius: var(--radius-sm);
  border: 1px solid var(--border-muted);
  background: var(--surface-1);
  box-shadow: var(--shadow-lg, 0 4px 6px -1px rgb(0 0 0 / 0.1));
}

.undo-fade-enter-active,
.undo-fade-leave-active {
  transition: opacity 0.15s ease;
}
.undo-fade-enter-from,
.undo-fade-leave-to {
  opacity: 0;
}

/* ── streaming caret ──────────────────────────────────────────────────── */
.streaming-caret {
  animation: streaming-caret-blink 900ms steps(2, start) infinite;
}

@keyframes streaming-caret-blink {
  to {
    visibility: hidden;
  }
}
</style>
