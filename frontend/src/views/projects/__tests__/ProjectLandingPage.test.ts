import { describe, it, expect, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import ProjectLandingPage from '@/views/projects/ProjectLandingPage.vue';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import { HarnessClientKey } from '@/lib/harnessClientContext';
import type { Attachment, Project, Session } from '@/lib/types';

function setup(opts: {
  projectId: string;
  project?: Project | null;
  sessions?: Session[];
  getError?: Error;
  attachments?: Attachment[];
  attachmentsList?: ReturnType<typeof vi.fn>;
}) {
  const project = opts.project;
  const sessions = opts.sessions ?? [];
  const attachments = opts.attachments ?? [];
  const client = createFakeHarnessClient({
    projects: {
      list: async () => [],
      get: async (id: string) => {
        if (opts.getError) throw opts.getError;
        if (project && project.id === id) return project;
        throw new Error('not found');
      },
      create: async () => ({
        id: 'p',
        name: 'p',
        description: '',
        createdAt: '',
        updatedAt: '',
      }),
      rename: async () => undefined,
      updateDescription: async () => undefined,
      remove: async () => undefined,
      addSession: async () => undefined,
      removeSession: async () => undefined,
      listSessions: async () => sessions,
    } as any,
    attachments: {
      list: opts.attachmentsList ?? (async () => [...attachments]),
      listResolved: async () => [],
      add: async () => attachments[0] ?? ({} as Attachment),
      remove: async () => undefined,
      reorder: async () => undefined,
      refresh: async () => attachments[0] ?? ({} as Attachment),
    } as any,
  });
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/projects/:id',
        name: 'project',
        component: ProjectLandingPage,
      },
      { path: '/sessions/:id?', name: 'sessions', component: { template: '<div />' } },
    ],
  });
  return { client, router };
}

