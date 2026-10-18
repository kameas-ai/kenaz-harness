/**
 * RailEntry.matchPrefix — nav-ia-sweep-01DOGF0F WP02, pin P-2.
 *
 * RailEntry used to compute `props.active ?? route.path === props.to`. Two
 * defects hid in that one line: the exact-path match never lit an entry on a
 * nested route, and Vue's Boolean prop casting turns an absent `active` into
 * `false`, so `??` never even reached the route comparison. The table below
 * drives a real router through nested paths and asserts which entry is
 * current.
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import RailEntry from '@/shell/RailEntry.vue';
import { railPathMatches, SETTINGS_HUB_PREFIXES } from '@/shell/railMatch';
import { GitBranch } from '@/shell/icons';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';

const Stub = defineComponent({ render: () => h('div') });

async function mountAt(path: string, props: Record<string, unknown>) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: Stub }],
  });
  await router.push(path);
  await router.isReady();
  const w = mount(RailEntry, {
    props: { label: 'X', icon: GitBranch, ...props },
    global: { plugins: [router] },
  });
  await flushPromises();
  return w;
}

function current(w: Awaited<ReturnType<typeof mountAt>>): boolean {
  return w.find('a,button').attributes('aria-current') === 'page';
}

describe('railPathMatches', () => {
  it.each([
    ['/workflows', '/workflows', true],
    ['/workflows/x', '/workflows', true],
    ['/workflowsX', '/workflows', false],
    ['/agentgraph/run/x/graph', '/agentgraph', true],
    ['/sessions/abc', '/agentgraph', false],
    ['/permissions/fs', SETTINGS_HUB_PREFIXES, true],
    ['/policy', SETTINGS_HUB_PREFIXES, true],
    ['/settings', SETTINGS_HUB_PREFIXES, true],
    ['/tools', SETTINGS_HUB_PREFIXES, false],
  ] as const)('%s vs %j → %s', (path, prefix, want) => {
    expect(railPathMatches(path, prefix)).toBe(want);
  });
});

describe('RailEntry active state (P-2)', () => {
  it.each([
    // [route, props, expected current]
    ['/agentgraph/run/x/graph', { to: '/agentgraph', matchPrefix: '/agentgraph' }, true],
    ['/agentgraph', { to: '/agentgraph', matchPrefix: '/agentgraph' }, true],
    ['/sessions/abc', { to: '/agentgraph', matchPrefix: '/agentgraph' }, false],
    ['/permissions/fs', { to: '/settings', matchPrefix: SETTINGS_HUB_PREFIXES }, true],
    ['/settings', { to: '/settings', matchPrefix: SETTINGS_HUB_PREFIXES }, true],
    ['/workflows', { to: '/workflows', matchPrefix: '/workflows' }, true],
    ['/memory', { to: '/workflows', matchPrefix: '/workflows' }, false],
    // No matchPrefix: exact match still works — the Boolean-cast regression.
    ['/tools', { to: '/tools' }, true],
    ['/tools/x', { to: '/tools' }, false],
  ] as const)('at %s with %j → current=%s', async (path, props, want) => {
    const w = await mountAt(path, props);
    expect(current(w)).toBe(want);
  });

  it('an explicit active prop overrides the route both ways', async () => {
    expect(current(await mountAt('/tools', { to: '/tools', active: false }))).toBe(false);
    expect(current(await mountAt('/memory', { to: '/tools', active: true }))).toBe(true);
  });
});

// Review follow-up: SETTINGS_HUB_PREFIXES is hand-kept. Tie it to the
// SettingsTabs rail so a new separately-routed Settings entry (one whose `to`
// is not a /settings?tab= URL) cannot be added without the rail's Settings
// entry learning to light for it.
describe('SETTINGS_HUB_PREFIXES covers every SettingsTabs route', () => {
  it('every non-?tab entry in SettingsTabs matches a hub prefix', async () => {
    const pushed: string[] = [];
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/:pathMatch(.*)*', component: Stub }],
    });
    router.beforeEach((to) => {
      pushed.push(to.path);
    });
    await router.push('/');
    await router.isReady();
    pushed.length = 0;
    const w = mount(SettingsTabs, { global: { plugins: [router] } });
    const items = w.findAll('[data-testid^="settings-tab-"]');
    expect(items.length).toBeGreaterThan(0);
    for (const item of items) {
      await item.trigger('click');
      await flushPromises();
    }
    expect(pushed.length).toBe(items.length);
    for (const path of pushed) {
      expect(railPathMatches(path, SETTINGS_HUB_PREFIXES), path).toBe(true);
    }
  });
});
