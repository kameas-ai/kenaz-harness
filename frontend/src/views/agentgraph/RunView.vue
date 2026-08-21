<script setup lang="ts">
/**
 * RunView — Bundle A WP06 surface for a single kernel run. Polls
 * Graph_GetRunStatus + Graph_GetRunTrace at 500 ms and renders:
 *
 *   - the lifecycle state pill
 *   - per-counter readouts (LLM tokens / calls / tools / cost)
 *   - the Airflow-style GRAPH of the run, per-node status, clickable
 *     through to the trace rows (visual-graph-authoring-01PMUX01 WP05)
 *   - the trace tail (last N events) with kind + node-id badges
 *   - paused-state UI: reads PendingAsk and exposes a resume input
 *
 * ── WHERE THE LIVE NODE STATES COME FROM ──────────────────────────────
 *
 * `Graph_GetRunStatus` carries NO node-level information — it is a
 * counter snapshot (`nodesComplete`, tokens, calls, cost) plus the
 * lifecycle state. So the overlay is NOT fed from the status poll, and
 * no new polling RPC was invented for it either.
 *
 * It is fed from `Graph_MaterializeRun`, which projects the run's
 * EventLog into a graph and is explicitly documented as working on "a
 * finished (or in-flight) run" (core/rpc/views/agentgraph/manager.go).
 * Re-projecting an in-flight run yields the topology so far with each
 * node's status derived from the very same event stream the trace list
 * below is rendering — one source, so the graph and the trace can never
 * disagree about what happened.
 *
 * The projection is refreshed when the trace poll actually returns new
 * events, not on a timer of its own: no new events means nothing in the
 * projection can have changed.
 *
 * The one translation is `incomplete` → `running`: a fire whose log has
 * no matching complete is a crash on a finished run and an
 * executing node on a live one. `materializationStatuses(…, {live})`
 * owns that mapping.
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import CanvasHead from '@/shell/CanvasHead.vue';
import GraphCanvas from '@/components/canvas/GraphCanvas.vue';
import { useHarnessClient } from '@/lib/useHarnessAPI';
import { useServedMode } from '@/lib/useServedMode';
import NotAvailableInServedMode from '@/components/ui/NotAvailableInServedMode.vue';
import { useManifestStore } from '@/composables/useNodeManifest';
import {
  buildGraphAdapter,
  materializationStatuses,
  SPEC_PROVENANCE_LIBRARY_FALLBACK,
} from '@/lib/canvas/graphAdapter';
import { parseGraphText, type ParsedGraph } from '@/lib/canvas/graphSpec';
import type { GraphRunStatus, GraphRunTraceEvent } from '@/lib/types';

const client = useHarnessClient();
const route = useRoute();
const router = useRouter();

// served-mode-is-a-real-mode-01PMZ707 WP03, spec.md §2/§5.3 (D-701).
const servedMode = useServedMode();

const runId = computed(() => String(route.params.runId ?? ''));

const status = ref<GraphRunStatus | null>(null);
const events = ref<readonly GraphRunTraceEvent[]>([]);
const error = ref<string | null>(null);
const askResponse = ref('');
const submittingAsk = ref(false);

let pollHandle: ReturnType<typeof setTimeout> | null = null;
let cancelled = false;

const POLL_MS = 500;

async function pollOnce() {
  const id = runId.value;
  if (!id) return;
  try {
    const st = await client.graph.getRunStatus(id);
    status.value = st;
    const since = events.value.length > 0 ? events.value[events.value.length - 1].seq : 0;
    const tail = await client.graph.getRunTrace(id, since);
    if (tail.length > 0) {
      events.value = [...events.value, ...tail];
      // New events ⇒ the projection can have changed. No new events ⇒
      // it provably cannot, so the graph is left alone.
      //
      // COALESCING: this is awaited, and `schedulePoll` only arms the next
      // timeout after `pollOnce` resolves, so a slow projection delays the
      // next poll rather than overlapping with itself. That is the
      // intended trade — a busy run re-projects at whatever rate the
      // projection can sustain instead of queueing work it cannot finish,
      // and the trace tail catches up in one larger batch on the next tick
      // (`since` is the last seq seen, so nothing is skipped).
      //
      // It also means the 500 ms POLL_MS is a floor, not a period. If that
      // ever needs to become a true period, the fix is to drop a projection
      // that is already in flight — not to fire this without awaiting,
      // which would let two refreshes race and settle in arrival order.
      await refreshGraph();
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

// ── the run, as a graph (WP05) ───────────────────────────────────────

const runGraph = ref<ParsedGraph | null>(null);
/**
 * Why the graph gets its own error ref instead of sharing `error`: a run
 * whose resolved spec was evicted cannot be projected at all, and that
 * must not blank the trace list — the trace is the surface that still
 * works. The graph pane says why it is empty; everything else carries on.
 */
