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

function baseStatus(overrides: Partial<UpdateStatus> = {}): UpdateStatus {
  return {
    currentVersion: '0.4.0',
    available: true,
    availableVersion: '0.4.1',
    channel: 'stable',
    downloadState: 'downloading',
    ...overrides,
  };
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

/**
 * installLatest — self-update-repair-01PMUP01 WP02.
 *
 * The A7 fix: installLatest must NOT be the unconditioned
 * `await Update_StartDownload(); await Update_Apply();` that always throws
 * ErrNothingStaged (StartDownload is fire-and-forget and clears hasStaged
 * before returning). It must poll Update_Status to a terminal state
 * (DC-1) and never read a broker event.
 */
describe('installLatest (WP02 — polls to a terminal state)', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    uninstallBindings();
    vi.useRealTimers();
  });

  // THE REGRESSION TEST. Mutation: restore
  // `await Update_StartDownload(); await Update_Apply();` → this test
  // fails, because Update_Apply would be called (and installLatest would
  // resolve/reject) on the very first microtask turn, before any
  // 'downloading' status was ever observed — i.e. Apply gets called
  // while downloading statuses are still queued up, and the assertions
  // below catch that on the very first poll.
  it('does not call Apply, and does not settle, while downloading is outstanding', async () => {
    const b = installFakeBindings();
    b.Update_Status
      .mockResolvedValueOnce(baseStatus({ downloadState: 'downloading', downloadProgress: 10 }))
      .mockResolvedValueOnce(baseStatus({ downloadState: 'downloading', downloadProgress: 40 }))
      .mockResolvedValueOnce(baseStatus({ downloadState: 'downloading', downloadProgress: 80 }))
      .mockResolvedValueOnce(baseStatus({ downloadState: 'staged' }));

    const client = createUpdateClient({ pollIntervalMs: 500 });
    let settled = false;
    const p = client.installLatest('0.4.1').then(
      () => {
        settled = true;
      },
      () => {
        settled = true;
      },
    );

    // Let StartDownload's await + the first Status() read resolve.
    await vi.advanceTimersByTimeAsync(0);
    expect(b.Update_Apply).not.toHaveBeenCalled();
    expect(settled).toBe(false);

    // Walk through the two remaining 'downloading' ticks — Apply must
    // stay uncalled and the promise unsettled at every step.
    await vi.advanceTimersByTimeAsync(500);
    expect(b.Update_Apply).not.toHaveBeenCalled();
    expect(settled).toBe(false);

    await vi.advanceTimersByTimeAsync(500);
    expect(b.Update_Apply).not.toHaveBeenCalled();
    expect(settled).toBe(false);

    // Final poll observes 'staged' — now, and only now, Apply fires.
    await vi.advanceTimersByTimeAsync(500);
    await p;
    expect(settled).toBe(true);
    expect(b.Update_Apply).toHaveBeenCalledTimes(1);
    expect(b.Update_Status).toHaveBeenCalledTimes(4);
  });

  it("rejects with the status's downloadError string on a 'failed' terminal state", async () => {
    const b = installFakeBindings();
    b.Update_Status.mockResolvedValueOnce(
      baseStatus({ downloadState: 'failed', downloadError: 'sha256 mismatch' }),
    );
    const client = createUpdateClient({ pollIntervalMs: 500 });

    const p = client.installLatest('0.4.1');
    const assertion = expect(p).rejects.toThrow('sha256 mismatch');
    await vi.advanceTimersByTimeAsync(0);
    await assertion;
    expect(b.Update_Apply).not.toHaveBeenCalled();
  });

  it('propagates ErrDownloadInFlight from startDownload unchanged, without polling', async () => {
    const b = installFakeBindings();
    b.Update_StartDownload.mockRejectedValueOnce(
      new Error('update: download already in progress'),
    );
    const client = createUpdateClient({ pollIntervalMs: 500 });

    await expect(client.installLatest('0.4.1')).rejects.toThrow(
      'update: download already in progress',
    );
    expect(b.Update_Status).not.toHaveBeenCalled();
    expect(b.Update_Apply).not.toHaveBeenCalled();
  });

  // Mutation: remove the deadline check (`if (Date.now() >= deadline)`)
  // → this test hangs (or times out per vitest's own test timeout)
  // instead of rejecting, because a wedged pump that never leaves
  // 'downloading' would poll forever.
  it('rejects naming the observed state once the poll ceiling expires', async () => {
    const b = installFakeBindings();
    b.Update_Status.mockResolvedValue(
      baseStatus({ downloadState: 'downloading', downloadProgress: 3 }),
    );
    const client = createUpdateClient({
      pollIntervalMs: 500,
      pollTimeoutMs: 2000,
    });

    const p = client.installLatest('0.4.1');
    let rejection: unknown;
    p.catch((e) => {
      rejection = e;
    });

    // Advance well past the 2s ceiling.
    await vi.advanceTimersByTimeAsync(5000);
    await expect(p).rejects.toThrow(/timed out/i);
    expect(rejection).toBeInstanceOf(Error);
    expect((rejection as Error).message).toMatch(/downloading/);
    expect(b.Update_Apply).not.toHaveBeenCalled();
  });
});
