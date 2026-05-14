/**
 * artifactBytesCache — module-level LRU cache for artifact byte payloads.
 *
 * `Artifacts_Get` fetches both the artifact metadata and its full bytes
 * payload over the Wails IPC boundary. For model-generated images this
 * can be up to 20 MiB per call. The cache prevents redundant IPC round
 * trips when the same artifact is viewed multiple times (e.g. opening and
 * closing the lightbox, or re-rendering after a Vue component remount).
 *
 * The cache is intentionally simple:
 *   - Keyed by artifact ID (stable, server-assigned).
 *   - Capacity: at most MAX_ENTRIES entries (LRU eviction).
 *   - No TTL — entries live until evicted by cap or explicit clear.
 *   - Singleton (module scope) — shared across all component instances.
 *
 * Usage:
 *   import { getArtifactBytes, clearArtifactBytesCache } from '@/lib/artifactBytesCache';
 *   const bytes = await getArtifactBytes(client, artifactId);
 *
 * (multimodal-io-extended-01KQ8TD2 WP06)
 */

import type { HarnessClient } from './harnessClient';
import type { ArtifactWithBytes } from './types';

/** Maximum number of entries kept in memory at once. */
const MAX_ENTRIES = 32;

/**
 * LRU cache implemented with a Map (insertion-order keys).
 * On each access we delete + re-insert to move the entry to the tail
 * (most-recently-used), so the head is always the oldest entry.
 */
const cache = new Map<string, ArtifactWithBytes>();

/**
 * In-flight request dedup map. When two concurrent callers request the
 * same artifact before the first IPC call resolves, only one IPC is issued;
 * the second awaits the same Promise.
 */
const inflight = new Map<string, Promise<ArtifactWithBytes>>();

function evictIfNeeded(): void {
  // Map.keys() returns keys in insertion order (oldest first).
  while (cache.size >= MAX_ENTRIES) {
    const oldest = cache.keys().next().value;
    if (oldest !== undefined) {
      cache.delete(oldest);
    }
  }
}

/**
 * Retrieve artifact bytes from the cache or fetch them via IPC.
 *
 * @param client  The `HarnessClient` instance; only `client.artifacts.get`
 *                is called.
 * @param id      The artifact ID to fetch.
 * @returns       The full `ArtifactWithBytes` envelope.
 */
export async function getArtifactBytes(
  client: HarnessClient,
  id: string,
): Promise<ArtifactWithBytes> {
  // Cache hit: promote to tail and return.
  const cached = cache.get(id);
  if (cached !== undefined) {
    cache.delete(id);
    cache.set(id, cached);
    return cached;
  }

  // Dedup: join an existing in-flight request.
  const existing = inflight.get(id);
  if (existing !== undefined) {
    return existing;
  }

  // Cache miss: issue the IPC call.
  const promise = client.artifacts.get(id).then((result) => {
    inflight.delete(id);
    evictIfNeeded();
    cache.set(id, result);
    return result;
  }).catch((err: unknown) => {
    inflight.delete(id);
    throw err;
  });

  inflight.set(id, promise);
  return promise;
}

/**
 * Evict a single artifact from the cache (e.g. after it is deleted or
 * re-captured with a new byte payload).
 */
export function evictArtifactBytes(id: string): void {
  cache.delete(id);
}

/**
 * Flush the entire cache. Useful in tests or after a bulk delete operation.
 */
export function clearArtifactBytesCache(): void {
  cache.clear();
  inflight.clear();
}
