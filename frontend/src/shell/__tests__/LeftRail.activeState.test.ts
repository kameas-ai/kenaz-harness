/**
 * LeftRail.activeState — nav-ia-sweep-01DOGF0F WP02, pin P-2 at the rail level.
 *
 * With a chat open the session surface's active indicator is the selected
 * session ROW (aria-current="page"), and with a nested agent-graph route open
 * the "Agent graphs" surface entry is current. Before WP02 no surface entry
 * was ever current on any route (exact-path match + Boolean prop casting).
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import type { Session } from '@/lib/types';

const Stub = defineComponent({ render: () => h('div') });

const sessions: Session[] = [
  { id: 'abc', name: 'abc', createdAt: '', updatedAt: '' },
  { id: 'def', name: 'def', createdAt: '', updatedAt: '' },
];

async function mountRailAt(path: string) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', name: 'sessions', component: Stub },
      { path: '/projects/:id', name: 'project', component: Stub },
      { path: '/:pathMatch(.*)*', component: Stub },
    ],
  });
  await router.push(path);
  await router.isReady();
  const base = createFakeHarnessClient();
  const w = mount(LeftRail, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, {
              sessions: { ...base.sessions, list: async () => sessions },
            });
          },
        },
      ],
    },
  });
  await flushPromises();
  await nextTick();
  return w;
}

function surfaceCurrent(w: Awaited<ReturnType<typeof mountRailAt>>): string[] {
  return w
    .find('nav[aria-label="Surfaces"]')
    .findAll('[aria-current="page"]')
    .map((e) => e.attributes('aria-label') ?? '');
}

describe('LeftRail active state (P-2)', () => {
  it('marks the open session row current at /sessions/abc', async () => {
    const w = await mountRailAt('/sessions/abc');
    expect(w.find('[data-testid="open-session-abc"]').attributes('aria-current')).toBe('page');
    expect(w.find('[data-testid="open-session-def"]').attributes('aria-current')).toBeUndefined();
  });

  it.each([
    ['/agentgraph/run/x/graph', 'Agent graphs'],
    ['/workflows', 'Workflows'],
    ['/permissions/fs', 'Settings'],
    ['/settings', 'Settings'],
    ['/knowledge/curated', 'Knowledge'],
    ['/knowledge/learned', 'Knowledge'],
  ])('at %s exactly the %s surface entry is current', async (path, label) => {
    const w = await mountRailAt(path);
    expect(surfaceCurrent(w)).toEqual([label]);
  });
});
