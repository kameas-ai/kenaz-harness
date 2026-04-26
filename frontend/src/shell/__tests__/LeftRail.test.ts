import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { Project, Session } from '@/lib/types';

async function mountRail(seed: Partial<HarnessClient> = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/sessions/:id?',
        name: 'sessions',
        component: defineComponent({ render: () => h('div', 'sessions') }),
      },
      {
        path: '/projects/:id',
        name: 'project',
        component: defineComponent({ render: () => h('div', 'project') }),
      },
    ],
  });
  await router.push('/sessions');
  await router.isReady();
  const w = mount(LeftRail, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
  await flushPromises();
  await nextTick();
  return { w, router };
}

describe('LeftRail (project grouping)', () => {
  it('renders sessions grouped under their project header', async () => {
    const projects: Project[] = [
      {
        id: 'p1',
        name: 'Alpha',
        description: '',
        createdAt: '',
        updatedAt: '',
      },
    ];
    const sessions: Session[] = [
      {
        id: 's-attached',
        name: 'attached',
        createdAt: '',
        updatedAt: '',
        projectId: 'p1',
      },
      {
        id: 's-loose',
        name: 'loose',
        createdAt: '',
        updatedAt: '',
      },
    ];
    const { w } = await mountRail({
      sessions: {
        list: async () => sessions,
        get: async (id) => sessions.find((s) => s.id === id) ?? sessions[0],
        create: async (name) => ({ id: 'x', name, createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) => ({
          id: 'm1',
          sessionId: id,
          role,
          content,
          createdAt: '',
        }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
        setSystemPrompt: async () => undefined,
        moveToProject: async () => undefined,
      },
      projects: {
        list: async () => projects,
        get: async (id) => projects[0]!,
        create: async (name) => ({
          id: 'np',
          name,
          description: '',
          createdAt: '',
          updatedAt: '',
        }),
        rename: async () => undefined,
        updateDescription: async () => undefined,
        remove: async () => undefined,
        addSession: async () => undefined,
        removeSession: async () => undefined,
        listSessions: async () => [],
      },
    });
    expect(w.find('[data-testid="project-group-p1"]').exists()).toBe(true);
    expect(w.find('[data-testid="project-header-p1"]').exists()).toBe(true);
    expect(w.find('[data-testid="open-session-s-attached"]').exists()).toBe(true);
    expect(w.find('[data-testid="loose-header"]').exists()).toBe(true);
    expect(w.find('[data-testid="open-session-s-loose"]').exists()).toBe(true);
  });

  it('opens an inline name prompt and creates a project on enter', async () => {
    const created: string[] = [];
    let projectsList: Project[] = [];
    const { w } = await mountRail({
      sessions: {
        list: async () => [],
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async (name) => ({ id: 's1', name, createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) => ({
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
      },
      projects: {
        list: async () => projectsList,
        get: async () => ({
          id: '',
          name: '',
          description: '',
          createdAt: '',
          updatedAt: '',
        }),
        create: async (name) => {
          created.push(name);
          const p: Project = {
            id: 'p-new',
            name,
            description: '',
            createdAt: '',
            updatedAt: '',
          };
          projectsList = [p];
          return p;
        },
        rename: async () => undefined,
        updateDescription: async () => undefined,
        remove: async () => undefined,
        addSession: async () => undefined,
        removeSession: async () => undefined,
        listSessions: async () => [],
      },
    });

    await w.find('[data-testid="new-project"]').trigger('click');
    await nextTick();
    const input = w.find<HTMLInputElement>('[data-testid="new-project-input"]');
    expect(input.exists()).toBe(true);
    await input.setValue('Bravo');
    await input.trigger('keydown.enter');
    await flushPromises();
    expect(created).toEqual(['Bravo']);
  });

  it('opens the rename / delete menu on right-click of a project header', async () => {
    const projects: Project[] = [
      {
        id: 'p1',
        name: 'Alpha',
        description: '',
        createdAt: '',
        updatedAt: '',
      },
    ];
    const { w } = await mountRail({
      sessions: {
        list: async () => [],
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async (name) => ({ id: 's', name, createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) => ({
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
      },
      projects: {
        list: async () => projects,
        get: async (id) => projects[0]!,
        create: async (name) => ({
          id: 'np',
          name,
          description: '',
          createdAt: '',
          updatedAt: '',
        }),
        rename: async () => undefined,
        updateDescription: async () => undefined,
        remove: async () => undefined,
        addSession: async () => undefined,
        removeSession: async () => undefined,
        listSessions: async () => [],
      },
    });
    const header = w.find('[data-testid="project-header-p1"]');
    await header.trigger('contextmenu');
    await nextTick();
    expect(w.find('[data-testid="project-menu"]').exists()).toBe(true);
    expect(w.find('[data-testid="project-menu-rename-p1"]').exists()).toBe(true);
    expect(w.find('[data-testid="project-menu-delete-p1"]').exists()).toBe(true);
  });

  it('renders the cascade-delete checkbox in the delete-project modal', async () => {
    const projects: Project[] = [
      {
        id: 'p1',
        name: 'Doomed',
        description: '',
        createdAt: '',
        updatedAt: '',
      },
    ];
    const removeCalls: { id: string; cascade: boolean }[] = [];
    const { w } = await mountRail({
      sessions: {
        list: async () => [],
        get: async (id) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async (name) => ({ id: 's', name, createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id, role, content) => ({
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
      },
      projects: {
        list: async () => projects,
        get: async (id) => projects[0]!,
        create: async (name) => ({
          id: 'np',
          name,
          description: '',
          createdAt: '',
          updatedAt: '',
        }),
        rename: async () => undefined,
        updateDescription: async () => undefined,
        remove: async (id, cascade) => {
          removeCalls.push({ id, cascade });
        },
        addSession: async () => undefined,
        removeSession: async () => undefined,
        listSessions: async () => [],
      },
    });

    await w.find('[data-testid="project-header-p1"]').trigger('contextmenu');
    await nextTick();
    await w.find('[data-testid="project-menu-delete-p1"]').trigger('click');
    await nextTick();
    expect(w.find('[data-testid="delete-project-modal"]').exists()).toBe(true);
    const cb = w.find<HTMLInputElement>('[data-testid="delete-project-cascade"]');
    expect(cb.exists()).toBe(true);
    await cb.setValue(true);
    await w.find('[data-testid="delete-project-confirm"]').trigger('click');
    await flushPromises();
    expect(removeCalls).toEqual([{ id: 'p1', cascade: true }]);
  });
});
