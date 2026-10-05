/**
 * WorkflowsView.automationHome — nav-ia-sweep-01DOGF0F WP04 (FR-3), pins P-4
 * and P-6.
 *
 * Workflows became the automation home: Library | Schedules | Runs | Tasks,
 * URL-addressable via ?tab=. (The Catalog tab was retired by
 * install-framework-01DOGF0B WP05 — templates install from the "Add
 * capability" surface; ?tab=catalog redirects there.) The Schedules tab carries the workflow
 * cron editor that used to exist ONLY in Settings › Workflows
 * (WorkflowsSettingsPanel was the sole caller of scheduleSet/scheduleClear),
 * so the P-4 test below is the proof the capability survived the move.
 *
 * Uses a real memory router (not a mocked useRoute) so the ?tab= round trip
 * — URL → tab at mount, click → URL — is exercised end to end.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import WorkflowsView from '../WorkflowsView.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsClient,
  type WorkflowsScheduleEntry,
  type WorkflowsSummary,
  type WorkflowsWorkflow,
} from '@/lib/workflowsClient';
import { createFakeScheduledChatClient } from '@/lib/scheduledChatClient';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

vi.mock('@/shell/CanvasHead.vue', () => ({
  default: { template: '<div />' },
}));
vi.mock('@/views/marketplace/PublishDialog.vue', () => ({
  default: { template: '<div />' },
}));

const WF: WorkflowsSummary = {
  id: 'release_notes',
  name: 'Release notes',
  description: '',
  version: 3,
  stepCount: 2,
  source: 'user',
};
const WF_DETAIL: WorkflowsWorkflow = {
  id: 'release_notes',
  name: 'Release notes',
  version: 3,
  steps: [{ name: 'draft', kind: 'model_turn' }],
};

/** A client whose schedule store is real state, so set → list → clear round-trips. */
function statefulClient(initial: WorkflowsScheduleEntry[] = []) {
  let store = [...initial];
  const scheduleSet = vi.fn(async (inp: { workflowId: string; cron: string; timezone?: string }) => {
    store = store.filter((s) => s.workflowId !== inp.workflowId);
    store.push({ workflowId: inp.workflowId, cron: inp.cron, timezone: inp.timezone, enabled: true });
  });
  const scheduleClear = vi.fn(async (id: string) => {
    store = store.filter((s) => s.workflowId !== id);
  });
  const remove = vi.fn(async () => undefined);
  const client: WorkflowsClient = createFakeWorkflowsClient({
    list: async () => [WF],
    get: async () => WF_DETAIL,
    scheduleList: async () => [...store],
    scheduleSet: scheduleSet as unknown as WorkflowsClient['scheduleSet'],
    scheduleClear,
    remove,
  });
  return { client, scheduleSet, scheduleClear, remove };
}

async function mountAt(path: string, client: WorkflowsClient) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/workflows', component: defineComponent({ render: () => h('div') }) },
      { path: '/tools', component: defineComponent({ render: () => h('div') }) },
    ],
  });
  await router.push(path);
  await router.isReady();
  const w = mount(WorkflowsView, {
    props: { client, chatClient: createFakeScheduledChatClient() },
    global: {
      plugins: [router],
      provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() },
    },
  });
  await flushPromises();
  return { w, router };
}

describe('Workflows automation home (FR-3)', () => {
  it('offers Library | Schedules | Runs | Tasks (Catalog retired into Add capability)', async () => {
    const { w } = await mountAt('/workflows', statefulClient().client);
    const labels = w
      .find('[data-testid="workflows-tab-nav"]')
      .findAll('button')
      .map((b) => b.text());
    expect(labels).toEqual(['Library', 'Schedules', 'Runs', 'Tasks']);
  });

  // install-framework-01DOGF0B WP05: the retired Catalog tab's deep link and
  // the Library's browse affordance both land on the workflow kind of the
  // one install surface.
  it('?tab=catalog redirects to the Add-capability surface filtered to workflows', async () => {
    const { router } = await mountAt('/workflows?tab=catalog', statefulClient().client);
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/tools');
    expect(router.currentRoute.value.query.kind).toBe('workflow');
  });

  it('Browse workflow templates goes to the Add-capability surface', async () => {
    const { w, router } = await mountAt('/workflows', statefulClient().client);
    await w.get('[data-testid="workflows-browse-templates"]').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/tools');
    expect(router.currentRoute.value.query.kind).toBe('workflow');
  });

  it.each([
    ['tasks', 'workflows-tasks-tab'],
    ['schedules', 'workflows-schedules-tab'],
    ['runs', 'scheduled-inbox'],
  ])('?tab=%s opens that tab at mount', async (tab, testid) => {
    const { w } = await mountAt(`/workflows?tab=${tab}`, statefulClient().client);
    expect(w.find(`[data-testid="${testid}"]`).exists()).toBe(true);
    expect(w.find('[data-testid="workflows-new-button"]').exists()).toBe(false);
  });

  it('Tasks tab mounts the background-task list (moved from Settings › Runtime)', async () => {
    const { w } = await mountAt('/workflows?tab=tasks', statefulClient().client);
    expect(w.find('[data-testid="workflows-tasks-tab"]').exists()).toBe(true);
    expect(w.findComponent({ name: 'TasksPanel' }).exists()).toBe(true);
  });

  it('Schedules tab carries both workflow schedules and scheduled chats', async () => {
    const { w } = await mountAt('/workflows?tab=schedules', statefulClient().client);
    expect(w.find('[data-testid="workflow-schedules-section"]').exists()).toBe(true);
    expect(w.find('[data-testid="scheduled-chats-panel"]').exists()).toBe(true);
  });

  it('clicking a tab mirrors it into ?tab= (and Library clears it)', async () => {
    const { w, router } = await mountAt('/workflows?run=r1', statefulClient().client);
    await w.find('[data-testid="workflows-tab-tasks"]').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.query.tab).toBe('tasks');
    expect(router.currentRoute.value.query.run).toBeUndefined();
    await w.find('[data-testid="workflows-tab-library"]').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.query.tab).toBeUndefined();
  });
});

