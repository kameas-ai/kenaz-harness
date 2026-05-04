/**
 * WorkflowEditor.spec.ts — covers the WP09 YAML editor:
 *
 *   1. happy path: edit YAML, click Save, client.save called with the
 *      current textarea contents and a `saved` event is emitted.
 *   2. validation error path: backend rejects with `step "foo": ...`,
 *      the inline error banner appears and the offending step name is
 *      surfaced.
 *   3. cancel: clicking Cancel emits `cancel` without invoking save.
 */

import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import WorkflowEditor from '../WorkflowEditor.vue';
import {
  createFakeWorkflowsClient,
  type WorkflowsSaveOutput,
} from '@/lib/workflowsClient';

function savedOutput(yaml: string): WorkflowsSaveOutput {
  return {
    id: 'wf-saved',
    name: 'Saved',
    version: 1,
    hash: 'h',
    yaml,
    createdAt: '1970-01-01T00:00:00.000Z',
    updatedAt: '1970-01-01T00:00:00.000Z',
  };
}

describe('WorkflowEditor', () => {
  it('saves the textarea contents and emits `saved`', async () => {
    const save = vi.fn((input: { yaml?: string }) =>
      Promise.resolve(savedOutput(input.yaml ?? '')),
    );
    const client = createFakeWorkflowsClient({ save });
    const wrapper = mount(WorkflowEditor, {
      props: { client, initialYaml: 'id: hello\n' },
    });

    const textarea = wrapper.find<HTMLTextAreaElement>(
      '[data-testid="workflow-editor-textarea"]',
    );
    expect(textarea.element.value).toBe('id: hello\n');

    await textarea.setValue('id: hello\nname: hi\n');
    await wrapper.find('[data-testid="workflow-editor-save"]').trigger('click');
    await flushPromises();

    expect(save).toHaveBeenCalledWith({ yaml: 'id: hello\nname: hi\n' });
    expect(wrapper.emitted('saved')).toBeTruthy();
    expect(wrapper.find('[data-testid="workflow-editor-error"]').exists()).toBe(
      false,
    );
  });

  it('surfaces validation errors inline with the offending step name', async () => {
    const save = vi.fn(() =>
      Promise.reject(new Error('step "fetch": missing required field "url"')),
    );
    const client = createFakeWorkflowsClient({ save });
    const wrapper = mount(WorkflowEditor, {
      props: { client, initialYaml: 'id: bad\n' },
    });

    await wrapper.find('[data-testid="workflow-editor-save"]').trigger('click');
    await flushPromises();

    const banner = wrapper.find('[data-testid="workflow-editor-error"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('missing required field');
    const stepHint = wrapper.find('[data-testid="workflow-editor-error-step"]');
    expect(stepHint.exists()).toBe(true);
    expect(stepHint.text()).toContain('fetch');
    expect(wrapper.emitted('saved')).toBeFalsy();
  });

  it('emits `cancel` without saving when Cancel is clicked', async () => {
    const save = vi.fn();
    const client = createFakeWorkflowsClient({ save });
    const wrapper = mount(WorkflowEditor, {
      props: { client, initialYaml: 'id: discarded\n' },
    });

    await wrapper.find('[data-testid="workflow-editor-cancel"]').trigger('click');

    expect(save).not.toHaveBeenCalled();
    expect(wrapper.emitted('cancel')).toBeTruthy();
  });

  it('Save+Run saves first then triggers run() with the saved id', async () => {
    const save = vi.fn(() => Promise.resolve(savedOutput('id: x\n')));
    const run = vi.fn(() =>
      Promise.resolve({
        runId: 'r1',
        workflowId: 'wf-saved',
        status: 'completed',
        steps: [],
      }),
    );
    const client = createFakeWorkflowsClient({ save, run });
    const wrapper = mount(WorkflowEditor, {
      props: { client, initialYaml: 'id: x\n' },
    });

    await wrapper.find('[data-testid="workflow-editor-run"]').trigger('click');
    await flushPromises();

    expect(save).toHaveBeenCalledOnce();
    expect(run).toHaveBeenCalledWith('wf-saved', {});
    expect(wrapper.emitted('ran')).toBeTruthy();
  });
});
