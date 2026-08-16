/**
 * AccountPanel.capabilityRefresh.spec.ts — the mid-session half of the fleet
 * capability gate.
 *
 * Boot-time initialisation (see `src/__tests__/entrypoint.featureFlags.test.ts`)
 * is not enough on its own. `AccountPanel` mutates the fleet session long after
 * boot: sign-in creates one, sign-out destroys one, and "Refresh" re-enrols and
 * can change the tier the capability set is derived from. A boot-only write
 * would leave a user who signs in mid-session gated until they restart the app
 * — a quieter version of the same lie.
 *
 * Unlike `SyncPanel.spec.ts`, nothing here mocks `@/lib/featureFlags`. The
 * assertions read the real module, which is the only way to catch a missing
 * refresh call.
 *
 * (docs/dead-code-audit-2026-08-16.md finding A4, part 2)
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { defineComponent, h } from 'vue';
import { createMemoryHistory, createRouter } from 'vue-router';
import AccountPanel from '@/views/settings/AccountPanel.vue';
import SyncPanel from '@/views/settings/SyncPanel.vue';
import { createFakeHarnessClient, type HarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { initFeatureFlags, signedIn, capability } from '@/lib/featureFlags';
import type { AppInfo, FleetIdentity, FleetProfileInfo } from '@/lib/types';

const prodProfile: FleetProfileInfo = {
  name: 'prod',
  badgeColor: '',
  fleetBaseUrl: 'https://fleet.example.com',
  configured: true,
};

const mockIdentity: FleetIdentity = {
  userId: 'user-1',
  orgId: '42',
  teamId: 'team-1',
  email: 'alice@example.com',
  tier: 'team',
  orgName: 'Acme Corp',
  teamName: 'Engineering',
  roles: ['member'],
};

const CAPS = { context_sync: true, sites_hosting: true };

/**
 * A client that models the backend's actual session semantics:
 *
 *   - `appInfo()` carries the capability map only while a session exists,
 *     mirroring `core/rpc/api.go`'s `capView.Source != "default-deny"` guard.
 *   - `fleetRefreshCapabilities()` throws once the session is gone, mirroring
 *     `FleetSignOut` → `StopFleetBackground` → nil poller → ErrFleetDisabled.
 *
 * Both details matter: they are why sign-out has to fall through to AppInfo
 * rather than trusting the refresh RPC.
 */
function buildClient() {
  const base = createFakeHarnessClient();
  const server = { session: false };
  const client = {
    ...base,
    appInfo: async (): Promise<AppInfo> => ({
      build: 'test',
      commit: 'test',
      buildTime: '',
      goVersion: '',
      platform: 'test',
      windowSize: { width: 1280, height: 800 },
      ...(server.session ? { capabilities: { ...CAPS }, tier: 'team' as const } : {}),
    }),
    settings: {
      ...base.settings,
      fleetProfile: vi.fn(async () => prodProfile),
      fleetSignedIn: vi.fn(async () => server.session),
      fleetSignIn: vi.fn(async () => {
        server.session = true;
        return mockIdentity;
      }),
      fleetSignOut: vi.fn(async () => {
        server.session = false;
      }),
      fleetRefreshIdentity: vi.fn(async () => mockIdentity),
      fleetRefreshCapabilities: vi.fn(async () => {
        if (!server.session) throw new Error('fleet: disabled by env');
        return { tier: 'team', enabled: { ...CAPS }, fetchedAt: '', source: 'fleet' };
      }),
    },
  } as unknown as HarnessClient;
  return { client, server };
}

function mountPanel(component: unknown, client: HarnessClient) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/:pathMatch(.*)*', component: { render: () => null } }],
  });
  return mount(component as never, {
    global: {
      plugins: [router],
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
}

describe('AccountPanel — capability refresh on session transitions', () => {
  beforeEach(() => {
    // Boot state: app started signed out, so main.ts installed an AppInfo with
    // no capability map.
    initFeatureFlags(null);
  });

  it('opens the capability gates after a mid-session sign-in', async () => {
    const { client } = buildClient();
    const wrapper = mountPanel(AccountPanel, client);
    await flushPromises();

    expect(signedIn.value).toBe(false);
    expect(capability('sites_hosting')).toBe(false);

    await wrapper.find('[data-testid="sign-in-btn"]').trigger('click');
    await flushPromises();

    expect(signedIn.value).toBe(true);
    expect(capability('sites_hosting')).toBe(true);
    expect(capability('context_sync')).toBe(true);
  });

  it('forces a capability fetch rather than trusting the poller cache', async () => {
    // The poller's cached snapshot still holds the signed-out answer at this
    // point; without the forced Refresh, AppInfo would report no capabilities.
    const { client } = buildClient();
    const wrapper = mountPanel(AccountPanel, client);
    await flushPromises();

    await wrapper.find('[data-testid="sign-in-btn"]').trigger('click');
    await flushPromises();

    expect(client.settings.fleetRefreshCapabilities).toHaveBeenCalled();
  });

  it('closes the capability gates on sign-out', async () => {
    const { client, server } = buildClient();
    server.session = true;
    initFeatureFlags({
      build: 'test',
      commit: 'test',
      buildTime: '',
      goVersion: '',
      platform: 'test',
      windowSize: { width: 1280, height: 800 },
      capabilities: { ...CAPS },
      tier: 'team',
    });

    const wrapper = mountPanel(AccountPanel, client);
    await flushPromises();
    expect(signedIn.value).toBe(true);

    await wrapper.find('[data-testid="sign-out-btn"]').trigger('click');
    await flushPromises();

    // Fail-OPEN is the direction that matters here: leaving the snapshot behind
    // would keep Publish-to-team and the Sites nav on screen for a signed-out
    // user.
    expect(signedIn.value).toBe(false);
    expect(capability('sites_hosting')).toBe(false);
  });

  it('re-reads capabilities when the user hits Refresh', async () => {
    const { client, server } = buildClient();
    server.session = true;
    const wrapper = mountPanel(AccountPanel, client);
    await flushPromises();

    // Boot left the snapshot empty; Refresh must repair it.
    expect(signedIn.value).toBe(false);

    await wrapper.find('[data-testid="refresh-btn"]').trigger('click');
    await flushPromises();

    expect(signedIn.value).toBe(true);
  });
});

describe('Settings → Account and Settings → Sync agree with each other', () => {
  beforeEach(() => {
    initFeatureFlags(null);
  });

  /**
   * Both panels mount inside SettingsView. Before the gate was wired, Account
   * read a live RPC and correctly showed the user signed in, while Sync read
   * the never-populated module ref and told the same user, on the same screen,
   * to "Go to Settings → Account to sign in".
   */
  it('does not tell a signed-in user to sign in', async () => {
    const { client } = buildClient();
    const Both = defineComponent({
      components: { AccountPanel, SyncPanel },
      render: () => h('div', [h(AccountPanel), h(SyncPanel)]),
    });

    const wrapper = mountPanel(Both, client);
    await flushPromises();

    // Signed out: both panels agree the user is signed out.
    expect(wrapper.find('[data-testid="signed-out-panel"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sync-not-signed-in"]').exists()).toBe(true);

    await wrapper.find('[data-testid="sign-in-btn"]').trigger('click');
    await flushPromises();

    // Signed in: Account shows the identity card, and Sync must stop
    // contradicting it.
    expect(wrapper.find('[data-testid="identity-card"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sync-not-signed-in"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="sync-pro-gate"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="sync-panel"]').exists()).toBe(true);
  });
});
