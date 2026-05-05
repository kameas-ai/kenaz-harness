import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import PrunePreviewModal from '@/views/memory/PrunePreviewModal.vue';
import type { MemoryPrunePreview } from '@/lib/types';

function makePreview(
  over: Partial<MemoryPrunePreview> = {},
): MemoryPrunePreview {
  return {
    verdicts: [],
    stats: {
      startedAt: '2026-05-05T00:00:00Z',
      durationMs: 120,
      kept: 10,
      dropped: 2,
      collapsed: 1,
      pinned: 3,
    },
    rows: [],
    ...over,
  };
}

function mountModal(
  preview: MemoryPrunePreview,
  running = false,
) {
  const onConfirm = vi.fn();
  const onCancel = vi.fn();
  const wrapper = mount(PrunePreviewModal, {
    props: { preview, running },
  });
  wrapper.vm.$emit = vi.fn((event: string) => {
    if (event === 'confirm') onConfirm();
    if (event === 'cancel') onCancel();
  });
  return { wrapper, onConfirm, onCancel };
}

describe('PrunePreviewModal (§2.5)', () => {
  it('renders the modal with stats', () => {
    const preview = makePreview();
    const { wrapper } = mountModal(preview);
    expect(wrapper.find('[data-testid="prune-preview-modal"]').exists()).toBe(true);
    expect(wrapper.find('[data-testid="prune-preview-kept"]').text()).toContain('10');
    expect(wrapper.find('[data-testid="prune-preview-dropped"]').text()).toContain('2');
  });

  it('shows empty state when no rows would be pruned', () => {
    const preview = makePreview({
      rows: [],
      verdicts: [],
      stats: { ...makePreview().stats, dropped: 0, collapsed: 0 },
    });
    const { wrapper } = mountModal(preview);
    expect(wrapper.find('[data-testid="prune-preview-empty"]').exists()).toBe(true);
  });

  it('renders rows from the preview.rows field (§2.5)', () => {
    const preview = makePreview({
      rows: [
        {
          id: 'mem-001',
          snippet: 'the user prefers vim',
          reason: '0 retrievals in 45 days',
          action: 'drop',
        },
        {
          id: 'mem-002',
          snippet: 'duplicate content here',
          reason: 'duplicate of mem-001',
          action: 'collapse',
        },
      ],
    });
    const { wrapper } = mountModal(preview);
    const rows = wrapper.find('[data-testid="prune-preview-rows"]');
    expect(rows.exists()).toBe(true);
    expect(rows.text()).toContain('mem-001');
    expect(rows.text()).toContain('the user prefers vim');
    expect(rows.text()).toContain('0 retrievals in 45 days');
    expect(rows.text()).toContain('mem-002');
    expect(rows.text()).toContain('collapse');
  });

  it('falls back to synthesising rows from verdicts when rows is empty', () => {
    const preview = makePreview({
      rows: [],
      verdicts: [
        {
          id: 'mem-fallback',
          action: 'drop',
          reason: 'stale',
          keepScore: 0.2,
        },
        {
          id: 'mem-kept',
          action: 'keep',
          reason: 'ok',
          keepScore: 0.9,
        },
      ],
    });
    const { wrapper } = mountModal(preview);
    const rows = wrapper.find('[data-testid="prune-preview-rows"]');
    // Should show mem-fallback (drop) but NOT mem-kept (keep)
    expect(rows.text()).toContain('mem-fallback');
    expect(rows.text()).not.toContain('mem-kept');
  });

  it('emits confirm when Run is clicked', async () => {
    const preview = makePreview();
    const { wrapper } = mountModal(preview);
    await wrapper.find('[data-testid="prune-preview-run"]').trigger('click');
    // The component emits 'confirm' via $emit — check the emitted events.
    expect(wrapper.emitted('confirm')).toBeTruthy();
  });

  it('emits cancel when Cancel is clicked', async () => {
    const preview = makePreview();
    const { wrapper } = mountModal(preview);
    await wrapper.find('[data-testid="prune-preview-cancel"]').trigger('click');
    expect(wrapper.emitted('cancel')).toBeTruthy();
  });

  it('disables the Run button while running=true', () => {
    const preview = makePreview();
    const { wrapper } = mountModal(preview, true);
    const runBtn = wrapper.find('[data-testid="prune-preview-run"]');
    expect(runBtn.attributes('disabled')).toBeDefined();
    expect(runBtn.text()).toBe('Pruning…');
  });

  it('applies scroll container for > 50 rows', () => {
    const rows = Array.from({ length: 55 }, (_, i) => ({
      id: `mem-${i}`,
      snippet: `content ${i}`,
      reason: 'stale',
      action: 'drop' as const,
    }));
    const preview = makePreview({ rows });
    const { wrapper } = mountModal(preview);
    const container = wrapper.find('[data-testid="prune-preview-rows"]');
    expect(container.classes()).toContain('max-h-64');
  });

  it('does not apply scroll container for <= 50 rows', () => {
    const rows = Array.from({ length: 20 }, (_, i) => ({
      id: `mem-${i}`,
      snippet: `content ${i}`,
      reason: 'aged out',
      action: 'drop' as const,
    }));
    const preview = makePreview({ rows });
    const { wrapper } = mountModal(preview);
    const container = wrapper.find('[data-testid="prune-preview-rows"]');
    expect(container.classes()).not.toContain('max-h-64');
  });

  it('shows the individual row testid for each row', async () => {
    await flushPromises();
    const preview = makePreview({
      rows: [
        { id: 'row-abc', snippet: 'abc', reason: 'stale', action: 'drop' },
      ],
    });
    const { wrapper } = mountModal(preview);
    expect(wrapper.find('[data-testid="prune-row-row-abc"]').exists()).toBe(true);
  });
});
