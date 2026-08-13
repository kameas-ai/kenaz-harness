/**
 * GraphEditor, materialized-run mode
 * (agentgraph-total-convergence-01PMGX01 WP12).
 *
 * The /agentgraph/run/:runId/graph route mounts this same editor, and it
 * must load the run through graph.materializeRun rather than
 * graph.loadGraph — a materialized run has no library file to read. The
 * route param is the whole trigger, which is why this file carries its
 * own vue-router mock (the sibling GraphEditor.test.ts pins `params.id`).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { GraphSpec } from '@/lib/types';

const pushMock = vi.fn();
vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { runId: 'chat-3' }, query: {} }),
  useRouter: () => ({ push: pushMock }),
}));

import GraphEditor from '@/views/agentgraph/GraphEditor.vue';

const materializedYAML = [
  'spec_version: "1"',
  'id: chat_default__run_chat-3',
  'name: Default chat graph — run chat-3',
  'entrypoints:',
  '  - history_in@1',
  'nodes:',
  '  - id: assistant_turn@1',
  '    kind: model',
  '    materialized:',
  '      source_node: assistant_turn',
  '      instance: 1',
  '      iteration: 1',
  '      status: completed',
  '  - id: tool_dispatch@1[toolu_01aa]',
  '    kind: tool_dispatch',
  '    materialized:',
  '      tool: kenaz__read_file',
  '      args_summary: "1 argument: path (string)"',
  '',
].join('\n');

function mountWith(spec?: Partial<GraphSpec>) {
  const materializeRun = vi.fn(async (runID: string) => ({
    id: 'chat_default__run_' + runID,
    name: 'Default chat graph — run ' + runID,
    scope: 'materialized' as const,
    yaml: materializedYAML,
    ...spec,
  }));
  const loadGraph = vi.fn(async (id: string) => ({
    id,
    scope: 'library' as const,
    yaml: '',
  }));
  const client = createFakeHarnessClient({
    graph: {
      listGraphs: async () => [],
      loadGraph,
      saveGraph: async () => undefined,
      deleteGraph: async () => undefined,
      validate: async () => ({ ok: true, issues: [] }),
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
      materializeRun,
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
  return { wrapper, materializeRun, loadGraph };
}

describe('GraphEditor — materialized run', () => {
  it('loads the run through materializeRun, not loadGraph', async () => {
    const { wrapper, materializeRun, loadGraph } = mountWith();
    await flushPromises();
    expect(materializeRun).toHaveBeenCalledWith('chat-3');
    expect(loadGraph).not.toHaveBeenCalled();
    const ta = wrapper.get('[data-testid="editor-yaml"]');
    expect((ta.element as HTMLTextAreaElement).value).toContain(
      'assistant_turn@1',
    );
    expect((ta.element as HTMLTextAreaElement).value).toContain(
      'tool_dispatch@1[toolu_01aa]',
    );
  });

  it('is read-only: no save control, and the materialized banner shows', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="editor-save"]').exists()).toBe(false);
    expect(
      wrapper.find('[data-testid="editor-materialized-banner"]').exists(),
    ).toBe(true);
    // The generic library banner must not double up.
    expect(wrapper.find('[data-testid="editor-readonly-banner"]').exists()).toBe(
      false,
    );
    const ta = wrapper.get('[data-testid="editor-yaml"]');
    expect(ta.attributes('readonly')).toBeDefined();
  });

  // Review finding F2: an evicted run is projected against the library
  // spec, which may describe a different topology than the one that
  // ran. The backend stamps spec_provenance; the viewer must say so.
  it('badges a degraded (library-fallback) projection', async () => {
    const { wrapper } = mountWith({
      yaml: materializedYAML + 'spec_provenance: library_fallback\n',
    });
    await flushPromises();
    const badge = wrapper.find('[data-testid="editor-materialized-degraded"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toContain('topology shown may differ');
  });

  it('does not badge a faithful projection', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(
      wrapper.find('[data-testid="editor-materialized-degraded"]').exists(),
    ).toBe(false);
  });

  it('tells the reader errors are classified, not reproduced', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const banner = wrapper.get('[data-testid="editor-materialized-banner"]');
    expect(banner.text()).toContain('errors are classified');
  });

  it('surfaces a projection failure instead of rendering an empty graph', async () => {
    const failing = vi.fn(async () => {
      throw new Error('agentgraph: run "chat-3" not found');
    });
    const client = createFakeHarnessClient({
      graph: {
        ...createFakeHarnessClient().graph,
        materializeRun: failing,
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
    await flushPromises();
    expect(wrapper.text()).toContain('not found');
  });
});
