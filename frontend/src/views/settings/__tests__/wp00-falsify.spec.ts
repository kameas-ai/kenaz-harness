// wp00-falsify.spec.ts — controls-and-readouts-that-tell-the-truth-01PMZ808
// UNIT-0 / WP00. Observes (does not predict) claim 3 from tasks.md. Claim 1
// (SettingsView.vue useRouter() after await) is superseded by
// SettingsView.wp14.spec.ts, which asserts the fixed behaviour — this file
// is trimmed accordingly. Deleted entirely once WP10 lands its own
// embedder-test coverage.
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
    onboarding: {
      state: async () => ({ harnessSelfMCPDisabled: false }),
      restartPhase2: async () => ({ sessionId: 'sess-wp00-observed' }),
      ...clientOverrides.onboarding,
    } as any,
  });
  return { client };
}

describe('WP00 claim 3 — SettingsView.vue "Test embedder" never contacts an embedder', () => {
  it('OBSERVES: embedderTestStatus reports "ok" from a settings-file read alone, with no embedder call in the fake client at all', async () => {
    const getEmbedderConfigSpy = vi.fn().mockResolvedValue({
      provider: 'openai',
      model: 'text-embedding-3-small',
      baseURL: 'http://unreachable.invalid:1',
    });
    const { client } = provide({ settings: { getEmbedderConfig: getEmbedderConfigSpy } });
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const btn = w.find('[data-testid="embedder-test-button"]');
    expect(btn.exists()).toBe(true);
    await btn.trigger('click');
    await flushPromises();

    // eslint-disable-next-line no-console
    console.log('OBSERVED getEmbedderConfig call count:', getEmbedderConfigSpy.mock.calls.length);
    console.log(
      'OBSERVED status pill:',
      w.find('[data-testid="embedder-test-ok"]').exists() ? 'ok' : 'not-ok',
    );
    expect(getEmbedderConfigSpy).toHaveBeenCalled();
    expect(w.find('[data-testid="embedder-test-ok"]').exists()).toBe(true);
  });
});
