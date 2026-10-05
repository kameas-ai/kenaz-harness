/**
 * legacySettingsRedirects — nav-ia-sweep-01DOGF0F WP05 (FR-4), pin P-3.
 *
 * Settings › Runtime (Scheduled Chats, Tasks) and Settings › Authoring ›
 * Workflows moved to the Workflows surface. Their old URLs must keep
 * working — bookmarks, the pre-WP05 chat-header chip, a persisted lastRoute —
 * so the /settings record carries a beforeEnter redirect in BOTH bundles.
 * Driven through the real exported route tables, not a hand-built router, so
 * dropping the guard from either entry point fails here.
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

let desktop: RouteRecordRaw[];
let served: RouteRecordRaw[];

beforeAll(async () => {
  document.body.innerHTML = '<div id="app"></div>';
  desktop = (await import('../main')).routes;
  document.body.innerHTML = '<div id="app"></div>';
  served = (await import('../main-served')).routes;
});

/** Swap lazy view components for a stub so navigation never loads a real view. */
function stubbed(rs: RouteRecordRaw[]): RouteRecordRaw[] {
  const Stub = defineComponent({ render: () => h('div') });
  return rs.map((r) => ('component' in r && r.component ? { ...r, component: Stub } : r)) as RouteRecordRaw[];
}

const LEGACY_CASES = [
  ['/settings?tab=tasks', '/workflows', 'tasks'],
  ['/settings?tab=scheduledchats', '/workflows', 'schedules'],
  ['/settings?tab=workflows', '/workflows', undefined],
] as const;

describe.each(['desktop', 'served'] as const)('legacy /settings?tab= redirects (%s)', (which) => {
  it.each(LEGACY_CASES)('%s → %s ?tab=%s', async (from, path, tab) => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: stubbed(which === 'desktop' ? desktop : served),
    });
    await router.push('/sessions');
    await router.isReady();
    await router.push(from);
    expect(router.currentRoute.value.path).toBe(path);
    expect(router.currentRoute.value.query.tab).toBe(tab);
  });

  it('leaves every other Settings tab alone', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: stubbed(which === 'desktop' ? desktop : served),
    });
    await router.push('/settings?tab=hooks');
    await router.isReady();
    expect(router.currentRoute.value.path).toBe('/settings');
    expect(router.currentRoute.value.query.tab).toBe('hooks');
    await router.push('/settings');
    expect(router.currentRoute.value.fullPath).toBe('/settings');
  });
});
