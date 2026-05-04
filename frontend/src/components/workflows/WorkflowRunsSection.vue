<script setup lang="ts">
/**
 * WorkflowRunsSection — sidebar widget that lists recent workflow runs
 * (mission workflows-01KQ8TDG, WP10).
 *
 * Renders a collapsible header "Workflow runs (N)" followed by one row
 * per run (recent first). Each row shows the workflow name, a relative
 * "started X ago" timestamp, and a status pill (running/done/failed).
 * Clicking a row toggles inline expansion that lists the per-step
 * transcript with each step's status. Failed runs surface the failed
 * step name on the row itself so the user doesn't have to expand.
 *
 * Beta scope: read-only view fed by the workflowRunsStore broker
 * subscription. No filtering, no search, no delete/clear. The "Beta"
 * pill in the header is the user-facing tell that this surface is
 * still moving.
 */
import { computed, ref } from 'vue';
import { useRouter } from 'vue-router';
import {
  ChevronDown,
  ChevronRight,
  GitBranch,
} from '@/shell/icons';
import {
  useWorkflowRunsStore,
  type RunState,
  type StepProgress,
} from '@/lib/workflowRunsStore';

const emit = defineEmits<{
  /** Fired when the user clicks a row; parent can route or focus. */
  (e: 'workflow-run:focus', runId: string): void;
}>();

const router = (() => {
  // Defensive: useRouter() throws when there's no router (rare in
  // tests). Catch and degrade to noop navigation.
  try {
    return useRouter();
  } catch {
    return null;
  }
})();

const store = useWorkflowRunsStore();
const runs = store.runs;

const collapsed = ref(false);
const expandedRunId = ref<string | null>(null);

const runCount = computed(() => runs.value.length);
const isHidden = computed(() => runCount.value === 0);

function toggleCollapsed() {
  collapsed.value = !collapsed.value;
}

function onRowClick(run: RunState) {
  expandedRunId.value =
    expandedRunId.value === run.runId ? null : run.runId;
  emit('workflow-run:focus', run.runId);
  // Best-effort deep-link to the workflows surface; the WorkflowsView
  // can pick up `?run=<id>` when WP09 wires the query param. Until
  // then this is just a hash-style hint and harmless if ignored.
  if (router) {
    void router.push({ path: '/workflows', query: { run: run.runId } });
  }
}

function statusPillClass(status: RunState['status']): string {
  // All three pill variants use existing tailwind tokens (no raw color
  // literals — Privacy CI invariant #4). The "background" is a faint
  // glow + matching foreground; we lean on accent/accent-glow for
  // running and signal-ok / signal-danger for terminal states.
  if (status === 'running') return 'bg-accent-glow text-accent';
  if (status === 'done') return 'bg-surface-2 text-signal-ok';
  return 'bg-surface-2 text-signal-danger';
}

function stepStatusClass(status: StepProgress['status']): string {
  if (status === 'running') return 'text-accent';
  if (status === 'done') return 'text-signal-ok';
  if (status === 'failed') return 'text-signal-danger';
  if (status === 'skipped') return 'text-ink-dim';
  return 'text-ink-muted';
}

/**
 * startedAgo renders a coarse "Xm ago" / "Xs ago" label. Sub-minute
 * precision keeps the sidebar honest during a live run; minute-grained
 * once a run is older than a minute is more than enough.
 */
function startedAgo(iso: string): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const deltaMs = Date.now() - t;
  if (deltaMs < 0) return 'just now';
  const s = Math.floor(deltaMs / 1000);
  if (s < 5) return 'just now';
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}
</script>

<template>
  <div
    v-if="!isHidden"
    class="px-2 mt-3"
    data-testid="workflow-runs-section"
  >
    <button
      type="button"
      class="w-full flex items-center gap-1 px-2 py-1 rounded-sm text-left font-ui text-[10px] uppercase tracking-[0.18em] text-ink-subtle hover:text-ink"
      :aria-expanded="!collapsed"
      data-testid="workflow-runs-header"
      @click="toggleCollapsed"
    >
      <component
        :is="collapsed ? ChevronRight : ChevronDown"
        :size="11"
        class="text-ink-dim"
      />
      <GitBranch :size="11" class="text-ink-dim" />
      <span class="flex-1 hidden two-col:inline">
        Workflow runs ({{ runCount }})
      </span>
      <span
        class="rounded-sm border border-accent-hairline px-1 text-[9px] tracking-[0.14em] text-accent hidden two-col:inline"
        title="v0.3.0 beta surface"
      >
        BETA
      </span>
    </button>

    <ul
      v-if="!collapsed"
      class="mt-1 space-y-1"
      data-testid="workflow-runs-list"
    >
      <li
        v-for="run in runs"
        :key="run.runId"
        :data-testid="`workflow-run-row-${run.runId}`"
      >
        <button
          type="button"
          class="w-full flex items-start gap-2 px-2 py-1.5 rounded-sm text-left font-ui text-xs text-ink-muted hover:text-ink hover:bg-surface-2 transition-fast ease-kenaz"
          :class="
            expandedRunId === run.runId
              ? 'bg-surface-2 text-ink ring-1 ring-accent-hairline'
              : ''
          "
          :title="`${run.workflowName} — ${run.status}`"
          :data-testid="`workflow-run-open-${run.runId}`"
          @click="onRowClick(run)"
        >
          <div class="flex-1 min-w-0">
            <div class="flex items-center gap-1.5">
              <span
                class="truncate hidden two-col:inline"
                :data-testid="`workflow-run-name-${run.runId}`"
              >
                {{ run.workflowName }}
              </span>
              <span
                class="ml-auto rounded-sm px-1 py-0.5 text-[9px] uppercase tracking-[0.14em]"
                :class="statusPillClass(run.status)"
                :data-testid="`workflow-run-pill-${run.runId}`"
              >
                {{ run.status }}
              </span>
            </div>
            <div
              class="text-[10px] text-ink-dim truncate hidden two-col:block"
            >
              {{ startedAgo(run.startedAt) }}
              <template v-if="run.status === 'failed' && run.failedStepName">
                ·
                <span
                    class="text-signal-danger"
                  :data-testid="`workflow-run-failed-step-${run.runId}`"
                >
                  failed at {{ run.failedStepName }}
                </span>
              </template>
            </div>
          </div>
        </button>

        <ol
          v-if="expandedRunId === run.runId && run.steps.length > 0"
          class="mt-1 ml-4 space-y-0.5 border-l border-border-muted pl-2"
          :data-testid="`workflow-run-steps-${run.runId}`"
        >
          <li
            v-for="step in run.steps"
            :key="`${run.runId}:${step.name}`"
            class="flex items-baseline gap-2 text-[11px] font-ui"
            :data-testid="`workflow-run-step-${run.runId}-${step.name}`"
          >
            <span class="truncate flex-1 text-ink-muted">
              {{ step.name }}
              <span class="text-ink-dim">({{ step.kind }})</span>
            </span>
            <span
              class="text-[10px] uppercase tracking-[0.14em]"
              :class="stepStatusClass(step.status)"
            >
              {{ step.status }}
            </span>
          </li>
        </ol>
      </li>
    </ul>
  </div>
</template>
