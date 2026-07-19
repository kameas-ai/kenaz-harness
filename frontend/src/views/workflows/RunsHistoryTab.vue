<script setup lang="ts">
/**
 * RunsHistoryTab — the Runs tab for WorkflowsView (mission 01NBUG04).
 *
 * FR-001  Lists actual executions (most-recent first): recipe name,
 *         run id, status (running/succeeded/failed), started-at.
 * FR-002  Live updates: subscribes to the workflowRunsStore broker
 *         topic so a run started from the Library tab appears and
 *         advances to its terminal status without manual refresh.
 * FR-003  The "scheduled workflows / chat runs" content lives under
 *         a clearly-labeled "Scheduled" sub-section below the history
 *         list so it is not conflated with execution history.
 * FR-004  Reuses the SAME data source (workflowRunsStore) as the
 *         bottom-left "Workflow Runs (BETA)" sidebar panel.
 * FR-005  Runs empty-state copy invites running a workflow from the
 *         Library tab rather than adding a cron schedule.
 * FR-006  Clicking a run opens the per-step breakdown (same inline
 *         transcript the Library tab shows after a run), including
 *         failure reason for failed steps.
 */
import { ref } from 'vue';
import ScheduledInbox from './ScheduledInbox.vue';
import {
  useWorkflowRunsStore,
  type RunState,
  type StepProgress,
} from '@/lib/workflowRunsStore';
import type { WorkflowsClient } from '@/lib/workflowsClient';
import type { ScheduledChatClient } from '@/lib/scheduledChatClient';

const props = defineProps<{
  /** Test seam: injected by WorkflowsView; tests can pass a fake. */
  client: WorkflowsClient;
  /** Optional chat client forwarded to the Scheduled subsection. */
  chatClient?: ScheduledChatClient;
}>();

const store = useWorkflowRunsStore();
const runs = store.runs;

const expandedRunId = ref<string | null>(null);

function toggleExpand(run: RunState) {
  expandedRunId.value =
    expandedRunId.value === run.runId ? null : run.runId;
}

// ── formatting helpers ─────────────────────────────────────────────────

function statusPillClass(status: RunState['status']): string {
  if (status === 'running') return 'bg-accent-glow text-accent';
  if (status === 'done')    return 'bg-surface-2 text-signal-ok';
  return 'bg-surface-2 text-signal-danger';
}

function statusPillLabel(status: RunState['status']): string {
  if (status === 'done') return 'succeeded';
  return status; // 'running' | 'failed'
}

function stepStatusClass(status: StepProgress['status']): string {
  if (status === 'running') return 'text-accent';
  if (status === 'done')    return 'text-signal-ok';
  if (status === 'failed')  return 'text-signal-danger';
  if (status === 'skipped') return 'text-ink-dim';
  return 'text-ink-muted';
}

