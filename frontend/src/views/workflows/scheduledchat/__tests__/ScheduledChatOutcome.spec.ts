/**
 * ScheduledChatOutcome.spec.ts — dogfood 2026-10-08 round 2.
 *
 * A scheduled chat's run outcome used to reach the user only through a
 * 10-second toast: the Schedules row showed no last-run state and the
 * form gave no hint which model "active default" meant. These pin:
 *   - the Schedules row renders the persisted lastRun (status, time,
 *     model, cost, error, Open-session link), or "Not run yet";
 *   - Run now reloads so the row reflects the run just made;
 *   - the New form shows what "active default" resolves to;
 *   - Runs-tab history rows carry model, cost and an Open-session link.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ScheduledChatsPanel from '@/views/workflows/scheduledchat/ScheduledChatsPanel.vue';
import ScheduledChatFormModal from '@/views/workflows/scheduledchat/ScheduledChatFormModal.vue';
import ScheduledInbox from '@/views/workflows/ScheduledInbox.vue';
import {
  createFakeScheduledChatClient,
  type ScheduledChatEntry,
  type ScheduledChatRunSummary,
} from '@/lib/scheduledChatClient';
import type { WorkflowsClient } from '@/lib/workflowsClient';

const RouterLinkStub = {
  name: 'RouterLink',
  props: ['to'],
  template: '<a :href="String(to)"><slot /></a>',
};

const FAILED_RUN: ScheduledChatRunSummary = {
  id: 'hist-1',
  chatRunId: 'run-1',
  status: 'failed',
  startedAt: '2026-10-09T01:24:00Z',
  endedAt: '2026-10-09T01:24:02Z',
  error: 'Request too large for aion-labs/aion-2.0 (131072-token window): tool definitions and context alone exceed it — choose a larger model or disable tools',
  model: 'aion-labs/aion-2.0',
};

const COMPLETED_RUN: ScheduledChatRunSummary = {
  id: 'hist-2',
  chatRunId: 'run-1',
  sessionId: 'sess-42',
  status: 'completed',
  startedAt: '2026-10-09T01:27:00Z',
  model: '~anthropic/claude-haiku-latest',
  costUsd: 0.1124,
};

const ENTRY: ScheduledChatEntry = {
  id: 'run-1',
  name: 'Dogfood sentinel',
  promptTemplate: 'ping',
  cron: '*/3 * * * *',
  outputSink: 'banner',
  enabled: true,
  createdAt: '2026-10-08T00:00:00Z',
  updatedAt: '2026-10-08T00:00:00Z',
};

function mountPanel(runs: ScheduledChatEntry[], extra: Parameters<typeof createFakeScheduledChatClient>[0] = {}) {
  const client = createFakeScheduledChatClient({ list: vi.fn().mockResolvedValue(runs), ...extra });
  const wrapper = mount(ScheduledChatsPanel, {
    props: { client },
    global: {
      stubs: {
        RouterLink: RouterLinkStub,
        ScheduledChatFormModal: { template: '<div />', emits: ['saved', 'cancel'] },
      },
    },
  });
  return { wrapper, client };
}

