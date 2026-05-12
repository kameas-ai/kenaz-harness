<script setup lang="ts">
/**
 * ScheduledInbox — scheduled-runs inbox (workflow-extensions-01KW2D3Y WP03).
 *
 * Replaces the "Run history coming soon." stub in WorkflowsView.vue.
 *
 * Shows one accordion row per scheduled workflow; each row lists up to 20
 * recent runs with status badges, duration, and per-run re-run / cancel
 * affordances.  Auto-refreshes every 30 seconds while mounted.
 */
import { ref, onMounted, onUnmounted, computed } from 'vue';
import type {
  WorkflowsClient,
  WorkflowsScheduleEntry,
  WorkflowsRunSummary,
} from '@/lib/workflowsClient';

const props = defineProps<{
  client: WorkflowsClient;
}>();

// ── state ─────────────────────────────────────────────────────────────────

interface ScheduledRow {
  entry: WorkflowsScheduleEntry;
  nextFire: string; // ISO or ''
  runs: WorkflowsRunSummary[];
  expanded: boolean;
  loadingRuns: boolean;
  loadError: string | null;
}

const rows = ref<ScheduledRow[]>([]);
const loading = ref(false);
const loadError = ref<string | null>(null);

const hasRows = computed(() => rows.value.length > 0);

// ── helpers ───────────────────────────────────────────────────────────────

function statusClass(status: string): string {
  if (status === 'completed') return 'text-emerald-400';
  if (status === 'running') return 'text-amber-400';
  if (status === 'failed') return 'text-red-400';
  if (status === 'cancelled') return 'text-ink-muted';
  return 'text-ink-muted';
}

