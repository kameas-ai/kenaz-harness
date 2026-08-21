import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { GraphSpec, GraphValidationResult } from '@/lib/types';

const pushMock = vi.fn();
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: 'user_g' }, query: {} }),
  useRouter: () => ({ push: pushMock }),
}));

import GraphEditor from '@/views/agentgraph/GraphEditor.vue';

interface MountOpts {
  spec?: GraphSpec;
  validateImpl?: (yaml: string) => Promise<GraphValidationResult>;
  saveImpl?: (spec: GraphSpec) => Promise<void>;
}

function mountWith(opts: MountOpts = {}) {
  const spec =
    opts.spec ?? {
      id: 'user_g',
      scope: 'user' as const,
      yaml: 'spec_version: "1"\nid: user_g\nentrypoints: [a]\nnodes:\n  - id: a\n    kind: plan\n    attrs:\n      verbosity: terse\n',
    };
  const loadGraph = vi.fn(async (_id: string) => spec);
  const validate = vi.fn(
    opts.validateImpl ?? (async () => ({ ok: true, issues: [] })),
  );
  const saveGraph = vi.fn(opts.saveImpl ?? (async () => undefined));

  const client = createFakeHarnessClient({
    graph: {
      listGraphs: async () => [],
      loadGraph,
      saveGraph,
      deleteGraph: async () => undefined,
      validate,
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

  const wrapper = mount(GraphEditor, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        CanvasHead: {
          template: '<div class="canvas-head"><slot name="trailing" /></div>',
        },
      },
    },
  });
  return { wrapper, loadGraph, validate, saveGraph };
}

describe('GraphEditor', () => {
  it('loads the graph YAML on mount', async () => {
    const { wrapper, loadGraph } = mountWith();
    await flushPromises();
    expect(loadGraph).toHaveBeenCalledWith('user_g');
    const ta = wrapper.get('[data-testid="editor-yaml"]');
    expect((ta.element as HTMLTextAreaElement).value).toContain('user_g');
  });

  it('runs the validator on click and renders an OK pane', async () => {
    const { wrapper, validate } = mountWith();
    await flushPromises();
    await wrapper.get('[data-testid="editor-validate"]').trigger('click');
    await flushPromises();
    expect(validate).toHaveBeenCalled();
    expect(wrapper.find('[data-testid="editor-validation-ok"]').exists()).toBe(true);
  });

  it('renders validator issues with rule + message', async () => {
    const { wrapper } = mountWith({
      validateImpl: async () => ({
        ok: false,
        issues: [
          { rule: 'cycle', message: 'unbounded cycle outside loop' },
          { rule: 'port', message: 'orphan node x' },
        ],
      }),
    });
    await flushPromises();
    await wrapper.get('[data-testid="editor-validate"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="editor-validation-issue-0"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="editor-validation-issue-1"]').exists()).toBe(true);
    expect(wrapper.html()).toContain('cycle');
    expect(wrapper.html()).toContain('orphan node x');
  });

  it('refuses to save when validation fails', async () => {
    const { wrapper, saveGraph } = mountWith({
      validateImpl: async () => ({
        ok: false,
        issues: [{ rule: 'schema', message: 'bad' }],
      }),
    });
    await flushPromises();
    await wrapper.get('[data-testid="editor-save"]').trigger('click');
    await flushPromises();
    expect(saveGraph).not.toHaveBeenCalled();
  });

  it('saves a green graph', async () => {
    const { wrapper, saveGraph } = mountWith();
    await flushPromises();
    await wrapper.get('[data-testid="editor-save"]').trigger('click');
    await flushPromises();
    expect(saveGraph).toHaveBeenCalled();
    expect(wrapper.find('[data-testid="editor-saved"]').exists()).toBe(true);
  });

  it('renders read-only banner for library graphs and hides Save', async () => {
    const { wrapper } = mountWith({
      spec: {
        id: 'toolloop_default',
        scope: 'library' as const,
        yaml: 'spec_version: "1"\nid: toolloop_default\nentrypoints: [a]\nnodes:\n  - id: a\n    kind: plan\n    attrs:\n      verbosity: terse\n',
      },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="editor-readonly-banner"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="editor-save"]').exists()).toBe(false);
  });

  it('renders the unreviewed-draft banner for a model-authored graph (UNIT-6, AC-009)', async () => {
    const { wrapper } = mountWith({
      spec: {
        id: 'user_g',
        scope: 'user' as const,
        yaml: 'spec_version: "1"\nid: user_g\nspec_provenance: model_authored\nentrypoints: [a]\nnodes:\n  - id: a\n    kind: plan\n    attrs:\n      verbosity: terse\n',
      },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="editor-unreviewed-banner"]').exists()).toBe(true);
    // A model-authored graph is scope "user" — still editable and
    // savable. Saving is the review step (UNIT-5 clears the marker
    // server-side on a user-initiated save), so Save must stay present.
    expect(wrapper.find('[data-testid="editor-save"]').exists()).toBe(true);
  });

  it('does not render the unreviewed-draft banner for an ordinary human-authored graph', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="editor-unreviewed-banner"]').exists()).toBe(false);
  });
});
