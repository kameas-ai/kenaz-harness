import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MemoryView from '@/views/memory/MemoryView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MemoryChunk } from '@/lib/types';

function mountWith(chunks: MemoryChunk[]) {
  const forget = vi.fn(async () => undefined);
  const listChunks = vi.fn(async () => chunks);
  const client = createFakeHarnessClient({
    memory: {
      listChunks,
      forget,
      rememberMessage: vi.fn(async () => 'mem-id'),
    },
  });
  const wrapper = mount(MemoryView, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        CanvasHead: { template: '<div class="canvas-head"><slot /></div>' },
      },
    },
  });
  return { wrapper, forget, listChunks };
}

describe('MemoryView', () => {
  it('renders the empty state when no memories are stored', async () => {
    const { wrapper, listChunks } = mountWith([]);
    await flushPromises();
    expect(listChunks).toHaveBeenCalledTimes(1);
    expect(wrapper.html()).toContain('No memories saved yet');
    expect(
      wrapper.find('[data-testid="memory-empty-state"]').exists(),
    ).toBe(true);
  });

  it('renders one row per chunk with a Forget button', async () => {
    const chunks: MemoryChunk[] = [
      {
        id: 'chunk-1',
        content: 'the user prefers vim keybindings',
        createdAt: '2026-04-25T12:00:00Z',
        sessionId: 'sess-A',
        sourceTurn: 'user',
      },
      {
        id: 'chunk-2',
        content: 'the user lives in Brooklyn',
        createdAt: '2026-04-24T08:30:00Z',
      },
    ];
    const { wrapper } = mountWith(chunks);
    await flushPromises();
    expect(wrapper.html()).toContain('the user prefers vim keybindings');
    expect(wrapper.html()).toContain('the user lives in Brooklyn');
    expect(
      wrapper.find('[data-testid="memory-forget-chunk-1"]').exists(),
    ).toBe(true);
    expect(
      wrapper.find('[data-testid="memory-forget-chunk-2"]').exists(),
    ).toBe(true);
  });

  it('truncates long content for the row preview', async () => {
    const long = 'x'.repeat(500);
    const { wrapper } = mountWith([
      {
        id: 'chunk-long',
        content: long,
        createdAt: '2026-04-25T12:00:00Z',
      },
    ]);
    await flushPromises();
    const text = wrapper.text();
    // Truncation marker; full string should not be rendered.
    expect(text).toContain('…');
    expect(text).not.toContain('x'.repeat(300));
  });

  it('calls forget(id) and refreshes when the Forget button fires', async () => {
    const chunks: MemoryChunk[] = [
      {
        id: 'chunk-1',
        content: 'pin me',
        createdAt: '2026-04-25T12:00:00Z',
      },
    ];
    const { wrapper, forget, listChunks } = mountWith(chunks);
    await flushPromises();
    listChunks.mockClear();
    await wrapper.find('[data-testid="memory-forget-chunk-1"]').trigger('click');
    await flushPromises();
    expect(forget).toHaveBeenCalledWith('chunk-1');
    // Refresh should have been triggered after the forget.
    expect(listChunks).toHaveBeenCalledTimes(1);
  });
});
