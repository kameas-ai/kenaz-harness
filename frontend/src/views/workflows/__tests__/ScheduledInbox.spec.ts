/**
 * ScheduledInbox.spec.ts
 *
 * Vitest unit tests for the scheduled-runs inbox component.
 * workflow-extensions-01KW2D3Y WP03.
 * scheduled-chat-runs-01KX5R8B WP06 — Chat Runs section tests appended.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ScheduledInbox from '../ScheduledInbox.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsScheduleEntry,
  type WorkflowsRunSummary,
} from '@/lib/workflowsClient';
import {
  createFakeScheduledChatClient,
  type ScheduledChatEntry,
  type ScheduledChatRunSummary,
} from '@/lib/scheduledChatClient';

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
    expect(completedStatus.classes()).toContain('text-signal-ok');

    const runningStatus = wrapper.find(`[data-testid="scheduled-run-status-${RUN_RUNNING.runId}"]`);
    expect(runningStatus.classes()).toContain('text-signal-warn');

    const failedStatus = wrapper.find(`[data-testid="scheduled-run-status-${RUN_FAILED.runId}"]`);
    expect(failedStatus.classes()).toContain('text-signal-danger');
  });
});

// ── Chat Runs section (scheduled-chat-runs-01KX5R8B WP06) ─────────────────

const CHAT_ENTRY_A: ScheduledChatEntry = {
  id: 'chat-1',
  name: 'Daily briefing',
  promptTemplate: 'Summarize {{date}}.',
  cron: '0 9 * * *',
  timezone: 'America/New_York',
  outputSink: 'banner',
  enabled: true,
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
};

const CHAT_ENTRY_DISABLED: ScheduledChatEntry = {
  ...CHAT_ENTRY_A,
  id: 'chat-2',
  name: 'Weekly report',
  enabled: false,
};

const CHAT_HIST_COMPLETED: ScheduledChatRunSummary = {
  id: 'hist-1',
  chatRunId: 'chat-1',
  status: 'completed',
  startedAt: '2026-05-12T09:00:00.000Z',
  endedAt: '2026-05-12T09:00:05.000Z',
  outputSnippet: 'Today you have 3 meetings.',
};

const CHAT_HIST_FAILED: ScheduledChatRunSummary = {
  id: 'hist-2',
  chatRunId: 'chat-1',
  status: 'failed',
  startedAt: '2026-05-11T09:00:00.000Z',
  error: 'model timeout',
};

function mountWithChat(
  chatOverrides: Parameters<typeof createFakeScheduledChatClient>[0] = {},
  workflowOverrides: Parameters<typeof createFakeWorkflowsClient>[0] = {},
) {
  const client = createFakeWorkflowsClient({
    scheduleList: vi.fn().mockResolvedValue([]),
    ...workflowOverrides,
  });
  const chatClient = createFakeScheduledChatClient(chatOverrides);
  const wrapper = mount(ScheduledInbox, {
    props: { client, chatClient },
  });
  return { wrapper, client, chatClient };
}

describe('ScheduledInbox — Chat Runs section', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('does not render the chat section when chatClient prop is absent', async () => {
    const client = createFakeWorkflowsClient({
      scheduleList: vi.fn().mockResolvedValue([]),
    });
    const wrapper = mount(ScheduledInbox, { props: { client } });
    await flushPromises();
    expect(wrapper.find('[data-testid="chat-runs-section"]').exists()).toBe(false);
  });

  it('renders the chat section heading when chatClient is provided', async () => {
    const { wrapper } = mountWithChat();
    await flushPromises();
    expect(wrapper.find('[data-testid="chat-runs-section"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="chat-runs-heading"]').text()).toContain('Chat Runs');
  });

  it('shows empty state when no chat entries exist', async () => {
    const { wrapper } = mountWithChat({ list: vi.fn().mockResolvedValue([]) });
    await flushPromises();
    expect(wrapper.find('[data-testid="chat-runs-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="chat-runs-list"]').exists()).toBe(false);
  });

  it('shows a row per chat entry', async () => {
    const { wrapper } = mountWithChat({
      list: vi.fn().mockResolvedValue([CHAT_ENTRY_A, CHAT_ENTRY_DISABLED]),
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="chat-runs-list"]').exists()).toBe(true);
    expect(wrapper.find(`[data-testid="chat-run-row-${CHAT_ENTRY_A.id}"]`).exists()).toBe(true);
    expect(wrapper.find(`[data-testid="chat-run-row-${CHAT_ENTRY_DISABLED.id}"]`).exists()).toBe(true);
  });

  it('shows load error when chat list() rejects', async () => {
    const { wrapper } = mountWithChat({
      list: vi.fn().mockRejectedValue(new Error('chat store down')),
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="chat-runs-load-error"]').text()).toContain('chat store down');
  });

  it('shows "disabled" badge for disabled chat entries', async () => {
    const { wrapper } = mountWithChat({
      list: vi.fn().mockResolvedValue([CHAT_ENTRY_DISABLED]),
    });
    await flushPromises();
    const row = wrapper.find(`[data-testid="chat-run-row-${CHAT_ENTRY_DISABLED.id}"]`);
    expect(row.text()).toContain('disabled');
  });

  it('shows run history when a chat row is expanded', async () => {
    const histMock = vi.fn().mockResolvedValue([CHAT_HIST_COMPLETED, CHAT_HIST_FAILED]);
    const { wrapper } = mountWithChat({
      list: vi.fn().mockResolvedValue([CHAT_ENTRY_A]),
      history: histMock,
    });
    await flushPromises();

    await wrapper.find(`[data-testid="chat-run-header-${CHAT_ENTRY_A.id}"]`).trigger('click');
    await flushPromises();

    expect(histMock).toHaveBeenCalledWith(CHAT_ENTRY_A.id, 20);
    expect(wrapper.find(`[data-testid="chat-run-history-${CHAT_ENTRY_A.id}"]`).exists()).toBe(true);
    expect(wrapper.find(`[data-testid="chat-run-hist-row-${CHAT_HIST_COMPLETED.id}"]`).exists()).toBe(true);
    expect(wrapper.find(`[data-testid="chat-run-hist-row-${CHAT_HIST_FAILED.id}"]`).exists()).toBe(true);
  });

  it('collapses history when header is clicked again', async () => {
    const { wrapper } = mountWithChat({
      list: vi.fn().mockResolvedValue([CHAT_ENTRY_A]),
      history: vi.fn().mockResolvedValue([CHAT_HIST_COMPLETED]),
    });
    await flushPromises();

    const header = wrapper.find(`[data-testid="chat-run-header-${CHAT_ENTRY_A.id}"]`);
    await header.trigger('click');
    await flushPromises();
    expect(wrapper.find(`[data-testid="chat-run-history-${CHAT_ENTRY_A.id}"]`).exists()).toBe(true);

    await header.trigger('click');
    await flushPromises();
    expect(wrapper.find(`[data-testid="chat-run-history-${CHAT_ENTRY_A.id}"]`).exists()).toBe(false);
  });

  it('shows "No runs yet." when chat history is empty', async () => {
    const { wrapper } = mountWithChat({
      list: vi.fn().mockResolvedValue([CHAT_ENTRY_A]),
      history: vi.fn().mockResolvedValue([]),
    });
    await flushPromises();

    await wrapper.find(`[data-testid="chat-run-header-${CHAT_ENTRY_A.id}"]`).trigger('click');
    await flushPromises();

    expect(wrapper.find(`[data-testid="chat-run-history-empty-${CHAT_ENTRY_A.id}"]`).exists()).toBe(true);
  });

  it('shows the output snippet for completed history entries', async () => {
    const { wrapper } = mountWithChat({
      list: vi.fn().mockResolvedValue([CHAT_ENTRY_A]),
      history: vi.fn().mockResolvedValue([CHAT_HIST_COMPLETED]),
    });
    await flushPromises();

    await wrapper.find(`[data-testid="chat-run-header-${CHAT_ENTRY_A.id}"]`).trigger('click');
    await flushPromises();

    expect(
      wrapper.find(`[data-testid="chat-run-hist-snippet-${CHAT_HIST_COMPLETED.id}"]`).text(),
    ).toContain('Today you have 3 meetings.');
  });

  it('shows inline error for failed history entries', async () => {
    const { wrapper } = mountWithChat({
      list: vi.fn().mockResolvedValue([CHAT_ENTRY_A]),
      history: vi.fn().mockResolvedValue([CHAT_HIST_FAILED]),
    });
    await flushPromises();

    await wrapper.find(`[data-testid="chat-run-header-${CHAT_ENTRY_A.id}"]`).trigger('click');
    await flushPromises();

    expect(
      wrapper.find(`[data-testid="chat-run-hist-error-${CHAT_HIST_FAILED.id}"]`).text(),
    ).toContain('model timeout');
  });

  it('calls chatClient.runNow when Run now is clicked', async () => {
    const runNowMock = vi.fn().mockResolvedValue({
      id: 'hist-new',
      chatRunId: CHAT_ENTRY_A.id,
      status: 'completed',
      startedAt: new Date().toISOString(),
    });
    const { wrapper } = mountWithChat({
      list: vi.fn().mockResolvedValue([CHAT_ENTRY_A]),
      runNow: runNowMock,
    });
    await flushPromises();

    await wrapper.find(`[data-testid="chat-run-now-${CHAT_ENTRY_A.id}"]`).trigger('click');
    await flushPromises();

    expect(runNowMock).toHaveBeenCalledWith(CHAT_ENTRY_A.id);
  });

  it('shows action error when chat runNow fails', async () => {
    const { wrapper } = mountWithChat({
      list: vi.fn().mockResolvedValue([CHAT_ENTRY_A]),
      runNow: vi.fn().mockRejectedValue(new Error('dispatcher unavailable')),
    });
    await flushPromises();

    await wrapper.find(`[data-testid="chat-run-now-${CHAT_ENTRY_A.id}"]`).trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="scheduled-inbox-action-error"]').text()).toContain('dispatcher unavailable');
  });

  it('shows correct status classes for chat run history', async () => {
    const { wrapper } = mountWithChat({
      list: vi.fn().mockResolvedValue([CHAT_ENTRY_A]),
      history: vi.fn().mockResolvedValue([CHAT_HIST_COMPLETED, CHAT_HIST_FAILED]),
    });
    await flushPromises();

    await wrapper.find(`[data-testid="chat-run-header-${CHAT_ENTRY_A.id}"]`).trigger('click');
    await flushPromises();

    const completedEl = wrapper.find(`[data-testid="chat-run-hist-status-${CHAT_HIST_COMPLETED.id}"]`);
    expect(completedEl.classes()).toContain('text-signal-ok');

    const failedEl = wrapper.find(`[data-testid="chat-run-hist-status-${CHAT_HIST_FAILED.id}"]`);
    expect(failedEl.classes()).toContain('text-signal-danger');
  });
});
