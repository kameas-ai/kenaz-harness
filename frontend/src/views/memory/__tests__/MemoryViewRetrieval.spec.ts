/**
 * MemoryView — Retrieval Inspector tab + Embedding Probe tests.
 * memory-inspection-ui-01KX5R8E WP07.
 */
import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MemoryView from '@/views/memory/MemoryView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { RetrievalReport, ScoredChunk, MemoryChunk } from '@/lib/types';

function makeReport(over: Partial<RetrievalReport> = {}): RetrievalReport {
  return {
    sessionId: 'sess-test',
    query: 'hello world',
    threshold: 0.5,
    at: '2026-05-01T10:00:00Z',
    results: [
      {
        chunk: {
          id: 'c1',
          content: 'injected chunk',
          scopeKind: 'session',
          scopeId: 'sess-test',
          contentHash: '',
          createdAt: '',
        },
        similarity: 0.84,
        injected: true,
      },
      {
        chunk: {
          id: 'c2',
          content: 'below threshold chunk',
          scopeKind: 'session',
          scopeId: 'sess-test',
          contentHash: '',
          createdAt: '',
        },
        similarity: 0.32,
        injected: false,
      },
    ],
    ...over,
  };
}

function makeProbeResult(): ScoredChunk[] {
  return [
    {
      chunk: {
        id: 'p1',
        content: 'probe result alpha',
        scopeKind: 'session',
        scopeId: '',
        contentHash: '',
        createdAt: '',
      },
      similarity: 0.91,
      injected: false,
    },
  ];
}

function mountViewWithRetrieval(opts: {
  lastRetrieval?: RetrievalReport;
  embeddingProbe?: ScoredChunk[];
} = {}) {
  const lastRetrievalFn = vi.fn(async () => opts.lastRetrieval ?? { sessionId: '', query: '', results: [], threshold: 0.5, at: '' });
  const embeddingProbeFn = vi.fn(async () => opts.embeddingProbe ?? []);

  const client = createFakeHarnessClient({
    memory: {
      listChunks: async () => [],
      forget: async () => undefined,
      rememberMessage: async () => 'id',
      promoteScope: async () => 'id',
      pin: async () => undefined,
      journalTail: async () => [],
      prunePreview: async () => ({ verdicts: [], stats: { startedAt: '', durationMs: 0, kept: 0, dropped: 0, collapsed: 0, pinned: 0 } }),
      runPruneNow: async () => ({ startedAt: '', durationMs: 0, kept: 0, dropped: 0, collapsed: 0, pinned: 0 }),
      healthSnapshot: async () => ({ counts: { total: 0, raw: 0, narrative: 0, longTermPromoted: 0, embedded: 0, unembedded: 0 }, activity: { captured: 0, pruned: 0, promoted: 0 }, embedder: { kind: 'noop', model: '', dimensions: 0 }, capturedAt: '' }),
      testEmbedder: async () => 0,
      lastRetrieval: lastRetrievalFn,
      embeddingProbe: embeddingProbeFn,
    } as any,
  });

  const wrapper = mount(MemoryView, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        CanvasHead: { template: '<div class="canvas-head"><slot /></div>' },
        ProvenanceDrawer: true,
        MemoryHealthPanel: { template: '<div data-testid="memory-health-tab-panel"></div>' },
        PrunePreviewModal: true,
      },
    },
  });

  return { wrapper, lastRetrievalFn, embeddingProbeFn };
}

