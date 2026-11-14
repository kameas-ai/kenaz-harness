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
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AccountPanel from '@/views/settings/AccountPanel.vue';
import { createFakeHarnessClient, fakeFleetSession } from '@/lib/harnessClient';
import { _resetFleetSessionForTest, applyFleetSession } from '@/lib/fleetSession';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { FleetIdentity, FleetProfileInfo, FleetSessionView } from '@/lib/types';

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

// fleet-session-truth-01DOGF0A WP04: the panel renders the shared fleet
// session store, so every client here serves Settings_FleetSession snapshots
// and flips them the way the backend would on sign-in / sign-out.
function session(
  state: FleetSessionView['state'],
  profile: FleetProfileInfo = prodProfile,
  over: Partial<FleetSessionView> = {},
): FleetSessionView {
  return fakeFleetSession({
    state,
    profile,
    identity: state === 'signed_in' || state === 'degraded' ? mockIdentity : undefined,
    ...over,
  });
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function buildClient(initial: FleetSessionView, over: Record<string, any> = {}) {
  let current = initial;
  return createFakeHarnessClient({
    settings: {
      fleetSession: vi.fn(async () => current),
      fleetProfile: vi.fn(async () => current.profile ?? prodProfile),
      fleetSignedIn: vi.fn(async () => current.state === 'signed_in'),
      fleetSignIn: vi.fn(async () => {
        current = session('signed_in', current.profile);
        return mockIdentity;
      }),
      fleetSignOut: vi.fn(async () => {
        current = session('signed_out', current.profile);
      }),
      fleetRefreshIdentity: vi.fn(async () => mockIdentity),
      ...over,
    } as any,
  });
}

function buildDisabledClient() {
  return buildClient(fakeFleetSession({ state: 'disabled' }));
}

function buildSignedOutClient() {
  return buildClient(session('signed_out'));
}

function buildSignedInClient(profile: FleetProfileInfo = prodProfile) {
  return buildClient(session('signed_in', profile));
}

// ── specs ────────────────────────────────────────────────────────────────

describe('AccountPanel', () => {
  beforeEach(() => _resetFleetSessionForTest());

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
    const client = buildClient(session('signed_out'), {
      fleetSignIn: vi.fn(async () => {
        // Mirrors core/fleet/identity.go's wrapped sentinel: stable
        // prefix from ErrUserNotProvisioned, raw server body appended
        // after "(server: ...)" the way mapSiteError-style wrapping does.
        throw new Error(
          'fleet: this Zitadel user has no Fleet account; finish signup at the SPA host ' +
            `(server: ${rawServerBody})`,
        );
      }),
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
    // The backend snapshot for that state: tokens usable, enroll failing
    // with not-provisioned → degraded, automatic retry stopped (FR-4).
    void rawServerBody;
    const client = buildClient(
      session('degraded', prodProfile, {
        reason: 'not_provisioned',
        autoRetry: false,
        identity: undefined,
      }),
    );
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

describe('AccountPanel — degraded session (fleet-session-truth-01DOGF0A FR-3)', () => {
  beforeEach(() => _resetFleetSessionForTest());

  it('a failed identity refresh with valid tokens keeps the identity and offers Retry, not Sign in', async () => {
    const client = buildClient(session('degraded', prodProfile, { reason: 'network' }));
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="signed-in-panel"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="sign-in-btn"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="identity-email"]').text()).toContain('alice@example.com');
    expect(wrapper.find('[data-testid="account-degraded"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="refresh-btn"]').text()).toBe('Retry');
  });

  it('reads the shared store: mounting fires no enroll of its own', async () => {
    const client = buildSignedInClient();
    mount(AccountPanel, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    expect(client.settings.fleetRefreshIdentity).not.toHaveBeenCalled();
  });

  it('needs_reauth (token without org claim) → "Update sign-in" runs the sign-in flow', async () => {
    const client = buildClient(session('degraded', prodProfile, { reason: 'needs_reauth' }));
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="account-degraded"]').text()).toContain('Update your sign-in');
    await wrapper.find('[data-testid="reauth-btn"]').trigger('click');
    await flushPromises();
    expect(client.settings.fleetSignIn).toHaveBeenCalledOnce();
    expect(wrapper.find('[data-testid="reauth-btn"]').exists()).toBe(false);
  });

  it('FR-9: roles and name render in the Account body', async () => {
    const client = buildClient(
      session('signed_in', prodProfile, {
        identity: { ...mockIdentity, displayName: 'Alice Cooper', roles: ['org_owner'] },
      }),
    );
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="identity-roles"]').text()).toBe('Org owner');
    expect(wrapper.find('[data-testid="identity-name"]').text()).toBe('Alice Cooper');
  });

  // Dogfood 2026-10-08 P3: "Name" showed the email (fleet's display-name
  // fallback). A display name equal to the email is not rendered as a name.
  it('hides the Name row when the display name is just the email', async () => {
    const client = buildClient(
      session('signed_in', prodProfile, {
        identity: { ...mockIdentity, displayName: 'Alice@Example.com' },
      }),
    );
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="identity-name"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="identity-email"]').text()).toBe('alice@example.com');
  });

  // device-keys-handoff-01DEVKH01 review fix #4.
  it("shows this device's own handoff-key fingerprint when registered", async () => {
    const fp = 'sha256:' + 'ab'.repeat(32);
    const client = buildClient(
      session('signed_in', prodProfile, { deviceKeys: { status: 'registered', handoffFingerprint: fp } }),
    );
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="identity-device-fingerprint"]').text()).toBe(fp);
  });

  it('shows the cannot-receive reason (no fingerprint) when keys did not register', async () => {
    const msg = "This device can't receive shared sessions: too many devices.";
    const client = buildClient(
      session('signed_in', prodProfile, { deviceKeys: { status: 'too_many_devices', message: msg } }),
    );
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="identity-device-fingerprint"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="device-keys-msg"]').text()).toContain('too many devices');
  });

  it('shows the device-keys reason even while the session is degraded', async () => {
    const msg = "This device can't receive shared sessions: too many devices.";
    const client = buildClient(
      session('degraded', prodProfile, {
        reason: 'fleet_unreachable',
        deviceKeys: { status: 'too_many_devices', message: msg },
      } as Partial<FleetSessionView>),
    );
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="error-msg"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="device-keys-msg"]').text()).toContain('too many devices');
  });

  // kenaz-fleet ER-40: Fleet kept the old key; replacing it needs a fresh
  // sign-in. The panel offers "Sign in again" and retries once (backend).
  describe('device keys need a fresh sign-in', () => {
    const reauthMsg =
      "Sign in again to update this device's keys. Until you do, sessions shared with this device may not open here.";
    const reauthSession = () =>
      session('signed_in', prodProfile, { deviceKeys: { status: 'reauth_required', message: reauthMsg } });

    it('shows the message and a Sign in again button', async () => {
      const client = buildClient(reauthSession(), { fleetDeviceKeysReauth: vi.fn(async () => {}) });
      const wrapper = mount(AccountPanel, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      expect(wrapper.find('[data-testid="device-keys-msg"]').text()).toContain('Sign in again to update');
      expect(wrapper.find('[data-testid="keys-reauth-btn"]').text()).toBe('Sign in again');
      expect(wrapper.find('[data-testid="keys-reauth-btn"]').classes()).toContain('btn-primary');
      expect(wrapper.find('[data-testid="identity-device-fingerprint"]').exists()).toBe(false);
    });

    it('click runs the step-up once; success clears the prompt', async () => {
      let current = reauthSession();
      const reauth = vi.fn(async () => {
        current = session('signed_in', prodProfile, {
          deviceKeys: { status: 'registered', handoffFingerprint: 'sha256:' + 'cd'.repeat(32) },
        });
      });
      const client = buildClient(current, {
        fleetSession: vi.fn(async () => current),
        fleetDeviceKeysReauth: reauth,
      });
      const wrapper = mount(AccountPanel, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      await wrapper.find('[data-testid="keys-reauth-btn"]').trigger('click');
      await flushPromises();
      expect(reauth).toHaveBeenCalledOnce();
      expect(wrapper.find('[data-testid="keys-reauth-btn"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="identity-device-fingerprint"]').exists()).toBe(true);
    });

    it('cancel keeps the prompt visible and shows no error', async () => {
      let rejectFlow: (e: Error) => void = () => {};
      const reauth = vi.fn(
        () =>
          new Promise<void>((_, reject) => {
            rejectFlow = reject;
          }),
      );
      const cancel = vi.fn(async () => rejectFlow(new Error('context canceled')));
      const client = buildClient(reauthSession(), {
        fleetDeviceKeysReauth: reauth,
        fleetSignInCancel: cancel,
      });
      const wrapper = mount(AccountPanel, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      await wrapper.find('[data-testid="keys-reauth-btn"]').trigger('click');
      await flushPromises();
      expect(wrapper.find('[data-testid="keys-reauth-btn"]').text()).toBe('Waiting for browser…');
      await wrapper.find('[data-testid="keys-reauth-cancel-btn"]').trigger('click');
      await flushPromises();
      expect(cancel).toHaveBeenCalledOnce();
      expect(wrapper.find('[data-testid="keys-reauth-cancel-btn"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="keys-reauth-btn"]').text()).toBe('Sign in again');
      // The recoverable state stays visible; a cancel is not an error.
      expect(wrapper.find('[data-testid="device-keys-msg"]').text()).toContain('Sign in again to update');
      expect(wrapper.find('[data-testid="error-msg"]').exists()).toBe(false);
    });

    it('a refused fresh sign-in shows the admin copy and a secondary Try again, no temporary error', async () => {
      const refusedCopy = "Your organization's sign-in can't confirm this change yet. Contact your administrator.";
      let current = reauthSession();
      const reauth = vi.fn(async () => {
        current = session('signed_in', prodProfile, { deviceKeys: { status: 'reauth_refused', message: refusedCopy } });
        throw new Error(
          "fleet: sign in again to update this device's keys: your organization's sign-in can't confirm this change yet",
        );
      });
      const client = buildClient(current, {
        fleetSession: vi.fn(async () => current),
        fleetDeviceKeysReauth: reauth,
      });
      const wrapper = mount(AccountPanel, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      await wrapper.find('[data-testid="keys-reauth-btn"]').trigger('click');
      await flushPromises();
      expect(reauth).toHaveBeenCalledOnce();
      expect(wrapper.find('[data-testid="device-keys-msg"]').text()).toBe(refusedCopy);
      expect(wrapper.find('[data-testid="error-msg"]').exists()).toBe(false);
      const btn = wrapper.find('[data-testid="keys-reauth-btn"]');
      expect(btn.text()).toBe('Try again');
      expect(btn.classes()).toContain('btn-secondary');
    });

    it('reauth_refused on mount (persists across remounts) shows the admin copy', async () => {
      const refusedCopy = "Your organization's sign-in can't confirm this change yet. Contact your administrator.";
      const client = buildClient(
        session('signed_in', prodProfile, { deviceKeys: { status: 'reauth_refused', message: refusedCopy } }),
      );
      const wrapper = mount(AccountPanel, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      expect(wrapper.find('[data-testid="device-keys-msg"]').text()).toBe(refusedCopy);
      expect(wrapper.find('[data-testid="keys-reauth-btn"]').text()).toBe('Try again');
    });

    it('shows only one sign-in button when the session also needs a new sign-in', async () => {
      const client = buildClient(
        session('degraded', prodProfile, {
          reason: 'needs_reauth',
          deviceKeys: { status: 'reauth_required', message: reauthMsg },
        } as Partial<FleetSessionView>),
      );
      const wrapper = mount(AccountPanel, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      expect(wrapper.find('[data-testid="reauth-btn"]').exists()).toBe(true);
      expect(wrapper.find('[data-testid="keys-reauth-btn"]').exists()).toBe(false);
      // Both explanations still show.
      expect(wrapper.find('[data-testid="error-msg"]').text()).toContain('sign in again');
      expect(wrapper.find('[data-testid="device-keys-msg"]').text()).toContain('Sign in again to update');
    });
  });
});

// ── Team tree (kenaz-fleet spec 018 H-1) ───────────────────────────────────

describe('AccountPanel team tree (spec 018 H-1)', () => {
  beforeEach(() => _resetFleetSessionForTest());

  it('shows the caller\'s teams as a tree, root first, keyed by id', async () => {
    const fleetTeams = vi.fn(async () => [
      { id: 'root-id', name: 'Everyone', is_default: true, tree_depth: 0 },
      { id: 'eng-id', name: 'Engineering', is_default: false, parent_team_id: 'root-id', tree_depth: 1 },
      { id: 'be-id', name: 'Backend', is_default: false, parent_team_id: 'eng-id', tree_depth: 2 },
    ]);
    const client = buildClient(session('signed_in'), { fleetTeams });
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(fleetTeams).toHaveBeenCalled();
    const rows = wrapper.findAll('[data-testid="fleet-team-tree-row"]');
    expect(rows.map((r) => r.attributes('data-team-id'))).toEqual(['root-id', 'eng-id', 'be-id']);
    expect(rows.map((r) => r.attributes('data-depth'))).toEqual(['0', '1', '2']);
    expect(rows[0].find('[data-testid="fleet-team-root"]').exists()).toBe(true);
    // The tree replaces the single enrolled-team row.
    expect(wrapper.find('[data-testid="identity-team"]').exists()).toBe(false);
  });

  it('falls back to the enrolled team row when the team list is unavailable', async () => {
    const client = buildClient(session('signed_in'), {
      fleetTeams: vi.fn(async () => {
        throw new Error('older Fleet');
      }),
    });
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.find('[data-testid="fleet-team-tree"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="identity-team"]').text()).toBe('Engineering');
  });
});

describe('AccountPanel team tree across accounts (spec 018 H-1 review)', () => {
  beforeEach(() => _resetFleetSessionForTest());

  it('drops the previous account\'s teams as soon as the account changes', async () => {
    let call = 0;
    const fleetTeams = vi.fn(() =>
      ++call === 1
        ? Promise.resolve([
            { id: 'root-a', name: 'Everyone', is_default: true, tree_depth: 0 },
            { id: 'team-a', name: 'Alpha', is_default: false, parent_team_id: 'root-a', tree_depth: 1, membership: 'inherited' },
          ])
        : new Promise<never>(() => {}),
    );
    const client = buildClient(session('signed_in'), { fleetTeams });
    const wrapper = mount(AccountPanel, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.findAll('[data-testid="fleet-team-tree-row"]')).toHaveLength(2);
    // An inherited team stays in the Account tree, labelled.
    expect(wrapper.find('[data-team-id="team-a"] [data-testid="fleet-team-inherited"]').exists()).toBe(true);

    applyFleetSession(
      session('signed_in', prodProfile, { identity: { ...mockIdentity, userId: 'user-2', orgId: '43' } }),
    );
    await flushPromises();
    expect(fleetTeams).toHaveBeenCalledTimes(2);
    expect(wrapper.find('[data-testid="fleet-team-tree"]').exists()).toBe(false);
  });
});
