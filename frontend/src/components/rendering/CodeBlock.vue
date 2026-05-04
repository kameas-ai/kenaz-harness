<script setup lang="ts">
/**
 * CodeBlock — renders a fenced code block with:
 *   - Language pill
 *   - Copy button (clipboard.writeText; shows checkmark flash)
 *   - Save-as-Artifact button (silent save + 5s toast with Undo)
 *   - Long-block collapse: > 30 lines → "Show N more lines" footer
 *
 * Buttons are disabled while `streaming === true`.
 *
 * The component is mounted by MarkdownBlock's post-mount walker into
 * placeholder <div data-md-block="code"> elements; it is never used
 * directly in templates.
 */
import { computed, ref } from 'vue';
import { Copy, Check, BookmarkPlus } from 'lucide-vue-next';

const props = defineProps<{
  lang: string;
  source: string;
  streaming?: boolean;
  messageId?: string;
  sessionId?: string;
}>();

const emit = defineEmits<{
  (e: 'artifact-saved', id: string, title: string): void;
}>();

// ─── Collapse logic ──────────────────────────────────────────────────────────

const COLLAPSE_THRESHOLD = 30;

const lines = computed(() => props.source.split('\n'));
const lineCount = computed(() => lines.value.length);
const needsCollapse = computed(() => lineCount.value > COLLAPSE_THRESHOLD);
const expanded = ref(false);

const visibleSource = computed(() => {
  if (!needsCollapse.value || expanded.value) return props.source;
  return lines.value.slice(0, COLLAPSE_THRESHOLD).join('\n');
});

const hiddenLineCount = computed(() =>
  needsCollapse.value ? lineCount.value - COLLAPSE_THRESHOLD : 0,
);

// ─── Copy button ─────────────────────────────────────────────────────────────

const copied = ref(false);
let copyTimer: ReturnType<typeof setTimeout> | null = null;

async function copyToClipboard() {
  if (props.streaming) return;
  try {
    await navigator.clipboard.writeText(props.source);
    copied.value = true;
    if (copyTimer) clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copied.value = false;
    }, 1800);
  } catch {
    // Clipboard API not available; silent fail.
  }
}

// ─── Save as artifact ────────────────────────────────────────────────────────

const saving = ref(false);

/** Extension map for known languages. */
const EXT_MAP: Record<string, string> = {
  typescript: 'ts',
  ts: 'ts',
  tsx: 'tsx',
  javascript: 'js',
  js: 'js',
  jsx: 'jsx',
  python: 'py',
  py: 'py',
  go: 'go',
  rust: 'rs',
  rs: 'rs',
  shell: 'sh',
  bash: 'sh',
  sh: 'sh',
  zsh: 'sh',
  json: 'json',
  yaml: 'yaml',
  yml: 'yaml',
  toml: 'toml',
  markdown: 'md',
  md: 'md',
  html: 'html',
  css: 'css',
  scss: 'scss',
  sql: 'sql',
  dockerfile: 'dockerfile',
  c: 'c',
  cpp: 'cpp',
  java: 'java',
  kotlin: 'kt',
  swift: 'swift',
  ruby: 'rb',
  php: 'php',
};

function sha8(content: string): string {
  // Simple djb2 hash truncated to 8 hex chars. Not cryptographic —
  // used only for default artifact titles.
  let h = 5381;
  for (let i = 0; i < content.length; i++) {
    h = ((h << 5) + h) ^ content.charCodeAt(i);
    h = h >>> 0; // keep 32-bit unsigned
  }
  return h.toString(16).padStart(8, '0');
}

function artifactTitle(): string {
  const ext = EXT_MAP[props.lang.toLowerCase()] ?? 'txt';
  const langSlug = props.lang || 'code';
  return `${langSlug}-${sha8(props.source)}.${ext}`;
}

// Toast emission for parent to handle. Since CodeBlock is mounted via
// createApp outside the Vue tree, we use a custom event on the host element.
function emitToast(message: string, undoFn?: () => void) {
  const el = document.createElement('div');
  const event = new CustomEvent('md-toast', {
    bubbles: true,
    detail: { message, undoFn },
  });
  // Dispatch on the CodeBlock's root element by using the container.
  document.dispatchEvent(event);
  void el;
}

