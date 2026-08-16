/**
 * entrypoint.featureFlags.test.ts — the fleet capability gate, asserted from
 * the entry point.
 *
 * This file exists because the *previous* test suite is what let the bug ship.
 * Every existing capability spec seeds the module by hand — e.g.
 * `shell/__tests__/LeftRail.sites-nav.test.ts` calls
 * `initFeatureFlags(makeAppInfo({ sites_hosting: true }))` and then asserts the
 * Sites nav item renders. That test was green for months while the nav item was
 * invisible to every user on earth, because in production nothing ever called
 * `initFeatureFlags`. A test that seeds the state it is testing can only prove
 * the gate's arithmetic, never that anyone opens the gate.
 *
 * So these tests import `main.ts` / `main-served.ts` and let them boot. The
 * only things stubbed are the transport (so `appInfo()` is deterministic) and
 * the root component (so we are not mounting the entire shell). The capability
 * module is the real one, reached through the real import graph — delete the
 * `bootFeatureFlags(client)` line from either entry point and these fail.
 *
 * (docs/dead-code-audit-2026-08-16.md finding A4, part 3)
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { AppInfo } from '@/lib/types';

// Mutable across tests; read by the mocked transport below. `vi.hoisted` is
// required because vi.mock factories are hoisted above the imports.
const boot = vi.hoisted(() => ({
  /** Resolved by appInfo(), or thrown if it is an Error. */
  result: undefined as unknown,
  /** Counts AppInfo RPCs so we can prove boot does not double-fetch. */
  calls: 0,
}));

vi.mock('@/App.vue', () => ({
  default: { name: 'AppStub', render: () => null },
}));

vi.mock('@/lib/harnessClient', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/lib/harnessClient')>();
  const make = () => ({
    ...actual.createFakeHarnessClient(),
    appInfo: async () => {
      boot.calls += 1;
      if (boot.result instanceof Error) throw boot.result;
      return boot.result;
    },
  });
  return {
    ...actual,
    // Both entry points get the same stub; each test imports only one.
    createHarnessClient: make,
    createServedHarnessClient: make,
  };
});

function makeAppInfo(capabilities?: Record<string, boolean>): AppInfo {
  return {
    build: '1.2.3',
    commit: 'deadbeef',
    buildTime: '',
    goVersion: 'go1.25',
    platform: 'test/arm64',
    windowSize: { width: 1280, height: 800 },
    ...(capabilities ? { capabilities, tier: 'team' as const } : {}),
  };
}

/**
 * Boots an entry point and returns the capability module *from the same module
 * registry*, so the instance under assertion is the one the entry point wrote
 * to. Importing `@/lib/featureFlags` at the top of this file instead would
 * hand back a stale copy after `vi.resetModules()` and the assertions would be
 * meaningless.
 */
async function bootEntryPoint(entry: 'desktop' | 'served') {
  document.body.innerHTML = '<div id="app"></div>';
  if (entry === 'desktop') {
    await import('../main');
  } else {
    await import('../main-served');
  }
  const flags = await import('@/lib/featureFlags');
  // Two turns: one for the appInfo() promise, one for bootFeatureFlags' own
  // continuation after the await.
  await Promise.resolve();
  await Promise.resolve();
  return flags;
}

describe.each(['desktop', 'served'] as const)(
  '%s entry point — fleet capability boot',
  (entry) => {
    beforeEach(() => {
      vi.resetModules();
      boot.result = makeAppInfo();
      boot.calls = 0;
    });

    afterEach(() => {
      document.body.innerHTML = '';
    });

    it('populates the capability snapshot from AppInfo at boot', async () => {
      boot.result = makeAppInfo({ sites_hosting: true, context_sync: false });
      const flags = await bootEntryPoint(entry);

      // These two assertions are the whole point of the file: nothing in this
      // test touched initFeatureFlags. The entry point did.
      expect(flags.signedIn.value).toBe(true);
      expect(flags.capability('sites_hosting')).toBe(true);
      expect(flags.capability('context_sync')).toBe(false);
    });

    it('leaves every gate closed when the user is signed out', async () => {
      // Signed out → core/rpc/api.go omits the capability map entirely
      // (the capView.Source == "default-deny" branch).
      boot.result = makeAppInfo();
      const flags = await bootEntryPoint(entry);

      expect(flags.signedIn.value).toBe(false);
      expect(flags.capability('sites_hosting')).toBe(false);
    });

    it('fails closed when the AppInfo fetch rejects', async () => {
      boot.result = new Error('transport unavailable');
      const flags = await bootEntryPoint(entry);

      expect(flags.signedIn.value).toBe(false);
      expect(flags.capability('sites_hosting')).toBe(false);
    });

    it('fetches AppInfo exactly once at boot', async () => {
      boot.result = makeAppInfo({ sites_hosting: true });
      await bootEntryPoint(entry);
      // The desktop entry point shares one AppInfo promise between the
      // capability gate and Sentry's release/gitsha rather than issuing two.
      expect(boot.calls).toBe(1);
    });
  },
);
