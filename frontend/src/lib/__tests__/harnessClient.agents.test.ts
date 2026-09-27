/**
 * harnessClient.agents.test.ts — the Agents_* served overlay, bound to the
 * Go allowlist (contracts/agents-served-rpc.md §5).
 *
 * Same drift-guard shape as harnessClient.documents.test.ts: reads the Go
 * served allowlist and fails if the Agents_* family on either side drifts
 * from the other, and pins the param shapes each call sends.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';

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

function goAgentsMethods(): string[] {
  const methodsGo = readFileSync(resolve(__dirname, '../../../../core/serve/methods.go'), 'utf8');
  return [...methodsGo.matchAll(/"(Agents_[A-Za-z]+)"/g)].map((m) => m[1]).sort();
}

describe('served agents overlay', () => {
  it('calls exactly the Agents_* methods the Go allowlist serves, with contract params', async () => {
    const { client, calls } = await servedClientRecording(() => ({ result: null }));

    const profile = {
      id: 'p1',
      name: 'Profile One',
      description: 'desc',
      autonomyTier: 'default',
      mergePolicy: 'auto',
      bundled: false,
    };

    await client.agents.listProfiles();
    await client.agents.loadProfile('p1');
    await client.agents.saveProfile(profile as never);
    await client.agents.deleteProfile('p1');

    expect(calls).toEqual([
      { method: 'Agents_ListProfiles', params: {} },
      { method: 'Agents_LoadProfile', params: { id: 'p1' } },
      // The whole profile IS the params object — no wrapper key — matching
      // core/serve/server.go's Agents_SaveProfile case (json.Unmarshal(params, &p)).
      { method: 'Agents_SaveProfile', params: profile },
      { method: 'Agents_DeleteProfile', params: { id: 'p1' } },
    ]);

    const called = [...new Set(calls.map((c) => c.method))].sort();
    const served = goAgentsMethods();
    expect(served.length).toBe(4);
    expect(called).toEqual(served);
  });

  it('propagates a rejected profile save (e.g. bundled read-only) as a thrown error', async () => {
    const { client } = await servedClientRecording(() => ({
      error: 'agents.SaveProfile: agents: bundled profiles are read-only',
    }));
    const err = await client.agents
      .saveProfile({ id: 'explore', name: 'x', autonomyTier: 'default', mergePolicy: 'auto', bundled: false } as never)
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(Error);
    expect((err as Error).message).toContain('read-only');
  });
});
