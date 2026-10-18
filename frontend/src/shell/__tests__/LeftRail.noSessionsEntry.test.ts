/**
 * LeftRail.noSessionsEntry — nav-ia-sweep-01DOGF0F WP03, pin P-1 (rail half).
 *
 * Owner ruling F4: the rail's session list is the sessions home, so the
 * "Sessions" surface entry is removed. The route half (/ → /sessions,
 * /sessions/:id) is pinned in __tests__/entrypoint.routes.test.ts.
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { useCommandPalette } from '@/lib/useCommandPalette';
import type { Session } from '@/lib/types';

const Stub = defineComponent({ render: () => h('div') });

async function mountRail() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', name: 'sessions', component: Stub },
      { path: '/projects/:id', name: 'project', component: Stub },
      { path: '/:pathMatch(.*)*', component: Stub },
    ],
  });
  await router.push('/sessions');
  await router.isReady();
  const sessions: Session[] = [{ id: 'abc', name: 'abc', createdAt: '', updatedAt: '' }];
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
  return { w, router };
}

describe('LeftRail without a Sessions surface entry (P-1)', () => {
  it('has no surface entry labelled or routed to Sessions', async () => {
    const { w } = await mountRail();
    const nav = w.find('nav[aria-label="Surfaces"]');
    expect(nav.exists()).toBe(true);
    expect(nav.find('[aria-label="Sessions"]').exists()).toBe(false);
    expect(nav.findAll('a').map((a) => a.attributes('href'))).not.toContain('/sessions');
  });

  it('still opens /sessions/:id from the rail session list', async () => {
    const { w, router } = await mountRail();
    await w.find('[data-testid="open-session-abc"]').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/sessions/abc');
  });

  it('the palette action no longer promises a session list', () => {
    const host = mount(
      defineComponent({
        setup() {
          return { palette: useCommandPalette() };
        },
        render: () => h('div'),
      }),
    );
    const a = (host.vm as unknown as { palette: ReturnType<typeof useCommandPalette> })
      .palette.actions.value.find((x) => x.id === 'nav.sessions');
    expect(a?.label).toBe('Go to Sessions home');
    expect(a?.hint).not.toMatch(/list/i);
    host.unmount();
  });
});