describe('?tab= is the source of truth for the active tab', () => {
  it('a bare /workflows (Back / rail click) resets to Library', async () => {
    const { w, router } = await mountAt('/workflows?tab=tasks', statefulClient().client);
    expect(w.find('[data-testid="workflows-tasks-tab"]').exists()).toBe(true);
    await router.push('/workflows');
    await flushPromises();
    expect(w.find('[data-testid="workflows-tasks-tab"]').exists()).toBe(false);
    expect(w.find('[data-testid="workflows-new-button"]').exists()).toBe(true);
  });

  it('leaving /workflows?run=x for a bare /workflows also resets to Library', async () => {
    const { w, router } = await mountAt('/workflows?run=r1', statefulClient().client);
    expect(w.find('[data-testid="scheduled-inbox"]').exists()).toBe(true);
    await router.push('/workflows');
    await flushPromises();
    expect(w.find('[data-testid="workflows-new-button"]').exists()).toBe(true);
  });

  it('Back from a tab click returns to the previous tab', async () => {
    const { w, router } = await mountAt('/workflows?tab=schedules', statefulClient().client);
    await router.push('/workflows?tab=tasks');
    await flushPromises();
    expect(w.find('[data-testid="workflows-tasks-tab"]').exists()).toBe(true);
    await router.push('/workflows?tab=schedules');
    await flushPromises();
    expect(w.find('[data-testid="workflows-schedules-tab"]').exists()).toBe(true);
  });
});

describe('WorkflowSchedulesSection states (ported from WorkflowsSettingsPanel.spec)', () => {
  it('shows the load error when list() rejects', async () => {
    const client = createFakeWorkflowsClient({
      list: vi.fn().mockRejectedValue(new Error('store offline')),
    });
    const { w } = await mountAt('/workflows?tab=schedules', client);
    expect(w.find('[data-testid="wf-sched-load-error"]').text()).toContain('store offline');
  });

  it('shows the empty state when no workflows are installed', async () => {
    const client = createFakeWorkflowsClient({ list: async () => [] });
    const { w } = await mountAt('/workflows?tab=schedules', client);
    expect(w.find('[data-testid="wf-sched-empty"]').exists()).toBe(true);
    expect(w.find('[data-testid="wf-sched-table"]').exists()).toBe(false);
  });
});

