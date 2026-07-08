<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type { ContextHealth } from '@/lib/harnessClient';

/**
 * ContextHealthCard — a COMPACT rollup of the user's context graph
 * (context-bootstrap-harness-integration WP07b). Reads ContextBootstrap_Health
 * and shows: total nodes, by-source counts, last sync, connected sources, and
 * a "re-run" button that re-kicks a bootstrap run over the connected sources.
 *
 * This intentionally does NOT rebuild the full fleet dashboard health view — it
 * is a small, self-contained card meant for the onboarding tail + a settings
 * corner. When fleet is disabled the health rollup is all-zero and the card
 * renders an empty state.
 */

const client = useHarnessClient();

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

const lastSyncLabel = computed<string>(() => {
  const ts = health.value?.last_sync;
  if (!ts) return 'never';
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleString();
});

async function rerun(): Promise<void> {
  if (rerunning.value) return;
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
  <div
    class="rounded-md border border-border bg-surface-2 p-3 flex flex-col gap-2"
    data-testid="context-health-card"
  >
    <div class="flex items-center justify-between">
      <span class="font-ui text-sm font-medium text-ink">Context health</span>
      <button
        type="button"
        class="font-ui text-xs text-ink-muted hover:text-ink px-2 py-1 rounded-sm disabled:opacity-50"
        :disabled="rerunning || loading"
        data-testid="context-health-rerun"
        @click="rerun"
      >
        {{ rerunning ? 'Re-running…' : 'Re-run' }}
      </button>
    </div>

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
          <template v-if="health.connected_sources.length">
            {{ health.connected_sources.join(', ') }}
          </template>
          <template v-else>none</template>
        </span>
        <span
          v-if="health.latest_run"
          class="font-ui text-xs text-ink-muted"
          data-testid="context-health-latest-run"
        >
          Latest run: {{ health.latest_run.status }}
        </span>
      </div>
    </template>
  </div>
</template>
