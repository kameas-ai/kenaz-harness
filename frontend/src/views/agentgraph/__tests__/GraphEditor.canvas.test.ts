/**
 * GraphEditor ⇄ canvas integration
 * (visual-graph-authoring-01PMUX01 WP03/WP04).
 *
 * The claim under test is the one the spec makes non-negotiable: the
 * canvas and the textarea are ONE buffer. So every assertion here reads
 * the textarea's value after a canvas op, and reads the canvas after a
 * textarea edit — never an intermediate model.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { __resetManifestStoreCache } from '@/composables/useNodeManifest';
import type { GraphSpec, GraphEdgeCheckResult, GraphEdgeRef } from '@/lib/types';

const pushMock = vi.fn();
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: 'user_g' }, query: {} }),
  useRouter: () => ({ push: pushMock }),
}));

import GraphEditor from '@/views/agentgraph/GraphEditor.vue';

const BASE_YAML = `spec_version: "1"
id: user_g
# a comment the canvas must not eat
entrypoints: [a]
nodes:
  - id: a
    kind: plan
    attrs:
      verbosity: terse
  - id: b
    kind: transform
    attrs: {}
`;

function mountWith(
  opts: {
    scope?: 'user' | 'library' | 'materialized';
    yaml?: string;
    checkEdgeImpl?: () => Promise<GraphEdgeCheckResult>;
  } = {},
) {
  __resetManifestStoreCache();
  const spec: GraphSpec = {
    id: 'user_g',
    scope: opts.scope ?? 'user',
    yaml: opts.yaml ?? BASE_YAML,
  };
  const saveGraph = vi.fn(async (_spec: GraphSpec) => undefined);
  const checkEdge = vi.fn(
    async (
      _graphJSON: string,
      _edge: GraphEdgeRef,
    ): Promise<GraphEdgeCheckResult> =>
      opts.checkEdgeImpl ? opts.checkEdgeImpl() : { ok: true },
  );
  const client = createFakeHarnessClient({
    graph: {
      listGraphs: async () => [],
      loadGraph: async () => spec,
      saveGraph,
      deleteGraph: async () => undefined,
      validate: async () => ({ ok: true, issues: [] }),
      checkEdge,
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
      materializeRun: async (runID) => ({
        id: runID,
        scope: 'materialized' as const,
        yaml: '',
      }),
    },
    nodes: {
      catalog: async () => [
        { id: 'plan', callable: true, category: 'compute' },
        { id: 'transform', callable: true, category: 'compute' },
        { id: 'model', callable: true, category: 'compute' },
      ],
      get: async (id) => ({
        summary: { id, callable: true },
        chain: [id],
        attrs:
          id === 'model'
            ? [{ name: 'model', type: 'string', default: 'default' }]
            : [],
        ports: {},
        provenance: [],
      }),
      reloadOverrides: async () => ({ added: [], removed: [], modified: [] }),
      listUserOverrides: async () => [],
      doctor: async () => ({
        shippedCount: 0,
        userOverrideCount: 0,
        archetypeCount: 0,
        callableCount: 0,
        aliasCount: 0,
        hotReloadEnabled: false,
      }),
    },
  });

  const wrapper = mount(GraphEditor, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, saveGraph, checkEdge };
}

function textarea(wrapper: ReturnType<typeof mount>): HTMLTextAreaElement {
  return wrapper.get('[data-testid="editor-yaml"]').element as HTMLTextAreaElement;
}

interface EditorVM {
  applyCanvasOp: (op: unknown) => void;
  persistCanvasLayout: () => void;
  save: () => Promise<void>;
  flushCanvasParse: () => void;
  dirty: boolean;
}

describe('GraphEditor canvas — one buffer', () => {
  it('mounts the canvas alongside the textarea', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="graph-canvas"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="editor-yaml"]').exists()).toBe(true);
  });

  it('renders the loaded graph on the canvas', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(
      wrapper.get('[data-testid="graph-canvas"]').attributes('data-node-count'),
    ).toBe('2');
  });

  it('a canvas add-node op lands in the textarea', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    (wrapper.vm as unknown as EditorVM).applyCanvasOp({
      type: 'add-node',
      kind: 'model',
      position: { x: 12, y: 34 },
    });
    await flushPromises();
    const text = textarea(wrapper).value;
    expect(text).toContain('id: model_1');
    expect(text).toContain('kind: model');
    // Manifest-derived default attrs, not an empty stub.
    expect(text).toContain('model: default');
    // The drop position went into the layout block.
    expect(text).toMatch(/layout:[\s\S]*model_1:[\s\S]*x: 12[\s\S]*y: 34/);
    // And the comment survived.
    expect(text).toContain('# a comment the canvas must not eat');
  });

  it('a canvas op re-renders the canvas from the same buffer', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    (wrapper.vm as unknown as EditorVM).applyCanvasOp({
      type: 'add-node',
      kind: 'transform',
      position: { x: 0, y: 0 },
    });
    await flushPromises();
    expect(
      wrapper.get('[data-testid="graph-canvas"]').attributes('data-node-count'),
    ).toBe('3');
  });

  it('a hand edit to the textarea re-renders the canvas', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    await wrapper.get('[data-testid="editor-yaml"]').setValue(
      `${BASE_YAML}  - id: c\n    kind: transform\n    attrs: {}\n`,
    );
    (wrapper.vm as unknown as EditorVM).flushCanvasParse();
    await flushPromises();
    expect(
      wrapper.get('[data-testid="graph-canvas"]').attributes('data-node-count'),
    ).toBe('3');
  });

  it('keeps the last good render when the buffer stops parsing', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    await wrapper
      .get('[data-testid="editor-yaml"]')
      .setValue('nodes:\n  - id: a\n   kind: broken\n  bad indent here\n');
    (wrapper.vm as unknown as EditorVM).flushCanvasParse();
    await flushPromises();
    // Still showing the two nodes from before the bad edit…
    expect(
      wrapper.get('[data-testid="graph-canvas"]').attributes('data-node-count'),
    ).toBe('2');
    // …and saying so, rather than pretending it is current.
    expect(wrapper.find('[data-testid="canvas-stale"]').exists()).toBe(true);
  });

  it('a palette click adds a node through the same op as a canvas drop', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const kind = wrapper.find('[data-testid="palette-kind-transform"]');
    expect(kind.exists()).toBe(true);
    await kind.trigger('click');
    await flushPromises();
    expect(textarea(wrapper).value).toContain('id: transform_1');
  });

  it('the palette cannot mutate a read-only buffer (WP12 review N3)', async () => {
    const { wrapper } = mountWith({ scope: 'library' });
    await flushPromises();
    const before = textarea(wrapper).value;
    const kind = wrapper.find('[data-testid="palette-kind-transform"]');
    await kind.trigger('click');
    await flushPromises();
    expect(textarea(wrapper).value).toBe(before);
  });

  it('the attribute editor cannot mutate a read-only buffer either', async () => {
    // The other half of WP12 review N3. Attr edits used to rewrite the
    // text by string-splicing without consulting readOnly; they go
    // through the same op path the canvas does now.
    const { wrapper } = mountWith({ scope: 'materialized' });
    await flushPromises();
    const before = textarea(wrapper).value;
    (wrapper.vm as unknown as EditorVM).applyCanvasOp({
      type: 'set-attrs',
      id: 'a',
      attrs: { verbosity: 'verbose' },
    });
    await flushPromises();
    expect(textarea(wrapper).value).toBe(before);
  });

  it('renders the canvas read-only for a library graph', async () => {
    const { wrapper } = mountWith({ scope: 'library' });
    await flushPromises();
    expect(
      wrapper.get('[data-testid="graph-canvas"]').attributes('data-readonly'),
    ).toBe('true');
  });

  it('drops the textarea drag-and-drop path (WP03 removed it)', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const before = textarea(wrapper).value;
    await wrapper.get('[data-testid="editor-yaml"]').trigger('drop', {
      dataTransfer: {
        types: ['application/x-kenaz-node-kind'],
        getData: () => 'model',
      },
    });
    await flushPromises();
    expect(textarea(wrapper).value).toBe(before);
  });

  it('persists layout only on save, and prunes stale entries then', async () => {
    const { wrapper, saveGraph } = mountWith({
      yaml: `${BASE_YAML}layout:\n  a: { x: 1, y: 2 }\n  ghost: { x: 9, y: 9 }\n`,
    });
    await flushPromises();
    // Nothing was dragged, so the buffer is untouched apart from the
    // stale-entry prune the save performs.
    await (wrapper.vm as unknown as EditorVM).save();
    await flushPromises();
    expect(saveGraph).toHaveBeenCalled();
    const savedYAML = saveGraph.mock.calls[0][0].yaml;
    expect(savedYAML).toContain('a:');
    expect(savedYAML).not.toContain('ghost');
  });

  it('a save with zero drags writes NO layout block at all', async () => {
    // The assertion the first version of this suite never made, and the
    // bug it hid: `pendingLayout` counted "no authored layout" as
    // "moved", so the FIRST save of an untouched layout-free graph
    // injected a full auto-layout block — pinning the graph forever
    // (no more reflow when nodes are added) and breaking spec §4's
    // "authored graphs without layout stay byte-identical" for anyone
    // who opens a library graph, copies it, and saves.
    const { wrapper, saveGraph } = mountWith();
    await flushPromises();
    expect(textarea(wrapper).value).not.toContain('layout:');
    await (wrapper.vm as unknown as EditorVM).save();
    await flushPromises();
    expect(saveGraph).toHaveBeenCalled();
    expect(saveGraph.mock.calls[0][0].yaml).not.toContain('layout:');
    expect(textarea(wrapper).value).not.toContain('layout:');
  });

  it('a save after one drag writes only that node', async () => {
    const { wrapper, saveGraph } = mountWith();
    await flushPromises();
    const canvas = wrapper.findComponent({ name: 'GraphCanvas' });
    (
      canvas.vm as unknown as {
        onNodeDragStop: (p: {
          nodes: Array<{ id: string; position: { x: number; y: number } }>;
        }) => void;
      }
    ).onNodeDragStop({ nodes: [{ id: 'b', position: { x: 17, y: 29 } }] });
    await flushPromises();
    await (wrapper.vm as unknown as EditorVM).save();
    await flushPromises();
    const savedYAML = saveGraph.mock.calls[0][0].yaml;
    expect(savedYAML).toContain('layout:');
    expect(savedYAML).toMatch(/b:\s*\n?\s*x: 17/);
    // Node `a` was never dragged, so it stays on auto-layout and out of
    // the file — only the deliberate placement is persisted.
    expect(savedYAML).not.toMatch(/^\s+a:$/m);
  });

  it('refuses an illegal edge with the validator message', async () => {
    const { wrapper, checkEdge } = mountWith({
      checkEdgeImpl: async () => ({
        ok: false,
        reason: 'type mismatch a.out(text) → b.in(json)',
      }),
    });
    await flushPromises();
    const canvas = wrapper.findComponent({ name: 'GraphCanvas' });
    await (
      canvas.vm as unknown as {
        onConnect: (c: Record<string, string>) => Promise<void>;
      }
    ).onConnect({
      source: 'a',
      target: 'b',
      sourceHandle: 'out',
      targetHandle: 'in',
    });
    await flushPromises();
    expect(checkEdge).toHaveBeenCalled();
    // The graph went over as JSON — the buffer the canvas is showing.
    const [graphJSON, edge] = checkEdge.mock.calls[0];
    expect(JSON.parse(graphJSON).id).toBe('user_g');
    expect(edge).toEqual({
      from: { node: 'a', port: 'out' },
      to: { node: 'b', port: 'in' },
    });
    expect(textarea(wrapper).value).not.toContain('edges:');
    expect(wrapper.find('[data-testid="canvas-edge-rejected"]').text()).toContain(
      'type mismatch',
    );
  });

  it('debounces the re-parse a manual edit triggers', async () => {
    vi.useFakeTimers();
    try {
      const { wrapper } = mountWith();
      await vi.runAllTimersAsync();
      await flushPromises();
      await wrapper
        .get('[data-testid="editor-yaml"]')
        .setValue(`${BASE_YAML}  - id: c\n    kind: transform\n    attrs: {}\n`);
      await flushPromises();
      // Still two: the parse has not run yet.
      expect(
        wrapper.get('[data-testid="graph-canvas"]').attributes('data-node-count'),
      ).toBe('2');
      await vi.advanceTimersByTimeAsync(200);
      await flushPromises();
      expect(
        wrapper.get('[data-testid="graph-canvas"]').attributes('data-node-count'),
      ).toBe('3');
    } finally {
      vi.useRealTimers();
    }
  });

  it('tracks one dirty flag across both panes', async () => {
    const { wrapper, saveGraph } = mountWith();
    await flushPromises();
    const vm = wrapper.vm as unknown as EditorVM;
    expect(vm.dirty).toBe(false);
    expect(wrapper.find('[data-testid="editor-dirty"]').exists()).toBe(false);

    // A canvas op dirties it…
    vm.applyCanvasOp({ type: 'add-node', kind: 'transform', position: { x: 0, y: 0 } });
    await flushPromises();
    expect(vm.dirty).toBe(true);
    expect(wrapper.find('[data-testid="editor-dirty"]').exists()).toBe(true);

    // …and the one save path clears it.
    await vm.save();
    await flushPromises();
    expect(saveGraph).toHaveBeenCalled();
    expect(vm.dirty).toBe(false);
  });

  it('a textarea edit dirties the same flag a canvas op does', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const vm = wrapper.vm as unknown as EditorVM;
    expect(vm.dirty).toBe(false);
    await wrapper.get('[data-testid="editor-yaml"]').setValue(`${BASE_YAML}\n`);
    await flushPromises();
    expect(vm.dirty).toBe(true);
  });

  it('routes an attribute-panel edit through the same buffer', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const vm = wrapper.vm as unknown as EditorVM;
    vm.applyCanvasOp({ type: 'set-attrs', id: 'a', attrs: { verbosity: 'verbose' } });
    await flushPromises();
    const text = textarea(wrapper).value;
    expect(text).toContain('verbosity: verbose');
    expect(text).not.toContain('verbosity: terse');
    expect(text).toContain('# a comment the canvas must not eat');
  });

  it('commits a legal edge into the buffer', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const canvas = wrapper.findComponent({ name: 'GraphCanvas' });
    await (
      canvas.vm as unknown as {
        onConnect: (c: Record<string, string>) => Promise<void>;
      }
    ).onConnect({
      source: 'a',
      target: 'b',
      sourceHandle: 'out',
      targetHandle: 'in',
    });
    await flushPromises();
    const text = textarea(wrapper).value;
    expect(text).toContain('edges:');
    expect(text).toContain('node: a');
    expect(text).toContain('node: b');
  });
});
