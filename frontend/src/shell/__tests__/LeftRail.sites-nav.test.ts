/**
 * LeftRail.sites-nav.test.ts — sites_hosting capability gate tests.
 *
 * Verifies that the Sites nav item:
 *   - Is absent when sites_hosting capability is missing (default)
 *   - Is present when signedIn AND capability('sites_hosting') is true
 *
 * Uses initFeatureFlags() to set the appInfo state before each test,
 * matching how the real app populates the capability snapshot.
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
        path: '/projects/:id',
        name: 'project',
        component: defineComponent({ render: () => h('div', 'project') }),
      },
      {
        path: '/sites',
        name: 'sites',
        component: defineComponent({ render: () => h('div', 'sites') }),
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

describe('LeftRail — Sites nav gating', () => {
  afterEach(() => {
    // Reset featureFlags state between tests.
    initFeatureFlags(null);
  });

  it('hides the Sites nav item when not signed in (no capabilities)', async () => {
    initFeatureFlags(null);
    const { w } = await mountRail();
    expect(w.find('[data-testid=nav-sites]').exists()).toBe(false);
  });

  it('hides the Sites nav item when signed in but sites_hosting is absent', async () => {
    // signedIn=true but sites_hosting not in capabilities map
    initFeatureFlags(makeAppInfo({ other_capability: true }));
    const { w } = await mountRail();
    await nextTick();
    expect(w.find('[data-testid=nav-sites]').exists()).toBe(false);
  });

  it('hides the Sites nav item when sites_hosting is explicitly false', async () => {
    initFeatureFlags(makeAppInfo({ sites_hosting: false }));
    const { w } = await mountRail();
    await nextTick();
    expect(w.find('[data-testid=nav-sites]').exists()).toBe(false);
  });

  it('shows the Sites nav item when signedIn AND sites_hosting is true', async () => {
    initFeatureFlags(makeAppInfo({ sites_hosting: true }));
    const { w } = await mountRail();
    await nextTick();
    const navItem = w.find('[data-testid=nav-sites]');
    expect(navItem.exists()).toBe(true);
    expect(navItem.text()).toContain('Sites');
  });
});
