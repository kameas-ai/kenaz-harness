/**
 * SettingsView.accountHead.spec.ts — fleet-session-truth-01DOGF0A WP04, P-2.
 *
 * Dogfood 2026-10-04 F5: Settings › Account rendered "Sign in to access fleet
 * features…" in its section head ABOVE a signed-in panel body. The head was a
 * static string (SettingsView.vue SECTION_HEADS.account). It now derives from
 * the shared fleet session store (FR-10). Mounted through the real parent via
 * ?tab=account, against the real store.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';

vi.mock('vue-router', async (importOriginal) => {
  const actual = await importOriginal<typeof import('vue-router')>();
  return {
    ...actual,
    useRoute: () => ({ query: { tab: 'account' }, path: '/settings' }),
    useRouter: () => undefined,
  };
});
vi.mock('@/lib/useServedMode', async () => {
  const { readonly, ref } = await import('vue');
  const f = ref(false);
  return { isServedMode: () => f.value, useServedMode: () => readonly(f) };
});

import SettingsView from '@/views/settings/SettingsView.vue';
import { createFakeHarnessClient, fakeFleetSession } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { _resetFleetSessionForTest } from '@/lib/fleetSession';
import { dispatchServedEvent } from '@/lib/useServedEvents';
import type { FleetSessionView, Settings } from '@/lib/types';

function client(session: FleetSessionView) {
  const settings: Settings = {
    schemaVersion: 1,
    lastRoute: '/sessions',
    theme: 'dark',
    accent: 'default',
    windowSize: { width: 1280, height: 800 },
  };
  const base = createFakeHarnessClient();
  return createFakeHarnessClient({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    settings: {
      ...base.settings,
      get: async () => settings,
      loadRoute: async () => settings.lastRoute,
      loadTheme: async () => settings.theme,
      fleetSession: vi.fn(async () => session),
    } as any,
  });
}

const signedIn = fakeFleetSession({
  state: 'signed_in',
  identity: {
    userId: 'u',
    orgId: 'o',
    teamId: 't',
    email: 'alice@example.com',
    orgName: 'Acme Corp',
    tier: 'enterprise',
  },
  profile: { name: 'prod', badgeColor: '', fleetBaseUrl: 'https://f', configured: true },
});

describe('SettingsView — Account section head (FR-10)', () => {
  beforeEach(() => _resetFleetSessionForTest());
  afterEach(() => _resetFleetSessionForTest());

  it('P-2: signed-in store → the Account head does not say "Sign in to access"', async () => {
    const w = mount(SettingsView, {
      global: { provide: { [HarnessClientKey as symbol]: client(signedIn) } },
    });
    await flushPromises();
    expect(w.find('[data-testid="signed-in-panel"]').exists()).toBe(true);
    expect(w.text()).not.toContain('Sign in to access');
    expect(w.text()).toContain('Signed in as alice@example.com · Acme Corp');
  });

  it('signed-out store keeps the sign-in explainer, and follows a push to signed-in', async () => {
    const w = mount(SettingsView, {
      global: {
        provide: {
          [HarnessClientKey as symbol]: client(fakeFleetSession({ state: 'signed_out' })),
        },
      },
    });
    await flushPromises();
    expect(w.text()).toContain('Sign in to access fleet features');

    dispatchServedEvent('fleet:session-changed', signedIn);
    await flushPromises();
    expect(w.text()).not.toContain('Sign in to access');
  });
});
