import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ContextsView from '@/views/contexts/ContextsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { ContextNode } from '@/lib/types';

function provide(opts: {
  tree?: ContextNode;
  treeAll?: ContextNode;
  files?: Record<string, string>;
  recent?: string[];
  rootPath?: string;
  saveSpy?: (path: string, content: string) => Promise<void>;
}) {
  const tree: ContextNode =
    opts.tree ?? { name: '', path: '', kind: 'folder' };
  const treeAll: ContextNode = opts.treeAll ?? tree;
  const files = opts.files ?? {};
  const recent = opts.recent ?? [];
  const rootPath = opts.rootPath ?? '/tmp/contexts';

  const client = createFakeHarnessClient({
    contexts: {
      list: async () => tree,
      listAll: async () => treeAll,
      get: async (path) => {
        const v = files[path];
        if (v === undefined) {
          throw new Error(`not found: ${path}`);
        }
        return v;
      },
      save: opts.saveSpy ?? (async () => undefined),
      createFolder: async () => undefined,
      rename: async () => undefined,
      delete: async () => undefined,
      recentlyApplied: async () => recent,
      rootPath: async () => rootPath,
    },
  });
  return { client };
}

describe('ContextsView', () => {
  it('renders the canvas head with section number 07 and title', async () => {
    const { client } = provide({});
    const w = mount(ContextsView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    expect(w.text()).toContain('07');
    expect(w.text()).toContain('CONTEXTS');
    expect(w.text()).toContain('Context library');
  });

  it('shows the empty-state card when the library has no files', async () => {
    const { client } = provide({ rootPath: '/Users/me/.harness/contexts' });
    const w = mount(ContextsView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    const empty = w.find('[data-testid=context-empty-state]');
    expect(empty.exists()).toBe(true);
    expect(w.text()).toContain('No contexts yet');
    // Empty-state card surfaces the library root path so the user
    // knows where to drop files.
    expect(w.text()).toContain('/Users/me/.harness/contexts');
    // … and offers a create-folder affordance.
    expect(w.find('[data-testid=context-empty-create-folder]').exists()).toBe(
      true,
    );
  });

  it('renders the tree of files when the library has content', async () => {
    const tree: ContextNode = {
      name: '',
      path: '',
      kind: 'folder',
      children: [
        {
          name: 'notes',
          path: 'notes',
          kind: 'folder',
          children: [
            { name: 'welcome.md', path: 'notes/welcome.md', kind: 'file' },
          ],
        },
        { name: 'top.md', path: 'top.md', kind: 'file' },
      ],
    };
    const { client } = provide({ tree });
    const w = mount(ContextsView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    const root = w.find('[data-testid=context-tree-root]');
    expect(root.exists()).toBe(true);
    expect(w.text()).toContain('notes');
    expect(w.text()).toContain('top.md');
  });

  it('renders the file content in the preview pane on click', async () => {
    const tree: ContextNode = {
      name: '',
      path: '',
      kind: 'folder',
      children: [{ name: 'hello.md', path: 'hello.md', kind: 'file' }],
    };
    const { client } = provide({
      tree,
      files: { 'hello.md': '# greetings' },
    });
    const w = mount(ContextsView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    const node = w.find('[data-testid="context-node-hello.md"]');
    expect(node.exists()).toBe(true);
    await node.trigger('click');
    await flushPromises();
    expect(w.text()).toContain('# greetings');
  });

  it('renders the recently-applied empty hint by default', async () => {
    const { client } = provide({});
    const w = mount(ContextsView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    expect(w.text()).toContain('Files you attach to sessions will appear here.');
  });

  describe('WP05 — editor + watcher polish', () => {
    let eventHandlers: Record<string, (payload: unknown) => void>;
    const originalRuntime = (window as unknown as { runtime?: unknown }).runtime;

    beforeEach(() => {
      eventHandlers = {};
      (window as unknown as {
        runtime?: {
          EventsOn: (
            t: string,
            cb: (payload: unknown) => void,
          ) => () => void;
        };
      }).runtime = {
        EventsOn: (topic, cb) => {
          eventHandlers[topic] = cb;
          return () => {
            delete eventHandlers[topic];
          };
        },
      };
    });

    afterEach(() => {
      (window as unknown as { runtime?: unknown }).runtime = originalRuntime;
    });

    it('saves edits via the preview and refreshes the tree', async () => {
      const tree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [{ name: 'doc.md', path: 'doc.md', kind: 'file' }],
      };
      const saveSpy = vi.fn(async () => undefined);
      const { client } = provide({
        tree,
        files: { 'doc.md': 'before' },
        saveSpy,
      });
      const listSpy = vi.spyOn(client.contexts, 'list');
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();

      // Open the file → enter edit mode → mutate → Save.
      await w
        .find('[data-testid="context-node-doc.md"]')
        .trigger('click');
      await flushPromises();
      await w
        .find('[data-testid=context-preview-edit]')
        .trigger('click');
      await flushPromises();
      await w
        .find('[data-testid=context-preview-editor]')
        .setValue('after');
      await w
        .find('[data-testid=context-preview-save]')
        .trigger('click');
      await flushPromises();

      expect(saveSpy).toHaveBeenCalledWith('doc.md', 'after');
      // Re-fetch on save: list called once on mount + once on save.
      expect(listSpy.mock.calls.length).toBeGreaterThanOrEqual(2);
      // Read view returns and shows the freshly-saved content.
      expect(
        w.find('[data-testid=context-preview-content]').text(),
      ).toContain('after');
    });

    it('toggles "Show hidden" and reloads via listAll', async () => {
      const visibleTree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [{ name: 'visible.md', path: 'visible.md', kind: 'file' }],
      };
      const allTree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [
          { name: '.hidden.md', path: '.hidden.md', kind: 'file' },
          { name: 'visible.md', path: 'visible.md', kind: 'file' },
        ],
      };
      const { client } = provide({ tree: visibleTree, treeAll: allTree });
      const listAllSpy = vi.spyOn(client.contexts, 'listAll');
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      expect(w.text()).not.toContain('.hidden.md');
      await w.find('[data-testid=context-show-hidden]').trigger('change');
      await flushPromises();
      expect(listAllSpy).toHaveBeenCalled();
      expect(w.text()).toContain('.hidden.md');
    });

    it('flashes the external-change toast on contexts:tree-changed', async () => {
      vi.useFakeTimers();
      const { client } = provide({});
      const listSpy = vi.spyOn(client.contexts, 'list');
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      const baseline = listSpy.mock.calls.length;
      // Simulate a Wails event from the Go-side fsnotify watcher.
      eventHandlers['contexts:tree-changed']?.(undefined);
      await flushPromises();
      expect(listSpy.mock.calls.length).toBe(baseline + 1);
      expect(
        w.find('[data-testid=context-external-change-toast]').exists(),
      ).toBe(true);
      // Toast clears after the 1.5 s timer.
      vi.advanceTimersByTime(1600);
      await flushPromises();
      expect(
        w.find('[data-testid=context-external-change-toast]').exists(),
      ).toBe(false);
      vi.useRealTimers();
    });

    it('imports a local file via the file picker and saves it under root', async () => {
      const saveSpy = vi.fn(async () => undefined);
      const { client } = provide({ saveSpy });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
        attachTo: document.body,
      });
      await flushPromises();

      const input = w.find('[data-testid=context-import-input]')
        .element as HTMLInputElement;
      const file = new File(['# imported'], 'imported.md', {
        type: 'text/markdown',
      });
      Object.defineProperty(input, 'files', { value: [file] });
      await w.find('[data-testid=context-import-input]').trigger('change');
      await flushPromises();
      expect(saveSpy).toHaveBeenCalledWith('imported.md', '# imported');

      w.unmount();
    });
  });

  it('lists recently-applied paths when populated', async () => {
    const tree: ContextNode = {
      name: '',
      path: '',
      kind: 'folder',
      children: [{ name: 'pinned.md', path: 'pinned.md', kind: 'file' }],
    };
    const { client } = provide({
      tree,
      recent: ['pinned.md'],
    });
    const w = mount(ContextsView, {
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    expect(w.find('[data-testid="context-recent-pinned.md"]').exists()).toBe(
      true,
    );
  });
});
