/**
 * LeftRail.agentgraph-nav.test.ts — agentgraph-total-convergence-01PMGX01 WP16.
 *
 * The Agent graphs entry was demoted out of the left rail by
 * nav-settings-ia-cleanup WP03 and made command-palette-only. WP16 reverses
 * that: runs now materialize as graphs and the routed topology is the
 * production chat path, so the surface has real content in it.
 *
 * These tests pin BOTH halves of the WP's contract, because the failure mode
 * that motivated it is silent removal:
 *
 *   1. The rail entry exists, is unconditional (no capability / sign-in gate,
 *      unlike Sites and Marketplace), and routes to /agentgraph.
 *   2. The command-palette action was KEPT, not traded away for the rail
 *      entry. A rail entry and a Cmd+K action serve different reach.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { initFeatureFlags } from '@/lib/featureFlags';
import { useCommandPalette } from '@/lib/useCommandPalette';

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
        path: '/agentgraph',
        name: 'agentgraph',
        component: defineComponent({ render: () => h('div', 'agentgraph') }),
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

describe('LeftRail — Agent graphs nav (WP16)', () => {
  afterEach(() => {
    initFeatureFlags(null);
  });

  it('renders the Agent graphs rail entry', async () => {
    initFeatureFlags(null);
    const { w } = await mountRail();
    const navItem = w.find('[data-testid=nav-agentgraph]');
    expect(navItem.exists()).toBe(true);
    expect(navItem.text()).toContain('Agent graphs');
  });

  it('links the entry to /agentgraph', async () => {
    const { w } = await mountRail();
    const link = w.find('[data-testid=nav-agentgraph] a');
    expect(link.exists()).toBe(true);
    expect(link.attributes('href')).toContain('/agentgraph');
  });

  it('does not gate the entry behind sign-in or a capability', async () => {
    // Sites and Marketplace are both hidden for a signed-out user. The
    // graph editor is a local surface — gating it the same way would
    // re-create the unreachability WP16 exists to fix.
    initFeatureFlags(null);
    const { w } = await mountRail();
    expect(w.find('[data-testid=nav-agentgraph]').exists()).toBe(true);
    expect(w.find('[data-testid=nav-sites]').exists()).toBe(false);
    expect(w.find('[data-testid=nav-marketplace]').exists()).toBe(false);
  });

  it('keeps the command-palette action alongside the rail entry', () => {
    // useCommandPalette's onMounted/onBeforeUnmount hooks are no-ops
    // outside a component instance; `actions` is module-level state, so
    // reading it here is safe and does not require a mount.
    const { actions } = useCommandPalette();
    const action = actions.value.find((a) => a.id === 'nav.agentgraph');
    expect(action).toBeDefined();
    expect(action?.label).toContain('Agent graphs');
  });
});
