/**
 * WorkflowsSettingsPanel.spec.ts
 *
 * Vitest unit tests for Settings → Workflows panel.
 * workflow-extensions-01KW2D3Y WP02.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import WorkflowsSettingsPanel from '@/views/settings/WorkflowsSettingsPanel.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsSummary,
} from '@/lib/workflowsClient';

// ── fixtures ────────────────────────────────────────────────────────────────

const FAKE_WF: WorkflowsSummary = {
  id: 'release_notes',
  name: 'Release notes',
  description: 'Summarise commits',
  version: 3,
  stepCount: 2,
  source: 'user',
};

const FAKE_WF_BUILTIN: WorkflowsSummary = {
  id: 'plan_implement_review',
  name: 'Plan → Implement → Review',
  version: 1,
  stepCount: 3,
  source: 'builtin',
};

// ── helpers ────────────────────────────────────────────────────────────────

function mountPanel(catalogList: WorkflowsSummary[] = [FAKE_WF]) {
  const client = createFakeWorkflowsClient({
    list: vi.fn().mockResolvedValue(catalogList),
  });
  const wrapper = mount(WorkflowsSettingsPanel, {
    props: { client },
    global: {
      stubs: {
        // Stub sub-editors so we don't need their full dependency trees.
        SimpleTemplateEditor: { template: '<div data-testid="stub-template-editor" />' },
        WorkflowGraphEditor: { template: '<div data-testid="stub-graph-editor" />' },
      },
    },
  });
  return { wrapper, client };
}

// ── tests ──────────────────────────────────────────────────────────────────

describe('WorkflowsSettingsPanel', () => {
  it('renders the panel root', async () => {
    const { wrapper } = mountPanel();
    await flushPromises();
    expect(wrapper.find('[data-testid="workflows-settings-panel"]').exists()).toBe(true);
  });

  it('shows a table row per workflow after load', async () => {
    const { wrapper } = mountPanel([FAKE_WF, FAKE_WF_BUILTIN]);
    await flushPromises();
    expect(wrapper.find('[data-testid="wsp-table"]').exists()).toBe(true);
    expect(wrapper.find(`[data-testid="wsp-row-${FAKE_WF.id}"]`).exists()).toBe(true);
    expect(wrapper.find(`[data-testid="wsp-row-${FAKE_WF_BUILTIN.id}"]`).exists()).toBe(true);
  });

  it('shows empty state when catalog is empty', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    expect(wrapper.find('[data-testid="wsp-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="wsp-table"]').exists()).toBe(false);
  });

  it('shows load error when list() rejects', async () => {
    const client = createFakeWorkflowsClient({
      list: vi.fn().mockRejectedValue(new Error('network failure')),
    });
    const wrapper = mount(WorkflowsSettingsPanel, {
      props: { client },
      global: {
        stubs: {
          SimpleTemplateEditor: { template: '<div />' },
          WorkflowGraphEditor: { template: '<div />' },
        },
      },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="wsp-load-error"]').text()).toContain('network failure');
  });

  it('opens the template editor on "From template" click', async () => {
    const { wrapper } = mountPanel();
    await flushPromises();
    await wrapper.find('[data-testid="wsp-new-button"]').trigger('click');
    await wrapper.find('[data-testid="wsp-new-template"]').trigger('click');
    expect(wrapper.find('[data-testid="wsp-editor-template"]').exists()).toBe(true);
  });

  it('opens the graph editor on "Visual editor" click', async () => {
    const { wrapper } = mountPanel();
    await flushPromises();
    await wrapper.find('[data-testid="wsp-new-button"]').trigger('click');
    await wrapper.find('[data-testid="wsp-new-graph"]').trigger('click');
    expect(wrapper.find('[data-testid="wsp-editor-graph"]').exists()).toBe(true);
  });

  it('shows delete confirmation dialog for user-source workflows', async () => {
    const { wrapper } = mountPanel([FAKE_WF]);
    await flushPromises();
    await wrapper.find(`[data-testid="wsp-delete-${FAKE_WF.id}"]`).trigger('click');
    expect(wrapper.find('[data-testid="wsp-delete-dialog"]').exists()).toBe(true);
  });

  it('does NOT show delete button for builtin-source workflows', async () => {
    const { wrapper } = mountPanel([FAKE_WF_BUILTIN]);
    await flushPromises();
    expect(wrapper.find(`[data-testid="wsp-delete-${FAKE_WF_BUILTIN.id}"]`).exists()).toBe(false);
  });

  it('calls client.remove on delete confirmation', async () => {
    const removeMock = vi.fn().mockResolvedValue(undefined);
    const client = createFakeWorkflowsClient({
      list: vi.fn().mockResolvedValue([FAKE_WF]),
      remove: removeMock,
    });
    const wrapper = mount(WorkflowsSettingsPanel, {
      props: { client },
      global: {
        stubs: {
          SimpleTemplateEditor: { template: '<div />' },
          WorkflowGraphEditor: { template: '<div />' },
        },
      },
    });
    await flushPromises();
    await wrapper.find(`[data-testid="wsp-delete-${FAKE_WF.id}"]`).trigger('click');
    await wrapper.find('[data-testid="wsp-delete-confirm"]').trigger('click');
    await flushPromises();
    expect(removeMock).toHaveBeenCalledWith(FAKE_WF.id);
  });

  it('cancels delete without removing', async () => {
    const removeMock = vi.fn();
    const client = createFakeWorkflowsClient({
      list: vi.fn().mockResolvedValue([FAKE_WF]),
      remove: removeMock,
    });
    const wrapper = mount(WorkflowsSettingsPanel, {
      props: { client },
      global: {
        stubs: {
          SimpleTemplateEditor: { template: '<div />' },
          WorkflowGraphEditor: { template: '<div />' },
        },
      },
    });
    await flushPromises();
    await wrapper.find(`[data-testid="wsp-delete-${FAKE_WF.id}"]`).trigger('click');
    await wrapper.find('[data-testid="wsp-delete-cancel"]').trigger('click');
    expect(wrapper.find('[data-testid="wsp-delete-dialog"]').exists()).toBe(false);
    expect(removeMock).not.toHaveBeenCalled();
  });

  it('opens schedule modal on Schedule click', async () => {
    const { wrapper } = mountPanel([FAKE_WF]);
    await flushPromises();
    await wrapper.find(`[data-testid="wsp-schedule-${FAKE_WF.id}"]`).trigger('click');
    expect(wrapper.find('[data-testid="wsp-schedule-modal"]').exists()).toBe(true);
  });

  it('calls scheduleSet on save schedule', async () => {
    const scheduleSetMock = vi.fn().mockResolvedValue(undefined);
    const client = createFakeWorkflowsClient({
      list: vi.fn().mockResolvedValue([FAKE_WF]),
      scheduleSet: scheduleSetMock,
    });
    const wrapper = mount(WorkflowsSettingsPanel, {
      props: { client },
      global: {
        stubs: {
          SimpleTemplateEditor: { template: '<div />' },
          WorkflowGraphEditor: { template: '<div />' },
        },
      },
    });
    await flushPromises();
    await wrapper.find(`[data-testid="wsp-schedule-${FAKE_WF.id}"]`).trigger('click');
    await wrapper.find('[data-testid="wsp-schedule-save"]').trigger('click');
    await flushPromises();
    expect(scheduleSetMock).toHaveBeenCalledWith(expect.objectContaining({
      workflowId: FAKE_WF.id,
    }));
  });
});
