/**
 * entrypoint.routes.test.ts — the desktop and served route tables, diffed.
 *
 * Two entry points each declared an anonymous route array that no test
 * imported, so nothing could see them drift. They did: `main-served.ts` had no
 * catch-all, meaning an unmatched hash in the browser build rendered a blank
 * page — `Shell.vue`'s `<router-view>` has no fallback. That was a P3 curiosity
 * until the fleet capability gate was wired, at which point the LeftRail's
 * Sites and Marketplace entries became visible in served mode and clicked into
 * the void.
 *
 * This test pins both halves of the resolution: the catch-all exists in both
 * tables, and the set of paths that differ is exactly the named allowlist
 * below. Adding a route to one entry point without a decision about the other
 * fails here.
 *
 * (docs/dead-code-audit-2026-08-16.md findings A4 + B4)
 */
import { describe, it, expect, vi, beforeAll } from 'vitest';
import { createMemoryHistory, createRouter, type RouteRecordRaw } from 'vue-router';

vi.mock('@/App.vue', () => ({
  default: { name: 'AppStub', render: () => null },
}));

vi.mock('@/lib/harnessClient', async (importOriginal) => {
  const actual =
    await importOriginal<typeof import('@/lib/harnessClient')>();
  const make = () => actual.createFakeHarnessClient();
  return {
    ...actual,
    createHarnessClient: make,
    createServedHarnessClient: make,
  };
});

/**
 * Paths that exist only in the desktop bundle, each with the reason it is not
 * in the served table. This list is the contract: shrink it by registering the
 * route in served mode, grow it only with a written reason.
 *
 * See docs/served-mode-boundary.md.
 */
const DESKTOP_ONLY: Record<string, string> = {
  '/sites':
    'core/serve/methods.go dispatches no Sites_* method; SitesView would ' +
    'render over a backend that rejects every call.',
  '/marketplace':
    'core/serve/methods.go dispatches no Catalog_* method; MarketplaceView ' +
    'would render over a backend that rejects every call.',
};

let desktop: RouteRecordRaw[];
let served: RouteRecordRaw[];

function paths(rs: RouteRecordRaw[]): string[] {
  return rs.map((r) => r.path).sort();
}

beforeAll(async () => {
  document.body.innerHTML = '<div id="app"></div>';
  desktop = (await import('../main')).routes;
  // Fresh host element: both entry points mount to #app, and Vue warns when a
  // second app lands on a container that already has one.
  document.body.innerHTML = '<div id="app"></div>';
  served = (await import('../main-served')).routes;
});

describe('entry-point route tables', () => {
  it('served mode registers a catch-all not-found route', () => {
    const catchAll = served.find((r) => r.path === '/:pathMatch(.*)*');
    expect(catchAll, 'main-served.ts must register a catch-all').toBeDefined();
    expect(catchAll?.name).toBe('not-found');
  });

  it('resolves an unknown served path to the not-found route', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: served });
    await router.push('/no-such-surface/deep/link');
    await router.isReady();
    expect(router.currentRoute.value.name).toBe('not-found');
  });

  it('resolves /sites in served mode to not-found rather than a blank page', async () => {
    // The rail hides this entry in served mode, but a bookmark or a hand-typed
    // hash can still get here. It must land somewhere that explains itself.
    const router = createRouter({ history: createMemoryHistory(), routes: served });
    await router.push('/sites');
    await router.isReady();
    expect(router.currentRoute.value.name).toBe('not-found');
  });

  it('differs from the desktop table only by the named allowlist', () => {
    const onlyDesktop = paths(desktop).filter((p) => !paths(served).includes(p));
    const onlyServed = paths(served).filter((p) => !paths(desktop).includes(p));

    expect(onlyDesktop).toEqual(Object.keys(DESKTOP_ONLY).sort());
    expect(onlyServed).toEqual([]);
  });

  it('keeps the desktop routes for both fleet surfaces', () => {
    // Deleting these is a product decision, not a cleanup: SitesView and
    // MarketplaceView are finished surfaces over live backends.
    expect(paths(desktop)).toContain('/sites');
    expect(paths(desktop)).toContain('/marketplace');
  });

  // engineer-truth-pass-01PMTP01 WP05 (finding B13a, B13b): /search and
  // /hooks were both routed with no reachable entry point — /search had
  // no router.beforeEach guard anywhere in the tree despite a comment
  // claiming one, and /hooks (HooksView.vue) duplicated the reachable
  // HooksSettingsView (mounted at /settings?tab=hooks) with zero in-app
  // links. Both registrations were deleted from main.ts and
  // main-served.ts; this pins that neither path resolves to anything
  // special anymore — the shared catch-all handles both.
  it('/search and /hooks resolve to not-found in both bundles (B13a, B13b)', () => {
    expect(paths(desktop)).not.toContain('/search');
    expect(paths(desktop)).not.toContain('/hooks');
    expect(paths(served)).not.toContain('/search');
    expect(paths(served)).not.toContain('/hooks');
  });

  // consent-surfaces-truth-01PMTR01 WP06 (FR-007): the denial panel's
  // acceptance says "reachable from the app shell (route or nav entry,
  // not just a component that exists)". PolicyView's own spec mounts the
  // component directly, which cannot see a route registration being
  // dropped — blind spot #1 one level up. This is the route half of the
  // chain; SettingsTabsNav.spec.ts pins the nav-entry half.
  it.each(['desktop', 'served'] as const)(
    '/policy is routed, so the denial panel is reachable (%s)',
    async (which) => {
      const routes = which === 'desktop' ? desktop : served;
      const router = createRouter({ history: createMemoryHistory(), routes });
      await router.push('/policy');
      await router.isReady();
      expect(router.currentRoute.value.name, `${which} /policy`).toBe('policy');
      expect(
        router.currentRoute.value.matched.length,
        `${which} /policy must match a real record, not the catch-all`,
      ).toBeGreaterThan(0);
    },
  );

  it.each(['desktop', 'served'] as const)(
    '#/search and #/hooks resolve to the not-found route (%s)',
    async (which) => {
      const routes = which === 'desktop' ? desktop : served;
      const router = createRouter({ history: createMemoryHistory(), routes });
      for (const path of ['/search', '/hooks']) {
        await router.push(path);
        await router.isReady();
        expect(router.currentRoute.value.name, `${which} ${path}`).toBe('not-found');
      }
    },
  );
});
