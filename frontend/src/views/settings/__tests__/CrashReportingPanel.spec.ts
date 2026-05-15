/**
 * CrashReportingPanel.spec.ts
 *
 * Unit tests for the Privacy → Crash Reporting settings panel.
 * (sentry-error-monitoring-01KX5R8G WP05)
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CrashReportingPanel from '@/views/settings/CrashReportingPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

// ── helpers ────────────────────────────────────────────────────────────────

type MountOptions = {
  tier?: string;
  sentryDsn?: string;
  lastFive?: Array<{ id: string; capturedAt: string; kind: string; summary: string }>;
};

function setup(opts: MountOptions = {}) {
  const { tier = 'off', sentryDsn = '', lastFive = [] } = opts;

  const client = createFakeHarnessClient();

  vi.spyOn(client.settings, 'get').mockResolvedValue({
    schemaVersion: 1,
    lastRoute: '/settings',
    theme: 'system',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    memoryEnabled: false,
    confirmEachDisabled: false,
    crashReportingTier: tier,
    sentryDsn,
    hasSeenCrashReportingOnboarding: true,
  } as Parameters<typeof client.settings.set>[0]);
  const setFn = vi.spyOn(client.settings, 'set').mockResolvedValue(undefined);

  vi.spyOn(client.sentry, 'getLastFive').mockResolvedValue(lastFive);
  const generateReportFn = vi.spyOn(client.sentry, 'generateLocalReport').mockResolvedValue({
    path: '/tmp/crash.json',
    byteCount: 1234,
  });
  const testDsnFn = vi.spyOn(client.sentry, 'testDsn').mockResolvedValue({ ok: true });

  const wrapper = mount(CrashReportingPanel, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });

  return { wrapper, client, setFn, generateReportFn, testDsnFn };
}

// ── tests ─────────────────────────────────────────────────────────────────

describe('CrashReportingPanel', () => {
  it('renders the panel', async () => {
    const { wrapper } = setup();
    await flushPromises();
    expect(wrapper.find('[data-testid="crash-reporting-panel"]').exists()).toBe(true);
  });

  it('loads tier from settings on mount and checks the matching radio', async () => {
    const { wrapper } = setup({ tier: 'anonymous' });
    await flushPromises();
    const anonymousRadio = wrapper.find('input[value="anonymous"]');
    expect((anonymousRadio.element as HTMLInputElement).checked).toBe(true);
  });

  it('shows empty state when there are no recent events', async () => {
    const { wrapper } = setup({ lastFive: [] });
    await flushPromises();
    expect(wrapper.text()).toContain('No crash events captured yet');
  });

  it('shows cached events when present', async () => {
    const { wrapper } = setup({
      lastFive: [
        {
          id: 'evt-1',
          capturedAt: new Date().toISOString(),
          kind: 'exception',
          summary: 'TypeError: cannot read property',
        },
      ],
    });
    await flushPromises();
    expect(wrapper.text()).toContain('TypeError: cannot read property');
  });

  it('generates a local report and shows path', async () => {
    const { wrapper, generateReportFn } = setup();
    await flushPromises();

    const btn = wrapper.findAll('button').find((b) => b.text().includes('Generate local'));
    expect(btn).toBeDefined();
    await btn!.trigger('click');
    await flushPromises();

    expect(generateReportFn).toHaveBeenCalled();
    expect(wrapper.text()).toContain('/tmp/crash.json');
  });

  it('tests DSN reachability and shows success result', async () => {
    const { wrapper, testDsnFn } = setup({
      tier: 'anonymous',
      sentryDsn: 'https://key@sentry.example.com/123',
    });
    await flushPromises();

    const testBtn = wrapper.findAll('button').find((b) => b.text().includes('Test'));
    expect(testBtn).toBeDefined();
    await testBtn!.trigger('click');
    await flushPromises();

    expect(testDsnFn).toHaveBeenCalledWith('https://key@sentry.example.com/123');
    expect(wrapper.text()).toContain('DSN reachable');
  });

  it('shows error when DSN test fails', async () => {
    const { wrapper, testDsnFn } = setup({
      tier: 'anonymous',
      sentryDsn: 'https://bad@sentry.example.com/0',
    });
    testDsnFn.mockResolvedValueOnce({ ok: false, error: 'connection refused' });
    await flushPromises();

    const testBtn = wrapper.findAll('button').find((b) => b.text().includes('Test'));
    await testBtn!.trigger('click');
    await flushPromises();

    expect(wrapper.text()).toContain('connection refused');
  });
});
