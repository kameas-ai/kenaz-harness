/**
 * workflowRunsStore.test — verifies the broker-event reducer that
 * powers the sidebar WorkflowRunsSection (mission workflows-01KQ8TDG,
 * WP10).
 *
 * The reducer is exported pure (`applyEvent`) so most tests don't
 * touch the live ref store — they just assert "given prior + event,
 * here's the next state". The bottom block exercises the live store +
 * subscription path with a fake window.runtime.
 */

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import {
  applyEvent,
  ingest,
  useWorkflowRunsStore,
  __resetWorkflowRunsStoreForTests,
  type RunProgressEvent,
  type RunState,
} from '@/lib/workflowRunsStore';

function evt(p: Partial<RunProgressEvent>): RunProgressEvent {
  return {
    runId: 'r1',
    workflowId: 'wf-plan',
    workflowName: 'Plan→Implement→Review',
    phase: 'run_started',
    ts: '2026-05-01T10:00:00.000Z',
    ...p,
  };
}

describe('workflowRunsStore.applyEvent (pure reducer)', () => {
  it('bootstraps a run on first event regardless of phase', () => {
    const next = applyEvent(
      new Map(),
      evt({ phase: 'step_started', stepName: 'plan', stepKind: 'model_turn' }),
    );
    const run = next.get('r1')!;
    expect(run.runId).toBe('r1');
    expect(run.workflowName).toBe('Plan→Implement→Review');
    expect(run.status).toBe('running');
    expect(run.steps).toHaveLength(1);
    expect(run.steps[0]).toMatchObject({
      name: 'plan',
      kind: 'model_turn',
      status: 'running',
    });
  });

  it('aggregates step transitions on the same step in place', () => {
    let s = new Map<string, RunState>();
    s = applyEvent(s, evt({ phase: 'run_started' }));
    s = applyEvent(
      s,
      evt({
        phase: 'step_started',
        stepName: 'plan',
        stepKind: 'model_turn',
        ts: '2026-05-01T10:00:01.000Z',
      }),
    );
    s = applyEvent(
      s,
      evt({
        phase: 'step_completed',
        stepName: 'plan',
        stepKind: 'model_turn',
        ts: '2026-05-01T10:00:05.000Z',
      }),
    );
    s = applyEvent(
      s,
      evt({
        phase: 'step_started',
        stepName: 'implement',
        stepKind: 'model_turn',
        ts: '2026-05-01T10:00:06.000Z',
      }),
    );
    const run = s.get('r1')!;
    expect(run.steps).toHaveLength(2);
    expect(run.steps[0]).toMatchObject({ name: 'plan', status: 'done' });
    expect(run.steps[1]).toMatchObject({ name: 'implement', status: 'running' });
    expect(run.steps[0].finishedAt).toBe('2026-05-01T10:00:05.000Z');
  });

  it('flips run to failed on step_failed and records failedStepName', () => {
    let s = new Map<string, RunState>();
    s = applyEvent(s, evt({ phase: 'run_started' }));
    s = applyEvent(
      s,
      evt({
        phase: 'step_started',
        stepName: 'review',
        stepKind: 'model_turn',
      }),
    );
    s = applyEvent(
      s,
      evt({
        phase: 'step_failed',
        stepName: 'review',
        stepKind: 'model_turn',
        error: 'cedar denied',
      }),
    );
    const run = s.get('r1')!;
    expect(run.status).toBe('failed');
    expect(run.failedStepName).toBe('review');
    expect(run.steps[0].status).toBe('failed');
    expect(run.steps[0].error).toBe('cedar denied');
  });

  it('honors run_completed → status=done', () => {
    let s = new Map<string, RunState>();
    s = applyEvent(s, evt({ phase: 'run_started' }));
    s = applyEvent(s, evt({ phase: 'run_completed' }));
    expect(s.get('r1')!.status).toBe('done');
  });

  it('honors run_failed and surfaces stepName', () => {
    let s = new Map<string, RunState>();
    s = applyEvent(s, evt({ phase: 'run_started' }));
    s = applyEvent(
      s,
      evt({ phase: 'run_failed', stepName: 'implement', error: 'boom' }),
    );
    const run = s.get('r1')!;
    expect(run.status).toBe('failed');
    expect(run.failedStepName).toBe('implement');
  });

  it('keeps multiple concurrent runs isolated', () => {
    let s = new Map<string, RunState>();
    s = applyEvent(s, evt({ runId: 'r1', phase: 'run_started' }));
    s = applyEvent(
      s,
      evt({
        runId: 'r2',
        workflowId: 'wf-other',
        workflowName: 'Other',
        phase: 'run_started',
      }),
    );
    s = applyEvent(s, evt({ runId: 'r2', phase: 'run_completed' }));
    expect(s.get('r1')!.status).toBe('running');
    expect(s.get('r2')!.status).toBe('done');
    expect(s.size).toBe(2);
  });

  it('ignores events without a runId', () => {
    const prior = new Map<string, RunState>();
    const next = applyEvent(
      prior,
      // @ts-expect-error — simulate a malformed broker payload
      { workflowId: 'wf', phase: 'run_started' },
    );
    expect(next).toBe(prior);
  });

  it('skipped step is preserved', () => {
    let s = new Map<string, RunState>();
    s = applyEvent(s, evt({ phase: 'run_started' }));
    s = applyEvent(
      s,
      evt({ phase: 'step_skipped', stepName: 'review', stepKind: 'tool_call' }),
    );
    expect(s.get('r1')!.steps[0].status).toBe('skipped');
  });
});

