/**
 * PolicyView.served.test.ts — served-mode boundary panel coverage for
 * /policy (AC-708, served-mode-is-a-real-mode-01PMZ707 WP03, spec.md §5.3,
 * D-701).
 *
 * CedarPolicy_ and Policy_ RPCs have no serve dispatch case, so the view renders
 * NotAvailableInServedMode instead of the file list/editor/decisions
 * surface, and must not call appInfo() or any CedarPolicy_* method while
 * doing it.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { ref } from 'vue';
import PolicyView from '../PolicyView.vue';
import type { HarnessClient, CedarPolicyClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

let servedModeFlag = true;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return {
    ...actual,
    isServedMode: () => servedModeFlag,
    useServedMode: () => ref(servedModeFlag),
  };
});

function failingPolicyClient(): Partial<HarnessClient> {
  const fail = () => {
    throw new Error(
      'cedarPolicy.* must not be called in served mode — CedarPolicy_*/Policy_* have no serve dispatch case',
    );
  };
  const policy: CedarPolicyClient = {
    listPolicies: fail,
    reloadPolicies: fail,
    recentDecisions: fail,
    writeSnippet: fail,
    revokeSnippet: fail,
    getPolicy: fail,
    savePolicy: fail,
    deletePolicy: fail,
    validatePolicy: fail,
    installTemplate: fail,
  };
  return {
    appInfo: fail,
    cedarPolicy: policy,
  };
}

describe('PolicyView (served mode)', () => {
  it('renders the boundary panel and never calls appInfo/CedarPolicy_*', async () => {
    servedModeFlag = true;
    const wrapper = mount(PolicyView, {
      global: {
        provide: {
          [HarnessClientKey as symbol]: failingPolicyClient(),
        },
      },
    });
    await flushPromises();

    expect(
      wrapper.find('[data-testid="not-available-in-served-mode"]').exists(),
    ).toBe(true);
    expect(wrapper.find('[data-testid="policy-editor-disabled"]').exists()).toBe(
      false,
    );
  });
});
