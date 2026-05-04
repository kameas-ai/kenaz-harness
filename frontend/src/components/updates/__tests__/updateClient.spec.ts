/**
 * updateClient.spec — round-trips every method of the WP04 update shim
 * against a fake `window.go.rpc.Bindings`. The real bindings will arrive
 * once WP03 (the RPC view PR) merges; until then this is the only place
 * we exercise the bridge contract.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  createUpdateClient,
  createFakeUpdateClient,
  type UpdateStatus,
} from '@/lib/updateClient';

interface FakeBindings {
  Update_Status: ReturnType<typeof vi.fn>;
  Update_StartCheck: ReturnType<typeof vi.fn>;
  Update_StartDownload: ReturnType<typeof vi.fn>;
  Update_Apply: ReturnType<typeof vi.fn>;
  Update_SkipVersion: ReturnType<typeof vi.fn>;
}

function installFakeBindings(): FakeBindings {
  const bindings: FakeBindings = {
    Update_Status: vi.fn(),
    Update_StartCheck: vi.fn().mockResolvedValue(undefined),
    Update_StartDownload: vi.fn().mockResolvedValue(undefined),
    Update_Apply: vi.fn().mockResolvedValue(undefined),
    Update_SkipVersion: vi.fn().mockResolvedValue(undefined),
  };
  (window as unknown as { go: unknown }).go = { rpc: { Bindings: bindings } };
  return bindings;
}

function uninstallBindings() {
  delete (window as unknown as { go?: unknown }).go;
}

const sampleStatus: UpdateStatus = {
  currentVersion: '0.4.0',
  available: true,
  availableVersion: '0.4.1',
  channel: 'stable',
  downloadState: 'idle',
  notes: 'Bug fixes & UI polish.',
  releaseUrl: 'https://github.com/kenaz/releases/tag/v0.4.1',
};

describe('createUpdateClient (live bridge)', () => {
  beforeEach(() => {
    installFakeBindings();
  });
  afterEach(() => {
    uninstallBindings();
  });

  it('throws when the bridge is missing', () => {
    uninstallBindings();
    const client = createUpdateClient();
    expect(() => client.status()).toThrow(/window\.go\.rpc\.Bindings/);
  });

  it('round-trips status() through Update_Status', async () => {
    const b = installFakeBindings();
    b.Update_Status.mockResolvedValueOnce(sampleStatus);
    const client = createUpdateClient();
    const out = await client.status();
    expect(out).toEqual(sampleStatus);
    expect(b.Update_Status).toHaveBeenCalledTimes(1);
  });

  it('forwards startCheck / startDownload / apply', async () => {
    const b = installFakeBindings();
    const client = createUpdateClient();
    await client.startCheck();
    await client.startDownload();
    await client.apply();
    expect(b.Update_StartCheck).toHaveBeenCalledTimes(1);
    expect(b.Update_StartDownload).toHaveBeenCalledTimes(1);
    expect(b.Update_Apply).toHaveBeenCalledTimes(1);
  });

  it('forwards skipVersion with the supplied version', async () => {
    const b = installFakeBindings();
    const client = createUpdateClient();
    await client.skipVersion('0.4.1');
    expect(b.Update_SkipVersion).toHaveBeenCalledWith('0.4.1');
  });
});

describe('createFakeUpdateClient (test stub)', () => {
  it('returns a "no update" status by default', async () => {
    const c = createFakeUpdateClient();
    const s = await c.status();
    expect(s.available).toBe(false);
    expect(s.downloadState).toBe('idle');
  });

  it('lets a caller override individual methods', async () => {
    const status = vi.fn().mockResolvedValue(sampleStatus);
    const c = createFakeUpdateClient({ status });
    const s = await c.status();
    expect(s).toEqual(sampleStatus);
    expect(status).toHaveBeenCalledTimes(1);
    // Other methods still work as no-ops.
    await expect(c.startDownload()).resolves.toBeUndefined();
    await expect(c.apply()).resolves.toBeUndefined();
    await expect(c.skipVersion('0.4.1')).resolves.toBeUndefined();
  });
});
