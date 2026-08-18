import { describe, it, expect } from 'vitest';
import {
  applyDownloadProgressEvent,
  applyDownloadCompleteEvent,
  applyDownloadFailedEvent,
} from '@/lib/updateDownloadEvents';
import type { UpdateStatus } from '@/lib/updateClient';

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

describe('applyDownloadProgressEvent', () => {
  it('sets downloading + the percent from the payload', () => {
    const out = applyDownloadProgressEvent(baseStatus({ downloadProgress: 0 }), {
      bytes: 40,
      total: 100,
      percent: 40,
    });
    expect(out?.downloadState).toBe('downloading');
    expect(out?.downloadProgress).toBe(40);
  });

  it('is a no-op on a null snapshot (no status fetched yet)', () => {
    expect(applyDownloadProgressEvent(null, { bytes: 1, total: 1, percent: 100 })).toBeNull();
  });
});

describe('applyDownloadCompleteEvent', () => {
  it('sets staged + 100% and clears any prior error', () => {
    const out = applyDownloadCompleteEvent(
      baseStatus({ downloadState: 'downloading', downloadProgress: 80 }),
      { targetVersion: '0.4.1' },
    );
    expect(out?.downloadState).toBe('staged');
    expect(out?.downloadProgress).toBe(100);
    expect(out?.downloadError).toBeUndefined();
  });
});

describe('applyDownloadFailedEvent', () => {
  it('sets failed + the error string from the payload', () => {
    const out = applyDownloadFailedEvent(baseStatus(), { err: 'sha256 mismatch' });
    expect(out?.downloadState).toBe('failed');
    expect(out?.downloadError).toBe('sha256 mismatch');
  });
});
