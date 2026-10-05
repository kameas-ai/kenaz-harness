/**
 * CedarEditor.publish.spec.ts — fleet-share-and-sync-01NDFSEX14 WP08
 *
 * Specs (FR-201 / FR-202):
 *   1. policy_admin user sees "Publish to team" button for non-team-managed files
 *   2. non-admin user does NOT see "Publish to team" button
 *   3. (fleet-session-truth-01DOGF0A WP04) the role comes from the shared
 *      fleet session store — mounting the editor fires no enroll
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CedarEditor from '../CedarEditor.vue';
import { signedIn, capability } from '@/lib/featureFlags';
import type { FleetIdentity, FleetSessionView } from '@/lib/types';
import { _resetFleetSessionForTest } from '@/lib/fleetSession';
import { fakeFleetSession } from '@/lib/harnessClient';

// ── featureFlags mock ──────────────────────────────────────────────────────
vi.mock('@/lib/featureFlags', () => ({
  signedIn: { value: true },
  capability: vi.fn().mockReturnValue(false),
}));

// ── harnessAPI mock ────────────────────────────────────────────────────────
const mockListPolicies = vi.fn().mockResolvedValue([
  {
    name: 'local.cedar',
    source: 'permit(principal, action, resource);',
    embedded: false,
    read_only: false,
    parse_ok: true,
    errors: [],
  },
]);
const mockFleetConfigPullStatus = vi.fn().mockResolvedValue({
  lastAppliedId: 0,
  lastAppliedAt: '',
  lastError: '',
  source: 'default-deny',
  bundleChecksum: '',
});
const mockFleetRefreshIdentity = vi.fn<() => Promise<FleetIdentity>>();
function sessionWith(identity: FleetIdentity): FleetSessionView {
  return fakeFleetSession({ state: 'signed_in', identity });
}
const mockFleetSession = vi.fn<() => Promise<FleetSessionView>>();
const mockPublishToTeam = vi.fn(async () => {});

vi.mock('@/lib/useHarnessAPI', () => ({
  useHarnessClient: () => ({
    cedarPolicy: {
      listPolicies: mockListPolicies,
      getPolicy: vi.fn().mockResolvedValue({
        name: 'local.cedar',
        source: 'permit(principal, action, resource);',
        embedded: false,
        read_only: false,
        parse_ok: true,
        errors: [],
      }),
      validatePolicy: vi.fn().mockResolvedValue({ ok: true, errors: [] }),
      savePolicy: vi.fn().mockResolvedValue({ ok: true, errors: [] }),
      deletePolicy: vi.fn().mockResolvedValue(undefined),
      reloadPolicies: vi.fn().mockResolvedValue(undefined),
    },
    settings: {
      fleetConfigPullStatus: mockFleetConfigPullStatus,
      fleetSession: mockFleetSession,
      fleetRefreshIdentity: mockFleetRefreshIdentity,
      fleetSignIn: vi.fn(),
      fleetSignOut: vi.fn(),
    },
    cedarPublish: {
      publishToTeam: mockPublishToTeam,
    },
  }),
}));

describe('CedarEditor — publish to team (WP08)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    (signedIn as { value: boolean }).value = true;
    vi.mocked(capability).mockReturnValue(false);

    mockListPolicies.mockResolvedValue([
      {
        name: 'local.cedar',
        source: 'permit(principal, action, resource);',
        embedded: false,
        read_only: false,
        parse_ok: true,
        errors: [],
      },
    ]);
    mockFleetConfigPullStatus.mockResolvedValue({
      lastAppliedId: 0,
      lastAppliedAt: '',
      lastError: '',
      source: 'default-deny',
      bundleChecksum: '',
    });
    _resetFleetSessionForTest();
    mockFleetSession.mockResolvedValue(fakeFleetSession({ state: 'signed_out' }));
    mockPublishToTeam.mockResolvedValue(undefined);
  });

  it('1. policy_admin user sees "Publish to team" button for a local file', async () => {
    // Set up a signed-in policy_admin identity
    mockFleetSession.mockResolvedValue(
      sessionWith({ userId: 'admin-1', orgId: 'o1', teamId: 't1', roles: ['policy_admin'] }),
    );

    const wrapper = mount(CedarEditor);
    await flushPromises();

    // Select the file by clicking on it in the list
    const fileItem = wrapper.find('[data-testid="cedar-file-local.cedar"]');
    if (fileItem.exists()) {
      await fileItem.trigger('click');
      await flushPromises();
    }

    // The "Publish to team" button must be visible
    expect(wrapper.find('[data-testid="cedar-publish-to-team-btn"]').exists()).toBe(true);
  });

  it('2. non-admin user does NOT see "Publish to team" button', async () => {
    // Signed in but with a regular member role (not policy_admin)
    mockFleetSession.mockResolvedValue(
      sessionWith({ userId: 'member-1', orgId: 'o1', teamId: 't1', roles: ['member'] }),
    );

    const wrapper = mount(CedarEditor);
    await flushPromises();

    // Select the file
    const fileItem = wrapper.find('[data-testid="cedar-file-local.cedar"]');
    if (fileItem.exists()) {
      await fileItem.trigger('click');
      await flushPromises();
    }

    // The "Publish to team" button must NOT be visible
    expect(wrapper.find('[data-testid="cedar-publish-to-team-btn"]').exists()).toBe(false);
  });

  it('3. mounting the editor reads the shared session — no enroll round trip', async () => {
    mockFleetSession.mockResolvedValue(
      sessionWith({ userId: 'admin-1', orgId: 'o1', teamId: 't1', roles: ['policy_admin'] }),
    );
    mount(CedarEditor);
    await flushPromises();
    expect(mockFleetRefreshIdentity).not.toHaveBeenCalled();
    expect(mockFleetSession).toHaveBeenCalled();
  });
});
