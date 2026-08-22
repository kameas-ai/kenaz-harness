/**
 * ChatInput.wp04ServedGating.test.ts — served-mode-is-a-real-mode-01PMZ707
 * WP04. Pins the per-affordance port/gate decisions that live in
 * ChatInput.vue: the paperclip/attachments dial (AC-711) and the `/`
 * slash-menu gate.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ChatInput from '@/components/chat/ChatInput.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { ServedUnsupportedError } from '@/lib/errors';
import type { HarnessClient } from '@/lib/harnessClient';

let servedModeFlag = false;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return { ...actual, isServedMode: () => servedModeFlag };
});

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
  servedModeFlag = false;
});

describe('ChatInput — multimodal dial (AC-711)', () => {
  it('keeps the paperclip visible when getMultimodalInput resolves true (desktop default)', async () => {
    const w = mountInput();
    await flushPromises();
    expect(w.find('[data-testid="chat-input-paperclip"]').exists()).toBe(true);
  });

  it('hides the paperclip on a ServedUnsupportedError rejection, instead of assuming enabled', async () => {
    const w = mountInput(
      {},
      {
        settings: {
          ...createFakeHarnessClient().settings,
          getMultimodalInput: async () => {
            throw new ServedUnsupportedError('Settings_GetMultimodalInput');
          },
        },
      },
    );
    await flushPromises();
    // *Falsify*: restore the bare `catch {}` that kept the `true` default →
    // this assertion goes red because the paperclip renders anyway.
    expect(w.find('[data-testid="chat-input-paperclip"]').exists()).toBe(false);
  });

  it('keeps the paperclip visible (best-effort default) on a non-served rejection', async () => {
    const w = mountInput(
      {},
      {
        settings: {
          ...createFakeHarnessClient().settings,
          getMultimodalInput: async () => {
            throw new Error('settings store unreadable');
          },
        },
      },
    );
    await flushPromises();
    expect(w.find('[data-testid="chat-input-paperclip"]').exists()).toBe(true);
  });
});

describe('ChatInput — `/` slash menu (WP04 gate)', () => {
  it('opens the dropdown and fetches the command list in desktop mode', async () => {
    servedModeFlag = false;
    const list = vi.fn(async () => [
      { name: 'help', description: 'Show help', args: [], comingSoon: false },
    ]);
    const w = mountInput(
      {},
      { slash: { ...createFakeHarnessClient().slash, list } },
    );
    await flushPromises();
    expect(list).toHaveBeenCalled();
    const textarea = w.find('textarea');
    await textarea.setValue('/');
    await textarea.trigger('input');
    await flushPromises();
    expect(w.findComponent({ name: 'SlashAutocomplete' }).exists()).toBe(true);
  });

  it('sends a leading-slash message as a normal message instead of calling slash.execute under served mode', async () => {
    servedModeFlag = true;
    const w = mountInput();
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/help');
    await textarea.trigger('keydown', { key: 'Enter' });
    // *Falsify*: drop `&& !served` from ChatInput.vue's send() slash
    // branch -- this goes red because 'slashCommand' would be emitted
    // instead of 'send', reaching SessionsView.vue's unguarded
    // client.slash.execute() call.
    expect(w.emitted('slashCommand')).toBeFalsy();
    expect(w.emitted('send')).toBeTruthy();
    expect(w.emitted('send')![0]).toEqual(['/help']);
  });

  it('never fetches the command list and never opens the dropdown under served mode', async () => {
    servedModeFlag = true;
    const list = vi.fn(async () => [
      { name: 'help', description: 'Show help', args: [], comingSoon: false },
    ]);
    const w = mountInput(
      {},
      { slash: { ...createFakeHarnessClient().slash, list } },
    );
    await flushPromises();
    // *Falsify*: remove the `if (served) return;` guard on the onMounted
    // fetch → `list` gets called even though Slash_Execute can never
    // complete in served mode.
    expect(list).not.toHaveBeenCalled();
    const textarea = w.find('textarea');
    await textarea.setValue('/');
    await textarea.trigger('input');
    await flushPromises();
    expect(w.findComponent({ name: 'SlashAutocomplete' }).exists()).toBe(false);
  });
});
