/**
 * LeftRail — one Knowledge entry (knowledge-home-01DOGF0E WP02, pin P-1).
 * The separate Contexts and Memory entries are gone; the Knowledge entry is
 * highlighted on either section. The final surface list is pinned in order
 * so a later mission that re-adds a top-level entry has to say so here.
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
      { path: '/knowledge/:section(curated|learned)', name: 'knowledge', component: stub },
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

describe('LeftRail — Knowledge entry (P-1)', () => {
  it('has exactly one Knowledge entry and no Contexts/Memory entries', async () => {
    const w = await mountRailAt('/sessions');
    const surfaces = w.find('nav[aria-label="Surfaces"]');
    expect(surfaces.findAll('[aria-label="Knowledge"]')).toHaveLength(1);
    expect(surfaces.find('[aria-label="Contexts"]').exists()).toBe(false);
    expect(surfaces.find('[aria-label="Memory"]').exists()).toBe(false);
    expect(w.find('[data-testid="nav-knowledge"] a').attributes('href')).toContain('/knowledge/curated');
    w.unmount();
  });

  it('pins the desktop surface list in order', async () => {
    const w = await mountRailAt('/sessions');
    const labels = w
      .find('nav[aria-label="Surfaces"]')
      .findAll('a[aria-label]')
      .map((a) => a.attributes('aria-label'));
    expect(labels.slice(0, 4)).toEqual(['Tools', 'Workflows', 'Knowledge', 'Library']);
    expect(labels).not.toContain('Contexts');
    expect(labels).not.toContain('Memory');
    w.unmount();
  });

  it.each(['/knowledge/curated', '/knowledge/learned'])('is active on %s', async (path) => {
    const w = await mountRailAt(path);
    expect(w.find('[aria-label="Knowledge"]').attributes('aria-current')).toBe('page');
    w.unmount();
  });
});
