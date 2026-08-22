/**
 * SlashCommandEditor.spec.ts
 *
 * Tests for the slash command authoring form.
 * user-slash-commands-01KQ8TD9 WP07.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';

import SlashCommandEditor from '@/views/settings/SlashCommandEditor.vue';
import type { UserCommand } from '@/lib/types';

const TEXT_CMD: UserCommand = {
  name: 'standup',
  scope: 'global',
  kind: 'text',
  description: 'Daily standup',
  modelInvokable: false,
  body: 'What did I do today?',
};

const TOOL_CMD: UserCommand = {
  name: 'deploy',
  scope: 'global',
  kind: 'tool',
  description: 'Deploy to staging',
  modelInvokable: false,
  tool: 'kenaz__bash',
  toolArgsTemplate: './scripts/deploy.sh --env {{env}}',
};

function mountEditor(cmd: UserCommand | null = TEXT_CMD, opts: { readOnly?: boolean } = {}) {
  const onSave = vi.fn();
  const onCancel = vi.fn();
  const onDelete = vi.fn();
  const wrapper = mount(SlashCommandEditor, {
    props: {
      command: cmd,
      readOnly: opts.readOnly ?? false,
    },
  });
  wrapper.vm.$el; // trigger mount
  return { wrapper, onSave, onCancel, onDelete };
}

describe('SlashCommandEditor', () => {
  describe('new command (null)', () => {
    it('renders with empty name', () => {
      const { wrapper } = mountEditor(null);
      const nameInput = wrapper.find('[data-testid="cmd-name-input"]') as ReturnType<typeof wrapper.find>;
      expect((nameInput.element as HTMLInputElement).value).toBe('');
    });

    it('save button is disabled when name is empty', () => {
      const { wrapper } = mountEditor(null);
      const saveBtn = wrapper.find('[data-testid="cmd-save-btn"]');
      expect((saveBtn.element as HTMLButtonElement).disabled).toBe(true);
    });

    it('save button enables when form is valid', async () => {
      const { wrapper } = mountEditor(null);
      await wrapper.find('[data-testid="cmd-name-input"]').setValue('greet');
      await wrapper.find('[data-testid="cmd-body-textarea"]').setValue('Hello!');
      await wrapper.vm.$nextTick();
      const saveBtn = wrapper.find('[data-testid="cmd-save-btn"]');
      expect((saveBtn.element as HTMLButtonElement).disabled).toBe(false);
    });
  });

  describe('edit existing command', () => {
    it('populates form fields from command prop', () => {
      const { wrapper } = mountEditor(TEXT_CMD);
      const nameInput = wrapper.find('[data-testid="cmd-name-input"]') as ReturnType<typeof wrapper.find>;
      expect((nameInput.element as HTMLInputElement).value).toBe('standup');
    });

    it('name field is disabled when editing existing', () => {
      const { wrapper } = mountEditor(TEXT_CMD);
      const nameInput = wrapper.find('[data-testid="cmd-name-input"]') as ReturnType<typeof wrapper.find>;
      expect((nameInput.element as HTMLInputElement).disabled).toBe(true);
    });

    it('emits save with correct payload', async () => {
      const { wrapper } = mountEditor(TEXT_CMD);
      await wrapper.find('[data-testid="cmd-save-btn"]').trigger('click');
      const saves = wrapper.emitted('save');
      expect(saves).toHaveLength(1);
      const saved = (saves![0] as [UserCommand])[0];
      expect(saved.name).toBe('standup');
      expect(saved.body).toBe('What did I do today?');
    });

    it('emits cancel on cancel click', async () => {
      const { wrapper } = mountEditor(TEXT_CMD);
      await wrapper.find('[data-testid="cmd-cancel-btn"]').trigger('click');
      expect(wrapper.emitted('cancel')).toHaveLength(1);
    });

    it('emits delete on delete click', async () => {
      vi.spyOn(window, 'confirm').mockReturnValueOnce(true);
      const { wrapper } = mountEditor(TEXT_CMD);
      await wrapper.find('[data-testid="cmd-delete-btn"]').trigger('click');
      expect(wrapper.emitted('delete')).toHaveLength(1);
      const [name] = (wrapper.emitted('delete')![0] as [string]);
      expect(name).toBe('standup');
    });
  });

  describe('kind-aware fields', () => {
    it('renders body textarea for kind=text', () => {
      const { wrapper } = mountEditor(TEXT_CMD);
      expect(wrapper.find('[data-testid="cmd-body-textarea"]').exists()).toBe(true);
      expect(wrapper.find('[data-testid="cmd-tool-input"]').exists()).toBe(false);
    });

    it('renders tool fields for kind=tool', () => {
      const { wrapper } = mountEditor(TOOL_CMD);
      expect(wrapper.find('[data-testid="cmd-tool-input"]').exists()).toBe(true);
      expect(wrapper.find('[data-testid="cmd-tool-args-textarea"]').exists()).toBe(true);
      expect(wrapper.find('[data-testid="cmd-body-textarea"]').exists()).toBe(false);
    });

    it('renders builtin var chips for kind=prompt', async () => {
      const promptCmd: UserCommand = {
        ...TEXT_CMD,
        name: 'standup',
        kind: 'prompt',
        body: 'Hello {{session_id}}',
      };
      const { wrapper } = mountEditor(promptCmd);
      expect(wrapper.find('[data-testid="builtin-var-chips"]').exists()).toBe(true);
    });

    it('switches tool fields when kind changes to tool', async () => {
      const { wrapper } = mountEditor(null);
      await wrapper.find('[data-testid="cmd-kind-select"]').setValue('tool');
      await wrapper.vm.$nextTick();
      expect(wrapper.find('[data-testid="cmd-tool-input"]').exists()).toBe(true);
    });
  });

  describe('validation', () => {
    it('shows name error for invalid name', async () => {
      const { wrapper } = mountEditor(null);
      await wrapper.find('[data-testid="cmd-name-input"]').setValue('BAD NAME!');
      await wrapper.vm.$nextTick();
      expect(wrapper.find('[data-testid="cmd-name-error"]').exists()).toBe(true);
    });

    it('shows body error for empty body on kind=text', async () => {
      const { wrapper } = mountEditor(null);
      await wrapper.find('[data-testid="cmd-name-input"]').setValue('greet');
      await wrapper.find('[data-testid="cmd-body-textarea"]').setValue('  ');
      await wrapper.vm.$nextTick();
      expect(wrapper.find('[data-testid="cmd-body-error"]').exists()).toBe(true);
    });
  });

  describe('YAML toggle', () => {
    it('shows YAML preview on enter-yaml-mode', async () => {
      const { wrapper } = mountEditor(TEXT_CMD);
      await wrapper.find('[data-testid="enter-yaml-mode"]').trigger('click');
      expect(wrapper.find('[data-testid="yaml-preview"]').exists()).toBe(true);
    });

    it('returns to form on exit-yaml-mode', async () => {
      const { wrapper } = mountEditor(TEXT_CMD);
      await wrapper.find('[data-testid="enter-yaml-mode"]').trigger('click');
      await wrapper.find('[data-testid="exit-yaml-mode"]').trigger('click');
      expect(wrapper.find('[data-testid="yaml-preview"]').exists()).toBe(false);
      expect(wrapper.find('[data-testid="cmd-body-textarea"]').exists()).toBe(true);
    });

    it('save button disabled in YAML mode', async () => {
      const { wrapper } = mountEditor(TEXT_CMD);
      await wrapper.find('[data-testid="enter-yaml-mode"]').trigger('click');
      const saveBtn = wrapper.find('[data-testid="cmd-save-btn"]');
      expect((saveBtn.element as HTMLButtonElement).disabled).toBe(true);
    });
  });

  describe('read-only mode', () => {
    it('hides YAML toggle in read-only mode', () => {
      const { wrapper } = mountEditor(TEXT_CMD, { readOnly: true });
      expect(wrapper.find('[data-testid="enter-yaml-mode"]').exists()).toBe(false);
    });

    it('disables inputs in read-only mode', () => {
      const { wrapper } = mountEditor(TEXT_CMD, { readOnly: true });
      const nameInput = wrapper.find('[data-testid="cmd-name-input"]') as ReturnType<typeof wrapper.find>;
      expect((nameInput.element as HTMLInputElement).disabled).toBe(true);
    });

    it('shows Close button in read-only mode', () => {
      const { wrapper } = mountEditor(TEXT_CMD, { readOnly: true });
      // No delete button in read-only
      expect(wrapper.find('[data-testid="cmd-delete-btn"]').exists()).toBe(false);
      // But cancel/close is present
      expect(wrapper.find('[data-testid="cmd-cancel-btn"]').exists()).toBe(true);
    });
  });
});
