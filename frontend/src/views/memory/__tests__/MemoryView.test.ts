import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import MemoryView from '@/views/memory/MemoryView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { MemoryChunk, Session } from '@/lib/types';

interface MountOpts {
  chunks?: MemoryChunk[];
  // Per-call results so we can assert the filter argument forwarded
  // to listChunks across pill clicks.
  listImpl?: (filter: { scopeKind?: string }) => MemoryChunk[];
  promoteImpl?: () => Promise<string>;
  sessions?: Record<string, Session>;
}

function mountWith(opts: MountOpts = {}) {
  const chunks = opts.chunks ?? [];
  const forget = vi.fn(async () => undefined);
  const listChunks = vi.fn(
    async (filter: { scopeKind?: string } = {}) => {
      if (opts.listImpl) return opts.listImpl(filter);
      if (filter.scopeKind) {
        return chunks.filter((c) => c.scopeKind === filter.scopeKind);
      }
      return chunks;
    },
  );
  const rememberMessage = vi.fn(async () => 'mem-id');
  const promoteScope = vi.fn(opts.promoteImpl ?? (async () => 'new-mem-id'));
  const sessionGet = vi.fn(async (id: string) => {
    const seed = opts.sessions?.[id];
    if (seed) return seed;
    return {
      id,
      name: id,
      createdAt: '',
      updatedAt: '',
    };
  });
  const client = createFakeHarnessClient({
    memory: {
      listChunks,
      forget,
      rememberMessage,
      promoteScope,
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
      healthSnapshot: async () => ({
        counts: {
          total: 0,
          raw: 0,
          narrative: 0,
          longTermPromoted: 0,
          embedded: 0,
          unembedded: 0,
        },
        activity: { captured: 0, pruned: 0, promoted: 0 },
        embedder: { kind: 'noop', model: '', dimensions: 0 },
        capturedAt: '',
      }),
      testEmbedder: async () => 0,
    } as any,
    sessions: {
      list: async () => [],
      get: sessionGet,
      create: async (name: string) => ({
        id: 'new',
        name,
        createdAt: '',
        updatedAt: '',
      }),
      rename: async () => undefined,
      delete: async () => undefined,
      reorder: async () => undefined,
      startStream: async () => 'sub',
      stopStream: async () => undefined,
      listMessages: async () => [],
      appendMessage: async (id: string, role: string, content: string) => ({
        id: 'm',
        sessionId: id,
        role,
        content,
        createdAt: '',
      }),
      saveDraft: async () => undefined,
      loadDraft: async () => '',
      setSystemPrompt: async () => undefined,
      moveToProject: async () => undefined,
    } as any,
  });
  const wrapper = mount(MemoryView, {
    global: {
      provide: { [HarnessClientKey as symbol]: client },
      stubs: {
        CanvasHead: { template: '<div class="canvas-head"><slot /></div>' },
      },
    },
  });
  return {
    wrapper,
    forget,
    listChunks,
    promoteScope,
    sessionGet,
    rememberMessage,
  };
}

function makeChunk(over: Partial<MemoryChunk>): MemoryChunk {
  return {
    id: 'chunk',
    content: 'content',
    createdAt: '2026-04-25T12:00:00Z',
    scopeKind: 'session',
    scopeId: 'sess-A',
    ...over,
  };
}

