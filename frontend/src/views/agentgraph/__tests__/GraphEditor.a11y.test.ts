/**
 * GraphEditor — axe-core accessibility assertions
 * (accessibility-audit-01KQ8TDA WP02)
 *
 * The workflow graph editor is a functional view (not a placeholder) with a
 * YAML textarea surface and a node-palette sidebar. We mount it and run axe.
 * color-contrast is disabled: happy-dom returns 0/0 ratio
 * (needs_manual_verification in a real browser).
 *
 * If the component renders an error or loading state rather than the full
 * editor, the test still validates the fallback surface is accessible.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { GraphSpec } from '@/lib/types';
import { axe } from 'vitest-axe';

const pushMock = vi.fn();

vi.mock('vue-router', () => ({
  useRoute: () => ({ params: { id: 'user_g' }, query: {} }),
  useRouter: () => ({ push: pushMock }),
}));

// Import AFTER mocking vue-router.
import GraphEditor from '@/views/agentgraph/GraphEditor.vue';

/** Axe options shared by all tests in this file. */
const axeOptions = {
  rules: {
    // happy-dom does not compute CSS, so color-contrast always reports 0/0.
    // needs_manual_verification: check contrast in dark + light themes with
    // a real browser.
    'color-contrast': { enabled: false },
    // region — component tree mounts without Shell landmark wrapper in tests.
    region: { enabled: false },
  },
};

const sampleSpec: GraphSpec = {
  id: 'user_g',
  scope: 'user' as const,
  yaml: 'spec_version: "1"\nid: user_g\nentrypoints: [a]\nnodes:\n  - id: a\n    kind: plan\n    attrs:\n      verbosity: terse\n',
};

function makeClient() {
  return createFakeHarnessClient({
    graph: {
      listGraphs: async () => [],
      loadGraph: vi.fn(async () => sampleSpec),
      saveGraph: vi.fn(async () => undefined),
      deleteGraph: async () => undefined,
      validate: vi.fn(async () => ({ ok: true, issues: [] })),
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
        nodesComplete: 1,
        llmTokens: 10,
        llmCalls: 1,
        toolCalls: 0,
        costUsd: 0.001,
      }),
      listRuns: async () => [],
    },
  });
}

describe('GraphEditor — a11y (axe-core)', () => {
  it('has no axe violations in the graph editor surface', async () => {
    const client = makeClient();
    const w = mount(GraphEditor, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const results = await axe(w.element, axeOptions);
    // @ts-expect-error — toHaveNoViolations added via test-setup.ts extend
    expect(results).toHaveNoViolations();
  });
});
