/**
 * RunView — two runs, one view instance (dogfood 2026-10-08 round 2).
 *
 * "Run details" for the second turn of a session rendered the FIRST
 * turn's numbers (llm tokens 224802 / $0.1124 for a run whose own
 * llm_call recorded 237262 / $0.4746). The backend answers per run id
 * correctly; the view did not: vue-router reuses RunView when only
 * :runId changes, RunView read the id once in onMounted, and a
 * completed run stops polling — so the previous run's status stayed on
 * screen under the new run's URL.
 */
import { describe, it, expect, vi } from 'vitest';
import { reactive } from 'vue';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { GraphRunStatus, GraphRunTraceEvent } from '@/lib/types';

const route = reactive({ params: { runId: 'chat-turn-1' } as Record<string, string>, query: {} });
vi.mock('vue-router', () => ({
  useRoute: () => route,
  useRouter: () => ({ push: vi.fn() }),
}));

import RunView from '@/views/agentgraph/RunView.vue';

function status(runId: string, llmTokens: number, costUsd: number): GraphRunStatus {
  return {
    runId,
    graphId: 'chat_default',
    state: 'completed',
    startedAt: '2026-10-08T21:00:00Z',
    updatedAt: '2026-10-08T21:00:05Z',
    nodesComplete: 5,
    llmTokens,
    llmCalls: 1,
    toolCalls: 0,
    costUsd,
  };
}

const STATUSES: Record<string, GraphRunStatus> = {
  'chat-turn-1': status('chat-turn-1', 224802, 0.1124),
  'chat-turn-2': status('chat-turn-2', 237262, 0.4746),
};

const TRACES: Record<string, GraphRunTraceEvent[]> = {
  'chat-turn-1': [{ seq: 1, kind: 'run_start', nodeId: '', timestamp: '2026-10-08T21:00:00Z' } as GraphRunTraceEvent],
  'chat-turn-2': [{ seq: 1, kind: 'run_start', nodeId: '', timestamp: '2026-10-08T21:01:00Z' } as GraphRunTraceEvent],
};

describe('RunView — switching between two runs of one session', () => {
  it('shows the second run its own counters, not the first run\'s', async () => {
    const getRunStatus = vi.fn(async (id: string) => STATUSES[id]);
    const getRunTrace = vi.fn(async (id: string, since: number) => (since > 0 ? [] : TRACES[id]));
    const client = createFakeHarnessClient({
      graph: {
        listGraphs: async () => [],
        loadGraph: async (id) => ({ id, scope: 'library' as const, yaml: '' }),
        saveGraph: async () => undefined,
        deleteGraph: async () => undefined,
        validate: async () => ({ ok: true, issues: [] }),
        checkEdge: async () => ({ ok: true }),
        startRun: async () => ({ runId: 'x', status: STATUSES['chat-turn-1'] }),
        getRunStatus,
        getRunTrace,
        resume: async () => undefined,
        resolveApproval: async () => undefined,
        cancelRun: async () => undefined,
        materializeRun: async (id: string) => ({ id, scope: 'materialized' as const, yaml: '' }),
      },
    });
    route.params.runId = 'chat-turn-1';
    const wrapper = mount(RunView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
        stubs: { CanvasHead: { template: '<div><slot name="trailing" /></div>' } },
      },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="run-status"]').text()).toContain('llm tokens: 224802');

    // Same component instance, new :runId — what vue-router does when the
    // user opens the next turn's "Run details".
    route.params.runId = 'chat-turn-2';
    await flushPromises();

    const text = wrapper.find('[data-testid="run-status"]').text();
    expect(text).toContain('llm tokens: 237262');
    expect(text).toContain('cost: $0.4746');
    expect(getRunStatus).toHaveBeenLastCalledWith('chat-turn-2');
    // The trace restarted from the new run's beginning, not from the old
    // run's last seq.
    expect(getRunTrace).toHaveBeenCalledWith('chat-turn-2', 0);
    wrapper.unmount();
  });
});
