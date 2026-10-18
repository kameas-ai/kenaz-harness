/**
 * undelivered-message-retry (owner dogfood 2026-10-07) at the view
 * boundary: a session reopened after an out-of-credits failure shows the
 * NOT DELIVERED badge on the message and the classified composer banner
 * (from the PERSISTED run outcome — a reload, not a live event), and Retry
 * re-dispatches through llm.startStream exactly once with no
 * sessions.appendMessage — the same message, no duplicate user row.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import ChatInput from '@/components/chat/ChatInput.vue';
import type { Message, Provider, TurnRun } from '@/lib/types';

const SID = 'sess-undelivered';

const MESSAGES: Message[] = [
  { id: 'u-1', sessionId: SID, role: 'user', content: 'Summarise the release notes', createdAt: '2026-10-07T00:00:00Z' },
];

const PROVIDER: Provider = {
  id: 'or-profile',
  name: 'OpenRouter',
  tier: 'personal',
  kind: 'openrouter',
  model: 'moonshotai/kimi-k3',
  models: ['moonshotai/kimi-k3'],
};

const PAY_RUN: TurnRun = {
  runId: 'chat-1', turnSpanId: 'u-1', graphId: 'chat_default', specDigest: 'sha256:ab',
  createdAt: '2026-10-07T00:00:00Z', outcome: 'failed', delivered: false,
  failureClass: 'user_actionable', failureCode: 'payment_required', failureStatus: 402,
  failureProvider: 'openrouter', failureSummary: 'Out of credits with OpenRouter',
  failureMessage: 'This request requires more credits.',
};

async function mountView(opts: { runs?: TurnRun[]; startStream?: () => Promise<string> } = {}) {
  const base = createFakeHarnessClient();
  const startStream = vi.fn(opts.startStream ?? (async () => 'chat-retry-1'));
  const appendMessage = vi.fn(base.sessions.appendMessage);
  const turnRuns = vi.fn(async (): Promise<TurnRun[]> => opts.runs ?? [PAY_RUN]);
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      { path: '/providers', component: defineComponent({ render: () => h('div') }) },
      { path: '/agentgraph/run/:runId', component: defineComponent({ render: () => h('div') }) },
      { path: '/agentgraph/run/:runId/graph', component: defineComponent({ render: () => h('div') }) },
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
                get: async () => ({
                  id: SID, name: 'undelivered', createdAt: '2026-10-07T00:00:00Z',
                  updatedAt: '2026-10-07T00:00:00Z', systemPrompt: '', contextKind: 'system' as const,
                }),
                listMessagesActive: async () => ({ messages: MESSAGES, sweptCount: 0 }),
                turnRuns,
                appendMessage,
              },
              llm: { ...base.llm, listProviders: async () => [PROVIDER], startStream },
            });
          },
        },
      ],
    },
    attachTo: document.body,
  });
  await flushPromises();
  return { w, startStream, appendMessage };
}

describe('SessionsView — NOT DELIVERED + Retry', () => {
  it('renders the persisted not-delivered state; Retry re-runs the same message once', async () => {
    setConnectionState('ready');
    const { w, startStream, appendMessage } = await mountView();

    const badge = w.find('[data-message-id="u-1"] [data-testid="undelivered-badge"]');
    expect(badge.exists()).toBe(true);
    expect(badge.text()).toContain('Out of credits with OpenRouter');
    const banner = w.find('[data-testid="delivery-banner"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('Add credits, then retry.');
    // Not "Send failed" — the message was saved; it just never reached the model.
    expect(w.text()).not.toContain('Send failed');

    await w.find('[data-testid="delivery-banner-retry"]').trigger('click');
    await flushPromises();
    expect(startStream).toHaveBeenCalledTimes(1);
    expect(startStream).toHaveBeenCalledWith('or-profile', SID, 'moonshotai/kimi-k3');
    expect(appendMessage).not.toHaveBeenCalled();

    // The run is in flight: badges and banner step aside, and the badge's
    // own Retry cannot fire a second dispatch.
    expect(w.find('[data-testid="delivery-banner"]').exists()).toBe(false);
    expect(w.find('[data-testid="undelivered-retry"]').exists()).toBe(false);
    expect(startStream).toHaveBeenCalledTimes(1);
    w.unmount();
  });

  it('a send while a Retry is still being dispatched is queued — one startStream, no second run', async () => {
    setConnectionState('ready');
    let resolveRetry!: (id: string) => void;
    const { w, startStream, appendMessage } = await mountView({
      startStream: () => new Promise<string>((r) => { resolveRetry = r; }),
    });
    await w.find('[data-testid="delivery-banner-retry"]').trigger('click');
    await flushPromises();
    expect(startStream).toHaveBeenCalledTimes(1);

    // Enter while the Retry's startStream has not resolved yet.
    w.findComponent(ChatInput).vm.$emit('send', 'and one more thing');
    await flushPromises();
    expect(startStream).toHaveBeenCalledTimes(1);
    expect(appendMessage).not.toHaveBeenCalled();
    expect(w.findComponent(ChatInput).props('queueDepth')).toBe(1);

    resolveRetry('chat-retry-1');
    await flushPromises();
    expect(startStream).toHaveBeenCalledTimes(1);
    w.unmount();
  });

  it('session_full is reported only by its own banner — no NOT DELIVERED badge, no delivery banner', async () => {
    setConnectionState('ready');
    const { w } = await mountView({
      runs: [{ ...PAY_RUN, failureCode: 'session_full', failureSummary: "The conversation no longer fits the model's context window" }],
    });
    expect(w.find('[data-testid="undelivered-badge"]').exists()).toBe(false);
    expect(w.find('[data-testid="delivery-banner"]').exists()).toBe(false);
    w.unmount();
  });
});
