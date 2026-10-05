/**
 * LeftRail — one Library entry (artifacts-as-units-01DOGF0C WP06, P-7).
 * The separate Artifacts and Documents entries are gone; the Library entry
 * is highlighted on either of its views.
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';

async function mountRailAt(path: string) {
  const stub = defineComponent({ render: () => h('div') });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', name: 'sessions', component: stub },
      { path: '/library/:view(captured|authored)', name: 'library', component: stub },
    ],
  });
  await router.push(path);
  await router.isReady();
  const w = mount(LeftRail, {
    global: { plugins: [router, { install(app) { provideFakeClient(app); } }] },
  });
  await flushPromises();
  await nextTick();
  return w;
}

describe('LeftRail — Library entry', () => {
  it('has exactly one Library entry and no Artifacts/Documents entries', async () => {
    const w = await mountRailAt('/sessions');
    const surfaces = w.find('nav[aria-label="Surfaces"]');
    expect(surfaces.findAll('[aria-label="Library"]')).toHaveLength(1);
    expect(surfaces.find('[aria-label="Artifacts"]').exists()).toBe(false);
    expect(surfaces.find('[aria-label="Documents"]').exists()).toBe(false);
    expect(w.find('[data-testid="nav-library"] a').attributes('href')).toContain('/library/captured');
    w.unmount();
  });

  it.each(['/library/captured', '/library/authored'])('is active on %s', async (path) => {
    const w = await mountRailAt(path);
    expect(w.find('[aria-label="Library"]').attributes('aria-current')).toBe('page');
    w.unmount();
  });
});

describe('LeftRail — project delete warns about artifacts (artifacts-as-units-01DOGF0C)', () => {
  it('counts the project-scoped artifacts the delete will remove', async () => {
    const stub = defineComponent({ render: () => h('div') });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/sessions/:id?', name: 'sessions', component: stub }],
    });
    await router.push('/sessions');
    await router.isReady();
    let seenFilter: unknown;
    const w = mount(LeftRail, {
      global: {
        plugins: [router, {
          install(app) {
            provideFakeClient(app, {
              projects: {
                list: async () => [{ id: 'p1', name: 'Alpha', description: '', createdAt: '', updatedAt: '' }],
                remove: async () => undefined,
                listSessions: async () => [],
              } as any,
              artifacts: {
                list: async (f: unknown) => { seenFilter = f; return [{ id: 'a' }, { id: 'b' }]; },
              } as any,
            });
          },
        }],
      },
    });
    await flushPromises();
    await w.find('[data-testid="project-header-p1"]').trigger('contextmenu');
    await nextTick();
    await w.find('[data-testid="project-menu-delete-p1"]').trigger('click');
    await flushPromises();
    expect(seenFilter).toEqual({ projectId: 'p1', scopeKind: 'project' });
    expect(w.find('[data-testid="delete-project-artifacts-warning"]').text()).toContain('2 artifacts promoted to this project will be permanently deleted');
    w.unmount();
  });
});
