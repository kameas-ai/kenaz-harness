/**
 * LeftRail.served-fleet-nav.test.ts — the fleet nav entries are desktop-only.
 *
 * `main-served.ts` registers neither `/sites` nor `/marketplace`, and the
 * served RPC allowlist (`core/serve/methods.go`) dispatches no `Sites_*` or
 * `Catalog_*` method, so both views are non-functional in a browser build.
 * Wiring the capability gate made these two entries render for the first time
 * ever — including in served mode, where they would have been a link into the
 * not-found page.
 *
 * (docs/dead-code-audit-2026-08-16.md findings A4 + B4)
 */
import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick, readonly, ref } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { initFeatureFlags } from '@/lib/featureFlags';
import type { AppInfo } from '@/lib/types';

// Served mode. test-setup.ts stubs window.go.rpc.Bindings so the default is
// desktop; this flips it for the whole module graph under test.
vi.mock('@/lib/useServedMode', () => {
  const served = ref(true);
  return {
    isServedMode: () => served.value,
    useServedMode: () => readonly(served),
  };
});

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
        path: '/:pathMatch(.*)*',
        name: 'not-found',
        component: defineComponent({ render: () => h('div', 'not found') }),
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
  return w;
}

describe('LeftRail — fleet nav in served mode', () => {
  afterEach(() => {
    initFeatureFlags(null);
  });

  it('hides Sites even when the capability is granted', async () => {
    initFeatureFlags(makeAppInfo({ sites_hosting: true }));
    const w = await mountRail();
    expect(w.find('[data-testid=nav-sites]').exists()).toBe(false);
  });

  it('hides Marketplace even when the user is signed in', async () => {
    initFeatureFlags(makeAppInfo({ sites_hosting: true }));
    const w = await mountRail();
    expect(w.find('[data-testid=nav-marketplace]').exists()).toBe(false);
  });

  it('still renders the surfaces served mode does support', async () => {
    // A sanity check that the rail itself mounted — otherwise the two
    // assertions above would pass for the wrong reason.
    initFeatureFlags(makeAppInfo({ sites_hosting: true }));
    const w = await mountRail();
    expect(w.text()).toContain('Sessions');
  });

  // served-mode-is-a-real-mode-01PMZ707 WP03 (AC-709). Graph_* has no
  // serve dispatch case (D-701) — the rail entry would route into
  // GraphsView.vue's own boundary panel, so it is hidden at the nav layer
  // too, mirroring Sites/Marketplace above.
  it('hides Agent graphs — Graph_* has no serve dispatch case', async () => {
    initFeatureFlags(makeAppInfo({}));
    const w = await mountRail();
    expect(w.find('[data-testid=nav-agentgraph]').exists()).toBe(false);
  });
});
