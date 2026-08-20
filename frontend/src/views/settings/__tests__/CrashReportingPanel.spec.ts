/**
 * CrashReportingPanel.spec.ts
 *
 * Unit tests for the Privacy → Crash Reporting settings panel.
 * (sentry-error-monitoring-01KX5R8G WP05)
 */
import { describe, it, expect, vi, afterEach } from 'vitest';
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

  it('tests DSN reachability and shows success result naming the Go process', async () => {
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
    // entry-points-and-crash-reporting-01PMZD13 UNIT-7, AC-17: the string
    // must name the process it actually tested — the Go crash reporter,
    // not an unqualified "reachable" that a user could read as covering
    // the renderer too.
    expect(wrapper.text()).toContain('Go crash reporter: DSN reachable');
  });

  // ── UNIT-7, AC-17: the browser-transmit line is SEPARATELY computed from
  // the Go DSN test, and its verdict is driven by the page's own CSP meta
  // tag — not a hardcoded string — so it "flips on its own" if the CSP is
  // ever relaxed (E-001). Falsifiable form from the mission spec: with
  // connect-src 'none', the browser line must be negative while the Go
  // line is positive; today (before this unit) one green string covered
  // both. ─────────────────────────────────────────────────────────────────

  function setCSPMeta(content: string) {
    document.head.querySelectorAll('meta[http-equiv="Content-Security-Policy"]').forEach((el) => el.remove());
    const meta = document.createElement('meta');
    meta.setAttribute('http-equiv', 'Content-Security-Policy');
    meta.setAttribute('content', content);
    document.head.appendChild(meta);
  }

  afterEach(() => {
    document.head.querySelectorAll('meta[http-equiv="Content-Security-Policy"]').forEach((el) => el.remove());
  });

  it('reports the renderer as blocked when connect-src is none (desktop production)', async () => {
    setCSPMeta("default-src 'none'; connect-src 'none'; script-src 'self'");
    const { wrapper } = setup({
      tier: 'anonymous',
      sentryDsn: 'https://key@sentry.example.com/123',
    });
    await flushPromises();

    const testBtn = wrapper.findAll('button').find((b) => b.text().includes('Test'));
    await testBtn!.trigger('click');
    await flushPromises();

    // Go line positive, browser line negative — the two lines disagree,
    // which is the whole point: one green string used to cover both.
    expect(wrapper.text()).toContain('Go crash reporter: DSN reachable');
    expect(wrapper.text()).toContain('blocks the renderer from reaching Sentry');
  });

  it('reports the renderer as allowed when connect-src permits the DSN origin', async () => {
    setCSPMeta("default-src 'none'; connect-src 'self'; script-src 'self'");
    const { wrapper } = setup({
      tier: 'anonymous',
      // Same-origin as jsdom's default test origin (http://localhost:3000
      // / http://localhost) so browserCanTransmitUnderCurrentCSP's
      // 'self' branch evaluates true.
      sentryDsn: `${window.location.origin}/123`,
    });
    await flushPromises();

    const testBtn = wrapper.findAll('button').find((b) => b.text().includes('Test'));
    await testBtn!.trigger('click');
    await flushPromises();

    expect(wrapper.text()).toContain("CSP allows reaching the DSN origin");
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