const graphError = ref<string | null>(null);

const manifestStore = useManifestStore();

/** True while the run can still produce events. */
const live = computed(
  () => status.value?.state === 'running' || status.value?.state === 'paused',
);

async function refreshGraph() {
  const id = runId.value;
  if (!id) return;
  try {
    const spec = await client.graph.materializeRun(id);
    const parsed = parseGraphText(spec.yaml);
    if (parsed.graph) {
      runGraph.value = parsed.graph;
      graphError.value = null;
    } else {
      graphError.value = parsed.error ?? 'The projected run does not parse.';
    }
  } catch (err) {
    graphError.value = err instanceof Error ? err.message : String(err);
  }
}

const runStatuses = computed(() =>
  materializationStatuses(runGraph.value, { live: live.value }),
);

/**
 * The run canvas is read-only, and structurally so: `buildGraphAdapter`
 * replaces `onSpecOp` with a no-op when `readOnly` is set, and
 * `GraphCanvas` registers no drop / connect / drag / delete handler at
 * all. A record of something that already happened has no edit path to
 * guard — which is what closes the WP12-review N3 hazard on this surface
 * rather than inheriting it.
 */
const runAdapter = computed(() =>
  buildGraphAdapter({
    graph: runGraph.value,
    manifests: manifestStore.manifests.value ?? [],
    readOnly: true,
    checkEdge: async () => ({ ok: false, reason: 'This run is read-only.' }),
    applyOp: () => undefined,
    statuses: runStatuses.value,
  }),
);

const isDegraded = computed(
  () => runGraph.value?.specProvenance === SPEC_PROVENANCE_LIBRARY_FALLBACK,
);
const canvasNotice = computed(() =>
  isDegraded.value
    ? 'Degraded projection — the resolved spec this run executed was evicted, so this topology may differ from the one that ran.'
    : '',
);

// ── click-through: node → its trace rows ─────────────────────────────

const selectedNodeId = ref('');
/** The trace row a node click jumped to; highlighted in the list. */
const focusedSeq = ref<number | null>(null);
/** Template ref on the trace `<ul>`, for the scroll-to. */
const traceList = ref<HTMLElement | null>(null);

function onCanvasSelect(id: string) {
  selectedNodeId.value = id;
}

/**
 * Jumps the trace list to the rows this node produced.
 *
 * `startSeq` is the materialization's join key back into the EventLog,
 * and the trace list below is keyed by that same `seq` — so the jump is
 * a lookup, not a heuristic. The one bit of slack: the polled tail may
 * not contain the exact seq (a run's very first rows can be pruned, and
 * some seqs belong to events the list does not render), so the target is
 * the FIRST row at or after `startSeq`, which is the row that opens the
 * node's span.
 */
function onNodeStatusClick(payload: { detail?: Record<string, unknown> }) {
  const raw = payload.detail?.startSeq;
  const startSeq = typeof raw === 'number' ? raw : Number(raw);
  if (!Number.isFinite(startSeq)) return;
  const target = events.value.find((ev) => ev.seq >= startSeq);
  if (!target) return;
  focusedSeq.value = target.seq;
  void scrollToFocused();
}

async function scrollToFocused() {
  await nextTick();
  const seq = focusedSeq.value;
  if (seq === null || !traceList.value) return;
  const row = traceList.value.querySelector(`[data-testid="trace-event-${seq}"]`);
  // happy-dom has no layout, so scrollIntoView is absent there. The
  // highlight is the part that is actually asserted; the scroll is a
  // convenience that degrades to nothing.
  if (row && typeof (row as HTMLElement).scrollIntoView === 'function') {
    (row as HTMLElement).scrollIntoView({ block: 'center' });
  }
}

function schedulePoll() {
  if (cancelled) return;
  pollHandle = setTimeout(async () => {
    await pollOnce();
    const st = status.value;
    if (st && (st.state === 'completed' || st.state === 'failed')) return;
    schedulePoll();
  }, POLL_MS);
}