async function saveAsArtifact() {
  if (props.streaming || saving.value) return;
  if (!props.sessionId || !props.messageId) {
    // No session context — copy as fallback.
    await copyToClipboard();
    return;
  }

  saving.value = true;
  const title = artifactTitle();

  try {
    // Dynamic import to avoid loading harnessClient eagerly.
    const { createHarnessClient } = await import('@/lib/harnessClient');
    const client = createHarnessClient();
    const artifact = await client.sessions.saveAsArtifact(
      props.sessionId,
      props.messageId,
      title,
    );

    emitToast(`Saved as artifact: ${title}`, async () => {
      try {
        await client.artifacts.remove(artifact.id);
      } catch {
        // best-effort undo
      }
    });

    emit('artifact-saved', artifact.id, title);
  } catch (err) {
    emitToast(`Save failed: ${err instanceof Error ? err.message : String(err)}`);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="md-code-block">
    <!-- Header bar -->
    <div class="md-code-header">
      <span class="md-code-lang">{{ lang || 'text' }}</span>
      <div class="md-code-actions">
        <button
          type="button"
          class="md-code-btn"
          :class="{ 'md-code-btn--disabled': streaming }"
          :disabled="streaming"
          :aria-label="copied ? 'Copied' : 'Copy code'"
          title="Copy"
          @click="copyToClipboard"
        >
          <Check v-if="copied" class="md-code-btn-icon" />
          <Copy v-else class="md-code-btn-icon" />
        </button>
        <button
          type="button"
          class="md-code-btn"
          :class="{ 'md-code-btn--disabled': streaming || saving }"
          :disabled="streaming || saving"
          aria-label="Save as artifact"
          title="Save as artifact"
          @click="saveAsArtifact"
        >
          <BookmarkPlus class="md-code-btn-icon" />
        </button>
      </div>
    </div>

    <!-- Code content -->
    <pre class="md-code-pre"><code :class="lang ? `language-${lang}` : ''">{{ visibleSource }}</code></pre>

    <!-- Collapse footer -->
    <button
      v-if="needsCollapse"
      type="button"
      class="md-code-collapse-btn"
      @click="expanded = !expanded"
    >
      {{ expanded ? 'Show less' : `Show ${hiddenLineCount} more lines` }}
    </button>
  </div>
</template>

<style scoped>
.md-code-block {
  margin: 0.5em 0 0.75em 0;
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-sm);
  overflow: hidden;
  background: var(--surface-2);
}

.md-code-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.3em 0.6em;
  background: var(--surface-3);
  border-bottom: 1px solid var(--border-muted);
}

.md-code-lang {
  font-family: var(--font-mono);
  font-size: 0.75em;
  color: var(--ink-muted);
  letter-spacing: 0.04em;
  text-transform: lowercase;
}

.md-code-actions {
  display: flex;
  gap: 0.25em;
}

.md-code-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 1.6em;
  height: 1.6em;
  border: none;
  background: transparent;
  color: var(--ink-muted);
  cursor: pointer;
  border-radius: var(--radius-sm);
  transition: color var(--motion-fast), background var(--motion-fast);
}

.md-code-btn:hover:not(.md-code-btn--disabled) {
  color: var(--ink);
  background: var(--surface-4);
}

.md-code-btn--disabled {
  opacity: 0.4;
  cursor: not-allowed;
  pointer-events: none;
}

.md-code-btn-icon {
  width: 0.85em;
  height: 0.85em;
}

.md-code-pre {
  font-family: var(--font-mono);
  font-size: 0.88em;
  background: transparent;
  border: 0;
  border-radius: 0;
  padding: 0.65em 0.85em;
  margin: 0;
  overflow-x: auto;
  line-height: 1.45;
  color: var(--ink);
  white-space: pre;
}

.md-code-pre code {
  background: transparent;
  border: 0;
  padding: 0;
  color: inherit;
  font-size: inherit;
}

.md-code-collapse-btn {
  display: block;
  width: 100%;
  padding: 0.35em 0.85em;
  background: var(--surface-3);
  border: 0;
  border-top: 1px solid var(--border-muted);
  color: var(--accent);
  font-family: var(--font-mono);
  font-size: 0.8em;
  text-align: left;
  cursor: pointer;
  transition: background var(--motion-fast);
}

.md-code-collapse-btn:hover {
  background: var(--surface-4);
}
</style>
