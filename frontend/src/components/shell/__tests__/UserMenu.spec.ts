/**
 * UserMenu — unified top-right dropdown tests.
 *
 * Covers: trigger render in all 3 fleet states, identity header, search
 * row, palette row, theme row, account row variants, sign-out flow.
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
    settings: {
      fleetProfile: vi.fn(async () => prodProfile),
      fleetSignedIn: vi.fn(async () => false),
      fleetSignIn: vi.fn(async () => aliceIdentity),
      fleetSignOut: vi.fn(async () => {}),
      fleetRefreshIdentity: vi.fn(async () => aliceIdentity),
    },
  });
}

function buildSignedInClient(
  profile: FleetProfileInfo = prodProfile,
  identity: FleetIdentity = aliceIdentity,
) {
  return createFakeHarnessClient({
    settings: {
      fleetProfile: vi.fn(async () => profile),
      fleetSignedIn: vi.fn(async () => true),
      fleetSignIn: vi.fn(async () => identity),
      fleetSignOut: vi.fn(async () => {}),
      fleetRefreshIdentity: vi.fn(async () => identity),
    },
  });
}

function buildDisabledClient() {
  return createFakeHarnessClient({
    settings: {
      fleetProfile: vi.fn(async () => {
        throw new Error('fleet: disabled by env');
      }),
      fleetSignedIn: vi.fn(async () => false),
      fleetSignIn: vi.fn(async () => ({ userId: '', orgId: '', teamId: '' })),
      fleetSignOut: vi.fn(async () => {}),
      fleetRefreshIdentity: vi.fn(async () => ({ userId: '', orgId: '', teamId: '' })),
    },
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

  it('trigger always renders, even when fleet is disabled', async () => {
    const wrapper = mountUserMenu(buildDisabledClient());
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-trigger"]').exists()).toBe(true);
  });

  it('renders avatar with initials when signed in', async () => {
    const wrapper = mountUserMenu(buildSignedInClient());
    await flushPromises();
    // Initials from "Alice Cooper" → "AC".
    expect(wrapper.find('[data-testid="user-menu-trigger"]').text()).toContain('AC');
  });

  it('renders env badge for non-prod profile', async () => {
    const wrapper = mountUserMenu(buildSignedInClient(devProfile));
    await flushPromises();
    expect(wrapper.find('[data-testid="user-menu-trigger"]').text()).toContain('DEV');
  });

  it('non-fleet menu rows render in all fleet states', async () => {
    for (const client of [
      buildSignedOutClient(),
      buildSignedInClient(),
      buildDisabledClient(),
    ]) {
      const wrapper = mountUserMenu(client);
      await flushPromises();
      await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
      await flushPromises();
      expect(wrapper.find('[data-testid="menu-search"]').exists()).toBe(true);
      expect(wrapper.find('[data-testid="menu-command-palette"]').exists()).toBe(true);
      expect(wrapper.find('[data-testid="menu-theme"]').exists()).toBe(true);
    }
  });

  it('fleet account rows render only when fleet is enabled', async () => {
    // Signed-out + fleet enabled: shows "Sign in" row only.
    {
      const wrapper = mountUserMenu(buildSignedOutClient());
      await flushPromises();
      await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
      await flushPromises();
      const signInBtn = wrapper.find('[data-testid="menu-sign-in"]');
      expect(signInBtn.exists()).toBe(true);
      expect(signInBtn.text()).toContain('Sign in');
      expect(wrapper.find('[data-testid="menu-account"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="menu-sign-out"]').exists()).toBe(false);
    }
    // Signed-in: "Account settings" + "Sign out".
    {
      const wrapper = mountUserMenu(buildSignedInClient());
      await flushPromises();
      await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
      await flushPromises();
      expect(wrapper.find('[data-testid="menu-sign-in"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="menu-account"]').text()).toContain('Account settings');
      expect(wrapper.find('[data-testid="menu-sign-out"]').exists()).toBe(true);
    }
    // Fleet disabled: no account / sign-in / sign-out rows.
    {
      const wrapper = mountUserMenu(buildDisabledClient());
      await flushPromises();
      await wrapper.find('[data-testid="user-menu-trigger"]').trigger('click');
      await flushPromises();
      expect(wrapper.find('[data-testid="menu-sign-in"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="menu-account"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="menu-sign-out"]').exists()).toBe(false);
    }
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