describe('MemoryView', () => {
  it('renders the empty state when no memories are stored', async () => {
    const { wrapper, listChunks } = mountWith();
    await flushPromises();
    expect(listChunks).toHaveBeenCalledTimes(1);
    expect(wrapper.html()).toContain('No memories saved yet');
    expect(
      wrapper.find('[data-testid="memory-empty-state"]').exists(),
    ).toBe(true);
  });

  it('renders one row per chunk with a Forget button + scope badge', async () => {
    const chunks: MemoryChunk[] = [
      makeChunk({
        id: 'chunk-1',
        content: 'the user prefers vim keybindings',
        scopeKind: 'session',
        scopeId: 'sess-A',
        sessionId: 'sess-A',
        sourceTurn: 'user',
      }),
      makeChunk({
        id: 'chunk-2',
        content: 'the user lives in Brooklyn',
        scopeKind: 'global',
        scopeId: '',
      }),
    ];
    const { wrapper } = mountWith({ chunks });
    await flushPromises();
    expect(wrapper.html()).toContain('the user prefers vim keybindings');
    expect(wrapper.html()).toContain('the user lives in Brooklyn');
    expect(
      wrapper.find('[data-testid="memory-forget-chunk-1"]').exists(),
    ).toBe(true);
    expect(
      wrapper.find('[data-testid="memory-forget-chunk-2"]').exists(),
    ).toBe(true);
    expect(
      wrapper.find('[data-testid="memory-scope-badge-chunk-1"]').text(),
    ).toContain('session');
    expect(
      wrapper.find('[data-testid="memory-scope-badge-chunk-2"]').text(),
    ).toContain('global');
  });

  it('truncates long content for the row preview', async () => {
    const long = 'x'.repeat(500);
    const { wrapper } = mountWith({
      chunks: [makeChunk({ id: 'chunk-long', content: long })],
    });
    await flushPromises();
    const text = wrapper.text();
    expect(text).toContain('…');
    expect(text).not.toContain('x'.repeat(300));
  });

  it('calls forget(id) and refreshes when the Forget button fires', async () => {
    const chunks: MemoryChunk[] = [
      makeChunk({ id: 'chunk-1', content: 'pin me' }),
    ];
    const { wrapper, forget, listChunks } = mountWith({ chunks });
    await flushPromises();
    listChunks.mockClear();
    await wrapper.find('[data-testid="memory-forget-chunk-1"]').trigger('click');
    await flushPromises();
    expect(forget).toHaveBeenCalledWith('chunk-1');
    expect(listChunks).toHaveBeenCalledTimes(1);
  });

  // ── WP06 T005: scope filter pills ────────────────────────────────────

  it('renders the four scope filter pills', async () => {
    const { wrapper } = mountWith();
    await flushPromises();
    expect(wrapper.find('[data-testid="memory-scope-filter"]').exists()).toBe(
      true,
    );
    for (const id of ['all', 'global', 'project', 'session']) {
      expect(
        wrapper.find(`[data-testid="memory-scope-pill-${id}"]`).exists(),
      ).toBe(true);
    }
  });

  it('forwards the scope filter to listChunks when a pill is clicked', async () => {
    const { wrapper, listChunks } = mountWith({
      chunks: [
        makeChunk({ id: 'g1', scopeKind: 'global', scopeId: '' }),
        makeChunk({ id: 's1', scopeKind: 'session', scopeId: 'sess-A' }),
      ],
    });
    await flushPromises();
    listChunks.mockClear();
    await wrapper.find('[data-testid="memory-scope-pill-global"]').trigger('click');
    await flushPromises();
    expect(listChunks).toHaveBeenCalledTimes(1);
    expect(listChunks).toHaveBeenCalledWith({ scopeKind: 'global' });
  });

  it('passes an empty filter when "All" is selected', async () => {
    const { wrapper, listChunks } = mountWith({
      chunks: [makeChunk({ id: 'g1', scopeKind: 'global', scopeId: '' })],
    });
    await flushPromises();
    // Switch away from "all" first, then back.
    await wrapper.find('[data-testid="memory-scope-pill-session"]').trigger('click');
    await flushPromises();
    listChunks.mockClear();
    await wrapper.find('[data-testid="memory-scope-pill-all"]').trigger('click');
    await flushPromises();
    expect(listChunks).toHaveBeenCalledWith({});
  });

  // ── WP06 T005: promote action wiring ─────────────────────────────────

  it('opens the promote menu and lists the valid targets for a session chunk in a project', async () => {
    const chunks: MemoryChunk[] = [
      makeChunk({
        id: 'chunk-1',
        scopeKind: 'session',
        scopeId: 'sess-A',
        sessionId: 'sess-A',
        projectId: 'proj-1',
      }),
    ];
    const { wrapper } = mountWith({ chunks });
    await flushPromises();
    await wrapper.find('[data-testid="memory-promote-chunk-1"]').trigger('click');
    expect(
      wrapper.find('[data-testid="memory-promote-menu-chunk-1"]').exists(),
    ).toBe(true);
    expect(
      wrapper.find('[data-testid="memory-promote-chunk-1-project"]').exists(),
    ).toBe(true);
    expect(
      wrapper.find('[data-testid="memory-promote-chunk-1-global"]').exists(),
    ).toBe(true);
  });

  it('only offers global as a target when the session has no project', async () => {
    const chunks: MemoryChunk[] = [
      makeChunk({
        id: 'chunk-loose',
        scopeKind: 'session',
        scopeId: 'sess-loose',
        sessionId: 'sess-loose',
      }),
    ];
    const { wrapper } = mountWith({ chunks });
    await flushPromises();
    await wrapper
      .find('[data-testid="memory-promote-chunk-loose"]')
      .trigger('click');
    expect(
      wrapper.find('[data-testid="memory-promote-chunk-loose-project"]').exists(),
    ).toBe(false);
    expect(
      wrapper.find('[data-testid="memory-promote-chunk-loose-global"]').exists(),
    ).toBe(true);
  });

  it('promotes a project chunk to global with the resolved scope id', async () => {
    const chunks: MemoryChunk[] = [
      makeChunk({
        id: 'chunk-2',
        scopeKind: 'project',
        scopeId: 'proj-1',
        projectId: 'proj-1',
        sessionId: 'sess-A',
      }),
    ];
    const { wrapper, promoteScope, listChunks } = mountWith({ chunks });
    await flushPromises();
    await wrapper.find('[data-testid="memory-promote-chunk-2"]').trigger('click');
    await wrapper
      .find('[data-testid="memory-promote-chunk-2-global"]')
      .trigger('click');
    await flushPromises();
    listChunks.mockClear();
    await wrapper
      .find('[data-testid="memory-promote-confirm"]')
      .trigger('click');
    await flushPromises();
    expect(promoteScope).toHaveBeenCalledWith('chunk-2', 'global', '');
    // Refresh after a successful promotion.
    expect(listChunks).toHaveBeenCalledTimes(1);
  });

  it('promotes a session chunk to project, resolving the project id from the chunk', async () => {
    const chunks: MemoryChunk[] = [
      makeChunk({
        id: 'chunk-3',
        scopeKind: 'session',
        scopeId: 'sess-A',
        sessionId: 'sess-A',
        projectId: 'proj-42',
      }),
    ];
    const { wrapper, promoteScope } = mountWith({ chunks });
    await flushPromises();
    await wrapper.find('[data-testid="memory-promote-chunk-3"]').trigger('click');
    await wrapper
      .find('[data-testid="memory-promote-chunk-3-project"]')
      .trigger('click');
    await flushPromises();
    await wrapper
      .find('[data-testid="memory-promote-confirm"]')
      .trigger('click');
    await flushPromises();
    expect(promoteScope).toHaveBeenCalledWith('chunk-3', 'project', 'proj-42');
  });

  it('cancels promotion without calling promoteScope', async () => {
    const chunks: MemoryChunk[] = [
      makeChunk({
        id: 'chunk-cancel',
        scopeKind: 'project',
        scopeId: 'proj-1',
        projectId: 'proj-1',
      }),
    ];
    const { wrapper, promoteScope } = mountWith({ chunks });
    await flushPromises();
    await wrapper
      .find('[data-testid="memory-promote-chunk-cancel"]')
      .trigger('click');
    await wrapper
      .find('[data-testid="memory-promote-chunk-cancel-global"]')
      .trigger('click');
    await flushPromises();
    await wrapper
      .find('[data-testid="memory-promote-cancel"]')
      .trigger('click');
    await flushPromises();
    expect(promoteScope).not.toHaveBeenCalled();
    expect(
      wrapper.find('[data-testid="memory-promote-modal"]').exists(),
    ).toBe(false);
  });

  it('disables Promote scope when the chunk is already global', async () => {
    const chunks: MemoryChunk[] = [
      makeChunk({
        id: 'chunk-global',
        scopeKind: 'global',
        scopeId: '',
      }),
    ];
    const { wrapper } = mountWith({ chunks });
    await flushPromises();
    const btn = wrapper.find('[data-testid="memory-promote-chunk-global"]');
    expect(btn.exists()).toBe(true);
    expect(btn.attributes('disabled')).toBeDefined();
  });
});
