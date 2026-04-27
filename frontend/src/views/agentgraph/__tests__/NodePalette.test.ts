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
import type { NodeManifestSummary } from '@/lib/types';

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

function mountPalette(rows: NodeManifestSummary[] = FIXTURE) {
  const catalog = vi.fn(async () => rows);
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
      reloadOverrides: async () => ({ added: [], removed: [], modified: [] }),
      listUserOverrides: async () => [],
      doctor: async () => ({
        shippedCount: 0,
        userOverrideCount: 0,
        archetypeCount: 0,
        callableCount: 0,
        aliasCount: 0,
        hotReloadEnabled: false,
      }),
    },
  });
  const wrapper = mount(NodePalette, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
  return { wrapper, catalog };
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
      'application/x-kaneaz-node-kind',
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
});
