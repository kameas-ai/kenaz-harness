/**
 * harnessClient.fleet.spec.ts
 *
 * Tests that the FleetClient interface is fully and correctly implemented in
 * the fake harness client, ensuring the consent surface is stable for
 * FleetTelemetryPanel and other consumers.
 * (fleet-otel-archival-01NDFSEX11 WP06)
 */
import { describe, it, expect, vi } from 'vitest';
import { createFakeHarnessClient } from '@/lib/harnessClient';

describe('createFakeHarnessClient — fleet sub-client', () => {
  it('exposes a fleet property', () => {
    const client = createFakeHarnessClient();
    expect(client.fleet).toBeDefined();
  });

  it('getTelemetryConsent resolves to "none" by default', async () => {
    const client = createFakeHarnessClient();
    const level = await client.fleet.getTelemetryConsent();
    expect(level).toBe('none');
  });

  it('setTelemetryConsent resolves without throwing by default', async () => {
    const client = createFakeHarnessClient();
    await expect(client.fleet.setTelemetryConsent('aggregate')).resolves.not.toThrow();
  });

  it('fleet methods can be overridden via seed', async () => {
    const mockLevel: 'aggregate' = 'aggregate';
    const setFn = vi.fn().mockResolvedValue(undefined);

    const client = createFakeHarnessClient({
      fleet: {
        getTelemetryConsent: async () => mockLevel,
        setTelemetryConsent: setFn,
      },
    });

    const level = await client.fleet.getTelemetryConsent();
    expect(level).toBe('aggregate');

    await client.fleet.setTelemetryConsent('full');
    expect(setFn).toHaveBeenCalledWith('full');
  });

  it('setTelemetryConsent passes the level through to the implementation', async () => {
    let received: string | undefined;
    const client = createFakeHarnessClient({
      fleet: {
        getTelemetryConsent: async () => 'none',
        setTelemetryConsent: async (level) => {
          received = level;
        },
      },
    });

    await client.fleet.setTelemetryConsent('full');
    expect(received).toBe('full');
  });
});
