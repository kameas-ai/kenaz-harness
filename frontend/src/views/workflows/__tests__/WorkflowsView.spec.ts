/**
 * WorkflowsView.spec.ts — covers the v0.3.0-beta surface:
 *
 *   1. catalog renders, first row auto-selects, details show steps
 *   2. clicking a row in the catalog re-fetches the detail
 *   3. Run button invokes client.run with the seeded inputs and
 *      renders the per-step transcript
 *   4. load failure surfaces an error banner
 *   5. Runs tab mounts ScheduledInbox (WP03)
 */

import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import WorkflowsView from '../WorkflowsView.vue';

// ScheduledInbox is NOT stubbed here — the real component mounts fine with
// the fake client's default scheduleList() → [] stub.  We just verify the
// inbox root element appears when the Runs tab is active.
import {
  createFakeWorkflowsClient,
  type WorkflowsClient,
  type WorkflowsSummary,
  type WorkflowsWorkflow,
  type WorkflowsRunResult,
  type WorkflowsSaveInput,
} from '@/lib/workflowsClient';

// CanvasHead pulls in design-token CSS. Stub it so the test stays
// focused on the view's behaviour and doesn't depend on shell setup.
vi.mock('@/shell/CanvasHead.vue', () => ({
  default: { template: '<div data-testid="canvas-head-stub" />' },
}));

