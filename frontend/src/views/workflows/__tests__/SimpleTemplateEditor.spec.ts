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
import { readFileSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import SimpleTemplateEditor from '../SimpleTemplateEditor.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsSaveOutput,
} from '@/lib/workflowsClient';

// automation-actually-runs-01PMZ404 UNIT-9 / AC-010 rule 1: "fails if the
// test compares assembled strings to expected strings — that passes with
// path:/no-title: present, which is how this shipped." The fixtures below
// are the SAME files core/workflows/starter_templates_test.go validates
// through the real backend loader/validator — byte-equality here is what
// catches drift between the two sides, since vitest cannot itself invoke
// the Go validator.
const HERE = dirname(fileURLToPath(import.meta.url));
const TESTDATA = resolve(HERE, '../../../../../core/workflows/testdata/starter_templates');
function fixture(name: string): string {
  return readFileSync(resolve(TESTDATA, name), 'utf8');
}

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
    // automation-actually-runs-01PMZ404 UNIT-9 / D-10: `path:` is not a
    // Step field — every save of this template failed backend
    // validation ("write_artifact requires title"). The mustache-style
    // `{{ steps.fetch.body }}` also matched nothing in the engine's ref
    // grammar (`${step.<name>.output}`), so even a save that got past
    // validation would run and store the literal template text. Pin the
    // corrected shape: a real `title`, and `content_ref` sourced from
    // the engine's actual ref syntax (the value is the whole
    // {status,headers,body} envelope, not just the body — spec X-10, so
    // this template is honest about saving "the response").
    expect(yaml).not.toContain('path:');
    expect(yaml).not.toContain('{{');
    expect(yaml).toContain("title: 'data.json'");
    expect(yaml).toContain('content_ref: ${step.fetch.output}');
    expect(yaml).toContain('mime_type: application/json');
    expect(yaml).toContain('inputs_from: [fetch]');
  });

  it('emits a plan_execute YAML using the real ${...} ref grammar, not mustache', async () => {
    const save = vi.fn((input: { yaml?: string }) =>
      Promise.resolve(savedOutput(input.yaml ?? '')),
    );
    const client = createFakeWorkflowsClient({ save });
    const wrapper = mount(SimpleTemplateEditor, { props: { client } });

    await wrapper
      .find<HTMLSelectElement>('[data-testid="template-picker"]')
      .setValue('plan_execute');
    await wrapper
      .find<HTMLInputElement>('[data-testid="template-name"]')
      .setValue('Planner');

    await wrapper.find('[data-testid="template-save"]').trigger('click');
    await flushPromises();

    const yaml = (save.mock.calls[0][0] as { yaml: string }).yaml;
    // automation-actually-runs-01PMZ404 UNIT-9 / D-10: the old
    // `{{ steps.plan.output }}` token matched nothing in the engine's
    // grammar and was passed to the model VERBATIM — the workflow
    // "succeeded" while never actually chaining. Pin the fix: the real
    // ref syntax, plus an explicit inputs_from declaring the dependency
    // rather than leaving it implied by declaration order.
    expect(yaml).not.toContain('{{');
    expect(yaml).toContain('Execute the plan from ${step.plan.output}');
    expect(yaml).toContain('inputs_from: [plan]');
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

  describe('assembleYaml matches the Go-validated fixtures byte-for-byte', () => {
    // Canonical input set: only "name" set, everything else left at its
    // default. Matches core/workflows/testdata/starter_templates/*.yaml
    // exactly — if either side drifts, this test (or
    // starter_templates_test.go) catches it.
    async function assembled(templateId: 'single_llm' | 'plan_execute' | 'http_save') {
      const client = createFakeWorkflowsClient({});
      const wrapper = mount(SimpleTemplateEditor, { props: { client } });
      await wrapper
        .find<HTMLSelectElement>('[data-testid="template-picker"]')
        .setValue(templateId);
      await wrapper
        .find<HTMLInputElement>('[data-testid="template-name"]')
        .setValue('Test Workflow');
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      return (wrapper.vm as any).assembleYaml() as string;
    }

    it('single_llm', async () => {
      expect(await assembled('single_llm')).toBe(fixture('single_llm.yaml'));
    });

    it('plan_execute', async () => {
      expect(await assembled('plan_execute')).toBe(fixture('plan_execute.yaml'));
    });

    it('http_save', async () => {
      expect(await assembled('http_save')).toBe(fixture('http_save.yaml'));
    });
  });
});
