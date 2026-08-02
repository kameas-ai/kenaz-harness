/**
 * servedChat.test.ts — the served-mode chat surface.
 *
 * A served harness is the default app inside every Kenaz workbench. Until
 * this wiring landed it could list conversations but not start one: the
 * create / append / start-stream / stop-stream calls all rejected with
 * ServedUnsupportedError, and the per-token events never left the
 * WebSocket frame handler. These tests pin each half of that.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { onServedEvent } from '@/lib/useServedEvents';
import { isServedUnsupportedError } from '@/lib/errors';

type FetchStub = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

interface RPCCall {
  method: string;
  params: Record<string, unknown>;
}

/**
 * withRPCRecorder stubs fetch, records every /rpc call, and returns a
 * served client plus the recording.
 */
async function withRPCRecorder(result: unknown = null) {
  const calls: RPCCall[] = [];
  globalThis.fetch = ((async (_input: RequestInfo | URL, init?: RequestInit) => {
    const body = JSON.parse((init?.body as string) ?? '{}') as RPCCall;
    calls.push({ method: body.method, params: body.params ?? {} });
    return new Response(JSON.stringify({ result }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }) satisfies FetchStub) as typeof globalThis.fetch;

  const { createServedHarnessClient } = await import('@/lib/harnessClient');
  const client = createServedHarnessClient({
    baseURL: 'http://127.0.0.1:7880',
    token: '',
  });
  return { client, calls };
}

// ── a fake WebSocket so we can drive server frames ────────────────────────

interface FakeWS {
  url: string;
  protocols: string[];
  sent: string[];
  listeners: Map<string, ((ev: unknown) => void)[]>;
  readyState: number;
  addEventListener(type: string, fn: (ev: unknown) => void): void;
  send(data: string): void;
  close(): void;
  /** Test helper: deliver a server frame to the client. */
  serverFrame(event: string, data: unknown): void;
}

let lastWS: FakeWS | null = null;

function installFakeWebSocket(): void {
  class WS implements FakeWS {
    static readonly OPEN = 1;
    static readonly CONNECTING = 0;
    url: string;
    protocols: string[];
    sent: string[] = [];
    listeners = new Map<string, ((ev: unknown) => void)[]>();
    readyState = 1;

    constructor(url: string, protocols?: string | string[]) {
      this.url = url;
      this.protocols = Array.isArray(protocols)
        ? protocols
        : protocols
          ? [protocols]
          : [];
      lastWS = this;
      // Fire 'open' asynchronously, like a real socket.
      queueMicrotask(() => this.emit('open', {}));
    }

    addEventListener(type: string, fn: (ev: unknown) => void): void {
      const arr = this.listeners.get(type) ?? [];
      arr.push(fn);
      this.listeners.set(type, arr);
    }

    private emit(type: string, ev: unknown): void {
      for (const fn of this.listeners.get(type) ?? []) fn(ev);
    }

    send(data: string): void {
      this.sent.push(data);
    }

    close(): void {
      this.readyState = 3;
    }

    serverFrame(event: string, data: unknown): void {
      this.emit('message', { data: JSON.stringify({ event, data }) });
    }
  }
  (globalThis as unknown as { WebSocket: unknown }).WebSocket = WS;
}

// ── RPC surface ───────────────────────────────────────────────────────────

describe('served client: conversation lifecycle', () => {
  let origFetch: typeof globalThis.fetch;

  beforeEach(() => {
    origFetch = globalThis.fetch;
  });
  afterEach(() => {
    globalThis.fetch = origFetch;
  });

  it('routes every call the send path makes to its served RPC', async () => {
    const { client, calls } = await withRPCRecorder({});

    await client.sessions.create('my chat');
    await client.sessions.appendMessage('s1', 'user', 'hello');
    await client.sessions.listMessagesActive('s1');
    await client.llm.startStream('profile-1', 's1', 'claude-x');
    await client.llm.stopStream('sub-1');

    expect(calls.map((c) => c.method)).toEqual([
      'Sessions_Create',
      'Sessions_AppendMessage',
      'Sessions_ListMessagesActive',
      'LLM_StartStream',
      'LLM_StopStream',
    ]);
    expect(calls[0]?.params).toEqual({ name: 'my chat' });
    expect(calls[1]?.params).toEqual({ id: 's1', role: 'user', content: 'hello' });
    expect(calls[3]?.params).toEqual({
      profileId: 'profile-1',
      sessionId: 's1',
      modelOverride: 'claude-x',
    });
    expect(calls[4]?.params).toEqual({ subId: 'sub-1' });
  });

  it('wires the whole new-session dialog, not just create()', async () => {
    // The dialog files the session under a project and seeds a system
    // prompt in the same submit. Porting create() alone would leave it
    // throwing halfway, with the session already made.
    const { client, calls } = await withRPCRecorder({});

    await client.projects.list();
    await client.sessions.create('x');
    await client.sessions.moveToProject('s1', 'p1');
    await client.sessions.setSystemPrompt('s1', 'be terse', 'system');

    expect(calls.map((c) => c.method)).toEqual([
      'Projects_List',
      'Sessions_Create',
      'Sessions_MoveToProject',
      'Sessions_SetSystemPrompt',
    ]);
  });

  it('lets the user answer a permission prompt', async () => {
    // A tool call BLOCKS on this answer. Forwarding the prompt event
    // without this write would render a modal whose buttons do nothing.
    const { client, calls } = await withRPCRecorder({});
    await client.permissions.resolve('req-7', 'allow_once');
    expect(calls[0]).toEqual({
      method: 'Permissions_Resolve',
      params: { requestId: 'req-7', decision: 'allow_once' },
    });
  });

  it('keeps provider writes unported', async () => {
    // Host-delivered providers are immutable from inside the VM; an edit
    // form that cannot succeed is worse than none.
    const { client } = await withRPCRecorder({});
    const err = await client.llm.removeProvider('p').catch((e: unknown) => e);
    expect(isServedUnsupportedError(err)).toBe(true);
  });

  it('names the missing method instead of telling a VM user to run a desktop app', async () => {
    const { client } = await withRPCRecorder({});
    const err = await client.settings.get().catch((e: unknown) => e);
    expect(isServedUnsupportedError(err)).toBe(true);
    const copy = (err as { friendly(): string }).friendly();
    expect(copy).toContain('settings.get');
    expect(copy.toLowerCase()).not.toContain('desktop app');
  });
});

describe('served transport: error taxonomy', () => {
  let origFetch: typeof globalThis.fetch;

  beforeEach(() => {
    origFetch = globalThis.fetch;
  });
  afterEach(() => {
    globalThis.fetch = origFetch;
  });

  it('preserves a structured RPCError envelope so the desktop hint still renders', async () => {
    const envelope = JSON.stringify({
      code: 'auth',
      message: 'provider rejected the credential',
      hint: 'Rotate the key in Kenaz, then reopen this workbench.',
      retryable: false,
    });
    globalThis.fetch = (async () =>
      new Response(JSON.stringify({ error: envelope }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })) as typeof globalThis.fetch;

    const { createServedHarnessClient } = await import('@/lib/harnessClient');
    const client = createServedHarnessClient({ baseURL: '', token: '' });
    const err = await client.llm.startStream('p', 's').catch((e: unknown) => e);

    const { friendly } = await import('@/lib/errors');
    expect(friendly(err)).toBe(
      'Rotate the key in Kenaz, then reopen this workbench.',
    );
  });

  it('still labels plain errors with the method that failed', async () => {
    globalThis.fetch = (async () =>
      new Response(JSON.stringify({ error: 'session not found' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })) as typeof globalThis.fetch;

    const { createServedHarnessClient } = await import('@/lib/harnessClient');
    const client = createServedHarnessClient({ baseURL: '', token: '' });
    const err = await client.sessions.get('nope').catch((e: unknown) => e);
    expect((err as Error).message).toContain('Sessions_Get');
    expect((err as Error).message).toContain('session not found');
  });
});

// ── WebSocket fan-out ─────────────────────────────────────────────────────

describe('served client: stream frames reach the event bus', () => {
  let origFetch: typeof globalThis.fetch;
  let origWS: unknown;

  beforeEach(() => {
    origFetch = globalThis.fetch;
    origWS = (globalThis as unknown as { WebSocket: unknown }).WebSocket;
    installFakeWebSocket();
    lastWS = null;
  });
  afterEach(() => {
    globalThis.fetch = origFetch;
    (globalThis as unknown as { WebSocket: unknown }).WebSocket = origWS;
  });

  async function openStream() {
    const { client } = await withRPCRecorder({});
    const subId = await client.sessions.startStream('s1');
    await Promise.resolve(); // let the 'open' microtask run
    if (!lastWS) throw new Error('no WebSocket was opened');
    return { client, subId, ws: lastWS };
  }

  it('subscribes to Sessions_Stream on open', async () => {
    const { ws } = await openStream();
    expect(JSON.parse(ws.sent[0] ?? '{}')).toEqual({
      method: 'Sessions_Stream',
      params: { id: 's1' },
    });
  });

  it.each([
    'llm:stream-chunk',
    'llm:stream-closed',
    'llm:fallback-attempted',
    'session.usage.updated',
    'cost.threshold.crossed',
    'bash:permission-pending',
    'cred:permission-pending',
    'fs:permission-pending',
    'tool:permission-pending',
    'elicit:pending',
    'served:stream-truncated',
  ])('re-publishes %s onto the served event bus', async (topic) => {
    const { ws } = await openStream();
    const seen: unknown[] = [];
    const off = onServedEvent(topic, (p) => seen.push(p));

    ws.serverFrame(topic, { marker: topic });
    off();

    expect(seen).toEqual([{ marker: topic }]);
  });

  it('assembles a streamed reply in order', async () => {
    const { ws } = await openStream();
    let text = '';
    const off = onServedEvent('llm:stream-chunk', (p) => {
      text += (p as { chunk: { text: string } }).chunk.text;
    });

    for (const tok of ['2', '+', '2 = 4']) {
      ws.serverFrame('llm:stream-chunk', {
        sub_id: 'sub-1',
        session_id: 's1',
        chunk: { kind: 'text', text: tok },
      });
    }
    off();

    expect(text).toBe('2+2 = 4');
  });

  it('fans an elicit snapshot out as individual pending asks', async () => {
    const { ws } = await openStream();
    const seen: unknown[] = [];
    const off = onServedEvent('elicit:pending', (p) => seen.push(p));

    ws.serverFrame('elicit:pending:snapshot', [{ request_id: 'a' }, { request_id: 'b' }]);
    off();

    expect(seen).toEqual([{ request_id: 'a' }, { request_id: 'b' }]);
  });

  it('drops nothing that the server bothered to send', async () => {
    // Regression guard for the original bug shape: a topic the server
    // forwards but the client silently ignores looks exactly like a
    // hung backend.
    const { SERVED_STREAM_TOPICS } = await import('@/lib/harnessClient');
    const { ws } = await openStream();
    expect(SERVED_STREAM_TOPICS.length).toBeGreaterThan(0);
    for (const topic of SERVED_STREAM_TOPICS) {
      const seen: unknown[] = [];
      const off = onServedEvent(topic, (p) => seen.push(p));
      ws.serverFrame(topic, { t: topic });
      off();
      expect(seen, `topic ${topic} was not forwarded`).toHaveLength(1);
    }
  });
});

// ── useSession surfaces truncation ────────────────────────────────────────

let servedModeFlag = false;
vi.mock('@/lib/useServedMode', async () => {
  const actual = await vi.importActual<typeof import('@/lib/useServedMode')>(
    '@/lib/useServedMode',
  );
  return { ...actual, isServedMode: () => servedModeFlag };
});

describe('useSession: truncation is visible, never silent', () => {
  beforeEach(() => {
    servedModeFlag = true;
  });
  afterEach(() => {
    servedModeFlag = false;
  });

  it('records a served:stream-truncated notice so the surface can warn', async () => {
    const { defineComponent, h, ref } = await import('vue');
    const { mount } = await import('@vue/test-utils');
    const { useSession } = await import('@/lib/useSession');
    const { provideFakeClient } = await import('@/lib/harnessClientContext');
    const { dispatchServedEvent } = await import('@/lib/useServedEvents');

    let captured: ReturnType<typeof useSession> | null = null;
    const Comp = defineComponent({
      setup() {
        captured = useSession(ref('s1'));
        return () => h('div');
      },
    });
    const wrapper = mount(Comp, {
      global: {
        plugins: [
          {
            install(app) {
              provideFakeClient(app, {});
            },
          },
        ],
      },
    });
    await Promise.resolve();

    expect(captured!.streamTruncated.value).toBeNull();

    dispatchServedEvent('served:stream-truncated', {
      dropped: 12,
      reason: 'slow-consumer',
      message: 'This browser could not keep up with the live stream.',
    });

    expect(captured!.streamTruncated.value?.dropped).toBe(12);
    // The copy must be usable from inside a VM.
    expect(
      captured!.streamTruncated.value?.message.toLowerCase(),
    ).not.toContain('desktop app');

    wrapper.unmount();
  });
});