describe('Schedules row — last run outcome', () => {
  it('renders a failed last run with its error and model, and no session link', async () => {
    const { wrapper } = mountPanel([{ ...ENTRY, lastRun: FAILED_RUN }]);
    await flushPromises();
    const row = wrapper.find('[data-testid="scheduled-chat-row-run-1"]');
    expect(row.find('[data-testid="chat-run-outcome-status-hist-1"]').text()).toBe('failed');
    expect(row.find('[data-testid="chat-run-outcome-error-hist-1"]').text()).toContain('Request too large for aion-labs/aion-2.0');
    expect(row.find('[data-testid="chat-run-outcome-model-hist-1"]').text()).toBe('aion-labs/aion-2.0');
    expect(row.find('[data-testid="chat-run-outcome-open-hist-1"]').exists()).toBe(false);
    expect(row.text()).toContain('Last run');
  });

  it('renders a completed last run with cost and an Open-session link', async () => {
    const { wrapper } = mountPanel([{ ...ENTRY, lastRun: COMPLETED_RUN }]);
    await flushPromises();
    const link = wrapper.find('[data-testid="chat-run-outcome-open-hist-2"]');
    expect(link.exists()).toBe(true);
    expect(link.attributes('href')).toBe('/sessions/sess-42');
    expect(wrapper.find('[data-testid="chat-run-outcome-cost-hist-2"]').text()).toBe('$0.112');
    expect(wrapper.find('[data-testid="chat-run-outcome-error-hist-2"]').exists()).toBe(false);
  });

  it('says "Not run yet" when there is no last run', async () => {
    const { wrapper } = mountPanel([ENTRY]);
    await flushPromises();
    expect(wrapper.find('[data-testid="scheduled-chat-never-run-run-1"]').text()).toBe('Not run yet');
  });

  it('Run now reloads the list so the row shows the new outcome', async () => {
    const list = vi
      .fn()
      .mockResolvedValueOnce([ENTRY])
      .mockResolvedValueOnce([{ ...ENTRY, lastRun: FAILED_RUN }]);
    const { wrapper } = mountPanel([], { list });
    await flushPromises();
    await wrapper.find('[data-testid="scheduled-chat-run-now-run-1"]').trigger('click');
    await flushPromises();
    expect(list).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[data-testid="chat-run-outcome-status-hist-1"]').text()).toBe('failed');
  });
});

describe('New scheduled chat form — active default model', () => {
  it('shows which model "active default" resolves to', async () => {
    const client = createFakeScheduledChatClient({
      defaultModel: vi.fn().mockResolvedValue({ profileId: 'p1', model: 'aion-labs/aion-2.0' }),
    });
    const wrapper = mount(ScheduledChatFormModal, { props: { client, editing: null } });
    await flushPromises();
    expect(wrapper.find('[data-testid="sc-model-default-hint"]').text()).toContain('aion-labs/aion-2.0');
  });

  it('hides the hint once a model is typed', async () => {
    const client = createFakeScheduledChatClient({
      defaultModel: vi.fn().mockResolvedValue({ profileId: 'p1', model: 'aion-labs/aion-2.0' }),
    });
    const wrapper = mount(ScheduledChatFormModal, { props: { client, editing: null } });
    await flushPromises();
    await wrapper.find('[data-testid="sc-model-input"]').setValue('~anthropic/claude-sonnet-latest');
    expect(wrapper.find('[data-testid="sc-model-default-hint"]').exists()).toBe(false);
  });

  it('says no default is configured when none resolves', async () => {
    const wrapper = mount(ScheduledChatFormModal, {
      props: { client: createFakeScheduledChatClient(), editing: null },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="sc-model-default-hint"]').text()).toContain('No default model is configured');
  });
});

describe('Runs tab — chat run history rows', () => {
  it('shows the last run on the collapsed row and model/cost/Open session per history row', async () => {
    const chatClient = createFakeScheduledChatClient({
      list: vi.fn().mockResolvedValue([{ ...ENTRY, lastRun: COMPLETED_RUN }]),
      history: vi.fn().mockResolvedValue([COMPLETED_RUN, FAILED_RUN]),
    });
    const client = {
      scheduleList: vi.fn().mockResolvedValue([]),
      scheduleRunHistory: vi.fn().mockResolvedValue([]),
      runNow: vi.fn(),
    } as unknown as WorkflowsClient;
    const wrapper = mount(ScheduledInbox, {
      props: { client, chatClient },
      global: { stubs: { RouterLink: RouterLinkStub } },
    });
    await flushPromises();
    // Collapsed: last run already visible.
    expect(wrapper.find('[data-testid="chat-run-outcome-status-hist-2"]').text()).toBe('completed');

    await wrapper.find('[data-testid="chat-run-header-run-1"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="chat-run-hist-model-hist-2"]').text()).toBe('~anthropic/claude-haiku-latest');
    expect(wrapper.find('[data-testid="chat-run-hist-cost-hist-2"]').text()).toBe('$0.112');
    expect(wrapper.find('[data-testid="chat-run-hist-open-hist-2"]').attributes('href')).toBe('/sessions/sess-42');
    expect(wrapper.find('[data-testid="chat-run-hist-open-hist-1"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="chat-run-hist-error-hist-1"]').text()).toContain('Request too large');
    wrapper.unmount();
  });
});
