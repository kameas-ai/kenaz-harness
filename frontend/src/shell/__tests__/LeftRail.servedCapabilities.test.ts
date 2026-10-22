/**
 * LeftRail — served capability map (audit findings HR-4 / HR-5, 2026-10-21).
 *
 * In served mode most nav entries were dead ends discovered by clicking in.
 * lib/servedCapabilities.ts now drives an `unavailable` state on RailEntry:
 * muted, "Desktop only", a tooltip with the reason, and no navigation.
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick, readonly, ref } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';

const servedFlag = ref(false);
vi.mock('@/lib/useServedMode', () => ({
  isServedMode: () => servedFlag.value,
  useServedMode: () => readonly(servedFlag),
}));

const Stub = defineComponent({ render: () => h('div') });

async function mountRail() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', name: 'sessions', component: Stub },
      { path: '/:pathMatch(.*)*', component: Stub },
    ],
  });
  await router.push('/sessions');
  await router.isReady();
  const w = mount(LeftRail, {
    global: { plugins: [router, { install: (app) => provideFakeClient(app) }] },
  });
  await flushPromises();
  await nextTick();
  return w;
}

function entry(w: Awaited<ReturnType<typeof mountRail>>, label: string) {
  const nav = w.get('nav[aria-label="Surfaces"]');
  const link = nav.findAll('a').find((a) => a.attributes('aria-label') === label);
  const dead = nav
    .findAll('[data-rail-unavailable="true"]')
    .find((e) => (e.attributes('aria-label') ?? '').startsWith(`${label} (`));
  return { link, dead };
}

describe('LeftRail — served capability map', () => {
  afterEach(() => {
    servedFlag.value = false;
  });

  it('desktop: every entry is a live link (control)', async () => {
    servedFlag.value = false;
    const w = await mountRail();
    for (const label of ['Capabilities', 'Workflows', 'Knowledge', 'Library', 'Settings']) {
      const { link, dead } = entry(w, label);
      expect(link, label).toBeDefined();
      expect(dead, label).toBeUndefined();
    }
    expect(entry(w, 'Library').link?.attributes('href')).toBe('/library/captured');
  });

  it('served: unavailable surfaces are marked Desktop only and do not navigate', async () => {
    servedFlag.value = true;
    const w = await mountRail();
    for (const label of ['Capabilities', 'Workflows', 'Knowledge']) {
      const { link, dead } = entry(w, label);
      expect(link, label).toBeUndefined();
      expect(dead, label).toBeDefined();
      expect(dead!.attributes('aria-disabled')).toBe('true');
      expect(dead!.attributes('title')).toMatch(/^Desktop only\. /);
      expect(dead!.text()).toContain('Desktop only');
    }
  });

  it('served: Library opens the view that works, Settings stays live', async () => {
    servedFlag.value = true;
    const w = await mountRail();
    expect(entry(w, 'Library').link?.attributes('href')).toBe('/library/authored');
    expect(entry(w, 'Settings').link).toBeDefined();
  });
});
