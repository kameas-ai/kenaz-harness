/**
 * MemoryAccessNotice — kenaz-fleet spec 017 T23 (contract-harness-sync
 * §17.3–§17.5, legal D168(2)): dormant until Fleet serves a text, verbatim
 * once it does, "I have read this" and nothing like agree/accept/consent,
 * withdraw after an ack, plain wording on refusals.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MemoryAccessNotice from '@/views/settings/MemoryAccessNotice.vue';
import MemorySyncPanel from '@/views/settings/MemorySyncPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MemberNotice, MemberNotices, MemorySyncStatus } from '@/lib/types';

const TEXT = 'Team leads and admins in your organization can see notes you sync.\n\n  Spacing as registered.';

function notice(over: Partial<MemberNotice> = {}): MemberNotice {
  return {
    family: 'memory-access',
    edition: 'memory-access/v1',
    text: TEXT,
    textHash: 'b'.repeat(64),
    action: '',
    rowEdition: '',
    acked: false,
    actedAt: '',
    ackedSince: '',
    ...over,
  };
}

function mountNotice(
  first: MemberNotices,
  respond: (...a: any[]) => Promise<MemberNotices> = async () => ({ notices: [] }),
  holdActive = false,
) {
  const memberNotices = vi.fn(async () => first);
  const respondMemberNotice = vi.fn(respond);
  const client = createFakeHarnessClient({ fleet: { memberNotices, respondMemberNotice } as any });
  const wrapper = mount(MemoryAccessNotice, {
    props: { holdActive },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, memberNotices, respondMemberNotice };
}

describe('MemoryAccessNotice', () => {
  it('renders nothing while no text is registered', async () => {
    const { wrapper } = mountNotice({ notices: [] });
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-access-notice"]').exists()).toBe(false);
    expect(wrapper.html()).toBe('<!--v-if-->');
  });

  it('renders nothing when the read is unavailable (served mode)', async () => {
    const memberNotices = vi.fn(async () => {
      throw new Error('not available');
    });
    const client = createFakeHarnessClient({ fleet: { memberNotices } as any });
    const wrapper = mount(MemoryAccessNotice, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-access-notice"]').exists()).toBe(false);
  });

  it('ignores a served agent-telemetry text', async () => {
    const { wrapper } = mountNotice({ notices: [notice({ family: 'agent-telemetry', edition: 'agent-telemetry/v4' })] });
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-access-notice"]').exists()).toBe(false);
  });

  it('shows the served text verbatim with exactly "I have read this"', async () => {
    const { wrapper } = mountNotice({ notices: [notice()] }, undefined, true);
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-access-notice-text"]').element.textContent).toBe(TEXT);
    const btn = wrapper.find('[data-testid="memory-access-notice-read"]');
    expect(btn.text()).toBe('I have read this');
    expect(wrapper.find('[data-testid="memory-access-notice-hold"]').exists()).toBe(true);
    const words = wrapper.findAll('button').map((b) => b.text().toLowerCase());
    for (const w of words) {
      expect(w).not.toMatch(/agree|accept|consent/);
    }
  });

  it('acks with the served edition and hash, then offers withdraw', async () => {
    const { wrapper, respondMemberNotice } = mountNotice({ notices: [notice()] }, async () => ({
      notices: [notice({ action: 'ack', rowEdition: 'memory-access/v1', acked: true })],
    }));
    await flushPromises();
    await wrapper.find('[data-testid="memory-access-notice-read"]').trigger('click');
    await flushPromises();
    expect(respondMemberNotice).toHaveBeenCalledWith('memory-access', 'memory-access/v1', 'b'.repeat(64), 'ack');
    expect(wrapper.emitted('changed')).toBeTruthy();
    expect(wrapper.find('[data-testid="memory-access-notice-read"]').exists()).toBe(false);
    const withdraw = wrapper.find('[data-testid="memory-access-notice-withdraw"]');
    expect(withdraw.text()).toBe('Stop team leads and admins seeing my notes');
  });

  it('withdraws, and offers "I have read this" again afterwards', async () => {
    const { wrapper, respondMemberNotice } = mountNotice(
      { notices: [notice({ action: 'ack', rowEdition: 'memory-access/v1', acked: true })] },
      async () => ({ notices: [notice({ action: 'withdraw', rowEdition: 'memory-access/v1' })] }),
    );
    await flushPromises();
    await wrapper.find('[data-testid="memory-access-notice-withdraw"]').trigger('click');
    await flushPromises();
    expect(respondMemberNotice).toHaveBeenCalledWith('memory-access', 'memory-access/v1', 'b'.repeat(64), 'withdraw');
    expect(wrapper.find('[data-testid="memory-access-notice-state"]').text()).toContain('stopped');
    expect(wrapper.find('[data-testid="memory-access-notice-read"]').text()).toBe('I have read this');
  });

  it('shows a paused refusal in plain wording', async () => {
    const { wrapper } = mountNotice({ notices: [notice()] }, async () => {
      throw new Error(
        "your organization's Fleet features are paused, so this can't be recorded right now. Try again once they're back",
      );
    });
    await flushPromises();
    await wrapper.find('[data-testid="memory-access-notice-read"]').trigger('click');
    await flushPromises();
    const err = wrapper.find('[data-testid="memory-access-notice-error"]').text();
    expect(err).toBe(
      "Your organization's Fleet features are paused, so this can't be recorded right now. Try again once they're back.",
    );
    expect(err).not.toMatch(/403|regist/i);
  });
});

describe('MemorySyncPanel + MemoryAccessNotice', () => {
  function status(over: Partial<MemorySyncStatus> = {}): MemorySyncStatus {
    return {
      wired: true,
      entitled: true,
      enabled: true,
      scopes: ['long_term'],
      consentVersion: 'v',
      optedInAt: '',
      currentConsentVersion: 'v',
      liveRecords: 0,
      liveBytes: 0,
      maxRecords: 1,
      maxBytes: 1,
      blockedCount: 0,
      pendingCount: 2,
      olderNotes: 0,
      unresolvedNotes: 0,
      lane: { status: 'ok', consecutiveFailures: 0 },
      ...over,
    };
  }

  it('is absent from the panel while nothing is registered', async () => {
    const client = createFakeHarnessClient({
      fleet: {
        memorySyncStatus: async () => status(),
        memberNotices: async () => ({ notices: [] }),
      } as any,
    });
    const wrapper = mount(MemorySyncPanel, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-sync-panel"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="memory-access-notice"]').exists()).toBe(false);
  });

  it('shows the hold line in the panel and refreshes status after the read', async () => {
    const memorySyncStatus = vi
      .fn()
      .mockResolvedValueOnce(status({ noticeHold: true, noticeHoldReason: 'unread' }))
      .mockResolvedValue(status({ noticeHold: false }));
    const client = createFakeHarnessClient({
      fleet: {
        memorySyncStatus,
        memberNotices: async () => ({ notices: [notice()] }),
        respondMemberNotice: async () => ({
          notices: [notice({ action: 'ack', rowEdition: 'memory-access/v1', acked: true })],
        }),
      } as any,
    });
    const wrapper = mount(MemorySyncPanel, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-access-notice-hold"]').exists()).toBe(true);
    await wrapper.find('[data-testid="memory-access-notice-read"]').trigger('click');
    await flushPromises();
    expect(memorySyncStatus).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[data-testid="memory-access-notice-hold"]').exists()).toBe(false);
  });
});
