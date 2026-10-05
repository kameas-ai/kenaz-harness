import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ContextsView from '@/views/contexts/ContextsView.vue';
import { createFakeHarnessClient, fakeFleetSession } from '@/lib/harnessClient';
import { _resetFleetSessionForTest } from '@/lib/fleetSession';
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

  const base = createFakeHarnessClient();
  const client = createFakeHarnessClient({
    // fleet-session-truth-01DOGF0A WP05: the team gate reads the shared
    // fleet-session capability set. Model the backend: syncStatus's
    // team_cap_enabled and the session's shared_team_graph come from the
    // same capability poller.
    settings: {
      ...base.settings,
      fleetSession: async () =>
        fakeFleetSession({
          state: 'signed_in',
          capabilities: {
            tier: 'enterprise',
            enabled: { shared_team_graph: syncStatus.team_cap_enabled },
            fetchedAt: '',
            source: 'fleet',
          },
        }),
    } as any,
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
        effective_layer: _req.layer,
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

beforeEach(() => _resetFleetSessionForTest());
afterEach(() => _resetFleetSessionForTest());

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

    it('shows the publish button disabled with a reason when team cap is absent (knowledge-home WP04, P-5)', async () => {
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
      // Never hidden for capability reasons (FR-6): rendered, disabled, and
      // the reason says what would enable it.
      const btn = w.find('[data-testid=context-publish-btn]');
      expect(btn.exists()).toBe(true);
      expect((btn.element as HTMLButtonElement).disabled).toBe(true);
      const reason = w.find('[data-testid=context-share-disabled-reason]');
      expect(reason.text()).toContain('does not have the team-graph capability');
      expect(w.find('[data-testid=context-share-account-link]').attributes('href')).toBe('#/settings?tab=account');
      await btn.trigger('click');
      await flushPromises();
      expect(w.find('[data-testid=context-publish-confirm-dialog]').exists()).toBe(false);
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
      expect(dialog.text()).toContain('Share this entry?');
      // The layer choice is explicit — both "team" and the deliberate
      // "org" option are offered (finding #97), defaulting to "team".
      expect(dialog.find('[data-testid=context-publish-layer-team]').exists()).toBe(true);
      expect(dialog.find('[data-testid=context-publish-layer-org]').exists()).toBe(true);
      expect(
        (dialog.find('[data-testid=context-publish-layer-team]').element as HTMLInputElement)
          .checked,
      ).toBe(true);
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
        effective_layer: _req.layer,
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
        effective_layer: _req.layer,
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

    it('publishes org-wide when the user explicitly picks the org layer (finding #97)', async () => {
      const tree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [{ name: 'policy.md', path: 'policy.md', kind: 'file' }],
      };
      const publishSpy = vi.fn(async (_req: ContextPublishRequest) => ({
        accepted_nodes: 1,
        accepted_edges: 0,
        conflicts: [],
        effective_layer: _req.layer,
      }));
      const { client } = provide({
        tree,
        files: { 'policy.md': '# Org policy' },
        syncStatus: { cursor: '', last_pull_err: '', last_push_err: '', pull_count: 0, team_cap_enabled: true },
        publishSpy,
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      await w.find('[data-testid="context-node-policy.md"]').trigger('click');
      await flushPromises();
      await w.find('[data-testid=context-publish-btn]').trigger('click');
      await flushPromises();
      // Deliberately pick "org" instead of the default "team".
      await w.find('[data-testid=context-publish-layer-org]').setValue(true);
      await w.find('[data-testid=context-publish-confirm-ok]').trigger('click');
      await flushPromises();

      expect(publishSpy).toHaveBeenCalledOnce();
      const req = publishSpy.mock.calls[0]![0];
      expect(req.layer).toBe('org');
      // No team picker exists yet — team_id must never be sent.
      expect(req.team_id).toBeUndefined();
      expect(w.find('[data-testid=context-publish-result]').text()).toContain(
        'Published to your organisation',
      );
    });

    it('honestly reports a team-request-that-fell-back-to-org, not a silent team share', async () => {
      const tree: ContextNode = {
        name: '',
        path: '',
        kind: 'folder',
        children: [{ name: 'note.md', path: 'note.md', kind: 'file' }],
      };
      // Simulates the backend fallback (finding #97): the request asked
      // for "team" but the server had no team_id to honour it, so it
      // published at "org" instead and says so via effective_layer.
      const publishSpy = vi.fn(async (_req: ContextPublishRequest) => ({
        accepted_nodes: 1,
        accepted_edges: 0,
        conflicts: [],
        effective_layer: 'org' as const,
      }));
      const { client } = provide({
        tree,
        files: { 'note.md': '# note' },
        syncStatus: { cursor: '', last_pull_err: '', last_push_err: '', pull_count: 0, team_cap_enabled: true },
        publishSpy,
      });
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      await w.find('[data-testid="context-node-note.md"]').trigger('click');
      await flushPromises();
      await w.find('[data-testid=context-publish-btn]').trigger('click');
      await flushPromises();
      // Leave the default "team" choice selected, then confirm.
      await w.find('[data-testid=context-publish-confirm-ok]').trigger('click');
      await flushPromises();

      const req = publishSpy.mock.calls[0]![0];
      expect(req.layer).toBe('team');
      const resultText = w.find('[data-testid=context-publish-result]').text();
      expect(resultText).toContain('Published org-wide');
      expect(resultText).toContain("team sync isn't available yet");
      // Must NOT claim it went only to the team — that's the exact lie
      // this fix exists to prevent.
      expect(resultText).not.toContain('Published to your team');
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

  // BLOCKER-1: ContextHealthCard must be mounted. knowledge-home-01DOGF0E
  // WP06 moved it from the top of the right column to a collapsed status
  // chip in the toolbar (dogfood F11 — too prominent for a rarely-used card).
  describe('ContextHealthCard (context-bootstrap-harness-integration WP07b)', () => {
    it('mounts ContextHealthCard as a collapsed chip in the toolbar, not the right column', async () => {
      try { localStorage.removeItem('harness.knowledge.contextHealthExpanded.v1'); } catch { /* */ }
      const { client } = provide({});
      const w = mount(ContextsView, {
        global: { provide: { [HarnessClientKey as symbol]: client } },
      });
      await flushPromises();
      const toolbar = w.find('[data-testid="context-toolbar"]');
      expect(toolbar.find('[data-testid="context-health-chip"]').exists()).toBe(true);
      // Collapsed by default: the full card is not rendered.
      expect(w.find('[data-testid="context-health-card"]').exists()).toBe(false);
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
        expect(w.find('[data-testid="context-share-disabled-reason"]').exists()).toBe(true);

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

describe('ContextsView sharing affordances (knowledge-home-01DOGF0E WP04, P-5)', () => {
  const tree: ContextNode = {
    name: '',
    path: '',
    kind: 'folder',
    children: [{ name: 'notes.md', path: 'notes.md', kind: 'file' }],
  };
  const status = (cap: boolean): ContextSyncStatusView => ({
    cursor: '', last_pull_err: '', last_push_err: '', pull_count: 0, team_cap_enabled: cap,
  });

  async function selectNotes(client: ReturnType<typeof provide>['client']) {
    const w = mount(ContextsView, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    await w.find('[data-testid="context-node-notes.md"]').trigger('click');
    await flushPromises();
    return w;
  }

  it('cap off + file selected → Share… and Promote both visible, disabled, with one reason', async () => {
    const { client } = provide({ tree, files: { 'notes.md': '# n' }, syncStatus: status(false) });
    const w = await selectNotes(client);
    for (const id of ['context-publish-btn', 'context-promote-btn']) {
      const btn = w.find(`[data-testid=${id}]`);
      expect(btn.exists(), id).toBe(true);
      expect((btn.element as HTMLButtonElement).disabled, id).toBe(true);
      expect(btn.attributes('title'), id).toContain('Sharing is off');
    }
    expect(w.findAll('[data-testid=context-share-disabled-reason]')).toHaveLength(1);
    w.unmount();
  });

  it('cap on + file selected → both enabled, no reason shown', async () => {
    const { client } = provide({ tree, files: { 'notes.md': '# n' }, syncStatus: status(true) });
    const w = await selectNotes(client);
    for (const id of ['context-publish-btn', 'context-promote-btn']) {
      expect((w.find(`[data-testid=${id}]`).element as HTMLButtonElement).disabled, id).toBe(false);
    }
    expect(w.find('[data-testid=context-share-disabled-reason]').exists()).toBe(false);
    w.unmount();
  });

  it('sync status unreadable → reason still renders from the session store (D5 switch-over)', async () => {
    const { client } = provide({ tree, files: { 'notes.md': '# n' } });
    (client.contexts as any).syncStatus = async () => {
      throw new Error('fleet not wired');
    };
    const w = await selectNotes(client);
    expect((w.find('[data-testid=context-publish-btn]').element as HTMLButtonElement).disabled).toBe(true);
    expect(w.find('[data-testid=context-share-disabled-reason]').text()).toContain('team-graph capability');
    w.unmount();
  });
});

describe('ContextsView folder sharing state (knowledge-home-01DOGF0E WP05, P-6)', () => {
  // The owner's F10 scenario: a context module folder selected, fleet team
  // cap off. Before WP05 a folder click only expanded the row and NO sharing
  // affordance rendered. Folder-level share/promote is the FR-7 batch
  // dialog (decision record D4, owner ruled "build" 2026-10-05).
  const tree: ContextNode = {
    name: '',
    path: '',
    kind: 'folder',
    children: [
      {
        name: 'kameas-ai',
        path: 'kameas-ai',
        kind: 'folder',
        children: [{ name: 'context.md', path: 'kameas-ai/context.md', kind: 'file' }],
      },
    ],
  };
  const status = (cap: boolean): ContextSyncStatusView => ({
    cursor: '', last_pull_err: '', last_push_err: '', pull_count: 0, team_cap_enabled: cap,
  });

  it('folder selected, cap off → sharing section renders with the session reason; folder actions open, nothing publishes', async () => {
    const publishSpy = vi.fn();
    const promoteSpy = vi.fn();
    const { client } = provide({ tree, files: { 'kameas-ai/context.md': '# k' }, syncStatus: status(false), publishSpy, promoteSpy });
    const w = mount(ContextsView, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    await w.find('[data-testid="context-node-kameas-ai"]').trigger('click');
    await flushPromises();
    const reason = w.find('[data-testid=context-share-disabled-reason]');
    expect(reason.attributes('data-share-target')).toBe('folder');
    expect(reason.text()).toContain('does not have the team-graph capability');
    for (const id of ['context-publish-btn', 'context-promote-btn']) {
      const btn = w.find(`[data-testid=${id}]`);
      expect(btn.exists(), id).toBe(true);
      // FR-7 (D4 ruled 2026-10-05): the folder actions open the batch
      // dialog, which renders disabled with the same reason.
      expect((btn.element as HTMLButtonElement).disabled, id).toBe(false);
      await btn.trigger('click');
      await flushPromises();
      expect(w.find('[data-testid=folder-share-dialog]').attributes('data-disabled'), id).toBe('true');
      await w.find('[data-testid=folder-share-cancel]').trigger('click');
      await flushPromises();
    }
    expect(publishSpy).not.toHaveBeenCalled();
    expect(promoteSpy).not.toHaveBeenCalled();
    w.unmount();
  });

  it('folder selected, cap on → folder actions enabled, no reason shown', async () => {
    const { client } = provide({ tree, files: { 'kameas-ai/context.md': '# k' }, syncStatus: status(true) });
    const w = mount(ContextsView, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    await w.find('[data-testid="context-node-kameas-ai"]').trigger('click');
    await flushPromises();
    expect((w.find('[data-testid=context-publish-btn]').element as HTMLButtonElement).disabled).toBe(false);
    expect(w.find('[data-testid=context-publish-btn]').text()).toBe('Share folder…');
    expect(w.find('[data-testid=context-promote-btn]').text()).toBe('Promote folder…');
    expect(w.find('[data-testid=context-share-disabled-reason]').exists()).toBe(false);
    expect(w.find('[data-testid=context-share-account-link]').exists()).toBe(false);
    w.unmount();
  });

  it('selecting a folder clears the previewed file (review F6) and targets "+ Folder" at it', async () => {
    const { client } = provide({ tree, files: { 'kameas-ai/context.md': 'UNIQUE-PREVIEW-BODY' }, syncStatus: status(true) });
    const w = mount(ContextsView, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    await w.find('[data-testid="context-node-kameas-ai"]').trigger('click');
    await flushPromises();
    await w.find('[data-testid="context-node-kameas-ai/context.md"]').trigger('click');
    await flushPromises();
    expect(w.text()).toContain('UNIQUE-PREVIEW-BODY');
    // Click the folder again (collapses it) — the file preview must go.
    await w.find('[data-testid="context-node-kameas-ai"]').trigger('click');
    await flushPromises();
    expect(w.text()).not.toContain('UNIQUE-PREVIEW-BODY');
    // The sharing controls now act on the folder (FR-7 batch dialog).
    expect(w.find('[data-testid=context-publish-btn]').text()).toBe('Share folder…');
    await w.find('[data-testid=context-create-folder]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid=context-new-folder-row]').text()).toContain('kameas-ai');
    w.unmount();
  });

  it('cap off with a readable status names the possibilities, not one guessed cause (review F2)', async () => {
    const { client } = provide({ tree, files: { 'kameas-ai/context.md': '# k' }, syncStatus: status(false) });
    const w = mount(ContextsView, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    await w.find('[data-testid="context-node-kameas-ai"]').trigger('click');
    await flushPromises();
    const text = w.find('[data-testid=context-share-disabled-reason]').text();
    expect(text).toContain('does not have the team-graph capability');
    
    w.unmount();
  });

  it('then selecting a file in the folder → the file is shareable (cap on)', async () => {
    const { client } = provide({ tree, files: { 'kameas-ai/context.md': '# k' }, syncStatus: status(true) });
    const w = mount(ContextsView, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    await w.find('[data-testid="context-node-kameas-ai"]').trigger('click');
    await flushPromises();
    await w.find('[data-testid="context-node-kameas-ai/context.md"]').trigger('click');
    await flushPromises();
    expect((w.find('[data-testid=context-publish-btn]').element as HTMLButtonElement).disabled).toBe(false);
    expect((w.find('[data-testid=context-promote-btn]').element as HTMLButtonElement).disabled).toBe(false);
    expect(w.find('[data-testid=context-share-disabled-reason]').exists()).toBe(false);
    w.unmount();
  });
});

describe('ContextsView folder share/promote batch dialog (knowledge-home-01DOGF0E FR-7, P-6/P-7)', () => {
  // D4 ruled by the owner 2026-10-05: build the batch dialog. A context
  // module folder with a nested file and an empty (ineligible) file.
  const tree: ContextNode = {
    name: '',
    path: '',
    kind: 'folder',
    children: [
      {
        name: 'kameas-ai',
        path: 'kameas-ai',
        kind: 'folder',
        children: [
          { name: 'context.md', path: 'kameas-ai/context.md', kind: 'file', size: 3 },
          { name: 'agents.md', path: 'kameas-ai/agents.md', kind: 'file', size: 3 },
          {
            name: 'sub',
            path: 'kameas-ai/sub',
            kind: 'folder',
            children: [{ name: 'notes.md', path: 'kameas-ai/sub/notes.md', kind: 'file', size: 3 }],
          },
          // Zero-byte file in the REAL wire shape: Go's `size,omitempty`
          // drops the field entirely (review F2) — no hand-set size: 0.
          { name: 'empty.md', path: 'kameas-ai/empty.md', kind: 'file' },
        ],
      },
      { name: 'outside.md', path: 'outside.md', kind: 'file', size: 3 },
    ],
  };
  const files = {
    'kameas-ai/context.md': '# c',
    'kameas-ai/agents.md': '# a',
    'kameas-ai/sub/notes.md': '# n',
    'kameas-ai/empty.md': '',
    'outside.md': '# o',
  };
  const status = (cap: boolean): ContextSyncStatusView => ({
    cursor: '', last_pull_err: '', last_push_err: '', pull_count: 0, team_cap_enabled: cap,
  });

  async function openDialog(client: ReturnType<typeof provide>['client'], btn: 'context-publish-btn' | 'context-promote-btn') {
    const w = mount(ContextsView, { global: { provide: { [HarnessClientKey as symbol]: client } } });
    await flushPromises();
    await w.find('[data-testid="context-node-kameas-ai"]').trigger('click');
    await flushPromises();
    await w.find(`[data-testid=${btn}]`).trigger('click');
    await flushPromises();
    return w;
  }

  it('(a) lists the folder entries recursively and batches only the checked ones', async () => {
    const publishSpy = vi.fn(async (req: ContextPublishRequest): Promise<ContextPublishResult> => ({
      accepted_nodes: 1, accepted_edges: 0, conflicts: [], effective_layer: req.layer,
    }));
    const { client } = provide({ tree, files, syncStatus: status(true), publishSpy });
    const w = await openDialog(client, 'context-publish-btn');
    const dialog = w.find('[data-testid=folder-share-dialog]');
    expect(dialog.exists()).toBe(true);
    expect(dialog.attributes('data-mode')).toBe('share');
    expect(dialog.attributes('data-disabled')).toBe('false');
    // Every file under the folder, recursive, folder-relative paths; nothing outside it.
    const rows = w.findAll('[data-testid^=folder-share-entry-]');
    expect(rows.map((r) => r.attributes('data-testid'))).toEqual([
      'folder-share-entry-kameas-ai/context.md',
      'folder-share-entry-kameas-ai/agents.md',
      'folder-share-entry-kameas-ai/sub/notes.md',
      'folder-share-entry-kameas-ai/empty.md',
    ]);
    expect(w.find('[data-testid="folder-share-entry-kameas-ai/sub/notes.md"]').text()).toContain('sub/notes.md');
    // Default: all eligible checked; the empty file is ineligible, unchecked, with its reason inline.
    const check = (p: string) => w.find(`[data-testid="folder-share-check-${p}"]`).element as HTMLInputElement;
    expect(check('kameas-ai/context.md').checked).toBe(true);
    expect(check('kameas-ai/empty.md').checked).toBe(false);
    expect(check('kameas-ai/empty.md').disabled).toBe(true);
    expect(w.find('[data-testid="folder-share-ineligible-kameas-ai/empty.md"]').text()).toContain('Empty file');
    // Uncheck one entry, then confirm.
    await w.find('[data-testid="folder-share-check-kameas-ai/agents.md"]').setValue(false);
    expect(w.find('[data-testid=folder-share-confirm]').text()).toBe('Share 2 files');
    await w.find('[data-testid=folder-share-confirm]').trigger('click');
    await flushPromises();
    expect(publishSpy.mock.calls.map((c) => c[0].node_id)).toEqual([
      btoa('kameas-ai/context.md'),
      btoa('kameas-ai/sub/notes.md'),
    ]);
    expect(publishSpy.mock.calls[0][0]).toMatchObject({ layer: 'team', title: 'context', body: '# c' });
    expect(w.find('[data-testid=folder-share-summary]').text()).toContain('2 shared, 0 failed');
    expect(w.find('[data-testid="folder-share-entry-kameas-ai/agents.md"]').attributes('data-status')).toBe('pending');
    w.unmount();
  });

  it('(b) one entry failing leaves the others completed and the summary reports both', async () => {
    const promoteSpy = vi.fn(async (nodeID: string) => {
      if (nodeID === btoa('kameas-ai/agents.md')) throw new Error('entry not shared to team yet');
      return { updated_node_id: nodeID, new_classification: 'org_shared' as const };
    });
    const { client } = provide({ tree, files, syncStatus: status(true), promoteSpy });
    const w = await openDialog(client, 'context-promote-btn');
    expect(w.find('[data-testid=folder-share-dialog]').attributes('data-mode')).toBe('promote');
    await w.find('[data-testid=folder-share-confirm]').trigger('click');
    await flushPromises();
    // The failure did not abort the batch: every eligible entry was attempted.
    expect(promoteSpy).toHaveBeenCalledTimes(4);
    const st = (p: string) => w.find(`[data-testid="folder-share-entry-${p}"]`).attributes('data-status');
    expect(st('kameas-ai/context.md')).toBe('done');
    expect(st('kameas-ai/agents.md')).toBe('failed');
    expect(st('kameas-ai/sub/notes.md')).toBe('done');
    expect(st('kameas-ai/empty.md')).toBe('done');
    expect(w.find('[data-testid="folder-share-message-kameas-ai/agents.md"]').text()).toBe('promote failed: entry not shared to team yet');
    const summary = w.find('[data-testid=folder-share-summary]').text();
    expect(summary).toContain('3 promoted, 1 failed');
    expect(summary).toContain('agents.md — promote failed: entry not shared to team yet');
    w.unmount();
  });

  it('(c) capability off renders the dialog disabled with the session-derived reason', async () => {
    const publishSpy = vi.fn();
    const { client } = provide({ tree, files, syncStatus: status(false), publishSpy });
    // Signed out — the FleetSession store's sentence, not a folder-specific one.
    (client.settings as any).fleetSession = async () => fakeFleetSession({ state: 'signed_out' });
    const w = await openDialog(client, 'context-publish-btn');
    const dialog = w.find('[data-testid=folder-share-dialog]');
    expect(dialog.attributes('data-disabled')).toBe('true');
    const sentence = 'Sharing is off — you are signed out of fleet. Sign in with a team-graph-enabled account to share.';
    expect(w.find('[data-testid=folder-share-disabled-reason]').text()).toContain(sentence);
    // The same sentence the pane's reason paragraph shows (one gate, not two).
    expect(w.find('[data-testid=context-share-disabled-reason]').text()).toContain(sentence);
    // Entries still listed so the user sees what a share would cover — all controls inert.
    for (const cb of w.findAll('[data-testid^=folder-share-check-]')) {
      expect((cb.element as HTMLInputElement).disabled).toBe(true);
    }
    const confirm = w.find('[data-testid=folder-share-confirm]');
    expect((confirm.element as HTMLButtonElement).disabled).toBe(true);
    await confirm.trigger('click');
    await flushPromises();
    expect(publishSpy).not.toHaveBeenCalled();
    expect(w.find('[data-testid=folder-share-summary]').exists()).toBe(false);
    w.unmount();
  });

  it('(d) the interim "pending a product decision" folder copy is gone', async () => {
    for (const cap of [false, true]) {
      const { client } = provide({ tree, files, syncStatus: status(cap) });
      const w = mount(ContextsView, { global: { provide: { [HarnessClientKey as symbol]: client } } });
      await flushPromises();
      await w.find('[data-testid="context-node-kameas-ai"]').trigger('click');
      await flushPromises();
      expect(w.text(), `cap=${cap}`).not.toContain('pending a product decision');
      expect(w.text(), `cap=${cap}`).not.toContain('Sharing works per file today');
      expect(w.find('[data-testid=context-publish-btn]').text()).toBe('Share folder…');
      w.unmount();
    }
  });

  it('(F2) a zero-byte file arriving with NO size field is ineligible before the run', async () => {
    const publishSpy = vi.fn();
    const { client } = provide({ tree, files, syncStatus: status(true), publishSpy });
    const w = await openDialog(client, 'context-publish-btn');
    const empty = w.find('[data-testid="folder-share-check-kameas-ai/empty.md"]').element as HTMLInputElement;
    expect(empty.checked).toBe(false);
    expect(empty.disabled).toBe(true);
    expect(w.find('[data-testid="folder-share-ineligible-kameas-ai/empty.md"]').text()).toBe('Empty file — nothing to share.');
    expect(w.find('[data-testid=folder-share-confirm]').text()).toBe('Share 3 files');
    w.unmount();
  });

  it('(F3) failures name their stage and never print [object Object]', async () => {
    const publishSpy = vi.fn(async (req: ContextPublishRequest): Promise<ContextPublishResult> => {
      if (req.node_id === btoa('kameas-ai/context.md')) throw { code: 'conflict', node: 'c' };
      return { accepted_nodes: 1, accepted_edges: 0, conflicts: [], effective_layer: req.layer };
    });
    // agents.md unreadable: the fake get throws "not found: …".
    const { 'kameas-ai/agents.md': _drop, ...partial } = files;
    void _drop;
    const { client } = provide({ tree, files: partial, syncStatus: status(true), publishSpy });
    const w = await openDialog(client, 'context-publish-btn');
    await w.find('[data-testid=folder-share-confirm]').trigger('click');
    await flushPromises();
    const msg = (p: string) => w.find(`[data-testid="folder-share-message-${p}"]`).text();
    expect(msg('kameas-ai/context.md')).toBe('publish rejected: {"code":"conflict","node":"c"}');
    expect(msg('kameas-ai/agents.md')).toBe('read failed: not found: kameas-ai/agents.md');
    expect(w.find('[data-testid=folder-share-summary]').text()).toContain('1 shared, 2 failed');
    expect(w.text()).not.toContain('[object Object]');
    w.unmount();
  });

  it('(F7) ESC closes the dialog while choosing, and is ignored mid-batch', async () => {
    let release: (() => void) | null = null;
    const publishSpy = vi.fn(
      (req: ContextPublishRequest) =>
        new Promise<ContextPublishResult>((resolve) => {
          release = () => resolve({ accepted_nodes: 1, accepted_edges: 0, conflicts: [], effective_layer: req.layer });
        }),
    );
    const { client } = provide({ tree, files, syncStatus: status(true), publishSpy });
    const w = await openDialog(client, 'context-publish-btn');
    expect(w.find('[data-testid=folder-share-dialog]').attributes('aria-modal')).toBe('true');
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();
    expect(w.find('[data-testid=folder-share-dialog]').exists()).toBe(false);
    await w.find('[data-testid=context-publish-btn]').trigger('click');
    await flushPromises();
    await w.find('[data-testid=folder-share-confirm]').trigger('click');
    await flushPromises();
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    await flushPromises();
    expect(w.find('[data-testid=folder-share-dialog]').exists()).toBe(true);
    // Drain the batch.
    for (let i = 0; i < 3; i++) {
      release!();
      await flushPromises();
    }
    expect(w.find('[data-testid=folder-share-summary]').text()).toContain('3 shared, 0 failed');
    w.unmount();
  });

  describe('(F1) an outside tree change mid-run', () => {
    let handlers: Record<string, (p: unknown) => void>;
    const originalRuntime = (window as unknown as { runtime?: unknown }).runtime;
    beforeEach(() => {
      handlers = {};
      (window as unknown as { runtime: unknown }).runtime = {
        EventsOn: (topic: string, cb: (p: unknown) => void) => {
          handlers[topic] = cb;
          return () => delete handlers[topic];
        },
      };
    });
    afterEach(() => {
      (window as unknown as { runtime?: unknown }).runtime = originalRuntime;
    });

    it('keeps the dialog mounted, the batch completes, and the next folder click does not reopen it', async () => {
      const releases: Array<() => void> = [];
      const publishSpy = vi.fn(
        (req: ContextPublishRequest) =>
          new Promise<ContextPublishResult>((resolve) => {
            releases.push(() => resolve({ accepted_nodes: 1, accepted_edges: 0, conflicts: [], effective_layer: req.layer }));
          }),
      );
      const { client } = provide({ tree, files, syncStatus: status(true), publishSpy });
      const w = await openDialog(client, 'context-publish-btn');
      await w.find('[data-testid=folder-share-confirm]').trigger('click');
      await flushPromises();
      expect(publishSpy).toHaveBeenCalledTimes(1);
      // The folder is renamed away outside the app.
      const without: ContextNode = { ...tree, children: tree.children!.filter((c) => c.path !== 'kameas-ai') };
      (client.contexts as any).list = async () => without;
      handlers['contexts:tree-changed']?.(undefined);
      await flushPromises();
      expect(w.find('[data-testid="context-node-kameas-ai"]').exists()).toBe(false);
      // Still mounted, Stop still reachable, batch runs to the end.
      expect(w.find('[data-testid=folder-share-dialog]').exists()).toBe(true);
      expect(w.find('[data-testid=folder-share-cancel]').text()).toBe('Stop');
      while (releases.length > 0) {
        releases.shift()!();
        await flushPromises();
      }
      expect(publishSpy).toHaveBeenCalledTimes(3);
      expect(w.find('[data-testid=folder-share-summary]').text()).toContain('3 shared, 0 failed');
      await w.find('[data-testid=folder-share-cancel]').trigger('click');
      await flushPromises();
      expect(w.find('[data-testid=folder-share-dialog]').exists()).toBe(false);
      // The folder comes back; clicking it must NOT reopen a stale dialog.
      (client.contexts as any).list = async () => tree;
      handlers['contexts:tree-changed']?.(undefined);
      await flushPromises();
      await w.find('[data-testid="context-node-kameas-ai"]').trigger('click');
      await flushPromises();
      expect(w.find('[data-testid=folder-share-dialog]').exists()).toBe(false);
      w.unmount();
    });
  });

  it('cancel while running: the in-flight entry finishes, the rest are not started', async () => {
    let release: (() => void) | null = null;
    const publishSpy = vi.fn(
      (req: ContextPublishRequest) =>
        new Promise<ContextPublishResult>((resolve) => {
          release = () => resolve({ accepted_nodes: 1, accepted_edges: 0, conflicts: [], effective_layer: req.layer });
        }),
    );
    const { client } = provide({ tree, files, syncStatus: status(true), publishSpy });
    const w = await openDialog(client, 'context-publish-btn');
    await w.find('[data-testid=folder-share-confirm]').trigger('click');
    await flushPromises();
    expect(publishSpy).toHaveBeenCalledTimes(1);
    expect(w.find('[data-testid="folder-share-entry-kameas-ai/context.md"]').attributes('data-status')).toBe('running');
    // Stop does not close the dialog mid-entry.
    await w.find('[data-testid=folder-share-cancel]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid=folder-share-dialog]').exists()).toBe(true);
    release!();
    await flushPromises();
    expect(publishSpy).toHaveBeenCalledTimes(1);
    const st = (p: string) => w.find(`[data-testid="folder-share-entry-${p}"]`).attributes('data-status');
    expect(st('kameas-ai/context.md')).toBe('done');
    expect(st('kameas-ai/agents.md')).toBe('not_started');
    expect(st('kameas-ai/sub/notes.md')).toBe('not_started');
    expect(w.find('[data-testid=folder-share-summary]').text()).toContain('1 shared, 0 failed, 2 not started (cancelled)');
    w.unmount();
  });
});
