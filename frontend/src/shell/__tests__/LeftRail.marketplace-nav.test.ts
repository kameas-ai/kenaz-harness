/**
 * LeftRail.marketplace-nav.test.ts — Marketplace nav entry gate tests.
 *
 * Verifies that the Marketplace nav item:
 *   - Is absent when not signed in (no capabilities)
 *   - Is present when signedIn is true (no capability gate beyond signedIn)
 *
 * Mirrors the pattern in LeftRail.sites-nav.test.ts.
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

async function mountRail() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/sessions/:id?',
        name: 'sessions',
        component: defineComponent({ render: () => h('div', 'sessions') }),
      },
      {
        path: '/marketplace',
        name: 'marketplace',
        component: defineComponent({ render: () => h('div', 'marketplace') }),
      },
    ],
  });
  await router.push('/sessions');
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

describe('LeftRail — Marketplace nav gating', () => {
  afterEach(() => {
    initFeatureFlags(null);
  });

  it('hides the Marketplace nav item when not signed in (no capabilities)', async () => {
    initFeatureFlags(null);
    const { w } = await mountRail();
    expect(w.find('[data-testid=nav-marketplace]').exists()).toBe(false);
  });

  it('shows the Marketplace nav item when signedIn is true', async () => {
    initFeatureFlags(makeAppInfo({ some_cap: true }));
    const { w } = await mountRail();
    await nextTick();
    const navItem = w.find('[data-testid=nav-marketplace]');
    expect(navItem.exists()).toBe(true);
    expect(navItem.text()).toContain('Marketplace');
  });
});
