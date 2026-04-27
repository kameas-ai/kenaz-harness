/**
 * useNodeManifest — composable tests covering store load/cache and
 * per-id resolved-manifest fetch + cache. Mirrors the WP07 RPC contract
 * via the createFakeHarnessClient seam.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { defineComponent, h, ref, nextTick } from 'vue';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import {
  useManifestStore,
  useNodeManifest,
  __resetManifestStoreCache,
} from '@/composables/useNodeManifest';
import type {
  HarnessClient,
} from '@/lib/harnessClient';
import type {
  NodeManifestSummary,
  NodeManifestDetail,
} from '@/lib/types';

function makeClient(over: {
  catalog?: () => Promise<NodeManifestSummary[]>;
  get?: (id: string) => Promise<NodeManifestDetail>;
}): HarnessClient {
  return createFakeHarnessClient({
    nodes: {
      catalog: vi.fn(over.catalog ?? (async () => [])),
      get:
        vi.fn(
          over.get ??
            (async (id: string) => ({
              summary: { id, callable: true },
              chain: [id],
              attrs: [],
              ports: {},
              provenance: [],
            })),
        ),
      reloadOverrides: async () => ({
        added: [],
        removed: [],
        modified: [],
      }),
      listUserOverrides: async () => [],
    },
  });
}

function mountWith<T>(client: HarnessClient, setup: () => T) {
  const Comp = defineComponent({
    setup() {
      const result = setup();
      // Expose via component instance for assertions.
      return () => h('div', { 'data-testid': 'host' }, JSON.stringify({ ok: !!result }));
    },
  });
  return mount(Comp, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
}

describe('useManifestStore', () => {
  beforeEach(() => {
    __resetManifestStoreCache();
  });

  it('loads the catalog on first construction', async () => {
    const rows: NodeManifestSummary[] = [
      { id: 'planner', callable: true, category: 'compute' },
      { id: 'compute', callable: false, category: 'compute' },
    ];
    const catalog = vi.fn(async () => rows);
    const client = makeClient({ catalog });

    let store: ReturnType<typeof useManifestStore> | null = null;
    mountWith(client, () => {
      store = useManifestStore();
      return store;
    });
    await flushPromises();

    expect(catalog).toHaveBeenCalledTimes(1);
    expect(store!.manifests.value).toHaveLength(2);
    expect(store!.manifests.value[0].id).toBe('planner');
    expect(store!.error.value).toBeNull();
  });

  it('shares the catalog across multiple consumers (singleton)', async () => {
    const catalog = vi.fn(async () => [
      { id: 'planner', callable: true } as NodeManifestSummary,
    ]);
    const client = makeClient({ catalog });

    mountWith(client, () => useManifestStore());
    mountWith(client, () => useManifestStore());
    mountWith(client, () => useManifestStore());
    await flushPromises();

    // Only one fetch despite three consumers.
    expect(catalog).toHaveBeenCalledTimes(1);
  });

  it('reload re-fetches the catalog', async () => {
    let n = 0;
    const catalog = vi.fn(async (): Promise<NodeManifestSummary[]> => {
      n += 1;
      return [{ id: `gen${n}`, callable: true }];
    });
    const client = makeClient({ catalog });

    let store: ReturnType<typeof useManifestStore> | null = null;
    mountWith(client, () => {
      store = useManifestStore();
      return store;
    });
    await flushPromises();
    expect(store!.manifests.value[0].id).toBe('gen1');
    await store!.reload();
    expect(store!.manifests.value[0].id).toBe('gen2');
    expect(catalog).toHaveBeenCalledTimes(2);
  });

  it('exposes an error and empty list when the catalog call rejects', async () => {
    const client = makeClient({
      catalog: async () => {
        throw new Error('boom');
      },
    });
    let store: ReturnType<typeof useManifestStore> | null = null;
    mountWith(client, () => {
      store = useManifestStore();
      return store;
    });
    await flushPromises();
    expect(store!.error.value).toContain('boom');
    expect(store!.manifests.value).toEqual([]);
  });
});

describe('useNodeManifest', () => {
  beforeEach(() => {
    __resetManifestStoreCache();
  });

  it('fetches a manifest by id and caches it', async () => {
    const get = vi.fn(async (id: string): Promise<NodeManifestDetail> => ({
      summary: { id, callable: true, displayName: id.toUpperCase() },
      chain: [id],
      attrs: [{ name: 'foo', type: 'string' }],
      ports: {},
      provenance: [{ fieldPath: 'attrs.foo', layer: 'shipped' }],
    }));
    const client = makeClient({ get });

    let result: ReturnType<typeof useNodeManifest> | null = null;
    mountWith(client, () => {
      result = useNodeManifest('planner');
      return result;
    });
    await flushPromises();

    expect(get).toHaveBeenCalledWith('planner');
    expect(result!.manifest.value?.summary.id).toBe('planner');
    expect(result!.manifest.value?.attrs[0].name).toBe('foo');

    // Second consumer requesting the same id hits the cache.
    mountWith(client, () => useNodeManifest('planner'));
    await flushPromises();
    expect(get).toHaveBeenCalledTimes(1);
  });

  it('refetches when the id ref changes', async () => {
    const get = vi.fn(async (id: string): Promise<NodeManifestDetail> => ({
      summary: { id, callable: true },
      chain: [id],
      attrs: [],
      ports: {},
      provenance: [],
    }));
    const client = makeClient({ get });

    const id = ref('planner');
    let result: ReturnType<typeof useNodeManifest> | null = null;
    mountWith(client, () => {
      result = useNodeManifest(id);
      return result;
    });
    await flushPromises();
    expect(get).toHaveBeenCalledWith('planner');

    id.value = 'reflect';
    await nextTick();
    await flushPromises();
    expect(get).toHaveBeenCalledWith('reflect');
    expect(result!.manifest.value?.summary.id).toBe('reflect');
  });

  it('resolves to null for an empty id', async () => {
    const client = makeClient({});
    let result: ReturnType<typeof useNodeManifest> | null = null;
    mountWith(client, () => {
      result = useNodeManifest('');
      return result;
    });
    await flushPromises();
    expect(result!.manifest.value).toBeNull();
  });

  it('reload clears the detail cache', async () => {
    let n = 0;
    const get = vi.fn(async (id: string): Promise<NodeManifestDetail> => {
      n += 1;
      return {
        summary: { id, callable: true, version: `v${n}` },
        chain: [id],
        attrs: [],
        ports: {},
        provenance: [],
      };
    });
    const client = makeClient({ get });

    // Prime the cache.
    let result: ReturnType<typeof useNodeManifest> | null = null;
    mountWith(client, () => {
      result = useNodeManifest('planner');
      return result;
    });
    await flushPromises();
    expect(get).toHaveBeenCalledTimes(1);

    // Reload via the store.
    let store: ReturnType<typeof useManifestStore> | null = null;
    mountWith(client, () => {
      store = useManifestStore();
      return store;
    });
    await flushPromises();
    await store!.reload();

    // Re-mount — cache must be re-populated, not reused.
    mountWith(client, () => useNodeManifest('planner'));
    await flushPromises();
    expect(get).toHaveBeenCalledTimes(2);
  });
});
