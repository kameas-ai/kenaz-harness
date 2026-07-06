/**
 * SettingsView — axe-core accessibility assertions
 * (accessibility-audit-01KQ8TDA WP02)
 *
 * Mounts SettingsView with a minimal fake client and asserts no axe-core
 * violations. color-contrast is disabled: happy-dom returns 0/0 ratio
 * (needs_manual_verification in a real browser).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SettingsView from '@/views/settings/SettingsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Settings } from '@/lib/types';
import { axe } from 'vitest-axe';

/** Axe options shared by all tests in this file. */
const axeOptions = {
  rules: {
    // happy-dom does not compute CSS, so color-contrast always reports 0/0.
    // needs_manual_verification: check contrast in dark + light themes with
    // a real browser.
    'color-contrast': { enabled: false },
    // region — component tree mounts without Shell landmark wrapper in tests.
    region: { enabled: false },
  },
};

function makeClient(overrides: Partial<Settings> = {}) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    ...overrides,
  };
  return createFakeHarnessClient({
    settings: {
      get: async () => settings,
      set: vi.fn().mockResolvedValue(undefined),
      loadRoute: async () => settings.lastRoute,
      saveRoute: async () => undefined,
      logRouteChange: async () => undefined,
      loadTheme: async () => settings.theme,
      saveTheme: vi.fn().mockResolvedValue(undefined),
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
    attachments: {
      list: async () => [],
      listResolved: async () => [],
      add: async () => ({}) as never,
      remove: async () => undefined,
      reorder: async () => undefined,
      refresh: async () => ({}) as never,
    } as any,
  });
}

describe('SettingsView — a11y (axe-core)', () => {
  it('has no axe violations in default state', async () => {
    const client = makeClient();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const results = await axe(w.element, axeOptions);
    // @ts-expect-error — toHaveNoViolations added via test-setup.ts extend
    expect(results).toHaveNoViolations();
  });
});
