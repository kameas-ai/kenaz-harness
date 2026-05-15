/**
 * LLMRoutingPanel.spec.ts
 *
 * Tests for the Settings → LLM Routing tab.
 * model-fallback-routing-01NDFSEX04 WP05.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createRouter, createWebHashHistory } from 'vue-router';

import LLMRoutingPanel from '@/views/settings/LLMRoutingPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { FallbackChain, FallbackChainSummary } from '@/lib/types';

const SUMMARY_BUNDLED: FallbackChainSummary = {
  id: 'anthropic-with-openrouter-fallback',
  name: 'Anthropic + OpenRouter Fallback',
  description: 'Uses openrouter when anthropic is down',
  entryCount: 1,
  bundled: true,
};

const SUMMARY_USER: FallbackChainSummary = {
  id: 'my-custom-chain',
  name: 'My Custom Chain',
  entryCount: 2,
  bundled: false,
};

const FULL_CHAIN: FallbackChain = {
  id: 'my-custom-chain',
  name: 'My Custom Chain',
  description: '',
  entries: [
    {
      providerID: 'openrouter',
      model: 'openai/gpt-4o',
      triggers: ['error_5xx'],
      maxAttempts: 1,
      paramOverrides: {},
    },
    {
      providerID: 'bedrock',
      model: '',
      triggers: ['error_429'],
      maxAttempts: 2,
      paramOverrides: {},
    },
  ],
};

function buildClient(overrides: {
  listFn?: FallbackChainSummary[];
  loadFn?: FallbackChain;
  saveFn?: ReturnType<typeof vi.fn>;
  deleteFn?: ReturnType<typeof vi.fn>;
  listError?: boolean;
}) {
  const saveFn = overrides.saveFn ?? vi.fn(async () => undefined);
  const deleteFn = overrides.deleteFn ?? vi.fn(async () => undefined);

  const client = createFakeHarnessClient({
    llm: {
      listFallbackChains: overrides.listError
        ? async () => { throw new Error('backend down'); }
        : async () => overrides.listFn ?? [],
      loadChain: async () => overrides.loadFn ?? FULL_CHAIN,
      saveChain: saveFn,
      deleteChain: deleteFn,
    } as never,
  });
  return { client, saveFn, deleteFn };
}

const router = createRouter({
  history: createWebHashHistory(),
  routes: [{ path: '/', component: { template: '<div/>' } }],
});

function mountPanel(overrides: Parameters<typeof buildClient>[0] = {}) {
  const { client, saveFn, deleteFn } = buildClient(overrides);
  const wrapper = mount(LLMRoutingPanel, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      plugins: [router],
    },
  });
  return { wrapper, client, saveFn, deleteFn };
}

describe('LLMRoutingPanel', () => {
  describe('empty state', () => {
    it('shows empty state when no chains', async () => {
      const { wrapper } = mountPanel({ listFn: [] });
      await flushPromises();
      expect(wrapper.find('[data-testid="llm-routing-empty"]').exists()).toBe(true);
      expect(wrapper.find('[data-testid="llm-routing-chain-list"]').exists()).toBe(false);
    });
  });

  describe('list state', () => {
    it('renders bundled and user chains', async () => {
      const { wrapper } = mountPanel({ listFn: [SUMMARY_BUNDLED, SUMMARY_USER] });
      await flushPromises();

      expect(wrapper.find('[data-testid="llm-routing-chain-list"]').exists()).toBe(true);
      expect(wrapper.find(`[data-testid="llm-routing-chain-${SUMMARY_BUNDLED.id}"]`).exists()).toBe(true);
      expect(wrapper.find(`[data-testid="llm-routing-chain-${SUMMARY_USER.id}"]`).exists()).toBe(true);
    });

    it('shows bundled label for bundled chains', async () => {
      const { wrapper } = mountPanel({ listFn: [SUMMARY_BUNDLED] });
      await flushPromises();

      const row = wrapper.find(`[data-testid="llm-routing-chain-${SUMMARY_BUNDLED.id}"]`);
      expect(row.text()).toContain('bundled');
    });

    it('shows error message on list failure', async () => {
      const { wrapper } = mountPanel({ listError: true });
      await flushPromises();
      expect(wrapper.find('[data-testid="llm-routing-list-error"]').exists()).toBe(true);
    });
  });

  describe('editor', () => {
    it('opens editor on "New chain" click', async () => {
      const { wrapper } = mountPanel({ listFn: [] });
      await flushPromises();

      await wrapper.find('[data-testid="llm-routing-new-chain"]').trigger('click');
      expect(wrapper.find('[data-testid="llm-routing-editor"]').exists()).toBe(true);
    });

    it('opens editor on row click and loads chain data', async () => {
      const { wrapper } = mountPanel({ listFn: [SUMMARY_USER], loadFn: FULL_CHAIN });
      await flushPromises();

      await wrapper.find(`[data-testid="llm-routing-chain-${SUMMARY_USER.id}"]`).trigger('click');
      await flushPromises();

      expect(wrapper.find('[data-testid="llm-routing-editor"]').exists()).toBe(true);
      const nameInput = wrapper.find('[data-testid="llm-routing-editor-name"]');
      expect((nameInput.element as HTMLInputElement).value).toBe(FULL_CHAIN.name);
    });

    it('closes editor on Cancel click', async () => {
      const { wrapper } = mountPanel({ listFn: [] });
      await flushPromises();

      await wrapper.find('[data-testid="llm-routing-new-chain"]').trigger('click');
      expect(wrapper.find('[data-testid="llm-routing-editor"]').exists()).toBe(true);

      await wrapper.find('[data-testid="llm-routing-editor-cancel"]').trigger('click');
      expect(wrapper.find('[data-testid="llm-routing-editor"]').exists()).toBe(false);
    });

    it('calls saveChain on Save click', async () => {
      const saveFn = vi.fn(async () => undefined);
      const { wrapper } = mountPanel({ listFn: [], saveFn });
      await flushPromises();

      await wrapper.find('[data-testid="llm-routing-new-chain"]').trigger('click');

      // Set required fields.
      await wrapper.find('[data-testid="llm-routing-editor-id"]').setValue('test-chain');
      await wrapper.find('[data-testid="llm-routing-editor-name"]').setValue('Test Chain');

      await wrapper.find('[data-testid="llm-routing-editor-save"]').trigger('click');
      await flushPromises();

      expect(saveFn).toHaveBeenCalledOnce();
      const arg = saveFn.mock.calls[0][0] as FallbackChain;
      expect(arg.id).toBe('test-chain');
      expect(arg.name).toBe('Test Chain');
    });

    it('shows save error on failure', async () => {
      const saveFn = vi.fn(async () => { throw new Error('validation failed'); });
      const { wrapper } = mountPanel({ listFn: [], saveFn });
      await flushPromises();

      await wrapper.find('[data-testid="llm-routing-new-chain"]').trigger('click');
      await wrapper.find('[data-testid="llm-routing-editor-save"]').trigger('click');
      await flushPromises();

      expect(wrapper.find('[data-testid="llm-routing-save-error"]').text()).toContain('validation failed');
    });

    it('calls deleteChain and closes editor', async () => {
      const deleteFn = vi.fn(async () => undefined);
      const { wrapper } = mountPanel({ listFn: [SUMMARY_USER], loadFn: FULL_CHAIN, deleteFn });
      await flushPromises();

      await wrapper.find(`[data-testid="llm-routing-chain-${SUMMARY_USER.id}"]`).trigger('click');
      await flushPromises();

      expect(wrapper.find('[data-testid="llm-routing-editor-delete"]').exists()).toBe(true);
      await wrapper.find('[data-testid="llm-routing-editor-delete"]').trigger('click');
      await flushPromises();

      expect(deleteFn).toHaveBeenCalledWith(SUMMARY_USER.id);
      expect(wrapper.find('[data-testid="llm-routing-editor"]').exists()).toBe(false);
    });

    it('does not show delete button for bundled chains', async () => {
      const bundledFull: FallbackChain = { ...FULL_CHAIN, id: SUMMARY_BUNDLED.id, bundled: true };
      const { wrapper } = mountPanel({ listFn: [SUMMARY_BUNDLED], loadFn: bundledFull });
      await flushPromises();

      await wrapper.find(`[data-testid="llm-routing-chain-${SUMMARY_BUNDLED.id}"]`).trigger('click');
      await flushPromises();

      expect(wrapper.find('[data-testid="llm-routing-editor-delete"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="llm-routing-bundled-notice"]').exists()).toBe(true);
    });

    it('can add and remove hops', async () => {
      const { wrapper } = mountPanel({ listFn: [] });
      await flushPromises();

      await wrapper.find('[data-testid="llm-routing-new-chain"]').trigger('click');
      expect(wrapper.find('[data-testid="llm-routing-editor-no-entries"]').exists()).toBe(true);

      await wrapper.find('[data-testid="llm-routing-editor-add-entry"]').trigger('click');
      expect(wrapper.find('[data-testid="llm-routing-entry-0"]').exists()).toBe(true);

      await wrapper.find('[data-testid="llm-routing-entry-remove-0"]').trigger('click');
      expect(wrapper.find('[data-testid="llm-routing-entry-0"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="llm-routing-editor-no-entries"]').exists()).toBe(true);
    });
  });
});
