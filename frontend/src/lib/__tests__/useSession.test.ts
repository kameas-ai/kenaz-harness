import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { defineComponent, h, ref, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { useSession } from '@/lib/useSession';
import { useSessions } from '@/lib/useHarnessAPI';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import type { Message, Session } from '@/lib/types';

// Controllable served-mode flag. Defaults to false (desktop) so the existing
// suite is unaffected; the served-stream test flips it to true. useSession
// reads isServedMode() once at setup time, so toggling before mount suffices.
let servedModeFlag = false;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return {
    ...actual,
    isServedMode: () => servedModeFlag,
  };
});

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
      return () => {
        s!.delete(cb);
      };
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
    role: 'assistant',
    content: '',
    createdAt: '2026-04-25T00:00:00Z',
    ...overrides,
  };
}

describe('useSession (chat-ui)', () => {
  let rt: FakeRuntime;

  beforeEach(() => {
    rt = installFakeRuntime();
    setConnectionState('ready');
    vi.useFakeTimers();
  });

  afterEach(() => {
    uninstallRuntime();
    vi.useRealTimers();
    servedModeFlag = false;
  });

  function mountWithSession(
    seed: Partial<HarnessClient>,
    sessionIdRef = ref('s-1'),
  ) {
    let session: ReturnType<typeof useSession> | null = null;
    const Comp = defineComponent({
      setup() {
        session = useSession(sessionIdRef);
        return () => h('div');
      },
    });
    const w = mount(Comp, {
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
    return { w, get session() {
      if (!session) throw new Error('no session');
      return session;
    }, sessionIdRef };
  }

  it('loads session, messages, and draft on mount', async () => {
    const initial: Message[] = [
      makeMessage({ id: 'a', role: 'user', content: 'hi' }),
    ];
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: 'My Session', createdAt: '', updatedAt: '' }),
        create: async () => ({ id: 'x', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => initial,
        appendMessage: async (id: string, role: string, content: string) =>
          makeMessage({ id: 'new', sessionId: id, role: role as Message['role'], content }),
        saveDraft: async () => undefined,
        loadDraft: async () => 'pending draft',
      } as any,
    });
    await vi.runAllTimersAsync();
    await nextTick();
    expect(session.session.value?.name).toBe('My Session');
    expect(session.messages.value).toHaveLength(1);
    expect(session.draft.value).toBe('pending draft');
    w.unmount();
  });

  it('append + send wires the LLM stream and surfaces subscriptionId', async () => {
    const seenAppend: string[] = [];
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) => {
          seenAppend.push(`${role}:${content}`);
          return makeMessage({ id: 'u-1', sessionId: id, role: role as Message['role'], content });
        },
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      } as any,
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-llm-1',
        stopStream: async () => undefined,
      } as any,
    });
    await vi.runAllTimersAsync();
    await session.send('Hello!', 'profile-anthropic');
    await nextTick();
    expect(seenAppend).toEqual(['user:Hello!']);
    expect(session.streamSubscriptionId.value).toBe('sub-llm-1');
    expect(session.messages.value.find((m) => m.content === 'Hello!')).toBeTruthy();
    w.unmount();
  });

  it('splices stream chunks into the in-flight move buffer', async () => {
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) =>
          makeMessage({ id: 'u-1', sessionId: id, role: role as Message['role'], content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      } as any,
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-x',
        stopStream: async () => undefined,
      } as any,
    });
    await vi.runAllTimersAsync();
    await session.send('q', 'profile');
    await nextTick();
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-x',
      session_id: 's-1',
      chunk: { kind: 'text', text: 'Hello' },
    });
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-x',
      session_id: 's-1',
      chunk: { kind: 'text', text: ', world' },
    });
    await nextTick();
    expect(session.streamingMoves.value).toHaveLength(1);
    expect(session.streamingMoves.value[0].content).toBe('Hello, world');
    expect(session.streamingMoves.value[0].streaming).toBe(true);
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-x',
      session_id: 's-1',
      chunk: { kind: 'text', text: '!' },
    });
    rt.emit('llm:stream-closed', {
      sub_id: 'sub-x',
      session_id: 's-1',
      reason: 'completed',
      finish_reason: 'end_turn',
    });
    await nextTick();
    expect(session.streamingMoves.value).toHaveLength(0);
    expect(session.streamSubscriptionId.value).toBeNull();
    const last = session.messages.value[session.messages.value.length - 1];
    expect(last.content).toBe('Hello, world!');
    expect(last.streaming).toBe(false);
    w.unmount();
  });

  it('appends messages from sessions:event/message_appended', async () => {
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) =>
          makeMessage({ id: 'u-1', sessionId: id, role: role as Message['role'], content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      } as any,
    });
    await vi.runAllTimersAsync();
    rt.emit('sessions:event', {
      kind: 'message_appended',
      sessionId: 's-1',
      message: makeMessage({ id: 'inbound', role: 'system', content: 'context loaded' }),
    });
    await nextTick();
    expect(session.messages.value.find((m) => m.id === 'inbound')).toBeTruthy();
    w.unmount();
  });

  it('debounces draft save', async () => {
    let saveCount = 0;
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) =>
          makeMessage({ id: 'u-1', sessionId: id, role: role as Message['role'], content }),
        saveDraft: async () => {
          saveCount += 1;
        },
        loadDraft: async () => '',
      } as any,
    });
    await vi.runAllTimersAsync();
    session.draft.value = 'a';
    session.draft.value = 'ab';
    session.draft.value = 'abc';
    await vi.advanceTimersByTimeAsync(500);
    expect(saveCount).toBe(1);
    w.unmount();
  });

  it('persists partial assistant content on error event + stream-closed (resilience WP00)', async () => {
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) =>
          makeMessage({ id: 'u-1', sessionId: id, role: role as Message['role'], content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      } as any,
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-err',
        stopStream: async () => undefined,
      } as any,
    });
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-err',
      session_id: 's-1',
      chunk: { kind: 'text', text: 'partial ' },
    });
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-err',
      session_id: 's-1',
      chunk: { kind: 'text', text: 'reply' },
    });
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-err',
      session_id: 's-1',
      chunk: { kind: 'error', err: 'transient provider error' },
    });
    rt.emit('llm:stream-closed', {
      sub_id: 'sub-err',
      session_id: 's-1',
      reason: 'error',
    });
    await nextTick();
    const assistantRows = session.messages.value.filter((m) => m.role === 'assistant');
    expect(assistantRows).toHaveLength(1);
    expect(assistantRows[0].content).toBe('partial reply');
    expect(assistantRows[0].streaming).toBe(false);
    expect(assistantRows[0].streamingError).toBe('transient provider error');
    expect(session.streamingMoves.value).toHaveLength(0);
    expect(session.error.value).toBe('transient provider error');
    w.unmount();
  });

  it('does not stamp streamingError when stream closes with reason=completed', async () => {
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) =>
          makeMessage({ id: 'u-1', sessionId: id, role: role as Message['role'], content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      } as any,
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-ok',
        stopStream: async () => undefined,
      } as any,
    });
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-ok',
      session_id: 's-1',
      chunk: { kind: 'text', text: 'all good' },
    });
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-ok',
      session_id: 's-1',
      chunk: { kind: 'finish', finish: 'end_turn' },
    });
    rt.emit('llm:stream-closed', {
      sub_id: 'sub-ok',
      session_id: 's-1',
      reason: 'completed',
      finish_reason: 'end_turn',
    });
    await nextTick();
    const assistantRows = session.messages.value.filter((m) => m.role === 'assistant');
    expect(assistantRows).toHaveLength(1);
    expect(assistantRows[0].content).toBe('all good');
    expect(assistantRows[0].streamingError).toBeUndefined();
    w.unmount();
  });

  it('does not append a spurious message when stream-closed cancelled with no content', async () => {
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) =>
          makeMessage({ id: 'u-1', sessionId: id, role: role as Message['role'], content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      } as any,
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-cancel',
        stopStream: async () => undefined,
      } as any,
    });
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();
    rt.emit('llm:stream-closed', {
      sub_id: 'sub-cancel',
      session_id: 's-1',
      reason: 'cancelled',
    });
    await nextTick();
    const assistantRows = session.messages.value.filter((m) => m.role === 'assistant');
    expect(assistantRows).toHaveLength(0);
    expect(session.streamingMoves.value).toHaveLength(0);
    w.unmount();
  });

  it('flags streamingTimedOut after 30s with no chunks', async () => {
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) =>
          makeMessage({ id: 'u-1', sessionId: id, role: role as Message['role'], content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      } as any,
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-no-chunks',
        stopStream: async () => undefined,
      } as any,
    });
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();
    expect(session.streamingTimedOut.value).toBe(false);
    await vi.advanceTimersByTimeAsync(31_000);
    expect(session.streamingTimedOut.value).toBe(true);
    w.unmount();
  });

  // ── served-mode Sessions_Stream wiring (FR-007) ──────────────────────────
  // In served mode useSession must open the Sessions_Stream for the active
  // session (so elicit/session events reach the browser) and re-open it on
  // reconnect. The isServedMode() mock is flipped true just for this test.
  it('opens the served Sessions_Stream for the active session and re-opens on reconnect', async () => {
    servedModeFlag = true;
    const startCalls: string[] = [];
    const stopCalls: string[] = [];
    const { w } = mountWithSession(
      {
        sessions: {
          list: async () => [],
          get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
          create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
          rename: async () => undefined,
          delete: async () => undefined,
          reorder: async () => undefined,
          startStream: async (id: string) => {
            startCalls.push(id);
            return `sub-${startCalls.length}`;
          },
          stopStream: async (sub: string) => {
            stopCalls.push(sub);
          },
          listMessages: async () => [],
          appendMessage: async (id: string, role: string, content: string) =>
            makeMessage({ id: 'x', sessionId: id, role: role as Message['role'], content }),
          saveDraft: async () => undefined,
          loadDraft: async () => '',
        } as any,
      },
      ref('sess-live'),
    );

    await vi.runAllTimersAsync();
    await nextTick();

    // Mount opened the stream for the active session.
    expect(startCalls).toContain('sess-live');
    const afterMount = startCalls.length;

    // Simulate a connection drop then recovery; the stream must be re-opened.
    setConnectionState('lost');
    await nextTick();
    setConnectionState('ready');
    await vi.runAllTimersAsync();
    await nextTick();

    expect(startCalls.length).toBeGreaterThan(afterMount);
    expect(startCalls[startCalls.length - 1]).toBe('sess-live');

    w.unmount();
    // Teardown closed the last subscription.
    expect(stopCalls.length).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// useSessions — broker-driven real-time refresh (v0.5.3 fix)
// ---------------------------------------------------------------------------

function makeSession(overrides: Partial<Session>): Session {
  return {
    id: 's-1',
    name: 'My Session',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    systemPrompt: '',
    contextKind: 'system',
    autoTitled: false,
    ...overrides,
  };
}

/** Minimal stub that satisfies all SessionsClient members. Override individual
 * methods by spreading in the overrides param. Used by the useSessions tests
 * to avoid repeating all 15+ members in every test case. */
function makeSessionsStub(
  overrides: Partial<HarnessClient['sessions']>,
): HarnessClient['sessions'] {
  const stub: HarnessClient['sessions'] = {
    list: async () => [],
    get: async (id: string) => makeSession({ id }),
    create: async () => makeSession({}),
    rename: async () => undefined,
    delete: async () => undefined,
    reorder: async () => undefined,
    startStream: async () => '',
    stopStream: async () => undefined,
    listMessages: async () => [],
    listMessagesActive: async () => ({ messages: [], sweptCount: 0 }),
    listMessagesAll: async () => ({ messages: [], sweptCount: 0 }),
    appendMessage: async (id: string, role: string, content: string) => makeMessage({ id: 'x', sessionId: id, role: role as Message['role'], content }),
    sendMessageWithBlocks: async () => makeMessage({}),
    saveDraft: async () => undefined,
    loadDraft: async () => '',
    setSystemPrompt: async () => undefined,
    moveToProject: async () => undefined,
    resumeMessage: async () => ({ subscriptionId: '', originalMessageId: '' }),
    getUsage: async () => ({ promptTokens: 0, completionTokens: 0, totalTokens: 0, costUsd: 0, costSource: 'unknown', messageCount: 0, pricingDataDate: '' }),
    saveAsArtifact: async () => ({ id: '', sessionId: '', title: '', mimeType: '', byteSize: 0, source: 'user_pin' as const, sourceRef: { messageId: '' }, scopeKind: 'session' as const, createdAt: '', contentHash: '' }),
    suggestTitle: async () => '',
    clearTitle: async () => undefined,
    getAutonomy: async () => ({ level: null, overrides: {} }),
    setAutonomy: async () => undefined,
    resolveAutonomy: async () => ({
      resolved: { maxIterations: 0, askOnAmbiguity: '', autoApproveFamilies: [], tokenCeilingPerTurn: 0, recapStyle: '', continueOnError: '', destructiveActionPosture: '', sourceTrace: {}, tier: '' },
      global: { level: null, overrides: {} },
      project: { level: null, overrides: {} },
      session: { level: null, overrides: {} },
    }),
    export: async () => ({ path: '', byteCount: 0 }),
  };
  return { ...stub, ...overrides };
}

describe('useSessions — broker-driven refresh', () => {
  let rt: FakeRuntime;

  beforeEach(() => {
    rt = installFakeRuntime();
    vi.useFakeTimers();
  });

  afterEach(() => {
    uninstallRuntime();
    vi.useRealTimers();
    servedModeFlag = false;
  });

  function mountWithSessions(seed: Partial<HarnessClient>) {
    let sessions: ReturnType<typeof useSessions> | null = null;
    const Comp = defineComponent({
      setup() {
        sessions = useSessions();
        return () => h('div');
      },
    });
    const w = mount(Comp, {
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
    return {
      w,
      get sessions() {
        if (!sessions) throw new Error('no sessions');
        return sessions;
      },
    };
  }

  it('external-rename event arrives → debounced refresh() runs → list updates', async () => {
    const initial = [makeSession({ id: 's-1', name: 'Old Name' })];
    const renamed = [makeSession({ id: 's-1', name: 'New Name' })];
    let listCallCount = 0;
    const { w, sessions } = mountWithSessions({
      sessions: makeSessionsStub({
        list: async () => {
          listCallCount += 1;
          return listCallCount === 1 ? initial : renamed;
        },
      }),
    });

    // Trigger the initial load via the LeftRail pattern.
    await sessions.refresh();
    expect(sessions.list.value[0].name).toBe('Old Name');
    expect(listCallCount).toBe(1);

    // Simulate the backend emitting session.list_changed (e.g. from auto-title).
    rt.emit('session.list_changed', { reason: 'renamed', sessionId: 's-1', timestamp: Date.now() });

    // Before debounce fires, the list should not have changed yet.
    expect(sessions.list.value[0].name).toBe('Old Name');

    // Advance past the 150 ms debounce.
    await vi.advanceTimersByTimeAsync(160);
    await nextTick();

    expect(sessions.list.value[0].name).toBe('New Name');
    expect(listCallCount).toBe(2);
    w.unmount();
  });

  it('burst of events collapses to a single refresh after the debounce window', async () => {
    let listCallCount = 0;
    const { w, sessions } = mountWithSessions({
      sessions: makeSessionsStub({
        list: async () => {
          listCallCount += 1;
          return [makeSession({ id: `s-${listCallCount}`, name: `Call ${listCallCount}` })];
        },
      }),
    });

    // Initial load.
    await sessions.refresh();
    const afterInitial = listCallCount;

    // Emit three events in rapid succession — all within the debounce window.
    rt.emit('session.list_changed', { reason: 'created', timestamp: Date.now() });
    rt.emit('session.list_changed', { reason: 'title_set', timestamp: Date.now() });
    rt.emit('session.list_changed', { reason: 'renamed', timestamp: Date.now() });

    // Advance past the debounce window.
    await vi.advanceTimersByTimeAsync(200);
    await nextTick();

    // Only one additional refresh should have fired for the three events.
    expect(listCallCount).toBe(afterInitial + 1);
    w.unmount();
  });

  it('unsubscribes and cancels pending debounce on unmount', async () => {
    let listCallCount = 0;
    const { w, sessions } = mountWithSessions({
      sessions: makeSessionsStub({
        list: async () => {
          listCallCount += 1;
          return [];
        },
      }),
    });

    await sessions.refresh();
    const afterInitial = listCallCount;

    // Emit event, then unmount before the debounce fires.
    rt.emit('session.list_changed', { reason: 'deleted', timestamp: Date.now() });
    w.unmount();

    // Advance past the debounce window — no additional refresh should fire.
    await vi.advanceTimersByTimeAsync(300);
    await nextTick();

    expect(listCallCount).toBe(afterInitial);
  });
});
