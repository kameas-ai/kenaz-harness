import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SettingsView from '@/views/settings/SettingsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Settings, Theme } from '@/lib/types';

function provide(overrides: Partial<Settings> = {}) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
    ...overrides,
  };
  const saveTheme = vi.fn().mockResolvedValue(undefined);
  const set = vi.fn().mockResolvedValue(undefined);
  const client = createFakeHarnessClient({
    settings: {
      get: async () => settings,
      set,
      loadRoute: async () => settings.lastRoute,
      saveRoute: async () => undefined,
      logRouteChange: async () => undefined,
      loadTheme: async () => settings.theme,
      saveTheme,
    },
    appInfo: async () => ({
      build: '0.1.0-test',
      commit: 'abcdef0',
      buildTime: '2026-04-25T00:00:00Z',
      goVersion: 'go1.23.0',
      platform: 'darwin/arm64',
      windowSize: settings.windowSize,
    }),
  });
  return { client, saveTheme, set };
}

describe('SettingsView (FR-001b numbered-section header)', () => {
  it('renders the canvas head with section number 06', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.text()).toContain('06');
    expect(w.text()).toContain('SETTINGS');
    expect(w.text()).toContain('App preferences');
  });

  it('exposes a theme selector with system / dark / light options', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.text()).toContain('System');
    expect(w.text()).toContain('Dark');
    expect(w.text()).toContain('Light');
  });

  it('persists a theme change via saveTheme', async () => {
    const { client, saveTheme } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const buttons = w.findAll('button[role=radio]');
    const lightBtn = buttons.find((b) => b.text().includes('Light'));
    expect(lightBtn).toBeTruthy();
    await lightBtn!.trigger('click');
    await flushPromises();
    expect(saveTheme).toHaveBeenCalledWith<Theme[]>('light');
  });

  it('renders harness build info from AppInfo', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.text()).toContain('0.1.0-test');
    expect(w.text()).toContain('abcdef0');
    expect(w.text()).toContain('darwin/arm64');
  });

  it('renders the schema-version + last-route row (privacy invariant #5)', async () => {
    const { client } = provide({ lastRoute: '/audit' });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(w.text()).toContain('Schema version');
    expect(w.text()).toContain('/audit');
  });

  it('uses only design tokens — no raw hex/rgba', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const html = w.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });
});
