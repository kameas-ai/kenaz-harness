/**
 * lastRoute.knowledgeRedirect — knowledge-home-01DOGF0E WP-PI (persistence
 * integrity).
 *
 * A previous release persisted `lastRoute` = "/contexts" or "/memory"
 * (settings.json via SettingsStore.SaveRoute/LoadRoute) when the user quit on
 * those surfaces. This mission retired both as homes in favour of
 * /knowledge/curated and /knowledge/learned. Seeds lastRoute to each legacy
 * value and drives the REAL launch path — lib/routing.ts restoreLastRoute over
 * the real exported route tables, both bundles — asserting the restore lands
 * in the right Knowledge section instead of the not-found page.
 *
 * The Go half (core/rpc/views/settings/knowledge_home_pi_test.go) pins that
 * the stored string round-trips verbatim through the real FileStore.
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
  await router.push('/');
  await router.isReady();
  const base = createFakeHarnessClient();
  const client = createFakeHarnessClient({
    settings: { ...base.settings, loadRoute: async () => stored },
  });
  await restoreLastRoute(router, client);
  return router.currentRoute.value;
}

describe.each(['desktop', 'served'] as const)('persisted lastRoute from before Knowledge (%s)', (which) => {
  it.each([
    ['/contexts', '/knowledge/curated', {}],
    ['/memory', '/knowledge/learned', {}],
    ['/memory?scopeKind=project&scopeId=p1', '/knowledge/learned', { scopeKind: 'project', scopeId: 'p1' }],
    ['/corpora', '/knowledge/curated', {}],
  ] as const)('lastRoute=%s restores to %s', async (stored, path, query) => {
    const r = await launchWith(stored, which);
    expect(r.path).toBe(path);
    expect(r.name).toBe('knowledge');
    expect(r.query).toEqual(query);
  });
});
