import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ProvidersView from '@/views/providers/ProvidersView.vue';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import {
  createFakeHarnessClient,
  type HarnessClient,
} from '@/lib/harnessClient';
import type { Provider, AddProviderInput, TestResult } from '@/lib/types';

function makeClient(
  override: Partial<HarnessClient['llm']> = {},
): HarnessClient {
  const fake = createFakeHarnessClient();
  return {
    ...fake,
    llm: { ...fake.llm, ...override },
  };
}

describe('ProvidersView', () => {
  it('renders existing providers from the client on mount', async () => {
    const providers: Provider[] = [
      {
        id: 'p1',
        name: 'Anthropic',
        tier: 'bundle',
        model: 'claude-sonnet',
        source: 'bundle',
        validated: true,
      },
    ];
    const client = makeClient({
      listProviders: vi.fn().mockResolvedValue(providers),
    });
    const wrapper = mount(ProvidersView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.text()).toContain('Anthropic');
    expect(wrapper.text()).toContain('claude-sonnet');
    expect(wrapper.text()).toContain('VALIDATED');
  });

  it('opens drawer, submits AddProvider, and runs TestProvider', async () => {
    const list = vi
      .fn<() => Promise<Provider[]>>()
      .mockResolvedValueOnce([])
      .mockResolvedValue([
        {
          id: 'new-prov',
          name: 'New',
          tier: 'personal',
          model: 'm',
          source: 'personal',
          validated: false,
        },
      ]);
    const addProvider = vi
      .fn<(_input: AddProviderInput) => Promise<void>>()
      .mockResolvedValue();
    const testProvider = vi
      .fn<(_id: string) => Promise<TestResult>>()
      .mockResolvedValue({
        success: true,
        latency_ms: 10,
        message: 'ok',
      });
    const client = makeClient({
      listProviders: list,
      addProvider,
      testProvider,
    });

    const wrapper = mount(ProvidersView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    // Open drawer.
    await wrapper.find('[data-testid="open-add-provider"]').trigger('click');
    expect(wrapper.find('[data-testid="add-provider-drawer"]').exists()).toBe(
      true,
    );

    // Fill form.
    await wrapper
      .find('[data-testid="add-provider-id"]')
      .setValue('new-prov');
    await wrapper.find('[data-testid="add-provider-name"]').setValue('New');
    await wrapper.find('[data-testid="add-provider-model"]').setValue('m');
    await wrapper
      .find('[data-testid="add-provider-apikey"]')
      .setValue('sk-test');

    await wrapper.find('form').trigger('submit.prevent');
    await flushPromises();

    expect(addProvider).toHaveBeenCalledTimes(1);
    const callArg = addProvider.mock.calls[0][0];
    expect(callArg.id).toBe('new-prov');
    expect(callArg.cred.kind).toBe('keychain');
    expect(callArg.plaintextApiKey).toBe('sk-test');

    expect(testProvider).toHaveBeenCalledWith('new-prov');
    // Inline test-result banner should now show.
    expect(wrapper.find('[data-testid="last-test-result"]').exists()).toBe(
      true,
    );
    expect(wrapper.find('[data-testid="last-test-result"]').text()).toContain(
      'Validated new-prov',
    );
  });

  it('renders error banner when listProviders rejects', async () => {
    const client = makeClient({
      listProviders: vi.fn().mockRejectedValue(new Error('boom')),
    });
    const wrapper = mount(ProvidersView, {
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(wrapper.text()).toContain('boom');
  });
});
