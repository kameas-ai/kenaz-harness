/**
 * BlockedPermissionRequestsPanel.spec.ts — model-scheduled-jobs-01PMSJ01
 * WP07 (AC-008's frontend half + FR-004's surfacing requirement).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import BlockedPermissionRequestsPanel from '@/views/settings/BlockedPermissionRequestsPanel.vue';
import { createFakeBlockedRequestsClient } from '@/lib/blockedRequestsClient';
import { createFakeScheduledChatClient } from '@/lib/scheduledChatClient';
import type { BlockedPermissionRequest } from '@/lib/blockedRequestsClient';

const SCHEDULED_ROW: BlockedPermissionRequest = {
  id: 'req-1',
  origin: 'scheduled_chat_run',
  originId: 'chatrun-1',
  sessionId: 'sess-1',
  family: 'fs',
  action: 'write_filesystem',
  resource: '/tmp/briefing.md',
  reason: 'no policy permits this write',
  status: 'pending',
  createdAt: '2026-01-01T00:00:00Z',
};

const INTERACTIVE_ROW: BlockedPermissionRequest = {
  ...SCHEDULED_ROW,
  id: 'req-2',
  origin: 'interactive',
  originId: '',
  resource: '/tmp/notes.md',
};

describe('BlockedPermissionRequestsPanel', () => {
  it('renders the empty state when there is nothing pending', async () => {
    const client = createFakeBlockedRequestsClient({ listPending: async () => [] });
    const wrapper = mount(BlockedPermissionRequestsPanel, { props: { client } });
    await flushPromises();
    expect(wrapper.find('[data-testid="blocked-requests-empty"]').exists()).toBe(true);
  });

  it('lists pending rows with resource and origin', async () => {
    const client = createFakeBlockedRequestsClient({
      listPending: async () => [{ ...SCHEDULED_ROW }, { ...INTERACTIVE_ROW }],
    });
    const wrapper = mount(BlockedPermissionRequestsPanel, { props: { client } });
    await flushPromises();
    expect(wrapper.find('[data-testid="blocked-request-row-req-1"]').text()).toContain(
      '/tmp/briefing.md',
    );
    expect(wrapper.find('[data-testid="blocked-request-row-req-1"]').text()).toContain(
      'Scheduled chat run',
    );
    expect(wrapper.find('[data-testid="blocked-request-row-req-2"]').text()).toContain(
      'Interactive',
    );
  });

  it('shows a Re-run button only for scheduled-run-origin rows', async () => {
    const client = createFakeBlockedRequestsClient({
      listPending: async () => [{ ...SCHEDULED_ROW }, { ...INTERACTIVE_ROW }],
    });
    const wrapper = mount(BlockedPermissionRequestsPanel, { props: { client } });
    await flushPromises();
    expect(wrapper.find('[data-testid="blocked-request-rerun-req-1"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="blocked-request-rerun-req-2"]').exists()).toBe(false);
  });

  it('Grant calls client.grant and marks the row Granted (keeping Re-run visible)', async () => {
    const grantMock = vi.fn().mockResolvedValue(undefined);
    const client = createFakeBlockedRequestsClient({
      listPending: async () => [{ ...SCHEDULED_ROW }],
      grant: grantMock,
    });
    const wrapper = mount(BlockedPermissionRequestsPanel, { props: { client } });
    await flushPromises();
    await wrapper.find('[data-testid="blocked-request-grant-req-1"]').trigger('click');
    await flushPromises();
    expect(grantMock).toHaveBeenCalledWith('req-1');
    expect(wrapper.find('[data-testid="blocked-request-granted-label"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="blocked-request-rerun-req-1"]').exists()).toBe(true);
  });

  it('Dismiss calls client.dismiss and removes the row', async () => {
    const dismissMock = vi.fn().mockResolvedValue(undefined);
    const client = createFakeBlockedRequestsClient({
      listPending: async () => [{ ...SCHEDULED_ROW }],
      dismiss: dismissMock,
    });
    const wrapper = mount(BlockedPermissionRequestsPanel, { props: { client } });
    await flushPromises();
    await wrapper.find('[data-testid="blocked-request-dismiss-req-1"]').trigger('click');
    await flushPromises();
    expect(dismissMock).toHaveBeenCalledWith('req-1');
    expect(wrapper.find('[data-testid="blocked-request-row-req-1"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="blocked-requests-empty"]').exists()).toBe(true);
  });

  it('Re-run calls the EXISTING ScheduledChat_RunNow binding via chatClient.runNow — no new RPC', async () => {
    const runNowMock = vi.fn().mockResolvedValue({
      id: 'hist-1',
      chatRunId: 'chatrun-1',
      status: 'completed',
      startedAt: '2026-01-01T00:00:00Z',
    });
    const client = createFakeBlockedRequestsClient({ listPending: async () => [{ ...SCHEDULED_ROW }] });
    const chatClient = createFakeScheduledChatClient({ runNow: runNowMock });
    const wrapper = mount(BlockedPermissionRequestsPanel, { props: { client, chatClient } });
    await flushPromises();
    await wrapper.find('[data-testid="blocked-request-rerun-req-1"]').trigger('click');
    await flushPromises();
    expect(runNowMock).toHaveBeenCalledWith('chatrun-1');
  });

  it('shows an error banner when Grant fails', async () => {
    const client = createFakeBlockedRequestsClient({
      listPending: async () => [{ ...SCHEDULED_ROW }],
      grant: vi.fn().mockRejectedValue(new Error('policy write failed')),
    });
    const wrapper = mount(BlockedPermissionRequestsPanel, { props: { client } });
    await flushPromises();
    await wrapper.find('[data-testid="blocked-request-grant-req-1"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="blocked-requests-error"]').text()).toContain(
      'policy write failed',
    );
    // The row must NOT be marked granted on failure.
    expect(wrapper.find('[data-testid="blocked-request-granted-label"]').exists()).toBe(false);
  });
});
