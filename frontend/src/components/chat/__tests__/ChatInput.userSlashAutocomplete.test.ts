/**
 * ChatInput — user commands in the slash autocomplete
 * (dogfood 2026-10-08 round 2: "/" opened the dropdown but "/bug" did
 * not — the list held only built-ins, so the user's /bughunt filtered to
 * nothing and the dropdown looked like it never opened).
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ChatInput from '@/components/chat/ChatInput.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';

function mountWithUserCommands(list: () => Promise<unknown>) {
  const base = createFakeHarnessClient();
  return mount(ChatInput, {
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, { slashcmd: { ...base.slashcmd, list } } as never);
          },
        },
      ],
    },
  });
}

describe('ChatInput slash autocomplete — user commands', () => {
  it('"/bug" offers the user\'s /bughunt, marked as a user command', async () => {
    const list = vi.fn(async () => [
      { name: 'bughunt', scope: 'global', kind: 'prompt', description: 'Triage a bug', modelInvokable: true },
    ]);
    const w = mountWithUserCommands(list);
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/bug');
    await flushPromises();
    expect(list).toHaveBeenCalledWith('');
    expect(w.find('[data-testid="slash-suggestions"]').exists()).toBe(true);
    const opts = w.findAll('[data-testid^="slash-option-"]');
    expect(opts).toHaveLength(1);
    expect(opts[0].text()).toContain('bughunt');
    expect(opts[0].find('[data-testid="slash-user-chip"]').exists()).toBe(true);
  });

  it('a user-command lookup failure leaves the built-ins working', async () => {
    const w = mountWithUserCommands(async () => {
      throw new Error('store not wired');
    });
    await flushPromises();
    const textarea = w.find('textarea');
    await textarea.setValue('/he');
    await flushPromises();
    expect(w.findAll('[data-testid^="slash-option-"]')).toHaveLength(1);
  });
});
