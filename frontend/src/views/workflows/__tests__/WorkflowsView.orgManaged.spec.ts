/**
 * skill-library-01SKLIB01 — closes docs/unwired-ledger.md 2026-10-06
 * conformance residual item 4 (UI half): an org-required (mandated)
 * workflow is read-only. The Library offers Run but no Edit / Edit on
 * canvas / Delete, and says why; the Schedules tab offers no Schedule /
 * Unschedule for it. The backend refuses those calls too
 * (core/rpc/views/workflows mandated_guard_test.go) — this pins that the UI
 * does not offer them. In-memory fake client, deliberately (WP-PI AC-PI-2):
 * pixels and calls only, no storage.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { reactive } from 'vue';
import WorkflowsView from '../WorkflowsView.vue';
import WorkflowSchedulesSection from '../WorkflowSchedulesSection.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsSummary,
  type WorkflowsWorkflow,
} from '@/lib/workflowsClient';

const mockRoute = reactive<{ query: Record<string, string | undefined> }>({ query: {} });
vi.mock('vue-router', () => ({ useRoute: () => mockRoute }));
vi.mock('@/shell/CanvasHead.vue', () => ({ default: { template: '<div />' } }));
vi.mock('@/views/catalog/PublishDialog.vue', () => ({ default: { template: '<div />' } }));

const orgFlow: WorkflowsSummary = {
  id: 'org-flow', name: 'Org flow', version: 1, stepCount: 1, source: 'user', orgManaged: true,
};
const mine: WorkflowsSummary = { id: 'mine', name: 'Mine', version: 1, stepCount: 1, source: 'user' };
const detail = (id: string): WorkflowsWorkflow => ({
  id, name: id, version: 1, inputs: [], steps: [{ name: 'a', kind: 'shell' }],
});

describe('org-required workflows are read-only', () => {
  it('Library: run only, no edit/delete, with the reason', async () => {
    const client = createFakeWorkflowsClient({
      list: () => Promise.resolve([orgFlow]),
      get: (id: string) => Promise.resolve(detail(id)),
    });
    const w = mount(WorkflowsView, { props: { client } });
    await flushPromises();
    expect(w.find('[data-testid="workflows-run-button"]').exists()).toBe(true);
    expect(w.find('[data-testid="workflows-edit-button"]').exists()).toBe(false);
    expect(w.find('[data-testid="workflows-edit-canvas-button"]').exists()).toBe(false);
    expect(w.find('[data-testid="workflows-delete-button"]').exists()).toBe(false);
    expect(w.get('[data-testid="workflows-org-managed-note"]').text()).toContain('Required by your org');
    expect(w.get('[data-testid="workflow-row-org-flow"]').text()).toContain('required by your org');
  });

  it('a user workflow keeps its edit and delete affordances', async () => {
    const client = createFakeWorkflowsClient({
      list: () => Promise.resolve([mine]),
      get: (id: string) => Promise.resolve(detail(id)),
    });
    const w = mount(WorkflowsView, { props: { client } });
    await flushPromises();
    expect(w.find('[data-testid="workflows-edit-button"]').exists()).toBe(true);
    expect(w.find('[data-testid="workflows-delete-button"]').exists()).toBe(true);
    expect(w.find('[data-testid="workflows-org-managed-note"]').exists()).toBe(false);
  });

  it('Schedules: no Schedule / Unschedule for the org workflow', async () => {
    const scheduleSet = vi.fn(() => Promise.resolve());
    const client = createFakeWorkflowsClient({
      list: () => Promise.resolve([orgFlow, mine]),
      scheduleList: () => Promise.resolve([{ workflowId: 'org-flow', cron: '0 7 * * *', timezone: 'UTC', enabled: true }]),
      scheduleSet,
    });
    const w = mount(WorkflowSchedulesSection, { props: { client } });
    await flushPromises();
    expect(w.find('[data-testid="wf-sched-set-org-flow"]').exists()).toBe(false);
    expect(w.find('[data-testid="wf-sched-clear-org-flow"]').exists()).toBe(false);
    expect(w.get('[data-testid="wf-sched-org-managed-org-flow"]').text()).toContain('Required by your org');
    // Its schedule is still shown.
    expect(w.get('[data-testid="wf-sched-badge-org-flow"]').text()).toContain('0 7 * * *');
    expect(w.find('[data-testid="wf-sched-set-mine"]').exists()).toBe(true);
  });
});
