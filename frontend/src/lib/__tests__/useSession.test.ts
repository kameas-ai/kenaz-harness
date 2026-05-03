import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { defineComponent, h, ref, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { useSession } from '@/lib/useSession';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import type { Message } from '@/lib/types';

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
        get: async (id) => ({ id, name: 'My Session', createdAt: '', updatedAt: '' }),
        create: async () => ({ id: 'x', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => initial,
        appendMessage: async (id, role, content) =>
          makeMessage({ id: 'new', sessionId: id, role, content }),
        saveDraft: async () => undefined,
        loadDraft: async () => 'pending draft',
      },
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
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) => {
          seenAppend.push(`${role}:${content}`);
          return makeMessage({ id: 'u-1', sessionId: id, role, content });
        },
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      },
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-llm-1',
        stopStream: async () => undefined,
      },
    });
    await vi.runAllTimersAsync();
    await session.send('Hello!', 'profile-anthropic');
    await nextTick();
    expect(seenAppend).toEqual(['user:Hello!']);
    expect(session.streamSubscriptionId.value).toBe('sub-llm-1');
    expect(session.messages.value.find((m) => m.content === 'Hello!')).toBeTruthy();
    w.unmount();
  });

  it('splices stream chunks into currentlyStreaming', async () => {
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) =>
          makeMessage({ id: 'u-1', sessionId: id, role, content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      },
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-x',
        stopStream: async () => undefined,
      },
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
    expect(session.currentlyStreaming.value?.content).toBe('Hello, world');
    expect(session.currentlyStreaming.value?.streaming).toBe(true);
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
    expect(session.currentlyStreaming.value).toBeNull();
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
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) =>
          makeMessage({ id: 'u-1', sessionId: id, role, content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      },
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
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) =>
          makeMessage({ id: 'u-1', sessionId: id, role, content }),
        saveDraft: async () => {
          saveCount += 1;
        },
        loadDraft: async () => '',
      },
    });
    await vi.runAllTimersAsync();
    session.draft.value = 'a';
    session.draft.value = 'ab';
    session.draft.value = 'abc';
    await vi.advanceTimersByTimeAsync(500);
    expect(saveCount).toBe(1);
    w.unmount();
  });

  it('preserves partial assistant content when the stream errors mid-turn', async () => {
    // long-turn-resilience WP00 regression: a stream that emits text
    // deltas and then errors must NOT throw away the partial content.
    // The committed message lands in messages.value with streamingError
    // stamped so the bubble can render the "Connection lost" hint.
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) =>
          makeMessage({ id: 'u-1', sessionId: id, role, content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      },
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-err',
        stopStream: async () => undefined,
      },
    });
    await vi.runAllTimersAsync();
    await session.send('q', 'profile');
    await nextTick();
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-err',
      session_id: 's-1',
      chunk: { kind: 'text', text: 'partial reply ' },
    });
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-err',
      session_id: 's-1',
      chunk: { kind: 'text', text: 'before death' },
    });
    await nextTick();
    expect(session.currentlyStreaming.value?.content).toBe(
      'partial reply before death',
    );
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-err',
      session_id: 's-1',
      chunk: { kind: 'error', err: 'connection reset' },
    });
    await nextTick();
    // The partial content must already be in messages.value, with
    // streamingError set, BEFORE stream-closed arrives.
    const committedAfterError = session.messages.value.find(
      (m) => m.role === 'assistant' && m.content.startsWith('partial reply'),
    );
    expect(committedAfterError).toBeTruthy();
    expect(committedAfterError?.streamingError).toBe('connection reset');
    expect(committedAfterError?.streaming).toBe(false);
    expect(session.error.value).toBe('connection reset');
    // stream-closed arrives second; idempotency check — no duplicate
    // append even though currentlyStreaming was non-null at error time.
    rt.emit('llm:stream-closed', {
      sub_id: 'sub-err',
      session_id: 's-1',
      reason: 'backend-error',
      message: 'connection reset',
    });
    await nextTick();
    const matches = session.messages.value.filter(
      (m) => m.role === 'assistant' && m.content.startsWith('partial reply'),
    );
    expect(matches).toHaveLength(1);
    expect(session.currentlyStreaming.value).toBeNull();
    expect(session.streamSubscriptionId.value).toBeNull();
    w.unmount();
  });

  it('commits partial content on stream-closed when reason !== completed', async () => {
    // long-turn-resilience WP00: the close handler is now responsible
    // for committing partial content even when the stream never sent a
    // discrete `error` chunk (e.g. a clean TCP RST mid-stream that the
    // backend surfaces only via stream-closed).
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) =>
          makeMessage({ id: 'u-1', sessionId: id, role, content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      },
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-cut',
        stopStream: async () => undefined,
      },
    });
    await vi.runAllTimersAsync();
    await session.send('q', 'profile');
    await nextTick();
    rt.emit('llm:stream-chunk', {
      sub_id: 'sub-cut',
      session_id: 's-1',
      chunk: { kind: 'text', text: 'half a reply' },
    });
    await nextTick();
    rt.emit('llm:stream-closed', {
      sub_id: 'sub-cut',
      session_id: 's-1',
      reason: 'ctx-cancelled',
    });
    await nextTick();
    const last = session.messages.value[session.messages.value.length - 1];
    expect(last.role).toBe('assistant');
    expect(last.content).toBe('half a reply');
    expect(last.streamingError).toBe('ctx-cancelled');
    expect(last.streaming).toBe(false);
    expect(session.currentlyStreaming.value).toBeNull();
    w.unmount();
  });

  it('flags streamingTimedOut after 30s with no chunks', async () => {
    const { w, session } = mountWithSession({
      sessions: {
        list: async () => [],
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) =>
          makeMessage({ id: 'u-1', sessionId: id, role, content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      },
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub-no-chunks',
        stopStream: async () => undefined,
      },
    });
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();
    expect(session.streamingTimedOut.value).toBe(false);
    await vi.advanceTimersByTimeAsync(31_000);
    expect(session.streamingTimedOut.value).toBe(true);
    w.unmount();
  });
});
