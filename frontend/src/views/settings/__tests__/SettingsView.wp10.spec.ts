/**
 * SettingsView.wp10.spec.ts —
 * controls-and-readouts-that-tell-the-truth-01PMZ808 UNIT-6 / WP10.
 *
 * AC-024: with the embedder pointed at an unreachable host,
 * embedderTestStatus is NOT 'ok' and the error surfaces. Supersedes
 * wp00-falsify.spec.ts's "claim 3" observation test, which pinned the
 * pre-fix buggy answer (embedderTestStatus reported 'ok' from a
 * settings-file read alone, with no embedder contact at all) — deleted
 * in this commit.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SettingsView from '@/views/settings/SettingsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Attachment, Settings } from '@/lib/types';

function provide(clientOverrides: any = {}, attachmentRows: Attachment[] = []) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
  };
  const client = createFakeHarnessClient({
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
      ...clientOverrides.settings,
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
      list: async () => [...attachmentRows],
      listResolved: async () => [],
      add: async () => attachmentRows[0] ?? ({} as Attachment),
      remove: async () => undefined,
      reorder: async () => undefined,
      refresh: async () => attachmentRows[0] ?? ({} as Attachment),
    } as any,
    memory: {
      testEmbedder: vi.fn().mockRejectedValue(new Error('memory: embedder unavailable')),
      ...clientOverrides.memory,
    } as any,
  });
  return { client };
}

describe('AC-024 — Test embedder surfaces a real failure against an unreachable host', () => {
  it('embedderTestStatus becomes "error" (not "ok") and the error message surfaces', async () => {
    const { client } = provide();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const btn = w.find('[data-testid="embedder-test-button"]');
    expect(btn.exists()).toBe(true);
    await btn.trigger('click');
    await flushPromises();

    expect(client.memory.testEmbedder).toHaveBeenCalled();
    expect(w.find('[data-testid="embedder-test-ok"]').exists()).toBe(false);
    const errorEl = w.find('[data-testid="embedder-test-error"]');
    expect(errorEl.exists()).toBe(true);
    expect(errorEl.text()).toContain('embedder unavailable');
  });

  it('a resolving testEmbedder() call reports "ok"', async () => {
    const { client } = provide({ memory: { testEmbedder: vi.fn().mockResolvedValue(1536) } });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    await w.find('[data-testid="embedder-test-button"]').trigger('click');
    await flushPromises();

    expect(w.find('[data-testid="embedder-test-ok"]').exists()).toBe(true);
  });
});
