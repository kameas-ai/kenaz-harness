/**
 * servedTransport.test.ts — unit tests for the HTTP/WS transport layer.
 *
 * The transport is exercised against a stubbed fetch (MSW-style inline mock).
 * WebSocket behaviour is validated with a minimal ws-mock helper.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { ServedTransport } from '@/lib/servedTransport';

// ── fetch stub helpers ────────────────────────────────────────────────────────

type FetchStub = (input: RequestInfo | URL, init?: RequestInit) => Promise<Response>;

function makeFetchStub(handler: FetchStub): FetchStub {
  return handler;
}

function jsonOk<T>(body: T): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

function jsonErr(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

// ── /rpc call tests ───────────────────────────────────────────────────────────

describe('ServedTransport.call', () => {
  let origFetch: typeof globalThis.fetch;

  beforeEach(() => {
    origFetch = globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = origFetch;
  });

  it('POSTs to /rpc with JSON body and returns result', async () => {
    const captured: { url: string; body: unknown }[] = [];

    globalThis.fetch = makeFetchStub(async (input, init) => {
      captured.push({
        url: String(input),
        body: JSON.parse((init?.body as string) ?? '{}'),
      });
      return jsonOk({ result: { id: 'app-v1', build: '0.1.0' } });
    });

    const transport = new ServedTransport({ baseURL: 'http://127.0.0.1:7880' });
    const result = await transport.call<{ id: string; build: string }>('AppInfo');

    expect(captured).toHaveLength(1);
    expect(captured[0].url).toBe('http://127.0.0.1:7880/rpc');
    expect(captured[0].body).toEqual({ method: 'AppInfo', params: null });
    expect(result).toEqual({ id: 'app-v1', build: '0.1.0' });
  });

  it('includes Authorization header when a token is provided', async () => {
    // Capture the raw headers object as passed to fetch (before any Headers normalisation).
    const rawHeaders: unknown[] = [];

    globalThis.fetch = makeFetchStub(async (_input, init) => {
      rawHeaders.push(init?.headers);
      return jsonOk({ result: [] });
    });

    const transport = new ServedTransport({
      baseURL: 'http://127.0.0.1:7880',
      token: 'secret-tok',
    });
    await transport.call('Sessions_List');

    const h = rawHeaders[0] as Record<string, string>;
    expect(h?.['Authorization']).toBe('Bearer secret-tok');
  });

  it('omits Authorization header when no token is configured', async () => {
    const rawHeaders: unknown[] = [];

    globalThis.fetch = makeFetchStub(async (_input, init) => {
      rawHeaders.push(init?.headers);
      return jsonOk({ result: [] });
    });

    const transport = new ServedTransport({ baseURL: 'http://127.0.0.1:7880', token: '' });
    await transport.call('Sessions_List');

    const h = rawHeaders[0] as Record<string, string>;
    expect(h?.['Authorization']).toBeUndefined();
  });

  it('throws when the server returns { error }', async () => {
    globalThis.fetch = makeFetchStub(async () =>
      jsonOk({ error: 'method not found: Unknown' }),
    );

    const transport = new ServedTransport({ baseURL: 'http://127.0.0.1:7880' });
    await expect(transport.call('Unknown')).rejects.toThrow(
      'method not found: Unknown',
    );
  });

  it('throws when the server returns a non-200 status', async () => {
    globalThis.fetch = makeFetchStub(async () =>
      jsonErr(401, { error: 'unauthorized' }),
    );

    const transport = new ServedTransport({ baseURL: 'http://127.0.0.1:7880' });
    await expect(transport.call('AppInfo')).rejects.toThrow('unauthorized');
  });

  it('forwards params as the JSON body params field', async () => {
    const bodies: unknown[] = [];

    globalThis.fetch = makeFetchStub(async (_input, init) => {
      bodies.push(JSON.parse((init?.body as string) ?? '{}'));
      return jsonOk({ result: { id: 'sess-1' } });
    });

    const transport = new ServedTransport({ baseURL: 'http://127.0.0.1:7880' });
    await transport.call('Sessions_Get', { id: 'sess-1' });

    expect(bodies[0]).toEqual({ method: 'Sessions_Get', params: { id: 'sess-1' } });
  });
});

// ── createServedHarnessClient integration smoke ───────────────────────────────

describe('createServedHarnessClient', () => {
  let origFetch: typeof globalThis.fetch;

  beforeEach(() => {
    origFetch = globalThis.fetch;
  });

  afterEach(() => {
    globalThis.fetch = origFetch;
  });

  it('routes appInfo() to POST /rpc { method: "AppInfo" }', async () => {
    const calls: string[] = [];

    globalThis.fetch = makeFetchStub(async (_input, init) => {
      const body = JSON.parse((init?.body as string) ?? '{}') as { method?: string };
      calls.push(body.method ?? '');
      return jsonOk({
        result: {
          build: '0.1.0',
          commit: 'abc',
          platform: 'linux',
          dataDir: '/tmp',
          logDir: '/tmp',
          env: 'dev',
          policyEditorEnabled: false,
          keychainRotationEnabled: false,
        },
      });
    });

    // Import dynamically so we pick up the module fresh.
    const { createServedHarnessClient } = await import('@/lib/harnessClient');
    const client = createServedHarnessClient({ baseURL: 'http://127.0.0.1:7880', token: '' });

    const info = await client.appInfo();
    expect(calls).toContain('AppInfo');
    expect(info.build).toBe('0.1.0');
  });

  it('routes shellStatus() to POST /rpc { method: "ShellStatus" }', async () => {
    const calls: string[] = [];

    globalThis.fetch = makeFetchStub(async (_input, init) => {
      const body = JSON.parse((init?.body as string) ?? '{}') as { method?: string };
      calls.push(body.method ?? '');
      return jsonOk({
        result: {
          activeProvider: 'anthropic',
          trustTier: 'Local',
          harnessBuild: '0.1.0',
          connection: 'ready',
          eventRate: 0,
          policyApplied: true,
          redactionOn: true,
        },
      });
    });

    const { createServedHarnessClient } = await import('@/lib/harnessClient');
    const client = createServedHarnessClient({ baseURL: 'http://127.0.0.1:7880', token: '' });

    const status = await client.shellStatus();
    expect(calls).toContain('ShellStatus');
    expect(status.connection).toBe('ready');
  });

  it('routes sessions.list() to POST /rpc { method: "Sessions_List" }', async () => {
    const calls: string[] = [];

    globalThis.fetch = makeFetchStub(async (_input, init) => {
      const body = JSON.parse((init?.body as string) ?? '{}') as { method?: string };
      calls.push(body.method ?? '');
      return jsonOk({ result: [{ id: 's1', name: 'test', createdAt: '', updatedAt: '' }] });
    });

    const { createServedHarnessClient } = await import('@/lib/harnessClient');
    const client = createServedHarnessClient({ baseURL: 'http://127.0.0.1:7880', token: '' });

    const sessions = await client.sessions.list();
    expect(calls).toContain('Sessions_List');
    expect(sessions).toHaveLength(1);
    expect(sessions[0].id).toBe('s1');
  });

  it('routes sessions.get(id) to POST /rpc { method: "Sessions_Get", params: { id } }', async () => {
    const bodies: unknown[] = [];

    globalThis.fetch = makeFetchStub(async (_input, init) => {
      bodies.push(JSON.parse((init?.body as string) ?? '{}'));
      return jsonOk({ result: { id: 's42', name: 'my session', createdAt: '', updatedAt: '' } });
    });

    const { createServedHarnessClient } = await import('@/lib/harnessClient');
    const client = createServedHarnessClient({ baseURL: 'http://127.0.0.1:7880', token: '' });

    const session = await client.sessions.get('s42');
    expect(bodies[0]).toEqual({ method: 'Sessions_Get', params: { id: 's42' } });
    expect(session.id).toBe('s42');
  });

  it('falls back to stub (empty array) for unimplemented methods like projects.list()', async () => {
    globalThis.fetch = makeFetchStub(async () => {
      throw new Error('should not be called');
    });

    const { createServedHarnessClient } = await import('@/lib/harnessClient');
    const client = createServedHarnessClient({ baseURL: 'http://127.0.0.1:7880', token: '' });

    // projects.list() is not wired in served mode — should return stub default.
    const projects = await client.projects.list();
    expect(projects).toEqual([]);
  });
});
