import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import AttachmentTreePicker from '@/components/contexts/AttachmentTreePicker.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Attachment, ContextNode } from '@/lib/types';

function tree(): ContextNode {
  return {
    name: '',
    path: '',
    kind: 'folder',
    children: [
      {
        name: 'briefs',
        path: 'briefs',
        kind: 'folder',
        children: [
          { name: 'intro.md', path: 'briefs/intro.md', kind: 'file' },
        ],
      },
      { name: 'standalone.md', path: 'standalone.md', kind: 'file' },
    ],
  };
}

function mountPicker(seed = {}, props: Record<string, unknown> = {}) {
  const client = createFakeHarnessClient(seed);
  return {
    wrapper: mount(AttachmentTreePicker, {
      props: {
        open: true,
        scopeKind: 'project',
        scopeId: 'p-1',
        ...props,
      },
      global: { provide: { [HarnessClientKey as symbol]: client } },
    }),
    client,
  };
}

describe('AttachmentTreePicker', () => {
  it('renders nothing when open is false', () => {
    const w = mount(AttachmentTreePicker, {
      props: { open: false, scopeKind: 'project', scopeId: 'p-1' },
      global: {
        provide: { [HarnessClientKey as symbol]: createFakeHarnessClient() },
      },
    });
    expect(w.find('[data-testid=attachment-tree-picker]').exists()).toBe(
      false,
    );
  });

  it('loads the library tree on open and renders top-level entries', async () => {
    const list = vi.fn().mockResolvedValue(tree());
    const { wrapper } = mountPicker({
      contexts: {
        list,
        get: async () => '',
        save: async () => undefined,
        createFolder: async () => undefined,
        rename: async () => undefined,
        delete: async () => undefined,
        recentlyApplied: async () => [],
        rootPath: async () => '/fake',
      },
    });
    await flushPromises();
    expect(list).toHaveBeenCalled();
    expect(wrapper.text()).toContain('briefs');
    expect(wrapper.text()).toContain('standalone.md');
  });

  it('renders the empty-state message when the library has no children', async () => {
    const { wrapper } = mountPicker({
      contexts: {
        list: async () => ({ name: '', path: '', kind: 'folder' as const }),
        get: async () => '',
        save: async () => undefined,
        createFolder: async () => undefined,
        rename: async () => undefined,
        delete: async () => undefined,
        recentlyApplied: async () => [],
        rootPath: async () => '/fake',
      },
    });
    await flushPromises();
    const empty = wrapper.find('[data-testid=attachment-tree-empty]');
    expect(empty.exists()).toBe(true);
  });

  it('selecting a file then clicking Attach calls contexts.get + attachments.add and emits added', async () => {
    const get = vi.fn().mockResolvedValue('# heading\n\nbody');
    const added: Attachment = {
      id: 'a-new',
      scopeKind: 'project',
      scopeId: 'p-1',
      contentSource: 'library:standalone.md',
      content: '# heading\n\nbody',
      kind: 'system',
      position: 0,
      createdAt: '2026-04-26T00:00:00Z',
    };
    const add = vi.fn().mockResolvedValue(added);
    const { wrapper } = mountPicker({
      contexts: {
        list: async () => tree(),
        get,
        save: async () => undefined,
        createFolder: async () => undefined,
        rename: async () => undefined,
        delete: async () => undefined,
        recentlyApplied: async () => [],
        rootPath: async () => '/fake',
      },
      attachments: {
        list: async () => [],
        listResolved: async () => [],
        add,
        remove: async () => undefined,
        reorder: async () => undefined,
        refresh: async () => added,
      },
    });
    await flushPromises();
    // Click the standalone.md row.
    const fileBtn = wrapper.find(
      '[data-testid="context-node-standalone.md"]',
    );
    expect(fileBtn.exists()).toBe(true);
    await fileBtn.trigger('click');
    await flushPromises();
    // Attach button now enabled.
    const attachBtn = wrapper.find('[data-testid=attachment-tree-add]');
    expect(attachBtn.exists()).toBe(true);
    await attachBtn.trigger('click');
    await flushPromises();
    expect(get).toHaveBeenCalledWith('standalone.md');
    expect(add).toHaveBeenCalledWith({
      scopeKind: 'project',
      scopeId: 'p-1',
      contentSource: 'library:standalone.md',
      content: '# heading\n\nbody',
      kind: 'system',
    });
    const addedEvents = wrapper.emitted('added');
    expect(addedEvents).toBeTruthy();
    expect(addedEvents![0][0]).toMatchObject({ id: 'a-new' });
    // Picker requests close after a successful attach.
    expect(wrapper.emitted('close')).toBeTruthy();
  });

  it('surfaces an error banner when the tree fetch fails', async () => {
    const { wrapper } = mountPicker({
      contexts: {
        list: async () => {
          throw new Error('disk on fire');
        },
        get: async () => '',
        save: async () => undefined,
        createFolder: async () => undefined,
        rename: async () => undefined,
        delete: async () => undefined,
        recentlyApplied: async () => [],
        rootPath: async () => '/fake',
      },
    });
    await flushPromises();
    const err = wrapper.find('[data-testid=attachment-tree-error]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('disk on fire');
  });

  it('attaches a whole module via attachModule when a folder is selected', async () => {
    const attachModule = vi.fn(
      async (scopeKind: string, scopeId: string, dirPath: string) => ({
        id: 'mod-1',
        scopeKind,
        scopeId,
        contentSource: `module:${dirPath}`,
        content: 'root body',
        kind: 'system',
        position: 0,
        createdAt: '',
      }),
    );
    const add = vi.fn();
    const { wrapper } = mountPicker({
      contexts: {
        list: async () => tree(),
        get: async () => 'file body',
        save: async () => undefined,
        createFolder: async () => undefined,
        attachModule,
        rename: async () => undefined,
        delete: async () => undefined,
        recentlyApplied: async () => [],
        rootPath: async () => '/fake',
      },
      attachments: { add },
    });
    await flushPromises();

    // Click the "briefs" folder row → selects it as a module target.
    await wrapper.find('[data-testid=context-node-briefs]').trigger('click');
    await flushPromises();

    // The Attach button routes to attachModule, NOT the single-file path.
    const addBtn = wrapper.find('[data-testid=attachment-tree-add]');
    expect(addBtn.text()).toContain('Attach module');
    await addBtn.trigger('click');
    await flushPromises();

    expect(attachModule).toHaveBeenCalledWith('project', 'p-1', 'briefs');
    expect(add).not.toHaveBeenCalled();
    expect(wrapper.emitted('added')).toBeTruthy();
    expect(wrapper.emitted('close')).toBeTruthy();
  });
});
