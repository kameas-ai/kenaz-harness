/**
 * UpdatesPanel.spec — covers the auto-update v0.4.0 WP05 Settings tab.
 *
 * Verifies the three blocks called out in the mission brief:
 *   - Status: current version, auto-check label, "Last checked" stamp,
 *     and the "Check now" trigger.
 *   - Preferences: auto-check toggle, channel radio, interval select.
 *   - Skipped versions: collapsible list, empty state, per-row "Unskip".
 */

import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';

import UpdatesPanel from '@/views/settings/UpdatesPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import {
  createFakeUpdateClient,
  type UpdateClient,
  type UpdateStatus,
} from '@/lib/updateClient';
import type { Settings } from '@/lib/types';

function makeSettings(overrides: Partial<Settings> = {}): Settings {
  return {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'system',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    ...overrides,
  };
}

function buildClient(initial: Settings) {
  let saved: Settings = initial;
  const set = vi.fn(async (s: Settings) => {
    saved = s;
  });
  const get = vi.fn(async () => saved);
  return {
    client: createFakeHarnessClient({
      settings: {
        get,
        set,
        loadRoute: async () => '/sessions',
        saveRoute: async () => undefined,
        logRouteChange: async () => undefined,
        loadTheme: async () => 'system' as const,
        saveTheme: async () => undefined,
        getMemory: async () => false,
        setMemory: async () => undefined,
        getConfirmEach: async () => true,
        setConfirmEach: async () => undefined,
        getWebSearch: async () => false,
        setWebSearch: async () => undefined,
        getBash: async () => false,
        setBash: async () => undefined,
        getSaveArtifact: async () => true,
        setSaveArtifact: async () => undefined,
        getMaxAgentTurns: async () => 0,
        setMaxAgentTurns: async () => undefined,
        getMonthlyCostNotifyUSD: async () => 0,
        setMonthlyCostNotifyUSD: async () => undefined,
        getPermissionMode: async () => 'normal' as const,
        setPermissionMode: async () => undefined,
        getPermissionCacheDangerousOps: async () => false,
        setPermissionCacheDangerousOps: async () => undefined,
        getBashAllowlistMigrated: async () => false,
        setBashAllowlistMigrated: async () => undefined,
        getPermissionsMigrationToastShown: async () => false,
        setPermissionsMigrationToastShown: async () => undefined,
        getCedarStrictCredentialMode: async () => false,
        setCedarStrictCredentialMode: async () => undefined,
        getFSRequestAccessEnabled: async () => true,
        setFSRequestAccessEnabled: async () => undefined,
        getAutonomy: async () => ({ level: null, overrides: {} }),
        setAutonomy: async () => undefined,
      },
    }),
    set,
    get,
  };
}