function startedAgo(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const deltaMs = Date.now() - t;
  if (deltaMs < 0) return 'just now';
  const s = Math.floor(deltaMs / 1000);
  if (s < 5)  return 'just now';
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

function fmtAbsolute(iso: string): string {
  if (!iso) return '';
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}
</script>

<template>
  <div data-testid="runs-history-tab">

    <!-- ── Execution history section ─────────────────────────────── -->
    <section class="mb-8" data-testid="runs-history-section">
      <h3
        class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-3"
        data-testid="runs-history-heading"
      >
        Execution History
      </h3>

      <!-- Empty state (FR-005) -->
      <div
        v-if="runs.length === 0"
        class="rounded-sm border border-border-muted bg-surface-1 p-6 max-w-2xl"
        data-testid="runs-history-empty"
      >
        <p class="font-ui text-sm text-ink mb-1">No runs yet.</p>
        <p class="font-ui text-sm text-ink-muted">
          Go to the <strong>Library</strong> tab, pick a workflow, and press
          <em>Run workflow</em> — it will appear here live.
        </p>
      </div>

      <!-- Run list (FR-001, FR-002) -->
      <ol v-else class="space-y-2" data-testid="runs-history-list">
        <li
          v-for="run in runs"
          :key="run.runId"
          :data-testid="`runs-history-row-${run.runId}`"
        >
          <!-- Row button (FR-006: click to expand) -->
          <button
            type="button"
            class="w-full rounded-sm border border-border-muted bg-surface-1 px-4 py-3 text-left hover:bg-surface-2 transition-fast ease-kenaz focus:outline-none focus:ring-1 focus:ring-accent"
            :class="expandedRunId === run.runId ? 'ring-1 ring-accent-hairline' : ''"
            :data-testid="`runs-history-open-${run.runId}`"
            :aria-expanded="expandedRunId === run.runId"
            @click="toggleExpand(run)"
          >
            <div class="flex items-center justify-between gap-2">
              <!-- Recipe name + run id -->
              <div class="min-w-0">
                <span
                  class="font-ui text-sm font-medium text-ink truncate block"
                  :data-testid="`runs-history-name-${run.runId}`"
                >
                  {{ run.workflowName }}
                </span>
                <span
                  class="font-mono text-xs text-ink-dim truncate block mt-0.5"
                  :data-testid="`runs-history-runid-${run.runId}`"
                >
                  {{ run.runId }}
                </span>
              </div>

              <!-- Status pill + started-at (FR-001) -->
              <div class="flex items-center gap-3 shrink-0">
                <span
                  class="font-ui text-xs text-ink-dim"
                  :title="fmtAbsolute(run.startedAt)"
                  :data-testid="`runs-history-started-${run.runId}`"
                >
                  {{ startedAgo(run.startedAt) }}
                </span>
                <span
                  class="rounded-sm px-1.5 py-0.5 font-ui text-[11px] uppercase tracking-[0.14em]"
                  :class="statusPillClass(run.status)"
                  :data-testid="`runs-history-pill-${run.runId}`"
                >
                  {{ statusPillLabel(run.status) }}
                </span>
                <!-- Expand chevron -->
                <span
                  class="font-ui text-xs text-ink-muted transition-transform"
                  :class="expandedRunId === run.runId ? 'rotate-180' : ''"
                  aria-hidden="true"
                >
                  ▾
                </span>
              </div>
            </div>

            <!-- Inline failure reason hint on the row (FR-006) -->
            <div
              v-if="run.status === 'failed' && run.failedStepName"
              class="mt-1 font-ui text-xs text-signal-danger"
              :data-testid="`runs-history-failed-step-${run.runId}`"
            >
              Failed at step: {{ run.failedStepName }}
            </div>
          </button>

          <!-- Per-step breakdown (FR-006) — expanded inline -->
          <div
            v-if="expandedRunId === run.runId"
            class="border border-t-0 border-border-muted rounded-b-sm bg-surface-0 px-4 py-3"
            :data-testid="`runs-history-steps-${run.runId}`"
          >
            <div
              v-if="run.steps.length === 0"
              class="font-ui text-xs text-ink-muted"
              :data-testid="`runs-history-steps-empty-${run.runId}`"
            >
              No step detail available yet.
            </div>
            <ol v-else class="space-y-2">
              <li
                v-for="step in run.steps"
                :key="`${run.runId}:${step.name}`"
                class="rounded-sm border border-border-muted bg-surface-1 p-2"
                :data-testid="`runs-history-step-${run.runId}-${step.name}`"
              >
                <div class="flex items-baseline justify-between gap-2">
                  <span class="font-ui text-sm text-ink">
                    {{ step.name }}
                    <span class="font-ui text-xs text-ink-muted">({{ step.kind }})</span>
                  </span>
                  <span
                    class="font-ui text-xs uppercase tracking-[0.14em]"
                    :class="stepStatusClass(step.status)"
                    :data-testid="`runs-history-step-status-${run.runId}-${step.name}`"
                  >
                    {{ step.status }}
                  </span>
                </div>
                <!-- Failure reason for failed steps (FR-006) -->
                <p
                  v-if="step.error"
                  class="mt-1 font-ui text-xs text-signal-danger"
                  :data-testid="`runs-history-step-error-${run.runId}-${step.name}`"
                >
                  {{ step.error }}
                </p>
                <!-- Timing -->
                <div
                  v-if="step.startedAt"
                  class="mt-0.5 font-ui text-[10px] text-ink-dim"
                >
                  {{ startedAgo(step.startedAt) }}
                  <span v-if="step.finishedAt">
                    → {{ startedAgo(step.finishedAt) }}
                  </span>
                </div>
              </li>
            </ol>
          </div>
        </li>
      </ol>
    </section>

    <!-- ── Scheduled subsection (FR-003) ─────────────────────────── -->
    <section data-testid="runs-scheduled-section">
      <h3
        class="font-ui text-[11px] uppercase tracking-[0.18em] text-ink-subtle mb-3"
        data-testid="runs-scheduled-heading"
      >
        Scheduled
      </h3>
      <!-- ScheduledInbox owns the cron/chat-run accordions unchanged -->
      <ScheduledInbox
        :client="props.client"
        :chat-client="props.chatClient"
      />
    </section>
  </div>
</template>
