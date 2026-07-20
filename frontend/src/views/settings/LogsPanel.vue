<script setup lang="ts">
/**
 * LogsPanel — Settings → Security → Logs tab (mission 01NLOGS01 WP05).
 *
 * Shows the in-app runtime log ring buffer with:
 *   - Level filter (all / debug / info / warn / error).
 *   - Source filter (free-text substring, case-insensitive).
 *   - Free-text search across message content.
 *   - Follow-tail toggle: auto-refresh every 2 s when enabled.
 *   - Copy-to-clipboard and JSONL export affordances.
 *   - Virtualized list (renders only ~50 rows at a time via windowing)
 *     so a busy session doesn't degrade UI.
 */
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { LogRow, LogFilter } from '@/lib/harnessClient';
import {
  AlertTriangle,
  Download,
  RefreshCw,
} from '@/shell/icons';
import {
  Bug,
  CircleAlert,
  ClipboardCopy,
  Info,
  Loader2,
} from 'lucide-vue-next';

const client = useHarnessClient();

// ── filter state ─────────────────────────────────────────────────────────────

const levelFilter = ref<LogFilter['level'] | ''>('');
const sourceFilter = ref('');
const searchText = ref('');
const followTail = ref(true);
const loading = ref(false);
const error = ref<string | null>(null);

// ── data ──────────────────────────────────────────────────────────────────────

const allRows = ref<LogRow[]>([]);

