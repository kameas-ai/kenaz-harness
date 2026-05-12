/**
 * ScheduledInbox.spec.ts
 *
 * Vitest unit tests for the scheduled-runs inbox component.
 * workflow-extensions-01KW2D3Y WP03.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ScheduledInbox from '../ScheduledInbox.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsScheduleEntry,
  type WorkflowsRunSummary,
} from '@/lib/workflowsClient';

// ── fixtures ───────────────────────────────────────────────────────────────

const SCHED_A: WorkflowsScheduleEntry = {
  workflowId: 'release_notes',
  cron: '0 9 * * 1',
  timezone: 'UTC',
  enabled: true,
};

const SCHED_B: WorkflowsScheduleEntry = {
  workflowId: 'nightly_clean',
  cron: '0 0 * * *',
  enabled: false,
};

const RUN_COMPLETED: WorkflowsRunSummary = {
  runId: 'run-abc',
  workflowId: 'release_notes',
  status: 'completed',
  startedAt: '2026-05-12T09:00:00.000Z',
  endedAt: '2026-05-12T09:02:30.000Z',
  scheduled: true,
};

const RUN_RUNNING: WorkflowsRunSummary = {
  runId: 'run-xyz',
  workflowId: 'release_notes',
  status: 'running',
  startedAt: '2026-05-12T09:05:00.000Z',
  scheduled: true,
};

const RUN_FAILED: WorkflowsRunSummary = {
  runId: 'run-fail',
  workflowId: 'release_notes',
  status: 'failed',
  startedAt: '2026-05-11T09:00:00.000Z',
  endedAt: '2026-05-11T09:00:10.000Z',
  error: 'step shell failed',
  scheduled: true,
};

// ── helpers ────────────────────────────────────────────────────────────────

function mountInbox(overrides: Parameters<typeof createFakeWorkflowsClient>[0] = {}) {
  const client = createFakeWorkflowsClient(overrides);
  const wrapper = mount(ScheduledInbox, { props: { client } });
  return { wrapper, client };
}

// ── tests ──────────────────────────────────────────────────────────────────

describe('ScheduledInbox', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('renders the inbox root', async () => {
    const { wrapper } = mountInbox();
    await flushPromises();
    expect(wrapper.find('[data-testid="scheduled-inbox"]').exists()).toBe(true);
  });

  it('shows empty state when no schedules exist', async () => {
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([]),
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="scheduled-inbox-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="scheduled-inbox-list"]').exists()).toBe(false);
  });

  it('shows a row per scheduled workflow', async () => {
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A, SCHED_B]),
      scheduleNextFire: vi.fn().mockResolvedValue(''),
    });
    await flushPromises();
    expect(wrapper.find(`[data-testid="scheduled-row-${SCHED_A.workflowId}"]`).exists()).toBe(true);
    expect(wrapper.find(`[data-testid="scheduled-row-${SCHED_B.workflowId}"]`).exists()).toBe(true);
  });

  it('shows load error when scheduleList rejects', async () => {
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockRejectedValue(new Error('network down')),
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="scheduled-inbox-error"]').text()).toContain('network down');
  });

  it('displays next-fire time when scheduleNextFire returns a value', async () => {
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A]),
      scheduleNextFire: vi.fn().mockResolvedValue('2026-05-19T09:00:00.000Z'),
    });
    await flushPromises();
    const nextFireEl = wrapper.find(`[data-testid="scheduled-next-fire-${SCHED_A.workflowId}"]`);
    expect(nextFireEl.exists()).toBe(true);
    // The text contains a localized date; just check 'Next:' prefix
    expect(nextFireEl.text()).toContain('Next:');
  });

  it('shows run history when an accordion row is expanded', async () => {
    const historyMock = vi.fn().mockResolvedValue([RUN_COMPLETED, RUN_FAILED]);
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A]),
      scheduleNextFire: vi.fn().mockResolvedValue(''),
      scheduleRunHistory: historyMock,
    });
    await flushPromises();

    // Click the accordion header to expand
    await wrapper.find(`[data-testid="scheduled-row-header-${SCHED_A.workflowId}"]`).trigger('click');
    await flushPromises();

    expect(historyMock).toHaveBeenCalledWith(SCHED_A.workflowId, 20);
    expect(wrapper.find(`[data-testid="scheduled-runs-${SCHED_A.workflowId}"]`).exists()).toBe(true);
    expect(wrapper.find(`[data-testid="scheduled-run-row-${RUN_COMPLETED.runId}"]`).exists()).toBe(true);
    expect(wrapper.find(`[data-testid="scheduled-run-row-${RUN_FAILED.runId}"]`).exists()).toBe(true);
  });

  it('collapses the row when the header is clicked a second time', async () => {
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A]),
      scheduleNextFire: vi.fn().mockResolvedValue(''),
      scheduleRunHistory: vi.fn().mockResolvedValue([RUN_COMPLETED]),
    });
    await flushPromises();

    const header = wrapper.find(`[data-testid="scheduled-row-header-${SCHED_A.workflowId}"]`);
    await header.trigger('click');
    await flushPromises();
    expect(wrapper.find(`[data-testid="scheduled-runs-${SCHED_A.workflowId}"]`).exists()).toBe(true);

    await header.trigger('click');
    await flushPromises();
    expect(wrapper.find(`[data-testid="scheduled-runs-${SCHED_A.workflowId}"]`).exists()).toBe(false);
  });

  it('shows "No runs yet." when history is empty', async () => {
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A]),
      scheduleNextFire: vi.fn().mockResolvedValue(''),
      scheduleRunHistory: vi.fn().mockResolvedValue([]),
    });
    await flushPromises();

    await wrapper.find(`[data-testid="scheduled-row-header-${SCHED_A.workflowId}"]`).trigger('click');
    await flushPromises();

    expect(wrapper.find(`[data-testid="scheduled-runs-empty-${SCHED_A.workflowId}"]`).exists()).toBe(true);
  });

  it('shows a cancel button only for running runs', async () => {
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A]),
      scheduleNextFire: vi.fn().mockResolvedValue(''),
      scheduleRunHistory: vi.fn().mockResolvedValue([RUN_RUNNING, RUN_COMPLETED]),
    });
    await flushPromises();

    await wrapper.find(`[data-testid="scheduled-row-header-${SCHED_A.workflowId}"]`).trigger('click');
    await flushPromises();

    // Running run has cancel button
    expect(wrapper.find(`[data-testid="scheduled-run-cancel-${RUN_RUNNING.runId}"]`).exists()).toBe(true);
    // Completed run does NOT have cancel button
    expect(wrapper.find(`[data-testid="scheduled-run-cancel-${RUN_COMPLETED.runId}"]`).exists()).toBe(false);
  });

  it('calls cancelRun when cancel button is clicked', async () => {
    const cancelMock = vi.fn().mockResolvedValue(undefined);
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A]),
      scheduleNextFire: vi.fn().mockResolvedValue(''),
      scheduleRunHistory: vi.fn().mockResolvedValue([RUN_RUNNING]),
      cancelRun: cancelMock,
    });
    await flushPromises();

    await wrapper.find(`[data-testid="scheduled-row-header-${SCHED_A.workflowId}"]`).trigger('click');
    await flushPromises();

    await wrapper.find(`[data-testid="scheduled-run-cancel-${RUN_RUNNING.runId}"]`).trigger('click');
    await flushPromises();

    expect(cancelMock).toHaveBeenCalledWith(RUN_RUNNING.runId);
  });

  it('calls runNow when "Run now" button is clicked on a row header', async () => {
    const runNowMock = vi.fn().mockResolvedValue({
      runId: 'new-run',
      workflowId: SCHED_A.workflowId,
      status: 'running',
      startedAt: new Date().toISOString(),
      scheduled: false,
    });
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A]),
      scheduleNextFire: vi.fn().mockResolvedValue(''),
      runNow: runNowMock,
    });
    await flushPromises();

    await wrapper.find(`[data-testid="scheduled-rerun-${SCHED_A.workflowId}"]`).trigger('click');
    await flushPromises();

    expect(runNowMock).toHaveBeenCalledWith(SCHED_A.workflowId);
  });

  it('shows action error when runNow fails', async () => {
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A]),
      scheduleNextFire: vi.fn().mockResolvedValue(''),
      runNow: vi.fn().mockRejectedValue(new Error('engine unavailable')),
    });
    await flushPromises();

    await wrapper.find(`[data-testid="scheduled-rerun-${SCHED_A.workflowId}"]`).trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="scheduled-inbox-action-error"]').text()).toContain('engine unavailable');
  });

  it('shows the failed run error text inline', async () => {
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A]),
      scheduleNextFire: vi.fn().mockResolvedValue(''),
      scheduleRunHistory: vi.fn().mockResolvedValue([RUN_FAILED]),
    });
    await flushPromises();

    await wrapper.find(`[data-testid="scheduled-row-header-${SCHED_A.workflowId}"]`).trigger('click');
    await flushPromises();

    const errEl = wrapper.find(`[data-testid="scheduled-run-error-${RUN_FAILED.runId}"]`);
    expect(errEl.exists()).toBe(true);
    expect(errEl.text()).toContain('step shell failed');
  });

  it('shows correct status class for run status', async () => {
    const { wrapper } = mountInbox({
      scheduleList: vi.fn().mockResolvedValue([SCHED_A]),
      scheduleNextFire: vi.fn().mockResolvedValue(''),
      scheduleRunHistory: vi.fn().mockResolvedValue([RUN_COMPLETED, RUN_RUNNING, RUN_FAILED]),
    });
    await flushPromises();

    await wrapper.find(`[data-testid="scheduled-row-header-${SCHED_A.workflowId}"]`).trigger('click');
    await flushPromises();

    const completedStatus = wrapper.find(`[data-testid="scheduled-run-status-${RUN_COMPLETED.runId}"]`);
    expect(completedStatus.classes()).toContain('text-emerald-400');

    const runningStatus = wrapper.find(`[data-testid="scheduled-run-status-${RUN_RUNNING.runId}"]`);
    expect(runningStatus.classes()).toContain('text-amber-400');

    const failedStatus = wrapper.find(`[data-testid="scheduled-run-status-${RUN_FAILED.runId}"]`);
    expect(failedStatus.classes()).toContain('text-red-400');
  });
});
