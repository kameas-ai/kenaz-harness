/**
 * harnessClient.documents.test.ts — the Documents_* served overlay, bound to
 * the Go allowlist (contracts/documents-rpc.md §5).
 *
 * The Go served allowlist (core/serve/methods.go) and the TS served overlay
 * are two lists of the same surface. Spec 091 once shipped methods into the
 * Go list with no TS caller; this test reads the Go file and fails if the
 * Documents_* family on either side drifts from the other, and pins the
 * param names each call sends.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { parseDocumentsError, documentsErrorMessage } from '@/lib/documentsErrors';

interface RPCCall {
  method: string;
  params: Record<string, unknown>;
}

const originalFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = originalFetch;
});

async function servedClientRecording(respond: (call: RPCCall) => { result?: unknown; error?: string }) {
  const calls: RPCCall[] = [];
  globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    const body = JSON.parse((init?.body as string) ?? '{}') as RPCCall;
    const call = { method: body.method, params: body.params ?? {} };
    calls.push(call);
    return new Response(JSON.stringify(respond(call)), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }) as typeof globalThis.fetch;
  const { createServedHarnessClient } = await import('@/lib/harnessClient');
  return { client: createServedHarnessClient({ baseURL: 'http://127.0.0.1:7880', token: '' }), calls };
}

function goDocumentsMethods(): string[] {
  const methodsGo = readFileSync(resolve(__dirname, '../../../../core/serve/methods.go'), 'utf8');
  return [...methodsGo.matchAll(/"(Documents_[A-Za-z]+)"/g)].map((m) => m[1]).sort();
}

describe('served documents overlay', () => {
  it('calls exactly the Documents_* methods the Go allowlist serves, with contract params', async () => {
    const { client, calls } = await servedClientRecording(() => ({ result: null }));

    await client.documents.list('s1');
    await client.documents.get('s1', 'D1');
    await client.documents.create('s1', 'Title', '<p>x</p>');
    await client.documents.update('s1', 'D1', 3, '<p>y</p>');
    await client.documents.preview('<p>z</p>');
    await client.documents.buildSite('s1', 'team-kb', 'Team KB', ['D1', 'D2']);
    await client.documents.exportsDir();

    expect(calls).toEqual([
      { method: 'Documents_List', params: { sessionId: 's1' } },
      { method: 'Documents_Get', params: { sessionId: 's1', id: 'D1' } },
      { method: 'Documents_Create', params: { sessionId: 's1', title: 'Title', body: '<p>x</p>' } },
      { method: 'Documents_Update', params: { sessionId: 's1', id: 'D1', baseVersion: 3, body: '<p>y</p>' } },
      { method: 'Documents_Preview', params: { body: '<p>z</p>' } },
      {
        method: 'Documents_BuildSite',
        params: { sessionId: 's1', slug: 'team-kb', title: 'Team KB', documentIds: ['D1', 'D2'] },
      },
      { method: 'Documents_ExportsDir', params: {} },
    ]);

    const called = [...new Set(calls.map((c) => c.method))].sort();
    const served = goDocumentsMethods();
    expect(served.length).toBeGreaterThan(0);
    expect(called).toEqual(served);
  });

  it('surfaces the contract error through the served transport', async () => {
    const { client } = await servedClientRecording(() => ({
      error: 'documents: version_conflict: this document changed since you opened it; reload to see the current version',
    }));
    const err = await client.documents.update('s1', 'D1', 0, '<p>x</p>').catch((e: unknown) => e);
    expect(parseDocumentsError(err)).toEqual({
      code: 'version_conflict',
      message: 'this document changed since you opened it; reload to see the current version',
    });
  });
});

describe('parseDocumentsError', () => {
  it('parses the desktop (raw) and served (prefixed) forms', () => {
    expect(parseDocumentsError(new Error('documents: no_session: no session with that id exists'))).toEqual({
      code: 'no_session',
      message: 'no session with that id exists',
    });
    expect(
      parseDocumentsError(new Error('servedTransport: Documents_Get: documents: document_not_found: gone')),
    ).toEqual({ code: 'document_not_found', message: 'gone' });
    expect(parseDocumentsError('documents: bad_params: the request was malformed')?.code).toBe('bad_params');
  });

  it('returns null for failures outside the contract and falls back to the raw message', () => {
    const net = new Error('servedTransport: Documents_List: network error');
    expect(parseDocumentsError(net)).toBeNull();
    expect(documentsErrorMessage(net)).toBe('servedTransport: Documents_List: network error');
  });
});