async function resume() {
  const id = runId.value;
  if (!id) return;
  submittingAsk.value = true;
  try {
    await client.graph.resume(id, askResponse.value);
    askResponse.value = '';
    await pollOnce();
    schedulePoll();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  } finally {
    submittingAsk.value = false;
  }
}

async function cancelRun() {
  const id = runId.value;
  if (!id) return;
  try {
    await client.graph.cancelRun(id);
    await pollOnce();
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err);
  }
}

function backToList() {
  void router.push({ name: 'graphs' });
}

/**
 * openMaterialized navigates to the run rendered AS A GRAPH
 * (agentgraph-total-convergence-01PMGX01 WP12) — the same trace this
 * view lists row by row, projected into the node/edge shape an authored
 * graph has.
 */
function openMaterialized() {
  void router.push({ name: 'graph-materialized', params: { runId: runId.value } });
}

onMounted(async () => {
  // Served mode: the whole view renders NotAvailableInServedMode instead
  // (Graph_* has no serve dispatch case — D-701). Starting the poll loop
  // anyway would just be a 500ms cadence of guaranteed-rejecting calls.
  if (servedMode.value) return;
  await pollOnce();
  // The first projection is unconditional: a run that finished before
  // this view opened returns its whole trace in one tail, and a run with
  // no events at all still has a topology worth drawing.
  await refreshGraph();
  schedulePoll();
});

onBeforeUnmount(() => {
  cancelled = true;
  if (pollHandle) clearTimeout(pollHandle);
});

const stateLabel = computed(() => {
  const st = status.value?.state;
  if (st === 'running') return 'Running';
  if (st === 'paused') return 'Paused';
  if (st === 'completed') return 'Completed';
  if (st === 'failed') return 'Failed';
  return 'Pending';
});

const stateClass = computed(() => {
  const st = status.value?.state;
  if (st === 'running') return 'border-accent text-accent';
  if (st === 'paused') return 'border-signal-warn text-signal-warn';
  if (st === 'completed') return 'border-signal-ok text-signal-ok';
  if (st === 'failed') return 'border-signal-danger text-signal-danger';
  return 'border-border-muted text-ink-dim';
});

defineExpose({ pollOnce, refreshGraph, onNodeStatusClick, focusedSeq });
</script>

