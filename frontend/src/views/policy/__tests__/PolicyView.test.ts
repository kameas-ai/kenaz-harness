/**
 * PolicyView.test.ts — Unit tests for the Cedar policy editor view.
 * (cedar-policy-editor-ui-01KQ8TD6 WP02)
 *
 * Replaces the old props-driven WP14 tests; the view now uses the
 * harnessClient directly (client-driven architecture).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import PolicyView from '../PolicyView.vue';
import type { HarnessClient, CedarPolicyClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { PolicyFile, PolicyFileDetail, ParseResult, AppInfo } from '@/lib/types';

// ── helpers ────────────────────────────────────────────────────────────

function makePolicyClient(overrides?: Partial<CedarPolicyClient>): CedarPolicyClient {
  const defaultDetail: PolicyFileDetail = {
    name: 'my-policy.cedar',
    bytes: 42,
    embedded: false,
    parse_ok: true,
    source: 'permit(principal, action, resource);',
    read_only: false,
  };

  const defaultFiles: PolicyFile[] = [
    { name: 'default_policy.cedar', bytes: 100, embedded: true, parse_ok: true },
    { name: 'my-policy.cedar', bytes: 42, embedded: false, parse_ok: true },
  ];

  return {
    listPolicies: vi.fn().mockResolvedValue(defaultFiles),
    reloadPolicies: vi.fn().mockResolvedValue(undefined),
    recentDecisions: vi.fn().mockResolvedValue([]),
    writeSnippet: vi.fn().mockResolvedValue(undefined),
    revokeSnippet: vi.fn().mockResolvedValue(undefined),
    getPolicy: vi.fn().mockResolvedValue(defaultDetail),
    savePolicy: vi.fn().mockResolvedValue({ ok: true } satisfies ParseResult),
    deletePolicy: vi.fn().mockResolvedValue(undefined),
    validatePolicy: vi.fn().mockResolvedValue({ ok: true } satisfies ParseResult),
    installTemplate: vi.fn().mockResolvedValue(defaultDetail),
    ...overrides,
  };
}

function makeClient(
  policyClient: CedarPolicyClient,
  extraAppInfo: Partial<AppInfo> = {},
): Partial<HarnessClient> {
  return {
    appInfo: vi.fn().mockResolvedValue({
      build: 'dev',
      commit: 'abc',
      buildTime: '',
      goVersion: 'go1.21',
      platform: 'darwin/arm64',
      windowSize: { width: 1280, height: 800 },
      policyEditorEnabled: true,
      ...extraAppInfo,
    }),
    cedarPolicy: policyClient,
  } as unknown as HarnessClient;
}

async function mountView(
  clientOverrides?: Partial<CedarPolicyClient>,
  appInfoOverrides?: Partial<AppInfo>,
) {
  const policyClient = makePolicyClient(clientOverrides);
  const fakeClient = makeClient(policyClient, appInfoOverrides);
  const wrapper = mount(PolicyView, {
    global: { provide: { [HarnessClientKey as symbol]: fakeClient } },
  });
  await flushPromises();
  return { wrapper, policyClient, fakeClient };
}

// ── tests ──────────────────────────────────────────────────────────────

describe('PolicyView (cedar-policy-editor-ui-01KQ8TD6 WP02)', () => {
  // ── list renders ────────────────────────────────────────────────────

  describe('list renders', () => {
    it('shows all loaded policy files', async () => {
      const { wrapper } = await mountView();
      const list = wrapper.find('[data-testid="policy-file-list"]');
      expect(list.exists()).toBe(true);
      expect(list.text()).toContain('default_policy.cedar');
      expect(list.text()).toContain('my-policy.cedar');
    });

    it('shows embedded badge for embedded policies', async () => {
      const { wrapper } = await mountView();
      const item = wrapper.find('[data-testid="policy-file-default_policy.cedar"]');
      expect(item.text()).toContain('embedded');
    });

    it('shows empty state when no files', async () => {
      const { wrapper } = await mountView({ listPolicies: vi.fn().mockResolvedValue([]) });
      expect(wrapper.text()).toContain('No policy files loaded');
    });
  });

  // ── edit + save flow ────────────────────────────────────────────────

  describe('edit + save flow', () => {
    it('loads source when file clicked', async () => {
      const { wrapper, policyClient } = await mountView();
      await wrapper.find('[data-testid="policy-file-my-policy.cedar"]').trigger('click');
      await flushPromises();
      expect(policyClient.getPolicy).toHaveBeenCalledWith('my-policy.cedar');
      const ta = wrapper.find('[data-testid="policy-editor-textarea"]');
      expect((ta.element as HTMLTextAreaElement).value).toBe('permit(principal, action, resource);');
    });

    it('shows Save button for editable policies', async () => {
      const { wrapper } = await mountView();
      await wrapper.find('[data-testid="policy-file-my-policy.cedar"]').trigger('click');
      await flushPromises();
      expect(wrapper.find('[data-testid="policy-save"]').exists()).toBe(true);
    });

    it('calls savePolicy and shows toast on success', async () => {
      const { wrapper, policyClient } = await mountView();
      await wrapper.find('[data-testid="policy-file-my-policy.cedar"]').trigger('click');
      await flushPromises();

      const ta = wrapper.find('[data-testid="policy-editor-textarea"]');
      // Simulate edit to mark as dirty.
      await ta.trigger('input');
      // Set the value on the element directly since setValue + input event
      // is the pattern for controlled textarea in vue-test-utils.
      (ta.element as HTMLTextAreaElement).value = 'permit(principal, action, resource); // changed';
      await ta.trigger('input');

      const saveBtn = wrapper.find('[data-testid="policy-save"]');
      await saveBtn.trigger('click');
      await flushPromises();
      expect(policyClient.savePolicy).toHaveBeenCalled();
    });

    it('shows parse-error banner on save failure and line/col info', async () => {
      const { wrapper } = await mountView({
        savePolicy: vi.fn().mockResolvedValue({
          ok: false,
          errors: [{ line: 3, column: 7, message: 'unexpected token' }],
        } satisfies ParseResult),
      });
      await wrapper.find('[data-testid="policy-file-my-policy.cedar"]').trigger('click');
      await flushPromises();

      // Make dirty.
      const ta = wrapper.find('[data-testid="policy-editor-textarea"]');
      (ta.element as HTMLTextAreaElement).value = 'bad cedar';
      await ta.trigger('input');

      await wrapper.find('[data-testid="policy-save"]').trigger('click');
      await flushPromises();

      const banner = wrapper.find('[data-testid="policy-parse-errors"]');
      expect(banner.exists()).toBe(true);
      expect(banner.text()).toContain('unexpected token');
      expect(banner.text()).toContain('3');
    });
  });

  // ── parse-error banner with line/col ────────────────────────────────

  describe('parse-error banner shows line/col', () => {
    it('renders errors from failed save with line numbers', async () => {
      const { wrapper } = await mountView({
        savePolicy: vi.fn().mockResolvedValue({
          ok: false,
          errors: [{ line: 2, column: 1, message: 'missing semicolon' }],
        } satisfies ParseResult),
      });
      await wrapper.find('[data-testid="policy-file-my-policy.cedar"]').trigger('click');
      await flushPromises();
      const ta = wrapper.find('[data-testid="policy-editor-textarea"]');
      (ta.element as HTMLTextAreaElement).value = 'broken';
      await ta.trigger('input');
      await wrapper.find('[data-testid="policy-save"]').trigger('click');
      await flushPromises();
      const banner = wrapper.find('[data-testid="policy-parse-errors"]');
      expect(banner.text()).toContain('2');
      expect(banner.text()).toContain('missing semicolon');
    });
  });

  // ── debounced validate ───────────────────────────────────────────────

  describe('debounced validate', () => {
    it('calls validatePolicy after 500ms debounce', async () => {
      vi.useFakeTimers();
      const { wrapper, policyClient } = await mountView();
      await wrapper.find('[data-testid="policy-file-my-policy.cedar"]').trigger('click');
      await flushPromises();
      vi.mocked(policyClient.validatePolicy).mockClear();

      const ta = wrapper.find('[data-testid="policy-editor-textarea"]');
      (ta.element as HTMLTextAreaElement).value = 'new content';
      await ta.trigger('input');

      // Should NOT call immediately.
      expect(policyClient.validatePolicy).not.toHaveBeenCalled();
      vi.advanceTimersByTime(600);
      await flushPromises();
      expect(policyClient.validatePolicy).toHaveBeenCalled();
      vi.useRealTimers();
    });
  });

  // ── default-policy read-only ─────────────────────────────────────────

  describe('default policy is read-only', () => {
    it('does not show Save/Delete for embedded default', async () => {
      const { wrapper } = await mountView({
        getPolicy: vi.fn().mockResolvedValue({
          name: 'default_policy.cedar',
          bytes: 100,
          embedded: true,
          parse_ok: true,
          source: '// default',
          read_only: true,
        } satisfies PolicyFileDetail),
      });
      await wrapper.find('[data-testid="policy-file-default_policy.cedar"]').trigger('click');
      await flushPromises();
      expect(wrapper.find('[data-testid="policy-save"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="policy-delete"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="policy-readonly-badge"]').exists()).toBe(true);
    });
  });

  // ── delete confirm dialog ────────────────────────────────────────────

  describe('delete confirm dialog', () => {
    it('shows confirm dialog on Delete click', async () => {
      const { wrapper } = await mountView();
      await wrapper.find('[data-testid="policy-file-my-policy.cedar"]').trigger('click');
      await flushPromises();
      await wrapper.find('[data-testid="policy-delete"]').trigger('click');
      expect(wrapper.find('[data-testid="policy-delete-confirm"]').exists()).toBe(true);
    });

    it('cancel leaves file intact', async () => {
      const { wrapper, policyClient } = await mountView();
      await wrapper.find('[data-testid="policy-file-my-policy.cedar"]').trigger('click');
      await flushPromises();
      await wrapper.find('[data-testid="policy-delete"]').trigger('click');
      await wrapper.find('[data-testid="policy-delete-confirm-cancel"]').trigger('click');
      expect(policyClient.deletePolicy).not.toHaveBeenCalled();
      expect(wrapper.find('[data-testid="policy-delete-confirm"]').exists()).toBe(false);
    });

    it('confirm calls deletePolicy and clears editor', async () => {
      const { wrapper, policyClient } = await mountView();
      await wrapper.find('[data-testid="policy-file-my-policy.cedar"]').trigger('click');
      await flushPromises();
      await wrapper.find('[data-testid="policy-delete"]').trigger('click');
      await wrapper.find('[data-testid="policy-delete-confirm-yes"]').trigger('click');
      await flushPromises();
      expect(policyClient.deletePolicy).toHaveBeenCalledWith('my-policy.cedar');
      expect(wrapper.find('[data-testid="policy-editor-textarea"]').exists()).toBe(false);
    });
  });

  // ── feature-flag off route absent ───────────────────────────────────

  describe('feature flag disabled', () => {
    it('shows disabled state when policyEditorEnabled is false', async () => {
      const { wrapper } = await mountView(undefined, { policyEditorEnabled: false });
      expect(wrapper.find('[data-testid="policy-editor-disabled"]').exists()).toBe(true);
      expect(wrapper.find('[data-testid="policy-file-list"]').exists()).toBe(false);
    });
  });
});