describe('ProjectLandingPage', () => {
  it('renders the project header and sessions list', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Foo',
      description: 'the foo project',
      createdAt: '2026-04-26T00:00:00Z',
      updatedAt: '2026-04-26T00:00:00Z',
    };
    const sessions: Session[] = [
      {
        id: 's1',
        name: 'first chat',
        createdAt: '',
        updatedAt: '',
        projectId: 'p1',
      },
      {
        id: 's2',
        name: 'second chat',
        createdAt: '',
        updatedAt: '',
        projectId: 'p1',
      },
    ];
    const { client, router } = setup({ projectId: 'p1', project, sessions });
    await router.push('/projects/p1');
    await router.isReady();

    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    expect(w.text()).toContain('Foo');
    expect(w.text()).toContain('the foo project');
    const list = w.find('[data-testid=project-sessions-list]');
    expect(list.exists()).toBe(true);
    expect(w.text()).toContain('first chat');
    expect(w.text()).toContain('second chat');
  });

  it('renders the empty-sessions hint when the project has no sessions', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Empty',
      description: '',
      createdAt: '',
      updatedAt: '',
    };
    const { client, router } = setup({ projectId: 'p1', project, sessions: [] });
    await router.push('/projects/p1');
    await router.isReady();

    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    const empty = w.find('[data-testid=project-sessions-empty]');
    expect(empty.exists()).toBe(true);
  });

  it('surfaces an error banner when the project lookup fails', async () => {
    const { client, router } = setup({
      projectId: 'ghost',
      getError: new Error('not found'),
    });
    await router.push('/projects/ghost');
    await router.isReady();

    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    const err = w.find('[data-testid=project-error]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('not found');
  });

  it('renders the project-context section with attachments scoped to the project (A2)', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Foo',
      description: '',
      createdAt: '',
      updatedAt: '',
    };
    const attachments: Attachment[] = [
      {
        id: 'a-1',
        scopeKind: 'project',
        scopeId: 'p1',
        contentSource: 'library:briefs/p1.md',
        content: 'project brief',
        kind: 'system',
        position: 0,
        createdAt: '',
      },
    ];
    const list = vi.fn(async (filter: { scopeKind: string; scopeId?: string }) => {
      expect(filter.scopeKind).toBe('project');
      expect(filter.scopeId).toBe('p1');
      return [...attachments];
    });
    const { client, router } = setup({
      projectId: 'p1',
      project,
      attachments,
      attachmentsList: list,
    });
    await router.push('/projects/p1');
    await router.isReady();

    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    const section = w.find('[data-testid=project-context-section]');
    expect(section.exists()).toBe(true);
    expect(list).toHaveBeenCalled();
    const list2 = w.find('[data-testid=project-attachments-list]');
    expect(list2.exists()).toBe(true);
    expect(w.text()).toContain('briefs/p1.md');
    // Add-context affordance is present.
    expect(w.find('[data-testid=project-add-context]').exists()).toBe(true);
  });

  it('renders the empty-attachments hint when the project has no attachments', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Empty',
      description: '',
      createdAt: '',
      updatedAt: '',
    };
    const { client, router } = setup({
      projectId: 'p1',
      project,
      attachments: [],
    });
    await router.push('/projects/p1');
    await router.isReady();

    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    const empty = w.find('[data-testid=project-attachments-empty]');
    expect(empty.exists()).toBe(true);
  });

  // ── WP07 T002 polish coverage ───────────────────────────────────────

  it('renders the editable description editor when Edit is clicked (WP07 T002)', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Foo',
      description: 'original',
      createdAt: '',
      updatedAt: '',
    };
    const updateDescription = vi.fn(async () => undefined);
    const client = createFakeHarnessClient({
      projects: {
        list: async () => [],
        get: async () => project,
        create: async () => ({ id: 'p', name: 'p', description: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        updateDescription,
        remove: async () => undefined,
        addSession: async () => undefined,
        removeSession: async () => undefined,
        listSessions: async () => [],
      } as any,
    });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/projects/:id', name: 'project', component: ProjectLandingPage },
        { path: '/sessions/:id?', name: 'sessions', component: { template: '<div />' } },
        { path: '/memory', name: 'memory', component: { template: '<div />' } },
      ],
    });
    await router.push('/projects/p1');
    await router.isReady();
    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    expect(w.find('[data-testid=project-description-text]').exists()).toBe(true);
    await w.find('[data-testid=project-edit-description]').trigger('click');
    await flushPromises();
    const ta = w.find<HTMLTextAreaElement>('[data-testid=project-description-input]');
    expect(ta.exists()).toBe(true);
    await ta.setValue('rewritten description');
    await w.find('[data-testid=project-description-save]').trigger('click');
    await flushPromises();
    expect(updateDescription).toHaveBeenCalledWith('p1', 'rewritten description');
  });

  it('renders project-scope memory count and a deep-link to /memory (WP07 T002)', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Foo',
      description: '',
      createdAt: '',
      updatedAt: '',
    };
    const listChunks = vi.fn(async () => [
      { id: 'm1', scopeKind: 'project', scopeId: 'p1', content: 'x', createdAt: '' },
      { id: 'm2', scopeKind: 'project', scopeId: 'p1', content: 'y', createdAt: '' },
    ]);
    const client = createFakeHarnessClient({
      projects: {
        list: async () => [],
        get: async () => project,
        create: async () => ({ id: 'p', name: 'p', description: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        updateDescription: async () => undefined,
        remove: async () => undefined,
        addSession: async () => undefined,
        removeSession: async () => undefined,
        listSessions: async () => [],
      } as any,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      memory: {
        listChunks,
        rememberMessage: async () => 'mem-id',
        promoteScope: async () => 'mem-id',
        forget: async () => undefined,
      } as any,
    });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/projects/:id', name: 'project', component: ProjectLandingPage },
        { path: '/sessions/:id?', name: 'sessions', component: { template: '<div />' } },
        { path: '/memory', name: 'memory', component: { template: '<div />' } },
      ],
    });
    await router.push('/projects/p1');
    await router.isReady();
    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    expect(w.find('[data-testid=project-memory-section]').exists()).toBe(true);
    // count chip shows "2"
    expect(w.text()).toContain('(2)');
    expect(listChunks).toHaveBeenCalledWith({ scopeKind: 'project', scopeId: 'p1' });
    await w.find('[data-testid=project-memory-link]').trigger('click');
    await flushPromises();
    expect(router.currentRoute.value.path).toBe('/memory');
    expect(router.currentRoute.value.query.scopeKind).toBe('project');
    expect(router.currentRoute.value.query.scopeId).toBe('p1');
  });

  // ── FR-021: project-scope "New context folder" affordance ─────────────

  it('shows the "New folder" button next to "Add context" in the project context section (FR-021)', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Foo',
      description: '',
      createdAt: '',
      updatedAt: '',
    };
    const { client, router } = setup({ projectId: 'p1', project });
    await router.push('/projects/p1');
    await router.isReady();
    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    expect(w.find('[data-testid=project-new-context-folder]').exists()).toBe(true);
    expect(w.find('[data-testid=project-add-context]').exists()).toBe(true);
  });

  it('reveals the inline folder-name input when "New folder" is clicked (FR-021)', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Foo',
      description: '',
      createdAt: '',
      updatedAt: '',
    };
    const { client, router } = setup({ projectId: 'p1', project });
    await router.push('/projects/p1');
    await router.isReady();
    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    // Input hidden initially.
    expect(w.find('[data-testid=project-new-folder-row]').exists()).toBe(false);
    await w.find('[data-testid=project-new-context-folder]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid=project-new-folder-row]').exists()).toBe(true);
    expect(w.find('[data-testid=project-new-folder-input]').exists()).toBe(true);
  });

  it('calls createFolder + attachments.add and reloads attachments on Enter (FR-021)', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Foo',
      description: '',
      createdAt: '',
      updatedAt: '',
    };
    const createFolder = vi.fn(async () => undefined);
    const addAttachment = vi.fn(async () => ({
      id: 'att-new',
      scopeKind: 'project' as const,
      scopeId: 'p1',
      contentSource: 'library:my-folder',
      content: '',
      kind: 'system' as const,
      position: 0,
      createdAt: '',
    }));
    const client = createFakeHarnessClient({
      projects: {
        list: async () => [],
        get: async () => project,
        create: async () => ({ id: 'p', name: 'p', description: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        updateDescription: async () => undefined,
        remove: async () => undefined,
        addSession: async () => undefined,
        removeSession: async () => undefined,
        listSessions: async () => [],
      } as any,
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
      } as any,
      attachments: {
        list: async () => [],
        listResolved: async () => [],
        add: addAttachment,
        remove: async () => undefined,
        reorder: async () => undefined,
        refresh: async () => ({} as Attachment),
      } as any,
    });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/projects/:id', name: 'project', component: ProjectLandingPage },
        { path: '/sessions/:id?', name: 'sessions', component: { template: '<div />' } },
      ],
    });
    await router.push('/projects/p1');
    await router.isReady();
    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    await w.find('[data-testid=project-new-context-folder]').trigger('click');
    await flushPromises();
    const input = w.find<HTMLInputElement>('[data-testid=project-new-folder-input]');
    await input.setValue('my-folder');
    await input.trigger('keydown.enter');
    await flushPromises();

    expect(createFolder).toHaveBeenCalledWith('my-folder');
    expect(addAttachment).toHaveBeenCalledWith({
      scopeKind: 'project',
      scopeId: 'p1',
      contentSource: 'library:my-folder',
      content: '',
    });
    // Input dismissed after create.
    expect(w.find('[data-testid=project-new-folder-row]').exists()).toBe(false);
  });

  it('surfaces an error and keeps the form open when createFolder fails', async () => {
    const project: Project = {
      id: 'p1', name: 'Foo', description: '', createdAt: '', updatedAt: '',
    };
    const client = createFakeHarnessClient({
      projects: {
        list: async () => [],
        get: async () => project,
        create: async () => ({ id: 'p', name: 'p', description: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        updateDescription: async () => undefined,
        remove: async () => undefined,
        addSession: async () => undefined,
        removeSession: async () => undefined,
        listSessions: async () => [],
      } as any,
      contexts: {
        list: async () => ({ name: '', path: '', kind: 'folder' as const }),
        listAll: async () => ({ name: '', path: '', kind: 'folder' as const }),
        get: async () => '',
        save: async () => undefined,
        createFolder: async () => {
          throw new Error('disk full');
        },
        rename: async () => undefined,
        delete: async () => undefined,
        recentlyApplied: async () => [],
        rootPath: async () => '/tmp/contexts',
      } as any,
    });
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/projects/:id', name: 'project', component: ProjectLandingPage },
        { path: '/sessions/:id?', name: 'sessions', component: { template: '<div />' } },
      ],
    });
    await router.push('/projects/p1');
    await router.isReady();
    const w = mount(ProjectLandingPage, {
      global: { plugins: [router], provide: { [HarnessClientKey as symbol]: client } },
    });
    await flushPromises();

    await w.find('[data-testid=project-new-context-folder]').trigger('click');
    await flushPromises();
    const input = w.find<HTMLInputElement>('[data-testid=project-new-folder-input]');
    await input.setValue('bad-folder');
    await input.trigger('keydown.enter');
    await flushPromises();

    // The error must be VISIBLE (form stays open) — not silently swallowed.
    const err = w.find('[data-testid=project-new-folder-error]');
    expect(err.exists()).toBe(true);
    expect(err.text()).toContain('disk full');
    expect(w.find('[data-testid=project-new-folder-row]').exists()).toBe(true);
  });

  it('dismisses the inline input when Esc is pressed (FR-021)', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Foo',
      description: '',
      createdAt: '',
      updatedAt: '',
    };
    const { client, router } = setup({ projectId: 'p1', project });
    await router.push('/projects/p1');
    await router.isReady();
    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    await w.find('[data-testid=project-new-context-folder]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid=project-new-folder-row]').exists()).toBe(true);
    await w.find('[data-testid=project-new-folder-input]').trigger('keydown.esc');
    await flushPromises();
    expect(w.find('[data-testid=project-new-folder-row]').exists()).toBe(false);
  });

  it('renders sessions sorted by lastActiveAt with the start-session CTA (WP07 T002)', async () => {
    const project: Project = {
      id: 'p1',
      name: 'Foo',
      description: '',
      createdAt: '',
      updatedAt: '',
    };
    const sessions: Session[] = [
      {
        id: 's-old',
        name: 'older',
        createdAt: '2026-04-20T00:00:00Z',
        updatedAt: '',
        lastActiveAt: '2026-04-20T00:00:00Z',
        projectId: 'p1',
      },
      {
        id: 's-new',
        name: 'newer',
        createdAt: '2026-04-25T00:00:00Z',
        updatedAt: '',
        lastActiveAt: '2026-04-25T00:00:00Z',
        projectId: 'p1',
      },
    ];
    const { client, router } = setup({ projectId: 'p1', project, sessions });
    await router.push('/projects/p1');
    await router.isReady();
    const w = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();

    expect(w.find('[data-testid=project-new-session]').exists()).toBe(true);
    const rows = w.findAll('[data-testid^="project-session-row-"]');
    expect(rows.length).toBe(2);
    // Newest session first.
    expect(rows[0].attributes('data-testid')).toBe('project-session-row-s-new');
    expect(rows[1].attributes('data-testid')).toBe('project-session-row-s-old');
  });
});
