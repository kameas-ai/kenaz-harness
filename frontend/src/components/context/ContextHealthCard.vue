<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { ContextHealth } from '@/lib/harnessClient';
import { readContextHealthExpanded, writeContextHealthExpanded } from './contextHealthPref';

/**
 * ContextHealthCard — a COMPACT rollup of the user's context graph
 * (context-bootstrap-harness-integration WP07b). Reads ContextBootstrap_Health
 * and shows: total nodes, by-source counts, last sync, connected sources, and
 * a "re-run" button that re-kicks a bootstrap run over the connected sources.
 *
 * knowledge-home-01DOGF0E WP06 (spec FR-8, dogfood F11 — "context health is
 * far too prominent for something that i assume will rarely be used"): it
 * renders as a ONE-LINE status chip by default and expands to the card on
 * click. The expanded/collapsed choice is remembered per device in
 * localStorage (contextHealthPref.ts); a profile with no stored value — every
 * upgraded install — gets the default, collapsed. Two honesty fixes ride
 * along:
 *   - "Re-run" is disabled with a reason when no source is connected. It
 *     used to start a bootstrap over ZERO sources, which is the likely origin
 *     of "Latest run: completed" beside "Last sync: never / Connected: none".
 *   - The latest-run row says what ran: a fleet context-bootstrap scan
 *     (core/fleet/context_bootstrap.go BootstrapLatestRun), not an
 *     unexplained "run".
 */

const client = useHarnessClient();

const expanded = ref<boolean>(readContextHealthExpanded());

function toggleExpanded(): void {
  expanded.value = !expanded.value;
  writeContextHealthExpanded(expanded.value);
}

const health = ref<ContextHealth | null>(null);
const loading = ref(true);
const error = ref<string | null>(null);
const rerunning = ref(false);

async function load(): Promise<void> {
  loading.value = true;
  error.value = null;
  try {
    health.value = await client.contextBootstrap.health();
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    loading.value = false;
  }
}

onMounted(load);

const sourceRows = computed<Array<{ kind: string; count: number }>>(() => {
  const map = health.value?.nodes_by_source_kind ?? {};
  return Object.keys(map)
    .sort()
    .map((kind) => ({ kind, count: map[kind] }));
});

function formatTs(ts: string | undefined): string | null {
  if (!ts) return null;
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleString();
}

const lastSyncLabel = computed<string>(() => formatTs(health.value?.last_sync) ?? 'never');

const connectedCount = computed<number>(() => health.value?.connected_sources?.length ?? 0);

/** Why Re-run is disabled, or null when it can run. */
const rerunDisabledReason = computed<string | null>(() => {
  if (!health.value) return null;
  if (connectedCount.value === 0) return 'No sources connected — connect a source to scan.';
  return null;
});

/** One-line chip summary: "0 nodes · never synced". */
const chipSummary = computed<string>(() => {
  if (loading.value) return 'loading…';
  if (error.value) return 'unavailable';
  const h = health.value;
  if (!h) return 'unavailable';
  const nodes = `${h.total_nodes} node${h.total_nodes === 1 ? '' : 's'}`;
  const synced = h.last_sync ? `synced ${lastSyncLabel.value}` : 'never synced';
  return `${nodes} · ${synced}`;
});

const latestRunLabel = computed<string | null>(() => {
  const run = health.value?.latest_run;
  if (!run) return null;
  const finished = formatTs(run.finished_at);
  return `Latest bootstrap scan: ${run.status}${finished ? ` · ${finished}` : ''}`;
});

async function rerun(): Promise<void> {
  if (rerunning.value || rerunDisabledReason.value) return;
  rerunning.value = true;
  error.value = null;
  try {
    const sources = health.value?.connected_sources ?? [];
    await client.contextBootstrap.start({ consented_sources: sources });
    await load();
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : String(e);
  } finally {
    rerunning.value = false;
  }
}
</script>

<template>
  <div class="relative" data-testid="context-health">
    <button
      type="button"
      class="flex items-center gap-1.5 rounded-sm border border-border-muted bg-surface-1 px-2 py-0.5 font-ui text-[11px] text-ink-muted hover:bg-surface-2 hover:text-ink"
      :aria-expanded="expanded"
      aria-controls="context-health-card"
      data-testid="context-health-chip"
      @click="toggleExpanded"
    >
      <span class="text-ink-subtle">Context health:</span>
      <span data-testid="context-health-chip-summary">{{ chipSummary }}</span>
      <span aria-hidden="true">{{ expanded ? '▾' : '▸' }}</span>
    </button>

    <div
      v-if="expanded"
      id="context-health-card"
      class="absolute right-0 z-20 mt-1 w-72 rounded-md border border-border bg-surface-2 p-3 flex flex-col gap-2 shadow-lg"
      data-testid="context-health-card"
    >
      <div class="flex items-center justify-between">
        <span class="font-ui text-sm font-medium text-ink">Context health</span>
        <button
          type="button"
          class="font-ui text-xs text-ink-muted hover:text-ink px-2 py-1 rounded-sm disabled:opacity-50 disabled:hover:text-ink-muted"
          :disabled="rerunning || loading || rerunDisabledReason !== null"
          :title="rerunDisabledReason ?? undefined"
          data-testid="context-health-rerun"
          @click="rerun"
        >
          {{ rerunning ? 'Re-running…' : 'Re-run' }}
        </button>
      </div>
      <p
        v-if="rerunDisabledReason"
        class="font-ui text-[11px] text-ink-subtle"
        data-testid="context-health-rerun-reason"
      >
        {{ rerunDisabledReason }}
      </p>

      <p v-if="loading" class="font-ui text-xs text-ink-muted">Loading…</p>

      <p v-else-if="error" class="font-ui text-xs text-signal-warning" data-testid="context-health-error">
        {{ error }}
      </p>

      <template v-else-if="health">
        <div class="flex items-baseline gap-2">
          <span class="font-ui text-2xl font-semibold text-ink" data-testid="context-health-total">
            {{ health.total_nodes }}
          </span>
          <span class="font-ui text-xs text-ink-muted">context nodes</span>
        </div>

        <ul v-if="sourceRows.length" class="flex flex-col gap-0.5">
          <li
            v-for="row in sourceRows"
            :key="row.kind"
            class="flex items-center justify-between font-ui text-xs text-ink-muted"
          >
            <span>{{ row.kind }}</span>
            <span class="tabular-nums">{{ row.count }}</span>
          </li>
        </ul>

        <div class="flex flex-col gap-0.5 pt-1 border-t border-border">
          <span class="font-ui text-xs text-ink-muted">
            Last sync: {{ lastSyncLabel }}
          </span>
          <span class="font-ui text-xs text-ink-muted">
            Connected:
            <template v-if="health.connected_sources?.length">
              {{ health.connected_sources.join(', ') }}
            </template>
            <template v-else>none</template>
          </span>
          <span
            v-if="latestRunLabel"
            class="font-ui text-xs text-ink-muted"
            data-testid="context-health-latest-run"
          >
            {{ latestRunLabel }}
          </span>
        </div>
      </template>
    </div>
  </div>
</template>
