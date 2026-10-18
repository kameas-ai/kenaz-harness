/**
 * LeftRail.capabilities-nav.test.ts — ONE rail entry replaces Tools +
 * Marketplace (install-framework-01DOGF0B Phase 4 WP09; decision record §3,
 * executed early by owner ruling 2026-10-05).
 *
 * Was LeftRail.marketplace-nav.test.ts, which pinned the Marketplace entry's
 * signed-in gate. That entry is gone; this pins its replacement:
 *   - one "Capabilities" entry to /tools, shown signed in AND signed out
 *     (local providers work signed out; the fleet catalog shows reason rows
 *     inside the page — P-5 — instead of the rail hiding a whole surface);
 *   - no "Tools" entry and no Marketplace entry, signed in or not;
 *   - the entry stays lit on /tools deep links (?kind=…).
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { initFeatureFlags } from '@/lib/featureFlags';
import type { AppInfo } from '@/lib/types';

function makeAppInfo(caps: Record<string, boolean>): AppInfo {
  return {
    build: 'test',
    commit: 'test',
    buildTime: '',
    goVersion: '',
    platform: 'test',
    windowSize: { width: 1280, height: 800 },
    capabilities: caps,
  };
}

async function mountRail(at = '/sessions') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/sessions/:id?',
        name: 'sessions',
        component: defineComponent({ render: () => h('div', 'sessions') }),
      },
      {
        path: '/tools',
        name: 'tools',
        component: defineComponent({ render: () => h('div', 'tools') }),
      },
    ],
  });
  await router.push(at);
  await router.isReady();
  const w = mount(LeftRail, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app);
          },
        },
      ],
    },
  });
  await flushPromises();
  await nextTick();
  return { w, router };
}

describe('LeftRail — one Capabilities entry (WP09)', () => {
  afterEach(() => {
    initFeatureFlags(null);
  });

  it.each([
    ['signed out', null],
    ['signed in', makeAppInfo({ some_cap: true })],
  ] as const)('%s: one Capabilities entry to /tools; no Tools or Marketplace entry', async (_label, info) => {
    initFeatureFlags(info);
    const { w } = await mountRail();
    await nextTick();
    const entry = w.get('[data-testid=nav-capabilities]');
    expect(entry.text()).toContain('Capabilities');
    expect(entry.get('a').attributes('href')).toContain('/tools');
    expect(w.find('[data-testid=nav-marketplace]').exists()).toBe(false);
    const labels = w.findAll('nav[aria-label=Surfaces] a').map((a) => a.text().trim());
    expect(labels).not.toContain('Tools');
    expect(labels).not.toContain('Marketplace');
    expect(labels.filter((l) => l === 'Capabilities')).toHaveLength(1);
  });

  it('stays active on a /tools deep link (?kind=…)', async () => {
    const { w } = await mountRail('/tools?kind=bundle');
    await nextTick();
    expect(w.get('[data-testid=nav-capabilities] a').attributes('aria-current')).toBe('page');
  });
});
