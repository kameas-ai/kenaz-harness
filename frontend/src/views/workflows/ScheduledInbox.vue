<script setup lang="ts">
/**
 * ScheduledInbox — scheduled-runs inbox (workflow-extensions-01KW2D3Y WP03).
 *
 * Replaces the "Run history coming soon." stub in WorkflowsView.vue.
 *
 * Shows one accordion row per scheduled workflow; each row lists up to 20
 * recent runs with status badges, duration, and per-run re-run / cancel
 * affordances.  Auto-refreshes every 30 seconds while mounted.
 *
 * scheduled-chat-runs-01KX5R8B WP06: extended with a "Chat Runs" accordion
 * section below workflow rows; requires the optional `chatClient` prop.
 */
import { ref, onMounted, onUnmounted, computed } from 'vue';
import type {
  WorkflowsClient,
  WorkflowsScheduleEntry,
  WorkflowsRunSummary,
} from '@/lib/workflowsClient';
import type {
  ScheduledChatClient,
  ScheduledChatEntry,
  ScheduledChatRunSummary,
} from '@/lib/scheduledChatClient';

const props = defineProps<{
  client: WorkflowsClient;
  /** Optional — when provided the Chat Runs section is rendered. */
  chatClient?: ScheduledChatClient;
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

// ── chat runs section (scheduled-chat-runs-01KX5R8B WP06) ────────────────

interface ChatRunRow {
  entry: ScheduledChatEntry;
  history: ScheduledChatRunSummary[];
  expanded: boolean;
  loadingHistory: boolean;
  loadError: string | null;
}

const chatRows = ref<ChatRunRow[]>([]);
const chatLoading = ref(false);
const chatLoadError = ref<string | null>(null);
const hasChatRows = computed(() => chatRows.value.length > 0);

async function loadChatInbox() {
  if (!props.chatClient) return;
  chatLoading.value = true;
  chatLoadError.value = null;
  try {
    const entries = await props.chatClient.list();
    const existing = new Map<string, ChatRunRow>(
      chatRows.value.map((r) => [r.entry.id, r]),
    );
    chatRows.value = entries.map((entry) => {
      const prev = existing.get(entry.id);
      return {
        entry,
        history: prev?.history ?? [],
        expanded: prev?.expanded ?? false,
        loadingHistory: false,
        loadError: null,
      };
    });
    // Re-fetch history for already-expanded rows
    for (const row of chatRows.value) {
      if (row.expanded) void fetchChatHistory(row);
    }
  } catch (err) {
    chatLoadError.value = err instanceof Error ? err.message : String(err);
  } finally {
    chatLoading.value = false;
  }
}

async function fetchChatHistory(row: ChatRunRow) {
  if (!props.chatClient) return;
  row.loadingHistory = true;
  row.loadError = null;
  try {
    row.history = await props.chatClient.history(row.entry.id, 20);
  } catch (err) {
    row.loadError = err instanceof Error ? err.message : String(err);
  } finally {
    row.loadingHistory = false;
  }
}

async function toggleChatExpand(row: ChatRunRow) {
  row.expanded = !row.expanded;
  if (row.expanded && row.history.length === 0) {
    await fetchChatHistory(row);
  }
}

async function chatRunNow(id: string) {
  if (!props.chatClient) return;
  actionError.value = null;
  try {
    await props.chatClient.runNow(id);
    await loadChatInbox();
  } catch (err) {
    actionError.value = err instanceof Error ? err.message : String(err);
  }
}

function chatHistoryStatusClass(status: string): string {
  if (status === 'completed') return 'text-emerald-400';
  if (status === 'running') return 'text-amber-400';
  if (status === 'failed') return 'text-red-400';
  return 'text-ink-muted';
}

// ── auto-refresh ──────────────────────────────────────────────────────────

let refreshTimer: ReturnType<typeof setInterval> | undefined;

onMounted(() => {
  void loadInbox();
  void loadChatInbox();
  refreshTimer = setInterval(() => {
    void loadInbox();
    void loadChatInbox();
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

    <!-- Action error banner (re-run / cancel — shared by workflow and chat rows) -->
    <div
      v-if="actionError"
      class="mt-3 rounded-sm border border-red-700 bg-red-950 px-3 py-2 font-ui text-sm text-red-200"
      data-testid="scheduled-inbox-action-error"
    >
      {{ actionError }}
    </div>

    <!-- scheduled-chat-runs-01KX5R8B WP06 — Chat Runs section -->
    <template v-if="chatClient">
      <div class="mt-6" data-testid="chat-runs-section">
        <h3
          class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-3"
          data-testid="chat-runs-heading"
        >
          Chat Runs
        </h3>

        <!-- Chat loading -->
        <div
          v-if="chatLoading && !hasChatRows"
          class="font-ui text-sm text-ink-muted py-2"
          data-testid="chat-runs-loading"
        >
          Loading chat runs…
        </div>

        <!-- Chat load error -->
        <div
          v-else-if="chatLoadError"
          class="rounded-sm border border-red-700 bg-red-950 p-4 font-ui text-sm text-red-200"
          data-testid="chat-runs-load-error"
        >
          {{ chatLoadError }}
        </div>

        <!-- Chat empty state -->
        <div
          v-else-if="!hasChatRows"
          class="rounded-sm border border-border-muted bg-surface-1 p-6 max-w-2xl"
          data-testid="chat-runs-empty"
        >
          <p class="font-ui text-sm text-ink mb-1">No scheduled chat runs.</p>
          <p class="font-ui text-sm text-ink-muted">
            Open Settings → Scheduled Chats to create a cron-fired prompt.
          </p>
        </div>

        <!-- Chat run accordion rows -->
        <div v-else class="space-y-2" data-testid="chat-runs-list">
          <div
            v-for="row in chatRows"
            :key="row.entry.id"
            class="rounded-sm border border-border-muted bg-surface-1"
            :data-testid="`chat-run-row-${row.entry.id}`"
          >
            <!-- Accordion header -->
            <div
              class="flex items-center justify-between px-4 py-3 cursor-pointer select-none"
              :data-testid="`chat-run-header-${row.entry.id}`"
              @click="toggleChatExpand(row)"
            >
              <div class="space-y-0.5">
                <div class="flex items-center gap-2">
                  <span class="font-ui text-sm font-medium text-ink">
                    {{ row.entry.name || row.entry.id }}
                  </span>
                  <span
                    v-if="!row.entry.enabled"
                    class="rounded px-1.5 py-0.5 font-ui text-xs bg-surface-2 text-ink-muted"
                  >
                    disabled
                  </span>
                </div>
                <div class="flex items-center gap-3">
                  <span class="font-mono text-xs text-ink-muted">{{ row.entry.cron }}</span>
                  <span v-if="row.entry.timezone" class="font-mono text-xs text-ink-muted">
                    {{ row.entry.timezone }}
                  </span>
                  <span class="font-ui text-xs text-ink-muted">→ {{ row.entry.outputSink }}</span>
                </div>
              </div>

              <div class="flex items-center gap-2">
                <button
                  type="button"
                  class="rounded-sm border border-border-muted bg-surface-2 px-2.5 py-1 font-ui text-xs text-ink hover:bg-surface-1"
                  :data-testid="`chat-run-now-${row.entry.id}`"
                  @click.stop="chatRunNow(row.entry.id)"
                >
                  Run now
                </button>
                <span
                  class="font-ui text-xs text-ink-muted transition-transform"
                  :class="row.expanded ? 'rotate-180' : ''"
                  :data-testid="`chat-run-chevron-${row.entry.id}`"
                >
                  ▾
                </span>
              </div>
            </div>

            <!-- History panel (expanded) -->
            <div
              v-if="row.expanded"
              class="border-t border-border-muted px-4 py-3"
              :data-testid="`chat-run-history-${row.entry.id}`"
            >
              <div
                v-if="row.loadingHistory"
                class="font-ui text-xs text-ink-muted py-2"
                :data-testid="`chat-run-history-loading-${row.entry.id}`"
              >
                Loading history…
              </div>

              <div
                v-else-if="row.loadError"
                class="font-ui text-xs text-red-300 py-2"
                :data-testid="`chat-run-history-error-${row.entry.id}`"
              >
                {{ row.loadError }}
              </div>

              <div
                v-else-if="row.history.length === 0"
                class="font-ui text-xs text-ink-muted py-2"
                :data-testid="`chat-run-history-empty-${row.entry.id}`"
              >
                No runs yet.
              </div>

              <ol v-else class="space-y-1">
                <li
                  v-for="hist in row.history"
                  :key="hist.id"
                  class="flex items-start justify-between rounded-sm px-2 py-1.5 bg-surface-2"
                  :data-testid="`chat-run-hist-row-${hist.id}`"
                >
                  <div class="space-y-0.5 min-w-0">
                    <div class="flex items-center gap-2">
                      <span
                        class="font-ui text-xs"
                        :class="chatHistoryStatusClass(hist.status)"
                        :data-testid="`chat-run-hist-status-${hist.id}`"
                      >
                        {{ hist.status }}
                      </span>
                      <span class="font-mono text-xs text-ink-muted truncate">
                        {{ hist.id }}
                      </span>
                    </div>
                    <div class="font-ui text-xs text-ink-muted">
                      {{ fmtTime(hist.startedAt) }}
                      <span v-if="hist.endedAt" class="ml-1">
                        ({{ fmtDuration(hist.startedAt, hist.endedAt) }})
                      </span>
                    </div>
                    <div
                      v-if="hist.error"
                      class="font-ui text-xs text-red-300 truncate"
                      :data-testid="`chat-run-hist-error-${hist.id}`"
                    >
                      {{ hist.error }}
                    </div>
                    <div
                      v-if="hist.outputSnippet"
                      class="font-ui text-xs text-ink-muted truncate"
                      :data-testid="`chat-run-hist-snippet-${hist.id}`"
                    >
                      {{ hist.outputSnippet }}
                    </div>
                  </div>
                </li>
              </ol>
            </div>
          </div>
        </div>
      </div>
    </template>
  </div>
</template>
