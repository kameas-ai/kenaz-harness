import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import AddProviderForm from '@/views/providers/AddProviderForm.vue';
import type { AddProviderInput } from '@/lib/types';

describe('AddProviderForm', () => {
  it('reports validation errors for empty inputs', async () => {
    const wrapper = mount(AddProviderForm);
    // Trigger submit on empty form — nothing should be emitted.
    await wrapper.find('form').trigger('submit.prevent');
    expect(wrapper.emitted('submit')).toBeUndefined();
    // Fill ID with invalid characters.
    await wrapper
      .find('[data-testid="add-provider-id"]')
      .setValue('not allowed!');
    await wrapper.find('form').trigger('submit.prevent');
    expect(wrapper.emitted('submit')).toBeUndefined();
    expect(wrapper.find('[data-testid="add-provider-id-error"]').exists()).toBe(
      true,
    );
  });

  it('emits submit with keychain reference and plaintext key', async () => {
    const wrapper = mount(AddProviderForm);
    await wrapper
      .find('[data-testid="add-provider-id"]')
      .setValue('anth-personal');
    await wrapper
      .find('[data-testid="add-provider-name"]')
      .setValue('Anthropic Personal');
    await wrapper
      .find('[data-testid="add-provider-model"]')
      .setValue('claude-sonnet');
    await wrapper
      .find('[data-testid="add-provider-apikey"]')
      .setValue('sk-secret');
    await wrapper.find('form').trigger('submit.prevent');
    const emitted = wrapper.emitted('submit') as AddProviderInput[][];
    expect(emitted).toBeTruthy();
    expect(emitted[0]).toBeTruthy();
    const input = emitted[0][0];
    expect(input.id).toBe('anth-personal');
    expect(input.cred.kind).toBe('keychain');
    expect(input.cred.locator).toBe('kaneaz-harness/anth-personal');
    expect(input.plaintextApiKey).toBe('sk-secret');
  });

  it('requires region when kind=bedrock', async () => {
    const wrapper = mount(AddProviderForm);
    await wrapper
      .find('[data-testid="add-provider-id"]')
      .setValue('bedrock-prof');
    await wrapper
      .find('[data-testid="add-provider-name"]')
      .setValue('Bedrock');
    await wrapper
      .find('[data-testid="add-provider-model"]')
      .setValue('anthropic.claude-3-sonnet');
    await wrapper
      .find('[data-testid="add-provider-kind"]')
      .setValue('bedrock');
    await wrapper
      .find('[data-testid="add-provider-apikey"]')
      .setValue('aws-key');
    // Region empty — submit should be blocked.
    await wrapper.find('form').trigger('submit.prevent');
    expect(wrapper.emitted('submit')).toBeUndefined();
    await wrapper
      .find('[data-testid="add-provider-region"]')
      .setValue('us-east-1');
    await wrapper.find('form').trigger('submit.prevent');
    const emitted = wrapper.emitted('submit') as AddProviderInput[][];
    expect(emitted).toBeTruthy();
    expect(emitted[0][0].region).toBe('us-east-1');
  });

  it('skips api key field for ollama (local-only)', async () => {
    const wrapper = mount(AddProviderForm);
    await wrapper
      .find('[data-testid="add-provider-kind"]')
      .setValue('ollama');
    await wrapper
      .find('[data-testid="add-provider-id"]')
      .setValue('ollama-local');
    await wrapper
      .find('[data-testid="add-provider-name"]')
      .setValue('Ollama');
    await wrapper
      .find('[data-testid="add-provider-model"]')
      .setValue('llama3.1');
    expect(
      wrapper.find('[data-testid="add-provider-apikey"]').exists(),
    ).toBe(false);
    await wrapper.find('form').trigger('submit.prevent');
    const emitted = wrapper.emitted('submit') as AddProviderInput[][];
    expect(emitted).toBeTruthy();
    expect(emitted[0][0].plaintextApiKey).toBeUndefined();
  });

  it('emits cancel when cancel button clicked', async () => {
    const wrapper = mount(AddProviderForm);
    const buttons = wrapper.findAll('button');
    const cancel = buttons.find((b) => b.text() === 'Cancel');
    expect(cancel).toBeTruthy();
    await cancel!.trigger('click');
    expect(wrapper.emitted('cancel')).toBeTruthy();
  });
});
