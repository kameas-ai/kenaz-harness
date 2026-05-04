/**
 * workflowRunsStore — module-singleton state for the sidebar's
 * "Workflow runs" section (mission workflows-01KQ8TDG, WP10).
 *
 * v0.3.0-beta wiring: subscribes to the broker topic
 * `workflows:run-progress` and folds each event into a Map<runId, RunState>.
 * The engine emits one event per step transition (started/completed/failed)
 * plus a final run-level event; callers don't have to care about ordering
 * because the reducer is idempotent — later events for a step overwrite
 * earlier ones.
 *
 * Why a module singleton (and not Pinia)?
 *   - The harness frontend has no Pinia dependency yet; pulling one in
 *     for a single sidebar widget is overkill.
 *   - Vue refs work just fine as a process-wide store when wrapped in a
 *     module-scoped instance + a `useWorkflowRunsStore()` composable.
 *
 * Tests reset the singleton via `__resetWorkflowRunsStoreForTests()`.
 */

import { computed, ref, type ComputedRef } from 'vue';

/* ── public payload + state shapes ─────────────────────────────────── */

/** RunStatus mirrors the engine's run.status enum (run_completed event). */
export type RunStatus = 'running' | 'done' | 'failed';

/** StepStatus mirrors the engine's step transition enum. */
export type StepStatus =
  | 'pending'
  | 'running'
  | 'done'
  | 'failed'
  | 'skipped';

/**
 * StepProgress is a single step's last-seen state. We keep the most
 * recent status, error (if any), and started/finished-at timestamps so
 * the inline transcript can show timing.
 */
export interface StepProgress {
  name: string;
  kind: string;
  status: StepStatus;
  error?: string;
  startedAt?: string;
  finishedAt?: string;
}

/** RunState is the per-run aggregate the sidebar consumes. */
export interface RunState {
  runId: string;
  workflowId: string;
  workflowName: string;
  startedAt: string; // ISO; first event we saw for this run
  updatedAt: string; // ISO; most recent event we folded in
  status: RunStatus;
  /** When status === 'failed', the step that failed (first one we saw). */
  failedStepName?: string;
  steps: StepProgress[];
}

/**
 * RunProgressEvent is the broker payload contract. The Go side emits
 * a discriminated union via the `phase` field; this type encodes every
 * phase the v0.3.0 engine actually publishes.
 *
 * Optional fields (`stepName`, `stepKind`, `error`) are populated only
 * for the phases that need them — the reducer guards against missing
 * values defensively so a malformed event never crashes the sidebar.
 */
export interface RunProgressEvent {
  runId: string;
  workflowId: string;
  workflowName?: string;
  phase:
    | 'run_started'
    | 'step_started'
    | 'step_completed'
    | 'step_failed'
    | 'step_skipped'
    | 'run_completed'
    | 'run_failed';
  stepName?: string;
  stepKind?: string;
  error?: string;
  /** ISO timestamp; if omitted the reducer fills in `new Date().toISOString()`. */
  ts?: string;
}

/* ── store internals ──────────────────────────────────────────────── */

/** Cap stored runs so a long-lived session doesn't grow unbounded. */
const MAX_RUNS = 50;

const runsMap = ref<Map<string, RunState>>(new Map());

/* ── reducer (pure; exported for tests) ───────────────────────────── */

/**
 * applyEvent folds one progress event into the prior state and returns
 * the next state. Pure — the live store wraps this with a ref write.
 */
