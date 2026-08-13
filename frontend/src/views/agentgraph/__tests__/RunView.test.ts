import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { GraphRunStatus, GraphRunTraceEvent } from '@/lib/types';

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

  const client = createFakeHarnessClient({
    graph: {
      listGraphs: async () => [],
      loadGraph: async (id) => ({ id, scope: 'library' as const, yaml: '' }),
      saveGraph: async () => undefined,
      deleteGraph: async () => undefined,
      validate: async () => ({ ok: true, issues: [] }),
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
      materializeRun: async (runID: string) => ({
        id: `g__run_${runID}`,
        scope: 'materialized' as const,
        yaml: '',
      }),
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
  return { wrapper, getRunStatus, getRunTrace, resume, cancelRun };
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
