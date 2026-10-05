/**
 * fleetSession.gates.spec.ts — fleet-session-truth-01DOGF0A WP05, P-10.
 *
 * Dogfood F10a: the org-promote affordance the owner went looking for was
 * hidden behind a capability snapshot ContextsView fetched once on mount,
 * while the rail's gates read boot AppInfo — two capability truths. Both now
 * read the one fleet-session store, so a capability arriving re-renders every
 * gate after ONE fleet:session-changed event.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import ContextsView from '@/views/contexts/ContextsView.vue';
import { createFakeHarnessClient, fakeFleetSession } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { _resetFleetSessionForTest } from '@/lib/fleetSession';
import { capability, initFeatureFlags, signedIn } from '@/lib/featureFlags';
import { dispatchServedEvent } from '@/lib/useServedEvents';
import type { FleetSessionView } from '@/lib/types';

function session(enabled: Record<string, boolean>): FleetSessionView {
  return fakeFleetSession({
    state: 'signed_in',
    capabilities: { tier: 'enterprise', enabled, fetchedAt: '', source: 'fleet' },
  });
}

async function mountBoth() {
  const base = createFakeHarnessClient();
  const client = createFakeHarnessClient({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    settings: { ...base.settings, fleetSession: async () => session({}) } as any,
  });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', name: 'sessions', component: defineComponent({ render: () => h('div') }) },
      { path: '/:p(.*)*', component: defineComponent({ render: () => h('div') }) },
    ],
  });
  await router.push('/sessions');
  await router.isReady();
  const Both = defineComponent({ render: () => h('div', [h(LeftRail), h(ContextsView)]) });
  const w = mount(Both, {
    global: { plugins: [router], provide: { [HarnessClientKey as symbol]: client } },
  });
  await flushPromises();
  return w;
}

describe('fleet capability gates share one truth (FR-8)', () => {
  beforeEach(() => {
    _resetFleetSessionForTest();
    initFeatureFlags(null);
  });
  afterEach(() => {
    _resetFleetSessionForTest();
    initFeatureFlags(null);
  });

  it('P-10: a capability added → ContextsView team gate AND LeftRail gate open after one event', async () => {
    const w = await mountBoth();
    expect(w.find('[data-testid=context-sync-status-strip]').exists()).toBe(false);
    expect(w.find('[data-testid=nav-sites]').exists()).toBe(false);

    dispatchServedEvent('fleet:session-changed', session({ shared_team_graph: true, sites_hosting: true }));
    await nextTick();

    expect(w.find('[data-testid=context-sync-status-strip]').exists()).toBe(true);
    expect(w.find('[data-testid=nav-sites]').exists()).toBe(true);
    expect(capability('shared_team_graph')).toBe(true);
  });

  it('a capability leaving closes both in the same tick', async () => {
    const w = await mountBoth();
    dispatchServedEvent('fleet:session-changed', session({ shared_team_graph: true, sites_hosting: true }));
    await nextTick();
    dispatchServedEvent('fleet:session-changed', session({}));
    await nextTick();
    expect(w.find('[data-testid=context-sync-status-strip]').exists()).toBe(false);
    expect(w.find('[data-testid=nav-sites]').exists()).toBe(false);
  });

  it('signed out → every gate closed even if a stale capability map lingers', async () => {
    await mountBoth();
    dispatchServedEvent(
      'fleet:session-changed',
      fakeFleetSession({
        state: 'signed_out',
        capabilities: { tier: '', enabled: { sites_hosting: true }, fetchedAt: '', source: 'fleet' },
      }),
    );
    await nextTick();
    expect(signedIn.value).toBe(false);
    expect(capability('sites_hosting')).toBe(false);
  });

  it('a real snapshot is never overwritten by a later boot AppInfo seed', async () => {
    await mountBoth();
    dispatchServedEvent('fleet:session-changed', session({ sites_hosting: true }));
    initFeatureFlags({
      build: 't',
      commit: 't',
      buildTime: '',
      goVersion: '',
      platform: 't',
      windowSize: { width: 1, height: 1 },
    });
    expect(capability('sites_hosting')).toBe(true);
  });
});
