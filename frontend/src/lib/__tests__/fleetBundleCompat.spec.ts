/**
 * signed-bundle-bytes-01SBBYT01 WP03 — plain-language notices for the
 * organization settings this build can't fully apply, and the Settings
 * issues provider that surfaces them.
 */
import { describe, it, expect, vi } from 'vitest';
import { fleetBundleNotices, settingLabel } from '@/lib/fleetBundleCompat';
import { collectSettingsIssues } from '@/lib/settingsIssues';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import type { FleetConfigPullStatusView } from '@/lib/types';

const base: FleetConfigPullStatusView = {
  lastAppliedId: 7,
  lastAppliedAt: '',
  lastError: '',
  source: 'fleet',
  bundleChecksum: 'x',
  configDistributionEnabled: true,
};

describe('fleetBundleNotices', () => {
  it('is empty for a healthy status (and for null fields from Go)', () => {
    expect(fleetBundleNotices(base)).toEqual([]);
    expect(
      fleetBundleNotices({ ...base, unknownSettingsCount: 0, unknownSettings: null, unenforceableSettings: null }),
    ).toEqual([]);
    expect(fleetBundleNotices(null)).toEqual([]);
  });

  it('counts newer settings in plain language, names only as detail', () => {
    const [n] = fleetBundleNotices({
      ...base,
      unknownSettingsCount: 2,
      unknownSettings: ['future_section', 'model_prefs.future_knob'],
    });
    expect(n.id).toBe('fleet-unknown-settings');
    expect(n.title).toBe('2 newer settings from your organization need a Kenaz update');
    expect(n.title).not.toMatch(/model_prefs|_/);
    expect(n.detail).toBe('future_section, model_prefs.future_knob');
  });

  it("names rules this device can't enforce until it updates", () => {
    const [n] = fleetBundleNotices({
      ...base,
      unenforceableSettings: [{ name: 'model_prefs.allow_user_credentials', minVersion: '99.0.0' }],
    });
    expect(n.id).toBe('fleet-unenforceable-settings');
    expect(n.title).toContain('can’t enforce one of your organization’s rules until Kenaz updates');
    expect(n.body).toContain('the rule on using your own AI provider keys (needs Kenaz 99.0.0 or later)');
  });

  it('puts update-required first, as an error', () => {
    const ns = fleetBundleNotices({ ...base, updateRequired: true, unknownSettingsCount: 1, unknownSettings: ['x'] });
    expect(ns.map((n) => n.id)).toEqual(['fleet-update-required', 'fleet-unknown-settings']);
    expect(ns[0].severity).toBe('error');
    expect(ns[1].title).toBe('1 newer setting from your organization needs a Kenaz update');
  });

  it('labels unknown names readably', () => {
    expect(settingLabel('mcp_allowlist')).toBe('the approved tools list');
    expect(settingLabel('tool_exposure.servers.*.new_key')).toBe('tool exposure servers new key');
  });
});

describe('settings issues provider: fleet-bundle-compat', () => {
  it('raises banner issues with update instructions and no fix button', async () => {
    const client = createFakeHarnessClient();
    client.settings.fleetConfigPullStatus = vi.fn(async () => ({
      ...base,
      unknownSettingsCount: 1,
      unknownSettings: ['future_section'],
      unenforceableSettings: [{ name: 'mcp_allowlist', minVersion: 'v2.0.0' }],
    }));
    const issues = (await collectSettingsIssues(client, false)).filter((i) => i.id.startsWith('fleet-'));
    expect(issues.map((i) => i.id)).toEqual(['fleet-unenforceable-settings', 'fleet-unknown-settings']);
    for (const i of issues) {
      expect(i.fix).toBeUndefined();
      expect(i.instructions?.[0]).toContain('Check for Updates');
    }
  });

  it('is skipped in a served build (no served dispatch)', async () => {
    const client = createFakeHarnessClient();
    const spy = vi.fn(async () => ({ ...base, updateRequired: true }));
    client.settings.fleetConfigPullStatus = spy;
    const issues = await collectSettingsIssues(client, true);
    expect(issues.find((i) => i.id === 'fleet-update-required')).toBeUndefined();
    expect(spy).not.toHaveBeenCalled();
  });
});
