/**
 * Settings sync one-time upload confirmation (provider-credentials-client-
 * 01PCCLI01 A2, owner ruling H1) — the settings-issue provider and its
 * banner rendering.
 *
 *   - no category awaiting confirmation → no issue;
 *   - held categories → one warning, plain-language title, the categories
 *     named in the body, a "Start syncing" fix that calls
 *     Sync_ConfirmUploads, and a secondary link to Settings › Sync;
 *   - after the fix the banner re-reads status and the issue is gone;
 *   - served mode skips it (Sync_* has no served dispatch).
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref, readonly } from 'vue';

const served = ref(false);
vi.mock('@/lib/useServedMode', () => ({
  isServedMode: () => served.value,
  useServedMode: () => readonly(served),
}));

import SettingsIssuesBanner from '@/views/settings/SettingsIssuesBanner.vue';
import { collectSettingsIssues } from '@/lib/settingsIssues';
import { createFakeHarnessClient, type HarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { SyncStatusView } from '@/lib/types';

function makeClient(rows: SyncStatusView[]) {
  const base = createFakeHarnessClient();
  let current = rows;
  const confirmUploads = vi.fn(async () => {
    current = current.map((r) => ({ ...r, awaiting_confirmation: false }));
  });
  const status = vi.fn(async () => current);
  const client: HarnessClient = {
    ...base,
    sync: { ...base.sync, status, confirmUploads },
  };
  return { client, confirmUploads, status };
}

const HELD: SyncStatusView[] = [
  { category: 'ui_theme', enabled: true, awaiting_confirmation: true },
  { category: 'model_prefs', enabled: true, awaiting_confirmation: true },
  { category: 'installed_mcp', enabled: false },
  { category: 'slash_commands', enabled: true, local_only: true },
];

beforeEach(() => {
  served.value = false;
});

describe('settings-sync-confirm provider', () => {
  it('reports nothing when no category is waiting', async () => {
    const { client } = makeClient([
      { category: 'ui_theme', enabled: true },
      { category: 'slash_commands', enabled: true, local_only: true },
    ]);
    const issues = await collectSettingsIssues(client, false);
    expect(issues.find((i) => i.id === 'settings-sync-confirm')).toBeUndefined();
  });

  it('names the held categories, offers Start syncing and a path to Settings › Sync', async () => {
    const { client, confirmUploads } = makeClient(HELD);
    const issues = await collectSettingsIssues(client, false);
    const issue = issues.find((i) => i.id === 'settings-sync-confirm');
    expect(issue).toBeDefined();
    expect(issue!.severity).toBe('warning');
    expect(issue!.title).toBe('Start syncing your settings?');
    expect(issue!.body).toContain('UI theme');
    expect(issue!.body).toContain('model preferences');
    expect(issue!.body).not.toContain('installed MCP servers');
    expect(issue!.body).not.toContain('slash commands');
    // Plain language: no wire ids or internal names.
    expect(issue!.body).not.toMatch(/ui_theme|model_prefs|Fleet|lww/);
    expect(issue!.fix?.label).toBe('Start syncing');
    expect(issue!.link).toEqual({ label: 'Change what syncs', to: '/settings?tab=sync' });

    await issue!.fix!.run();
    expect(confirmUploads).toHaveBeenCalledTimes(1);
  });

  // provider_profiles / mcp_recipes upload {} (personal sync not built), so
  // "uploads them so your other devices can use them" would be false.
  it('never names a not-syncing-yet category, and raises nothing when only those are on', async () => {
    const { client } = makeClient([
      { category: 'ui_theme', enabled: true, awaiting_confirmation: true },
      { category: 'provider_profiles', enabled: true, awaiting_confirmation: true, not_syncing_yet: true },
      { category: 'mcp_recipes', enabled: true, not_syncing_yet: true },
    ]);
    const issue = (await collectSettingsIssues(client, false)).find((i) => i.id === 'settings-sync-confirm');
    expect(issue!.body).toContain('UI theme');
    expect(issue!.body).not.toContain('provider profiles');
    expect(issue!.body).not.toContain('MCP recipes');

    const { client: onlyStubs } = makeClient([
      { category: 'provider_profiles', enabled: true, awaiting_confirmation: true, not_syncing_yet: true },
    ]);
    const none = (await collectSettingsIssues(onlyStubs, false)).find((i) => i.id === 'settings-sync-confirm');
    expect(none).toBeUndefined();
  });

  // The settings file cannot be read, so uploads are held for a reason
  // "Start syncing" cannot fix: one error naming it, and no confirm prompt.
  it('reports unreadable settings instead of asking to confirm', async () => {
    const reason = 'Settings sync is paused: Kenaz could not read your settings file.';
    const { client } = makeClient([
      { category: 'ui_theme', enabled: true, uploads_blocked_reason: reason },
    ]);
    const issues = await collectSettingsIssues(client, false);
    expect(issues.find((i) => i.id === 'settings-sync-confirm')).toBeUndefined();
    const blocked = issues.find((i) => i.id === 'settings-sync-blocked');
    expect(blocked?.severity).toBe('error');
    expect(blocked?.body).toBe(reason);
    expect(blocked?.fix).toBeUndefined();
  });

  it('is skipped in served mode', async () => {
    const { client, status } = makeClient(HELD);
    const issues = await collectSettingsIssues(client, true);
    expect(issues.find((i) => i.id === 'settings-sync-confirm')).toBeUndefined();
    expect(status).not.toHaveBeenCalled();
  });
});

describe('SettingsIssuesBanner with the sync confirmation', () => {
  it('renders the fix and the link, and clears after Start syncing', async () => {
    const { client, confirmUploads } = makeClient(HELD);
    const w = mount(SettingsIssuesBanner, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const section = w.find('[data-testid="settings-issue-settings-sync-confirm"]');
    expect(section.exists()).toBe(true);
    const link = section.find('[data-testid="settings-issue-link"]');
    expect(link.text()).toBe('Change what syncs');
    expect(link.attributes('href')).toBe('#/settings?tab=sync');

    await section.find('[data-testid="settings-issue-fix"]').trigger('click');
    await flushPromises();
    expect(confirmUploads).toHaveBeenCalledTimes(1);
    expect(w.find('[data-testid="settings-issue-settings-sync-confirm"]').exists()).toBe(false);
  });
});
