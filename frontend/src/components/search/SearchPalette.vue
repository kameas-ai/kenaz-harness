<script setup lang="ts">
import { ref, computed, watch, nextTick, onBeforeUnmount, h, type VNode } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { SearchHit, SearchHighlight } from '@/lib/harnessClient';
import { useSearchPalette } from '@/lib/useSearchPalette';

/**
 * SearchPalette — floating ⌘K search overlay.
 *
 * v0.5.6: wired to Search_Sessions (full-text message search).
 * v0.7.0+: will expand to cross-entity results when unified-search-01KX5R8C ships.
 *
 * Opens via:
 *   - ⌘K / Ctrl+K global shortcut (registered in Shell.vue)
 *   - Search icon in Titlebar
 *
 * Keyboard nav:
 *   ↑/↓ — move selection
 *   Enter — open highlighted result
 *   Esc — close palette
 *
 * Click outside (backdrop) — close palette
 */

const palette = useSearchPalette();
const client = useHarnessClient();
const router = useRouter();

const inputEl = ref<HTMLInputElement | null>(null);
const query = ref('');
const hits = ref<SearchHit[]>([]);
const loading = ref(false);
const highlightedIndex = ref(-1);
const resultListEl = ref<HTMLElement | null>(null);

// ── debounced search ───────────────────────────────────────────────────
let debounceTimer: ReturnType<typeof setTimeout> | null = null;

watch(query, () => {
  if (debounceTimer) clearTimeout(debounceTimer);
  if (!query.value.trim()) {
    hits.value = [];
    highlightedIndex.value = -1;
    loading.value = false;
    return;
  }
  loading.value = true;
  debounceTimer = setTimeout(runSearch, 150);
});

async function runSearch() {
  const q = query.value.trim();
  if (!q) {
    hits.value = [];
    loading.value = false;
    return;
  }
  loading.value = true;
  try {
    hits.value = await client.search.sessions(q, {});
    highlightedIndex.value = hits.value.length > 0 ? 0 : -1;
  } catch {
    hits.value = [];
  } finally {
    loading.value = false;
  }
}

// ── open / close ────────────────────────────────────────────────────────
watch(
  () => palette.isOpen.value,
  (open) => {
    if (open) {
      query.value = '';
      hits.value = [];
      highlightedIndex.value = -1;
      void nextTick(() => inputEl.value?.focus());
    }
  },
);

function close() {
  palette.close();
}

// ── keyboard navigation ────────────────────────────────────────────────
function onInputKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    close();
    return;
  }
  if (e.key === 'ArrowDown') {
    e.preventDefault();
    highlightedIndex.value = Math.min(
      highlightedIndex.value + 1,
      hits.value.length - 1,
    );
    scrollHitIntoView();
    return;
  }
  if (e.key === 'ArrowUp') {
    e.preventDefault();
    highlightedIndex.value = Math.max(highlightedIndex.value - 1, 0);
    scrollHitIntoView();
    return;
  }
  if (e.key === 'Enter') {
    e.preventDefault();
    openHighlighted();
  }
}

function onResultKeydown(e: KeyboardEvent, idx: number) {
  if (e.key === 'ArrowDown') {
    e.preventDefault();
    highlightedIndex.value = Math.min(idx + 1, hits.value.length - 1);
    scrollHitIntoView();
  } else if (e.key === 'ArrowUp') {
    e.preventDefault();
    if (idx === 0) {
      void nextTick(() => inputEl.value?.focus());
    } else {
      highlightedIndex.value = idx - 1;
      scrollHitIntoView();
    }
  } else if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault();
    openHit(hits.value[idx]);
  } else if (e.key === 'Escape') {
    close();
  }
}

function scrollHitIntoView() {
  void nextTick(() => {
    const list = resultListEl.value;
    if (!list) return;
    const item = list.children[highlightedIndex.value] as HTMLElement | undefined;
    item?.scrollIntoView({ block: 'nearest' });
  });
}

function openHighlighted() {
  const hit = hits.value[highlightedIndex.value];
  if (hit) openHit(hit);
}

function openHit(hit: SearchHit) {
  close();
  void router.push(`/sessions/${hit.sessionId}#${hit.messageId}`);
}

// ── snippet rendering (no v-html) ──────────────────────────────────────
function renderSnippet(text: string, highlights: SearchHighlight[]): VNode[] {
  if (!highlights || highlights.length === 0) {
    return [h('span', text)];
  }
  const enc = new TextEncoder();
  const dec = new TextDecoder();
  const bytes = enc.encode(text);
  const nodes: VNode[] = [];
  let cursor = 0;
  for (const hl of highlights) {
    if (hl.start > cursor) {
      nodes.push(h('span', dec.decode(bytes.slice(cursor, hl.start))));
    }
    nodes.push(
      h(
        'mark',
        { class: 'search-palette-highlight' },
        dec.decode(bytes.slice(hl.start, hl.end)),
      ),
    );
    cursor = hl.end;
  }
  if (cursor < bytes.length) {
    nodes.push(h('span', dec.decode(bytes.slice(cursor))));
  }
  return nodes;
}

// ── relative time ─────────────────────────────────────────────────────
function relativeTime(iso: string): string {
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '';
  const diff = Date.now() - d.getTime();
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return 'just now';
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  if (days < 30) return `${days}d ago`;
  return d.toLocaleDateString();
}

