/**
 * SettingsView.mcpServers.spec.ts — MCPHealthSettingsPanel is mounted and
 * reachable through the real parent. The panel was kept when the Health tab
 * was deleted (#405, ruling G-5) with zero mount sites; its own component
 * spec passing said nothing about whether a user could reach it. This goes
 * through SettingsView via ?tab=mcp-servers exactly as the SettingsTabs nav
 * entry does, and asserts the toggle reflects the real client read.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';

vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return {
    ...actual,
    useRoute: () => ({ query: { tab: 'mcp-servers' }, path: '/settings' }),
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

function provide(autoRestart: boolean) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
  };
  const getMCPAutoRestart = vi.fn(async () => autoRestart);
  const setMCPAutoRestart = vi.fn(async (_v: boolean) => undefined);
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
      getMCPAutoRestart,
      setMCPAutoRestart,
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
  return { client, getMCPAutoRestart, setMCPAutoRestart };
}

describe('SettingsView — MCP Servers sub-tab', () => {
  it('mounts MCPHealthSettingsPanel through ?tab=mcp-servers and reads the live knob', async () => {
    const { client, getMCPAutoRestart, setMCPAutoRestart } = provide(false);
    const w = mount(SettingsView, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    expect(w.find('[data-testid="settings-mcp-servers-pane"]').exists()).toBe(true);
    expect(w.find('[data-testid="mcp-health-settings-panel"]').exists()).toBe(true);
    expect(w.find('[data-testid="settings-form"]').exists()).toBe(false);
    expect(getMCPAutoRestart).toHaveBeenCalled();
    const toggle = w.find('[data-testid="mcp-auto-restart-toggle"]');
    expect((toggle.element as HTMLInputElement).checked).toBe(false);

    (toggle.element as HTMLInputElement).checked = true;
    await toggle.trigger('change');
    await flushPromises();
    expect(setMCPAutoRestart).toHaveBeenCalledWith(true);
  });

  it('has a nav entry in the settings rail that points at the tab', () => {
    const tabs = mount(SettingsTabs);
    const entry = tabs.find('[data-testid="settings-tab-mcp-servers"]');
    expect(entry.exists()).toBe(true);
    expect(entry.text()).toContain('MCP Servers');
  });
});