/**
 * Tests that exercise the REAL Go-emitted FrontendProgressEvent shape.
 *
 * The Go engine publishes FrontendProgressEvent values with:
 *   { runId, workflowId, phase, stepName, stepKind, error, ts }
 * NOT the old flat ProgressEvent shape
 *   { runId, workflowId, step, kind, status, at }
 *
 * These tests seed the store with events that match the actual Go
 * publish shape to confirm FR-005 (no silent-swallow / stuck sidebar).
 */
describe('workflowRunsStore — real Go-emitted FrontendProgressEvent shape', () => {
  beforeEach(() => {
    __resetWorkflowRunsStoreForTests();
  });
  afterEach(() => {
    __resetWorkflowRunsStoreForTests();
  });

  it('ingests a complete run lifecycle emitted by the Go engine', () => {
    // Simulate what Go actually publishes after the FR-005 fix.
    // The engine emits step events, then impl.go emits run_completed.
    const goEvents: RunProgressEvent[] = [
      {
        runId: 'run-go-01',
        workflowId: 'plan_implement_review',
        phase: 'step_started',
        stepName: 'plan',
        stepKind: 'model_turn',
        ts: '2026-07-07T10:00:00.000Z',
      },
      {
        runId: 'run-go-01',
        workflowId: 'plan_implement_review',
        phase: 'step_completed',
        stepName: 'plan',
        stepKind: 'model_turn',
        ts: '2026-07-07T10:00:05.000Z',
      },
      {
        runId: 'run-go-01',
        workflowId: 'plan_implement_review',
        phase: 'step_started',
        stepName: 'implement',
        stepKind: 'model_turn',
        ts: '2026-07-07T10:00:06.000Z',
      },
      {
        runId: 'run-go-01',
        workflowId: 'plan_implement_review',
        phase: 'step_completed',
        stepName: 'implement',
        stepKind: 'model_turn',
        ts: '2026-07-07T10:00:20.000Z',
      },
      // run_completed lifecycle event emitted by impl.go after engine.Run
      {
        runId: 'run-go-01',
        workflowId: 'plan_implement_review',
        phase: 'run_completed',
        ts: '2026-07-07T10:00:20.001Z',
      },
    ];

    for (const e of goEvents) {
      ingest(e);
    }

    const store = useWorkflowRunsStore();
    const run = store.getRun('run-go-01');
    expect(run).toBeDefined();
    // Run must be terminal — not stuck on "running".
    expect(run!.status).toBe('done');
    expect(run!.steps).toHaveLength(2);
    expect(run!.steps[0]).toMatchObject({ name: 'plan', kind: 'model_turn', status: 'done' });
    expect(run!.steps[1]).toMatchObject({ name: 'implement', kind: 'model_turn', status: 'done' });
  });

  it('sidebar flips to failed when run_failed lifecycle event arrives', () => {
    ingest({
      runId: 'run-go-fail',
      workflowId: 'daily_ea_briefing',
      phase: 'step_started',
      stepName: 'fetch_slack',
      stepKind: 'mcp_call',
      ts: '2026-07-07T11:00:00.000Z',
    });
    ingest({
      runId: 'run-go-fail',
      workflowId: 'daily_ea_briefing',
      phase: 'step_failed',
      stepName: 'fetch_slack',
      stepKind: 'mcp_call',
      error: 'MCP server "slack" is not installed — install it from Tools',
      ts: '2026-07-07T11:00:01.000Z',
    });
    // run_failed emitted by impl.go after engine.Run returns with an error
    ingest({
      runId: 'run-go-fail',
      workflowId: 'daily_ea_briefing',
      phase: 'run_failed',
      error: 'step fetch_slack failed',
      ts: '2026-07-07T11:00:01.001Z',
    });

    const store = useWorkflowRunsStore();
    const run = store.getRun('run-go-fail');
    expect(run).toBeDefined();
    // Must not be stuck on "running".
    expect(run!.status).toBe('failed');
    expect(run!.steps[0].status).toBe('failed');
    expect(run!.steps[0].error).toContain('slack');
  });

  it('rejects the OLD flat Go ProgressEvent shape (regression guard)', () => {
    // Before the FR-005 fix, Go emitted { step, kind, status, at }.
    // The reducer switches on evt.phase — if phase is missing, the
    // switch falls through and the run stays "running" forever.
    // @ts-expect-error — simulate old-shape event hitting the live store
    ingest({ runId: 'run-old', workflowId: 'wf', step: 'plan', kind: 'model_turn', status: 'completed', at: '2026-07-07T10:00:00Z' });
    // @ts-expect-error — simulate old-shape run lifecycle event
    ingest({ runId: 'run-old', workflowId: 'wf', step: '', kind: '', status: 'completed', at: '2026-07-07T10:00:01Z' });

    const store = useWorkflowRunsStore();
    const run = store.getRun('run-old');
    // The old shape bootstraps a run (runId is present) but the switch
    // falls through so status stays 'running' — demonstrating the bug
    // the FR-005 fix addresses.
    if (run) {
      // If a run exists, it must not be marked done (the old shape can't drive it to terminal).
      expect(run.status).toBe('running');
    }
    // The new shape (with phase) would have driven it to 'done'.
    // This documents the before/after behaviour for reviewers.
  });
});

