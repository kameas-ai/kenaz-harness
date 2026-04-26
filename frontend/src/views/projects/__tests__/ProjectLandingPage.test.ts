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
      get: async (id) => {
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
    },
    attachments: {
      list: opts.attachmentsList ?? (async () => [...attachments]),
      listResolved: async () => [],
      add: async () => attachments[0] ?? ({} as Attachment),
      remove: async () => undefined,
      reorder: async () => undefined,
      refresh: async () => attachments[0] ?? ({} as Attachment),
    },
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
});
