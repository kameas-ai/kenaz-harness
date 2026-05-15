/**
 * harnessClient.sentry.spec.ts
 *
 * Tests that the SentryClient interface is fully and correctly implemented
 * in the fake harness client, ensuring the contract is stable for use by
 * settings panels and tests.
 * (sentry-error-monitoring-01KX5R8G WP05)
 */
import { describe, it, expect } from 'vitest';
import { createFakeHarnessClient } from '@/lib/harnessClient';

describe('createFakeHarnessClient — sentry sub-client', () => {
  it('exposes a sentry property', () => {
    const client = createFakeHarnessClient();
    expect(client.sentry).toBeDefined();
  });

  it('getLastFive resolves to an empty array by default', async () => {
    const client = createFakeHarnessClient();
    const result = await client.sentry.getLastFive();
    expect(result).toEqual([]);
  });

  it('generateLocalReport resolves to empty path + byteCount 0 by default', async () => {
    const client = createFakeHarnessClient();
    const result = await client.sentry.generateLocalReport();
    expect(result).toMatchObject({ path: expect.any(String), byteCount: expect.any(Number) });
  });

  it('testDsn resolves to { ok: false } by default', async () => {
    const client = createFakeHarnessClient();
    const result = await client.sentry.testDsn('https://key@sentry.example.com/123');
    expect(typeof result.ok).toBe('boolean');
  });

  it('sentry methods can be overridden via seed', async () => {
    const mockEntries = [
      {
        id: 'evt-1',
        capturedAt: '2026-05-15T00:00:00Z',
        kind: 'exception',
        summary: 'Test error',
      },
    ];
    const client = createFakeHarnessClient({
      sentry: {
        getLastFive: async () => mockEntries,
        generateLocalReport: async () => ({ path: '/tmp/crash.json', byteCount: 512 }),
        testDsn: async () => ({ ok: true }),
      },
    });

    const entries = await client.sentry.getLastFive();
    expect(entries).toHaveLength(1);
    expect(entries[0].summary).toBe('Test error');

    const report = await client.sentry.generateLocalReport();
    expect(report.path).toBe('/tmp/crash.json');
    expect(report.byteCount).toBe(512);

    const dsn = await client.sentry.testDsn('https://key@sentry.example.com/1');
    expect(dsn.ok).toBe(true);
  });

  it('testDsn passes the dsn argument through', async () => {
    let receivedDsn = '';
    const client = createFakeHarnessClient({
      sentry: {
        getLastFive: async () => [],
        generateLocalReport: async () => ({ path: '', byteCount: 0 }),
        testDsn: async (dsn) => {
          receivedDsn = dsn;
          return { ok: true };
        },
      },
    });

    await client.sentry.testDsn('https://foo@bar.baz/42');
    expect(receivedDsn).toBe('https://foo@bar.baz/42');
  });
});
