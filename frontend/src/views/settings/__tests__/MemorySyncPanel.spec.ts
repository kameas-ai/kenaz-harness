/**
 * MemorySyncPanel — memory-sync-01MEMSY01 WP08 (contract H9).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MemorySyncPanel from '@/views/settings/MemorySyncPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MemorySyncStatus } from '@/lib/types';

function status(over: Partial<MemorySyncStatus> = {}): MemorySyncStatus {
  return {
    wired: true,
    entitled: true,
    enabled: false,
    scopes: ['global', 'long_term'],
    consentVersion: '',
    optedInAt: '',
    currentConsentVersion: '2026-10-07',
    liveRecords: 0,
    liveBytes: 0,
    maxRecords: 20000,
    maxBytes: 52428800,
    blockedCount: 0,
    pendingCount: 0,
    lane: { status: 'unknown', consecutiveFailures: 0 },
    ...over,
  };
}

function mountWith(initial: MemorySyncStatus) {
  const memorySyncStatus = vi.fn(async () => initial);
  const memorySyncEnable = vi.fn(async (scopes: string[]) =>
    status({ enabled: true, scopes, liveRecords: 3 }),
  );
  const memorySyncDisable = vi.fn(async () => status({ enabled: false }));
  const client = createFakeHarnessClient({
    fleet: { memorySyncStatus, memorySyncEnable, memorySyncDisable } as any,
  });
  const wrapper = mount(MemorySyncPanel, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, memorySyncStatus, memorySyncEnable, memorySyncDisable };
}

describe('MemorySyncPanel', () => {
  it('is hidden without the memory_sync capability (AC-7)', async () => {
    const { wrapper } = mountWith(status({ entitled: false }));
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-sync-panel"]').exists()).toBe(false);
  });

  it('is hidden when the install has no memory store', async () => {
    const { wrapper } = mountWith(status({ wired: false }));
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-sync-panel"]').exists()).toBe(false);
  });

  it('is off by default and enabling requires consent; sends scopes + consent version', async () => {
    const { wrapper, memorySyncEnable } = mountWith(status());
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-sync-disclosure"]').exists()).toBe(true);
    const btn = wrapper.find('[data-testid="memory-sync-enable"]');
    expect(btn.attributes('disabled')).toBeDefined();
    // Only long_term and global are offered.
    expect(wrapper.find('[data-testid="memory-sync-scope-long_term"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="memory-sync-scope-global"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="memory-sync-scope-project"]').exists()).toBe(false);
    await wrapper.find('[data-testid="memory-sync-scope-global"]').setValue(false);
    await wrapper.find('[data-testid="memory-sync-consent"]').setValue(true);
    await wrapper.find('[data-testid="memory-sync-enable"]').trigger('click');
    await flushPromises();
    expect(memorySyncEnable).toHaveBeenCalledWith(['long_term'], '2026-10-07');
    expect(wrapper.find('[data-testid="memory-sync-on"]').exists()).toBe(true);
  });

  it('shows usage, the sync_blocked count and lane health when on', async () => {
    const { wrapper } = mountWith(
      status({
        enabled: true,
        liveRecords: 12,
        blockedCount: 2,
        lane: { status: 'degraded', reason: 'clock_in_future', consecutiveFailures: 1 },
      }),
    );
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-sync-usage"]').text()).toContain('12 of 20000');
    expect(wrapper.find('[data-testid="memory-sync-blocked"]').text()).toContain('2 memories stay');
    expect(wrapper.find('[data-testid="memory-sync-lane"]').text()).toContain('clock');
  });

  it('disable keeps Fleet data by default', async () => {
    const { wrapper, memorySyncDisable } = mountWith(status({ enabled: true }));
    await flushPromises();
    await wrapper.find('[data-testid="memory-sync-disable"]').trigger('click');
    await wrapper.find('[data-testid="memory-sync-disable-confirm-button"]').trigger('click');
    await flushPromises();
    expect(memorySyncDisable).toHaveBeenCalledWith(false, '');
  });

  it('delete-from-Fleet requires typing forget-all', async () => {
    const { wrapper, memorySyncDisable } = mountWith(status({ enabled: true }));
    await flushPromises();
    await wrapper.find('[data-testid="memory-sync-disable"]').trigger('click');
    await wrapper.find('[data-testid="memory-sync-delete-fleet"]').setValue(true);
    const confirm = wrapper.find('[data-testid="memory-sync-disable-confirm-button"]');
    expect(confirm.attributes('disabled')).toBeDefined();
    await wrapper.find('[data-testid="memory-sync-confirm-text"]').setValue('forget all');
    expect(confirm.attributes('disabled')).toBeDefined();
    await wrapper.find('[data-testid="memory-sync-confirm-text"]').setValue('forget-all');
    expect(confirm.attributes('disabled')).toBeUndefined();
    await confirm.trigger('click');
    await flushPromises();
    expect(memorySyncDisable).toHaveBeenCalledWith(true, 'forget-all');
  });
});
