/**
 * CompliancePanel tests — fleet-audit-archival-01NDFSEX13 WP06
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CompliancePanel from '@/views/settings/CompliancePanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { ComplianceStatus } from '@/lib/types';

const defaultStatus: ComplianceStatus = {
  lastArchivedAt: '2026-07-05T12:00:00Z',
  pendingCount: 3,
  chainBreak: false,
  retentionDays: 90,
  enabled: true,
  archiverRunning: true,
};

function buildClient(overrides: Partial<ComplianceStatus> = {}, opts?: {
  archiveNowError?: string;
  setRetentionError?: string;
  skipToIdError?: string;
}) {
  let current: ComplianceStatus = { ...defaultStatus, ...overrides };
  const statusFn = vi.fn(async () => ({ ...current }));
  const archiveNowFn = vi.fn(async () => {
    if (opts?.archiveNowError) throw new Error(opts.archiveNowError);
    // Simulate flush: clear pending count.
    current = { ...current, pendingCount: 0 };
  });
  const setRetentionFn = vi.fn(async (days: number) => {
    if (opts?.setRetentionError) throw new Error(opts.setRetentionError);
    current = { ...current, retentionDays: days };
  });
  const skipToIdFn = vi.fn(async (_toID: string) => {
    if (opts?.skipToIdError) throw new Error(opts.skipToIdError);
    // Simulate the chain break clearing after a successful skip.
    current = { ...current, chainBreak: false };
  });
  const client = createFakeHarnessClient({
    compliance: {
      status: statusFn,
      archiveNow: archiveNowFn,
      setRetention: setRetentionFn,
      skipToId: skipToIdFn,
    } as any,
  });
  return { client, statusFn, archiveNowFn, setRetentionFn, skipToIdFn };
}

describe('CompliancePanel', () => {
  it('renders panel without crashing', async () => {
    const { client } = buildClient();
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="compliance-panel"]').exists()).toBe(true);
  });

  it('shows disabled notice when enabled=false', async () => {
    const { client } = buildClient({ enabled: false });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="compliance-disabled-notice"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="compliance-status-grid"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="compliance-archive-now-btn"]').exists()).toBe(false);
  });

  it('shows status grid and pending count when enabled', async () => {
    const { client } = buildClient({ pendingCount: 7 });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="compliance-status-grid"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="compliance-pending-count"]').text()).toBe('7');
  });

  it('shows chain-break banner when chainBreak=true', async () => {
    const { client } = buildClient({ chainBreak: true });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="compliance-chain-break-banner"]').exists()).toBe(true);
  });

  it('does not show chain-break banner when chainBreak=false', async () => {
    const { client } = buildClient({ chainBreak: false });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="compliance-chain-break-banner"]').exists()).toBe(false);
  });

  it('calls archiveNow and reloads status on button click', async () => {
    const { client, archiveNowFn, statusFn } = buildClient();
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    statusFn.mockClear();
    await wrapper.find('[data-testid="compliance-archive-now-btn"]').trigger('click');
    await flushPromises();
    expect(archiveNowFn).toHaveBeenCalledOnce();
    // Status is reloaded after archive.
    expect(statusFn).toHaveBeenCalledOnce();
  });

  it('shows archive error message when archiveNow rejects', async () => {
    const { client } = buildClient({}, { archiveNowError: 'connection refused' });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    await wrapper.find('[data-testid="compliance-archive-now-btn"]').trigger('click');
    await flushPromises();
    const err = wrapper.find('[data-testid="compliance-archive-error"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('connection refused');
  });

  // ── chain-break recovery control (fleet-enforcement-truth-01PMZ505 WP06) ──

  it('does not show skip-to-id control when chainBreak=false', async () => {
    const { client } = buildClient({ chainBreak: false });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="compliance-skip-to-id-control"]').exists()).toBe(false);
  });

  it('does not show skip-to-id control when enabled=false, even with chainBreak', async () => {
    // enabled=false hides the whole banner block, chainBreak included.
    const { client } = buildClient({ enabled: false, chainBreak: true });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="compliance-chain-break-banner"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="compliance-skip-to-id-control"]').exists()).toBe(false);
  });

  it('shows skip-to-id control when chainBreak=true and enabled=true', async () => {
    const { client } = buildClient({ chainBreak: true });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="compliance-skip-to-id-control"]').exists()).toBe(true);
  });

  it('calls skipToId with the typed id and reloads status on click', async () => {
    const { client, skipToIdFn, statusFn } = buildClient({ chainBreak: true });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    statusFn.mockClear();

    await wrapper.find('[data-testid="compliance-skip-to-id-input"]').setValue('SKIP-EVENT-42');
    await wrapper.find('[data-testid="compliance-skip-to-id-btn"]').trigger('click');
    await flushPromises();

    expect(skipToIdFn).toHaveBeenCalledWith('SKIP-EVENT-42');
    expect(statusFn).toHaveBeenCalledOnce();
    // Chain break clears (per the fake's simulated behaviour) and the
    // banner + control disappear along with it.
    expect(wrapper.find('[data-testid="compliance-chain-break-banner"]').exists()).toBe(false);
  });

  it('the skip button stays disabled until an id is typed', async () => {
    const { client } = buildClient({ chainBreak: true });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const btn = wrapper.find('[data-testid="compliance-skip-to-id-btn"]');
    expect((btn.element as HTMLButtonElement).disabled).toBe(true);
    await wrapper.find('[data-testid="compliance-skip-to-id-input"]').setValue('  ');
    expect((btn.element as HTMLButtonElement).disabled).toBe(true);
    await wrapper.find('[data-testid="compliance-skip-to-id-input"]').setValue('X1');
    expect((btn.element as HTMLButtonElement).disabled).toBe(false);
  });

  it('shows skip-to-id error and keeps the banner open when skipToId rejects', async () => {
    const { client } = buildClient({ chainBreak: true }, { skipToIdError: 'unknown event id' });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    await wrapper.find('[data-testid="compliance-skip-to-id-input"]').setValue('BAD-ID');
    await wrapper.find('[data-testid="compliance-skip-to-id-btn"]').trigger('click');
    await flushPromises();
    const err = wrapper.find('[data-testid="compliance-skip-to-id-error"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('unknown event id');
    // Mutation-check: on failure the banner must still be shown — a bug
    // that clears chainBreak unconditionally (regardless of the RPC
    // outcome) would make this assertion fail.
    expect(wrapper.find('[data-testid="compliance-chain-break-banner"]').exists()).toBe(true);
  });

  it('shows load error when status RPC rejects', async () => {
    const client = createFakeHarnessClient({
      compliance: {
        status: async () => { throw new Error('not available'); },
        archiveNow: async () => {},
        setRetention: async () => {},
      } as any,
    });
    const wrapper = mount(CompliancePanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="compliance-load-error"]').text()).toContain('not available');
  });
});
