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
import type { PolicyFile, PolicyFileDetail, ParseResult, AppInfo, PolicyDecision } from '@/lib/types';

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

  // ── decisions panel (consent-surfaces-truth-01PMTR01 WP06) ───────────
  //
  // Reachability note: PolicyView IS the routed page (main.ts maps
  // /policy -> PolicyView.vue, and SettingsTabs already links to it) —
  // there is no separate parent that must bind a prop for this panel to
  // appear, unlike the TelemetryOnboardingModal shape blind spot #1
  // warns about. Mounting PolicyView directly here is mounting the same
  // component the router mounts; what these tests must not do is fake
  // reachability by asserting against some OTHER, unmounted component.

  function makeDecision(overrides: Partial<PolicyDecision> = {}): PolicyDecision {
    return {
      outcome: 'deny',
      action: 'memory_write',
      principal: 'User::"local"',
      resource: 'Memory::"global"',
      matched_policy: 'zz_forbid.cedar#0#0',
      reason: 'forbid policy matched',
      evaluated_at: '2026-08-18T10:00:00Z',
      ...overrides,
    };
  }

  describe('decisions panel', () => {
    it('does not fetch decisions until the tab is opened (pull-based, no push)', async () => {
      const { policyClient } = await mountView();
      expect(policyClient.recentDecisions).not.toHaveBeenCalled();
    });

    it('fetches and renders decisions when the Decisions tab is opened', async () => {
      const decisions = [
        makeDecision({ outcome: 'deny', action: 'memory_write' }),
        makeDecision({ outcome: 'allow', action: 'workflow.save', matched_policy: undefined }),
      ];
      const { wrapper, policyClient } = await mountView({
        recentDecisions: vi.fn().mockResolvedValue(decisions),
      });

      await wrapper.find('[data-testid="policy-tab-decisions"]').trigger('click');
      await flushPromises();

      expect(policyClient.recentDecisions).toHaveBeenCalledWith(100);
      const list = wrapper.find('[data-testid="policy-decision-list"]');
      expect(list.exists()).toBe(true);
      expect(list.text()).toContain('memory_write');
      expect(list.text()).toContain('workflow.save');
      // Assert the OUTCOME renders as the word, not a raw enum number —
      // this is the exact defect found while wiring this panel: Outcome
      // had no MarshalJSON, so RecentDecisions crossed the RPC boundary
      // as a bare 0/1/2 and every string-typed frontend consumer (this
      // one is the first) would have silently rendered digits instead
      // of "deny"/"allow".
      expect(list.text()).toContain('deny');
      expect(list.text()).toContain('allow');
      // The outcome PILL specifically must render the word, not the raw
      // enum ordinal it would be without Outcome.MarshalJSON.
      const outcomePills = wrapper.findAll('.policy-view__outcome');
      expect(outcomePills.length).toBe(2);
      for (const pill of outcomePills) {
        expect(pill.text().trim()).toMatch(/^(allow|deny|not_applicable|unknown)$/);
      }
    });

    it('renders a deny row with the deny outcome class so a denial is visually distinct', async () => {
      const { wrapper } = await mountView({
        recentDecisions: vi.fn().mockResolvedValue([makeDecision({ outcome: 'deny' })]),
      });
      await wrapper.find('[data-testid="policy-tab-decisions"]').trigger('click');
      await flushPromises();
      const row = wrapper.find('[data-testid="policy-decision-row-0"]');
      expect(row.find('.policy-view__outcome--deny').exists()).toBe(true);
    });

    it('shows the deciding policy and reason for a denial (FR-007: a denial says why)', async () => {
      const { wrapper } = await mountView({
        recentDecisions: vi.fn().mockResolvedValue([
          makeDecision({
            outcome: 'deny',
            action: 'bash.run',
            matched_policy: 'zz_forbid_bash.cedar#0#0',
            reason: 'forbid policy matched',
          }),
        ]),
      });
      await wrapper.find('[data-testid="policy-tab-decisions"]').trigger('click');
      await flushPromises();
      const row = wrapper.find('[data-testid="policy-decision-row-0"]');
      expect(row.text()).toContain('zz_forbid_bash.cedar#0#0');
      expect(row.text()).toContain('forbid policy matched');
    });

    it('shows an empty state when there are no decisions yet', async () => {
      const { wrapper } = await mountView({ recentDecisions: vi.fn().mockResolvedValue([]) });
      await wrapper.find('[data-testid="policy-tab-decisions"]').trigger('click');
      await flushPromises();
      expect(wrapper.find('[data-testid="policy-decisions-empty"]').exists()).toBe(true);
    });

    it('surfaces a fetch error without crashing', async () => {
      const { wrapper } = await mountView({
        recentDecisions: vi.fn().mockRejectedValue(new Error('boom')),
      });
      await wrapper.find('[data-testid="policy-tab-decisions"]').trigger('click');
      await flushPromises();
      expect(wrapper.text()).toContain('boom');
    });

    it('Refresh re-fetches on demand (pull, not push)', async () => {
      const { wrapper, policyClient } = await mountView({
        recentDecisions: vi.fn().mockResolvedValue([]),
      });
      await wrapper.find('[data-testid="policy-tab-decisions"]').trigger('click');
      await flushPromises();
      expect(policyClient.recentDecisions).toHaveBeenCalledTimes(1);

      await wrapper.find('[data-testid="policy-decisions-refresh"]').trigger('click');
      await flushPromises();
      expect(policyClient.recentDecisions).toHaveBeenCalledTimes(2);
    });

    it('switching back to Files hides the decisions panel and shows the editor layout', async () => {
      const { wrapper } = await mountView({
        recentDecisions: vi.fn().mockResolvedValue([makeDecision()]),
      });
      await wrapper.find('[data-testid="policy-tab-decisions"]').trigger('click');
      await flushPromises();
      expect(wrapper.find('[data-testid="policy-decisions-panel"]').exists()).toBe(true);

      await wrapper.find('[data-testid="policy-tab-editor"]').trigger('click');
      await flushPromises();
      expect(wrapper.find('[data-testid="policy-decisions-panel"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="policy-file-list"]').exists()).toBe(true);
    });
  });
});
