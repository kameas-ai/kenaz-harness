/**
 * CedarEditor.publish.spec.ts — fleet-share-and-sync-01NDFSEX14 WP08
 *
 * Two specs (FR-201 / FR-202):
 *   1. policy_admin user sees "Publish to team" button for non-team-managed files
 *   2. non-admin user does NOT see "Publish to team" button
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import CedarEditor from '../CedarEditor.vue';
import { signedIn, capability } from '@/lib/featureFlags';
import type { FleetIdentity } from '@/lib/types';

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
const mockFleetSignedIn = vi.fn().mockResolvedValue(false);
const mockFleetRefreshIdentity = vi.fn<() => Promise<FleetIdentity>>().mockResolvedValue({
  userId: 'u1',
  orgId: 'o1',
  teamId: 't1',
  roles: [],
});
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
      fleetSignedIn: mockFleetSignedIn,
      fleetRefreshIdentity: mockFleetRefreshIdentity,
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
    mockFleetSignedIn.mockResolvedValue(false);
    mockFleetRefreshIdentity.mockResolvedValue({
      userId: 'u1',
      orgId: 'o1',
      teamId: 't1',
      roles: [],
    });
    mockPublishToTeam.mockResolvedValue(undefined);
  });

  it('1. policy_admin user sees "Publish to team" button for a local file', async () => {
    // Set up a signed-in policy_admin identity
    mockFleetSignedIn.mockResolvedValue(true);
    mockFleetRefreshIdentity.mockResolvedValue({
      userId: 'admin-1',
      orgId: 'o1',
      teamId: 't1',
      roles: ['policy_admin'],
    });

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
    mockFleetSignedIn.mockResolvedValue(true);
    mockFleetRefreshIdentity.mockResolvedValue({
      userId: 'member-1',
      orgId: 'o1',
      teamId: 't1',
      roles: ['member'],
    });

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
});
