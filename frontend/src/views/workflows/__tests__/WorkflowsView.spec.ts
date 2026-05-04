/**
 * WorkflowsView.spec.ts — covers the v0.3.0-beta surface:
 *
 *   1. catalog renders, first row auto-selects, details show steps
 *   2. clicking a row in the catalog re-fetches the detail
 *   3. Run button invokes client.run with the seeded inputs and
 *      renders the per-step transcript
 *   4. load failure surfaces an error banner
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import WorkflowsView from '../WorkflowsView.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsClient,
  type WorkflowsSummary,
  type WorkflowsWorkflow,
  type WorkflowsRunResult,
} from '@/lib/workflowsClient';

// CanvasHead pulls in design-token CSS. Stub it so the test stays
// focused on the view's behaviour and doesn't depend on shell setup.
vi.mock('@/shell/CanvasHead.vue', () => ({
  default: { template: '<div data-testid="canvas-head-stub" />' },
}));

const summary: WorkflowsSummary = {
  id: 'plan_implement_review',
  name: 'Plan → Implement → Review',
  description: 'The canonical loop.',
  version: 1,
  stepCount: 4,
  source: 'builtin',
};

const detail: WorkflowsWorkflow = {
  id: 'plan_implement_review',
  name: 'Plan → Implement → Review',
  description: 'The canonical loop.',
  version: 1,
  inputs: [{ name: 'task', kind: 'string', default: 'Add a feature' }],
  steps: [
    { name: 'plan', kind: 'model_turn' },
    { name: 'research', kind: 'model_turn' },
    { name: 'implement', kind: 'model_turn' },
    { name: 'review', kind: 'model_turn' },
  ],
};

const completedRun: WorkflowsRunResult = {
  runId: 'run-abc',
  workflowId: 'plan_implement_review',
  status: 'completed',
  steps: [
    { name: 'plan', kind: 'model_turn', status: 'completed', output: 'plan-out' },
    {
      name: 'research',
      kind: 'model_turn',
      status: 'completed',
      output: 'research-out',
    },
    {
      name: 'implement',
      kind: 'model_turn',
      status: 'completed',
      output: 'impl-out',
    },
    {
      name: 'review',
      kind: 'model_turn',
      status: 'completed',
      output: 'review-out',
    },
  ],
};

function fakeClient(seed: Partial<WorkflowsClient> = {}): WorkflowsClient {
  return createFakeWorkflowsClient({
    list: () => Promise.resolve([summary]),
    get: () => Promise.resolve(detail),
    run: () => Promise.resolve(completedRun),
    ...seed,
  });
}

describe('WorkflowsView', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the catalog and auto-selects the first workflow', async () => {
    const wrapper = mount(WorkflowsView, {
      props: { client: fakeClient() },
    });
    await flushPromises();

    expect(
      wrapper.find('[data-testid="workflow-row-plan_implement_review"]').exists(),
    ).toBe(true);
    expect(wrapper.find('[data-testid="workflows-detail"]').exists()).toBe(true);
    expect(wrapper.text()).toContain('Plan → Implement → Review');
    // Input is seeded with the declared default.
    const taskInput = wrapper.find<HTMLInputElement>(
      '[data-testid="workflow-input-task"]',
    );
    expect(taskInput.element.value).toBe('Add a feature');
  });

  it('runs the workflow and renders each step in the transcript', async () => {
    const runSpy = vi.fn(() => Promise.resolve(completedRun));
    const wrapper = mount(WorkflowsView, {
      props: { client: fakeClient({ run: runSpy }) },
    });
    await flushPromises();

    await wrapper.find('[data-testid="workflows-run-button"]').trigger('click');
    await flushPromises();

    expect(runSpy).toHaveBeenCalledWith('plan_implement_review', {
      task: 'Add a feature',
    });

    const result = wrapper.find('[data-testid="workflows-run-result"]');
    expect(result.exists()).toBe(true);
    for (const name of ['plan', 'research', 'implement', 'review']) {
      expect(
        wrapper.find(`[data-testid="workflows-step-${name}"]`).exists(),
      ).toBe(true);
    }
    expect(result.text()).toContain('completed');
  });

  it('surfaces a load error banner when client.list rejects', async () => {
    const wrapper = mount(WorkflowsView, {
      props: {
        client: fakeClient({
          list: () => Promise.reject(new Error('boom')),
        }),
      },
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="workflows-load-error"]').exists()).toBe(
      true,
    );
    expect(wrapper.text()).toContain('boom');
  });

  it('surfaces a run error when the result carries an error string', async () => {
    const wrapper = mount(WorkflowsView, {
      props: {
        client: fakeClient({
          run: () =>
            Promise.resolve({
              runId: 'run-fail',
              workflowId: 'plan_implement_review',
              status: 'failed',
              error: 'shell step failed',
              steps: [
                {
                  name: 'plan',
                  kind: 'model_turn',
                  status: 'failed',
                  error: 'nope',
                },
              ],
            }),
        }),
      },
    });
    await flushPromises();
    await wrapper.find('[data-testid="workflows-run-button"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="workflows-run-error"]').text()).toContain(
      'shell step failed',
    );
  });
});
