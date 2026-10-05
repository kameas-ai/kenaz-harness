/**
 * UserMenu — status pill + fleet-identity popover tests.
 *
 * fleet-session-truth-01DOGF0A WP03: UserMenu renders the shared fleet
 * session store (lib/fleetSession.ts) and runs no poll of its own. These
 * drive the REAL store through a fake client (`fleetSession()` snapshots)
 * and a fake event source (the served event bus `useEventStream` falls back
 * to when no Wails runtime is present).
 *
 * Regression pins (spec §4):
 *   P-1  tokens valid + enroll fails → identity + degraded + Retry, no "Sign in"
 *   P-3  not-provisioned (auto-retry stopped) → a fleet:session-changed from
 *        elsewhere updates the popover without remount
 *   P-4  sign-in via UserMenu → LeftRail Marketplace entry appears
 *   P-9  enroll with empty email → header + avatar fall back, never blank
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import UserMenu from '../UserMenu.vue';
import LeftRail from '@/shell/LeftRail.vue';
import { createFakeHarnessClient, fakeFleetSession } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { _resetFleetSessionForTest } from '@/lib/fleetSession';
import { initFeatureFlags } from '@/lib/featureFlags';
import { dispatchServedEvent } from '@/lib/useServedEvents';
import type { AppInfo, FleetIdentity, FleetProfileInfo, FleetSessionView } from '@/lib/types';

const prodProfile: FleetProfileInfo = {
  name: 'prod',
  badgeColor: '',
  fleetBaseUrl: 'https://fleet.example.com',
  configured: true,
};

const devProfile: FleetProfileInfo = {
  name: 'dev',
  badgeColor: 'yellow',
  fleetBaseUrl: 'https://dev.fleet.example.com',
  configured: true,
};

const aliceIdentity: FleetIdentity = {
  userId: 'user-1',
  orgId: '42',
  teamId: 'team-1',
  email: 'alice@example.com',
  displayName: 'Alice Cooper',
  tier: 'pro',
  orgName: 'Acme Corp',
  teamName: 'Engineering',
  roles: ['member'],
};

function signedIn(over: Partial<FleetSessionView> = {}): FleetSessionView {
  return fakeFleetSession({
    state: 'signed_in',
    identity: aliceIdentity,
    identitySource: 'enroll',
    profile: prodProfile,
    claims: { hasSubject: true, hasOrgClaim: true },
    capabilities: { tier: 'pro', enabled: { sites_hosting: true }, fetchedAt: '', source: 'fleet' },
    ...over,
  });
}

function signedOut(): FleetSessionView {
  return fakeFleetSession({ state: 'signed_out', profile: prodProfile });
}

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

/** A client whose snapshot is whatever `current` holds. */
function buildClient(initial: FleetSessionView) {
  let current = initial;
  const client = createFakeHarnessClient({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    settings: {
      fleetSession: vi.fn(async () => current),
      fleetProfile: vi.fn(async () => prodProfile),
      fleetSignedIn: vi.fn(async () => current.state !== 'signed_out'),
      fleetSignIn: vi.fn(async () => {
        current = signedIn();
        return aliceIdentity;
      }),
      fleetSignOut: vi.fn(async () => {
        current = signedOut();
      }),
      fleetRefreshIdentity: vi.fn(async () => aliceIdentity),
      fleetRefreshCapabilities: vi.fn(async () => current.capabilities),
      fleetSignInCancel: vi.fn(async () => {
        current = signedOut();
      }),
    } as any,
    appInfo: vi.fn(async () =>
      makeAppInfo(current.state === 'signed_out' ? {} : current.capabilities.enabled),
    ),
  });
  return { client };
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/settings', component: { template: '<div />' } },
      {
        path: '/sessions/:id?',
        name: 'sessions',
        component: defineComponent({ render: () => h('div', 'sessions') }),
      },
      {
        path: '/marketplace',
        name: 'marketplace',
        component: defineComponent({ render: () => h('div', 'marketplace') }),
      },
    ],
  });
}

