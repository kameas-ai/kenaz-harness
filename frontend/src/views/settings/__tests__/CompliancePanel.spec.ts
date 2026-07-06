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
  const client = createFakeHarnessClient({
    compliance: {
      status: statusFn,
      archiveNow: archiveNowFn,
      setRetention: setRetentionFn,
    } as any,
  });
  return { client, statusFn, archiveNowFn, setRetentionFn };
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
