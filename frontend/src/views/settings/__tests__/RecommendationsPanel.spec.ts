/**
 * RecommendationsPanel.spec.ts — mounted-view tests for the local ML
 * engine's Settings surface (laya-advisors-01LAYA001 WP13).
 *
 * The WP07 lesson: a composable-only test passes against a fully
 * unrendered surface. Everything here mounts the real component and reads
 * the rendered DOM — every state's status line, the size/location
 * disclosure BEFORE the install click, the install-phase line while the
 * blocking enable() call is in flight, the two-step uninstall, the
 * never-silent update card, and the served-mode note.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { readonly, ref } from 'vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import type { SidecarStatusView } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

const servedFlag = ref(false);
vi.mock('@/lib/useServedMode', () => ({
  isServedMode: () => servedFlag.value,
  useServedMode: () => readonly(servedFlag),
}));

import RecommendationsPanel from '@/views/settings/RecommendationsPanel.vue';

function view(over: Partial<SidecarStatusView> = {}): SidecarStatusView {
  return {
    state: 'not_installed',
    reason: '',
    detail: '',
    engineVersion: '',
    installed: false,
    installedVersion: '',
    supported: true,
    available: true,
    unavailableReason: '',
    release: { version: '1.2.0', sizeMB: 207 },
    installLocation: '/Users/x/.kenaz/harness/prod/ml',
    updateAvailable: false,
    ...over,
  };
}

function setup(initial: SidecarStatusView) {
  const client = createFakeHarnessClient();
  const status = vi.spyOn(client.sidecar, 'status').mockResolvedValue(initial);
  const enable = vi.spyOn(client.sidecar, 'enable').mockResolvedValue(view({ state: 'healthy', installed: true, installedVersion: '1.2.0', engineVersion: '1.2.0' }));
  const update = vi.spyOn(client.sidecar, 'update').mockResolvedValue(view({ state: 'healthy', installed: true }));
  const repair = vi.spyOn(client.sidecar, 'repair').mockResolvedValue(view({ state: 'healthy', installed: true }));
  const uninstall = vi.spyOn(client.sidecar, 'uninstall').mockResolvedValue(view());
  const wrapper = mount(RecommendationsPanel, {
    global: { provide: { [HarnessClientKey as symbol]: client } },
  });
  return { wrapper, status, enable, update, repair, uninstall };
}

beforeEach(() => {
  servedFlag.value = false;
});
afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

describe('RecommendationsPanel — the status line names every state distinctly', () => {
  // Every core/mlsidecar.State string plus the one view-only state.
  const cases: Array<{ name: string; v: Partial<SidecarStatusView>; headline: RegExp; explain?: RegExp }> = [
    { name: 'not_installed', v: { state: 'not_installed' }, headline: /^Not installed$/ },
    { name: 'installing', v: { state: 'installing', detail: 'unpacking 1.2.0' }, headline: /^Installing/, explain: /unpacking 1\.2\.0/ },
    { name: 'healthy', v: { state: 'healthy', installed: true, installedVersion: '1.2.0', engineVersion: '1.2.0' }, headline: /^Running — engine 1\.2\.0$/ },
    { name: 'installed_idle', v: { state: 'installed_idle', installed: true, installedVersion: '1.2.0' }, headline: /^Installed — starts when needed$/ },
    { name: 'installed_unhealthy (crash)', v: { state: 'installed_unhealthy', reason: 'crash', detail: 'exec format error', installed: true, installedVersion: '1.2.0' }, headline: /^Installed, but not working$/, explain: /failed to start.*exec format error/ },
    { name: 'installed_unhealthy (port_conflict)', v: { state: 'installed_unhealthy', reason: 'port_conflict', installed: true, installedVersion: '1.2.0' }, headline: /^Installed, but not working$/, explain: /Another program is using the engine's port/ },
    { name: 'installed_unhealthy (digest_mismatch)', v: { state: 'installed_unhealthy', reason: 'digest_mismatch', installed: true, installedVersion: '1.2.0' }, headline: /^Installed, but not working$/, explain: /do not match their signature/ },
    { name: 'unverified', v: { state: 'unverified', reason: 'digest_mismatch' }, headline: /could not be verified$/ },
    { name: 'contract_unsupported', v: { state: 'contract_unsupported' }, headline: /newer than this version of Kenaz supports$/, explain: /Update Kenaz/ },
    { name: 'legacy_unverified', v: { state: 'legacy_unverified', reason: 'legacy_engine' }, headline: /^An older ML engine is running$/, explain: /Update Kenaz to share the ML engine/ },
  ];

  const seen = new Map<string, string>();
  for (const c of cases) {
    it(c.name, async () => {
      const { wrapper } = setup(view(c.v));
      await flushPromises();
      const line = wrapper.find('[data-testid="sidecar-status"]');
      expect(line.exists()).toBe(true);
      expect(line.attributes('data-state')).toBe(c.v.state);
      expect(line.text()).toMatch(c.headline);
      if (c.explain) {
        expect(wrapper.find('[data-testid="sidecar-status-explain"]').text()).toMatch(c.explain);
      }
      seen.set(c.name, line.text());
    });
  }

  it('no two states share a headline except the reason-refined installed_unhealthy ones', () => {
    const byHeadline = new Map<string, string[]>();
    for (const [name, headline] of seen) {
      byHeadline.set(headline, [...(byHeadline.get(headline) ?? []), name]);
    }
    for (const [headline, names] of byHeadline) {
      if (names.length > 1) {
        expect(names.every((n) => n.startsWith('installed_unhealthy')), `${headline} shared by ${names.join(', ')}`).toBe(true);
      }
    }
    // not_installed / installed_idle / legacy_unverified are each their own line.
    expect(seen.get('not_installed')).not.toBe(seen.get('installed_idle'));
    expect(seen.get('not_installed')).not.toBe(seen.get('installed_unhealthy (crash)'));
  });
});

describe('RecommendationsPanel — one honest install action', () => {
  it('discloses size AND location BEFORE the download, and the button carries the promised copy', async () => {
    const { wrapper, enable } = setup(view());
    await flushPromises();
    const btn = wrapper.find('[data-testid="sidecar-enable"]');
    expect(btn.exists()).toBe(true);
    const text = btn.text().replace(/\s+/g, ' ');
    expect(text).toContain('Enable local recommendations — downloads the Kameas ML engine (207 MB, runs on this device)');
    const disclosure = wrapper.find('[data-testid="sidecar-disclosure"]').text();
    expect(disclosure).toContain('/Users/x/.kenaz/harness/prod/ml');
    expect(disclosure).toMatch(/signature before it is ever run/);
    expect(enable).not.toHaveBeenCalled(); // disclosure is visible without clicking
  });

  it('clicking Enable calls the install and renders the resulting Running state', async () => {
    const { wrapper, enable } = setup(view());
    await flushPromises();
    await wrapper.find('[data-testid="sidecar-enable"]').trigger('click');
    await flushPromises();
    expect(enable).toHaveBeenCalledTimes(1);
    expect(wrapper.find('[data-testid="sidecar-status"]').text()).toMatch(/^Running/);
    // Installed now: the install button is gone, uninstall is offered.
    expect(wrapper.find('[data-testid="sidecar-enable"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="sidecar-uninstall"]').exists()).toBe(true);
  });

  it('shows the live install phase while the blocking enable() is in flight', async () => {
    vi.useFakeTimers();
    let release!: (v: SidecarStatusView) => void;
    const client = createFakeHarnessClient();
    const phases = [
      view({ state: 'installing', detail: 'downloading 1.2.0' }),
      view({ state: 'installing', detail: 'verifying 1.2.0' }),
      view({ state: 'installing', detail: 'unpacking 1.2.0' }),
    ];
    let i = 0;
    vi.spyOn(client.sidecar, 'status').mockImplementation(async () => (i === 0 ? view() : phases[Math.min(i - 1, phases.length - 1)]));
    vi.spyOn(client.sidecar, 'enable').mockImplementation(() => new Promise((r) => (release = r)));
    const wrapper = mount(RecommendationsPanel, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();

    await wrapper.find('[data-testid="sidecar-enable"]').trigger('click');
    for (const expected of ['downloading', 'verifying', 'unpacking']) {
      i += 1;
      await vi.advanceTimersByTimeAsync(1600);
      await flushPromises();
      expect(wrapper.find('[data-testid="sidecar-status"]').text()).toMatch(/^Installing/);
      expect(wrapper.find('[data-testid="sidecar-status-explain"]').text()).toContain(expected);
      // The install button is disabled, not gone, while working.
      expect((wrapper.find('[data-testid="sidecar-enable"]').element as HTMLButtonElement).disabled).toBe(true);
    }
    release(view({ state: 'healthy', installed: true, installedVersion: '1.2.0', engineVersion: '1.2.0' }));
    await flushPromises();
    expect(wrapper.find('[data-testid="sidecar-status"]').text()).toMatch(/^Running/);
  });

  it('surfaces an install failure instead of swallowing it', async () => {
    const { wrapper, enable } = setup(view());
    enable.mockRejectedValueOnce(new Error('local recommendations are not available: boom'));
    await flushPromises();
    await wrapper.find('[data-testid="sidecar-enable"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="sidecar-error"]').text()).toContain('boom');
  });

  it('offers NO install button when the engine is not available, and says why', async () => {
    const { wrapper } = setup(view({ available: false, unavailableReason: 'The Kameas ML engine has not been published to the release channel yet.' }));
    await flushPromises();
    expect(wrapper.find('[data-testid="sidecar-enable"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="sidecar-unavailable"]').text()).toContain('not been published');
  });

  it('offers a fresh download (not just "try again") when the installed files fail verification', async () => {
    const { wrapper } = setup(view({ state: 'installed_unhealthy', reason: 'digest_mismatch', installed: true, installedVersion: '1.2.0' }));
    await flushPromises();
    expect(wrapper.find('[data-testid="sidecar-enable"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sidecar-repair"]').exists()).toBe(false);
  });
});

describe('RecommendationsPanel — repair, uninstall, update', () => {
  it('a crashed engine offers "Try again", which calls repair', async () => {
    const { wrapper, repair } = setup(view({ state: 'installed_unhealthy', reason: 'crash', installed: true, installedVersion: '1.2.0' }));
    await flushPromises();
    await wrapper.find('[data-testid="sidecar-repair"]').trigger('click');
    await flushPromises();
    expect(repair).toHaveBeenCalledTimes(1);
  });

  it('uninstall is a two-step inline confirm that names everything being removed', async () => {
    const { wrapper, uninstall } = setup(view({ state: 'healthy', installed: true, installedVersion: '1.2.0', engineVersion: '1.2.0' }));
    await flushPromises();
    await wrapper.find('[data-testid="sidecar-uninstall"]').trigger('click');
    const confirm = wrapper.find('[data-testid="sidecar-uninstall-confirm"]');
    expect(confirm.exists()).toBe(true);
    expect(confirm.text()).toMatch(/engine, its downloaded models and its\s+configuration/);
    expect(confirm.text()).toContain('/Users/x/.kenaz/harness/prod/ml');
    expect(uninstall).not.toHaveBeenCalled(); // clicking Uninstall once removes nothing

    await wrapper.find('[data-testid="sidecar-uninstall-confirm-no"]').trigger('click');
    expect(wrapper.find('[data-testid="sidecar-uninstall-confirm"]').exists()).toBe(false);
    expect(uninstall).not.toHaveBeenCalled();

    await wrapper.find('[data-testid="sidecar-uninstall"]').trigger('click');
    await wrapper.find('[data-testid="sidecar-uninstall-confirm-yes"]').trigger('click');
    await flushPromises();
    expect(uninstall).toHaveBeenCalledTimes(1);
    expect(wrapper.find('[data-testid="sidecar-status"]').text()).toBe('Not installed');
    expect(wrapper.find('[data-testid="sidecar-uninstall"]').exists()).toBe(false);
  });

  it('an available update is PROMPTED, never applied on its own', async () => {
    const { wrapper, update } = setup(
      view({ state: 'installed_idle', installed: true, installedVersion: '1.2.0', updateAvailable: true, release: { version: '1.3.0', sizeMB: 210 } }),
    );
    await flushPromises();
    const card = wrapper.find('[data-testid="sidecar-update-card"]');
    expect(card.exists()).toBe(true);
    expect(card.text()).toContain('1.3.0');
    expect(card.text()).toMatch(/only happens when you choose to/);
    expect(update).not.toHaveBeenCalled(); // rendering the prompt did not update
    await wrapper.find('[data-testid="sidecar-update"]').trigger('click');
    await flushPromises();
    expect(update).toHaveBeenCalledTimes(1);
  });

  it('no update card when no update is available', async () => {
    const { wrapper } = setup(view({ state: 'healthy', installed: true, installedVersion: '1.2.0', engineVersion: '1.2.0' }));
    await flushPromises();
    expect(wrapper.find('[data-testid="sidecar-update-card"]').exists()).toBe(false);
  });
});

describe('RecommendationsPanel — served mode', () => {
  it('renders an honest desktop-only note and never calls the engine surface', async () => {
    servedFlag.value = true;
    const { wrapper, status, enable } = setup(view());
    await flushPromises();
    expect(wrapper.find('[data-testid="sidecar-served-note"]').text()).toMatch(/desktop app/);
    expect(wrapper.find('[data-testid="sidecar-enable"]').exists()).toBe(false);
    expect(status).not.toHaveBeenCalled();
    expect(enable).not.toHaveBeenCalled();
  });
});
