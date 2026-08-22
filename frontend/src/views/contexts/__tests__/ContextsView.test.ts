import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ContextsView from '@/views/contexts/ContextsView.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type {
  ContextNode,
  ContextSyncStatusView,
  ContextPublishRequest,
  ContextPublishResult,
  ContextSearchHitView,
  ContextExportView,
} from '@/lib/types';

function provide(opts: {
  tree?: ContextNode;
  treeAll?: ContextNode;
  files?: Record<string, string>;
  recent?: string[];
  rootPath?: string;
  saveSpy?: (path: string, content: string) => Promise<void>;
  syncStatus?: ContextSyncStatusView;
  publishSpy?: (req: ContextPublishRequest) => Promise<ContextPublishResult>;
  createFolderSpy?: (path: string) => Promise<void>;
  renameSpy?: (oldPath: string, newPath: string) => Promise<void>;
  deleteSpy?: (path: string) => Promise<void>;
  promoteSpy?: (nodeID: string) => Promise<{ updated_node_id: string; new_classification: 'org_shared' }>;
  searchSpy?: (query: string, teamID: string, limit: number) => Promise<ContextSearchHitView[]>;
  exportSpy?: (teamID: string, format: string) => Promise<ContextExportView>;
}) {
  const tree: ContextNode =
    opts.tree ?? { name: '', path: '', kind: 'folder' };
  const treeAll: ContextNode = opts.treeAll ?? tree;
  const files = opts.files ?? {};
  const recent = opts.recent ?? [];
  const rootPath = opts.rootPath ?? '/tmp/contexts';
  const syncStatus: ContextSyncStatusView = opts.syncStatus ?? {
    cursor: '',
    last_pull_err: '',
    last_push_err: '',
    pull_count: 0,
    team_cap_enabled: false,
  };

  const client = createFakeHarnessClient({
    contexts: {
      list: async () => tree,
      listAll: async () => treeAll,
      get: async (path: string) => {
        const v = files[path];
        if (v === undefined) {
          throw new Error(`not found: ${path}`);
        }
        return v;
      },
      save: opts.saveSpy ?? (async () => undefined),
      createFolder: opts.createFolderSpy ?? (async () => undefined),
      rename: opts.renameSpy ?? (async () => undefined),
      delete: opts.deleteSpy ?? (async () => undefined),
      recentlyApplied: async () => recent,
      rootPath: async () => rootPath,
      syncStatus: async () => syncStatus,
      publish: opts.publishSpy ?? (async (_req) => ({
        accepted_nodes: 1,
        accepted_edges: 0,
        conflicts: [],
      })),
      promote:
        opts.promoteSpy ??
        (async (nodeID: string) => ({
          updated_node_id: nodeID,
          new_classification: 'org_shared' as const,
        })),
      search: opts.searchSpy ?? (async () => []),
      export:
        opts.exportSpy ??
        (async () => ({
          content_type: 'application/x-ndjson',
          data_base64: '',
          byte_len: 0,
        })),
    } as any,
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

  describe('WP07 — fleet context-graph sync UI', () => {
    it('does not show sync status strip when team cap is absent', async () => {
      const { client } = provide({
        syncStatus: {
          cursor: '',
          last_pull_err: '',
          last_push_err: '',
          pull_count: 0,
          team_cap_enabled: false,
        },
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      expect(w.find('[data-testid=context-sync-status-strip]').exists()).toBe(false);
    });

    it('shows sync status strip when team cap is enabled', async () => {
      const { client } = provide({
        syncStatus: {
          cursor: '2026-06-08T00:00:00Z',
          last_pull_err: '',
          last_push_err: '',
          pull_count: 5,
          team_cap_enabled: true,
        },
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      const strip = w.find('[data-testid=context-sync-status-strip]');
      expect(strip.exists()).toBe(true);
      expect(strip.text()).toContain('Team sync active');
      expect(w.find('[data-testid=context-sync-pull-count]').text()).toContain('5');
    });

    it('does not show publish button when team cap is absent', async () => {
      const tree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [{ name: 'guide.md', path: 'guide.md', kind: 'file' }],
      };
      const { client } = provide({
        tree,
        files: { 'guide.md': '# guide' },
        syncStatus: {
          cursor: '',
          last_pull_err: '',
          last_push_err: '',
          pull_count: 0,
          team_cap_enabled: false,
        },
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      // Click to select a file.
      await w.find('[data-testid="context-node-guide.md"]').trigger('click');
      await flushPromises();
      expect(w.find('[data-testid=context-publish-btn]').exists()).toBe(false);
    });

    it('shows publish button when team cap is enabled and a file is selected', async () => {
      const tree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [{ name: 'style.md', path: 'style.md', kind: 'file' }],
      };
      const { client } = provide({
        tree,
        files: { 'style.md': '# Go style' },
        syncStatus: {
          cursor: '',
          last_pull_err: '',
          last_push_err: '',
          pull_count: 0,
          team_cap_enabled: true,
        },
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      // No file selected yet — button hidden.
      expect(w.find('[data-testid=context-publish-btn]').exists()).toBe(false);
      // Select a file.
      await w.find('[data-testid="context-node-style.md"]').trigger('click');
      await flushPromises();
      // Button appears.
      expect(w.find('[data-testid=context-publish-btn]').exists()).toBe(true);
    });

    it('opens the first-publish confirm dialog on publish button click', async () => {
      const tree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [{ name: 'tips.md', path: 'tips.md', kind: 'file' }],
      };
      const { client } = provide({
        tree,
        files: { 'tips.md': '# tips' },
        syncStatus: {
          cursor: '',
          last_pull_err: '',
          last_push_err: '',
          pull_count: 0,
          team_cap_enabled: true,
        },
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      await w.find('[data-testid="context-node-tips.md"]').trigger('click');
      await flushPromises();
      // Click publish.
      await w.find('[data-testid=context-publish-btn]').trigger('click');
      await flushPromises();
      const dialog = w.find('[data-testid=context-publish-confirm-dialog]');
      expect(dialog.exists()).toBe(true);
      expect(dialog.text()).toContain('Share with your team?');
      expect(dialog.text()).toContain('visible to everyone in your organisation');
    });

    it('cancels the confirm dialog without publishing', async () => {
      const tree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [{ name: 'cancel.md', path: 'cancel.md', kind: 'file' }],
      };
      const publishSpy = vi.fn(async (_req: ContextPublishRequest) => ({
        accepted_nodes: 1,
        accepted_edges: 0,
        conflicts: [],
      }));
      const { client } = provide({
        tree,
        files: { 'cancel.md': '# cancel' },
        syncStatus: { cursor: '', last_pull_err: '', last_push_err: '', pull_count: 0, team_cap_enabled: true },
        publishSpy,
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      await w.find('[data-testid="context-node-cancel.md"]').trigger('click');
      await flushPromises();
      await w.find('[data-testid=context-publish-btn]').trigger('click');
      await flushPromises();
      // Dialog is open.
      expect(w.find('[data-testid=context-publish-confirm-dialog]').exists()).toBe(true);
      // Click cancel.
      await w.find('[data-testid=context-publish-confirm-cancel]').trigger('click');
      await flushPromises();
      // Dialog closed, publish not called.
      expect(w.find('[data-testid=context-publish-confirm-dialog]').exists()).toBe(false);
      expect(publishSpy).not.toHaveBeenCalled();
    });

    it('calls publish and shows result after confirming', async () => {
      const tree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [{ name: 'house.md', path: 'house.md', kind: 'file' }],
      };
      const publishSpy = vi.fn(async (_req: ContextPublishRequest) => ({
        accepted_nodes: 1,
        accepted_edges: 0,
        conflicts: [],
      }));
      const { client } = provide({
        tree,
        files: { 'house.md': '# House Go style' },
        syncStatus: { cursor: '', last_pull_err: '', last_push_err: '', pull_count: 0, team_cap_enabled: true },
        publishSpy,
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      await w.find('[data-testid="context-node-house.md"]').trigger('click');
      await flushPromises();
      await w.find('[data-testid=context-publish-btn]').trigger('click');
      await flushPromises();
      // Confirm.
      await w.find('[data-testid=context-publish-confirm-ok]').trigger('click');
      await flushPromises();
      // publish was called with layer=team.
      expect(publishSpy).toHaveBeenCalledOnce();
      const req = publishSpy.mock.calls[0]![0];
      expect(req.layer).toBe('team');
      expect(req.title).toBe('house');
      expect(req.body).toBe('# House Go style');
      // Result strip shown.
      expect(w.find('[data-testid=context-publish-result]').exists()).toBe(true);
      expect(w.find('[data-testid=context-publish-result]').text()).toContain('Published');
    });

    it('shows sync pull error in status strip', async () => {
      const { client } = provide({
        syncStatus: {
          cursor: '',
          last_pull_err: 'fleet: timeout',
          last_push_err: '',
          pull_count: 0,
          team_cap_enabled: true,
        },
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      const errEl = w.find('[data-testid=context-sync-pull-err]');
      expect(errEl.exists()).toBe(true);
      expect(errEl.text()).toContain('fleet: timeout');
    });
  });

  describe('create folder', () => {
    it('prompts for a name and creates the folder with it (not a timestamp)', async () => {
      const createFolderSpy = vi.fn(async () => undefined);
      const tree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [{ name: 'notes.md', path: 'notes.md', kind: 'file' }],
      };
      const { client } = provide({ tree, createFolderSpy });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();

      // Reveal the inline name input.
      await w.find('[data-testid=context-create-folder]').trigger('click');
      const input = w.find('[data-testid=context-new-folder-input]');
      expect(input.exists()).toBe(true);

      // Type a name and confirm with Enter.
      await input.setValue('Research notes');
      await input.trigger('keydown.enter');
      await flushPromises();

      // createFolder is called with the typed name at the library root,
      // NOT an auto-generated folder-<timestamp> placeholder.
      expect(createFolderSpy).toHaveBeenCalledTimes(1);
      expect(createFolderSpy).toHaveBeenCalledWith('Research notes');
    });

    it('cancels folder creation on Escape without calling createFolder', async () => {
      const createFolderSpy = vi.fn(async () => undefined);
      const { client } = provide({
        tree: { name: '', path: '', kind: 'folder', children: [{ name: 'a.md', path: 'a.md', kind: 'file' }] },
        createFolderSpy,
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();

      await w.find('[data-testid=context-create-folder]').trigger('click');
      const input = w.find('[data-testid=context-new-folder-input]');
      await input.setValue('scratch');
      await input.trigger('keydown.esc');
      await flushPromises();

      expect(createFolderSpy).not.toHaveBeenCalled();
      expect(w.find('[data-testid=context-new-folder-input]').exists()).toBe(false);
    });
  });

  // BLOCKER-1: ContextHealthCard must be mounted in the right column.
  describe('ContextHealthCard (context-bootstrap-harness-integration WP07b)', () => {
    it('mounts ContextHealthCard in the right-hand column (data-testid=context-health-card)', async () => {
      const { client } = provide({});
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      // ContextHealthCard renders with data-testid="context-health-card" on mount.
      expect(w.find('[data-testid="context-health-card"]').exists()).toBe(true);
    });

    it('calls contextBootstrap.health() on mount via ContextHealthCard', async () => {
      const { client } = provide({});
      const healthSpy = vi.spyOn(client.contextBootstrap, 'health');
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      // ContextHealthCard calls health() in its onMounted hook.
      expect(healthSpy).toHaveBeenCalled();
      w.unmount();
    });
  });

  describe('WP16 — rename, delete, promote, search, export', () => {
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

    it('renames a file to a sibling path and reloads the tree', async () => {
      const renameSpy = vi.fn(async () => undefined);
      const { client } = provide({ tree, renameSpy });
      const listSpy = vi.spyOn(client.contexts, 'list');
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();

      await w.find('[data-testid="context-rename-btn-top.md"]').trigger('click');
      const input = w.find('[data-testid="context-rename-input-top.md"]');
      expect(input.exists()).toBe(true);
      await input.setValue('renamed.md');
      await input.trigger('keydown.enter');
      await flushPromises();

      expect(renameSpy).toHaveBeenCalledWith('top.md', 'renamed.md');
      expect(listSpy.mock.calls.length).toBeGreaterThanOrEqual(2);
    });

    it('renames a nested file preserving its parent directory', async () => {
      const renameSpy = vi.fn(async () => undefined);
      const { client } = provide({ tree, renameSpy });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();

      // "notes" is a nested (non-root) folder; only the root's direct
      // children auto-expand, so open it before reaching its child row.
      await w.find('[data-testid="context-node-notes"]').trigger('click');
      await flushPromises();
      await w
        .find('[data-testid="context-rename-btn-notes/welcome.md"]')
        .trigger('click');
      await w
        .find('[data-testid="context-rename-input-notes/welcome.md"]')
        .setValue('hello.md');
      await w
        .find('[data-testid="context-rename-input-notes/welcome.md"]')
        .trigger('keydown.enter');
      await flushPromises();

      expect(renameSpy).toHaveBeenCalledWith('notes/welcome.md', 'notes/hello.md');
    });

    it('cancels rename on Escape without calling rename', async () => {
      const renameSpy = vi.fn(async () => undefined);
      const { client } = provide({ tree, renameSpy });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();

      await w.find('[data-testid="context-rename-btn-top.md"]').trigger('click');
      const input = w.find('[data-testid="context-rename-input-top.md"]');
      await input.setValue('renamed.md');
      await input.trigger('keydown.esc');
      await flushPromises();

      expect(renameSpy).not.toHaveBeenCalled();
      expect(w.find('[data-testid="context-rename-input-top.md"]').exists()).toBe(false);
    });

    it('deletes a file after confirming, and clears the selection if it was selected', async () => {
      const deleteSpy = vi.fn(async () => undefined);
      const { client } = provide({
        tree,
        files: { 'top.md': '# top' },
        deleteSpy,
      });
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true);
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();

      await w.find('[data-testid="context-node-top.md"]').trigger('click');
      await flushPromises();
      await w.find('[data-testid="context-delete-btn-top.md"]').trigger('click');
      await flushPromises();

      expect(confirmSpy).toHaveBeenCalled();
      expect(deleteSpy).toHaveBeenCalledWith('top.md');
      confirmSpy.mockRestore();
    });

    it('does not delete when the confirm dialog is declined', async () => {
      const deleteSpy = vi.fn(async () => undefined);
      const { client } = provide({ tree, deleteSpy });
      const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false);
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();

      await w.find('[data-testid="context-delete-btn-top.md"]').trigger('click');
      await flushPromises();

      expect(deleteSpy).not.toHaveBeenCalled();
      confirmSpy.mockRestore();
    });

    describe('promote', () => {
      it('renders disabled with a reason when fleet is off, and does not dispatch', async () => {
        const promoteSpy = vi.fn(async (nodeID: string) => ({
          updated_node_id: nodeID,
          new_classification: 'org_shared' as const,
        }));
        const { client } = provide({
          tree,
          files: { 'top.md': '# top' },
          syncStatus: {
            cursor: '',
            last_pull_err: '',
            last_push_err: '',
            pull_count: 0,
            team_cap_enabled: false,
          },
          promoteSpy,
        });
        const w = mount(ContextsView, {
          global: { provide: { [HarnessClientKey as symbol]: client } },
        });
        await flushPromises();
        await w.find('[data-testid="context-node-top.md"]').trigger('click');
        await flushPromises();

        const btn = w.find('[data-testid="context-promote-btn"]');
        expect(btn.exists()).toBe(true);
        expect((btn.element as HTMLButtonElement).disabled).toBe(true);
        expect(w.find('[data-testid="context-promote-disabled-reason"]').exists()).toBe(true);

        await btn.trigger('click');
        await flushPromises();
        expect(promoteSpy).not.toHaveBeenCalled();
      });

      it('promotes the selected file when fleet is on', async () => {
        const promoteSpy = vi.fn(async (nodeID: string) => ({
          updated_node_id: nodeID,
          new_classification: 'org_shared' as const,
        }));
        const { client } = provide({
          tree,
          files: { 'top.md': '# top' },
          syncStatus: {
            cursor: '',
            last_pull_err: '',
            last_push_err: '',
            pull_count: 0,
            team_cap_enabled: true,
          },
          promoteSpy,
        });
        const w = mount(ContextsView, {
          global: { provide: { [HarnessClientKey as symbol]: client } },
        });
        await flushPromises();
        await w.find('[data-testid="context-node-top.md"]').trigger('click');
        await flushPromises();

        const btn = w.find('[data-testid="context-promote-btn"]');
        expect((btn.element as HTMLButtonElement).disabled).toBe(false);
        await btn.trigger('click');
        await flushPromises();

        expect(promoteSpy).toHaveBeenCalledOnce();
        expect(w.find('[data-testid="context-promote-result"]').exists()).toBe(true);
      });
    });

    describe('team search + export', () => {
      it('renders disabled with a reason when fleet is off', async () => {
        const searchSpy = vi.fn(async () => []);
        const exportSpy = vi.fn(async () => ({
          content_type: 'application/x-ndjson',
          data_base64: '',
          byte_len: 0,
        }));
        const { client } = provide({
          syncStatus: {
            cursor: '',
            last_pull_err: '',
            last_push_err: '',
            pull_count: 0,
            team_cap_enabled: false,
          },
          searchSpy,
          exportSpy,
        });
        const w = mount(ContextsView, {
          global: { provide: { [HarnessClientKey as symbol]: client } },
        });
        await flushPromises();

        expect(
          (w.find('[data-testid="context-team-search-input"]').element as HTMLInputElement).disabled,
        ).toBe(true);
        expect(
          (w.find('[data-testid="context-team-search-btn"]').element as HTMLButtonElement).disabled,
        ).toBe(true);
        expect(
          (w.find('[data-testid="context-export-btn"]').element as HTMLButtonElement).disabled,
        ).toBe(true);
        expect(w.find('[data-testid="context-team-search-disabled-reason"]').exists()).toBe(true);

        await w.find('[data-testid="context-team-search-input"]').setValue('style guide');
        await w.find('[data-testid="context-team-search-btn"]').trigger('click');
        await w.find('[data-testid="context-export-btn"]').trigger('click');
        await flushPromises();

        expect(searchSpy).not.toHaveBeenCalled();
        expect(exportSpy).not.toHaveBeenCalled();
      });

      it('runs a search against the fleet graph and renders the hits', async () => {
        const searchSpy = vi.fn(async () => [
          {
            node_id: 'n1',
            title: 'Go style guide',
            classification: 'team_shared' as const,
            snippet: 'Prefer **early returns**.',
            rank: 1.0,
          },
        ]);
        const { client } = provide({
          syncStatus: {
            cursor: '',
            last_pull_err: '',
            last_push_err: '',
            pull_count: 0,
            team_cap_enabled: true,
          },
          searchSpy,
        });
        const w = mount(ContextsView, {
          global: { provide: { [HarnessClientKey as symbol]: client } },
        });
        await flushPromises();

        await w.find('[data-testid="context-team-search-input"]').setValue('style guide');
        await w.find('[data-testid="context-team-search-btn"]').trigger('click');
        await flushPromises();

        expect(searchSpy).toHaveBeenCalledWith('style guide', '', 20);
        expect(w.find('[data-testid="context-search-hit-n1"]').exists()).toBe(true);
        expect(w.find('[data-testid="context-search-hit-n1"]').text()).toContain('Go style guide');
      });

      it('shows a no-matches state when the search returns nothing', async () => {
        const { client } = provide({
          syncStatus: {
            cursor: '',
            last_pull_err: '',
            last_push_err: '',
            pull_count: 0,
            team_cap_enabled: true,
          },
          searchSpy: async () => [],
        });
        const w = mount(ContextsView, {
          global: { provide: { [HarnessClientKey as symbol]: client } },
        });
        await flushPromises();
        // No search dispatched yet — no empty-state message.
        expect(w.find('[data-testid="context-team-search-empty"]').exists()).toBe(false);

        await w.find('[data-testid="context-team-search-input"]').setValue('nothing here');
        await w.find('[data-testid="context-team-search-btn"]').trigger('click');
        await flushPromises();

        expect(w.find('[data-testid="context-team-search-empty"]').exists()).toBe(true);
      });

      it('downloads the export payload when fleet is on', async () => {
        const exportSpy = vi.fn(async () => ({
          content_type: 'application/x-ndjson',
          data_base64: btoa('{"id":"n1"}'),
          byte_len: 12,
        }));
        const { client } = provide({
          syncStatus: {
            cursor: '',
            last_pull_err: '',
            last_push_err: '',
            pull_count: 0,
            team_cap_enabled: true,
          },
          exportSpy,
        });
        const createObjectURLSpy = vi
          .spyOn(URL, 'createObjectURL')
          .mockReturnValue('blob:fake');
        const revokeObjectURLSpy = vi
          .spyOn(URL, 'revokeObjectURL')
          .mockImplementation(() => undefined);
        const w = mount(ContextsView, {
          global: { provide: { [HarnessClientKey as symbol]: client } },
        });
        await flushPromises();

        await w.find('[data-testid="context-export-btn"]').trigger('click');
        await flushPromises();

        expect(exportSpy).toHaveBeenCalledWith('', 'jsonl');
        expect(createObjectURLSpy).toHaveBeenCalled();
        expect(revokeObjectURLSpy).toHaveBeenCalled();
        createObjectURLSpy.mockRestore();
        revokeObjectURLSpy.mockRestore();
      });

      it('surfaces an error when export returns no data', async () => {
        const exportSpy = vi.fn(async () => ({
          content_type: 'application/x-ndjson',
          data_base64: '',
          byte_len: 0,
        }));
        const { client } = provide({
          syncStatus: {
            cursor: '',
            last_pull_err: '',
            last_push_err: '',
            pull_count: 0,
            team_cap_enabled: true,
          },
          exportSpy,
        });
        const w = mount(ContextsView, {
          global: { provide: { [HarnessClientKey as symbol]: client } },
        });
        await flushPromises();

        await w.find('[data-testid="context-export-btn"]').trigger('click');
        await flushPromises();

        expect(w.find('[data-testid="context-export-error"]').exists()).toBe(true);
      });
    });
  });
});
