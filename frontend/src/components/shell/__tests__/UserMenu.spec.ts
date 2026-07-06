/**
 * UserMenu — status pill + fleet-identity popover tests.
 *
 * v0.20.0: non-account rows (Search, Command Palette, Theme, Update Available)
 * have moved to the OS native menu bar. Asserts:
 *   - trigger always renders (except fleet disabled)
 *   - removed rows are absent
 *   - account rows behave identically to v0.19.0
 *   - fleet-disabled renders nothing
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createMemoryHistory } from 'vue-router';
import UserMenu from '../UserMenu.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { FleetIdentity, FleetProfileInfo } from '@/lib/types';

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

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/settings', component: { template: '<div />' } },
    ],
  });
}

function buildSignedOutClient() {
  return createFakeHarnessClient({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    settings: {
      fleetProfile: vi.fn(async () => prodProfile),
      fleetSignedIn: vi.fn(async () => false),
      fleetSignIn: vi.fn(async () => aliceIdentity),
      fleetSignOut: vi.fn(async () => {}),
      fleetRefreshIdentity: vi.fn(async () => aliceIdentity),
    } as any,
  });
}

function buildSignedInClient(
  profile: FleetProfileInfo = prodProfile,
  identity: FleetIdentity = aliceIdentity,
) {
  return createFakeHarnessClient({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    settings: {
      fleetProfile: vi.fn(async () => profile),
      fleetSignedIn: vi.fn(async () => true),
      fleetSignIn: vi.fn(async () => identity),
      fleetSignOut: vi.fn(async () => {}),
      fleetRefreshIdentity: vi.fn(async () => identity),
    } as any,
  });
}

function buildDisabledClient() {
  return createFakeHarnessClient({
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    settings: {
      fleetProfile: vi.fn(async () => {
        throw new Error('fleet: disabled by env');
      }),
      fleetSignedIn: vi.fn(async () => false),
      fleetSignIn: vi.fn(async () => ({ userId: '', orgId: '', teamId: '' })),
      fleetSignOut: vi.fn(async () => {}),
      fleetRefreshIdentity: vi.fn(async () => ({ userId: '', orgId: '', teamId: '' })),
    } as any,
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

describe('UserMenu', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  // ── Trigger render ─────────────────────────────────────────────────────

  it('trigger renders when fleet is enabled (signed-out)', async () => {
    const wrapper = mountUserMenu(buildSignedOutClient());
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-trigger"]').exists()).toBe(true);
  });

  it('trigger renders when signed in', async () => {
    const wrapper = mountUserMenu(buildSignedInClient());
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-trigger"]').exists()).toBe(true);
  });

  it('renders nothing when fleet is disabled (HARNESS_FLEET_DISABLED=1)', async () => {
    const wrapper = mountUserMenu(buildDisabledClient());
    await flushPromises();
    // The entire component renders nothing when fleet is disabled.
    expect(wrapper.find('[data-testid="user-menu-trigger"]').exists()).toBe(false);
  });

  it('renders avatar with initials when signed in', async () => {
    const wrapper = mountUserMenu(buildSignedInClient());
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-trigger"]').text()).toContain('AC');
  });

  it('renders env badge for non-prod profile', async () => {
    const wrapper = mountUserMenu(buildSignedInClient(devProfile));
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-trigger"]').text()).toContain('DEV');
  });

  // ── Removed rows (must be absent) ──────────────────────────────────────

  it('removed non-account rows are NOT present', async () => {
    for (const client of [buildSignedOutClient(), buildSignedInClient()]) {
      const wrapper = mountUserMenu(client);
      await flushPromises();
      await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
      await flushPromises();
      // These rows moved to the OS menu bar; they must not appear in the popover.
      expect(wrapper.find('[data-testid="menu-search"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="menu-command-palette"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="menu-theme"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="menu-update"]').exists()).toBe(false);
    }
  });

  it('update-dot overlay is NOT on the trigger', async () => {
    const wrapper = mountUserMenu(buildSignedInClient());
    await flushPromises();
    // The .update-dot was removed from the trigger in v0.20.0.
    expect(wrapper.find('.update-dot').exists()).toBe(false);
  });

  // ── Account rows ────────────────────────────────────────────────────────

  it('signed-out: shows Sign in row only', async () => {
    const wrapper = mountUserMenu(buildSignedOutClient());
    await flushPromises();
    await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="menu-sign-in"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="menu-account"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="menu-sign-out"]').exists()).toBe(false);
  });

  it('signed-in: shows Account settings + Sign out', async () => {
    const wrapper = mountUserMenu(buildSignedInClient());
    await flushPromises();
    await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="menu-sign-in"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="menu-account"]').text()).toContain('Account settings');
    expect(wrapper.find('[data-testid="menu-sign-out"]').exists()).toBe(true);
  });

  it('identity header populates when signed in', async () => {
    const wrapper = mountUserMenu(buildSignedInClient());
    await flushPromises();
    await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
    await flushPromises();
    const popover = wrapper.find('[data-testid="user-menu-popover"]');
    expect(popover.text()).toContain('alice@example.com');
    expect(popover.text()).toContain('Acme Corp');
    expect(popover.text()).toContain('pro');
  });

  it('"Sign in" row calls fleetSignIn directly (no route detour)', async () => {
    const client = buildSignedOutClient();
    const signIn = vi.spyOn(client.settings, 'fleetSignIn');
    const wrapper = mountUserMenu(client);
    await flushPromises();
    await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-testid="menu-sign-in"]').trigger('click');
    await flushPromises();
    expect(signIn).toHaveBeenCalledOnce();
  });

  it('signs out when "Sign out" is clicked', async () => {
    const client = buildSignedInClient();
    const signOut = vi.spyOn(client.settings, 'fleetSignOut');
    const wrapper = mountUserMenu(client);
    await flushPromises();
    await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-testid="menu-sign-out"]').trigger('click');
    await flushPromises();
    expect(signOut).toHaveBeenCalledOnce();
  });
});