describe('P-4: workflow cron schedule survives the move out of Settings', () => {
  it('sets and clears a workflow cron schedule from Workflows › Schedules', async () => {
    const { client, scheduleSet, scheduleClear } = statefulClient();
    const { w } = await mountAt('/workflows?tab=schedules', client);

    expect(w.find(`[data-testid="wf-sched-badge-${WF.id}"]`).exists()).toBe(false);
    await w.find(`[data-testid="wf-sched-set-${WF.id}"]`).trigger('click');
    await w.find('[data-testid="wf-sched-cron-input"]').setValue('15 9 * * 1-5');
    await w.find('[data-testid="wf-sched-tz-input"]').setValue('Europe/London');
    await w.find('[data-testid="wf-sched-save"]').trigger('click');
    await flushPromises();

    expect(scheduleSet).toHaveBeenCalledWith({
      workflowId: WF.id,
      cron: '15 9 * * 1-5',
      timezone: 'Europe/London',
    });
    expect(w.find('[data-testid="wf-sched-modal"]').exists()).toBe(false);
    expect(w.find(`[data-testid="wf-sched-badge-${WF.id}"]`).text()).toContain('15 9 * * 1-5');
    expect(w.find(`[data-testid="wf-sched-set-${WF.id}"]`).text()).toBe('Reschedule');

    await w.find(`[data-testid="wf-sched-clear-${WF.id}"]`).trigger('click');
    await flushPromises();
    expect(scheduleClear).toHaveBeenCalledWith(WF.id);
    expect(w.find(`[data-testid="wf-sched-badge-${WF.id}"]`).exists()).toBe(false);
  });

  it('reschedule pre-fills the existing cron and timezone', async () => {
    const { client } = statefulClient([
      { workflowId: WF.id, cron: '0 6 * * *', timezone: 'Asia/Tokyo', enabled: true },
    ]);
    const { w } = await mountAt('/workflows?tab=schedules', client);
    await w.find(`[data-testid="wf-sched-set-${WF.id}"]`).trigger('click');
    expect((w.find('[data-testid="wf-sched-cron-input"]').element as HTMLInputElement).value).toBe('0 6 * * *');
    expect((w.find('[data-testid="wf-sched-tz-input"]').element as HTMLInputElement).value).toBe('Asia/Tokyo');
  });

  it('surfaces a scheduleSet failure in the modal instead of closing it', async () => {
    const { client } = statefulClient();
    client.scheduleSet = vi.fn().mockRejectedValue(new Error('bad cron'));
    const { w } = await mountAt('/workflows?tab=schedules', client);
    await w.find(`[data-testid="wf-sched-set-${WF.id}"]`).trigger('click');
    await w.find('[data-testid="wf-sched-save"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="wf-sched-error"]').text()).toContain('bad cron');
    expect(w.find('[data-testid="wf-sched-modal"]').exists()).toBe(true);
  });
});

describe('P-6: Library absorbs the Settings panel’s delete affordances', () => {
  it('delete asks first, warns about an active schedule, and cancel removes nothing', async () => {
    const { client, remove } = statefulClient([
      { workflowId: WF.id, cron: '0 7 * * *', timezone: 'UTC', enabled: true },
    ]);
    const { w } = await mountAt('/workflows', client);
    await w.find('[data-testid="workflows-delete-button"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="workflows-delete-confirm-dialog"]').exists()).toBe(true);
    expect(w.find('[data-testid="workflows-delete-schedule-warning"]').exists()).toBe(true);
    await w.find('[data-testid="workflows-delete-cancel"]').trigger('click');
    expect(remove).not.toHaveBeenCalled();
    expect(w.find('[data-testid="workflows-delete-confirm-dialog"]').exists()).toBe(false);
  });

  it('schedules are preloaded at mount, so the warning is decided before the dialog opens', async () => {
    const { client } = statefulClient([
      { workflowId: WF.id, cron: '0 7 * * *', timezone: 'UTC', enabled: true },
    ]);
    const scheduleList = vi.spyOn(client, 'scheduleList');
    const { w } = await mountAt('/workflows', client);
    expect(scheduleList).toHaveBeenCalled();
    // Click and inspect synchronously — no flush between open and check, the
    // shape of a fast confirm.
    await w.find('[data-testid="workflows-delete-button"]').trigger('click');
    expect(w.find('[data-testid="workflows-delete-schedule-warning"]').exists()).toBe(true);
  });

  it('no schedule warning for an unscheduled workflow; confirm deletes', async () => {
    const { client, remove } = statefulClient();
    const { w } = await mountAt('/workflows', client);
    await w.find('[data-testid="workflows-delete-button"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="workflows-delete-schedule-warning"]').exists()).toBe(false);
    await w.find('[data-testid="workflows-delete-confirm"]').trigger('click');
    await flushPromises();
    expect(remove).toHaveBeenCalledWith(WF.id);
  });

  it('Delete is offered for user-source workflows only (builtins 404 on delete)', async () => {
    const builtin = createFakeWorkflowsClient({
      list: async () => [{ ...WF, source: 'builtin' }],
      get: async () => WF_DETAIL,
    });
    const { w } = await mountAt('/workflows', builtin);
    expect(w.find('[data-testid="workflows-detail"]').exists()).toBe(true);
    expect(w.find('[data-testid="workflows-delete-button"]').exists()).toBe(false);
  });

  it('template, YAML and canvas editors are all reachable from Library › New', async () => {
    const { w } = await mountAt('/workflows', statefulClient().client);
    await w.find('[data-testid="workflows-new-button"]').trigger('click');
    for (const id of ['workflows-new-template', 'workflows-new-yaml', 'workflows-new-canvas']) {
      expect(w.find(`[data-testid="${id}"]`).exists(), id).toBe(true);
    }
    expect(w.find('[data-testid="workflows-edit-canvas-button"]').exists()).toBe(true);
  });
});
