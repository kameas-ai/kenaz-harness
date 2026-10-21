/**
 * UserMenu in served mode — fleet-session-truth-01DOGF0A review F7: no dead
 * controls. The host broker owns the session; Retry / Sign in / Sign out /
 * Cancel would call methods served mode does not answer.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
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
import { _resetHostSessionForTest } from '@/lib/hostSession';

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

// ── kenaz audit HR-8: host identity from the Kenaz workbench chrome ─────────

describe('UserMenu — served mode host identity (HR-8)', () => {
  let frame: HTMLIFrameElement;
  let fakeParent: Window;

  beforeEach(() => {
    _resetFleetSessionForTest();
    _resetHostSessionForTest();
    frame = document.createElement('iframe');
    document.body.appendChild(frame);
    fakeParent = frame.contentWindow as Window;
    Object.defineProperty(window, 'parent', { value: fakeParent, configurable: true });
  });

  afterEach(() => {
    _resetHostSessionForTest();
    Object.defineProperty(window, 'parent', { value: window, configurable: true });
    frame.remove();
  });

  function mountSignedOut() {
    const base = createFakeHarnessClient();
    const client = createFakeHarnessClient({
      settings: {
        ...base.settings,
        fleetSession: async () => fakeFleetSession({ state: 'signed_out', tokensUsable: false }),
      },
    });
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/:p(.*)*', component: { template: '<div/>' } }] });
    return mount(UserMenu, { global: { plugins: [router], provide: { [HarnessClientKey as symbol]: client } } });
  }

  function post(data: unknown) {
    window.dispatchEvent(new MessageEvent('message', { data, source: fakeParent }));
  }

  it('shows the host identity instead of reading as signed out', async () => {
    const w = mountSignedOut();
    await flushPromises();
    post({ kind: 'kameas/host-session', signed_in: true, display_name: 'Nick Wisner', org: 'Kameas', plan: 'Enterprise' });
    await flushPromises();
    expect(w.find('[data-testid="user-menu-host-avatar"]').text()).toBe('NW');
    expect(w.find('[data-testid="user-menu-trigger"]').attributes('aria-label')).toBe('Account (Nick Wisner)');
    await w.find('[data-testid="user-menu-trigger"]').trigger('click');
    await flushPromises();
    const id = w.find('[data-testid="user-menu-host-identity"]');
    expect(id.exists()).toBe(true);
    expect(id.text()).toContain('Nick Wisner');
    expect(id.text()).toContain('Kameas');
    expect(id.text()).toContain('Enterprise');
    expect(id.text()).toContain('Signed in through Kenaz');
    expect(w.find('[data-testid="menu-sign-in"]').exists()).toBe(false);
  });

  it('says where to sign in when the host is signed out', async () => {
    const w = mountSignedOut();
    await flushPromises();
    post({ kind: 'kameas/host-session', signed_in: false, display_name: '', org: '', plan: '' });
    await flushPromises();
    await w.find('[data-testid="user-menu-trigger"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="user-menu-host-signed-out"]').text()).toContain('Sign in from the Kenaz app');
    expect(w.find('[data-testid="user-menu-host-identity"]').exists()).toBe(false);
    expect(w.find('[data-testid="menu-sign-in"]').exists()).toBe(false);
  });
});
