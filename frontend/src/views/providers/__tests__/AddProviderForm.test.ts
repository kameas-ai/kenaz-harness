import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AddProviderForm from '@/views/providers/AddProviderForm.vue';
import type { AddProviderInput, ModelInfo } from '@/lib/types';
import {
  createFakeHarnessClient,
  type HarnessClient,
} from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';

function makeClient(models: ModelInfo[] = []): HarnessClient {
  const fake = createFakeHarnessClient();
  fake.llm.listModels = vi.fn(async () => models);
  return fake;
}

function mountForm(client: HarnessClient) {
  return mount(AddProviderForm, {
    global: {
      provide: {
        [HarnessClientKey as symbol]: client,
      },
    },
  });
}

describe('AddProviderForm', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('blocks submit until the user has connected and picked a model', async () => {
    const wrapper = mountForm(makeClient([]));
    await wrapper.find('form').trigger('submit.prevent');
    expect(wrapper.emitted('submit')).toBeUndefined();
  });

  it('Connect probes the backend and populates the model dropdown', async () => {
    const client = makeClient([
      { id: 'claude-sonnet-4-5', displayName: 'Claude Sonnet 4.5' },
      { id: 'claude-opus-4-7', displayName: 'Claude Opus 4.7' },
    ]);
    const wrapper = mountForm(client);
    await wrapper
      .find('[data-testid="add-provider-apikey"]')
      .setValue('sk-test');
    await wrapper.find('[data-testid="add-provider-connect"]').trigger('click');
    await flushPromises();
    expect(client.llm.listModels).toHaveBeenCalledWith('anthropic', 'sk-test');
    expect(wrapper.find('[data-testid="add-provider-model"]').exists()).toBe(
      true,
    );
    const options = wrapper
      .find('[data-testid="add-provider-model"]')
      .findAll('option');
    expect(options).toHaveLength(2);
    expect(options[0].text()).toContain('Claude Sonnet 4.5');
  });

  it('emits submit with auto-derived id + keychain reference', async () => {
    const client = makeClient([
      { id: 'claude-sonnet-4-5', displayName: 'Claude Sonnet 4.5' },
    ]);
    const wrapper = mountForm(client);
    await wrapper
      .find('[data-testid="add-provider-apikey"]')
      .setValue('sk-secret');
    await wrapper.find('[data-testid="add-provider-connect"]').trigger('click');
    await flushPromises();
    await wrapper.find('form').trigger('submit.prevent');
    const emitted = wrapper.emitted('submit') as AddProviderInput[][];
    expect(emitted).toBeTruthy();
    const input = emitted[0][0];
    expect(input.id).toBe('anthropic-claude-sonnet-4-5');
    expect(input.kind).toBe('anthropic');
    expect(input.model).toBe('claude-sonnet-4-5');
    expect(input.name).toBe('Claude Sonnet 4.5');
    expect(input.cred.kind).toBe('keychain');
    expect(input.cred.locator).toBe(
      'kaneaz-harness/anthropic-claude-sonnet-4-5',
    );
    expect(input.plaintextApiKey).toBe('sk-secret');
  });

  it('honors a customized provider id', async () => {
    const client = makeClient([
      { id: 'claude-sonnet-4-5', displayName: 'Claude Sonnet 4.5' },
    ]);
    const wrapper = mountForm(client);
    await wrapper
      .find('[data-testid="add-provider-apikey"]')
      .setValue('sk-secret');
    await wrapper.find('[data-testid="add-provider-connect"]').trigger('click');
    await flushPromises();
    await wrapper
      .find('[data-testid="add-provider-customize-id"]')
      .trigger('click');
    await wrapper
      .find('[data-testid="add-provider-id"]')
      .setValue('my-anth');
    await wrapper.find('form').trigger('submit.prevent');
    const emitted = wrapper.emitted('submit') as AddProviderInput[][];
    expect(emitted[0][0].id).toBe('my-anth');
    expect(emitted[0][0].cred.locator).toBe('kaneaz-harness/my-anth');
  });

  it('falls back to manual model entry when /models returns empty', async () => {
    const wrapper = mountForm(makeClient([]));
    await wrapper.find('[data-testid="add-provider-apikey"]').setValue('k');
    await wrapper.find('[data-testid="add-provider-connect"]').trigger('click');
    await flushPromises();
    expect(
      wrapper.find('[data-testid="add-provider-model-manual"]').exists(),
    ).toBe(true);
    await wrapper
      .find('[data-testid="add-provider-model-manual"]')
      .setValue('claude-sonnet-4-5');
    await wrapper.find('form').trigger('submit.prevent');
    const emitted = wrapper.emitted('submit') as AddProviderInput[][];
    expect(emitted[0][0].model).toBe('claude-sonnet-4-5');
  });

  it('surfaces probe errors inline', async () => {
    const client = makeClient();
    client.llm.listModels = vi.fn(async () => {
      throw new Error('401 unauthorized');
    });
    const wrapper = mountForm(client);
    await wrapper
      .find('[data-testid="add-provider-apikey"]')
      .setValue('bad-key');
    await wrapper.find('[data-testid="add-provider-connect"]').trigger('click');
    await flushPromises();
    expect(
      wrapper.find('[data-testid="add-provider-probe-error"]').text(),
    ).toContain('401 unauthorized');
  });

  it('skips api key for ollama (local-only)', async () => {
    const client = makeClient([{ id: 'llama3.1:8b', displayName: 'Llama 3.1' }]);
    const wrapper = mountForm(client);
    await wrapper.find('[data-testid="add-provider-kind"]').setValue('ollama');
    expect(wrapper.find('[data-testid="add-provider-apikey"]').exists()).toBe(
      false,
    );
  });

  it('emits cancel when cancel button clicked', async () => {
    const wrapper = mountForm(makeClient());
    const buttons = wrapper.findAll('button');
    const cancel = buttons.find((b) => b.text() === 'Cancel');
    expect(cancel).toBeTruthy();
    await cancel!.trigger('click');
    expect(wrapper.emitted('cancel')).toBeTruthy();
  });
});