export function applyEvent(
  prior: Map<string, RunState>,
  evt: RunProgressEvent,
): Map<string, RunState> {
  if (!evt || !evt.runId) return prior;
  const next = new Map(prior);
  const ts = evt.ts ?? new Date().toISOString();
  const existing = next.get(evt.runId);

  // Bootstrap on the first event we ever see for this run, regardless
  // of phase — events can arrive out of order on bridge reconnect.
  const base: RunState = existing ?? {
    runId: evt.runId,
    workflowId: evt.workflowId,
    workflowName: evt.workflowName ?? evt.workflowId,
    startedAt: ts,
    updatedAt: ts,
    status: 'running',
    steps: [],
  };

  // Always refresh updatedAt + workflow metadata when a fresher payload
  // arrives (some phases include workflowName, earlier ones may not).
  const draft: RunState = {
    ...base,
    workflowId: evt.workflowId || base.workflowId,
    workflowName: evt.workflowName ?? base.workflowName,
    updatedAt: ts,
    steps: base.steps.slice(),
  };

  switch (evt.phase) {
    case 'run_started':
      draft.status = 'running';
      // run_started carries the canonical startedAt; trust it over the
      // bootstrap fallback if it's older (engine clock vs. UI clock).
      if (ts < draft.startedAt) draft.startedAt = ts;
      break;
    case 'run_completed':
      draft.status = 'done';
      break;
    case 'run_failed':
      draft.status = 'failed';
      if (evt.stepName && !draft.failedStepName) {
        draft.failedStepName = evt.stepName;
      }
      break;
    case 'step_started':
    case 'step_completed':
    case 'step_failed':
    case 'step_skipped': {
      if (!evt.stepName) break;
      const stepStatus: StepStatus =
        evt.phase === 'step_started'
          ? 'running'
          : evt.phase === 'step_completed'
            ? 'done'
            : evt.phase === 'step_failed'
              ? 'failed'
              : 'skipped';
      const idx = draft.steps.findIndex((s) => s.name === evt.stepName);
      const prev: StepProgress | undefined =
        idx >= 0 ? draft.steps[idx] : undefined;
      const merged: StepProgress = {
        name: evt.stepName,
        kind: evt.stepKind ?? prev?.kind ?? '',
        status: stepStatus,
        error: evt.phase === 'step_failed' ? evt.error : prev?.error,
        startedAt:
          evt.phase === 'step_started' ? ts : prev?.startedAt,
        finishedAt:
          evt.phase === 'step_started' ? prev?.finishedAt : ts,
      };
      if (idx >= 0) {
        draft.steps[idx] = merged;
      } else {
        draft.steps.push(merged);
      }
      // Bubble step_failed into the run-level failedStepName so the
      // sidebar row can surface it even if run_failed never lands
      // (e.g. crash before final event).
      if (evt.phase === 'step_failed' && !draft.failedStepName) {
        draft.failedStepName = evt.stepName;
        if (draft.status === 'running') draft.status = 'failed';
      }
      break;
    }
  }

  next.set(evt.runId, draft);

  // Cap by trimming oldest startedAt entries first.
  if (next.size > MAX_RUNS) {
    const sorted = Array.from(next.entries()).sort(
      (a, b) => a[1].startedAt.localeCompare(b[1].startedAt),
    );
    const drop = sorted.length - MAX_RUNS;
    for (let i = 0; i < drop; i++) {
      next.delete(sorted[i][0]);
    }
  }
  return next;
}

/* ── broker subscription (lazy, idempotent) ───────────────────────── */

const TOPIC = 'workflows:run-progress';
let unsubscribe: (() => void) | null = null;

interface RuntimeShape {
  EventsOn?: (
    topic: string,
    cb: (payload: unknown) => void,
  ) => () => void;
}

function ensureSubscription(): void {
  if (unsubscribe) return;
  if (typeof window === 'undefined') return;
  const rt = (window as unknown as { runtime?: RuntimeShape }).runtime;
  if (!rt?.EventsOn) return;
  unsubscribe = rt.EventsOn(TOPIC, (payload) => {
    ingest(payload as RunProgressEvent);
  });
}

/* ── public API ───────────────────────────────────────────────────── */

export interface WorkflowRunsStore {
  /** Recent-first list; sort key is `startedAt` desc. */
  runs: ComputedRef<RunState[]>;
  /** Lookup a single run by id. */
  getRun(runId: string): RunState | undefined;
  /**
   * Manually ingest an event. Production code never calls this — the
   * broker subscription does. Tests + the WorkflowsView synchronous
   * Run path call it to seed the sidebar without a real bridge.
   */
  ingest(evt: RunProgressEvent): void;
}

export function ingest(evt: RunProgressEvent): void {
  runsMap.value = applyEvent(runsMap.value, evt);
}

const runsSorted = computed<RunState[]>(() =>
  Array.from(runsMap.value.values()).sort((a, b) =>
    b.startedAt.localeCompare(a.startedAt),
  ),
);

export function useWorkflowRunsStore(): WorkflowRunsStore {
  ensureSubscription();
  return {
    runs: runsSorted,
    getRun: (id) => runsMap.value.get(id),
    ingest,
  };
}

/* ── test helpers ─────────────────────────────────────────────────── */

/** Wipe state + tear down subscription. Tests call this in beforeEach. */
export function __resetWorkflowRunsStoreForTests(): void {
  runsMap.value = new Map();
  if (unsubscribe) {
    unsubscribe();
    unsubscribe = null;
  }
}
