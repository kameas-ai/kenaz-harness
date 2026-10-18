/**
 * SettingsIssuesBanner — settings-cleanup-01SETUX01 WP01 (FR-3 / FR-4).
 *
 * Mounted-component coverage of every banner state the spec's acceptance
 * list names: no issue, code_only, id_mismatch (Repair succeeds and the
 * banner clears), Repair fails (instructions + diagnostics), ledger_only
 * (instructions only, nothing that mutates), the Copy diagnostics payload,
 * and the served-mode skip. Plus one SettingsShell mount, so the banner's
 * placement on every settings page is pinned at the shell, not assumed.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref, readonly } from 'vue';

const served = ref(false);
vi.mock('@/lib/useServedMode', () => ({
  isServedMode: () => served.value,
  useServedMode: () => readonly(served),
}));

import SettingsIssuesBanner from '@/views/settings/SettingsIssuesBanner.vue';
import SettingsShell from '@/views/settings/SettingsShell.vue';
import { createFakeHarnessClient, type HarnessClient, type LogRow } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { DriftEntry, DriftReport } from '@/lib/types';

const ID_MISMATCH: DriftEntry = {
  version: 322,
  ledgerId: 'sessions/0322-old-name',
  expectedId: 'sessions/0322-new-name',
  kind: 'id_mismatch',
  severity: 'error',
  suggestion: 'Apply automatic fix.',
};
const ID_MISMATCH_2: DriftEntry = { ...ID_MISMATCH, version: 323, ledgerId: 'a', expectedId: 'b' };
const LEDGER_ONLY: DriftEntry = {
  version: 999,
  ledgerId: 'sessions/0999-from-the-future',
  expectedId: '',
  kind: 'ledger_only',
  severity: 'warning',
  suggestion: 'Inspect manually.',
};
const CODE_ONLY: DriftEntry = {
  version: 400,
  ledgerId: '',
  expectedId: 'sessions/0400-pending',
  kind: 'code_only',
  severity: 'info',
  suggestion: 'Will apply on next boot.',
};

const LOG_ROWS: LogRow[] = [
  { timestamp: '2026-10-09T10:00:02Z', level: 'error', source: 'storage', message: 'drift detected' },
  { timestamp: '2026-10-09T10:00:01Z', level: 'warn', source: 'mcp', message: 'ping failed' },
];

interface Harness {
  client: HarnessClient;
  getReport: ReturnType<typeof vi.fn>;
  applyDriftFix: ReturnType<typeof vi.fn>;
  tail: ReturnType<typeof vi.fn>;
}

function makeClient(reports: DriftReport[], applyDriftFix = vi.fn(async () => undefined)): Harness {
  const base = createFakeHarnessClient();
  const queue = [...reports];
  const getReport = vi.fn(async () => (queue.length > 1 ? queue.shift()! : queue[0]));
  const tail = vi.fn(async () => LOG_ROWS);
  const client: HarnessClient = {
    ...base,
    appInfo: async () => ({
      build: '0.94.1',
      commit: 'abc1234',
      buildTime: '',
      goVersion: '',
      platform: 'darwin/arm64',
      windowSize: { width: 1280, height: 800 },
    }),
    storage: { ...base.storage, getMigrationDriftReport: getReport, applyDriftFix },
    runtimeLogs: { tail },
    settings: {
      ...base.settings,
      fleetProfile: async () => ({ name: 'prod', badgeColor: '', fleetBaseUrl: '', configured: true }),
    },
  };
  return { client, getReport, applyDriftFix, tail };
}

async function mountBanner(client: HarnessClient) {
  const w = mount(SettingsIssuesBanner, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  await flushPromises();
  return w;
}

/** Everything the user reads, joined: titles, bodies, steps, buttons. */
function userCopy(w: Awaited<ReturnType<typeof mountBanner>>): string {
  return w.find('[data-testid="settings-issues-banner"]').text();
}

let clipboardText: string | null;

