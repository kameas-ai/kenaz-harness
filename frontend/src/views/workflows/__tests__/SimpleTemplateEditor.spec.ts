/**
 * SimpleTemplateEditor.spec.ts — covers the WP09 template editor:
 *
 *   1. picking the http_save template flips the visible fields and
 *      assembles the expected http_request → write_artifact YAML.
 *   2. picking single_llm + filling form + Save calls client.save with
 *      the assembled YAML, includes the prompt and model, emits saved.
 *   3. Save with an empty name surfaces a guard error and never calls
 *      client.save.
 */

import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import SimpleTemplateEditor from '../SimpleTemplateEditor.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsSaveOutput,
} from '@/lib/workflowsClient';

function savedOutput(yaml: string): WorkflowsSaveOutput {
  return {
    id: 'wf-tpl',
    name: 'Tpl',
    version: 1,
    hash: 'h',
    yaml,
    createdAt: '1970-01-01T00:00:00.000Z',
    updatedAt: '1970-01-01T00:00:00.000Z',
  };
}

describe('SimpleTemplateEditor', () => {
  it('saves a single_llm template and routes the YAML through client.save', async () => {
    const save = vi.fn((input: { yaml?: string }) =>
      Promise.resolve(savedOutput(input.yaml ?? '')),
    );
    const client = createFakeWorkflowsClient({ save });
    const wrapper = mount(SimpleTemplateEditor, { props: { client } });

    await wrapper
      .find<HTMLInputElement>('[data-testid="template-name"]')
      .setValue('My Greeting');
    await wrapper
      .find<HTMLInputElement>('[data-testid="template-description"]')
      .setValue('Says hi');
    await wrapper
      .find<HTMLSelectElement>('[data-testid="template-model"]')
      .setValue('claude-sonnet-4-6');
    await wrapper
      .find<HTMLTextAreaElement>('[data-testid="template-prompt"]')
      .setValue('Greet the user warmly.');

    await wrapper.find('[data-testid="template-save"]').trigger('click');
    await flushPromises();

    expect(save).toHaveBeenCalledOnce();
    const yaml = (save.mock.calls[0][0] as { yaml: string }).yaml;
    expect(yaml).toContain("id: 'my-greeting'");
    expect(yaml).toContain("name: 'My Greeting'");
    expect(yaml).toContain("description: 'Says hi'");
    expect(yaml).toContain('kind: model_turn');
    expect(yaml).toContain("model: 'claude-sonnet-4-6'");
    expect(yaml).toContain('Greet the user warmly.');
    expect(wrapper.emitted('saved')).toBeTruthy();
  });

  it('switches fields and emits an http_request → write_artifact YAML', async () => {
    const save = vi.fn((input: { yaml?: string }) =>
      Promise.resolve(savedOutput(input.yaml ?? '')),
    );
    const client = createFakeWorkflowsClient({ save });
    const wrapper = mount(SimpleTemplateEditor, { props: { client } });

    await wrapper
      .find<HTMLSelectElement>('[data-testid="template-picker"]')
      .setValue('http_save');

    // The model/prompt fields disappear; URL/artifact appear.
    expect(wrapper.find('[data-testid="template-prompt"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="template-url"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="template-artifact"]').exists()).toBe(true);

    await wrapper
      .find<HTMLInputElement>('[data-testid="template-name"]')
      .setValue('Fetcher');
    await wrapper
      .find<HTMLInputElement>('[data-testid="template-url"]')
      .setValue('https://api.example.com/data');
    await wrapper
      .find<HTMLInputElement>('[data-testid="template-artifact"]')
      .setValue('data.json');

    await wrapper.find('[data-testid="template-save"]').trigger('click');
    await flushPromises();

    const yaml = (save.mock.calls[0][0] as { yaml: string }).yaml;
    expect(yaml).toContain('kind: http_request');
    expect(yaml).toContain('method: GET');
    expect(yaml).toContain("url: 'https://api.example.com/data'");
    expect(yaml).toContain('kind: write_artifact');
    expect(yaml).toContain("path: 'data.json'");
    expect(yaml).toContain('{{ steps.fetch.body }}');
  });

  it('blocks save with an empty name and shows a guard error', async () => {
    const save = vi.fn();
    const client = createFakeWorkflowsClient({ save });
    const wrapper = mount(SimpleTemplateEditor, { props: { client } });

    await wrapper.find('[data-testid="template-save"]').trigger('click');
    await flushPromises();

    expect(save).not.toHaveBeenCalled();
    const err = wrapper.find('[data-testid="template-editor-error"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('name is required');
  });
});
