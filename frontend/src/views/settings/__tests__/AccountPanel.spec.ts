/**
 * AccountPanel tests — fleet-auth-foundation-01NDFSEX08 WP06
 *
 * Five specs:
 *   1. disabled state renders banner + no sign-in CTA
 *   2. signed-out state renders sign-in button
 *   3. signed-in state renders identity fields
 *   4. sign-in button click calls fleetSignIn
 *   5. sign-out button click calls fleetSignOut
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AccountPanel from '@/views/settings/AccountPanel.vue';
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

const mockIdentity: FleetIdentity = {
  userId: 'user-1',
  orgId: '42',
  teamId: 'team-1',
  email: 'alice@example.com',
  tier: 'pro',
  orgName: 'Acme Corp',
  teamName: 'Engineering',
  roles: ['member'],
};

function buildDisabledClient() {
  return createFakeHarnessClient({
    settings: {
      fleetProfile: vi.fn(async () => { throw new Error('fleet: disabled by env'); }),
      fleetSignedIn: vi.fn(async () => false),
      fleetSignIn: vi.fn(async () => ({ userId: '', orgId: '', teamId: '' })),
      fleetSignOut: vi.fn(async () => {}),
      fleetRefreshIdentity: vi.fn(async () => ({ userId: '', orgId: '', teamId: '' })),
    },
  });
}

function buildSignedOutClient() {
  return createFakeHarnessClient({
    settings: {
      fleetProfile: vi.fn(async () => prodProfile),
      fleetSignedIn: vi.fn(async () => false),
      fleetSignIn: vi.fn(async () => mockIdentity),
      fleetSignOut: vi.fn(async () => {}),
      fleetRefreshIdentity: vi.fn(async () => mockIdentity),
    },
  });
}

function buildSignedInClient(profile: FleetProfileInfo = prodProfile) {
  return createFakeHarnessClient({
    settings: {
      fleetProfile: vi.fn(async () => profile),
      fleetSignedIn: vi.fn(async () => true),
      fleetSignIn: vi.fn(async () => mockIdentity),
      fleetSignOut: vi.fn(async () => {}),
      fleetRefreshIdentity: vi.fn(async () => mockIdentity),
    },
  });
}

// ── specs ────────────────────────────────────────────────────────────────

describe('AccountPanel', () => {
  it('1. disabled state renders banner with no sign-in CTA', async () => {
    const client = buildDisabledClient();
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="fleet-disabled-banner"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sign-in-btn"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="signed-in-panel"]').exists()).toBe(false);
  });

  it('2. signed-out state renders sign-in button', async () => {
    const client = buildSignedOutClient();
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="signed-out-panel"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sign-in-btn"]').exists()).toBe(true);
    // No identity card in signed-out state.
    expect(wrapper.find('[data-testid="identity-card"]').exists()).toBe(false);
  });

  it('3. signed-in state renders identity fields', async () => {
    const client = buildSignedInClient();
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="signed-in-panel"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="identity-card"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="identity-email"]').text()).toContain('alice@example.com');
    expect(wrapper.find('[data-testid="tier-badge"]').text()).toBe('pro');
    expect(wrapper.find('[data-testid="identity-org"]').text()).toContain('Acme Corp');
    expect(wrapper.find('[data-testid="identity-team"]').text()).toContain('Engineering');
  });

  it('4. sign-in button click calls fleetSignIn', async () => {
    const client = buildSignedOutClient();
    const signIn = vi.spyOn(client.settings, 'fleetSignIn');
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    await wrapper.find('[data-testid="sign-in-btn"]').trigger('click');
    await flushPromises();

    expect(signIn).toHaveBeenCalledOnce();
    // After successful sign-in, signed-in panel should appear.
    expect(wrapper.find('[data-testid="identity-card"]').exists()).toBe(true);
  });

  it('5. sign-out button click calls fleetSignOut', async () => {
    const client = buildSignedInClient();
    const signOut = vi.spyOn(client.settings, 'fleetSignOut');
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    await wrapper.find('[data-testid="sign-out-btn"]').trigger('click');
    await flushPromises();

    expect(signOut).toHaveBeenCalledOnce();
    // After sign-out, signed-out panel should appear.
    expect(wrapper.find('[data-testid="signed-out-panel"]').exists()).toBe(true);
  });

  it('env badge renders for dev profile', async () => {
    const client = buildSignedInClient(devProfile);
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    const badge = wrapper.find('[data-testid="env-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toBe('DEV');
    expect(badge.classes()).toContain('env-badge--yellow');
  });

  it('no env badge for prod profile', async () => {
    const client = buildSignedInClient(prodProfile);
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="env-badge"]').exists()).toBe(false);
  });
});