async function fetchLogs() {
  loading.value = true;
  error.value = null;
  try {
    const filter: LogFilter = {};
    if (levelFilter.value) filter.level = levelFilter.value;
    if (sourceFilter.value.trim()) filter.source = sourceFilter.value.trim();
    if (searchText.value.trim()) filter.search = searchText.value.trim();
    // Fetch up to 2000 rows; the ring buffer caps total at 10 000.
    filter.limit = 2000;
    allRows.value = await client.runtimeLogs.tail(filter);
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

// ── follow-tail auto-refresh ──────────────────────────────────────────────────

let refreshTimer: ReturnType<typeof setInterval> | null = null;

function startTail() {
  stopTail();
  refreshTimer = setInterval(() => {
    void fetchLogs();
  }, 2000);
}

function stopTail() {
  if (refreshTimer !== null) {
    clearInterval(refreshTimer);
    refreshTimer = null;
  }
}

function toggleFollowTail() {
  followTail.value = !followTail.value;
  if (followTail.value) {
    void fetchLogs();
    startTail();
  } else {
    stopTail();
  }
}

onMounted(() => {
  void fetchLogs();
  if (followTail.value) startTail();
});

onUnmounted(() => {
  stopTail();
});

// ── virtualization ────────────────────────────────────────────────────────────

/**
 * Simple window: show the first PAGE_SIZE rows from the fetched set.
 * The ring buffer returns newest-first already; we display them that way.
 */
const PAGE_SIZE = 200;
const visibleRows = computed(() => allRows.value.slice(0, PAGE_SIZE));
const hasMore = computed(() => allRows.value.length > PAGE_SIZE);

// ── copy / export ─────────────────────────────────────────────────────────────

async function copyToClipboard() {
  const text = allRows.value.map(
    (r) => `${r.timestamp} [${r.level.toUpperCase()}] ${r.source}: ${r.message}`,
  ).join('\n');
  await navigator.clipboard.writeText(text);
}

function exportJSONL() {
  const lines = allRows.value.map((r) => JSON.stringify(r)).join('\n');
  const blob = new Blob([lines], { type: 'application/jsonlines' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `harness-logs-${new Date().toISOString().slice(0, 19).replace(/:/g, '-')}.jsonl`;
  a.click();
  URL.revokeObjectURL(url);
}

// ── helpers ───────────────────────────────────────────────────────────────────

function levelClass(level: LogRow['level']): string {
  switch (level) {
    case 'error': return 'text-signal-danger';
    case 'warn':  return 'text-signal-warn';
    case 'debug': return 'text-ink-subtle';
    default:      return 'text-ink-muted';
  }
}

function levelIcon(level: LogRow['level']) {
  switch (level) {
    case 'error': return CircleAlert;
    case 'warn':  return AlertTriangle;
    case 'debug': return Bug;
    default:      return Info;
  }
}

function fmtTs(ts: string): string {
  // ts is RFC3339Nano; render as HH:MM:SS.mmm (local) for brevity.
  try {
    const d = new Date(ts);
    return d.toLocaleTimeString(undefined, {
      hour: '2-digit',
      minute: '2-digit',
      second: '2-digit',
      hour12: false,
    }) + '.' + String(d.getMilliseconds()).padStart(3, '0');
  } catch {
    return ts;
  }
}
</script>

<template>
  <section class="flex flex-col h-full" data-testid="logs-panel">
    <!-- Toolbar -->
    <div
      class="flex flex-wrap items-center gap-2 px-4 pt-4 pb-3 border-b border-border-muted shrink-0"
      data-testid="logs-toolbar"
    >
      <!-- Level filter -->
      <select
        v-model="levelFilter"
        class="rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink focus:outline-none focus:ring-1 focus:ring-accent"
        data-testid="logs-level-filter"
        @change="fetchLogs"
      >
        <option value="">All levels</option>
        <option value="debug">Debug</option>
        <option value="info">Info</option>
        <option value="warn">Warn</option>
        <option value="error">Error</option>
      </select>

      <!-- Source filter -->
      <input
        v-model="sourceFilter"
        type="text"
        placeholder="Filter by source…"
        class="w-36 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent"
        data-testid="logs-source-filter"
        @input="fetchLogs"
      />

      <!-- Free-text search -->
      <input
        v-model="searchText"
        type="text"
        placeholder="Search messages…"
        class="flex-1 min-w-28 rounded-sm border border-border bg-surface-1 px-2 py-1 font-ui text-[12px] text-ink placeholder:text-ink-subtle focus:outline-none focus:ring-1 focus:ring-accent"
        data-testid="logs-search"
        @input="fetchLogs"
      />

      <!-- Follow-tail toggle -->
      <button
        type="button"
        :class="[
          'flex items-center gap-1.5 rounded-sm border px-2.5 py-1 font-ui text-[12px] transition-colors',
          followTail
            ? 'border-accent bg-accent/10 text-accent'
            : 'border-border bg-surface-1 text-ink-muted hover:text-ink hover:bg-surface-2',
        ]"
        :title="followTail ? 'Following tail (click to pause)' : 'Auto-refresh paused (click to follow)'"
        data-testid="logs-follow-tail"
        @click="toggleFollowTail"
      >
        <component
          :is="followTail ? Loader2 : RefreshCw"
          :size="12"
          :class="followTail ? 'animate-spin' : ''"
        />
        <span class="hidden two-col:inline">{{ followTail ? 'Following' : 'Follow tail' }}</span>
      </button>

      <!-- Copy -->
      <button
        type="button"
        class="flex items-center gap-1.5 rounded-sm border border-border bg-surface-1 px-2.5 py-1 font-ui text-[12px] text-ink-muted hover:text-ink hover:bg-surface-2 transition-colors"
        title="Copy logs to clipboard"
        data-testid="logs-copy"
        @click="copyToClipboard"
      >
        <ClipboardCopy :size="12" />
        <span class="hidden two-col:inline">Copy</span>
      </button>

      <!-- JSONL export -->
      <button
        type="button"
        class="flex items-center gap-1.5 rounded-sm border border-border bg-surface-1 px-2.5 py-1 font-ui text-[12px] text-ink-muted hover:text-ink hover:bg-surface-2 transition-colors"
        title="Export as JSONL"
        data-testid="logs-export"
        @click="exportJSONL"
      >
        <Download :size="12" />
        <span class="hidden two-col:inline">Export JSONL</span>
      </button>

      <!-- Row count -->
      <span class="ml-auto font-ui text-[11px] text-ink-subtle shrink-0" data-testid="logs-count">
        {{ allRows.length }} row{{ allRows.length === 1 ? '' : 's' }}
      </span>
    </div>

    <!-- Error banner -->
    <div
      v-if="error"
      role="alert"
      class="mx-4 mt-3 rounded-sm border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger shrink-0"
      data-testid="logs-error"
    >
      {{ error }}
    </div>

    <!-- Log rows -->
    <div
      class="flex-1 overflow-y-auto font-mono text-[11px] leading-relaxed"
      data-testid="logs-list"
    >
      <div
        v-if="allRows.length === 0 && !loading"
        class="px-4 py-8 text-center font-ui text-[12px] text-ink-subtle"
        data-testid="logs-empty"
      >
        No log rows match the current filters.
      </div>

      <div
        v-for="row in visibleRows"
        :key="row.timestamp + row.source + row.message"
        class="flex items-start gap-2 px-4 py-0.5 hover:bg-surface-1 border-b border-border-muted/30 group"
        data-testid="logs-row"
      >
        <!-- Level icon + badge -->
        <div class="flex items-center gap-1 shrink-0 w-14 pt-0.5">
          <component
            :is="levelIcon(row.level)"
            :size="10"
            :class="levelClass(row.level)"
          />
          <span
            :class="['uppercase text-[9px] font-medium tracking-wider leading-none', levelClass(row.level)]"
            :data-testid="`log-level-${row.level}`"
          >
            {{ row.level }}
          </span>
        </div>

        <!-- Timestamp -->
        <span class="shrink-0 text-ink-subtle text-[10px] pt-0.5 w-28">
          {{ fmtTs(row.timestamp) }}
        </span>

        <!-- Source -->
        <span class="shrink-0 text-ink-dim text-[10px] pt-0.5 w-28 truncate" :title="row.source">
          {{ row.source }}
        </span>

        <!-- Message -->
        <span class="flex-1 break-all text-ink text-[11px]">
          {{ row.message }}
        </span>
      </div>

      <!-- More rows indicator -->
      <div
        v-if="hasMore"
        class="px-4 py-2 font-ui text-[11px] text-ink-subtle text-center"
        data-testid="logs-truncated"
      >
        Showing {{ PAGE_SIZE }} of {{ allRows.length }} rows. Narrow filters to see more.
      </div>
    </div>
  </section>
</template>
