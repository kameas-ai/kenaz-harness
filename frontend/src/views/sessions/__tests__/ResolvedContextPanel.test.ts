import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ResolvedContextPanel from '@/views/sessions/ResolvedContextPanel.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Attachment } from '@/lib/types';

function makeAttachment(overrides: Partial<Attachment>): Attachment {
  return {
    id: overrides.id ?? 'a-1',
    scopeKind: 'session',
    scopeId: '',
    contentSource: 'inline:abc',
    content: '',
    kind: 'system',
    position: 0,
    createdAt: '2026-04-26T00:00:00Z',
    ...overrides,
  };
}

function provide(seedListResolved: Attachment[]) {
  return createFakeHarnessClient({
    attachments: {
      list: async () => [],
      listResolved: async () => [...seedListResolved],
      add: async () => seedListResolved[0],
      remove: async () => undefined,
      reorder: async () => undefined,
      refresh: async () => seedListResolved[0],
    },
  });
}

function mountPanel(rows: Attachment[], sessionId = 's-1') {
  const client = provide(rows);
  return {
    wrapper: mount(ResolvedContextPanel, {
      props: { sessionId },
      global: { provide: { [HarnessClientKey as symbol]: client } },
    }),
  };
}

describe('ResolvedContextPanel', () => {
  it('renders collapsed by default and shows the per-scope summary count', async () => {
    const rows: Attachment[] = [
      makeAttachment({
        id: 'g-1',
        scopeKind: 'global',
        contentSource: 'library:global.md',
      }),
      makeAttachment({
        id: 'p-1',
        scopeKind: 'project',
        scopeId: 'proj',
        contentSource: 'library:project.md',
      }),
      makeAttachment({
        id: 's-1',
        scopeKind: 'session',
        scopeId: 'sess',
        contentSource: 'inline:abc',
      }),
    ];
    const { wrapper } = mountPanel(rows);
    await flushPromises();
    const summary = wrapper.find('[data-testid=resolved-context-summary]');
    expect(summary.exists()).toBe(true);
    expect(summary.text()).toContain('3 contexts');
    expect(summary.text()).toContain('1G / 1P / 1S');
    // Body is hidden in the collapsed state.
    expect(wrapper.find('[data-testid=resolved-context-body]').exists()).toBe(
      false,
    );
  });

  it('expands on toggle and renders global → project → session sub-sections in order (A8)', async () => {
    const rows: Attachment[] = [
      makeAttachment({
        id: 'g-1',
        scopeKind: 'global',
        contentSource: 'library:globals/header.md',
      }),
      makeAttachment({
        id: 'p-1',
        scopeKind: 'project',
        scopeId: 'proj',
        contentSource: 'library:projects/p.md',
      }),
      makeAttachment({
        id: 's-1',
        scopeKind: 'session',
        scopeId: 'sess',
        contentSource: 'inline:xyz',
      }),
    ];
    const { wrapper } = mountPanel(rows);
    await flushPromises();
    await wrapper.find('[data-testid=resolved-context-toggle]').trigger('click');
    await flushPromises();
    const html = wrapper.find('[data-testid=resolved-context-body]').html();
    const globalIdx = html.indexOf('resolved-context-global');
    const projectIdx = html.indexOf('resolved-context-project');
    const sessionIdx = html.indexOf('resolved-context-session');
    expect(globalIdx).toBeGreaterThan(-1);
    expect(projectIdx).toBeGreaterThan(-1);
    expect(sessionIdx).toBeGreaterThan(-1);
    expect(globalIdx).toBeLessThan(projectIdx);
    expect(projectIdx).toBeLessThan(sessionIdx);
  });

  it('renders the empty hint when no scopes have rows', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    await wrapper.find('[data-testid=resolved-context-toggle]').trigger('click');
    await flushPromises();
    expect(
      wrapper.find('[data-testid=resolved-context-empty]').exists(),
    ).toBe(true);
  });

  it('refetches resolved attachments when sessionId prop changes', async () => {
    const listResolved = vi.fn().mockResolvedValue([]);
    const client = createFakeHarnessClient({
      attachments: {
        list: async () => [],
        listResolved,
        add: async () => makeAttachment({}),
        remove: async () => undefined,
        reorder: async () => undefined,
        refresh: async () => makeAttachment({}),
      },
    });
    const wrapper = mount(ResolvedContextPanel, {
      props: { sessionId: 's-1' },
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    expect(listResolved).toHaveBeenCalledWith('s-1');
    await wrapper.setProps({ sessionId: 's-2' });
    await flushPromises();
    expect(listResolved).toHaveBeenCalledWith('s-2');
  });

  it('toggles an inline content preview when a row is clicked', async () => {
    const rows: Attachment[] = [
      makeAttachment({
        id: 's-1',
        scopeKind: 'session',
        contentSource: 'inline:abc',
        content: 'preview body 123',
      }),
    ];
    const { wrapper } = mountPanel(rows);
    await flushPromises();
    await wrapper.find('[data-testid=resolved-context-toggle]').trigger('click');
    await flushPromises();
    const row = wrapper.find('[data-testid=resolved-row-s-1]');
    await row.trigger('click');
    await flushPromises();
    const preview = wrapper.find('[data-testid=resolved-preview-s-1]');
    expect(preview.exists()).toBe(true);
    expect(preview.text()).toContain('preview body 123');
  });

  it('uses only design tokens — no raw hex/rgba', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    const html = wrapper.html();
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(html).not.toMatch(/rgba?\s*\(/i);
  });

  // ── FR-021: session-scope "New context folder" affordance ─────────────

  it('shows the "New folder" button in the session-new-folder-section when expanded (FR-021)', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    // Expand the panel first.
    await wrapper.find('[data-testid=resolved-context-toggle]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid=session-new-folder-section]').exists()).toBe(true);
    expect(wrapper.find('[data-testid=session-new-context-folder]').exists()).toBe(true);
  });

  it('reveals the inline folder-name input when "New folder" is clicked (FR-021)', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    await wrapper.find('[data-testid=resolved-context-toggle]').trigger('click');
    await flushPromises();
    // Input hidden initially.
    expect(wrapper.find('[data-testid=session-new-folder-row]').exists()).toBe(false);
    await wrapper.find('[data-testid=session-new-context-folder]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid=session-new-folder-row]').exists()).toBe(true);
    expect(wrapper.find('[data-testid=session-new-folder-input]').exists()).toBe(true);
  });

  it('calls createFolder + attachments.add and refreshes resolved on Enter (FR-021)', async () => {
    const createFolder = vi.fn(async () => undefined);
    const addAttachment = vi.fn(async () => makeAttachment({ id: 'att-new', scopeKind: 'session', contentSource: 'library:team-notes' }));
    const listResolved = vi.fn().mockResolvedValue([]);
    const client = createFakeHarnessClient({
      contexts: {
        list: async () => ({ name: '', path: '', kind: 'folder' as const }),
        listAll: async () => ({ name: '', path: '', kind: 'folder' as const }),
        get: async () => '',
        save: async () => undefined,
        createFolder,
        rename: async () => undefined,
        delete: async () => undefined,
        recentlyApplied: async () => [],
        rootPath: async () => '/tmp/contexts',
      },
      attachments: {
        list: async () => [],
        listResolved,
        add: addAttachment,
        remove: async () => undefined,
        reorder: async () => undefined,
        refresh: async () => makeAttachment({}),
      },
    });
    const wrapper = mount(ResolvedContextPanel, {
      props: { sessionId: 's-42' },
      global: { provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();
    await wrapper.find('[data-testid=resolved-context-toggle]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-testid=session-new-context-folder]').trigger('click');
    await flushPromises();
    const input = wrapper.find<HTMLInputElement>('[data-testid=session-new-folder-input]');
    await input.setValue('team-notes');
    await input.trigger('keydown.enter');
    await flushPromises();

    expect(createFolder).toHaveBeenCalledWith('team-notes');
    expect(addAttachment).toHaveBeenCalledWith({
      scopeKind: 'session',
      scopeId: 's-42',
      contentSource: 'library:team-notes',
      content: '',
    });
    // listResolved called again after create.
    expect(listResolved.mock.calls.length).toBeGreaterThanOrEqual(2);
    // Input dismissed after create.
    expect(wrapper.find('[data-testid=session-new-folder-row]').exists()).toBe(false);
  });

  it('dismisses the session folder input when Esc is pressed (FR-021)', async () => {
    const { wrapper } = mountPanel([]);
    await flushPromises();
    await wrapper.find('[data-testid=resolved-context-toggle]').trigger('click');
    await flushPromises();
    await wrapper.find('[data-testid=session-new-context-folder]').trigger('click');
    await flushPromises();
    expect(wrapper.find('[data-testid=session-new-folder-row]').exists()).toBe(true);
    await wrapper.find('[data-testid=session-new-folder-input]').trigger('keydown.esc');
    await flushPromises();
    expect(wrapper.find('[data-testid=session-new-folder-row]').exists()).toBe(false);
  });
});
