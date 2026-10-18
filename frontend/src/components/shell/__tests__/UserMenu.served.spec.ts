/**
 * UserMenu in served mode — fleet-session-truth-01DOGF0A review F7: no dead
 * controls. The host broker owns the session; Retry / Sign in / Sign out /
 * Cancel would call methods served mode does not answer.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';

vi.mock('@/lib/useServedMode', async () => {
  const { readonly, ref } = await import('vue');
  const f = ref(true);
  return { isServedMode: () => f.value, useServedMode: () => readonly(f) };
});

import UserMenu from '../UserMenu.vue';
import { createFakeHarnessClient, fakeFleetSession } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { _resetFleetSessionForTest } from '@/lib/fleetSession';

describe('UserMenu — served mode (review F7)', () => {
  beforeEach(() => _resetFleetSessionForTest());

  it('degraded in served mode shows identity + reason but no Retry / sign-out', async () => {
    const base = createFakeHarnessClient();
    const client = createFakeHarnessClient({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      settings: {
        ...base.settings,
        fleetSession: async () =>
          fakeFleetSession({
            state: 'degraded',
            reason: 'network',
            tokensUsable: true,
            identity: { userId: 'u', orgId: 'o', teamId: 't', email: 'a@b.c' },
          }),
      } as any,
    });
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div/>' } }] });
    const w = mount(UserMenu, { global: { plugins: [router], provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    await w.find('[data-testid="user-menu-trigger"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="user-menu-degraded"]').exists()).toBe(true);
    expect(w.find('[data-testid="menu-retry"]').exists()).toBe(false);
    expect(w.find('[data-testid="menu-sign-out"]').exists()).toBe(false);
    expect(w.find('[data-testid="menu-sign-in"]').exists()).toBe(false);
  });
});
