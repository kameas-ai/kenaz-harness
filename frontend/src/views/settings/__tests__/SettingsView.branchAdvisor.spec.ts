/**
 * SettingsView.branchAdvisor.spec.ts — BranchAdvisorSettings is mounted
 * and reachable (engineer-truth-pass-01PMTP01 WP03, finding B2b / FR-004).
 *
 * Before this WP, BranchAdvisorSettings.vue had zero mount sites — its
 * only non-test reference was its own docstring, and the existing
 * components/settings/__tests__/BranchAdvisorSettings.spec.ts mounted it
 * directly, which passed against a fully unmounted panel (the exact
 * green-test-over-dead-surface shape CLAUDE.md's blind-spot #2 describes).
 *
 * This test goes through the real parent — SettingsView, reached via
 * ?tab=branch-advisor exactly as SettingsTabs.vue's nav entry does — so a
 * regression that un-mounts the panel again (e.g. reverting the
 * v-else-if branch) fails here even if the component-level spec stays
 * green.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';

vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return {
    ...actual,
    useRoute: () => ({ query: { tab: 'branch-advisor' }, path: '/settings' }),
    useRouter: () => undefined,
  };
});

import SettingsView from '@/views/settings/SettingsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Settings } from '@/lib/types';

function provide(overrides: Partial<Settings> = {}) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    ...overrides,
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
  return client;
}

describe('SettingsView — Branch Advisor sub-tab (B2b / FR-004)', () => {
  it('mounts BranchAdvisorSettings through the real ?tab=branch-advisor click path', async () => {
    const client = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(w.find('[data-testid="settings-branch-advisor-pane"]').exists()).toBe(true);
    expect(w.find('[data-testid="branch-advisor-settings"]').exists()).toBe(true);
    // Sanity: the general (no-tab) pane content is NOT also rendered.
    expect(w.find('[data-testid="settings-hooks-pane"]').exists()).toBe(false);
  });

  it('reflects the persisted branchAdvisorEnabled value through the real parent', async () => {
    const client = provide({ branchAdvisorEnabled: true });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const checkbox = w.find(
      '[data-testid="branch-advisor-enabled-section"] input[type="checkbox"]',
    );
    expect(checkbox.exists()).toBe(true);
    expect((checkbox.element as HTMLInputElement).checked).toBe(true);
  });
});