describe('workflowRunsStore (live ref + ordering)', () => {
  beforeEach(() => {
    __resetWorkflowRunsStoreForTests();
  });
  afterEach(() => {
    __resetWorkflowRunsStoreForTests();
  });

  it('exposes runs sorted recent-first', () => {
    ingest(
      evt({
        runId: 'old',
        ts: '2026-05-01T09:00:00.000Z',
        phase: 'run_started',
      }),
    );
    ingest(
      evt({
        runId: 'mid',
        ts: '2026-05-01T10:00:00.000Z',
        phase: 'run_started',
      }),
    );
    ingest(
      evt({
        runId: 'new',
        ts: '2026-05-01T11:00:00.000Z',
        phase: 'run_started',
      }),
    );
    const store = useWorkflowRunsStore();
    expect(store.runs.value.map((r) => r.runId)).toEqual([
      'new',
      'mid',
      'old',
    ]);
    expect(store.getRun('mid')?.workflowId).toBe('wf-plan');
    expect(store.getRun('does-not-exist')).toBeUndefined();
  });

  it('subscribes to the broker topic when window.runtime exists', () => {
    const handlers = new Map<string, Set<(p: unknown) => void>>();
    (window as unknown as { runtime: unknown }).runtime = {
      EventsOn: (topic: string, cb: (p: unknown) => void) => {
        let s = handlers.get(topic);
        if (!s) {
          s = new Set();
          handlers.set(topic, s);
        }
        s.add(cb);
        return () => s!.delete(cb);
      },
    };
    try {
      // First call wires the subscription.
      const store = useWorkflowRunsStore();
      expect(handlers.get('workflows:run-progress')?.size).toBe(1);
      const cbs = Array.from(handlers.get('workflows:run-progress')!);
      cbs[0](evt({ runId: 'rx', phase: 'run_completed' }));
      expect(store.getRun('rx')?.status).toBe('done');
    } finally {
      delete (window as unknown as { runtime?: unknown }).runtime;
    }
  });
});
