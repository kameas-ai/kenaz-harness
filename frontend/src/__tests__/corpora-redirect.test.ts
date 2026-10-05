/**
 * FR-007 (01NKNOW01) — /corpora redirect test.
 *
 * Asserts that /corpora and /corpora/:id land on the context library, which
 * knowledge-home-01DOGF0E made Knowledge › Curated (/knowledge/curated).
 *
 * Until knowledge-home WP02 this test built a hand-copied route table that
 * mirrored the redirect, so it could not see main.ts / main-served.ts drop or
 * change the record. It now drives BOTH entry points' real route tables.
 */
import { describe, it, expect, vi, beforeAll } from 'vitest';
import { createMemoryHistory, createRouter, type RouteRecordRaw } from 'vue-router';

vi.mock('@/App.vue', () => ({ default: { name: 'AppStub', render: () => null } }));
vi.mock('@/lib/harnessClient', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/harnessClient')>();
  const make = () => actual.createFakeHarnessClient();
  return { ...actual, createHarnessClient: make, createServedHarnessClient: make };
});

let desktop: RouteRecordRaw[];
let served: RouteRecordRaw[];

beforeAll(async () => {
  document.body.innerHTML = '<div id="app"></div>';
  desktop = (await import('../main')).routes;
  document.body.innerHTML = '<div id="app"></div>';
  served = (await import('../main-served')).routes;
});

describe('corpora → Knowledge › Curated redirect (FR-007)', () => {
  it.each(['desktop', 'served'] as const)('redirects every /corpora path (%s)', async (which) => {
    const routes = which === 'desktop' ? desktop : served;
    for (const from of ['/corpora', '/corpora/some-corpus-id', '/corpora/foo/bar/baz']) {
      const router = createRouter({ history: createMemoryHistory(), routes });
      await router.push(from);
      await router.isReady();
      expect(router.currentRoute.value.path, `${which} ${from}`).toBe('/knowledge/curated');
      expect(router.currentRoute.value.name, `${which} ${from}`).toBe('knowledge');
    }
  });
});
