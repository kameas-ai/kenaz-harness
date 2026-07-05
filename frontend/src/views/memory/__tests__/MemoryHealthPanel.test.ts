import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MemoryHealthPanel from '@/views/memory/MemoryHealthPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MemoryHealthSnapshot } from '@/lib/types';

const BASE_SNAPSHOT: MemoryHealthSnapshot = {
  counts: {
    total: 100,
    raw: 64,
    narrative: 0,
    longTermPromoted: 36,
    embedded: 97,
    unembedded: 3,
  },
  activity: {
    captured: 12,
    pruned: 2,
    promoted: 1,
  },
  embedder: {
    kind: 'openai',
    model: 'text-embedding-3-small',
    dimensions: 1536,
  },
  capturedAt: '2026-05-05T12:00:00Z',
};

interface MountOpts {
  snapshot?: MemoryHealthSnapshot;
  healthSnapshotImpl?: () => Promise<MemoryHealthSnapshot>;
  testEmbedderImpl?: () => Promise<number>;
}

function mountWith(opts: MountOpts = {}) {
  const healthSnapshot = vi.fn(
    opts.healthSnapshotImpl ?? (async () => opts.snapshot ?? BASE_SNAPSHOT),
  );
  const testEmbedder = vi.fn(opts.testEmbedderImpl ?? (async () => 1536));
  const client = createFakeHarnessClient({
    memory: {
      listChunks: async () => [],
      rememberMessage: async () => 'id',
      promoteScope: async () => 'id',
      forget: async () => undefined,
      pin: async () => undefined,
      journalTail: async () => [],
      prunePreview: async () => ({
        verdicts: [],
        stats: {
          startedAt: '',
          durationMs: 0,
          kept: 0,
          dropped: 0,
          collapsed: 0,
          pinned: 0,
        },
      }),
      runPruneNow: async () => ({
        startedAt: '',
        durationMs: 0,
        kept: 0,
        dropped: 0,
        collapsed: 0,
        pinned: 0,
      }),
      healthSnapshot,
      testEmbedder,
      captureRate: async () => ({
        chunksPerMinute: 0,
        embedderHealth: 'ok',
        lastErrorAt: null,
        recentErrorCount: 0,
      }),
    } as any,
  });
  const wrapper = mount(MemoryHealthPanel, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
  return { wrapper, healthSnapshot, testEmbedder };
}

describe('MemoryHealthPanel (§2.4)', () => {
  it('renders the panel after loading the snapshot', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-health-panel"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="memory-health-counts"]').exists()).toBe(true);
  });

  it('displays total chunk count', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const row = wrapper.find('[data-testid="health-count-total"]');
    expect(row.exists()).toBe(true);
    expect(row.text()).toContain('100');
  });

  it('displays raw, narrative, and long-term promoted counts', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="health-count-raw"]').text()).toContain('64');
    expect(wrapper.find('[data-testid="health-count-narrative"]').text()).toContain('0');
    expect(wrapper.find('[data-testid="health-count-promoted"]').text()).toContain('36');
  });

  it('displays embedded and unembedded counts', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="health-count-embedded"]').text()).toContain('97');
    expect(wrapper.find('[data-testid="health-count-unembedded"]').text()).toContain('3');
  });

  it('displays last-7-day activity', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="health-activity-captured"]').text()).toContain('+12');
    expect(wrapper.find('[data-testid="health-activity-pruned"]').text()).toContain('-2');
    expect(wrapper.find('[data-testid="health-activity-promoted"]').text()).toContain('+1');
  });

  it('displays embedder info', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    const embedder = wrapper.find('[data-testid="memory-health-embedder"]');
    expect(embedder.text()).toContain('openai');
    expect(embedder.text()).toContain('text-embedding-3-small');
    expect(embedder.text()).toContain('1536');
  });

  it('calls healthSnapshot on mount', async () => {
    const { healthSnapshot } = mountWith();
    await flushPromises();
    expect(healthSnapshot).toHaveBeenCalledTimes(1);
  });

  it('refreshes when the Refresh button is clicked', async () => {
    const { wrapper, healthSnapshot } = mountWith();
    await flushPromises();
    healthSnapshot.mockClear();
    await wrapper.find('[data-testid="memory-health-refresh"]').trigger('click');
    await flushPromises();
    expect(healthSnapshot).toHaveBeenCalledTimes(1);
  });

  it('shows error state when healthSnapshot rejects', async () => {
    const { wrapper } = mountWith({
      healthSnapshotImpl: async () => {
        throw new Error('server error');
      },
    });
    await flushPromises();
    const err = wrapper.find('[data-testid="memory-health-error"]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('server error');
  });

  it('calls testEmbedder and shows result on Test embedder click', async () => {
    const { wrapper, testEmbedder } = mountWith();
    await flushPromises();
    await wrapper.find('[data-testid="memory-health-test-embedder"]').trigger('click');
    await flushPromises();
    expect(testEmbedder).toHaveBeenCalledTimes(1);
    const result = wrapper.find('[data-testid="memory-embedder-test-result"]');
    expect(result.exists()).toBe(true);
    expect(result.text()).toContain('1536');
  });

  it('shows "error" in test result when testEmbedder rejects', async () => {
    const { wrapper } = mountWith({
      testEmbedderImpl: async () => {
        throw new Error('no key');
      },
    });
    await flushPromises();
    await wrapper.find('[data-testid="memory-health-test-embedder"]').trigger('click');
    await flushPromises();
    const result = wrapper.find('[data-testid="memory-embedder-test-result"]');
    expect(result.text()).toContain('error');
    expect(result.text()).toContain('no key');
  });

  it('opens the re-embed stub modal when clicking Re-embed button (if unembedded > 0)', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    // BASE_SNAPSHOT has unembedded=3, so the button should be enabled.
    const btn = wrapper.find('[data-testid="memory-health-reembed"]');
    expect(btn.attributes('disabled')).toBeUndefined();
    await btn.trigger('click');
    expect(wrapper.find('[data-testid="memory-reembed-modal"]').exists()).toBe(true);
  });

  it('closes the re-embed modal on Close click', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    await wrapper.find('[data-testid="memory-health-reembed"]').trigger('click');
    expect(wrapper.find('[data-testid="memory-reembed-modal"]').exists()).toBe(true);
    await wrapper.find('[data-testid="memory-reembed-close"]').trigger('click');
    expect(wrapper.find('[data-testid="memory-reembed-modal"]').exists()).toBe(false);
  });

  it('disables Re-embed button when unembedded count is 0', async () => {
    const { wrapper } = mountWith({
      snapshot: {
        ...BASE_SNAPSHOT,
        counts: { ...BASE_SNAPSHOT.counts, unembedded: 0 },
      },
    });
    await flushPromises();
    const btn = wrapper.find('[data-testid="memory-health-reembed"]');
    expect(btn.attributes('disabled')).toBeDefined();
  });

  // ── v0.5.x audit: auto-refresh 30s timer ─────────────────────────────

  it('auto-refreshes after 30 s via setInterval (v0.5.x audit)', async () => {
    vi.useFakeTimers();
    try {
      const { wrapper, healthSnapshot } = mountWith();
      await flushPromises();
      // Called once on mount.
      expect(healthSnapshot).toHaveBeenCalledTimes(1);

      // Advance 30 s — the interval should fire.
      vi.advanceTimersByTime(30_000);
      await flushPromises();
      expect(healthSnapshot).toHaveBeenCalledTimes(2);

      // Advance another 30 s — fires again.
      vi.advanceTimersByTime(30_000);
      await flushPromises();
      expect(healthSnapshot).toHaveBeenCalledTimes(3);

      wrapper.unmount();
    } finally {
      vi.useRealTimers();
    }
  });

  it('stops auto-refreshing after unmount (no timer leak)', async () => {
    vi.useFakeTimers();
    try {
      const { wrapper, healthSnapshot } = mountWith();
      await flushPromises();
      expect(healthSnapshot).toHaveBeenCalledTimes(1);

      wrapper.unmount();
      healthSnapshot.mockClear();

      // Advance 60 s after unmount — no additional calls should fire.
      vi.advanceTimersByTime(60_000);
      await flushPromises();
      expect(healthSnapshot).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });
});
