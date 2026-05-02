import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { Artifact, Message, Provider } from '@/lib/types';
import { setConnectionState } from '@/lib/useConnectionState';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  emit: (topic: string, payload: unknown) => void;
  handlers: Map<string, Set<(payload: unknown) => void>>;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    handlers,
    EventsOn: (topic, cb) => {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => s!.delete(cb);
    },
    emit: (topic, payload) => {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

function makeMessage(overrides: Partial<Message>): Message {
  return {
    id: 'm-1',
    sessionId: 's-1',
    role: 'user',
    content: 'hello',
    createdAt: '2026-04-25T00:00:00Z',
    ...overrides,
  };
}

async function mountWithRoute(
  idFragment: string,
  seed: Partial<HarnessClient>,
) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      { path: '/providers', component: defineComponent({ render: () => h('div', 'providers') }) },
    ],
  });
  // Legacy callers pass `'#<id>'`; the new router uses /sessions/<id>.
  // Strip a leading `#` when present so existing tests continue to work.
  const id = idFragment.startsWith('#') ? idFragment.slice(1) : idFragment;
  await router.push(id ? `/sessions/${id}` : '/sessions');
  await router.isReady();

  const w = mount(SessionsView, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
  await flushPromises();
  return { w, router };
}

describe('SessionsView (chat-ui)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
  });

  it('renders the welcome empty placeholder with no session id in the hash', async () => {
    const { w } = await mountWithRoute('', {});
    expect(w.text()).toContain('Start your first conversation');
    w.unmount();
  });

  it('shows "no provider configured" when llm.listProviders is empty', async () => {
    const { w } = await mountWithRoute('#sess-1', {
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      },
    });
    expect(w.text()).toContain('No provider configured');
    expect(w.text()).toContain('Configure providers');
    w.unmount();
  });

  it('renders the chat surface (CanvasHead + MessageList + ChatInput) when a session is loaded', async () => {
    const messages: Message[] = [
      makeMessage({ id: 'q', role: 'user', content: 'How are you?' }),
      makeMessage({ id: 'a', role: 'assistant', content: 'I am well.' }),
    ];
    const providers: Provider[] = [
      { id: 'anthropic-p-1', name: 'Anthropic', tier: 'cloud', kind: 'anthropic', model: 'claude' },
    ];
    const { w } = await mountWithRoute('#s-1', {
      sessions: {
        list: async () => [],
        get: async (id) => ({
          id,
          name: 'Onboarding',
          createdAt: '',
          updatedAt: '',
        }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => messages,
        listMessagesActive: async () => ({ messages, sweptCount: 0 }),
        listMessagesAll: async () => ({ messages, sweptCount: 0 }),
        appendMessage: async (id, role, content) =>
          makeMessage({ id: 'new', sessionId: id, role, content }),
        sendMessageWithBlocks: async () => makeMessage({ id: 'b' }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
        setSystemPrompt: async () => undefined,
        moveToProject: async () => undefined,
        getUsage: async () => ({ promptTokens: 0, completionTokens: 0, totalTokens: 0, costUsd: 0, costSource: 'unknown' as const, messageCount: 0, pricingDataDate: '' }),
        saveAsArtifact: async () => ({ id: '', sessionId: '', title: '', mimeType: 'text/plain', contentHash: '', byteSize: 0, source: 'user_pin' as const, sourceRef: { messageId: '', offset: 0, length: 0 }, scopeKind: 'session' as const, createdAt: '' }),
      },
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub-llm',
        stopStream: async () => undefined,
      },
    });
    // CanvasHead title reflects the loaded session name.
    expect(w.text()).toContain('Onboarding');
    expect(w.text()).toContain('How are you?');
    expect(w.text()).toContain('I am well.');
    // ChatInput textarea is present and enabled.
    const textarea = w.find('textarea');
    expect(textarea.exists()).toBe(true);
    expect(textarea.attributes('disabled')).toBeUndefined();
    w.unmount();
  });

  it('renders the Artifacts tab and filters its list to the active session', async () => {
    const messages: Message[] = [
      makeMessage({ id: 'q', role: 'user', content: 'How are you?' }),
    ];
    const providers: Provider[] = [
      { id: 'anthropic-p-1', name: 'Anthropic', tier: 'cloud', kind: 'anthropic', model: 'claude' },
    ];
    const sessionArtifact: Artifact = {
      id: 'art-1',
      sessionId: 's-1',
      title: 'tictactoe.html',
      mimeType: 'text/html',
      contentHash: 'sha256:a',
      byteSize: 256,
      source: 'code_block',
      sourceRef: { messageId: 'q' },
      scopeKind: 'session',
      createdAt: '2026-04-26T00:00:00Z',
    };
    const otherArtifact: Artifact = {
      ...sessionArtifact,
      id: 'art-2',
      sessionId: 'other-session',
      title: 'other-session.html',
    };
    const recordedFilters: Array<unknown> = [];
    const { w } = await mountWithRoute('#s-1', {
      sessions: {
        list: async () => [],
        get: async (id) => ({ id, name: 'Demo', createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => messages,
        listMessagesActive: async () => ({ messages, sweptCount: 0 }),
        listMessagesAll: async () => ({ messages, sweptCount: 0 }),
        appendMessage: async (id, role, content) =>
          makeMessage({ id: 'new', sessionId: id, role, content }),
        sendMessageWithBlocks: async () => makeMessage({ id: 'b' }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
        setSystemPrompt: async () => undefined,
        moveToProject: async () => undefined,
        getUsage: async () => ({ promptTokens: 0, completionTokens: 0, totalTokens: 0, costUsd: 0, costSource: 'unknown' as const, messageCount: 0, pricingDataDate: '' }),
        saveAsArtifact: async () => sessionArtifact,
      },
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        addProvider: async () => undefined,
        updateProvider: async () => undefined,
        removeProvider: async () => undefined,
        testProvider: async () => ({ success: true, latency_ms: 1, message: 'ok' }),
        listModels: async () => [],
        resolveConfirm: async () => undefined,
      },
      artifacts: {
        list: async (filter) => {
          recordedFilters.push(filter);
          if (filter?.sessionId) {
            return [sessionArtifact, otherArtifact].filter(
              (a) => a.sessionId === filter.sessionId,
            );
          }
          return [sessionArtifact, otherArtifact];
        },
        get: async () => ({
          artifact: sessionArtifact,
          bytes: btoa('html'),
        }),
        promote: async () => sessionArtifact,
        remove: async () => undefined,
      },
    });
    await flushPromises();
    // The list should have been fetched with sessionId === 's-1'.
    expect(
      recordedFilters.some(
        (f) => (f as { sessionId?: string }).sessionId === 's-1',
      ),
    ).toBe(true);
    // Switch to artifacts tab.
    expect(w.find('[data-testid="session-tab-artifacts"]').exists()).toBe(true);
    await w.find('[data-testid="session-tab-artifacts"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="session-artifacts-tab"]').exists()).toBe(true);
    // Only the session-scoped artifact (art-1) should appear.
    expect(w.find('[data-testid="session-artifacts-row-art-1"]').exists()).toBe(true);
    expect(w.find('[data-testid="session-artifacts-row-art-2"]').exists()).toBe(false);
    w.unmount();
  });

  it('does not render the no-provider banner when a provider is configured', async () => {
    const providers: Provider[] = [
      { id: 'anthropic-p-1', name: 'Anthropic', tier: 'cloud', kind: 'anthropic', model: 'claude' },
    ];
    const { w } = await mountWithRoute('#s-1', {
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      },
    });
    expect(w.text()).not.toContain('No provider configured');
    w.unmount();
  });

  // Helper to build a minimal session client stub for context-meter tests.
  function makeSessionsStub(messages: Message[]) {
    return {
      list: async () => [],
      get: async (id: string) => ({ id, name: 'Test', createdAt: '', updatedAt: '' }),
      create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
      rename: async () => undefined,
      delete: async () => undefined,
      reorder: async () => undefined,
      startStream: async () => 'sub',
      stopStream: async () => undefined,
      listMessages: async () => messages,
      listMessagesActive: async () => ({ messages, sweptCount: 0 }),
      listMessagesAll: async () => ({ messages, sweptCount: 0 }),
      appendMessage: async (id: string, role: string, content: string) =>
        makeMessage({ id: 'new', sessionId: id, role: role as Message['role'], content }),
      sendMessageWithBlocks: async () => makeMessage({ id: 'b' }),
      saveDraft: async () => undefined,
      loadDraft: async () => '',
      setSystemPrompt: async () => undefined,
      moveToProject: async () => undefined,
      getUsage: async () => ({ promptTokens: 0, completionTokens: 0, totalTokens: 0, costUsd: 0, costSource: 'unknown' as const, messageCount: 0, pricingDataDate: '' }),
      saveAsArtifact: async () => ({ id: '', sessionId: '', title: '', mimeType: 'text/plain', contentHash: '', byteSize: 0, source: 'user_pin' as const, sourceRef: { messageId: '', offset: 0, length: 0 }, scopeKind: 'session' as const, createdAt: '' }),
    };
  }

  it('context meter renders bar and correct denominator when contextWindow is known', async () => {
    const providers: Provider[] = [
      {
        id: 'anthropic-p-1',
        name: 'Anthropic',
        tier: 'cloud',
        kind: 'anthropic',
        model: 'claude-sonnet-4-5',
        models: ['claude-sonnet-4-5'],
        modelInfos: [{ id: 'claude-sonnet-4-5', displayName: 'Claude Sonnet 4.5', contextWindow: 200_000 }],
      },
    ];
    const messages: Message[] = [
      makeMessage({ id: 'q', role: 'user', content: 'Hello' }),
    ];
    const { w } = await mountWithRoute('#ctx-test', {
      sessions: makeSessionsStub(messages),
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      },
    });
    const meter = w.find('[data-testid="session-context-meter"]');
    expect(meter.exists()).toBe(true);
    // Title should use the backend-supplied 200_000 cap.
    const title = meter.attributes('title') ?? '';
    expect(title).toContain('200,000');
    // The "unknown" badge must NOT appear when context window is known.
    expect(w.find('[data-testid="session-context-meter-unknown"]').exists()).toBe(false);
    w.unmount();
  });

  it('context meter renders bar and correct denominator for DeepSeek V4 Pro (1_048_576)', async () => {
    const providers: Provider[] = [
      {
        id: 'openrouter-p-1',
        name: 'OpenRouter',
        tier: 'cloud',
        kind: 'openrouter',
        model: 'deepseek/deepseek-chat-v3-0324',
        models: ['deepseek/deepseek-chat-v3-0324'],
        modelInfos: [{ id: 'deepseek/deepseek-chat-v3-0324', displayName: 'DeepSeek V3 0324', contextWindow: 1_048_576 }],
      },
    ];
    const messages: Message[] = [
      makeMessage({ id: 'q', role: 'user', content: 'Hello' }),
    ];
    const { w } = await mountWithRoute('#ctx-deepseek', {
      sessions: makeSessionsStub(messages),
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      },
    });
    const meter = w.find('[data-testid="session-context-meter"]');
    expect(meter.exists()).toBe(true);
    const title = meter.attributes('title') ?? '';
    expect(title).toContain('1,048,576');
    // The "unknown" badge must NOT appear.
    expect(w.find('[data-testid="session-context-meter-unknown"]').exists()).toBe(false);
    w.unmount();
  });

  it('context meter renders explicit unknown state when contextWindow is 0', async () => {
    const providers: Provider[] = [
      {
        id: 'openai-p-1',
        name: 'OpenAI',
        tier: 'cloud',
        kind: 'openai',
        model: 'some-unknown-future-model',
        models: ['some-unknown-future-model'],
        modelInfos: [{ id: 'some-unknown-future-model', displayName: 'Unknown model', contextWindow: 0 }],
      },
    ];
    const messages: Message[] = [
      makeMessage({ id: 'q', role: 'user', content: 'Hello' }),
    ];
    const { w } = await mountWithRoute('#ctx-unknown-zero', {
      sessions: makeSessionsStub(messages),
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      },
    });
    const meter = w.find('[data-testid="session-context-meter"]');
    expect(meter.exists()).toBe(true);
    // Title should indicate unknown state, not 200,000 fallback.
    const title = meter.attributes('title') ?? '';
    expect(title).toContain('unknown');
    expect(title).not.toContain('200,000');
    // The explicit "unknown" badge must be visible.
    expect(w.find('[data-testid="session-context-meter-unknown"]').exists()).toBe(true);
    w.unmount();
  });

  it('context meter renders explicit unknown state when modelInfos is absent', async () => {
    const providers: Provider[] = [
      {
        id: 'openai-p-1',
        name: 'OpenAI',
        tier: 'cloud',
        kind: 'openai',
        model: 'some-unknown-future-model',
        models: ['some-unknown-future-model'],
        // no modelInfos — simulates a provider that hasn't been catalogued yet
      },
    ];
    const messages: Message[] = [
      makeMessage({ id: 'q', role: 'user', content: 'Hello' }),
    ];
    const { w } = await mountWithRoute('#ctx-unknown-absent', {
      sessions: makeSessionsStub(messages),
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      },
    });
    const meter = w.find('[data-testid="session-context-meter"]');
    expect(meter.exists()).toBe(true);
    // Title should indicate unknown, not a misleading 200,000 fallback.
    const title = meter.attributes('title') ?? '';
    expect(title).toContain('unknown');
    expect(title).not.toContain('200,000');
    // The explicit "unknown" badge must be visible.
    expect(w.find('[data-testid="session-context-meter-unknown"]').exists()).toBe(true);
    w.unmount();
  });
});