function fmtTime(iso: string): string {
  if (!iso) return '—';
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

function fmtDuration(start: string, end?: string): string {
  if (!start || !end) return '';
  const ms = new Date(end).getTime() - new Date(start).getTime();
  if (ms < 0) return '';
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)}s`;
  const mins = Math.floor(ms / 60_000);
  const secs = Math.floor((ms % 60_000) / 1000);
  return `${mins}m ${secs}s`;
}

// ── data loading ──────────────────────────────────────────────────────────

async function loadInbox() {
  loading.value = true;
  loadError.value = null;
  try {
    const schedules = await props.client.scheduleList();
    // Build/merge rows preserving existing expansion state
    const existing = new Map<string, ScheduledRow>(
      rows.value.map((r) => [r.entry.workflowId, r]),
    );
    const next: ScheduledRow[] = [];
    for (const entry of schedules) {
      const prev = existing.get(entry.workflowId);
      let nextFire = '';
      try {
        nextFire = await props.client.scheduleNextFire(entry.workflowId);
      } catch {
        // Non-fatal — leave empty
      }
      const row: ScheduledRow = {
        entry,
        nextFire,
        runs: prev?.runs ?? [],
        expanded: prev?.expanded ?? false,
        loadingRuns: false,
        loadError: null,
      };
      next.push(row);
      if (row.expanded) {
        void fetchRuns(row);
      }
    }
    rows.value = next;
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

async function fetchRuns(row: ScheduledRow) {
  row.loadingRuns = true;
  row.loadError = null;
  try {
    row.runs = await props.client.scheduleRunHistory(row.entry.workflowId, 20);
  } catch (err) {
    row.loadError = err instanceof Error ? err.message : String(err);
  } finally {
    row.loadingRuns = false;
  }
}

async function toggleExpand(row: ScheduledRow) {
  row.expanded = !row.expanded;
  if (row.expanded && row.runs.length === 0) {
    await fetchRuns(row);
  }
}

// ── per-run actions ───────────────────────────────────────────────────────

const actionError = ref<string | null>(null);

async function rerunWorkflow(workflowId: string) {
  actionError.value = null;
  try {
    await props.client.runNow(workflowId);
    // Refresh to pick up the new running entry
    await loadInbox();
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : String(err);
  }
}

async function cancelRun(runId: string) {
  actionError.value = null;
  try {
    await props.client.cancelRun(runId);
    await loadInbox();
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : String(err);
  }
}

// ── auto-refresh ──────────────────────────────────────────────────────────

let refreshTimer: ReturnType<typeof setInterval> | undefined;

onMounted(() => {
  void loadInbox();
  refreshTimer = setInterval(() => {
    void loadInbox();
  }, 30_000);
});

onUnmounted(() => {
  if (refreshTimer !== undefined) {
    clearInterval(refreshTimer);
  }
});
</script>

<template>
  <div data-testid="scheduled-inbox">
    <!-- Loading skeleton -->
    <div
      v-if="loading && !hasRows"
      class="font-ui text-sm text-ink-muted py-4"
      data-testid="scheduled-inbox-loading"
    >
      Loading scheduled runs…
    </div>

    <!-- Top-level load error -->
    <div
      v-else-if="loadError"
      class="rounded-sm border border-red-700 bg-red-950 p-4 font-ui text-sm text-red-200"
      data-testid="scheduled-inbox-error"
    >
      {{ loadError }}
    </div>

    <!-- Empty state -->
    <div
      v-else-if="!hasRows"
      class="rounded-sm border border-border-muted bg-surface-1 p-6 max-w-2xl"
      data-testid="scheduled-inbox-empty"
    >
      <p class="font-ui text-sm text-ink mb-1">No scheduled workflows.</p>
      <p class="font-ui text-sm text-ink-muted">
        Open Settings → Workflows to add a cron schedule to any workflow.
      </p>
    </div>

    <!-- Per-workflow accordion rows -->
    <div v-else class="space-y-2" data-testid="scheduled-inbox-list">
      <!-- Action error banner (re-run / cancel) -->
      <div
        v-if="actionError"
        class="rounded-sm border border-red-700 bg-red-950 px-3 py-2 font-ui text-sm text-red-200"
        data-testid="scheduled-inbox-action-error"
      >
        {{ actionError }}
      </div>

      <div
        v-for="row in rows"
        :key="row.entry.workflowId"
        class="rounded-sm border border-border-muted bg-surface-1"
        :data-testid="`scheduled-row-${row.entry.workflowId}`"
      >
        <!-- Accordion header -->
        <div
          class="flex items-center justify-between px-4 py-3 cursor-pointer select-none"
          :data-testid="`scheduled-row-header-${row.entry.workflowId}`"
          @click="toggleExpand(row)"
        >
          <div class="space-y-0.5">
            <span class="font-ui text-sm font-medium text-ink">
              {{ row.entry.workflowId }}
            </span>
            <div class="flex items-center gap-3">
              <span class="font-mono text-xs text-ink-muted">
                {{ row.entry.cron }}
                <span v-if="row.entry.timezone" class="ml-1">{{ row.entry.timezone }}</span>
              </span>
              <span
                v-if="row.nextFire"
                class="font-ui text-xs text-ink-muted"
                :data-testid="`scheduled-next-fire-${row.entry.workflowId}`"
              >
                Next: {{ fmtTime(row.nextFire) }}
              </span>
              <span
                v-if="!row.entry.enabled"
                class="rounded px-1.5 py-0.5 font-ui text-xs bg-surface-2 text-ink-muted"
              >
                disabled
              </span>
            </div>
          </div>

          <div class="flex items-center gap-2">
            <!-- Re-run now button -->
            <button
              type="button"
              class="rounded-sm border border-border-muted bg-surface-2 px-2.5 py-1 font-ui text-xs text-ink hover:bg-surface-1"
              :data-testid="`scheduled-rerun-${row.entry.workflowId}`"
              @click.stop="rerunWorkflow(row.entry.workflowId)"
            >
              Run now
            </button>

            <!-- Expand/collapse chevron -->
            <span
              class="font-ui text-xs text-ink-muted transition-transform"
              :class="row.expanded ? 'rotate-180' : ''"
              :data-testid="`scheduled-chevron-${row.entry.workflowId}`"
            >
              ▾
            </span>
          </div>
        </div>

        <!-- Run history panel (expanded) -->
        <div
          v-if="row.expanded"
          class="border-t border-border-muted px-4 py-3"
          :data-testid="`scheduled-runs-${row.entry.workflowId}`"
        >
          <div
            v-if="row.loadingRuns"
            class="font-ui text-xs text-ink-muted py-2"
            :data-testid="`scheduled-runs-loading-${row.entry.workflowId}`"
          >
            Loading runs…
          </div>

          <div
            v-else-if="row.loadError"
            class="font-ui text-xs text-red-300 py-2"
            :data-testid="`scheduled-runs-error-${row.entry.workflowId}`"
          >
            {{ row.loadError }}
          </div>

          <div
            v-else-if="row.runs.length === 0"
            class="font-ui text-xs text-ink-muted py-2"
            :data-testid="`scheduled-runs-empty-${row.entry.workflowId}`"
          >
            No runs yet.
          </div>

          <ol v-else class="space-y-1">
            <li
              v-for="run in row.runs"
              :key="run.runId"
              class="flex items-center justify-between rounded-sm px-2 py-1.5 bg-surface-2"
              :data-testid="`scheduled-run-row-${run.runId}`"
            >
              <div class="space-y-0.5 min-w-0">
                <div class="flex items-center gap-2">
                  <span
                    class="font-ui text-xs"
                    :class="statusClass(run.status)"
                    :data-testid="`scheduled-run-status-${run.runId}`"
                  >
                    {{ run.status }}
                  </span>
                  <span class="font-mono text-xs text-ink-muted truncate">
                    {{ run.runId }}
                  </span>
                </div>
                <div class="font-ui text-xs text-ink-muted">
                  {{ fmtTime(run.startedAt) }}
                  <span v-if="run.endedAt" class="ml-1">
                    ({{ fmtDuration(run.startedAt, run.endedAt) }})
                  </span>
                </div>
                <div
                  v-if="run.error"
                  class="font-ui text-xs text-red-300 truncate"
                  :data-testid="`scheduled-run-error-${run.runId}`"
                >
                  {{ run.error }}
                </div>
              </div>

              <div class="flex items-center gap-1.5 shrink-0 ml-2">
                <!-- Re-run this specific workflow -->
                <button
                  type="button"
                  class="rounded-sm border border-border-muted bg-surface-1 px-2 py-0.5 font-ui text-xs text-ink hover:bg-surface-2"
                  :data-testid="`scheduled-run-rerun-${run.runId}`"
                  @click="rerunWorkflow(run.workflowId)"
                >
                  Re-run
                </button>

                <!-- Cancel only if still running -->
                <button
                  v-if="run.status === 'running'"
                  type="button"
                  class="rounded-sm border border-red-800 bg-red-950 px-2 py-0.5 font-ui text-xs text-red-300 hover:text-red-100"
                  :data-testid="`scheduled-run-cancel-${run.runId}`"
                  @click="cancelRun(run.runId)"
                >
                  Cancel
                </button>
              </div>
            </li>
          </ol>
        </div>
      </div>
    </div>
  </div>
</template>