beforeEach(() => {
  served.value = false;
  clipboardText = null;
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: {
      writeText: vi.fn(async (t: string) => {
        clipboardText = t;
      }),
    },
  });
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('SettingsIssuesBanner', () => {
  it('renders nothing when there is no issue', async () => {
    const { client, getReport } = makeClient([{ drifts: [] }]);
    const w = await mountBanner(client);
    expect(getReport).toHaveBeenCalledOnce();
    expect(w.find('[data-testid="settings-issues-banner"]').exists()).toBe(false);
    expect(w.html()).not.toContain('settings-issue');
  });

  it('renders nothing for code_only (a pending update is not a problem)', async () => {
    const { client } = makeClient([{ drifts: [CODE_ONLY] }]);
    const w = await mountBanner(client);
    expect(w.find('[data-testid="settings-issues-banner"]').exists()).toBe(false);
  });

  it('id_mismatch: Repair fixes every entry, re-checks, and the banner clears', async () => {
    const { client, getReport, applyDriftFix } = makeClient([
      { drifts: [ID_MISMATCH, ID_MISMATCH_2, CODE_ONLY] },
      { drifts: [CODE_ONLY] },
    ]);
    const w = await mountBanner(client);

    const issue = w.find('[data-testid="settings-issue-database-repairable"]');
    expect(issue.exists()).toBe(true);
    expect(issue.find('[data-testid="settings-issue-title"]').text()).toBe(
      'Kenaz found a database inconsistency it can repair.',
    );
    const fix = issue.find('[data-testid="settings-issue-fix"]');
    expect(fix.text()).toBe('Repair');
    // Instructions are the fallback for a fixable issue, not shown up front.
    expect(issue.find('[data-testid="settings-issue-instructions"]').exists()).toBe(false);
    expect(issue.find('[data-testid="settings-issue-copy-diagnostics"]').exists()).toBe(false);

    await fix.trigger('click');
    await flushPromises();

    expect(applyDriftFix.mock.calls).toEqual([[322], [323]]);
    expect(getReport).toHaveBeenCalledTimes(2);
    expect(w.find('[data-testid="settings-issues-banner"]').exists()).toBe(false);
  });

  it('Repair failure: shows the error, then instructions and Copy diagnostics', async () => {
    const applyDriftFix = vi.fn(async () => {
      throw new Error('backup failed: disk full');
    });
    const { client } = makeClient([{ drifts: [ID_MISMATCH] }], applyDriftFix);
    const w = await mountBanner(client);

    await w.find('[data-testid="settings-issue-fix"]').trigger('click');
    await flushPromises();

    const issue = w.find('[data-testid="settings-issue-database-repairable"]');
    expect(issue.find('[data-testid="settings-issue-fix-error"]').text()).toContain(
      'backup failed: disk full',
    );
    expect(issue.find('[data-testid="settings-issue-fix"]').exists()).toBe(false);
    const steps = issue.findAll('[data-testid="settings-issue-instructions"] li').map((li) => li.text());
    expect(steps).toHaveLength(4);
    expect(steps.join(' ')).toContain('Quit Kenaz');
    expect(steps.join(' ')).toContain('~/.kenaz/harness/prod');
    expect(issue.find('[data-testid="settings-issue-copy-diagnostics"]').exists()).toBe(true);
  });

  it('ledger_only: instructions and Copy diagnostics, no button that mutates', async () => {
    const { client, applyDriftFix } = makeClient([{ drifts: [LEDGER_ONLY, CODE_ONLY] }]);
    const w = await mountBanner(client);

    const issue = w.find('[data-testid="settings-issue-database-needs-support"]');
    expect(issue.exists()).toBe(true);
    expect(issue.find('[data-testid="settings-issue-fix"]').exists()).toBe(false);
    const steps = issue.findAll('[data-testid="settings-issue-instructions"] li').map((li) => li.text());
    expect(steps.some((s) => s.includes('Quit Kenaz'))).toBe(true);
    expect(steps.some((s) => s.includes('~/.kenaz/harness/prod'))).toBe(true);
    expect(steps.some((s) => s.includes('support'))).toBe(true);

    // The only button is Copy diagnostics, and it never calls a writer.
    const buttons = issue.findAll('button');
    expect(buttons.map((b) => b.attributes('data-testid'))).toEqual([
      'settings-issue-copy-diagnostics',
    ]);
    await buttons[0].trigger('click');
    await flushPromises();
    expect(applyDriftFix).not.toHaveBeenCalled();
  });

  it('user copy carries no developer vocabulary', async () => {
    const applyDriftFix = vi.fn(async () => {
      throw new Error('x');
    });
    const { client } = makeClient([{ drifts: [ID_MISMATCH, LEDGER_ONLY] }], applyDriftFix);
    const w = await mountBanner(client);
    await w.find('[data-testid="settings-issue-fix"]').trigger('click');
    await flushPromises();
    const copy = userCopy(w);
    expect(copy).not.toMatch(/ledger|migration|drift/i);
    expect(copy).not.toMatch(/\b(322|999)\b/);
    expect(copy).not.toContain('sessions/');
  });

  it('Copy diagnostics puts version, env, the report JSON and recent warn+error logs on the clipboard', async () => {
    const report: DriftReport = { drifts: [LEDGER_ONLY] };
    const { client, tail } = makeClient([report]);
    const w = await mountBanner(client);

    const btn = w.find('[data-testid="settings-issue-copy-diagnostics"]');
    await btn.trigger('click');
    await flushPromises();

    expect(tail).toHaveBeenCalledWith({ level: 'warn', limit: 200 });
    expect(clipboardText).not.toBeNull();
    const text = clipboardText!;
    expect(text).toContain('App version: 0.94.1 (commit abc1234)');
    expect(text).toContain('Platform: darwin/arm64');
    expect(text).toContain('Environment: prod');
    expect(text).toContain(JSON.stringify(report, null, 2));
    expect(text).toContain('2026-10-09T10:00:02Z ERROR [storage] drift detected');
    expect(text).toContain('2026-10-09T10:00:01Z WARN [mcp] ping failed');
    expect(btn.text()).toBe('Copied');
  });

  it('diagnostics still copy when the log tail fails', async () => {
    const { client, tail } = makeClient([{ drifts: [LEDGER_ONLY] }]);
    tail.mockRejectedValueOnce(new Error('logs offline'));
    const w = await mountBanner(client);
    await w.find('[data-testid="settings-issue-copy-diagnostics"]').trigger('click');
    await flushPromises();
    expect(clipboardText).toContain('(unavailable: logs offline)');
    expect(clipboardText).toContain('"kind": "ledger_only"');
  });

  it('a failing check renders nothing rather than breaking the page', async () => {
    const { client, getReport } = makeClient([{ drifts: [] }]);
    getReport.mockRejectedValueOnce(new Error('db closed'));
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    const w = await mountBanner(client);
    expect(w.find('[data-testid="settings-issues-banner"]').exists()).toBe(false);
    expect(warn).toHaveBeenCalled();
  });

  it('served mode: the database check is desktop-only and never runs', async () => {
    served.value = true;
    const { client, getReport } = makeClient([{ drifts: [ID_MISMATCH] }]);
    const w = await mountBanner(client);
    expect(getReport).not.toHaveBeenCalled();
    expect(w.find('[data-testid="settings-issues-banner"]').exists()).toBe(false);
  });

  it('renders nothing (and does not throw) without a client provider', async () => {
    const w = mount(SettingsIssuesBanner);
    await flushPromises();
    expect(w.find('[data-testid="settings-issues-banner"]').exists()).toBe(false);
  });

  it('SettingsShell renders the banner above every settings page', async () => {
    const { client } = makeClient([{ drifts: [ID_MISMATCH] }]);
    const w = mount(SettingsShell, {
      props: { title: 'Providers' },
      slots: { default: '<div data-testid="page-body">body</div>' },
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const html = w.html();
    expect(w.find('[data-testid="settings-issue-database-repairable"]').exists()).toBe(true);
    expect(html.indexOf('settings-issues-banner')).toBeLessThan(html.indexOf('page-body'));
  });
});
