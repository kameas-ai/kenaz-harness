/**
 * SessionsView — user slash-command routing (dogfood 2026-10-08 round 2).
 *
 * "/bughunt <text>" for a freshly created global PROMPT-kind command with
 * no declared inputs answered `slashcmd: unknown command: "bughunt"`.
 * Slashcmd_Get found the command; the view only ran user commands that
 * declared inputs and sent every other one to the BUILT-IN registry.
 * Pins: a no-input user command runs through Slashcmd_Run; a prompt
 * command's rendered body (+ the typed text) is sent as the user turn;
 * an unknown token gets honest copy.
 */
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import ChatInput from '@/components/chat/ChatInput.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import type { UserCommand } from '@/lib/types';

const SID = 's-slash';

const BUGHUNT: UserCommand = {
  name: 'bughunt',
  scope: 'global',
  kind: 'prompt',
  description: 'triage a bug',
  modelInvokable: true,
  body: 'Triage the reported bug.',
};

async function mountView(opts: {
  userCmd: UserCommand | null;
  runText?: string;
  runKind?: string;
  promptRendered?: boolean;
  builtinList?: () => Promise<unknown>;
}) {
  const base = createFakeHarnessClient();
  const appendMessage = vi.fn(async (id: string, role: string, content: string) => ({
    id: 'new', sessionId: id, role: role as 'user', content, createdAt: '2026-10-08T00:00:00Z',
  }));
  const startStream = vi.fn(async () => 'sub-llm');
  const get = vi.fn(async (name: string) => {
    if (opts.userCmd && name === opts.userCmd.name) return opts.userCmd;
    throw new Error(`slashcmd: command not found: "${name}"`);
  });
  const run = vi.fn(async () => ({
    kind: opts.runKind ?? 'info',
    text: opts.runText ?? 'Triage the reported bug.',
    metadata: opts.promptRendered === false ? {} : { prompt_rendered: true, slash_invocation: '/bughunt' },
  }));
  const execute = vi.fn(async (_sid: string, raw: string) => {
    const name = raw.slice(1).split(' ')[0];
    if (name === 'help') return { kind: 'info', text: 'built-in help' };
    if (name === 'pr') return { kind: 'info', text: 'fleet skill pr' };
    throw new Error(`slashcmd: unknown command: "${name}"`);
  });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      { path: '/providers', component: defineComponent({ render: () => h('div') }) },
    ],
  });
  await router.push(`/sessions/${SID}`);
  await router.isReady();
  const w = mount(SessionsView, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, {
              sessions: {
                ...base.sessions,
                get: async (id: string) => ({ id, name: 'slash', createdAt: '', updatedAt: '' }),
                listMessages: async () => [],
                listMessagesActive: async () => ({ messages: [], sweptCount: 0 }),
                appendMessage,
              },
              llm: {
                ...base.llm,
                listProviders: async () => [
                  { id: 'anthropic-p-1', name: 'Anthropic', tier: 'cloud', kind: 'anthropic', model: 'claude' },
                ],
                startStream,
              },
              slashcmd: { ...base.slashcmd, get, run },
              slash: {
                ...base.slash,
                execute,
                list:
                  opts.builtinList ??
                  (async () => [
                    { name: 'help', description: 'Help', comingSoon: false },
                    { name: 'pr', description: 'Org PR skill', comingSoon: false, isSkill: true },
                  ]),
              },
            } as never);
          },
        },
      ],
    },
    attachTo: document.body,
  });
  await flushPromises();
  return { w, get, run, execute, appendMessage, startStream };
}

describe('SessionsView — user slash-command routing', () => {
  beforeEach(() => setConnectionState('ready'));

  it('runs a no-input PROMPT user command and sends its rendered body plus the typed text as the turn', async () => {
    const { w, run, execute, appendMessage } = await mountView({ userCmd: BUGHUNT });
    w.findComponent(ChatInput).vm.$emit('slashCommand', '/bughunt the login button 500s');
    await flushPromises();

    expect(run).toHaveBeenCalledWith('bughunt', {}, SID, '', '', '');
    expect(execute).not.toHaveBeenCalled();
    expect(appendMessage).toHaveBeenCalled();
    const sent = appendMessage.mock.calls[0][2];
    expect(sent).toContain('Triage the reported bug.');
    expect(sent).toContain('the login button 500s');
    expect(w.text()).not.toContain('unknown command');
    w.unmount();
  });

  it('renders a no-input TEXT user command result instead of sending it', async () => {
    const { w, run, execute, appendMessage } = await mountView({
      userCmd: { ...BUGHUNT, name: 'motd', kind: 'text' },
      runText: 'Ship small, ship often.',
      promptRendered: false,
    });
    w.findComponent(ChatInput).vm.$emit('slashCommand', '/motd');
    await flushPromises();
    expect(run).toHaveBeenCalled();
    expect(execute).not.toHaveBeenCalled();
    expect(appendMessage).not.toHaveBeenCalled();
    expect(w.text()).toContain('Ship small, ship often.');
    w.unmount();
  });

  it('a built-in name wins over a user command of the same name', async () => {
    const { w, get, run, execute } = await mountView({ userCmd: { ...BUGHUNT, name: 'help', kind: 'text' } });
    w.findComponent(ChatInput).vm.$emit('slashCommand', '/help');
    await flushPromises();
    expect(execute).toHaveBeenCalled();
    expect(get).not.toHaveBeenCalled();
    expect(run).not.toHaveBeenCalled();
    expect(w.text()).toContain('built-in help');
    w.unmount();
  });

  it('a user command beats a fleet skill of the same name (user > skill)', async () => {
    const { w, run, execute } = await mountView({
      userCmd: { ...BUGHUNT, name: 'pr', kind: 'text' },
      runText: 'my own pr command',
      promptRendered: false,
    });
    w.findComponent(ChatInput).vm.$emit('slashCommand', '/pr');
    await flushPromises();
    expect(run).toHaveBeenCalled();
    expect(execute).not.toHaveBeenCalled();
    expect(w.text()).toContain('my own pr command');
    w.unmount();
  });

  it('a failed built-in list is retried, not cached as "no built-ins"', async () => {
    const builtinList = vi
      .fn()
      // Once for the composer's own autocomplete fetch at mount, once for
      // the view's first built-in check.
      .mockRejectedValueOnce(new Error('not wired yet'))
      .mockRejectedValueOnce(new Error('not wired yet'))
      .mockResolvedValue([{ name: 'help', description: 'Help', comingSoon: false }]);
    const { w, get } = await mountView({ userCmd: { ...BUGHUNT, name: 'help', kind: 'text' }, builtinList });
    w.findComponent(ChatInput).vm.$emit('slashCommand', '/help');
    await flushPromises();
    get.mockClear();
    w.findComponent(ChatInput).vm.$emit('slashCommand', '/help');
    await flushPromises();
    expect(get).not.toHaveBeenCalled(); // second time the built-in is known
    expect(w.text()).toContain('built-in help');
    w.unmount();
  });

  it('says "no user or built-in command" for a token neither registry knows', async () => {
    const { w, execute } = await mountView({ userCmd: null });
    w.findComponent(ChatInput).vm.$emit('slashCommand', '/nosuch thing');
    await flushPromises();
    expect(execute).toHaveBeenCalled();
    expect(w.text()).toContain('No user or built-in command named "/nosuch"');
    expect(w.text()).not.toContain('slashcmd: unknown command');
    w.unmount();
  });
});
