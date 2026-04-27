import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import HookJournalView from '@/views/memory/HookJournalView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MemoryJournalEntry } from '@/lib/types';

function makeEntry(over: Partial<MemoryJournalEntry>): MemoryJournalEntry {
  return {
    seq: 1,
    boundary: 'post-llm',
    scope: 'session',
    written: true,
    deduped: false,
    skipped: false,
    at: '2026-04-25T12:00:00Z',
    ...over,
  };
}

function mountWith(entries: MemoryJournalEntry[]) {
  const journalTail = vi.fn(async () => entries);
  const client = createFakeHarnessClient({
    memory: {
      listChunks: async () => [],
      rememberMessage: async () => 'mem',
      promoteScope: async () => 'mem',
      forget: async () => undefined,
      pin: async () => undefined,
      journalTail,
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
    },
  });
  const wrapper = mount(HookJournalView, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
    },
  });
  return { wrapper, journalTail };
}

describe('HookJournalView', () => {
  it('renders empty state when no entries', async () => {
    const { wrapper } = mountWith([]);
    await flushPromises();
    expect(wrapper.find('[data-testid="hook-journal-empty"]').exists()).toBe(
      true,
    );
  });

  it('renders journal rows when entries are returned', async () => {
    const entries = [
      makeEntry({ seq: 1, boundary: 'post-llm', written: true }),
      makeEntry({
        seq: 2,
        boundary: 'post-tool',
        written: false,
        deduped: true,
      }),
      makeEntry({
        seq: 3,
        boundary: 'on-checkpoint',
        skipped: true,
        skipReason: 'redaction filtered',
      }),
    ];
    const { wrapper } = mountWith(entries);
    await flushPromises();
    expect(wrapper.find('[data-testid="hook-journal-table"]').exists()).toBe(
      true,
    );
    expect(wrapper.find('[data-testid="hook-journal-row-1"]').text()).toContain(
      'post-llm',
    );
    expect(wrapper.find('[data-testid="hook-journal-row-2"]').text()).toContain(
      'deduped',
    );
    expect(wrapper.find('[data-testid="hook-journal-row-3"]').text()).toContain(
      'redaction filtered',
    );
  });

  it('refresh button re-fetches the journal', async () => {
    const { wrapper, journalTail } = mountWith([]);
    await flushPromises();
    await wrapper.find('[data-testid="hook-journal-refresh"]').trigger('click');
    await flushPromises();
    // Initial mount + click = 2 calls.
    expect(journalTail).toHaveBeenCalledTimes(2);
  });

  it('scope filter narrows the request', async () => {
    const { wrapper, journalTail } = mountWith([]);
    await flushPromises();
    await wrapper
      .find('[data-testid="hook-journal-scope-project"]')
      .trigger('click');
    await flushPromises();
    expect(journalTail).toHaveBeenLastCalledWith('project', 0, 200);
  });

  it('text filter hides non-matching rows', async () => {
    const entries = [
      makeEntry({ seq: 1, boundary: 'post-llm' }),
      makeEntry({ seq: 2, boundary: 'post-tool' }),
    ];
    const { wrapper } = mountWith(entries);
    await flushPromises();
    await wrapper
      .find('[data-testid="hook-journal-filter"]')
      .setValue('tool');
    expect(wrapper.find('[data-testid="hook-journal-row-1"]').exists()).toBe(
      false,
    );
    expect(wrapper.find('[data-testid="hook-journal-row-2"]').exists()).toBe(
      true,
    );
  });
});