// PublishDialog (WP03) calls useHarnessClient() internally. The existing
// WorkflowsView tests don't provide a HarnessClient injection, so stub
// PublishDialog here to avoid "called outside of a HarnessClient provider"
// errors in tests that focus on the workflow-run functionality.
vi.mock('@/views/marketplace/PublishDialog.vue', () => ({
  default: { template: '<div />' },
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

  it('saves an imported workflow and refreshes the catalog (WP07)', async () => {
    // Two-stage list seam: first call returns just the builtin; after
    // save the catalog has gained the imported user workflow.
    let calls = 0;
    const importedSummary: WorkflowsSummary = {
      id: 'wf-imported',
      name: 'Imported flow',
      version: 1,
      stepCount: 1,
      source: 'user',
    };
    const importedDetail: WorkflowsWorkflow = {
      id: 'wf-imported',
      name: 'Imported flow',
      version: 1,
      steps: [{ name: 'a', kind: 'shell', cmd: 'echo', args: ['hi'] }],
    };
    const list = vi.fn(() => {
      calls += 1;
      return Promise.resolve(calls === 1 ? [summary] : [summary, importedSummary]);
    });
    const get = vi.fn((id: string) =>
      Promise.resolve(id === 'wf-imported' ? importedDetail : detail),
    );
    const save = vi.fn(() =>
      Promise.resolve({
        id: 'wf-imported',
        name: 'Imported flow',
        version: 1,
        hash: 'h',
        yaml: 'id: imported\n',
        createdAt: '1970-01-01T00:00:00.000Z',
        updatedAt: '1970-01-01T00:00:00.000Z',
      }),
    );
    const wrapper = mount(WorkflowsView, {
      props: { client: fakeClient({ list, get, save }) },
    });
    await flushPromises();

    // Drive the import flow through the exposed test seam. The shim
    // is what production WP09 will replace with the real editor.
    await (wrapper.vm as unknown as {
      importFromYaml: (yaml: string) => Promise<void>;
    }).importFromYaml('id: imported\n');
    await flushPromises();

    expect(save).toHaveBeenCalledWith({ yaml: 'id: imported\n' });
    // Catalog refreshed (List called again post-save).
    expect(list).toHaveBeenCalledTimes(2);
    // UI reflects the new row.
    expect(
      wrapper.find('[data-testid="workflow-row-wf-imported"]').exists(),
    ).toBe(true);
  });

  it('deletes the selected workflow and refreshes the catalog (WP07)', async () => {
    let calls = 0;
    const list = vi.fn(() => {
      calls += 1;
      // First List returns the builtin; second List (post-delete)
      // returns an empty catalog so the UI flips to the empty state.
      return Promise.resolve(calls === 1 ? [summary] : []);
    });
    const remove = vi.fn(() => Promise.resolve());
    const wrapper = mount(WorkflowsView, {
      props: { client: fakeClient({ list, remove }) },
    });
    await flushPromises();

    await wrapper.find('[data-testid="workflows-delete-button"]').trigger('click');
    await flushPromises();

    expect(remove).toHaveBeenCalledWith('plan_implement_review');
    expect(list).toHaveBeenCalledTimes(2);
    // The catalog row is gone; the empty-state copy appears.
    expect(
      wrapper.find('[data-testid="workflow-row-plan_implement_review"]').exists(),
    ).toBe(false);
    expect(wrapper.text()).toContain('No workflows installed yet');
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

  it('shows RunsHistoryTab with execution history + scheduled section when Runs tab is active (01NBUG04)', async () => {
    const wrapper = mount(WorkflowsView, {
      props: { client: fakeClient() },
    });
    await flushPromises();

    // Switch to Runs tab
    await wrapper.find('[data-testid="workflows-tab-runs"]').trigger('click');
    await flushPromises();

    // RunsHistoryTab root is rendered
    expect(wrapper.find('[data-testid="runs-history-tab"]').exists()).toBe(true);
    // Execution history section present with empty state
    expect(wrapper.find('[data-testid="runs-history-section"]').exists()).toBe(true);
    // Scheduled subsection present (wraps ScheduledInbox)
    expect(wrapper.find('[data-testid="runs-scheduled-section"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="scheduled-inbox"]').exists()).toBe(true);
    // Library tab content is hidden
    expect(wrapper.find('[data-testid="workflows-catalog"]').exists()).toBe(false);
  });
});

/*
 * The canvas editor's MOUNTED PATH
 * (visual-graph-authoring-01PMUX01 WP06, FR-006).
 *
 * `WorkflowGraphEditor` was rebuilt on the shared canvas and then mounted
 * by nothing: this view offered the template editor and the YAML editor
 * only, so "workflows render on the shared canvas" was true of the code
 * and false of the running app. Component tests could not catch that —
 * they mount the editor directly, which is exactly the thing the app was
 * not doing. These tests drive the view.
 */
describe('WorkflowsView — the canvas editor is reachable', () => {
  async function openCanvas() {
    const wrapper = mount(WorkflowsView, { props: { client: fakeClient() } });
    await flushPromises();
    await wrapper.get('[data-testid="workflows-edit-canvas-button"]').trigger('click');
    await flushPromises();
    await new Promise((r) => setTimeout(r, 0));
    await flushPromises();
    return wrapper;
  }

  it('mounts the canvas from the detail pane, showing the workflow steps', async () => {
    const wrapper = await openCanvas();
    expect(wrapper.find('[data-testid="workflows-editor-slot-canvas"]').exists()).toBe(
      true,
    );
    // The SHARED canvas component, with one node per step.
    const canvas = wrapper.get('[data-testid="graph-canvas"]');
    expect(canvas.attributes('data-node-count')).toBe('4');
    const labels = wrapper
      .findAll('[data-testid^="canvas-node-"]')
      .map((w) => w.findAll('span')[0]?.text());
    expect(labels).toEqual(['plan', 'research', 'implement', 'review']);
  });

  it('offers a blank canvas from the New menu', async () => {
    const wrapper = mount(WorkflowsView, { props: { client: fakeClient() } });
    await flushPromises();
    await wrapper.get('[data-testid="workflows-new-button"]').trigger('click');
    await wrapper.get('[data-testid="workflows-new-canvas"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="workflows-editor-slot-canvas"]').exists()).toBe(
      true,
    );
    expect(wrapper.get('[data-testid="graph-canvas"]').attributes('data-node-count')).toBe(
      '0',
    );
  });

  it('toggles between the YAML and canvas panes on one workflow', async () => {
    const wrapper = await openCanvas();
    expect(wrapper.find('[data-testid="workflows-editor-pane-toggle"]').exists()).toBe(
      true,
    );
    await wrapper.get('[data-testid="workflows-editor-pane-yaml"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="workflows-editor-slot-yaml"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="workflows-editor-slot-canvas"]').exists()).toBe(
      false,
    );
    await wrapper.get('[data-testid="workflows-editor-pane-canvas"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="workflows-editor-slot-canvas"]').exists()).toBe(
      true,
    );
  });

  /*
   * The canvas saves through the EXISTING structured save path — the same
   * `client.save({workflow})` call the pre-canvas editor's emit was always
   * destined for. A canvas that persisted some other way would be a second
   * way to write a workflow, which is what this mission is against.
   */
  it('saves through the existing structured save path and returns to the catalog', async () => {
    const saveSpy = vi.fn((_input: WorkflowsSaveInput) =>
      Promise.resolve({
        id: 'plan_implement_review',
        name: 'x',
        version: 2,
        hash: 'h',
        yaml: '',
        createdAt: '',
        updatedAt: '',
      }),
    );
    const wrapper = mount(WorkflowsView, {
      props: { client: fakeClient({ save: saveSpy }) },
    });
    await flushPromises();
    await wrapper.get('[data-testid="workflows-edit-canvas-button"]').trigger('click');
    await flushPromises();
    await wrapper.get('[data-testid="wge-save"]').trigger('click');
    await flushPromises();

    expect(saveSpy).toHaveBeenCalledTimes(1);
    const arg = saveSpy.mock.calls[0][0];
    expect(arg.yaml).toBeUndefined();
    expect(arg.workflow?.id).toBe('plan_implement_review');
    expect(arg.workflow?.steps.map((s) => s.name)).toEqual([
      'plan',
      'research',
      'implement',
      'review',
    ]);
    // Saved ⇒ editor closes and the catalog is back.
    expect(wrapper.find('[data-testid="workflows-editor-slot-canvas"]').exists()).toBe(
      false,
    );
  });

  it('surfaces a save failure instead of closing the editor', async () => {
    const wrapper = mount(WorkflowsView, {
      props: {
        client: fakeClient({
          save: () => Promise.reject(new Error('cedar denied shell workflow')),
        }),
      },
    });
    await flushPromises();
    await wrapper.get('[data-testid="workflows-edit-canvas-button"]').trigger('click');
    await flushPromises();
    await wrapper.get('[data-testid="wge-save"]').trigger('click');
    await flushPromises();
    expect(wrapper.text()).toContain('cedar denied shell workflow');
    expect(wrapper.find('[data-testid="workflows-editor-slot-canvas"]').exists()).toBe(
      true,
    );
  });
});
