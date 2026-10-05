/**
 * lastRoute.legacyRedirect — nav-ia-sweep-01DOGF0F WP-PI (persistence integrity).
 *
 * The one persisted value this mission's IA change can strand is the
 * `lastRoute` setting (settings.json via SettingsStore.SaveRoute/LoadRoute),
 * restored on launch by lib/routing.ts `restoreLastRoute`. A previous release
 * could have written a route that now points at a removed Settings tab. Today
 * `installRouteAuditing` saves `to.path` only (never the query), so the
 * expected stored value is plain "/settings" — but a hand-edited file, an
 * older writer, or a future change to saveRoute could hold the full URL, and
 * the restore must not land on a dead tab.
 *
 * Seeds lastRoute to each legacy URL and drives the REAL launch path
 * (restoreLastRoute over the real exported route tables, both bundles).
 */
import { describe, it, expect, vi, beforeAll } from 'vitest';
import { createMemoryHistory, createRouter, type RouteRecordRaw } from 'vue-router';
import { defineComponent, h } from 'vue';

vi.mock('@/App.vue', () => ({
  default: { name: 'AppStub', render: () => null },
}));

vi.mock('@/lib/harnessClient', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/harnessClient')>();
  const make = () => actual.createFakeHarnessClient();
  return { ...actual, createHarnessClient: make, createServedHarnessClient: make };
});

import { restoreLastRoute } from '@/lib/routing';
import { createFakeHarnessClient } from '@/lib/harnessClient';

let desktop: RouteRecordRaw[];
let served: RouteRecordRaw[];

beforeAll(async () => {
  document.body.innerHTML = '<div id="app"></div>';
  desktop = (await import('../main')).routes;
  document.body.innerHTML = '<div id="app"></div>';
  served = (await import('../main-served')).routes;
});

function stubbed(rs: RouteRecordRaw[]): RouteRecordRaw[] {
  const Stub = defineComponent({ render: () => h('div') });
  return rs.map((r) => ('component' in r && r.component ? { ...r, component: Stub } : r)) as RouteRecordRaw[];
}

async function launchWith(stored: string, which: 'desktop' | 'served') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: stubbed(which === 'desktop' ? desktop : served),
  });
  // Boot exactly as the app does: `/` first, then restore.
  await router.push('/');
  await router.isReady();
  const base = createFakeHarnessClient();
  const client = createFakeHarnessClient({
    settings: { ...base.settings, loadRoute: async () => stored },
  });
  await restoreLastRoute(router, client);
  return router.currentRoute.value;
}

describe.each(['desktop', 'served'] as const)('persisted lastRoute from a previous release (%s)', (which) => {
  it.each([
    ['/settings?tab=tasks', '/workflows', 'tasks'],
    ['/settings?tab=scheduledchats', '/workflows', 'schedules'],
    ['/settings?tab=workflows', '/workflows', undefined],
  ] as const)('lastRoute=%s restores to %s ?tab=%s', async (stored, path, tab) => {
    const r = await launchWith(stored, which);
    expect(r.path).toBe(path);
    expect(r.query.tab).toBe(tab);
  });

  it('the value saveRoute actually writes ("/settings", no query) still restores to Settings', async () => {
    const r = await launchWith('/settings', which);
    expect(r.fullPath).toBe('/settings');
  });

  it('a persisted bare "/sessions" (the old Sessions tab) still restores to the session surface', async () => {
    const r = await launchWith('/sessions', which);
    expect(r.name).toBe('sessions');
  });
});
