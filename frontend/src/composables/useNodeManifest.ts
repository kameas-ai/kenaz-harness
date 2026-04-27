/**
 * useNodeManifest — manifest-store + per-id resolved-manifest fetcher.
 *
 * Mission agent-kernel-graph-node-catalog WP06 (FR-027). The composable
 * pair backs the GraphEditor's NodePalette + NodeAttributeEditor:
 *
 *   - `useManifestStore()` is a process-singleton — the catalog list
 *     (one row per kind + archetype) loads once via
 *     `harnessClient.nodes.catalog()` and is shared across consumers.
 *
 *   - `useNodeManifest(idRef)` is the per-id resolved-manifest fetcher
 *     with provenance. Results are cached in-memory; the same id never
 *     hits the RPC twice in one session.
 *
 * Both helpers consume the injected `HarnessClient` (FR-009 contract);
 * tests swap in `createFakeHarnessClient` via `provideFakeClient`.
 */

import {
  ref,
  computed,
  watch,
  unref,
  type Ref,
  type ComputedRef,
  type MaybeRef,
} from 'vue';
import { useHarnessClient } from '@/lib/harnessClientContext';
import type {
  NodeManifestSummary,
  NodeManifestDetail,
} from '@/lib/types';
import type { HarnessClient } from '@/lib/harnessClient';

// ── per-client cached store ───────────────────────────────────────────

interface StoreCacheEntry {
  manifests: Ref<NodeManifestSummary[]>;
  loading: Ref<boolean>;
  error: Ref<string | null>;
  loadedOnce: Ref<boolean>;
  inflight: Promise<void> | null;
  detailCache: Map<string, NodeManifestDetail>;
  detailInflight: Map<string, Promise<NodeManifestDetail>>;
}

/**
 * The cache is keyed on the client object so tests that build their
 * own fake client never see another test's fixture data. We use a
 * regular Map (not WeakMap) so the test-reset helper can iterate.
 * The cardinality is small — one entry per app instance.
 */
const storeCache = new Map<HarnessClient, StoreCacheEntry>();

function getEntry(client: HarnessClient): StoreCacheEntry {
  let entry = storeCache.get(client);
  if (!entry) {
    entry = {
      manifests: ref<NodeManifestSummary[]>([]),
      loading: ref(false),
      error: ref<string | null>(null),
      loadedOnce: ref(false),
      inflight: null,
      detailCache: new Map(),
      detailInflight: new Map(),
    };
    storeCache.set(client, entry);
  }
  return entry;
}

// ── store composable ──────────────────────────────────────────────────

export interface ManifestStore {
  /** Catalog rows; each row is one kind or archetype. */
  manifests: Ref<NodeManifestSummary[]>;
  loading: Ref<boolean>;
  error: Ref<string | null>;
  /** Fires the initial load if it hasn't run yet. */
  ensureLoaded: () => Promise<void>;
  /** Force re-fetch the catalog (used by the doctor "Reload" button). */
  reload: () => Promise<void>;
}

/**
 * useManifestStore — singleton store. The first caller triggers the
 * catalog load; subsequent callers share the same reactive refs.
 */
export function useManifestStore(): ManifestStore {
  const client = useHarnessClient();
  const entry = getEntry(client);

  async function reload(): Promise<void> {
    entry.loading.value = true;
    entry.error.value = null;
    try {
      const rows = await client.nodes.catalog();
      entry.manifests.value = Array.isArray(rows) ? rows : [];
      entry.loadedOnce.value = true;
      // Detail cache is reload-stale — clear it so the editor refetches.
      entry.detailCache.clear();
      entry.detailInflight.clear();
    } catch (err) {
      entry.error.value = err instanceof Error ? err.message : String(err);
      entry.manifests.value = [];
    } finally {
      entry.loading.value = false;
    }
  }

  async function ensureLoaded(): Promise<void> {
    if (entry.loadedOnce.value) return;
    if (entry.inflight) {
      await entry.inflight;
      return;
    }
    entry.inflight = reload().finally(() => {
      entry.inflight = null;
    });
    await entry.inflight;
  }

  // Kick off the load on first construction. Callers that mount inside
  // a real Vue tree will see the refs flip from loading=true → false.
  if (!entry.loadedOnce.value && !entry.inflight) {
    void ensureLoaded();
  }

  return {
    manifests: entry.manifests,
    loading: entry.loading,
    error: entry.error,
    ensureLoaded,
    reload,
  };
}

// ── per-id manifest composable ───────────────────────────────────────

export interface UseNodeManifestResult {
  manifest: Ref<NodeManifestDetail | null>;
  loading: Ref<boolean>;
  error: Ref<string | null>;
  /** The resolved kind id this composable is tracking. */
  kindId: ComputedRef<string>;
}

/**
 * useNodeManifest — async fetcher for a single resolved manifest.
 * Caches by id within the per-client store. Empty id resolves to null.
 */
export function useNodeManifest(
  id: MaybeRef<string>,
): UseNodeManifestResult {
  const client = useHarnessClient();
  const entry = getEntry(client);

  const kindId = computed(() => unref(id) ?? '');
  const manifest = ref<NodeManifestDetail | null>(null);
  const loading = ref(false);
  const error = ref<string | null>(null);

  async function fetchOne(target: string): Promise<void> {
    if (!target) {
      manifest.value = null;
      error.value = null;
      loading.value = false;
      return;
    }
    const cached = entry.detailCache.get(target);
    if (cached) {
      manifest.value = cached;
      error.value = null;
      loading.value = false;
      return;
    }
    loading.value = true;
    error.value = null;
    try {
      const existing = entry.detailInflight.get(target);
      const promise =
        existing ??
        (() => {
          const p = client.nodes.get(target).then((detail) => {
            entry.detailCache.set(target, detail);
            return detail;
          });
          entry.detailInflight.set(target, p);
          return p;
        })();
      const detail = await promise;
      if (kindId.value === target) {
        manifest.value = detail;
      }
    } catch (err) {
      if (kindId.value === target) {
        error.value = err instanceof Error ? err.message : String(err);
        manifest.value = null;
      }
    } finally {
      entry.detailInflight.delete(target);
      if (kindId.value === target) {
        loading.value = false;
      }
    }
  }

  watch(
    kindId,
    (next) => {
      void fetchOne(next);
    },
    { immediate: true },
  );

  return { manifest, loading, error, kindId };
}

// ── test helper ──────────────────────────────────────────────────────

/**
 * Test-only — resets every per-client cache entry so each test sees
 * fresh refs. Production code does not import this.
 */
export function __resetManifestStoreCache(): void {
  for (const entry of storeCache.values()) {
    entry.manifests.value = [];
    entry.loading.value = false;
    entry.error.value = null;
    entry.loadedOnce.value = false;
    entry.inflight = null;
    entry.detailCache.clear();
    entry.detailInflight.clear();
  }
  storeCache.clear();
}