// ── computed ───────────────────────────────────────────────────────────
const hasResults = computed(() => hits.value.length > 0);
const showEmpty = computed(
  () => !loading.value && query.value.trim() && !hasResults.value,
);

onBeforeUnmount(() => {
  if (debounceTimer) clearTimeout(debounceTimer);
});
</script>

<template>
  <!-- Rendered only when palette is open -->
  <div
    v-if="palette.isOpen.value"
    class="fixed inset-0 z-50 flex items-start justify-center pt-16 px-4"
    role="dialog"
    aria-modal="true"
    aria-label="Search"
    data-testid="search-palette"
    @click.self="close"
  >
    <!-- Backdrop -->
    <div
      class="absolute inset-0 bg-modal-overlay"
      data-testid="search-palette-backdrop"
      @click="close"
    />

    <!-- Palette pane — ~600px wide, max ~480px tall -->
    <div
      class="relative w-full max-w-[600px] rounded-md border border-border-strong bg-surface-2 shadow-xl flex flex-col max-h-[480px]"
      data-testid="search-palette-pane"
    >
      <!-- Input row -->
      <div class="px-3 py-2 border-b border-border-muted flex items-center gap-2">
        <span class="text-ink-subtle shrink-0" aria-hidden="true">
          <svg
            width="16"
            height="16"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="2"
          >
            <circle cx="11" cy="11" r="8" />
            <path d="m21 21-4.35-4.35" />
          </svg>
        </span>
        <input
          ref="inputEl"
          v-model="query"
          type="text"
          class="flex-1 bg-transparent font-mono text-sm text-ink outline-none placeholder:text-ink-subtle"
          placeholder="Search messages…"
          aria-label="Search query"
          aria-controls="search-palette-results"
          data-testid="search-palette-input"
          @keydown="onInputKeydown"
        />
        <span
          v-if="loading"
          class="text-xs text-ink-subtle shrink-0"
          aria-live="polite"
        >
          searching…
        </span>
        <kbd
          class="text-xs text-ink-subtle border border-border-muted rounded px-1 shrink-0"
          aria-label="Press Escape to close"
        >
          Esc
        </kbd>
      </div>

      <!-- Results list -->
      <ul
        id="search-palette-results"
        ref="resultListEl"
        role="listbox"
        aria-label="Search results"
        class="overflow-y-auto flex-1"
        data-testid="search-palette-results"
      >
        <!-- Section header (v0.7.0: will expand to more entity kinds) -->
        <li
          v-if="hasResults"
          class="px-4 pt-2 pb-1 text-[10px] uppercase tracking-[0.18em] text-ink-subtle font-ui"
          role="presentation"
        >
          Messages
        </li>

        <!-- Hit rows -->
        <li
          v-for="(hit, idx) in hits"
          :id="`search-palette-hit-${idx}`"
          :key="hit.messageId"
          role="option"
          :aria-selected="idx === highlightedIndex"
          :tabindex="0"
          class="px-4 py-3 cursor-pointer border-b border-border-muted last:border-0 flex flex-col gap-0.5"
          :class="{
            'bg-surface-3': idx === highlightedIndex,
            'hover:bg-surface-1': idx !== highlightedIndex,
          }"
          :data-testid="`search-palette-hit-${idx}`"
          @click="openHit(hit)"
          @keydown="onResultKeydown($event, idx)"
          @mouseenter="highlightedIndex = idx"
        >
          <!-- Session name + timestamp -->
          <div class="flex items-center gap-2 text-xs text-ink-subtle">
            <span class="font-medium text-ink truncate max-w-[200px]">
              {{ hit.sessionName }}
            </span>
            <span
              class="px-1 py-0.5 rounded bg-surface-1 border border-border-muted capitalize"
            >
              {{ hit.role }}
            </span>
            <span class="ml-auto shrink-0">{{ relativeTime(hit.createdAt) }}</span>
          </div>
          <!-- Snippet with inline highlights -->
          <p class="text-sm text-ink font-mono leading-relaxed truncate">
            <component :is="() => renderSnippet(hit.snippet, hit.highlights)" />
          </p>
        </li>

        <!-- Empty state — no results for a query -->
        <li
          v-if="showEmpty"
          class="px-4 py-8 text-center text-sm text-ink-subtle"
          aria-live="polite"
          data-testid="search-palette-empty"
        >
          No results for "{{ query }}"
        </li>

        <!-- Empty state — no query entered yet -->
        <li
          v-if="!query.trim() && !loading"
          class="px-4 py-8 text-center text-sm text-ink-subtle"
          data-testid="search-palette-placeholder"
        >
          Type to search across all sessions
        </li>
      </ul>

      <!-- Footer hint (shown when there are results) -->
      <div
        v-if="hasResults"
        class="px-3 py-1.5 border-t border-border-muted text-xs text-ink-subtle flex gap-3"
        aria-hidden="true"
      >
        <span>↑↓ navigate</span>
        <span>↵ open</span>
        <span>Esc close</span>
        <span class="ml-auto">
          {{ hits.length }} result{{ hits.length === 1 ? '' : 's' }}
        </span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.search-palette-highlight {
  background-color: color-mix(in srgb, var(--color-accent-a, #f59e0b) 35%, transparent); /* css-tokens-allow: --color-accent-a fallback for older render paths */
  border-radius: 2px;
  padding: 0 1px;
}
</style>
