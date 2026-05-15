/**
 * ProvidersView — axe-core accessibility assertions
 * (accessibility-audit-01KQ8TDA WP02)
 *
 * Mounts ProvidersView with a fake client and asserts no axe violations in
 * the empty-provider state. color-contrast disabled: happy-dom returns 0/0
 * (needs_manual_verification in a real browser).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ProvidersView from '@/views/providers/ProvidersView.vue';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { axe } from 'vitest-axe';

/** Axe options shared by all tests in this file. */
const axeOptions = {
  rules: {
    // happy-dom does not compute CSS, so color-contrast always reports 0/0.
    // needs_manual_verification: check contrast in dark + light themes with
    // a real browser.
    'color-contrast': { enabled: false },
    // region — component tree mounts without Shell landmark wrapper in tests.
    region: { enabled: false },
  },
};

function makeClient() {
  return createFakeHarnessClient({
    llm: {
      ...createFakeHarnessClient().llm,
      listProviders: vi.fn().mockResolvedValue([]),
    },
  });
}

describe('ProvidersView — a11y (axe-core)', () => {
  it('has no axe violations in the empty-provider state', async () => {
    const client = makeClient();
    const w = mount(ProvidersView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    const results = await axe(w.element, axeOptions);
    // @ts-expect-error — toHaveNoViolations added via test-setup.ts extend
    expect(results).toHaveNoViolations();
  });
});
