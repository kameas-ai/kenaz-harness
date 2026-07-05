/**
 * ScheduledChatsPanel.spec.ts
 *
 * Vitest unit tests for Settings → Scheduled Chats panel.
 * scheduled-chat-runs-01KX5R8B (WP05).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ScheduledChatsPanel from '@/views/settings/scheduledchat/ScheduledChatsPanel.vue';
import {
  createFakeScheduledChatClient,
  type ScheduledChatEntry,
} from '@/lib/scheduledChatClient';

// ── fixtures ────────────────────────────────────────────────────────────────

const FAKE_RUN: ScheduledChatEntry = {
  id: 'run-1',
  name: 'Daily briefing',
  promptTemplate: 'Summarize my calendar for {{date}}.',
  cron: '0 9 * * *',
  timezone: 'America/New_York',
  outputSink: 'banner',
  enabled: true,
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
};

const FAKE_RUN_DISABLED: ScheduledChatEntry = {
  id: 'run-2',
  name: 'Weekly report',
  promptTemplate: 'Give me a weekly report.',
  cron: '0 8 * * 1',
  outputSink: 'none',
  enabled: false,
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
};

// ── helpers ────────────────────────────────────────────────────────────────

function mountPanel(runs: ScheduledChatEntry[] = [FAKE_RUN]) {
  const client = createFakeScheduledChatClient({
    list: vi.fn().mockResolvedValue(runs),
  });
  const wrapper = mount(ScheduledChatsPanel, {
    props: { client },
    global: {
      stubs: {
        // Stub the modal so we don't need its full dependency tree.
        ScheduledChatFormModal: {
          template: '<div data-testid="stub-form-modal" />',
          emits: ['saved', 'cancel'],
        },
      },
    },
  });
  return { wrapper, client };
}

// ── tests ──────────────────────────────────────────────────────────────────

describe('ScheduledChatsPanel', () => {
  it('renders the panel root', async () => {
    const { wrapper } = mountPanel();
    await flushPromises();
    expect(wrapper.find('[data-testid="scheduled-chats-panel"]').exists()).toBe(true);
  });

  it('shows a row per scheduled run after load', async () => {
    const { wrapper } = mountPanel([FAKE_RUN, FAKE_RUN_DISABLED]);
    await flushPromises();
    expect(wrapper.find('[data-testid="scheduled-chats-list"]').exists()).toBe(true);
    expect(wrapper.find(`[data-testid="scheduled-chat-row-${FAKE_RUN.id}"]`).exists()).toBe(true);
    expect(wrapper.find(`[data-testid="scheduled-chat-row-${FAKE_RUN_DISABLED.id}"]`).exists()).toBe(true);
  });

  it('shows empty state when no runs exist', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    expect(wrapper.find('[data-testid="scheduled-chats-empty"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="scheduled-chats-list"]').exists()).toBe(false);
  });

  it('shows load error when list() rejects', async () => {
    const client = createFakeScheduledChatClient({
      list: vi.fn().mockRejectedValue(new Error('db unavailable')),
    });
    const wrapper = mount(ScheduledChatsPanel, { props: { client } });
    await flushPromises();
    expect(wrapper.find('[data-testid="scheduled-chats-load-error"]').text()).toContain('db unavailable');
  });

  it('opens create modal when "+ New" is clicked', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    await wrapper.find('[data-testid="scheduled-chats-create-btn"]').trigger('click');
    expect(wrapper.find('[data-testid="stub-form-modal"]').exists()).toBe(true);
  });

  it('opens edit modal when Edit is clicked', async () => {
    const { wrapper } = mountPanel([FAKE_RUN]);
    await flushPromises();
    await wrapper.find(`[data-testid="scheduled-chat-edit-${FAKE_RUN.id}"]`).trigger('click');
    expect(wrapper.find('[data-testid="stub-form-modal"]').exists()).toBe(true);
  });

  it('calls client.remove when Delete is clicked', async () => {
    const removeMock = vi.fn().mockResolvedValue(undefined);
    const client = createFakeScheduledChatClient({
      list: vi.fn().mockResolvedValue([FAKE_RUN]),
      remove: removeMock,
    });
    const wrapper = mount(ScheduledChatsPanel, { props: { client } });
    await flushPromises();
    await wrapper.find(`[data-testid="scheduled-chat-delete-${FAKE_RUN.id}"]`).trigger('click');
    await flushPromises();
    expect(removeMock).toHaveBeenCalledWith(FAKE_RUN.id);
  });

  it('calls client.setEnabled when Pause is clicked', async () => {
    const setEnabledMock = vi.fn().mockResolvedValue(undefined);
    const client = createFakeScheduledChatClient({
      list: vi.fn().mockResolvedValue([FAKE_RUN]),
      setEnabled: setEnabledMock,
    });
    const wrapper = mount(ScheduledChatsPanel, { props: { client } });
    await flushPromises();
    await wrapper.find(`[data-testid="scheduled-chat-toggle-${FAKE_RUN.id}"]`).trigger('click');
    await flushPromises();
    expect(setEnabledMock).toHaveBeenCalledWith(FAKE_RUN.id, false);
  });

  it('calls client.setEnabled with true when Resume is clicked on disabled run', async () => {
    const setEnabledMock = vi.fn().mockResolvedValue(undefined);
    const client = createFakeScheduledChatClient({
      list: vi.fn().mockResolvedValue([FAKE_RUN_DISABLED]),
      setEnabled: setEnabledMock,
    });
    const wrapper = mount(ScheduledChatsPanel, { props: { client } });
    await flushPromises();
    await wrapper.find(`[data-testid="scheduled-chat-toggle-${FAKE_RUN_DISABLED.id}"]`).trigger('click');
    await flushPromises();
    expect(setEnabledMock).toHaveBeenCalledWith(FAKE_RUN_DISABLED.id, true);
  });

  it('calls client.runNow when "Run now" is clicked', async () => {
    const runNowMock = vi.fn().mockResolvedValue({
      id: 'hist-1',
      chatRunId: FAKE_RUN.id,
      status: 'completed',
      startedAt: new Date().toISOString(),
    });
    const client = createFakeScheduledChatClient({
      list: vi.fn().mockResolvedValue([FAKE_RUN]),
      runNow: runNowMock,
    });
    const wrapper = mount(ScheduledChatsPanel, { props: { client } });
    await flushPromises();
    await wrapper.find(`[data-testid="scheduled-chat-run-now-${FAKE_RUN.id}"]`).trigger('click');
    await flushPromises();
    expect(runNowMock).toHaveBeenCalledWith(FAKE_RUN.id);
  });

  it('shows action error banner when delete fails', async () => {
    const client = createFakeScheduledChatClient({
      list: vi.fn().mockResolvedValue([FAKE_RUN]),
      remove: vi.fn().mockRejectedValue(new Error('delete failed')),
    });
    const wrapper = mount(ScheduledChatsPanel, { props: { client } });
    await flushPromises();
    await wrapper.find(`[data-testid="scheduled-chat-delete-${FAKE_RUN.id}"]`).trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="scheduled-chats-action-error"]').text()).toContain('delete failed');
  });

  it('shows "disabled" badge for disabled runs', async () => {
    const { wrapper } = mountPanel([FAKE_RUN_DISABLED]);
    await flushPromises();
    const row = wrapper.find(`[data-testid="scheduled-chat-row-${FAKE_RUN_DISABLED.id}"]`);
    expect(row.text()).toContain('disabled');
  });

  it('reloads list after modal emits saved', async () => {
    const listMock = vi.fn().mockResolvedValue([FAKE_RUN]);
    const client = createFakeScheduledChatClient({ list: listMock });
    const wrapper = mount(ScheduledChatsPanel, {
      props: { client },
      global: {
        stubs: {
          ScheduledChatFormModal: {
            template: '<div data-testid="stub-form-modal" />',
            emits: ['saved', 'cancel'],
          },
        },
      },
    });
    await flushPromises();
    await wrapper.find('[data-testid="scheduled-chats-create-btn"]').trigger('click');
    const initialCallCount = listMock.mock.calls.length;

    // Simulate the modal emitting "saved"
    // The modal is stubbed so emit via wrapper.vm directly
    await (wrapper.vm as any).handleSaved(FAKE_RUN);
    await flushPromises();

    expect(listMock.mock.calls.length).toBeGreaterThan(initialCallCount);
  });
});
