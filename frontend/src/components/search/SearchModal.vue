<script setup lang="ts">
import { ref, computed, watch, nextTick, onMounted, onBeforeUnmount, h, type VNode } from 'vue';
import { useRouter } from 'vue-router';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { SearchHit, SearchHighlight, SearchFilters } from '@/lib/harnessClient';
import { useProjects } from '@/lib/useHarnessAPI';

/**
 * SearchModal — Cmd-F full-text search modal (cross-session-search mission).
 *
 * Open: Cmd-F (Mac) / Ctrl-F (other) — registered globally in Shell.vue.
 * Close: Esc key or clicking the backdrop.
 *
 * Keyboard navigation:
 *   - Up / Down arrow keys from the input (or within the results list)
 *     move the highlighted result.
 *   - Enter opens the highlighted result.
 *   - Tab cycles: input → role-filter → results.
 *
 * Snippet rendering: server returns plain text + byte-offset highlight
 * ranges. renderSnippet() builds VNodes with <mark> spans — no v-html.
 */

const emit = defineEmits<{
  close: [];
}>();

const client = useHarnessClient();
const router = useRouter();
const { list: projects, refresh: refreshProjects } = useProjects();

// ── state ──────────────────────────────────────────────────────────────
const inputEl = ref<HTMLInputElement | null>(null);
const query = ref('');
const roleFilter = ref('');
const projectFilter = ref('');
const hits = ref<SearchHit[]>([]);
const loading = ref(false);
const highlightedIndex = ref(-1);

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

watch([roleFilter, projectFilter], () => {
  if (query.value.trim()) {
    if (debounceTimer) clearTimeout(debounceTimer);
    debounceTimer = setTimeout(runSearch, 150);
  }
});

async function runSearch() {
  const q = query.value.trim();
  if (!q) {
    hits.value = [];
    loading.value = false;
    return;
  }
  const filters: SearchFilters = {};
  if (roleFilter.value) filters.roleFilter = roleFilter.value;
  if (projectFilter.value) filters.projectId = projectFilter.value;
  loading.value = true;
  try {
    hits.value = await client.search.sessions(q, filters);
    highlightedIndex.value = hits.value.length > 0 ? 0 : -1;
  } catch {
    hits.value = [];
  } finally {
    loading.value = false;
  }
}

// ── keyboard navigation ────────────────────────────────────────────────
function onInputKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    emit('close');
    return;
  }
  if (e.key === 'ArrowDown') {
    e.preventDefault();
    highlightedIndex.value = Math.min(highlightedIndex.value + 1, hits.value.length - 1);
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
    emit('close');
  }
}

const resultListEl = ref<HTMLElement | null>(null);

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
  emit('close');
  void router.push(`/sessions/${hit.sessionId}#${hit.messageId}`);
}

// ── snippet rendering (no v-html) ──────────────────────────────────────
/**
 * renderSnippet — splits a plain-text snippet at highlight byte-offset
 * ranges and returns an array of VNodes (plain spans + <mark> spans).
 * Safe: text is TextNode-escaped by Vue's h(); no innerHTML involved.
 */
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
    nodes.push(h('mark', { class: 'search-highlight' }, dec.decode(bytes.slice(hl.start, hl.end))));
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

// ── lifecycle ──────────────────────────────────────────────────────────
onMounted(() => {
  void nextTick(() => inputEl.value?.focus());
  void refreshProjects();
});

onBeforeUnmount(() => {
  if (debounceTimer) clearTimeout(debounceTimer);
});

// ── computed ───────────────────────────────────────────────────────────
const hasResults = computed(() => hits.value.length > 0);
const showEmpty = computed(
  () => !loading.value && query.value.trim() && !hasResults.value,
);

// ── project name lookup ────────────────────────────────────────────────
const projectMap = computed(() => {
  const m = new Map<string, string>();
  for (const p of projects.value) m.set(p.id, p.name);
  return m;
});
</script>

