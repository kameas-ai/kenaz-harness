/**
 * WorkflowGraphEditor.spec.ts
 *
 * Vitest unit tests for the visual DAG canvas editor.
 * workflow-extensions-01KW2D3Y WP02.
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import WorkflowGraphEditor from '@/components/workflows/WorkflowGraphEditor.vue';
import type { WorkflowsWorkflow } from '@/lib/workflowsClient';

// ── fixtures ───────────────────────────────────────────────────────────────

const FAKE_WF: WorkflowsWorkflow = {
  id: 'my-wf',
  name: 'My Workflow',
  version: 1,
  steps: [
    { name: 'fetch', kind: 'http_request' },
    { name: 'parse', kind: 'transform' },
    { name: 'save', kind: 'write_artifact' },
  ],
};

// ── tests ──────────────────────────────────────────────────────────────────

describe('WorkflowGraphEditor', () => {
  it('renders the editor root', () => {
    const wrapper = mount(WorkflowGraphEditor);
    expect(wrapper.find('[data-testid="workflow-graph-editor"]').exists()).toBe(true);
  });

  it('shows the palette', () => {
    const wrapper = mount(WorkflowGraphEditor);
    expect(wrapper.find('[data-testid="wge-palette"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="wge-palette-model_turn"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="wge-palette-shell"]').exists()).toBe(true);
  });

  it('renders existing workflow nodes when initialised with a workflow', () => {
    const wrapper = mount(WorkflowGraphEditor, { props: { workflow: FAKE_WF } });
    expect(wrapper.find('[data-testid="wge-node-fetch"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="wge-node-parse"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="wge-node-save"]').exists()).toBe(true);
  });

  it('shows empty canvas placeholder when no workflow is supplied', () => {
    const wrapper = mount(WorkflowGraphEditor);
    expect(wrapper.find('[data-testid="wge-canvas"]').text()).toContain('Click a step kind');
  });

  it('adds a step when clicking a palette item', async () => {
    const wrapper = mount(WorkflowGraphEditor);
    await wrapper.find('[data-testid="wge-palette-shell"]').trigger('click');
    // Step node should appear in canvas
    const canvas = wrapper.find('[data-testid="wge-canvas"]');
    expect(canvas.find('svg').exists()).toBe(true);
  });

  it('pre-populates id and name fields from workflow prop', () => {
    const wrapper = mount(WorkflowGraphEditor, { props: { workflow: FAKE_WF } });
    const idInput = wrapper.find('[data-testid="wge-id"]') as ReturnType<typeof wrapper.find>;
    const nameInput = wrapper.find('[data-testid="wge-name"]') as ReturnType<typeof wrapper.find>;
    expect((idInput.element as HTMLInputElement).value).toBe('my-wf');
    expect((nameInput.element as HTMLInputElement).value).toBe('My Workflow');
  });

  it('shows YAML preview when toggle is clicked', async () => {
    const wrapper = mount(WorkflowGraphEditor, { props: { workflow: FAKE_WF } });
    expect(wrapper.find('[data-testid="wge-yaml-preview"]').exists()).toBe(false);
    await wrapper.find('[data-testid="wge-yaml-toggle"]').trigger('click');
    expect(wrapper.find('[data-testid="wge-yaml-preview"]').exists()).toBe(true);
    // YAML should contain step names
    const yaml = wrapper.find('[data-testid="wge-yaml-preview"]').text();
    expect(yaml).toContain('fetch');
    expect(yaml).toContain('http_request');
  });

  it('emits cancel when cancel button clicked', async () => {
    const wrapper = mount(WorkflowGraphEditor, { props: { workflow: FAKE_WF } });
    await wrapper.find('[data-testid="wge-cancel"]').trigger('click');
    expect(wrapper.emitted('cancel')).toBeTruthy();
  });

  it('emits save with structured workflow when save button clicked on valid form', async () => {
    const wrapper = mount(WorkflowGraphEditor, { props: { workflow: FAKE_WF } });
    await wrapper.find('[data-testid="wge-save"]').trigger('click');
    expect(wrapper.emitted('save')).toBeTruthy();
    const emitted = wrapper.emitted('save')![0][0] as WorkflowsWorkflow;
    expect(emitted.id).toBe('my-wf');
    expect(emitted.steps).toHaveLength(3);
  });

  it('blocks save and shows errors when required fields are missing', async () => {
    const wrapper = mount(WorkflowGraphEditor);
    // Save button should be disabled (no id, name, steps)
    const saveBtn = wrapper.find('[data-testid="wge-save"]');
    expect((saveBtn.element as HTMLButtonElement).disabled).toBe(true);
    expect(wrapper.find('[data-testid="wge-errors"]').exists()).toBe(true);
  });

  it('shows step config form when a node is selected', async () => {
    const wrapper = mount(WorkflowGraphEditor, { props: { workflow: FAKE_WF } });
    await wrapper.find('[data-testid="wge-node-fetch"]').trigger('click');
    expect(wrapper.find('[data-testid="wge-step-name"]').exists()).toBe(true);
    // http_request step shows URL field
    expect(wrapper.find('[data-testid="wge-step-url"]').exists()).toBe(true);
  });

  it('does not allow editing in readonly mode', async () => {
    const wrapper = mount(WorkflowGraphEditor, {
      props: { workflow: FAKE_WF, readonly: true },
    });
    expect(wrapper.find('[data-testid="wge-save"]').exists()).toBe(false);
    expect((wrapper.find('[data-testid="wge-id"]').element as HTMLInputElement).disabled).toBe(true);
  });

  it('removes a step when the delete button is clicked', async () => {
    const wrapper = mount(WorkflowGraphEditor, { props: { workflow: FAKE_WF } });
    await flushPromises();
    await wrapper.find('[data-testid="wge-node-delete-fetch"]').trigger('click');
    expect(wrapper.find('[data-testid="wge-node-fetch"]').exists()).toBe(false);
    // Remaining steps still present
    expect(wrapper.find('[data-testid="wge-node-parse"]').exists()).toBe(true);
  });
});
