/**
 * SettingsView.recommendations.spec.ts — the Recommendations panel is
 * mounted and reachable through the real parent (laya-advisors-01LAYA001
 * WP13). Same shape as SettingsView.branchAdvisor.spec.ts: a panel that
 * passes its own component spec while having no mount site is the
 * green-test-over-dead-surface failure (CLAUDE.md blind spot #2), so this
 * goes through SettingsView via ?tab=recommendations exactly as the
 * SettingsTabs nav entry does, and through SettingsTabs for the nav entry.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';

vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return {
    ...actual,
    useRoute: () => ({ query: { tab: 'recommendations' }, path: '/settings' }),
    useRouter: () => undefined,
  };
});
vi.mock('@/lib/useServedMode', async () => {
  const { readonly, ref } = await import('vue');
  const f = ref(false);
  return { isServedMode: () => f.value, useServedMode: () => readonly(f) };
});

import SettingsView from '@/views/settings/SettingsView.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Settings } from '@/lib/types';

function provide() {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
  };
  const client = createFakeHarnessClient({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    settings: {
      get: async () => settings,
      set: vi.fn().mockResolvedValue(undefined),
      loadRoute: async () => settings.lastRoute,
      saveRoute: async () => undefined,
      logRouteChange: async () => undefined,
      loadTheme: async () => settings.theme,
      saveTheme: async () => undefined,
      getMemory: async () => false,
      setMemory: async () => undefined,
      getWebFetchEnabled: async () => false,
      setWebFetchEnabled: async () => undefined,
    } as any,
    appInfo: async () => ({
      build: '0.1.0-test',
      commit: 'abcdef0',
      buildTime: '2026-04-25T00:00:00Z',
      goVersion: 'go1.23.0',
      platform: 'darwin/arm64',
      windowSize: settings.windowSize,
    }),
  });
  vi.spyOn(client.sidecar, 'status').mockResolvedValue({
    state: 'legacy_unverified',
    reason: 'legacy_engine',
    detail: '',
    engineVersion: '0.9.0',
    installed: false,
    installedVersion: '',
    supported: true,
    available: true,
    unavailableReason: '',
    release: { version: '1.2.0', sizeMB: 207 },
    installLocation: '/d/ml',
    updateAvailable: false,
  });
  return client;
}

describe('SettingsView — Recommendations sub-tab (WP13)', () => {
  it('mounts RecommendationsPanel through the real ?tab=recommendations path and renders live status', async () => {
    const client = provide();
    const w = mount(SettingsView, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    expect(w.find('[data-testid="settings-recommendations-pane"]').exists()).toBe(true);
    expect(w.find('[data-testid="sidecar-panel"]').exists()).toBe(true);
    // The status line is fed by the real client call, not hardcoded.
    expect(w.find('[data-testid="sidecar-status"]').attributes('data-state')).toBe('legacy_unverified');
    expect(w.find('[data-testid="settings-form"]').exists()).toBe(false);
  });

  it('has a nav entry in the settings rail that points at the tab', () => {
    const tabs = mount(SettingsTabs);
    const entry = tabs.find('[data-testid="settings-tab-recommendations"]');
    expect(entry.exists()).toBe(true);
    expect(entry.text()).toContain('Recommendations');
  });
});
