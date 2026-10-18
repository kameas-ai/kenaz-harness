/**
 * SessionsView — a model switch survives a re-seed (dogfood 2026-10-08
 * round 2: after Merge of a fork the MODEL label reverted from
 * sonnet-latest to haiku-latest). The session's selection is re-seeded
 * from the `kenaz.session.config.<id>` stash whenever the route's
 * session changes; NewSessionDialog wrote that stash, the switcher and
 * /model never did, so returning to the session restored the creation
 * model.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import ChatInput from '@/components/chat/ChatInput.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import { readSessionModel } from '@/lib/sessionModelStash';

const SID = 's-model';
const OTHER = 's-other';
const HAIKU = '~anthropic/claude-haiku-latest';
const SONNET = '~anthropic/claude-sonnet-latest';

describe('SessionsView — model switch persistence', () => {
  beforeEach(() => {
    setConnectionState('ready');
    window.localStorage.setItem(
      `kenaz.session.config.${SID}`,
      JSON.stringify({ providerId: 'openrouter-1', modelId: HAIKU }),
    );
  });
  afterEach(() => {
    window.localStorage.removeItem(`kenaz.session.config.${SID}`);
  });

  it('a /model switch is written to the session stash and survives leaving and returning', async () => {
    const base = createFakeHarnessClient();
    const startStream = vi.fn(async () => 'sub-llm');
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
                  get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
                  listMessages: async () => [],
                  listMessagesActive: async () => ({ messages: [], sweptCount: 0 }),
                  appendMessage: async (id: string, role: string, content: string) => ({
                    id: 'new', sessionId: id, role: role as 'user', content, createdAt: '',
                  }),
                },
                llm: {
                  ...base.llm,
                  listProviders: async () => [
                    {
                      id: 'openrouter-1', name: 'OpenRouter', tier: 'cloud', kind: 'openrouter',
                      model: HAIKU, models: [HAIKU, SONNET],
                    },
                  ],
                  startStream,
                },
                slashcmd: {
                  ...base.slashcmd,
                  get: async (name: string) => {
                    throw new Error(`not found: ${name}`);
                  },
                },
                slash: {
                  ...base.slash,
                  execute: async () => ({
                    kind: 'info',
                    text: `Model set to ${SONNET}`,
                    metadata: { providerId: 'openrouter-1', modelId: SONNET },
                  }),
                },
              } as never);
            },
          },
        ],
      },
      attachTo: document.body,
    });
    await flushPromises();

    w.findComponent(ChatInput).vm.$emit('slashCommand', `/model ${SONNET}`);
    await flushPromises();
    expect(readSessionModel(SID)?.modelId).toBe(SONNET);

    // Leave and come back — what Merge does to the view.
    await router.push(`/sessions/${OTHER}`);
    await flushPromises();
    await router.push(`/sessions/${SID}`);
    await flushPromises();

    w.findComponent(ChatInput).vm.$emit('send', 'hello again');
    await flushPromises();
    expect(startStream).toHaveBeenCalled();
    const lastCall = startStream.mock.calls.at(-1) as unknown[];
    expect(lastCall).toContain(SONNET);
    expect(lastCall).not.toContain(HAIKU);
    w.unmount();
  });
});
