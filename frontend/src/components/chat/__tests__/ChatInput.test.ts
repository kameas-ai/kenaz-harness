import { describe, it, expect } from 'vitest';
import { mount } from '@vue/test-utils';
import ChatInput from '@/components/chat/ChatInput.vue';

describe('ChatInput (chat-ui)', () => {
  it('emits send on Enter when there is content', async () => {
    const w = mount(ChatInput);
    const textarea = w.find('textarea');
    await textarea.setValue('hello');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('send')).toBeTruthy();
    expect(w.emitted('send')![0]).toEqual(['hello']);
  });

  it('does not emit send when content is empty', async () => {
    const w = mount(ChatInput);
    const textarea = w.find('textarea');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('send')).toBeFalsy();
  });

  it('inserts newline on Shift+Enter (does not emit send)', async () => {
    const w = mount(ChatInput);
    const textarea = w.find('textarea');
    await textarea.setValue('line one');
    await textarea.trigger('keydown', { key: 'Enter', shiftKey: true });
    expect(w.emitted('send')).toBeFalsy();
  });

  it('disables the textarea while streaming and shows Cancel', async () => {
    const w = mount(ChatInput, {
      props: { streaming: true, modelValue: 'pending' },
    });
    const textarea = w.find('textarea');
    expect(textarea.attributes('disabled')).toBeDefined();
    const cancel = w.find('button[aria-label="Cancel stream"]');
    expect(cancel.exists()).toBe(true);
    await cancel.trigger('click');
    expect(w.emitted('cancel')).toBeTruthy();
  });

  it('Enter does not send while streaming', async () => {
    const w = mount(ChatInput, {
      props: { streaming: true, modelValue: 'queued' },
    });
    const textarea = w.find('textarea');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('send')).toBeFalsy();
  });

  it('disabled prop also gates send', async () => {
    const w = mount(ChatInput, {
      props: { disabled: true },
    });
    const textarea = w.find('textarea');
    await textarea.setValue('blocked');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('send')).toBeFalsy();
    expect(textarea.attributes('disabled')).toBeDefined();
  });

  it('renders token + cost estimate placeholders', () => {
    const w = mount(ChatInput, {
      props: {
        estimate: { tokens: 1234, usd: 0.0123 },
      },
    });
    expect(w.text()).toContain('1,234 tok');
    expect(w.text()).toContain('$0.0123');
  });

  it('exposes accessible label and aria-multiline on textarea', () => {
    const w = mount(ChatInput);
    const t = w.find('textarea');
    expect(t.attributes('aria-label')).toBe('Message');
    expect(t.attributes('aria-multiline')).toBe('true');
  });

  it('clears the textarea after sending', async () => {
    const w = mount(ChatInput);
    const textarea = w.find('textarea');
    await textarea.setValue('msg');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('update:modelValue')).toBeTruthy();
    const updates = w.emitted('update:modelValue') as unknown[][];
    expect(updates[updates.length - 1]).toEqual(['']);
  });
});