function mountUserMenu(client: ReturnType<typeof createFakeHarnessClient>) {
  const router = makeRouter();
  return mount(UserMenu, {
    global: {
      plugins: [router],
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
}

async function openPopover(wrapper: ReturnType<typeof mountUserMenu>) {
  await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
  await flushPromises();
  return wrapper.find('[data-testid="user-menu-popover"]');
}

describe('UserMenu', () => {
  beforeEach(() => {
    _resetFleetSessionForTest();
    initFeatureFlags(null);
  });
  afterEach(() => {
    _resetFleetSessionForTest();
    initFeatureFlags(null);
    vi.useRealTimers();
  });

  // ── Trigger render ─────────────────────────────────────────────────────

  it('trigger renders when fleet is enabled (signed-out)', async () => {
    const wrapper = mountUserMenu(buildClient(signedOut()).client);
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-trigger"]').exists()).toBe(true);
  });

  it('renders nothing when fleet is disabled (HARNESS_FLEET_DISABLED=1)', async () => {
    const wrapper = mountUserMenu(buildClient(fakeFleetSession({ state: 'disabled' })).client);
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-trigger"]').exists()).toBe(false);
  });

  it('renders avatar with initials when signed in', async () => {
    const wrapper = mountUserMenu(buildClient(signedIn()).client);
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-trigger"]').text()).toContain('AC');
  });

  it('renders env badge for non-prod profile', async () => {
    const wrapper = mountUserMenu(buildClient(signedIn({ profile: devProfile })).client);
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-trigger"]').text()).toContain('DEV');
  });

  it('removed non-account rows are NOT present', async () => {
    for (const v of [signedOut(), signedIn()]) {
      _resetFleetSessionForTest();
      const wrapper = mountUserMenu(buildClient(v).client);
      await flushPromises();
      await openPopover(wrapper);
      expect(wrapper.find('[data-testid="menu-search"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="menu-command-palette"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="menu-theme"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="menu-update"]').exists()).toBe(false);
    }
  });

  // ── Account rows ────────────────────────────────────────────────────────

  it('signed-out: shows Sign in row only', async () => {
    const wrapper = mountUserMenu(buildClient(signedOut()).client);
    await flushPromises();
    await openPopover(wrapper);
    expect(wrapper.find('[data-testid="menu-sign-in"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="menu-account"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="menu-sign-out"]').exists()).toBe(false);
  });

  it('signed-in: identity header + Account settings + Sign out', async () => {
    const wrapper = mountUserMenu(buildClient(signedIn()).client);
    await flushPromises();
    const popover = await openPopover(wrapper);
    expect(wrapper.find('[data-testid="menu-sign-in"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="menu-account"]').text()).toContain('Account settings');
    expect(wrapper.find('[data-testid="menu-sign-out"]').exists()).toBe(true);
    expect(popover.text()).toContain('alice@example.com');
    expect(popover.text()).toContain('Acme Corp');
    expect(popover.text()).toContain('pro');
  });

  it('"Sign in" row calls fleetSignIn directly (no route detour)', async () => {
    const { client } = buildClient(signedOut());
    const wrapper = mountUserMenu(client);
    await flushPromises();
    await openPopover(wrapper);
    await wrapper.find('[data-testid="menu-sign-in"]').trigger('click');
    await flushPromises();
    expect(client.settings.fleetSignIn).toHaveBeenCalledOnce();
  });

  it('signs out when "Sign out" is clicked and renders the signed-out state', async () => {
    const { client } = buildClient(signedIn());
    const wrapper = mountUserMenu(client);
    await flushPromises();
    await openPopover(wrapper);
    await wrapper.find('[data-testid="menu-sign-out"]').trigger('click');
    await flushPromises();
    expect(client.settings.fleetSignOut).toHaveBeenCalledOnce();
    expect(wrapper.find('[data-testid="user-menu-avatar"]').exists()).toBe(false);
  });

  // ── FR-2: no per-component poll ─────────────────────────────────────────

  it('runs no identity poll of its own (the backend owns the cadence)', async () => {
    vi.useFakeTimers();
    const { client } = buildClient(signedIn());
    mountUserMenu(client);
    await flushPromises();
    await vi.advanceTimersByTimeAsync(60 * 60 * 1000);
    expect(client.settings.fleetRefreshIdentity).not.toHaveBeenCalled();
  });

  // ── P-1 ─────────────────────────────────────────────────────────────────

  it('P-1: tokens valid + enroll network error → identity (cached) + degraded + Retry, no "Sign in"', async () => {
    const { client } = buildClient(
      signedIn({
        state: 'degraded',
        reason: 'network',
        message: 'fleet: server unreachable',
        identitySource: 'cache',
      }),
    );
    const wrapper = mountUserMenu(client);
    await flushPromises();
    // Trigger still shows the signed-in avatar plus a status chip.
    expect(wrapper.find('[data-testid="user-menu-avatar"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="user-menu-status-chip"]').exists()).toBe(true);
    const popover = await openPopover(wrapper);
    expect(wrapper.find('[data-testid="menu-sign-in"]').exists()).toBe(false);
    expect(popover.text()).toContain('alice@example.com');
    expect(wrapper.find('[data-testid="user-menu-degraded"]').text()).toContain("Can't reach fleet");
    await wrapper.find('[data-testid="menu-retry"]').trigger('click');
    await flushPromises();
    expect(client.settings.fleetRefreshIdentity).toHaveBeenCalledOnce();
  });

  // ── P-3 ─────────────────────────────────────────────────────────────────

  it('P-3: not-provisioned → stopped; a session change from elsewhere updates the popover without remount', async () => {
    const { client } = buildClient(
      signedIn({
        state: 'degraded',
        reason: 'not_provisioned',
        autoRetry: false,
        identity: undefined,
      }),
    );
    const wrapper = mountUserMenu(client);
    await flushPromises();
    await openPopover(wrapper);
    expect(wrapper.find('[data-testid="menu-finish-setup"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="menu-sign-in"]').exists()).toBe(false);

    // Signed in elsewhere (Account panel / another window): the backend
    // pushes the new snapshot. No remount, no poll.
    dispatchServedEvent('fleet:session-changed', signedIn());
    await flushPromises();
    expect(wrapper.find('[data-testid="menu-finish-setup"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="user-menu-degraded"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="user-menu-popover"]').text()).toContain('alice@example.com');
  });

  // ── P-4 ─────────────────────────────────────────────────────────────────

  it('P-4: sign in via UserMenu → LeftRail Marketplace entry appears', async () => {
    const { client } = buildClient(signedOut());
    const router = makeRouter();
    await router.push('/sessions');
    await router.isReady();
    const Both = defineComponent({
      render: () => h('div', [h(UserMenu), h(LeftRail)]),
    });
    const wrapper = mount(Both, {
      global: { plugins: [router], provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid=nav-marketplace]').exists()).toBe(false);

    await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-testid="menu-sign-in"]').trigger('click');
    await flushPromises();
    await nextTick();
    expect(wrapper.find('[data-testid=nav-marketplace]').exists()).toBe(true);
  });

  // ── P-9 (render half) ───────────────────────────────────────────────────

  it('P-9: enroll with empty email → header shows the org, avatar shows its initial, never blank', async () => {
    const { client } = buildClient(
      signedIn({
        identity: { userId: '', orgId: '42', teamId: 't', email: '', tier: 'enterprise', orgName: 'Kameas Dogfood' },
      }),
    );
    const wrapper = mountUserMenu(client);
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-avatar"]').text()).toBe('K');
    await openPopover(wrapper);
    const header = wrapper.find('[data-testid="user-menu-identity"]');
    expect(header.find('.user-menu-email').text()).toBe('Kameas Dogfood');
  });

  // ── WP06: claim gap + sync lanes ─────────────────────────────────────────

  it('P-8 (UI): needs_reauth → "Update sign-in" re-runs the sign-in flow; no Retry, no sign-out forced', async () => {
    const { client } = buildClient(
      signedIn({
        state: 'degraded',
        reason: 'needs_reauth',
        claims: { hasSubject: true, hasOrgClaim: false },
      }),
    );
    const wrapper = mountUserMenu(client);
    await flushPromises();
    await openPopover(wrapper);
    expect(wrapper.find('[data-testid="user-menu-degraded"]').text()).toContain('Update your sign-in');
    expect(wrapper.find('[data-testid="menu-retry"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="menu-sign-out"]').exists()).toBe(true);
    await wrapper.find('[data-testid="menu-reauth"]').trigger('click');
    await flushPromises();
    expect(client.settings.fleetSignIn).toHaveBeenCalledOnce();
  });

  it('FR-6: a degraded sync lane lights the trigger chip and is listed with its reason', async () => {
    const lane = { status: 'unknown', consecutiveFailures: 0 };
    const { client } = buildClient(
      signedIn({
        sync: {
          contextSync: { ...lane },
          unitPoll: { status: 'degraded', reason: 'network', consecutiveFailures: 3 },
          telemetry: { ...lane },
        },
      }),
    );
    const wrapper = mountUserMenu(client);
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-status-chip"]').exists()).toBe(true);
    await openPopover(wrapper);
    expect(wrapper.find('[data-testid="user-menu-sync-unitPoll"]').text()).toContain("not syncing — can't reach fleet");
  });

  // ── WP07: sign-in in flight ──────────────────────────────────────────────

  it('FR-5 / P-6 (UI): while a sign-in is in flight the menu offers Cancel, never a second "Sign in"', async () => {
    const { client } = buildClient(fakeFleetSession({ state: 'signing_in', profile: prodProfile }));
    const wrapper = mountUserMenu(client);
    await flushPromises();
    await openPopover(wrapper);
    expect(wrapper.find('[data-testid="menu-sign-in"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="menu-signing-in"]').text()).toContain('Waiting for browser');
    await wrapper.find('[data-testid="menu-sign-in-cancel"]').trigger('click');
    await flushPromises();
    expect(client.settings.fleetSignInCancel).toHaveBeenCalledOnce();
    expect(wrapper.find('[data-testid="menu-sign-in"]').exists()).toBe(true);
  });

  // ── WP08: roles ──────────────────────────────────────────────────────────

  it('FR-9: roles from the enroll payload render in the identity header', async () => {
    const { client } = buildClient(
      signedIn({ identity: { ...aliceIdentity, roles: ['org_owner', 'policy_admin'] } }),
    );
    const wrapper = mountUserMenu(client);
    await flushPromises();
    await openPopover(wrapper);
    expect(wrapper.find('[data-testid="user-menu-roles"]').text()).toBe('Org owner, Policy admin');
  });
});
