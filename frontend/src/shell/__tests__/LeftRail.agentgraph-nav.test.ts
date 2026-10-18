/**
 * LeftRail.agentgraph-nav.test.ts — where Agent graphs lives in the nav.
 *
 * History, because the failure mode here is silent re-demotion or
 * re-promotion:
 *   - nav-settings-ia-cleanup WP03 demoted Agent graphs to palette-only;
 *     19 of 34 node kinds rotted unnoticed while it was unreachable.
 *   - agentgraph-total-convergence-01PMGX01 WP16 restored the top-level rail
 *     entry as "where you go to see what the agent actually did".
 *   - agentgraph-settings-linkage-01DOGF0D kept that promise where the user
 *     already is: every chat turn links to its run graph and run details
 *     (TurnRunLinks, WP04 — pinned in MessageList.runLinks.test.ts and
 *     SessionsView.turnRuns.test.ts), and WP05 moved the library + editor
 *     under Settings › Authoring.
 *
 * Pin P-4 (FR-4): the rail has no Agent graphs entry; Settings › Authoring
 * has it; /agentgraph/edit/:id (and a run view) light it — both the
 * SettingsTabs entry and the rail's Settings entry. The palette action and
 * the /agentgraph route stay.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import SettingsTabs from '@/views/settings/SettingsTabs.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { initFeatureFlags } from '@/lib/featureFlags';
import { useCommandPalette } from '@/lib/useCommandPalette';

const Stub = defineComponent({ render: () => h('div') });

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', name: 'sessions', component: Stub },
      { path: '/projects/:id', name: 'project', component: Stub },
      { path: '/agentgraph', name: 'graphs', component: Stub },
      { path: '/agentgraph/edit/:id', name: 'graph-editor', component: Stub },
      { path: '/agentgraph/run/:runId', name: 'graph-run', component: Stub },
      { path: '/agentgraph/run/:runId/graph', name: 'graph-materialized', component: Stub },
      { path: '/:pathMatch(.*)*', component: Stub },
    ],
  });
}

async function mountRailAt(path: string) {
  const router = makeRouter();
  await router.push(path);
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

async function mountTabsAt(path: string) {
  const router = makeRouter();
  await router.push(path);
  await router.isReady();
  const w = mount(SettingsTabs, { global: { plugins: [router] } });
  await flushPromises();
  return { w, router };
}

describe('Agent graphs nav (agentgraph-settings-linkage-01DOGF0D WP05, P-4)', () => {
  afterEach(() => {
    initFeatureFlags(null);
  });

  it('the left rail has no Agent graphs entry', async () => {
    initFeatureFlags(null);
    const w = await mountRailAt('/sessions');
    expect(w.find('[data-testid=nav-agentgraph]').exists()).toBe(false);
    const surfaces = w.find('nav[aria-label="Surfaces"]');
    expect(surfaces.text()).not.toContain('Agent graphs');
    expect(surfaces.find('a[href$="/agentgraph"]').exists()).toBe(false);
  });

  it('Settings › Authoring has the Agent graphs entry, routing to /agentgraph', async () => {
    const { w, router } = await mountTabsAt('/settings');
    const authoring = w.find('li[aria-label="Authoring"]');
    expect(authoring.exists()).toBe(true);
    const entry = authoring.find('[data-testid="settings-tab-agent-graphs"]');
    expect(entry.exists(), 'Authoring › Agent graphs').toBe(true);
    expect(entry.text()).toContain('Agent graphs');

    await entry.trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.name).toBe('graphs');
    expect(router.currentRoute.value.path).toBe('/agentgraph');
  });

  it.each([
    '/agentgraph',
    '/agentgraph/edit/my-graph',
    '/agentgraph/run/chat-01J/graph',
    '/agentgraph/run/chat-01J',
  ])('at %s the Settings › Agent graphs entry is current', async (path) => {
    const { w } = await mountTabsAt(path);
    const current = w.findAll('[aria-current="page"]').map((e) => e.attributes('data-testid'));
    expect(current).toEqual(['settings-tab-agent-graphs']);
  });

  it.each(['/agentgraph', '/agentgraph/edit/my-graph', '/agentgraph/run/chat-01J/graph'])(
    'at %s the rail lights exactly its Settings entry',
    async (path) => {
      const w = await mountRailAt(path);
      const current = w
        .find('nav[aria-label="Surfaces"]')
        .findAll('[aria-current="page"]')
        .map((e) => e.attributes('aria-label') ?? '');
      expect(current).toEqual(['Settings']);
    },
  );

  it('keeps the command-palette action, routing to the same library', () => {
    // useCommandPalette's onMounted/onBeforeUnmount hooks are no-ops
    // outside a component instance; `actions` is module-level state.
    const { actions } = useCommandPalette();
    const action = actions.value.find((a) => a.id === 'nav.agentgraph');
    expect(action).toBeDefined();
    expect(action?.label).toContain('Agent graphs');
    // Honest copy: the library lives under Settings; it is not where a
    // conversation's graph is shown (that is the per-turn link).
    expect(action?.hint).toContain('Settings');
    expect(action?.hint ?? '').not.toMatch(/each conversation/i);
  });
});
