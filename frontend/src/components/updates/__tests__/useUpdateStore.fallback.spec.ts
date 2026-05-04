/**
 * useUpdateStore.fallback.spec — Layer 3 regression guard for v0.4.4.
 *
 * These tests verify the frontend-direct manifest fallback: when the backend
 * update service is broken/unavailable, the store fetches the public manifest
 * directly via window.fetch() and surfaces the update indicator if a newer
 * version is found.
 *
 * Tests exercise:
 *  1. semverGt — pure comparison helper
 *  2. fetchManifestVersion — fetch spy, happy path and error paths
 *  3. checkManifestFallback — end-to-end: fetch → compare → state mutation
 *  4. ensureBoot integration — the fallback is invoked on store boot
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { flushPromises } from '@vue/test-utils';
import {
  semverGt,
  fetchManifestVersion,
  checkManifestFallback,
  MANIFEST_URL,
  __resetUpdateStoreForTests,
  useUpdateStore,
} from '@/components/updates/useUpdateStore';
import { createFakeUpdateClient } from '@/lib/updateClient';

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function makeFetchSpy(version: string | null, ok = true) {
  const body = version !== null ? JSON.stringify({ version }) : 'not-json{{{';
  return vi.spyOn(global, 'fetch').mockResolvedValueOnce(
    new Response(body, { status: ok ? 200 : 503 }),
  );
}

beforeEach(() => {
  __resetUpdateStoreForTests({
    client: createFakeUpdateClient(), // default: no update, currentVersion '0.4.0'
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  __resetUpdateStoreForTests();
});

// ---------------------------------------------------------------------------
// semverGt
// ---------------------------------------------------------------------------

describe('semverGt', () => {
  it('returns true when candidate has higher patch', () => {
    expect(semverGt('0.4.4', '0.4.3')).toBe(true);
  });
  it('returns true when candidate has higher minor', () => {
    expect(semverGt('0.5.0', '0.4.9')).toBe(true);
  });
  it('returns true when candidate has higher major', () => {
    expect(semverGt('1.0.0', '0.99.99')).toBe(true);
  });
  it('returns false when versions are equal', () => {
    expect(semverGt('0.4.4', '0.4.4')).toBe(false);
  });
  it('returns false when candidate is lower', () => {
    expect(semverGt('0.4.3', '0.4.4')).toBe(false);
  });
  it('handles leading v prefix', () => {
    expect(semverGt('v0.5.0', 'v0.4.0')).toBe(true);
    expect(semverGt('v0.4.0', 'v0.5.0')).toBe(false);
  });
  it('handles pre-release tags (strips them)', () => {
    // v0.4.4-beta should compare as 0.4.4 (candidate), 0.4.3 (current) → true
    expect(semverGt('v0.4.4-beta', 'v0.4.3')).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// fetchManifestVersion
// ---------------------------------------------------------------------------

describe('fetchManifestVersion', () => {
  it('returns the version string from a valid manifest', async () => {
    makeFetchSpy('0.4.4');
    const v = await fetchManifestVersion();
    expect(v).toBe('0.4.4');
    expect(global.fetch).toHaveBeenCalledWith(MANIFEST_URL);
  });

  it('returns null when the HTTP response is not OK', async () => {
    makeFetchSpy('0.4.4', false /* ok=false */);
    const v = await fetchManifestVersion();
    expect(v).toBeNull();
  });

  it('returns null when the manifest has no version field', async () => {
    vi.spyOn(global, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ note: 'no version here' }), { status: 200 }),
    );
    const v = await fetchManifestVersion();
    expect(v).toBeNull();
  });

  it('returns null when fetch throws (network error)', async () => {
    vi.spyOn(global, 'fetch').mockRejectedValueOnce(new Error('network error'));
    const v = await fetchManifestVersion();
    expect(v).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// checkManifestFallback
// ---------------------------------------------------------------------------

describe('checkManifestFallback', () => {
  it('sets status to available when manifest is newer than currentVersion', async () => {
    makeFetchSpy('0.4.4');
    await checkManifestFallback('0.4.0');

    const { status } = useUpdateStore();
    expect(status.value).not.toBeNull();
    expect(status.value?.available).toBe(true);
    expect(status.value?.availableVersion).toBe('0.4.4');
    expect(status.value?.releaseUrl).toBe('https://docs.kameas.ai/download');
    expect(status.value?.notes).toContain('manual install required');
  });

  it('does NOT update status when manifest version equals currentVersion', async () => {
    makeFetchSpy('0.4.0');
    await checkManifestFallback('0.4.0');

    const { status } = useUpdateStore();
    // Status stays as the default (null — no update) set by the fake client at boot.
    expect(status.value).toBeNull();
  });

  it('does NOT update status when manifest version is older', async () => {
    makeFetchSpy('0.3.9');
    await checkManifestFallback('0.4.0');

    const { status } = useUpdateStore();
    expect(status.value).toBeNull();
  });

  it('does NOT override when backend already reported the same availableVersion', async () => {
    makeFetchSpy('0.4.4');
    // Pre-seed the state as if the backend already reported this version.
    const { status } = useUpdateStore();
    (status as { value: unknown }).value = {
      currentVersion: '0.4.0',
      available: true,
      availableVersion: '0.4.4',
      channel: 'stable',
      downloadState: 'idle',
    };
    const sentinelNotes = 'Backend-supplied notes';
    (status as { value: { notes: string } }).value!.notes = sentinelNotes;

    await checkManifestFallback('0.4.0');

    // The backend-supplied notes should NOT be overwritten.
    expect(status.value?.notes).toBe(sentinelNotes);
  });

  it('does nothing when the fetch returns null', async () => {
    vi.spyOn(global, 'fetch').mockRejectedValueOnce(new Error('offline'));
    await checkManifestFallback('0.4.0');

    const { status } = useUpdateStore();
    expect(status.value).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// ensureBoot integration — fallback is triggered on boot
// ---------------------------------------------------------------------------

describe('ensureBoot — manifest fallback integration', () => {
  it('triggers a manifest fetch on boot and surfaces the indicator when manifest is newer', async () => {
    // Backend reports no update but returns currentVersion so the fallback can run.
    const fakeClient = createFakeUpdateClient({
      status: vi.fn().mockResolvedValue({
        currentVersion: '0.4.0',
        available: false,
        channel: 'stable',
        downloadState: 'idle' as const,
      }),
    });
    __resetUpdateStoreForTests({ client: fakeClient });

    // Manifest says a newer version is out.
    makeFetchSpy('0.4.4');

    const store = useUpdateStore();
    await store.ensureBoot();
    await flushPromises();

    expect(store.status.value?.available).toBe(true);
    expect(store.status.value?.availableVersion).toBe('0.4.4');
    expect(store.visible.value).toBe(true);
  });

  it('does NOT trigger a manifest fetch when backend status() fails (no currentVersion)', async () => {
    // Backend completely broken — status() throws.
    const fakeClient = createFakeUpdateClient({
      status: vi.fn().mockRejectedValue(new Error('bridge not ready')),
    });
    __resetUpdateStoreForTests({ client: fakeClient });

    const fetchSpy = vi.spyOn(global, 'fetch');
    const store = useUpdateStore();
    await store.ensureBoot();
    await flushPromises();

    // fetch should NOT have been called — no currentVersion to compare against.
    expect(fetchSpy).not.toHaveBeenCalled();
    expect(store.status.value).toBeNull();
  });
});
