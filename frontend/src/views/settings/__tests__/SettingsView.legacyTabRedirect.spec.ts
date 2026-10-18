/**
 * SettingsView.legacyTabRedirect — nav-ia-sweep-01DOGF0F WP05 (FR-4).
 *
 * The /settings beforeEnter redirect (lib/legacyRoutes.ts) cannot see a
 * query-only change while already on /settings — vue-router does not re-run
 * beforeEnter for it. SettingsView watches ?tab= and forwards the three moved
 * tabs itself. This pins that second half.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { reactive, nextTick } from 'vue';

const route = reactive<{ path: string; query: Record<string, string> }>({
  path: '/settings',
  query: {},
});
const replace = vi.fn(async () => undefined);
vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return { ...actual, useRoute: () => route, useRouter: () => ({ replace, push: vi.fn() }) };
});

import SettingsView from '@/views/settings/SettingsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

describe('SettingsView forwards moved tabs on a same-route query change', () => {
  it.each([
    ['tasks', { path: '/workflows', query: { tab: 'tasks' } }],
    ['scheduledchats', { path: '/workflows', query: { tab: 'schedules' } }],
    ['workflows', { path: '/workflows' }],
    // settings-cleanup-01SETUX01 WP01 (FR-2): retired developer tabs.
    ['flags', { path: '/settings' }],
    ['health', { path: '/settings' }],
    ['logs', { path: '/settings' }],
  ])('?tab=%s → %j', async (tab, want) => {
    route.query = {};
    replace.mockClear();
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() } },
    });
    await flushPromises();
    expect(replace).not.toHaveBeenCalled();

    route.query = { tab };
    await nextTick();
    expect(replace).toHaveBeenCalledWith(want);

    replace.mockClear();
    route.query = { tab: 'hooks' };
    await nextTick();
    expect(replace).not.toHaveBeenCalled();
    w.unmount();
  });
});