function mountPanel(opts: {
  settings?: Partial<Settings>;
  status?: Partial<UpdateStatus>;
  skipped?: string[];
  updaterOverrides?: Partial<UpdateClient>;
} = {}) {
  const { client, set } = buildClient(makeSettings(opts.settings));
  const fullStatus: UpdateStatus = {
    currentVersion: 'v0.3.3',
    autoCheckEnabled: true,
    lastCheckedAt: null,
    channel: 'stable',
    available: null,
    checking: false,
    ...opts.status,
  };
  const startCheck = vi.fn(async () => {});
  const installLatest = vi.fn(async () => {});
  const unskipVersion = vi.fn(async () => {});
  let skipped = [...(opts.skipped ?? [])];
  const updater = createFakeUpdateClient({
    status: async () => fullStatus,
    startCheck,
    installLatest,
    listSkippedVersions: async () => [...skipped],
    unskipVersion: async (v: string) => {
      skipped = skipped.filter((x) => x !== v);
      unskipVersion(v);
    },
    ...opts.updaterOverrides,
  });
  const wrapper = mount(UpdatesPanel, {
    props: { updateClientOverride: updater },
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return {
    wrapper,
    set,
    startCheck,
    installLatest,
    unskipVersion,
  };
}

describe('UpdatesPanel — status block', () => {
  it('renders the current version and auto-check indicator', async () => {
    const { wrapper } = mountPanel();
    await flushPromises();
    expect(wrapper.find('[data-testid="updates-current-version"]').text()).toContain(
      'v0.3.3',
    );
    expect(
      wrapper.find('[data-testid="updates-auto-check-label"]').text(),
    ).toBe('on');
  });

  it('shows "Never" when lastCheckedAt is null', async () => {
    const { wrapper } = mountPanel();
    await flushPromises();
    expect(wrapper.find('[data-testid="updates-last-checked"]').text()).toBe(
      'Never',
    );
  });

  it('triggers startCheck when "Check for updates now" is clicked', async () => {
    const { wrapper, startCheck } = mountPanel();
    await flushPromises();
    await wrapper.find('[data-testid="updates-check-now"]').trigger('click');
    await flushPromises();
    expect(startCheck).toHaveBeenCalled();
  });

  it('renders the available-update mini-panel when status reports one', async () => {
    const { wrapper, installLatest } = mountPanel({
      status: {
        available: {
          version: 'v0.3.4',
          notesUrl: 'https://example.com/r/v0.3.4',
          publishedAt: '2026-04-25T00:00:00Z',
        },
      },
    });
    await flushPromises();
    const panel = wrapper.find('[data-testid="updates-available-panel"]');
    expect(panel.exists()).toBe(true);
    expect(panel.text()).toContain('v0.3.4');
    await wrapper.find('[data-testid="updates-install"]').trigger('click');
    await flushPromises();
    expect(installLatest).toHaveBeenCalledWith('v0.3.4');
  });
});

describe('UpdatesPanel — preferences', () => {
  it('persists the auto-check toggle through Settings.set', async () => {
    const { wrapper, set } = mountPanel();
    await flushPromises();
    await wrapper
      .find('[data-testid="updates-auto-check-toggle"]')
      .trigger('change');
    await flushPromises();
    const last = set.mock.calls.at(-1)![0] as Settings;
    expect(last.autoCheckUpdatesDisabled).toBe(true);
  });

  it('persists the channel selection', async () => {
    const { wrapper, set } = mountPanel();
    await flushPromises();
    await wrapper
      .find('[data-testid="updates-channel-prerelease"]')
      .trigger('click');
    await flushPromises();
    const last = set.mock.calls.at(-1)![0] as Settings;
    expect(last.updateChannel).toBe('prerelease');
  });

  it('persists the check-interval selection', async () => {
    const { wrapper, set } = mountPanel();
    await flushPromises();
    const select = wrapper.find('[data-testid="updates-interval-select"]')
      .element as HTMLSelectElement;
    select.value = '3600';
    await wrapper
      .find('[data-testid="updates-interval-select"]')
      .trigger('change');
    await flushPromises();
    const last = set.mock.calls.at(-1)![0] as Settings;
    expect(last.updateCheckIntervalSec).toBe(3600);
  });
});

describe('UpdatesPanel — skipped versions', () => {
  it('renders the empty state when the skip-list is empty', async () => {
    const { wrapper } = mountPanel({ skipped: [] });
    await flushPromises();
    await wrapper
      .find('[data-testid="updates-skipped-toggle"]')
      .trigger('click');
    await flushPromises();
    expect(
      wrapper.find('[data-testid="updates-skipped-empty"]').exists(),
    ).toBe(true);
  });

  it('renders one row per skipped version and unskips on click', async () => {
    const { wrapper, unskipVersion } = mountPanel({
      skipped: ['v0.3.4', 'v0.3.5'],
    });
    await flushPromises();
    await wrapper
      .find('[data-testid="updates-skipped-toggle"]')
      .trigger('click');
    await flushPromises();
    expect(
      wrapper.find('[data-testid="updates-skipped-list"]').text(),
    ).toContain('v0.3.4');
    await wrapper
      .find('[data-testid="updates-unskip-v0.3.4"]')
      .trigger('click');
    await flushPromises();
    expect(unskipVersion).toHaveBeenCalledWith('v0.3.4');
  });
});
