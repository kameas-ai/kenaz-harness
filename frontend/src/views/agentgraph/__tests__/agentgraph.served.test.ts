/**
 * agentgraph.served.test.ts — served-mode boundary panel coverage for the
 * four /agentgraph* routes (AC-708, served-mode-is-a-real-mode-01PMZ707
 * WP03, spec.md §5.3, D-701).
 *
 * Graph_* has no serve dispatch case — verified in spec.md §2 (E-3): no
 * `Graph_*` entry in core/serve/methods.go's servedMethods, no case in the
 * dispatch switch, TestServedMethodsMatchDispatchSwitch pins it. Routing
 * graph authoring into served mode would be NEW capability work (D-701),
 * so these three views (GraphsView, GraphEditor — which backs both
 * /agentgraph/edit/:id and /agentgraph/run/:runId/graph — and RunView) all
 * render NotAvailableInServedMode instead of their real content, and none
 * of them may call a Graph_* method while doing it.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref } from 'vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

const pushMock = vi.fn();
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: 'user_g', runId: 'run-1' }, query: {} }),
  useRouter: () => ({ push: pushMock }),
}));

// Controllable served-mode flag, mirroring ProvidersView.served.test.ts.
let servedModeFlag = true;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return {
    ...actual,
    isServedMode: () => servedModeFlag,
    useServedMode: () => ref(servedModeFlag),
  };
});

import GraphsView from '@/views/agentgraph/GraphsView.vue';
import GraphEditor from '@/views/agentgraph/GraphEditor.vue';
import RunView from '@/views/agentgraph/RunView.vue';

function failingGraphClient() {
  const fail = () => {
    throw new Error(
      'graph.* must not be called in served mode — Graph_* has no serve dispatch case',
    );
  };
  return createFakeHarnessClient({
    graph: {
      listGraphs: fail,
      loadGraph: fail,
      saveGraph: fail,
      deleteGraph: fail,
      validate: fail,
      checkEdge: fail,
      startRun: fail,
      getRunStatus: fail,
      getRunTrace: fail,
      resume: fail,
      cancelRun: fail,
      materializeRun: fail,
    },
  });
}

const stubs = {
  CanvasHead: {
    template: '<div class="canvas-head"><slot name="trailing" /></div>',
  },
};

describe('agentgraph views (served mode)', () => {
  it('GraphsView renders the boundary panel and never calls Graph_*', async () => {
    servedModeFlag = true;
    const client = failingGraphClient();
    const wrapper = mount(GraphsView, {
      global: { provide: { [HarnessClientKey as symbol]: client }, stubs },
    });
    await flushPromises();

    expect(
      wrapper.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(true);
    expect(wrapper.text()).not.toContain('desktop app');
    expect(wrapper.find('[data-testid="graphs-new"]').exists()).toBe(false);
  });

  it('GraphEditor renders the boundary panel and never calls Graph_* (backs /agentgraph/edit/:id and /agentgraph/run/:runId/graph)', async () => {
    servedModeFlag = true;
    const client = failingGraphClient();
    const wrapper = mount(GraphEditor, {
      global: { provide: { [HarnessClientKey as symbol]: client }, stubs },
    });
    await flushPromises();

    expect(
      wrapper.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(true);
    expect(wrapper.text()).not.toContain('desktop app');
  });

  it('RunView renders the boundary panel, does not poll, and never calls Graph_*', async () => {
    servedModeFlag = true;
    const client = failingGraphClient();
    const wrapper = mount(RunView, {
      global: { provide: { [HarnessClientKey as symbol]: client }, stubs },
    });
    await flushPromises();

    expect(
      wrapper.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(true);
    expect(wrapper.text()).not.toContain('desktop app');
  });
});

describe('agentgraph views (desktop mode regression)', () => {
  it('GraphsView renders its real content, not the panel', async () => {
    servedModeFlag = false;
    const client = createFakeHarnessClient({
      graph: {
        listGraphs: async () => [],
        loadGraph: async (id) => ({ id, scope: 'library' as const, yaml: '' }),
        saveGraph: async () => undefined,
        deleteGraph: async () => undefined,
        validate: async () => ({ ok: true, issues: [] }),
        checkEdge: async () => ({ ok: true }),
        startRun: async (req) => ({
          runId: 'r',
          status: {
            runId: 'r',
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
        getRunStatus: async (id) => ({
          runId: id,
          graphId: 'g',
          state: 'completed' as const,
          startedAt: '',
          updatedAt: '',
          nodesComplete: 0,
          llmTokens: 0,
          llmCalls: 0,
          toolCalls: 0,
          costUsd: 0,
        }),
        getRunTrace: async () => [],
        resume: async () => undefined,
        cancelRun: async () => undefined,
        materializeRun: async (runID: string) => ({
          id: `g__run_${runID}`,
          scope: 'materialized' as const,
          yaml: '',
        }),
      },
    });
    const wrapper = mount(GraphsView, {
      global: { provide: { [HarnessClientKey as symbol]: client }, stubs },
    });
    await flushPromises();

    expect(
      wrapper.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(false);
    expect(wrapper.find('[data-testid="graphs-new"]').exists()).toBe(true);
  });
});
