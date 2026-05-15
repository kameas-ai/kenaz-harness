<script setup lang="ts">
/**
 * HookJournalView — tail of the greedy-memory hook journal (Bundle E
 * WP16). The kernel fires a hook at every boundary (post-llm,
 * post-tool, on-merge, on-checkpoint, ...) and writes a row to the
 * journal whether the content was committed, deduplicated, or
 * skipped. This view surfaces the tail so users can audit what
 * greedy memory is capturing on their behalf.
 *
 * The view is read-only — the inspector's pin / forget controls live
 * on MemoryView. Auto-refresh is disabled by default; the user can
 * click "Refresh" to fetch the latest tail.
 */
import { computed, onMounted, ref } from 'vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import type { MemoryJournalEntry } from '@/lib/types';

const client = useHarnessClient();

type ScopePill = 'all' | 'global' | 'project' | 'session';

const entries = ref<readonly MemoryJournalEntry[]>([]);
const loading = ref(false);
const error = ref<string | null>(null);
const scopeFilter = ref<ScopePill>('all');
const filterText = ref('');

async function refresh() {
  loading.value = true;
  error.value = null;
  try {
    const scope = scopeFilter.value === 'all' ? '' : scopeFilter.value;
    entries.value = await client.memory.journalTail(scope, 0, 200);
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
    entries.value = [];
  } finally {
    loading.value = false;
  }
}

const filtered = computed(() => {
  const q = filterText.value.toLowerCase().trim();
  if (!q) return entries.value;
  return entries.value.filter((e) => {
    const hay = [
      e.boundary,
      e.scope,
      e.title ?? '',
      e.source ?? '',
      e.skipReason ?? '',
      e.chunkId ?? '',
    ]
      .join(' ')
      .toLowerCase();
    return hay.includes(q);
  });
});

function statusOf(e: MemoryJournalEntry): { label: string; cls: string } {
  if (e.skipped) return { label: 'skipped', cls: 'text-signal-warning' };
  if (e.deduped) return { label: 'deduped', cls: 'text-ink-muted' };
  if (e.written) return { label: 'written', cls: 'text-signal-success' };
  return { label: 'unknown', cls: 'text-ink-muted' };
}

function setScope(p: ScopePill) {
  if (scopeFilter.value === p) return;
  scopeFilter.value = p;
  void refresh();
}

function formatTs(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

onMounted(() => {
  void refresh();
});

defineExpose({ refresh });
</script>

<template>
  <div data-testid="hook-journal-view">
    <div class="px-6 py-4 max-w-5xl">
      <header class="mb-4">
        <h2 class="font-ui text-[16px] uppercase tracking-[0.18em] text-ink">
          Hook journal
        </h2>
        <p class="mt-1 font-ui text-sm text-ink-muted">
          Greedy memory writes captured at each kernel boundary. This
          is the audit trail for what the harness chose to remember on
          your behalf.
        </p>
      </header>

      <div
        class="mb-3 flex flex-wrap items-center gap-2"
        data-testid="hook-journal-controls"
      >
        <button
          v-for="p in (['all', 'global', 'project', 'session'] as const)"
          :key="p"
          type="button"
          :data-testid="`hook-journal-scope-${p}`"
          class="px-3 py-1 rounded-sm border text-[11px] uppercase tracking-[0.18em] font-ui"
          :class="
            scopeFilter === p
              ? 'border-accent text-accent bg-surface-2'
              : 'border-border-muted text-ink-dim hover:bg-surface-2'
          "
          @click="setScope(p)"
        >
          {{ p }}
        </button>
        <input
          v-model="filterText"
          type="search"
          aria-label="Filter hook journal entries"
          placeholder="Filter by boundary, source, hash…"
          class="ml-auto px-2 py-1 rounded-sm border border-border-muted bg-surface-1 font-ui text-[12px] text-ink placeholder:text-ink-subtle"
          data-testid="hook-journal-filter"
        />
        <button
          type="button"
          class="px-3 py-1 rounded-sm border border-border-muted font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
          data-testid="hook-journal-refresh"
          @click="refresh"
        >
          Refresh
        </button>
      </div>

      <div
        v-if="error"
        class="mb-3 rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>

      <div
        v-if="loading"
        class="font-ui text-[12px] text-ink-muted"
        role="status"
      >
        Loading hook journal…
      </div>

      <div
        v-else-if="filtered.length === 0"
        class="rounded-md border border-border-muted bg-surface-1 px-4 py-6 text-center"
        data-testid="hook-journal-empty"
      >
        <p class="font-ui text-sm text-ink">
          No journal entries yet. The kernel writes one row per hook
          fire — try sending a message in the chat surface.
        </p>
      </div>

      <table
        v-else
        class="w-full table-auto text-left font-ui text-[12px] text-ink"
        data-testid="hook-journal-table"
      >
        <thead>
          <tr class="border-b border-border-muted text-[10px] uppercase tracking-[0.18em] text-ink-subtle">
            <th class="px-2 py-1 font-normal">When</th>
            <th class="px-2 py-1 font-normal">Boundary</th>
            <th class="px-2 py-1 font-normal">Scope</th>
            <th class="px-2 py-1 font-normal">Status</th>
            <th class="px-2 py-1 font-normal">Title / source</th>
            <th class="px-2 py-1 font-normal">Hash</th>
          </tr>
        </thead>
        <tbody>
          <tr
            v-for="entry in filtered"
            :key="entry.seq"
            class="border-b border-border-muted/40"
            :data-testid="`hook-journal-row-${entry.seq}`"
          >
            <td class="px-2 py-1 font-mono text-[11px] text-ink-dim">
              {{ formatTs(entry.at) }}
            </td>
            <td class="px-2 py-1 font-mono text-[11px]">
              {{ entry.boundary }}
            </td>
            <td class="px-2 py-1">{{ entry.scope }}</td>
            <td
              class="px-2 py-1 font-mono text-[11px]"
              :class="statusOf(entry).cls"
            >
              {{ statusOf(entry).label }}
              <span
                v-if="entry.skipReason"
                class="text-ink-subtle font-normal"
              >
                · {{ entry.skipReason }}
              </span>
            </td>
            <td class="px-2 py-1">
              {{ entry.title || entry.source || '—' }}
            </td>
            <td class="px-2 py-1 font-mono text-[10px] text-ink-subtle">
              {{ (entry.contentHash ?? '').slice(0, 12) || '—' }}
            </td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
