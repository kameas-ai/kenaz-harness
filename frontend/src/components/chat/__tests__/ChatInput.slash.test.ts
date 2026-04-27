import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ChatInput from '@/components/chat/ChatInput.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';

function mountInput(
  props: Record<string, unknown> = {},
  seed: Partial<HarnessClient> = {},
) {
  return mount(ChatInput, {
    props,
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
}

beforeEach(() => {
  if (typeof URL.createObjectURL !== 'function') {
    Object.defineProperty(URL, 'createObjectURL', {
      configurable: true,
      value: vi.fn(() => 'blob://fake'),
    });
  }
  if (typeof URL.revokeObjectURL !== 'function') {
    Object.defineProperty(URL, 'revokeObjectURL', {
      configurable: true,
      value: vi.fn(),
    });
  }
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe('ChatInput slash-command branch', () => {
  it('emits slashCommand instead of send when input starts with /', async () => {
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/help');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('send')).toBeFalsy();
    expect(w.emitted('sendBlocks')).toBeFalsy();
    expect(w.emitted('slashCommand')).toBeTruthy();
    expect(w.emitted('slashCommand')![0]).toEqual(['/help']);
  });

  it('emits slashCommand for commands with arguments', async () => {
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/model gpt-4o');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('slashCommand')).toBeTruthy();
    expect(w.emitted('slashCommand')![0]).toEqual(['/model gpt-4o']);
    expect(w.emitted('send')).toBeFalsy();
  });

  it('does NOT emit slashCommand for normal text', async () => {
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('hello world');
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('slashCommand')).toBeFalsy();
    expect(w.emitted('send')).toBeTruthy();
  });

  it('renders the slash autocomplete dropdown when input starts with /', async () => {
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/');
    // Default fake client returns 7 commands; the dropdown should
    // render every one when query is empty.
    expect(w.find('[data-testid="slash-suggestions"]').exists()).toBe(true);
  });

  it('filters dropdown options as the user types', async () => {
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/he');
    await flushPromises();
    const opts = w.findAll('[data-testid^="slash-option-"]');
    expect(opts).toHaveLength(1);
    expect(opts[0].text()).toContain('help');
  });

  it('hides the dropdown once the user types whitespace (entering args land)', async () => {
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/model gpt');
    await flushPromises();
    expect(w.find('[data-testid="slash-suggestions"]').exists()).toBe(false);
  });

  it('Enter on a half-typed name commits the active autocomplete entry instead of sending', async () => {
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    // "/he" matches only /help; activeIndex 0 picks it.
    await textarea.setValue('/he');
    await flushPromises();
    await textarea.trigger('keydown', { key: 'Enter' });
    // Pressing Enter committed the autocomplete; no slash submit.
    expect(w.emitted('slashCommand')).toBeFalsy();
    // The textarea now reads "/help " (trailing space).
    expect((textarea.element as HTMLTextAreaElement).value).toBe('/help ');
  });

  it('Enter on an exact command name submits (does not re-commit)', async () => {
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/help');
    await flushPromises();
    await textarea.trigger('keydown', { key: 'Enter' });
    expect(w.emitted('slashCommand')).toBeTruthy();
    expect(w.emitted('slashCommand')![0]).toEqual(['/help']);
  });

  it('ArrowDown advances activeIndex through the filtered list', async () => {
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/');
    await flushPromises();
    // Initial: activeIndex 0 → first option (alphabetical "branch").
    let opts = w.findAll('[data-testid^="slash-option-"]');
    expect(opts[0].attributes('aria-selected')).toBe('true');
    await textarea.trigger('keydown', { key: 'ArrowDown' });
    await flushPromises();
    opts = w.findAll('[data-testid^="slash-option-"]');
    expect(opts[1].attributes('aria-selected')).toBe('true');
    expect(opts[0].attributes('aria-selected')).toBe('false');
  });

  it('Tab commits the active autocomplete entry', async () => {
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/m');
    await flushPromises();
    // /m matches both /model and /memorize. The fake harness client lists
    // them in source order [model, memorize]; the autocomplete preserves
    // that order and clamps activeIndex to 0, so Tab commits /model.
    await textarea.trigger('keydown', { key: 'Tab' });
    await flushPromises();
    expect((textarea.element as HTMLTextAreaElement).value).toBe('/model ');
  });
});
