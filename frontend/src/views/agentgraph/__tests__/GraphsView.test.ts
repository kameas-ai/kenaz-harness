import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { GraphInfo, GraphRunStatus } from '@/lib/types';

const pushMock = vi.fn();
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: {}, query: {} }),
  useRouter: () => ({ push: pushMock }),
}));

import GraphsView from '@/views/agentgraph/GraphsView.vue';

interface MountOpts {
  graphs?: GraphInfo[];
  startRunImpl?: () => Promise<{ runId: string; status: GraphRunStatus }>;
}

function makeGraph(over: Partial<GraphInfo>): GraphInfo {
  return {
    id: 'g',
    name: 'Graph',
    scope: 'library',
    ...over,
  };
}

function mountWith(opts: MountOpts = {}) {
  const graphs = opts.graphs ?? [];
  const listGraphs = vi.fn(async () => graphs);
  const startRun = vi.fn(
    opts.startRunImpl ??
      (async () => ({
        runId: 'fake-run',
        status: {
          runId: 'fake-run',
          graphId: 'g',
          state: 'running' as const,
          startedAt: '',
          updatedAt: '',
          nodesComplete: 0,
          llmTokens: 0,
          llmCalls: 0,
          toolCalls: 0,
          costUsd: 0,
        },
      })),
  );
  const deleteGraph = vi.fn(async () => undefined);

  const client = createFakeHarnessClient({
    graph: {
      listGraphs,
      loadGraph: async (id) => ({ id, scope: 'library' as const, yaml: '' }),
      saveGraph: async () => undefined,
      deleteGraph,
      validate: async () => ({ ok: true, issues: [] }),
      checkEdge: async () => ({ ok: true }),
      startRun,
      getRunStatus: async (id) => ({
        runId: id,
        graphId: 'g',
        state: 'running' as const,
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
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        CanvasHead: {
          template: '<div class="canvas-head"><slot name="trailing" /></div>',
        },
      },
    },
  });
  return { wrapper, listGraphs, startRun, deleteGraph };
}

describe('GraphsView', () => {
  it('renders the empty state when there are no graphs', async () => {
    const { wrapper, listGraphs } = mountWith();
    await flushPromises();
    expect(listGraphs).toHaveBeenCalled();
    expect(wrapper.find('[data-testid="graphs-empty"]').exists()).toBe(true);
  });

  it('renders one row per graph and exposes a Run button', async () => {
    const graphs = [
      makeGraph({ id: 'toolloop_default', name: 'Default toolloop' }),
      makeGraph({ id: 'user_one', name: 'User one', scope: 'user' }),
    ];
    const { wrapper } = mountWith({ graphs });
    await flushPromises();
    expect(wrapper.find('[data-testid="graph-row-toolloop_default"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="graph-run-toolloop_default"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="graph-delete-user_one"]').exists()).toBe(true);
    // Library graphs should not expose a delete button.
    expect(wrapper.find('[data-testid="graph-delete-toolloop_default"]').exists()).toBe(false);
  });

  it('forwards graphId to startRun and routes to the RunView', async () => {
    pushMock.mockReset();
    const graphs = [makeGraph({ id: 'g1', name: 'G' })];
    const { wrapper, startRun } = mountWith({ graphs });
    await flushPromises();
    await wrapper.get('[data-testid="graph-run-g1"]').trigger('click');
    await flushPromises();
    expect(startRun).toHaveBeenCalledWith({ graphId: 'g1' });
    expect(pushMock).toHaveBeenCalledWith({
      name: 'graph-run',
      params: { runId: 'fake-run' },
    });
  });

  it('marks an invalid graph and disables its Run button (UNIT-2, FR-004)', async () => {
    const graphs = [
      makeGraph({ id: 'valid_one', name: 'Valid' }),
      makeGraph({
        id: 'bad_one',
        name: 'Bad',
        scope: 'user',
        invalid: true,
        invalidReason: 'schema: unknown entrypoint "missing"',
      }),
    ];
    const { wrapper, startRun } = mountWith({ graphs });
    await flushPromises();

    // Invalid marker renders only on the invalid row.
    expect(wrapper.find('[data-testid="graph-invalid-bad_one"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="graph-invalid-valid_one"]').exists()).toBe(false);

    // Run is disabled for the invalid row and clicking it never calls
    // startRun — this is honesty, not the security control (AC-008's
    // backend equivalent is the real gate); the mutation this guards is
    // "hardcode the disabled condition to false".
    const runButton = wrapper.get('[data-testid="graph-run-bad_one"]');
    expect(runButton.attributes('disabled')).toBeDefined();
    await runButton.trigger('click');
    await flushPromises();
    expect(startRun).not.toHaveBeenCalled();
  });

  it('badges an unreviewed model-authored graph and disables its Run button (UNIT-6, AC-009)', async () => {
    const graphs = [
      makeGraph({ id: 'human_one', name: 'Human', scope: 'user' }),
      makeGraph({
        id: 'draft_one',
        name: 'Draft',
        scope: 'user',
        specProvenance: 'model_authored',
      }),
    ];
    const { wrapper, startRun } = mountWith({ graphs });
    await flushPromises();

    // Badge renders only on the unreviewed row.
    expect(wrapper.find('[data-testid="graph-unreviewed-draft_one"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="graph-unreviewed-human_one"]').exists()).toBe(false);

    // Run is disabled for the unreviewed row and clicking it never calls
    // startRun — this is honesty, not the security control (AC-008's
    // graph_run_unreviewed_forbid.cedar is the real gate); the mutation
    // this guards is "hardcode the disabled/banner condition to false".
    const runButton = wrapper.get('[data-testid="graph-run-draft_one"]');
    expect(runButton.attributes('disabled')).toBeDefined();
    await runButton.trigger('click');
    await flushPromises();
    expect(startRun).not.toHaveBeenCalled();

    // The reviewed row's Run stays enabled.
    expect(
      wrapper.get('[data-testid="graph-run-human_one"]').attributes('disabled'),
    ).toBeUndefined();
  });

  it('confirms before deleting a user graph and forwards the id', async () => {
    const graphs = [makeGraph({ id: 'user_g', scope: 'user' })];
    const { wrapper, deleteGraph } = mountWith({ graphs });
    await flushPromises();
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
    await wrapper.get('[data-testid="graph-delete-user_g"]').trigger('click');
    await flushPromises();
    expect(confirmSpy).toHaveBeenCalled();
    expect(deleteGraph).toHaveBeenCalledWith('user_g');
    confirmSpy.mockRestore();
  });
});