<template>
  <!-- Backdrop -->
  <div
    class="fixed inset-0 z-50 flex items-start justify-center pt-16 px-4"
    role="dialog"
    aria-modal="true"
    aria-label="Search sessions"
    @click.self="$emit('close')"
  >
    <div class="absolute inset-0 bg-modal-overlay" @click="$emit('close')" />

    <!-- Modal panel -->
    <div
      class="relative w-full max-w-2xl rounded-md border border-border-strong bg-surface-2 shadow-xl flex flex-col max-h-[80vh]"
    >
      <!-- Search input -->
      <div class="px-3 py-2 border-b border-border-muted flex items-center gap-2">
        <span class="text-ink-subtle" aria-hidden="true">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="11" cy="11" r="8" /><path d="m21 21-4.35-4.35" />
          </svg>
        </span>
        <input
          ref="inputEl"
          v-model="query"
          type="text"
          class="flex-1 bg-transparent font-mono text-sm text-ink outline-none placeholder:text-ink-subtle"
          placeholder="Search messages…"
          aria-label="Search query"
          aria-controls="search-results"
          aria-activedescendant="search-results"
          @keydown="onInputKeydown"
        />
        <span v-if="loading" class="text-xs text-ink-subtle">searching…</span>
        <kbd
          class="text-xs text-ink-subtle border border-border-muted rounded px-1"
          aria-label="Press Escape to close"
        >
          Esc
        </kbd>
      </div>

      <!-- Filters row -->
      <div class="px-3 py-1.5 border-b border-border-muted flex items-center gap-3 text-xs">
        <label class="flex items-center gap-1 text-ink-subtle">
          Role
          <select
            v-model="roleFilter"
            class="bg-surface-1 border border-border-muted rounded px-1 py-0.5 text-ink text-xs outline-none focus:ring-1 focus:ring-accent-a"
            aria-label="Filter by role"
          >
            <option value="">All roles</option>
            <option value="user">User</option>
            <option value="assistant">Assistant</option>
            <option value="system">System</option>
          </select>
        </label>
        <label class="flex items-center gap-1 text-ink-subtle">
          Project
          <select
            v-model="projectFilter"
            class="bg-surface-1 border border-border-muted rounded px-1 py-0.5 text-ink text-xs outline-none focus:ring-1 focus:ring-accent-a"
            aria-label="Filter by project"
          >
            <option value="">All projects</option>
            <option v-for="p in projects" :key="p.id" :value="p.id">
              {{ p.name }}
            </option>
          </select>
        </label>
      </div>

      <!-- Results list -->
      <ul
        id="search-results"
        ref="resultListEl"
        role="listbox"
        aria-label="Search results"
        class="overflow-y-auto flex-1"
      >
        <!-- hit rows -->
        <li
          v-for="(hit, idx) in hits"
          :id="`search-hit-${idx}`"
          :key="hit.messageId"
          role="option"
          :aria-selected="idx === highlightedIndex"
          :tabindex="0"
          class="px-4 py-3 cursor-pointer border-b border-border-muted last:border-0 flex flex-col gap-0.5"
          :class="{
            'bg-surface-3': idx === highlightedIndex,
            'hover:bg-surface-1': idx !== highlightedIndex,
          }"
          @click="openHit(hit)"
          @keydown="onResultKeydown($event, idx)"
          @mouseenter="highlightedIndex = idx"
        >
          <!-- session name + meta -->
          <div class="flex items-center gap-2 text-xs text-ink-subtle">
            <span class="font-medium text-ink truncate max-w-[200px]">
              {{ hit.sessionName }}
            </span>
            <span class="px-1 py-0.5 rounded bg-surface-1 border border-border-muted capitalize">
              {{ hit.role }}
            </span>
            <span v-if="hit.projectId && projectMap.get(hit.projectId)" class="truncate max-w-[120px]">
              {{ projectMap.get(hit.projectId) }}
            </span>
            <span class="ml-auto shrink-0">{{ relativeTime(hit.createdAt) }}</span>
          </div>
          <!-- snippet with inline marks -->
          <p class="text-sm text-ink font-mono leading-relaxed truncate">
            <component
              :is="() => renderSnippet(hit.snippet, hit.highlights)"
            />
          </p>
        </li>

        <!-- empty state -->
        <li
          v-if="showEmpty"
          class="px-4 py-8 text-center text-sm text-ink-subtle"
          aria-live="polite"
        >
          No results for "{{ query }}"
        </li>

        <!-- placeholder before first query -->
        <li
          v-if="!query.trim() && !loading"
          class="px-4 py-8 text-center text-sm text-ink-subtle"
        >
          Type to search across all sessions
        </li>
      </ul>

      <!-- footer hint -->
      <div
        v-if="hasResults"
        class="px-3 py-1.5 border-t border-border-muted text-xs text-ink-subtle flex gap-3"
        aria-hidden="true"
      >
        <span>↑↓ navigate</span>
        <span>↵ open</span>
        <span>Esc close</span>
        <span class="ml-auto">{{ hits.length }} result{{ hits.length === 1 ? '' : 's' }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.search-highlight {
  background-color: color-mix(in srgb, var(--color-accent-a, #f59e0b) 35%, transparent);
  border-radius: 2px;
  padding: 0 1px;
}
</style>