describe('MemoryView — Retrieval inspector tab', () => {
  it('renders Retrieval inspector tab button', async () => {
    const { wrapper } = mountViewWithRetrieval();
    await flushPromises();
    const tab = wrapper.find('[data-testid="memory-main-tab-retrieval"]');
    expect(tab.exists()).toBe(true);
    expect(tab.text()).toContain('Retrieval inspector');
  });

  it('shows retrieval panel when tab is clicked', async () => {
    const { wrapper } = mountViewWithRetrieval();
    await flushPromises();
    await wrapper.find('[data-testid="memory-main-tab-retrieval"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-retrieval-tab-panel"]').exists()).toBe(true);
  });

  it('shows empty state before loading', async () => {
    const { wrapper } = mountViewWithRetrieval({ lastRetrieval: { sessionId: '', query: '', results: [], threshold: 0.5, at: '' } });
    await flushPromises();
    await wrapper.find('[data-testid="memory-main-tab-retrieval"]').trigger('click');
    await flushPromises();
    // Panel exists; no report loaded yet (button not clicked)
    expect(wrapper.find('[data-testid="memory-retrieval-load"]').exists()).toBe(true);
  });

  it('loads retrieval report on Load button click', async () => {
    const report = makeReport();
    const { wrapper, lastRetrievalFn } = mountViewWithRetrieval({ lastRetrieval: report });
    await flushPromises();
    await wrapper.find('[data-testid="memory-main-tab-retrieval"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-testid="memory-retrieval-load"]').trigger('click');
    await flushPromises();

    expect(lastRetrievalFn).toHaveBeenCalled();
    const report_div = wrapper.find('[data-testid="memory-retrieval-report"]');
    expect(report_div.exists()).toBe(true);
    expect(wrapper.find('[data-testid="memory-retrieval-query"]').text()).toBe('hello world');
  });

  it('renders injected chunks with bold similarity', async () => {
    const report = makeReport();
    const { wrapper } = mountViewWithRetrieval({ lastRetrieval: report });
    await flushPromises();
    await wrapper.find('[data-testid="memory-main-tab-retrieval"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-testid="memory-retrieval-load"]').trigger('click');
    await flushPromises();

    const injected = wrapper.find('[data-testid="memory-retrieval-injected"]');
    expect(injected.exists()).toBe(true);
    expect(injected.text()).toContain('0.84');
    expect(injected.text()).toContain('injected chunk');
  });

  it('renders below-threshold chunks separately', async () => {
    const report = makeReport();
    const { wrapper } = mountViewWithRetrieval({ lastRetrieval: report });
    await flushPromises();
    await wrapper.find('[data-testid="memory-main-tab-retrieval"]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-testid="memory-retrieval-load"]').trigger('click');
    await flushPromises();

    const below = wrapper.find('[data-testid="memory-retrieval-below"]');
    expect(below.exists()).toBe(true);
    expect(below.text()).toContain('0.32');
    expect(below.text()).toContain('below threshold chunk');
  });
});

describe('MemoryView — Embedding Probe', () => {
  it('renders probe input and button', async () => {
    const { wrapper } = mountViewWithRetrieval();
    await flushPromises();
    await wrapper.find('[data-testid="memory-main-tab-retrieval"]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-probe-input"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="memory-probe-run"]').exists()).toBe(true);
  });

  it('calls embeddingProbe and renders results', async () => {
    const probeResult = makeProbeResult();
    const { wrapper, embeddingProbeFn } = mountViewWithRetrieval({ embeddingProbe: probeResult });
    await flushPromises();
    await wrapper.find('[data-testid="memory-main-tab-retrieval"]').trigger('click');
    await flushPromises();

    // Type a query
    await wrapper.find('[data-testid="memory-probe-input"]').setValue('what is alpha');
    await wrapper.find('[data-testid="memory-probe-run"]').trigger('click');
    await flushPromises();

    expect(embeddingProbeFn).toHaveBeenCalledWith('what is alpha', 10);
    const results = wrapper.find('[data-testid="memory-probe-results"]');
    expect(results.exists()).toBe(true);
    expect(results.text()).toContain('0.91');
    expect(results.text()).toContain('probe result alpha');
  });

  it('does not call probe when query is empty', async () => {
    const { wrapper, embeddingProbeFn } = mountViewWithRetrieval();
    await flushPromises();
    await wrapper.find('[data-testid="memory-main-tab-retrieval"]').trigger('click');
    await flushPromises();

    // Do not type anything, just click
    await wrapper.find('[data-testid="memory-probe-run"]').trigger('click');
    await flushPromises();

    expect(embeddingProbeFn).not.toHaveBeenCalled();
  });

  it('shows no-results state when probe returns empty', async () => {
    const { wrapper } = mountViewWithRetrieval({ embeddingProbe: [] });
    await flushPromises();
    await wrapper.find('[data-testid="memory-main-tab-retrieval"]').trigger('click');
    await flushPromises();

    await wrapper.find('[data-testid="memory-probe-input"]').setValue('anything');
    await wrapper.find('[data-testid="memory-probe-run"]').trigger('click');
    await flushPromises();

    expect(wrapper.find('[data-testid="memory-probe-no-results"]').exists()).toBe(true);
  });
});
