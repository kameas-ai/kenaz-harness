/**
 * ProvenanceDrawer tests — memory-inspection-ui-01KX5R8E WP08.
 */
import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ProvenanceDrawer from '@/views/memory/ProvenanceDrawer.vue';
import type { ChunkProvenance } from '@/lib/types';

function makeProvenance(over: Partial<ChunkProvenance> = {}): ChunkProvenance {
  return {
    chunkId: 'mem-abc123',
    sourceTurn: 'user:msg-1',
    hookBoundary: 'post-llm',
    kind: 'raw',
    scopePath: 'session',
    pinned: false,
    retrievalCount: 5,
    citationCount: 2,
    promotionScore: 11.0,
    embedderKind: 'openai',
    embedDimensions: 1536,
    createdAt: '2026-05-01T10:00:00Z',
    ...over,
  };
}

function mountDrawer(props: Partial<InstanceType<typeof ProvenanceDrawer>['$props']> = {}) {
  const prov = makeProvenance();
  return mount(ProvenanceDrawer, {
    props: {
      provenance: prov,
      loading: false,
      error: null,
      ...props,
    },
  });
}

describe('ProvenanceDrawer', () => {
  it('renders all provenance fields', async () => {
    const wrapper = mountDrawer();
    await flushPromises();

    // Chunk ID
    expect(wrapper.find('[data-testid="provenance-chunk-id"]').text()).toContain('mem-abc123');
    // Source turn
    expect(wrapper.find('[data-testid="provenance-source-turn"]').text()).toContain('user:msg-1');
    // Hook boundary
    expect(wrapper.find('[data-testid="provenance-hook-boundary"]').text()).toContain('post-llm');
    // Kind
    expect(wrapper.find('[data-testid="provenance-kind"]').text()).toContain('raw');
    // Scope path
    expect(wrapper.find('[data-testid="provenance-scope-path"]').text()).toContain('session');
    // Counts
    expect(wrapper.find('[data-testid="provenance-retrieval-count"]').text()).toContain('5');
    expect(wrapper.find('[data-testid="provenance-citation-count"]').text()).toContain('2');
    expect(wrapper.find('[data-testid="provenance-promotion-score"]').text()).toContain('11.0');
    // Embedder
    expect(wrapper.find('[data-testid="provenance-embedder"]').text()).toContain('openai');
    expect(wrapper.find('[data-testid="provenance-embedder"]').text()).toContain('1536');
    // Created at (rendered as locale string or raw)
    expect(wrapper.find('[data-testid="provenance-created-at"]').text()).not.toBe('');
  });

  it('emits close when close button clicked', async () => {
    const wrapper = mountDrawer();
    await wrapper.find('[data-testid="provenance-drawer-close"]').trigger('click');
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  it('emits close when backdrop clicked', async () => {
    const wrapper = mountDrawer();
    await wrapper.find('[data-testid="provenance-drawer-backdrop"]').trigger('click');
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  it('emits close on Escape key', async () => {
    const wrapper = mountDrawer();
    // Simulate Escape via the document listener
    const event = new KeyboardEvent('keydown', { key: 'Escape' });
    document.dispatchEvent(event);
    await flushPromises();
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  it('shows loading state when loading=true', async () => {
    const wrapper = mountDrawer({
      provenance: makeProvenance(),
      loading: true,
    });
    expect(wrapper.find('[data-testid="provenance-drawer-loading"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="provenance-drawer-content"]').exists()).toBe(false);
  });

  it('shows error state when error is provided', async () => {
    const wrapper = mountDrawer({
      provenance: makeProvenance(),
      error: 'chunk not found',
    });
    expect(wrapper.find('[data-testid="provenance-drawer-error"]').text()).toContain('chunk not found');
    expect(wrapper.find('[data-testid="provenance-drawer-content"]').exists()).toBe(false);
  });

  it('renders scope path chain for multi-step scopes', async () => {
    const wrapper = mountDrawer({
      provenance: makeProvenance({ scopePath: 'session → project → global' }),
    });
    const scopeText = wrapper.find('[data-testid="provenance-scope-path"]').text();
    expect(scopeText).toContain('session');
    expect(scopeText).toContain('project');
    expect(scopeText).toContain('global');
  });

  it('shows pinned indicator when pinned=true', async () => {
    const wrapper = mountDrawer({ provenance: makeProvenance({ pinned: true }) });
    expect(wrapper.find('[data-testid="provenance-pinned"]').text()).toContain('Yes');
  });

  it('hides optional fields when absent', async () => {
    const wrapper = mountDrawer({
      provenance: makeProvenance({
        sourceTurn: undefined,
        hookBoundary: undefined,
        kind: undefined,
        embedderKind: undefined,
      }),
    });
    expect(wrapper.find('[data-testid="provenance-source-turn"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="provenance-hook-boundary"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="provenance-kind"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="provenance-embedder"]').exists()).toBe(false);
  });
});
