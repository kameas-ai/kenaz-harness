/**
 * Served mode is where everyday work happens. If the consent calls are not on
 * the served transport, a workbench can never leave consent "none".
 */
import { describe, it, expect, afterEach } from 'vitest';

const realFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = realFetch;
});

function captureRPC(result: unknown) {
  const calls: Array<{ method: string; params: unknown }> = [];
  globalThis.fetch = (async (_url: unknown, init?: RequestInit) => {
    calls.push(JSON.parse(String(init?.body)));
    return new Response(JSON.stringify({ result }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  }) as typeof globalThis.fetch;
  return calls;
}

describe('served client: fleet telemetry', () => {
  it('reads consent over the transport', async () => {
    const calls = captureRPC('aggregate');
    const { createServedHarnessClient } = await import('@/lib/harnessClient');
    const level = await createServedHarnessClient({ baseURL: '', token: '' }).fleet.getTelemetryConsent();
    expect(level).toBe('aggregate');
    expect(calls[0].method).toBe('Fleet_GetTelemetryConsent');
  });

  it('writes consent with the param name the Go dispatch decodes', async () => {
    const calls = captureRPC(null);
    const { createServedHarnessClient } = await import('@/lib/harnessClient');
    await createServedHarnessClient({ baseURL: '', token: '' }).fleet.setTelemetryConsent('full');
    expect(calls[0]).toEqual({ method: 'Fleet_SetTelemetryConsent', params: { level: 'full' } });
  });

  it('reads status, including the served-only enroll block', async () => {
    const calls = captureRPC({ wired: true, enroll: { auth_state: 'signed_in' } });
    const { createServedHarnessClient } = await import('@/lib/harnessClient');
    const st = await createServedHarnessClient({ baseURL: '', token: '' }).fleet.getTelemetryStatus();
    expect(calls[0].method).toBe('Fleet_TelemetryStatus');
    expect(st.enroll?.auth_state).toBe('signed_in');
  });
});
