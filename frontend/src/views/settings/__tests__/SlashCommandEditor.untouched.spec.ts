/**
 * SlashCommandEditor — no validation error on an untouched field
 * (dogfood 2026-10-08 round 2: the New-command form showed "Name must
 * start with a lowercase letter" before the user typed anything).
 */
import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import SlashCommandEditor from '@/views/settings/SlashCommandEditor.vue';
import type { UserCommand } from '@/lib/types';

describe('SlashCommandEditor — untouched fields', () => {
  it('a new form shows no name error until the name is edited, and Save stays disabled', async () => {
    const w = mount(SlashCommandEditor, { props: { command: null, readOnly: false } });
    expect(w.find('[data-testid="cmd-name-error"]').exists()).toBe(false);
    expect((w.find('[data-testid="cmd-save-btn"]').element as HTMLButtonElement).disabled).toBe(true);

    await w.find('[data-testid="cmd-name-input"]').setValue('Bad Name');
    expect(w.find('[data-testid="cmd-name-error"]').text()).toContain('lowercase letter');

    await w.find('[data-testid="cmd-name-input"]').setValue('bughunt');
    expect(w.find('[data-testid="cmd-name-error"]').exists()).toBe(false);
  });

  it('an existing invalid command shows its error immediately', () => {
    const bad: UserCommand = {
      name: 'Bad Name',
      scope: 'global',
      kind: 'text',
      description: 'd',
      modelInvokable: false,
      body: 'x',
    };
    const w = mount(SlashCommandEditor, { props: { command: bad, readOnly: false } });
    expect(w.find('[data-testid="cmd-name-error"]').exists()).toBe(true);
  });
});
