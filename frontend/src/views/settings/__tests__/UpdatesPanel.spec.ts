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
import * as updateDownloadEvents from '@/lib/updateDownloadEvents';
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
        getWebFetchEnabled: async () => false,
        setWebFetchEnabled: async () => undefined,
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
      } as any,
    }),
    set,
    get,
  };
}

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  emit: (topic: string, payload: unknown) => void;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    EventsOn: (topic, cb) => {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => s!.delete(cb);
    },
    emit: (topic, payload) => {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallFakeRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
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
    available: false,
    channel: 'stable',
    downloadState: 'idle',
    ...opts.status,
  };
  const startCheck = vi.fn(async () => {});
  const installLatest = vi.fn(async () => {});
  const unskipVersion = vi.fn(async (_v: string) => {});
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
        available: true,
        availableVersion: 'v0.3.4',
        releaseUrl: 'https://example.com/r/v0.3.4',
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

/**
 * WP03 accelerator wiring (self-update-repair-01PMUP01). The panel's
 * three useEventStream subscriptions must route each broker frame
 * through the matching applyDownload*Event pure function (tested in
 * isolation in lib/__tests__/updateDownloadEvents.spec.ts) — this is the
 * integration point between the Wails event bridge and that logic.
 *
 * Mutation: no-op one of the three UpdatesPanel.vue handlers (e.g.
 * `useEventStream('update:download-progress', () => {})`) → the
 * matching assertion below fails because the apply* spy is never
 * called for that topic.
 */
describe('UpdatesPanel — WP03 download-event accelerator wiring', () => {
  it('routes update:download-progress through applyDownloadProgressEvent', async () => {
    const rt = installFakeRuntime();
    const progressSpy = vi.spyOn(updateDownloadEvents, 'applyDownloadProgressEvent');
    try {
      mountPanel();
      await flushPromises();
      const payload = { bytes: 40, total: 100, percent: 40 };
      rt.emit('update:download-progress', payload);
      expect(progressSpy).toHaveBeenCalledWith(expect.anything(), payload);
    } finally {
      progressSpy.mockRestore();
      uninstallFakeRuntime();
    }
  });

  it('routes update:download-complete through applyDownloadCompleteEvent', async () => {
    const rt = installFakeRuntime();
    const completeSpy = vi.spyOn(updateDownloadEvents, 'applyDownloadCompleteEvent');
    try {
      mountPanel();
      await flushPromises();
      const payload = { targetVersion: '0.4.1' };
      rt.emit('update:download-complete', payload);
      expect(completeSpy).toHaveBeenCalledWith(expect.anything(), payload);
    } finally {
      completeSpy.mockRestore();
      uninstallFakeRuntime();
    }
  });

  it('routes update:download-failed through applyDownloadFailedEvent', async () => {
    const rt = installFakeRuntime();
    const failedSpy = vi.spyOn(updateDownloadEvents, 'applyDownloadFailedEvent');
    try {
      mountPanel();
      await flushPromises();
      const payload = { err: 'sha256 mismatch' };
      rt.emit('update:download-failed', payload);
      expect(failedSpy).toHaveBeenCalledWith(expect.anything(), payload);
    } finally {
      failedSpy.mockRestore();
      uninstallFakeRuntime();
    }
  });

  // Correctness-neutrality (DC-1's stated invariant, spec §4.1): with
  // NO runtime/event bridge installed at all (no window.runtime — the
  // three useEventStream subscriptions silently fail to attach, exactly
  // like "every subscription detached"), installLatest must still work
  // via the WP02 poll alone. This is the mutation tasks.md names for
  // WP03: "make installLatest await an event → fails" — installLatest
  // (updateClient.ts) never references window.runtime at all, so this
  // passing with zero event plumbing present is the proof.
  it('installLatest still works with no event bridge installed (poll alone)', async () => {
    uninstallFakeRuntime(); // ensure no window.runtime this test
    const { wrapper, installLatest } = mountPanel({
      status: { available: true, availableVersion: 'v0.3.4' },
    });
    await flushPromises();
    await wrapper.find('[data-testid="updates-install"]').trigger('click');
    await flushPromises();
    expect(installLatest).toHaveBeenCalledWith('v0.3.4');
  });
});

/**
 * WP04 — the progress surface (self-update-repair-01PMUP01, the A8 user
 * sentence). "A topic has a subscriber" is not the acceptance bar
 * (CLAUDE.md blind spot #1) — these assert the RENDERED value changes,
 * not merely that the progressbar element exists.
 */
describe('UpdatesPanel — WP04 download progress surface', () => {
  it('renders two distinct, increasing aria-valuenow values as progress advances', async () => {
    const rt = installFakeRuntime();
    try {
      const { wrapper } = mountPanel({
        status: {
          available: true,
          availableVersion: 'v0.3.4',
          downloadState: 'downloading',
          downloadProgress: 17,
        },
      });
      await flushPromises();
      const bar = wrapper.find('[data-testid="updates-progress"]');
      expect(bar.exists()).toBe(true);
      const first = bar.attributes('aria-valuenow');
      expect(first).toBe('17');

      rt.emit('update:download-progress', { bytes: 63, total: 100, percent: 63 });
      await flushPromises();
      const second = wrapper
        .find('[data-testid="updates-progress"]')
        .attributes('aria-valuenow');
      expect(second).toBe('63');
      expect(second).not.toBe(first);
    } finally {
      uninstallFakeRuntime();
    }
  });

  // DC-2 pin: 17 must render as 17%, never 1700% (the old lying
  // docstring said downloadProgress was 0..1, which would tempt a
  // `progress * 100 + '%'` renderer).
  it('renders a 17 percent value as "17%", not 1700%', async () => {
    const { wrapper } = mountPanel({
      status: {
        available: true,
        availableVersion: 'v0.3.4',
        downloadState: 'downloading',
        downloadProgress: 17,
      },
    });
    await flushPromises();
    const bar = wrapper.find('[data-testid="updates-progress"]');
    expect(bar.attributes('style')).toContain('width: 17%');
    expect(bar.attributes('style')).not.toContain('1700%');
    expect(
      wrapper.find('[data-testid="updates-progress-label"]').text(),
    ).toContain('17%');
  });

  it('renders the indeterminate variant when downloading with progress 0 (no Content-Length)', async () => {
    const { wrapper } = mountPanel({
      status: {
        available: true,
        availableVersion: 'v0.3.4',
        downloadState: 'downloading',
        downloadProgress: 0,
      },
    });
    await flushPromises();
    const bar = wrapper.find('[data-testid="updates-progress"]');
    expect(bar.exists()).toBe(true);
    expect(bar.attributes('data-indeterminate')).toBe('true');
    expect(bar.attributes('aria-valuenow')).toBeUndefined();
  });

  it('rehydrates a 42% download on mount from Status alone, with zero events replayed (AC-5)', async () => {
    uninstallFakeRuntime(); // no window.runtime — nothing CAN replay
    const { wrapper } = mountPanel({
      status: {
        available: true,
        availableVersion: 'v0.3.4',
        downloadState: 'downloading',
        downloadProgress: 42,
      },
    });
    await flushPromises();
    expect(
      wrapper.find('[data-testid="updates-progress"]').attributes('aria-valuenow'),
    ).toBe('42');
  });

  it('renders the failure reason from Status alone (no thrown exception in this session)', async () => {
    const { wrapper } = mountPanel({
      status: {
        downloadState: 'failed',
        downloadError: 'sha256 mismatch: got abc want def',
      },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="updates-error"]').text()).toContain(
      'sha256 mismatch: got abc want def',
    );
  });
});