<template>
  <NotAvailableInServedMode
    v-if="servedMode"
    feature="Graph run"
    reason="Run status and trace stream through Graph_* RPCs that are not routed in served mode — porting them would put graph execution behind the shared workbench token, which the served build's confinement model does not support yet."
  />
  <div v-else>
    <CanvasHead
      number="12"
      section="GRAPHS"
      title="Run"
      :subtitle="`Run ${runId.slice(0, 16)}…`"
    >
      <template #trailing>
        <div class="flex gap-2">
          <button
            type="button"
            class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            @click="backToList"
          >
            Back
          </button>
          <button
            type="button"
            class="rounded-sm border border-border-muted px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-ink-dim hover:bg-surface-2"
            data-testid="run-view-as-graph"
            @click="openMaterialized"
          >
            View as graph
          </button>
        </div>
      </template>
    </CanvasHead>

    <div class="px-6 py-4 max-w-5xl space-y-4">
      <div
        v-if="error"
        class="rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        {{ error }}
      </div>

      <div
        class="rounded-md border border-border-muted bg-surface-1 px-4 py-3"
        data-testid="run-status"
      >
        <div class="flex items-center gap-3">
          <span
            class="rounded-sm border px-2 py-0 font-ui text-[10px] uppercase tracking-[0.18em]"
            :class="stateClass"
            data-testid="run-state"
          >
            {{ stateLabel }}
          </span>
          <span class="font-ui text-[13px] text-ink">
            graph: <span class="font-semibold">{{ status?.graphId || '—' }}</span>
          </span>
          <span v-if="status?.error" class="font-ui text-[12px] text-signal-danger">
            error: {{ status.error }}
          </span>
        </div>
        <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1 font-ui text-[12px] text-ink-muted">
          <span data-testid="run-counter-nodes">nodes complete: {{ status?.nodesComplete ?? 0 }}</span>
          <span>llm tokens: {{ status?.llmTokens ?? 0 }}</span>
          <span>llm calls: {{ status?.llmCalls ?? 0 }}</span>
          <span>tool calls: {{ status?.toolCalls ?? 0 }}</span>
          <span>cost: ${{ (status?.costUsd ?? 0).toFixed(4) }}</span>
        </div>
        <div
          v-if="status?.state === 'running' || status?.state === 'paused'"
          class="mt-3"
        >
          <button
            type="button"
            class="rounded-sm border border-signal-danger px-2 py-1 font-ui text-[11px] uppercase tracking-[0.18em] text-signal-danger hover:bg-surface-2"
            data-testid="run-cancel"
            @click="cancelRun"
          >
            Cancel
          </button>
        </div>
      </div>

      <div
        v-if="status?.pendingAsk"
        class="rounded-md border border-signal-warn bg-surface-1 px-4 py-3"
        data-testid="run-pending-ask"
      >
        <div class="font-ui text-[12px] uppercase tracking-[0.18em] text-signal-warn">
          AskNode parked the run
        </div>
        <div class="mt-1 font-ui text-[14px] font-semibold text-ink">
          {{ status.pendingAsk.question }}
        </div>
        <div class="mt-2 flex items-end gap-2">
          <input
            v-model="askResponse"
            type="text"
            data-testid="run-ask-input"
            class="flex-1 rounded-sm border border-border-muted bg-surface-0 px-2 py-1 font-ui text-[13px] text-ink"
            placeholder="Type your answer…"
          />
          <button
            type="button"
            :disabled="submittingAsk"
            class="rounded-sm border border-accent bg-surface-2 px-3 py-1 font-ui text-[12px] uppercase tracking-[0.18em] text-accent disabled:opacity-50"
            data-testid="run-ask-submit"
            @click="resume"
          >
            Resume
          </button>
        </div>
      </div>

      <div class="rounded-md border border-border-muted bg-surface-1">
        <div
          class="flex items-center gap-2 border-b border-border-muted px-4 py-2 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-muted"
        >
          <span>Graph</span>
          <span
            v-if="live"
            class="normal-case tracking-normal text-accent"
            data-testid="run-graph-live"
            >live</span
          >
          <span class="ml-auto normal-case tracking-normal text-ink-muted">
            Click a node to jump to its trace rows
          </span>
        </div>
        <div class="p-2">
          <div
            v-if="graphError"
            class="rounded-sm border border-border-muted bg-surface-0 px-3 py-2 font-ui text-[12px] text-ink-muted"
            data-testid="run-graph-error"
          >
            This run cannot be drawn as a graph: {{ graphError }}
          </div>
          <GraphCanvas
            v-else
            :adapter="runAdapter"
            :selected-node-id="selectedNodeId"
            :notice="canvasNotice"
            @select-node="onCanvasSelect"
            @node-status-click="onNodeStatusClick"
          />
        </div>
      </div>

      <div class="rounded-md border border-border-muted bg-surface-1">
        <div
          class="border-b border-border-muted px-4 py-2 font-ui text-[11px] uppercase tracking-[0.18em] text-ink-dim"
        >
          Trace tail
        </div>
        <ul
          v-if="events.length > 0"
          ref="traceList"
          class="max-h-[320px] overflow-y-auto"
          data-testid="run-trace"
        >
          <li
            v-for="ev in events"
            :key="ev.seq"
            class="border-b border-border-muted px-4 py-2 font-ui text-[12px] text-ink-muted last:border-b-0"
            :class="ev.seq === focusedSeq ? 'bg-surface-2 ring-1 ring-accent' : ''"
            :data-testid="`trace-event-${ev.seq}`"
            :data-focused="ev.seq === focusedSeq ? 'true' : 'false'"
          >
            <span class="font-mono text-[10px] text-ink-dim">#{{ ev.seq }}</span>
            <span class="ml-2 rounded-sm border border-border-muted px-1 py-0 text-[10px] uppercase tracking-[0.18em] text-ink-dim">
              {{ ev.kind }}
            </span>
            <span v-if="ev.nodeId" class="ml-2 font-mono text-[11px] text-ink">
              {{ ev.nodeId }}
            </span>
          </li>
        </ul>
        <div
          v-else
          class="px-4 py-3 font-ui text-[12px] text-ink-muted"
          data-testid="run-trace-empty"
        >
          No events yet.
        </div>
      </div>
    </div>
  </div>
</template>
