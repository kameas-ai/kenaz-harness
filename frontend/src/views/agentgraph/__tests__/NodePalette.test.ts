/**
 * NodePalette tests — covers FR-025 / FR-028 acceptance:
 *   - Categories render in declared order.
 *   - Archetypes are visible but disabled (greyed out + tooltip).
 *   - Concrete kinds are draggable.
 *   - Filter input narrows the tree.
 *   - dragstart emits the kind payload to the parent.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import NodePalette from '@/views/agentgraph/NodePalette.vue';
import { __resetManifestStoreCache } from '@/composables/useNodeManifest';
import type {
  NodeManifestSummary,
  NodeDoctorReport,
  NodeUserOverrideInfo,
  NodeReloadResult,
} from '@/lib/types';

const FIXTURE: NodeManifestSummary[] = [
  // Compute archetype + two kinds.
  {
    id: 'compute',
    displayName: 'Compute',
    description: 'LLM-shaped surface',
    category: 'compute',
    callable: false,
  },
  {
    id: 'planner',
    displayName: 'Planner',
    description: 'Plans next step',
    category: 'compute',
    archetype: 'compute',
    callable: true,
  },
  {
    id: 'model',
    displayName: 'Model',
    description: 'Direct LLM call',
    category: 'compute',
    archetype: 'compute',
    callable: true,
  },
  // Control archetype + one kind.
  {
    id: 'control',
    displayName: 'Control',
    category: 'control',
    callable: false,
  },
  {
    id: 'decision',
    displayName: 'Decision',
    description: 'Predicate router',
    category: 'control',
    archetype: 'control',
    callable: true,
  },
  // State archetype tree.
  {
    id: 'state',
    displayName: 'State',
    category: 'state',
    callable: false,
  },
  {
    id: 'read',
    displayName: 'Read',
    category: 'state',
    archetype: 'state',
    callable: false,
  },
  {
    id: 'history_read',
    displayName: 'History read',
    description: 'Reads conversation history',
    category: 'state',
    archetype: 'read',
    callable: true,
  },
];

interface NodesOverrides {
  reloadOverrides?: () => Promise<NodeReloadResult>;
  listUserOverrides?: () => Promise<NodeUserOverrideInfo[]>;
  doctor?: () => Promise<NodeDoctorReport>;
}

function mountPalette(rows: NodeManifestSummary[] = FIXTURE, overrides: NodesOverrides = {}) {
  const catalog = vi.fn(async () => rows);
  const reloadOverrides = vi.fn(
    overrides.reloadOverrides ?? (async () => ({ added: [], removed: [], modified: [] })),
  );
  const listUserOverrides = vi.fn(overrides.listUserOverrides ?? (async () => []));
  const doctor = vi.fn(
    overrides.doctor ??
      (async () => ({
        shippedCount: 0,
        userOverrideCount: 0,
        archetypeCount: 0,
        callableCount: 0,
        aliasCount: 0,
        hotReloadEnabled: false,
      })),
  );
  const client = createFakeHarnessClient({
    nodes: {
      catalog,
      get: async (id) => ({
        summary: { id, callable: false },
        chain: [id],
        attrs: [],
        ports: {},
        provenance: [],
      }),
      reloadOverrides,
      listUserOverrides,
      doctor,
    },
  });
  const wrapper = mount(NodePalette, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
  return { wrapper, catalog, reloadOverrides, listUserOverrides, doctor };
}

describe('NodePalette', () => {
  beforeEach(() => {
    __resetManifestStoreCache();
  });

  it('renders Compute / Control / State categories', async () => {
    const { wrapper } = mountPalette();
    await flushPromises();
    expect(wrapper.find('[data-testid="palette-category-compute"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="palette-category-control"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="palette-category-state"]').exists()).toBe(true);
  });

  it('lists archetypes as non-droppable rows with abstract badge + tooltip', async () => {
    const { wrapper } = mountPalette();
    await flushPromises();
    const archetypeRow = wrapper.find(
      '[data-testid="palette-archetype-compute"]',
    );
    expect(archetypeRow.exists()).toBe(true);
    const inner = archetypeRow.find('[data-testid="palette-archetype-row"]');
    expect(inner.exists()).toBe(true);
    expect(inner.attributes('aria-disabled')).toBe('true');
    expect(inner.attributes('title')).toContain('archetypes are abstract');
    expect(inner.text().toLowerCase()).toContain('abstract');
    // Archetypes themselves should NOT be draggable.
    expect(inner.attributes('draggable')).toBeFalsy();
  });

  it('renders concrete kinds as draggable list items', async () => {
    const { wrapper } = mountPalette();
    await flushPromises();
    const planner = wrapper.find('[data-testid="palette-kind-planner"]');
    expect(planner.exists()).toBe(true);
    expect(planner.attributes('draggable')).toBe('true');
  });

  it('filter narrows the tree by case-insensitive substring', async () => {
    const { wrapper } = mountPalette();
    await flushPromises();
    const input = wrapper.get('[data-testid="palette-filter"]');
    await input.setValue('PLAN');
    await flushPromises();
    expect(wrapper.find('[data-testid="palette-kind-planner"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="palette-kind-decision"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="palette-kind-history_read"]').exists()).toBe(false);
  });

  it('dragstart on a concrete kind emits kind-drag-start with id', async () => {
    const { wrapper } = mountPalette();
    await flushPromises();
    const row = wrapper.get('[data-testid="palette-kind-decision"]');
    // happy-dom doesn't supply a real DataTransfer; provide a stub.
    const dt = {
      setData: vi.fn(),
      types: [] as string[],
      effectAllowed: '',
    };
    await row.trigger('dragstart', { dataTransfer: dt });
    const events = wrapper.emitted('kind-drag-start');
    expect(events).toBeTruthy();
    expect(events![0][0]).toEqual({ kind: 'decision' });
    expect(dt.setData).toHaveBeenCalledWith(
      'application/x-kenaz-node-kind',
      'decision',
    );
  });

  it('clicking a kind emits kind-select', async () => {
    const { wrapper } = mountPalette();
    await flushPromises();
    await wrapper.get('[data-testid="palette-kind-model"]').trigger('click');
    const events = wrapper.emitted('kind-select');
    expect(events).toBeTruthy();
    expect(events![0][0]).toEqual({ kind: 'model' });
  });

  it('shows an empty-state row when the filter has no matches', async () => {
    const { wrapper } = mountPalette();
    await flushPromises();
    await wrapper.get('[data-testid="palette-filter"]').setValue('xyzzy_nope');
    await flushPromises();
    expect(wrapper.find('[data-testid="palette-empty"]').exists()).toBe(true);
  });

  // ── node-override diagnostics (WP18, FR-027 C2V-15 / AC-046 / AC-047) ──

  it('Doctor button surfaces a fresh per-file parse error without a restart', async () => {
    const { wrapper, listUserOverrides, doctor, reloadOverrides } = mountPalette(FIXTURE, {
      listUserOverrides: async () => [
        {
          filename: 'broken.yaml',
          path: '/data/agent_graph/nodes/broken.yaml',
          status: 'error',
          error: 'yaml: line 3: did not find expected key',
        },
      ],
    });
    await flushPromises();

    expect(wrapper.find('[data-testid="node-diagnostics-panel"]').exists()).toBe(false);
    await wrapper.get('[data-testid="palette-doctor"]').trigger('click');
    await flushPromises();

    // Doctor pulls both the cached health report AND a fresh per-file
    // parse pass; it must NOT trigger the catalog-swapping reload.
    expect(doctor).toHaveBeenCalledTimes(1);
    expect(listUserOverrides).toHaveBeenCalledTimes(1);
    expect(reloadOverrides).not.toHaveBeenCalled();

    expect(wrapper.get('[data-testid="override-status-broken.yaml"]').text()).toBe('error');
    expect(wrapper.get('[data-testid="override-error-broken.yaml"]').text()).toContain(
      'did not find expected key',
    );
  });

  it(
    'Reload runs the real on-disk rescan so a fixed override kind appears ' +
      'without a restart (mutation: a Reload that only re-fetches the ' +
      'in-memory catalog instead of calling reloadOverrides must fail this)',
    async () => {
      const updatedRows: NodeManifestSummary[] = [
        ...FIXTURE,
        {
          id: 'archived_reader',
          displayName: 'Archived reader',
          description: 'A dropped-in user override',
          category: 'state',
          archetype: 'read',
          callable: true,
        },
      ];

      let catalogCalls = 0;
      const catalogFn = vi.fn(async () => (catalogCalls++ === 0 ? FIXTURE : updatedRows));

      let overrideCalls = 0;
      const listUserOverridesFn = vi.fn(async () =>
        overrideCalls++ === 0
          ? [
              {
                filename: 'archived_reader.yaml',
                path: '/data/agent_graph/nodes/archived_reader.yaml',
                status: 'error' as const,
                error: 'yaml: line 2: found character that cannot start any token',
              },
            ]
          : [
              {
                filename: 'archived_reader.yaml',
                path: '/data/agent_graph/nodes/archived_reader.yaml',
                id: 'archived_reader',
                status: 'ok' as const,
              },
            ],
      );

      const reloadOverridesFn = vi.fn(async () => ({
        added: ['archived_reader'],
        removed: [],
        modified: [],
      }));

      const client = createFakeHarnessClient({
        nodes: {
          catalog: catalogFn,
          get: async (id) => ({
            summary: { id, callable: false },
            chain: [id],
            attrs: [],
            ports: {},
            provenance: [],
          }),
          reloadOverrides: reloadOverridesFn,
          listUserOverrides: listUserOverridesFn,
          doctor: async () => ({
            shippedCount: 0,
            userOverrideCount: 1,
            archetypeCount: 0,
            callableCount: 0,
            aliasCount: 0,
            hotReloadEnabled: false,
          }),
        },
      });
      const wrapper = mount(NodePalette, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();

      // Drop an invalid override file, click Doctor: the parse error shows.
      await wrapper.get('[data-testid="palette-doctor"]').trigger('click');
      await flushPromises();
      expect(wrapper.get('[data-testid="override-status-archived_reader.yaml"]').text()).toBe(
        'error',
      );
      expect(wrapper.find('[data-testid="palette-kind-archived_reader"]').exists()).toBe(false);

      // Fix the file, click Reload: the real re-scan + atomic catalog swap
      // runs, and the kind appears in the palette without a restart.
      await wrapper.get('[data-testid="palette-reload"]').trigger('click');
      await flushPromises();

      expect(reloadOverridesFn).toHaveBeenCalledTimes(1);
      expect(catalogFn).toHaveBeenCalledTimes(2);
      expect(wrapper.find('[data-testid="palette-kind-archived_reader"]').exists()).toBe(true);
      expect(wrapper.get('[data-testid="override-status-archived_reader.yaml"]').text()).toBe(
        'ok',
      );
      expect(wrapper.get('[data-testid="reload-diff-added"]').text()).toContain('added');
    },
  );
});
