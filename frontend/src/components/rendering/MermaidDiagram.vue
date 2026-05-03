<script setup lang="ts">
/**
 * MermaidDiagram — renders a mermaid code fence as an SVG diagram.
 *
 * - Lazy-imports mermaid (only loads when a mermaid block is present).
 * - Auto-picks theme from document.documentElement.classList ('dark').
 * - Debounces re-renders by 120ms while streaming === true.
 * - On render error, falls back to plain code block + warning chip.
 * - Per-diagram %%{init: ...}%% directives work through Mermaid's
 *   parser unchanged (they override initialize() settings).
 *
 * Theme reactivity: watches the .dark class on <html> and re-renders
 * when it changes so diagram colours stay consistent with the UI theme.
 */
import { ref, watch, onMounted, onBeforeUnmount, computed } from 'vue';
import { sanitize } from '@/lib/markdown/sanitize';

const props = defineProps<{
  source: string;
  streaming?: boolean;
}>();

const svgHtml = ref<string>('');
const error = ref<string | null>(null);
let debounceTimer: ReturnType<typeof setTimeout> | null = null;
let renderCounter = 0; // used for stable mermaid element IDs

// ─── Theme detection ──────────────────────────────────────────────────────────

function isDarkTheme(): boolean {
  if (typeof document === 'undefined') return true;
  return document.documentElement.classList.contains('dark');
}

const mermaidTheme = computed(() => (isDarkTheme() ? 'dark' : 'default'));

// Watch for theme class changes on <html>.
let themeObserver: MutationObserver | null = null;

function setupThemeObserver() {
  if (typeof MutationObserver === 'undefined') return;
  themeObserver = new MutationObserver(() => {
    void renderDiagram();
  });
  themeObserver.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ['class'],
  });
}

// ─── Mermaid rendering ────────────────────────────────────────────────────────

async function renderDiagram() {
  if (!props.source.trim()) return;

  const id = `mermaid-${++renderCounter}-${Math.random().toString(36).slice(2, 7)}`;

  try {
    const { default: mermaid } = await import('mermaid');

    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: mermaidTheme.value,
    });

    const { svg } = await mermaid.render(id, props.source);
    svgHtml.value = sanitize(svg, 'mermaid-svg');
    error.value = null;
  } catch (err) {
    // Clean up any injected element mermaid may have left in the DOM.
    const stale = document.getElementById(id);
    stale?.remove();
    const staleD = document.getElementById(`dmermaid-${id}`);
    staleD?.remove();

    svgHtml.value = '';
    error.value = err instanceof Error ? err.message : String(err);
  }
}

function scheduleRender() {
  if (debounceTimer) clearTimeout(debounceTimer);
  const delay = props.streaming ? 120 : 0;
  debounceTimer = setTimeout(() => {
    void renderDiagram();
  }, delay);
}

// Re-render when source changes or streaming ends.
watch(
  () => [props.source, props.streaming],
  () => {
    scheduleRender();
  },
);

onMounted(() => {
  setupThemeObserver();
  scheduleRender();
});

onBeforeUnmount(() => {
  if (debounceTimer) clearTimeout(debounceTimer);
  themeObserver?.disconnect();
});
</script>

<template>
  <div class="md-mermaid-block">
    <!-- Successful SVG render -->
    <div
      v-if="svgHtml && !error"
      class="md-mermaid-svg"
      v-html="svgHtml"
    ></div>

    <!-- Error fallback: raw source + warning chip -->
    <template v-else-if="error">
      <pre class="md-mermaid-fallback"><code>{{ source }}</code></pre>
      <div class="md-mermaid-error" role="alert">
        Mermaid render failed: {{ error }}
      </div>
    </template>

    <!-- Loading state while first render pending -->
    <div v-else class="md-mermaid-loading" aria-label="Rendering diagram...">
      <pre class="md-mermaid-fallback"><code>{{ source }}</code></pre>
    </div>
  </div>
</template>

<style scoped>
.md-mermaid-block {
  margin: 0.5em 0 0.75em 0;
}

.md-mermaid-svg {
  overflow-x: auto;
}

.md-mermaid-svg :deep(svg) {
  max-width: 100%;
  height: auto;
}

.md-mermaid-fallback {
  font-family: var(--font-mono);
  font-size: 0.88em;
  background: var(--surface-2);
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-sm);
  padding: 0.65em 0.85em;
  margin: 0;
  overflow-x: auto;
  line-height: 1.45;
  color: var(--ink);
}

.md-mermaid-error {
  margin-top: 0.4em;
  font-family: var(--font-ui);
  font-size: 0.8em;
  color: var(--ink-muted);
  padding: 0.25em 0.5em;
  background: var(--surface-2);
  border: 1px solid var(--border-muted);
  border-radius: var(--radius-sm);
}

.md-mermaid-loading {
  opacity: 0.5;
}
</style>
