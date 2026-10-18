/**
 * SessionsView × tool-context-budget-01TCBUD01 WP06: a request_too_large
 * failure names the tool definitions' share of the model's window and its
 * "Open tools" opens the composer Tools menu (which then reads this
 * session's resolved tools); the MODEL row's "caches prompts" badge
 * renders only when the model's catalog entry says so.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import type { Message, Provider, ServerSchemaCost, SessionUsage, TurnRun } from '@/lib/types';

const SID = 'sess-too-large';
const MODEL = 'moonshotai/kimi-k3';

const MESSAGES: Message[] = [
  { id: 'u-1', sessionId: SID, role: 'user', content: 'Send the weekly report', createdAt: '2026-10-09T00:00:00Z' },
];

const TOO_LARGE: TurnRun = {
  runId: 'chat-1', turnSpanId: 'u-1', graphId: 'chat_default', specDigest: 'sha256:ab',
  createdAt: '2026-10-09T00:00:00Z', outcome: 'failed', delivered: false,
  failureClass: 'user_actionable', failureCode: 'request_too_large', failureStatus: 400,
  failureProvider: 'openrouter', failureSummary: "The request is larger than the model's context window",
};

function provider(supportsPromptCache?: boolean): Provider {
  return {
    id: 'or-profile', name: 'OpenRouter', tier: 'personal', kind: 'openrouter',
    model: MODEL, models: [MODEL],
    modelInfos: [{ id: MODEL, displayName: 'Kimi', contextWindow: 131_072, supportsPromptCache }],
  };
}

const USAGE: SessionUsage = {
  promptTokens: 0, completionTokens: 0, totalTokens: 0, costUsd: 0, costSource: 'unknown',
  messageCount: 1, pricingDataDate: '',
};

// What the next request would send for this session: 118,000 tokens of
// tool definitions. The failed first turn left no measured composition.
const NEXT_COSTS: ServerSchemaCost[] = [
  {
    server: 'outlook', state: 'running', running: true, toolCount: 94, tokenEst: 100_000,
    tier: 'full', source: 'project', pinned: false, sendableTokenEst: 100_000, tools: [],
  },
  {
    server: 'kenaz', state: 'running', running: true, toolCount: 15, tokenEst: 18_000,
    tier: 'full', source: 'default', pinned: false, sendableTokenEst: 18_000, tools: [],
  },
];

async function mountView(opts: { runs?: TurnRun[]; supportsPromptCache?: boolean } = {}) {
  const base = createFakeHarnessClient();
  const schemaCosts = vi.fn(async () => NEXT_COSTS);
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
                  id: SID, name: 'too large', createdAt: '2026-10-09T00:00:00Z',
                  updatedAt: '2026-10-09T00:00:00Z', systemPrompt: '', contextKind: 'system' as const,
                }),
                listMessagesActive: async () => ({ messages: MESSAGES, sweptCount: 0 }),
                turnRuns: async () => opts.runs ?? [TOO_LARGE],
                getUsage: async () => USAGE,
              },
              llm: { ...base.llm, listProviders: async () => [provider(opts.supportsPromptCache)] },
              tools: { ...base.tools, schemaCosts },
            });
          },
        },
      ],
    },
    attachTo: document.body,
  });
  await flushPromises();
  return { w, schemaCosts };
}

describe('SessionsView — Tools menu, request-too-large remedy, cache badge', () => {
  it('the request_too_large remedy names the next request’s N of M (no composition yet) and opens the Tools menu', async () => {
    setConnectionState('ready');
    const { w, schemaCosts } = await mountView();
    const banner = w.find('[data-testid="delivery-banner"]');
    expect(banner.text()).toContain(
      `Tool definitions use ${(118_000).toLocaleString()} of this model's ${(131_072).toLocaleString()} tokens — unload tools or pick a larger model`,
    );
    expect(w.find('[data-testid="undelivered-reason"]').text()).toContain('Tool definitions use');
    expect(w.find('[data-testid="tools-menu-panel"]').exists()).toBe(false);

    await w.find('[data-testid="delivery-banner-tools"]').trigger('click');
    await flushPromises();
    const panel = w.find('[data-testid="tools-menu-panel"]');
    expect(panel.exists()).toBe(true);
    expect(panel.element.contains(document.activeElement)).toBe(true);
    expect(schemaCosts).toHaveBeenCalledWith(SID, '');
    w.unmount();
  });

  it('the badge on the message opens the same menu', async () => {
    setConnectionState('ready');
    const { w } = await mountView();
    await w.find('[data-testid="undelivered-open-tools"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="tools-menu-panel"]').exists()).toBe(true);
    w.unmount();
  });

  it('other failures offer no Open tools', async () => {
    setConnectionState('ready');
    const { w } = await mountView({
      runs: [{ ...TOO_LARGE, failureCode: 'payment_required', failureSummary: 'Out of credits with OpenRouter' }],
    });
    expect(w.find('[data-testid="delivery-banner"]').exists()).toBe(true);
    expect(w.find('[data-testid="delivery-banner-tools"]').exists()).toBe(false);
    expect(w.find('[data-testid="undelivered-open-tools"]').exists()).toBe(false);
    w.unmount();
  });

  it('shows "caches prompts" on the MODEL row only when the model reports it', async () => {
    setConnectionState('ready');
    const on = await mountView({ runs: [], supportsPromptCache: true });
    expect(on.w.find('[data-testid="session-model-caches-prompts"]').exists()).toBe(true);
    on.w.unmount();

    const off = await mountView({ runs: [], supportsPromptCache: false });
    expect(off.w.find('[data-testid="session-model-caches-prompts"]').exists()).toBe(false);
    off.w.unmount();

    const unknown = await mountView({ runs: [] });
    expect(unknown.w.find('[data-testid="session-model-caches-prompts"]').exists()).toBe(false);
    unknown.w.unmount();
  });
});
