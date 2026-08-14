import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { GraphRunStatus, GraphRunTraceEvent } from '@/lib/types';
import {
  MATERIALIZED_DEGRADED_YAML,
  MATERIALIZED_RUN_YAML,
} from '@/lib/canvas/__fixtures__/graphFixtures';

const pushMock = vi.fn();
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { runId: 'run-1' }, query: {} }),
  useRouter: () => ({ push: pushMock }),
}));

import RunView from '@/views/agentgraph/RunView.vue';

interface MountOpts {
  status?: GraphRunStatus;
  trace?: GraphRunTraceEvent[];
  resumeImpl?: (runId: string, ans: string) => Promise<void>;
  /** Materialized projection the run graph renders from (WP05). */
  materializedYAML?: string;
  materializeError?: string;
}

function defaultStatus(over: Partial<GraphRunStatus> = {}): GraphRunStatus {
  return {
    runId: 'run-1',
    graphId: 'g',
    state: 'completed',
    startedAt: '2026-04-26T00:00:00Z',
    updatedAt: '2026-04-26T00:00:01Z',
    nodesComplete: 1,
    llmTokens: 0,
    llmCalls: 0,
    toolCalls: 0,
    costUsd: 0,
    ...over,
  };
}

function mountWith(opts: MountOpts = {}) {
  const status = opts.status ?? defaultStatus();
  const trace = opts.trace ?? [];
  const getRunStatus = vi.fn(async () => status);
  const getRunTrace = vi.fn(async () => trace);
  const resume = vi.fn(opts.resumeImpl ?? (async () => undefined));
  const cancelRun = vi.fn(async () => undefined);
  const materializeRun = vi.fn(async (runID: string) => {
    if (opts.materializeError) throw new Error(opts.materializeError);
    return {
      id: `g__run_${runID}`,
      scope: 'materialized' as const,
      yaml: opts.materializedYAML ?? '',
    };
  });

  const client = createFakeHarnessClient({
    graph: {
      listGraphs: async () => [],
      loadGraph: async (id) => ({ id, scope: 'library' as const, yaml: '' }),
      saveGraph: async () => undefined,
      deleteGraph: async () => undefined,
      validate: async () => ({ ok: true, issues: [] }),
      checkEdge: async () => ({ ok: true }),
      startRun: async (req) => ({
        runId: 'run-1',
        status: {
          runId: 'run-1',
          graphId: req.graphId,
          state: 'running' as const,
          startedAt: '',
          updatedAt: '',
          nodesComplete: 0,
          llmTokens: 0,
          llmCalls: 0,
          toolCalls: 0,
          costUsd: 0,
        },
      }),
      getRunStatus,
      getRunTrace,
      resume,
      cancelRun,
      materializeRun,
    },
  });

  const wrapper = mount(RunView, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        CanvasHead: {
          template: '<div class="canvas-head"><slot name="trailing" /></div>',
        },
      },
    },
  });
  return { wrapper, getRunStatus, getRunTrace, resume, cancelRun, materializeRun };
}

describe('RunView', () => {
  it('polls status + trace on mount and renders the state pill', async () => {
    const { wrapper, getRunStatus, getRunTrace } = mountWith();
    await flushPromises();
    expect(getRunStatus).toHaveBeenCalled();
    expect(getRunTrace).toHaveBeenCalled();
    expect(wrapper.find('[data-testid="run-state"]').text()).toContain('Completed');
  });

  it('renders the empty-trace card when there are no events', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="run-trace-empty"]').exists()).toBe(true);
  });

  it('renders trace events with their kind + node-id', async () => {
    const { wrapper } = mountWith({
      trace: [
        { seq: 1, runId: 'run-1', kind: 'run_start', ts: '' },
        { seq: 2, runId: 'run-1', kind: 'node_start', nodeId: 'a', ts: '' },
        { seq: 3, runId: 'run-1', kind: 'run_complete', ts: '' },
      ],
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="trace-event-1"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="trace-event-2"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="trace-event-3"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="trace-event-2"]').text()).toContain('a');
  });

  it('renders the pending-ask block and resumes on submit', async () => {
    const { wrapper, resume } = mountWith({
      status: defaultStatus({
        state: 'paused',
        pendingAsk: { nodeId: 'q', question: 'What is your name?' },
      }),
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="run-pending-ask"]').exists()).toBe(true);
    expect(wrapper.html()).toContain('What is your name?');
    await wrapper.get('[data-testid="run-ask-input"]').setValue('Alice');
    await wrapper.get('[data-testid="run-ask-submit"]').trigger('click');
    await flushPromises();
    expect(resume).toHaveBeenCalledWith('run-1', 'Alice');
  });

  // agentgraph-total-convergence-01PMGX01 WP12: the run's trace and the
  // run's graph are two renderings of one recorded stream, so the trace
  // view is where the graph view is reached from.
  it('navigates to the materialized graph for the run', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    pushMock.mockClear();
    await wrapper.get('[data-testid="run-view-as-graph"]').trigger('click');
    expect(pushMock).toHaveBeenCalledWith({
      name: 'graph-materialized',
      params: { runId: 'run-1' },
    });
  });

  it('exposes a Cancel button while running', async () => {
    const { wrapper, cancelRun } = mountWith({
      status: defaultStatus({ state: 'running' }),
    });
    await flushPromises();
    await wrapper.get('[data-testid="run-cancel"]').trigger('click');
    await flushPromises();
    expect(cancelRun).toHaveBeenCalledWith('run-1');
  });
});

