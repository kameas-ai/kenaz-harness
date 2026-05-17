import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount } from '@vue/test-utils';
import CedarEditor from '../CedarEditor.vue';
import { signedIn, capability } from '@/lib/featureFlags';

// ── fake featureFlags ──────────────────────────────────────────────────────
vi.mock('@/lib/featureFlags', () => ({
  signedIn: { value: false },
  capability: vi.fn().mockReturnValue(false),
}));

// ── fake harnessClient ─────────────────────────────────────────────────────
const mockListPolicies = vi.fn().mockResolvedValue([]);
const mockGetPolicy = vi.fn().mockResolvedValue({ name: 'test.cedar', source: '', embedded: false, read_only: false, parse_ok: true });
const mockValidatePolicy = vi.fn().mockResolvedValue({ ok: true, errors: [] });
const mockSavePolicy = vi.fn().mockResolvedValue({ ok: true, errors: [] });
const mockDeletePolicy = vi.fn().mockResolvedValue(undefined);
const mockReloadPolicies = vi.fn().mockResolvedValue(undefined);
const mockFleetConfigPullStatus = vi.fn().mockResolvedValue({
  lastAppliedId: 0, lastAppliedAt: '', lastError: '', source: 'default-deny', bundleChecksum: '',
});
const mockAppInfo = vi.fn().mockResolvedValue({ policyEditorEnabled: true });

vi.mock('@/lib/useHarnessAPI', () => ({
  useHarnessClient: () => ({
    cedarPolicy: {
      listPolicies: mockListPolicies,
      getPolicy: mockGetPolicy,
      validatePolicy: mockValidatePolicy,
      savePolicy: mockSavePolicy,
      deletePolicy: mockDeletePolicy,
      reloadPolicies: mockReloadPolicies,
    },
    settings: {
      fleetConfigPullStatus: mockFleetConfigPullStatus,
    },
    appInfo: mockAppInfo,
  }),
}));

describe('CedarEditor', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    // Reset featureFlags to defaults.
    (signedIn as { value: boolean }).value = false;
    vi.mocked(capability).mockReturnValue(false);
    mockListPolicies.mockResolvedValue([]);
    mockFleetConfigPullStatus.mockResolvedValue({
      lastAppliedId: 0, lastAppliedAt: '', lastError: '', source: 'default-deny', bundleChecksum: '',
    });
  });

  it('renders the file list after mount', async () => {
    const wrapper = mount(CedarEditor);
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();
    expect(mockListPolicies).toHaveBeenCalled();
  });

  it('shows team-managed badge for fleet-team/ files', async () => {
    mockListPolicies.mockResolvedValue([
      { name: 'fleet-team/rule-001', embedded: true, read_only: true, parse_ok: true, bytes: 100 },
    ]);
    const wrapper = mount(CedarEditor);
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();
    const badge = wrapper.find('[data-testid="cedar-team-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toContain('team-managed');
  });

  it('does NOT show Override button when capability is missing', async () => {
    mockListPolicies.mockResolvedValue([
      { name: 'fleet-team/rule-001', embedded: true, read_only: true, parse_ok: true, bytes: 100 },
    ]);
    (signedIn as { value: boolean }).value = true;
    vi.mocked(capability).mockReturnValue(false); // opa_custom_rego absent

    const wrapper = mount(CedarEditor);
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();

    // Click the file to select it.
    mockGetPolicy.mockResolvedValue({
      name: 'fleet-team/rule-001', source: 'permit(...);', embedded: true, read_only: true, parse_ok: true,
    });
    const fileItem = wrapper.find('[data-testid="cedar-file-fleet-team/rule-001"]');
    if (fileItem.exists()) {
      await fileItem.trigger('click');
      await wrapper.vm.$nextTick();
    }

    expect(wrapper.find('[data-testid="cedar-override-btn"]').exists()).toBe(false);
  });

  it('shows Override button when signedIn + capability(opa_custom_rego)', async () => {
    mockListPolicies.mockResolvedValue([
      { name: 'fleet-team/rule-001', embedded: true, read_only: true, parse_ok: true, bytes: 100 },
    ]);
    (signedIn as { value: boolean }).value = true;
    vi.mocked(capability).mockImplementation((key: string) => key === 'opa_custom_rego');

    mockGetPolicy.mockResolvedValue({
      name: 'fleet-team/rule-001', source: 'permit(...);', embedded: true, read_only: true, parse_ok: true,
    });

    const wrapper = mount(CedarEditor);
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();
    await wrapper.vm.$nextTick();

    const fileItem = wrapper.find('[data-testid="cedar-file-fleet-team/rule-001"]');
    if (fileItem.exists()) {
      await fileItem.trigger('click');
      await wrapper.vm.$nextTick();
      await wrapper.vm.$nextTick();
    }

    expect(wrapper.find('[data-testid="cedar-override-btn"]').exists()).toBe(true);
  });
});
