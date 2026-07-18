/**
 * routing.spec.ts
 *
 * FR-004: Route restoration is crash-safe. If restoreLastRoute navigates to a
 * route whose navigation throws, the app falls back to /sessions WITHOUT
 * persisting the crashing route as the new lastRoute.
 *
 * Tests:
 *   (a) Normal restoration navigates to the persisted route.
 *   (b) A failing restoration falls back to /sessions.
 *   (c) saveRoute is NOT called with the crashing path during the fallback.
 *   (d) No-op when persisted path === current path.
 *   (e) No-op when loadRoute() throws (backend not ready).
 */
import { describe, it, expect, vi } from 'vitest';
import { createRouter, createMemoryHistory } from 'vue-router';
import { defineComponent, h } from 'vue';
import { restoreLastRoute, installRouteAuditing, SAFE_FALLBACK_ROUTE } from '../routing';
import type { HarnessClient } from '../harnessClient';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

const Stub = defineComponent({ render: () => h('div', 'stub') });

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: Stub },
      { path: '/sessions', component: Stub },
      { path: '/contexts', component: Stub },
    ],
  });
}

/** Build a minimal client that only implements the settings sub-client. */
function makeClient(
  settingsOverrides: Partial<{
    loadRoute: () => Promise<string>;
    saveRoute: (r: string) => Promise<void>;
    logRouteChange: (f: string, t: string) => Promise<void>;
  }> = {},
): HarnessClient {
  const noop = async (): Promise<void> => undefined;

  const settings = {
    get: async () => ({
      schemaVersion: 1,
      lastRoute: '/sessions',
      theme: 'system' as const,
      accent: 'default',
      windowSize: { width: 1280, height: 800 },
      memoryEnabled: false,
    }),
    set: noop,
    loadRoute: async (): Promise<string> => '/sessions',
    saveRoute: noop as (r: string) => Promise<void>,
    logRouteChange: noop as (f: string, t: string) => Promise<void>,
    loadTheme: async () => 'system' as const,
    saveTheme: noop as (t: unknown) => Promise<void>,
    getMemory: async () => false,
    setMemory: noop,
    // Fill in the rest of the settings surface with noops.
    getWebFetchEnabled: async () => false, setWebFetchEnabled: noop,
    getWebSearch: async () => false, setWebSearch: noop,
    getBash: async () => false, setBash: noop,
    getSaveArtifact: async () => true, setSaveArtifact: noop,
    getMaxAgentTurns: async () => 0, setMaxAgentTurns: noop,
    getMonthlyCostNotifyUSD: async () => 0, setMonthlyCostNotifyUSD: noop,
    getPermissionMode: async () => 'normal' as const, setPermissionMode: noop as (m: unknown) => Promise<void>,
    getPermissionCacheDangerousOps: async () => false, setPermissionCacheDangerousOps: noop,
    getBashAllowlistMigrated: async () => false, setBashAllowlistMigrated: noop,
    getPermissionsMigrationToastShown: async () => false, setPermissionsMigrationToastShown: noop,
    getCedarStrictCredentialMode: async () => false, setCedarStrictCredentialMode: noop,
    getFSReadEnabled: async () => false, setFSReadEnabled: noop,
    getFSWriteEnabled: async () => false, setFSWriteEnabled: noop,
    getFSRequestAccessEnabled: async () => true, setFSRequestAccessEnabled: noop,
    getTodoEnabled: async () => false, setTodoEnabled: noop,
    getAutonomy: async () => ({ level: null as null, overrides: {} as Record<string, unknown> }),
    setAutonomy: noop as (a: unknown) => Promise<void>,
    getMCPAutoRestart: async () => true, setMCPAutoRestart: noop,
    getEmbedderConfig: async () => ({ profileId: '', modelOverride: '' }),
    setEmbedderConfig: noop as (c: unknown) => Promise<void>,
    getShowPerMessageTokenMeter: async () => false, setShowPerMessageTokenMeter: noop,
    getMultimodalInput: async () => true, setMultimodalInput: noop,
    getAutoCaptureGeneratedImages: async () => true, setAutoCaptureGeneratedImages: noop,
    getMaxGeneratedImageBytes: async () => 20 * 1024 * 1024, setMaxGeneratedImageBytes: noop,
    getAutoResumeOnKeyRotation: async () => true, setAutoResumeOnKeyRotation: noop,
    getArtifactPreview: async () => ({ enabled: false, maxBytes: 5 * 1024 * 1024, timeoutMs: 2000 }),
    getAutoTitleEnabled: async () => true, setAutoTitleEnabled: noop,
    getAuditSettings: async () => ({ strategy: 'keep_forever' as const, window_days: 90 }),
    setAuditSettings: noop as (s: unknown) => Promise<void>,
    ...settingsOverrides,
  };

  return { settings } as unknown as HarnessClient;
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

describe('restoreLastRoute (FR-004)', () => {
  it('restores to the persisted route when navigation succeeds', async () => {
    const router = makeRouter();
    await router.push('/');
    await router.isReady();

    const client = makeClient({ loadRoute: async () => '/sessions' });
    await restoreLastRoute(router, client);

    expect(router.currentRoute.value.path).toBe('/sessions');
  });

  it('falls back to /sessions when router.replace throws', async () => {
    const router = makeRouter();
    await router.push('/');
    await router.isReady();

    // Simulate a navigation failure: wrap router.replace so that navigating
    // to /contexts throws, but /sessions succeeds.
    const origReplace = router.replace.bind(router);
    vi.spyOn(router, 'replace').mockImplementation(async (to) => {
      const path = typeof to === 'string' ? to : (to as { path: string }).path ?? '';
      if (path === '/contexts') {
        throw new Error('route /contexts failed to load');
      }
      return origReplace(to);
    });

    const client = makeClient({ loadRoute: async () => '/contexts' });
    await restoreLastRoute(router, client);

    // Must land on the safe fallback (FR-004).
    expect(router.currentRoute.value.path).toBe(SAFE_FALLBACK_ROUTE);
  });

  it('does not call saveRoute with the crashing path on fallback', async () => {
    const router = makeRouter();
    await router.push('/');
    await router.isReady();

    const saveRouteSpy = vi.fn().mockResolvedValue(undefined);

    const origReplace = router.replace.bind(router);
    vi.spyOn(router, 'replace').mockImplementation(async (to) => {
      const path = typeof to === 'string' ? to : (to as { path: string }).path ?? '';
      if (path === '/contexts') throw new Error('navigation failed');
      return origReplace(to);
    });

    const client = makeClient({
      loadRoute: async () => '/contexts',
      saveRoute: saveRouteSpy,
    });

    // Install route auditing so saveRoute is hooked into afterEach.
    installRouteAuditing(router, client);

    await restoreLastRoute(router, client);

    // saveRoute must NOT be called with the crashing path (FR-004).
    const callsWithCrash = saveRouteSpy.mock.calls.filter(
      (args: unknown[]) => args[0] === '/contexts',
    );
    expect(callsWithCrash).toHaveLength(0);
  });

  it('no-ops when the persisted route matches the current path', async () => {
    const router = makeRouter();
    await router.push('/sessions');
    await router.isReady();

    const replaceSpy = vi.spyOn(router, 'replace');
    const client = makeClient({ loadRoute: async () => '/sessions' });

    await restoreLastRoute(router, client);

    // replace should NOT be called when the path is already correct.
    expect(replaceSpy).not.toHaveBeenCalled();
    expect(router.currentRoute.value.path).toBe('/sessions');
  });

  it('no-ops when loadRoute() throws (backend not ready)', async () => {
    const router = makeRouter();
    await router.push('/');
    await router.isReady();

    const replaceSpy = vi.spyOn(router, 'replace');
    const client = makeClient({
      loadRoute: async () => { throw new Error('backend not ready'); },
    });

    await restoreLastRoute(router, client);

    // replace should NOT be called; current route is unchanged.
    expect(replaceSpy).not.toHaveBeenCalled();
    expect(router.currentRoute.value.path).toBe('/');
  });
});