/*
 * The Airflow-style run overlay (visual-graph-authoring-01PMUX01 WP05,
 * FR-005). The run's graph and the run's trace are two renderings of ONE
 * recorded stream, and this view is where they sit side by side — so
 * this is where the click-through between them is pinned.
 */
describe('RunView — the run as a graph', () => {
  const TRACE: GraphRunTraceEvent[] = [
    { seq: 1, runId: 'run-1', kind: 'run_start', ts: '' },
    { seq: 2, runId: 'run-1', kind: 'node_start', nodeId: 'history_in', ts: '' },
    { seq: 4, runId: 'run-1', kind: 'node_start', nodeId: 'assistant_turn', ts: '' },
    { seq: 6, runId: 'run-1', kind: 'node_start', nodeId: 'tool_leg', ts: '' },
    { seq: 9, runId: 'run-1', kind: 'node_complete', nodeId: 'assistant_turn', ts: '' },
  ];

  function statusOf(wrapper: ReturnType<typeof mountWith>['wrapper'], id: string) {
    const node = wrapper
      .findAll('[data-testid^="canvas-node-"]')
      .find((w) => w.text().includes(id));
    return node?.attributes('data-status');
  }

  it('renders the projection with a status on every node', async () => {
    const { wrapper, materializeRun } = mountWith({
      materializedYAML: MATERIALIZED_RUN_YAML,
      trace: TRACE,
    });
    await flushPromises();
    expect(materializeRun).toHaveBeenCalledWith('run-1');
    const canvas = wrapper.get('[data-testid="graph-canvas"]');
    expect(canvas.attributes('data-node-count')).toBe('6');
    expect(statusOf(wrapper, 'assistant_turn@1')).toBe('complete');
    expect(statusOf(wrapper, 'tool_leg@1')).toBe('failed');
    expect(statusOf(wrapper, 'never_ran')).toBe('not_reached');
  });

  // Skipped branches are the whole reason a routed run needs a graph
  // view: the trace shows what ran, only the graph shows what didn't.
  it('greys the branch the run skipped', async () => {
    const { wrapper } = mountWith({ materializedYAML: MATERIALIZED_RUN_YAML });
    await flushPromises();
    expect(statusOf(wrapper, 'skipped_leg@1')).toBe('skipped');
    const skipped = wrapper
      .findAll('[data-testid^="canvas-node-"]')
      .find((w) => w.text().includes('skipped_leg@1'));
    expect(skipped?.classes().join(' ')).toContain('opacity-50');
  });

  /*
   * The live surface. `Graph_GetRunStatus` carries no node-level data —
   * it is counters and a lifecycle state — so a live overlay could only
   * come from a new RPC or from the stream that already exists. It comes
   * from the stream: the projection is re-read when the trace poll
   * returns new events, and the fire with no matching complete reads as
   * `running` instead of `incomplete`.
   */
  it('shows the in-flight node as running while the run is live', async () => {
    const { wrapper } = mountWith({
      status: defaultStatus({ state: 'running' }),
      materializedYAML: MATERIALIZED_RUN_YAML,
      trace: TRACE,
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="run-graph-live"]').exists()).toBe(true);
    expect(statusOf(wrapper, 'assistant_turn@2')).toBe('running');
  });

  it('reads the same node as incomplete once the run has ended', async () => {
    const { wrapper } = mountWith({
      status: defaultStatus({ state: 'failed' }),
      materializedYAML: MATERIALIZED_RUN_YAML,
      trace: TRACE,
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="run-graph-live"]').exists()).toBe(false);
    expect(statusOf(wrapper, 'assistant_turn@2')).toBe('incomplete');
  });

  it('re-projects only when the poll actually returned new events', async () => {
    const { wrapper, materializeRun } = mountWith({
      status: defaultStatus({ state: 'running' }),
      materializedYAML: MATERIALIZED_RUN_YAML,
      trace: [],
    });
    await flushPromises();
    const initial = materializeRun.mock.calls.length;
    await wrapper.vm.pollOnce();
    await flushPromises();
    expect(materializeRun.mock.calls.length).toBe(initial);
  });

  // start_seq is the materialization's join key back into the EventLog,
  // and the trace list is keyed by that same seq — so the jump is a
  // lookup rather than a guess.
  it('jumps to the trace rows for the node that was clicked', async () => {
    const { wrapper } = mountWith({
      materializedYAML: MATERIALIZED_RUN_YAML,
      trace: TRACE,
    });
    await flushPromises();
    wrapper.vm.onNodeStatusClick({ detail: { startSeq: 4 } });
    await flushPromises();
    expect(wrapper.get('[data-testid="trace-event-4"]').attributes('data-focused')).toBe('true');
    expect(wrapper.get('[data-testid="trace-event-1"]').attributes('data-focused')).toBe('false');
  });

  it('lands on the first row at or after the span when the exact seq is not in the tail', async () => {
    const { wrapper } = mountWith({
      materializedYAML: MATERIALIZED_RUN_YAML,
      trace: TRACE,
    });
    await flushPromises();
    // skipped_leg@1's start_seq is 8; the tail holds 6 then 9.
    wrapper.vm.onNodeStatusClick({ detail: { startSeq: 8 } });
    await flushPromises();
    expect(wrapper.get('[data-testid="trace-event-9"]').attributes('data-focused')).toBe('true');
  });

  it('ignores a click on a node with no recorded span', async () => {
    const { wrapper } = mountWith({
      materializedYAML: MATERIALIZED_RUN_YAML,
      trace: TRACE,
    });
    await flushPromises();
    wrapper.vm.onNodeStatusClick({ detail: { sourceNode: 'never_ran' } });
    await flushPromises();
    expect(wrapper.findAll('[data-focused="true"]').length).toBe(0);
  });

  it('badges a degraded projection on the canvas itself', async () => {
    const { wrapper } = mountWith({ materializedYAML: MATERIALIZED_DEGRADED_YAML });
    await flushPromises();
    expect(wrapper.get('[data-testid="canvas-notice"]').text()).toContain(
      'Degraded projection',
    );
  });

  it('does not badge a healthy projection', async () => {
    const { wrapper } = mountWith({ materializedYAML: MATERIALIZED_RUN_YAML });
    await flushPromises();
    expect(wrapper.find('[data-testid="canvas-notice"]').exists()).toBe(false);
  });

  /*
   * Structural read-only (FR-005, closing the WP12-review N3 hazard).
   * The assertion is that the mutation AFFORDANCES do not exist — not
   * that they are disabled — because a disabled control is one
   * `.trigger()` away from writing into a record of something that
   * already happened.
   */
  it('has no authoring affordance on the run canvas', async () => {
    const { wrapper } = mountWith({ materializedYAML: MATERIALIZED_RUN_YAML });
    await flushPromises();
    const canvas = wrapper.get('[data-testid="graph-canvas"]');
    expect(canvas.attributes('data-readonly')).toBe('true');
    expect(canvas.attributes('tabindex')).toBeUndefined();
    expect(wrapper.find('[data-testid="canvas-delete"]').exists()).toBe(false);
  });

  /*
   * A run whose resolved spec was evicted cannot be projected at all.
   * That must not take the trace list down with it — the trace is the
   * surface that still works, and it is the one this view existed for
   * before the graph pane arrived.
   */
  it('keeps the trace list when the projection fails', async () => {
    const { wrapper } = mountWith({
      materializeError: 'run spec unavailable',
      trace: TRACE,
    });
    await flushPromises();
    expect(wrapper.get('[data-testid="run-graph-error"]').text()).toContain(
      'run spec unavailable',
    );
    expect(wrapper.find('[data-testid="graph-canvas"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="trace-event-1"]').exists()).toBe(true);
    // The projection failing is not a run failure; the status card stays clean.
    expect(wrapper.find('[role="alert"]').exists()).toBe(false);
  });
});
