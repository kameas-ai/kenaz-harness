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
    } as any,
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
    } as any,
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
    } as any,
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

  // ── fleet-enroll-not-provisioned ────────────────────────────────────────
  //
  // Before this fix, enrollIdentity's raw-body error (no code branch) fell
  // through humanizeFleetError's `return raw;` and the panel rendered the
  // literal 403 JSON body to the user. This pins that a sign-in failing
  // with ErrUserNotProvisioned now renders an actionable message + a real
  // link built from FleetProfileInfo.fleetBaseUrl, not the raw response.

  it('6. user_not_provisioned sign-in error renders an actionable link, not raw JSON', async () => {
    const rawServerBody =
      '{"code":"user_not_provisioned","message":"This Zitadel user has no Fleet account. ' +
      'Finish signup at the SPA host.","details":{"zitadel_user_id":"test-user-id"}}';
    const client = createFakeHarnessClient({
      settings: {
        fleetProfile: vi.fn(async () => prodProfile),
        fleetSignedIn: vi.fn(async () => false),
        fleetSignIn: vi.fn(async () => {
          // Mirrors core/fleet/identity.go's wrapped sentinel: stable
          // prefix from ErrUserNotProvisioned, raw server body appended
          // after "(server: ...)" the way mapSiteError-style wrapping does.
          throw new Error(
            'fleet: this Zitadel user has no Fleet account; finish signup at the SPA host ' +
              `(server: ${rawServerBody})`,
          );
        }),
        fleetSignOut: vi.fn(async () => {}),
        fleetRefreshIdentity: vi.fn(async () => mockIdentity),
      } as any,
    });
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    await wrapper.find('[data-testid="sign-in-btn"]').trigger('click');
    await flushPromises();

    const errorMsg = wrapper.find('[data-testid="error-msg"]');
    expect(errorMsg.exists()).toBe(true);
    // The raw JSON body must never reach the DOM.
    expect(errorMsg.text()).not.toContain('user_not_provisioned');
    expect(errorMsg.text()).not.toContain('zitadel_user_id');
    expect(errorMsg.text()).not.toContain('{');

    const link = wrapper.find('[data-testid="finish-signup-link"]');
    expect(link.exists()).toBe(true);
    expect(link.attributes('href')).toBe(prodProfile.fleetBaseUrl);
  });

  it('7. mounting with stuck tokens (already "signed in", enroll never succeeded) renders the same actionable link', async () => {
    // This is the half-signed-in state from the bug report: FleetSignIn
    // saved tokens before enroll ran, so fleetSignedIn() (token-expiry
    // based) reports true on every future mount even though enroll has
    // never once succeeded. init() runs automatically on mount — this is
    // the path a user hits by opening the app or Settings → Account, with
    // no click required, so it must not stay silent.
    const rawServerBody =
      '{"code":"user_not_provisioned","message":"This Zitadel user has no Fleet account. ' +
      'Finish signup at the SPA host.","details":{"zitadel_user_id":"test-user-id"}}';
    const client = createFakeHarnessClient({
      settings: {
        fleetProfile: vi.fn(async () => prodProfile),
        fleetSignedIn: vi.fn(async () => true),
        fleetSignIn: vi.fn(async () => mockIdentity),
        fleetSignOut: vi.fn(async () => {}),
        fleetRefreshIdentity: vi.fn(async () => {
          throw new Error(
            'fleet: this Zitadel user has no Fleet account; finish signup at the SPA host ' +
              `(server: ${rawServerBody})`,
          );
        }),
      } as any,
    });
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    // Must NOT render as an indistinguishable-from-never-signed-in button
    // with no explanation.
    const errorMsg = wrapper.find('[data-testid="error-msg"]');
    expect(errorMsg.exists()).toBe(true);
    expect(errorMsg.text()).not.toContain('user_not_provisioned');
    expect(errorMsg.text()).not.toContain('zitadel_user_id');
    expect(errorMsg.text()).not.toContain('{');

    const link = wrapper.find('[data-testid="finish-signup-link"]');
    expect(link.exists()).toBe(true);
    expect(link.attributes('href')).toBe(prodProfile.fleetBaseUrl);
  });
});
