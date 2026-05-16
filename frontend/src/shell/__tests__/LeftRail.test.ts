import { describe, it, expect, vi } from 'vitest';
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

  it('moves a session to a project when a session row is dropped on the project header (WP07 T001)', async () => {
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
        id: 's-loose',
        name: 'loose',
        createdAt: '',
        updatedAt: '',
      },
    ];
    const moveCalls: { id: string; projectId: string }[] = [];
    const { w } = await mountRail({
      sessions: {
        list: async () => sessions,
        get: async (id) => sessions.find((s) => s.id === id) ?? sessions[0]!,
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
        moveToProject: async (id, projectId) => {
          moveCalls.push({ id, projectId });
        },
      },
      projects: {
        list: async () => projects,
        get: async () => projects[0]!,
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

    const sessionRow = w.find('[data-testid="session-row-s-loose"]');
    expect(sessionRow.exists()).toBe(true);

    const dataMap = new Map<string, string>();
    const dt = {
      effectAllowed: 'move',
      dropEffect: 'move',
      setData(k: string, v: string) {
        dataMap.set(k, v);
      },
      getData(k: string) {
        return dataMap.get(k) ?? '';
      },
    } as unknown as DataTransfer;

    await sessionRow.trigger('dragstart', { dataTransfer: dt });
    const projectRow = w.find('[data-testid="project-group-p1"] > div');
    expect(projectRow.exists()).toBe(true);
    await projectRow.trigger('dragover', { dataTransfer: dt });
    await projectRow.trigger('drop', { dataTransfer: dt });
    await flushPromises();

    expect(moveCalls).toEqual([{ id: 's-loose', projectId: 'p1' }]);
  });

  it('detaches a session when dropped on the Loose header (WP07 T001)', async () => {
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
    ];
    const moveCalls: { id: string; projectId: string }[] = [];
    const { w } = await mountRail({
      sessions: {
        list: async () => sessions,
        get: async (id) => sessions.find((s) => s.id === id) ?? sessions[0]!,
        create: async (name) => ({ id: 'x', name, createdAt: '', updatedAt: '' }),
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
        moveToProject: async (id, projectId) => {
          moveCalls.push({ id, projectId });
        },
      },
      projects: {
        list: async () => projects,
        get: async () => projects[0]!,
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

    const sessionRow = w.find('[data-testid="session-row-s-attached"]');
    expect(sessionRow.exists()).toBe(true);
    const dt = {
      effectAllowed: 'move',
      dropEffect: 'move',
      setData() { /* noop */ },
      getData() { return ''; },
    } as unknown as DataTransfer;

    await sessionRow.trigger('dragstart', { dataTransfer: dt });
    const looseHeader = w.find('[data-testid="loose-header"]');
    expect(looseHeader.exists()).toBe(true);
    await looseHeader.trigger('dragover', { dataTransfer: dt });
    await looseHeader.trigger('drop', { dataTransfer: dt });
    await flushPromises();

    // Empty projectId means "loose".
    expect(moveCalls).toEqual([{ id: 's-attached', projectId: '' }]);
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

// ── WP05: auto-title rail distinction + clear-title flow ─────────────────────

describe('LeftRail (WP05 auto-title rail distinction)', () => {
  it('auto-titled session span carries session-row__name--auto class', async () => {
    const sessions: Session[] = [
      {
        id: 'auto-s',
        name: 'Some engine title',
        createdAt: '',
        updatedAt: '',
        autoTitled: true,
      },
    ];
    const { w } = await mountRail({
      sessions: {
        list: async () => sessions,
        get: async (id) => sessions.find((s) => s.id === id) ?? sessions[0]!,
        create: async (name) => ({ id: 'new', name, createdAt: '', updatedAt: '' }),
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
        saveAsArtifact: async () => ({} as any),
        suggestTitle: async () => 'New title',
        clearTitle: async () => undefined,
      },
    });
    const span = w.find('[data-testid="auto-titled-name-auto-s"]');
    expect(span.exists()).toBe(true);
    expect(span.classes()).toContain('session-row__name--auto');
  });

  it('user-set title session span does NOT have session-row__name--auto class', async () => {
    const sessions: Session[] = [
      {
        id: 'user-s',
        name: 'My custom title',
        createdAt: '',
        updatedAt: '',
        autoTitled: false,
      },
    ];
    const { w } = await mountRail({
      sessions: {
        list: async () => sessions,
        get: async (id) => sessions.find((s) => s.id === id) ?? sessions[0]!,
        create: async (name) => ({ id: 'new', name, createdAt: '', updatedAt: '' }),
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
        saveAsArtifact: async () => ({} as any),
        suggestTitle: async () => 'New title',
        clearTitle: async () => undefined,
      },
    });
    const row = w.find('[data-testid="open-session-user-s"]');
    expect(row.exists()).toBe(true);
    // No auto-titled data-testid attribute should be present.
    expect(w.find('[data-testid="auto-titled-name-user-s"]').exists()).toBe(false);
  });

  it('omitting autoTitled field does not add session-row__name--auto class', async () => {
    const sessions: Session[] = [
      {
        id: 'plain-s',
        name: 'Plain session',
        createdAt: '',
        updatedAt: '',
        // autoTitled not set — undefined
      },
    ];
    const { w } = await mountRail({
      sessions: {
        list: async () => sessions,
        get: async (id) => sessions.find((s) => s.id === id) ?? sessions[0]!,
        create: async (name) => ({ id: 'new', name, createdAt: '', updatedAt: '' }),
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
        saveAsArtifact: async () => ({} as any),
        suggestTitle: async () => 'New title',
        clearTitle: async () => undefined,
      },
    });
    expect(w.find('[data-testid="auto-titled-name-plain-s"]').exists()).toBe(false);
  });

  // ── WP04 (a11y-backlog-cleanup-01NDFSEX07) — ⋯ icon-button keyboard affordance ──

  it('renders ⋯ project-options button for each project (WP04)', async () => {
    const projects: Project[] = [
      {
        id: 'pw4',
        name: 'WP04 Project',
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
        get: async () => projects[0]!,
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
    const optionsBtn = w.find('[data-testid="project-options-pw4"]');
    expect(optionsBtn.exists()).toBe(true);
    expect(optionsBtn.attributes('aria-label')).toBe('Project options for WP04 Project');
    expect(optionsBtn.attributes('aria-haspopup')).toBe('menu');
  });

  it('⋯ button opens project menu on click (WP04)', async () => {
    const projects: Project[] = [
      {
        id: 'pw4b',
        name: 'WP04 Click',
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
        get: async () => projects[0]!,
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
    // Menu hidden before click
    expect(w.find('[data-testid="project-menu"]').exists()).toBe(false);
    // Click the ⋯ button
    await w.find('[data-testid="project-options-pw4b"]').trigger('click');
    await nextTick();
    // Menu appears
    expect(w.find('[data-testid="project-menu"]').exists()).toBe(true);
    expect(w.find('[data-testid="project-menu-rename-pw4b"]').exists()).toBe(true);
    expect(w.find('[data-testid="project-menu-delete-pw4b"]').exists()).toBe(true);
  });

  // ── WP05 (a11y-backlog-cleanup-01NDFSEX07) — Move to project keyboard affordance ──

  it('renders move-to-project button when projects exist (WP05)', async () => {
    const projects: Project[] = [
      {
        id: 'p-move',
        name: 'Move Target',
        description: '',
        createdAt: '',
        updatedAt: '',
      },
    ];
    const sessions: Session[] = [
      {
        id: 's-move',
        name: 'session to move',
        createdAt: '',
        updatedAt: '',
        projectId: 'p-move',
      },
    ];
    const { w } = await mountRail({
      sessions: {
        list: async () => sessions,
        get: async (id) => sessions.find((s) => s.id === id) ?? sessions[0]!,
        create: async (name) => ({ id: 'x', name, createdAt: '', updatedAt: '' }),
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
        get: async () => projects[0]!,
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
    const moveBtn = w.find('[data-testid="move-session-s-move"]');
    expect(moveBtn.exists()).toBe(true);
    expect(moveBtn.attributes('aria-haspopup')).toBe('menu');
  });

  it('clicking move button opens move-session-menu (WP05)', async () => {
    const projects: Project[] = [
      {
        id: 'p-a',
        name: 'Project A',
        description: '',
        createdAt: '',
        updatedAt: '',
      },
      {
        id: 'p-b',
        name: 'Project B',
        description: '',
        createdAt: '',
        updatedAt: '',
      },
    ];
    const sessions: Session[] = [
      {
        id: 's-x',
        name: 'session x',
        createdAt: '',
        updatedAt: '',
        projectId: 'p-a',
      },
    ];
    const moveCalls: { id: string; projectId: string }[] = [];
    const { w } = await mountRail({
      sessions: {
        list: async () => sessions,
        get: async (id) => sessions.find((s) => s.id === id) ?? sessions[0]!,
        create: async (name) => ({ id: 'x', name, createdAt: '', updatedAt: '' }),
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
        moveToProject: async (id, projectId) => {
          moveCalls.push({ id, projectId });
        },
      },
      projects: {
        list: async () => projects,
        get: async () => projects[0]!,
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
    // Menu hidden initially
    expect(w.find('[data-testid="move-session-menu"]').exists()).toBe(false);
    // Click move button
    await w.find('[data-testid="move-session-s-x"]').trigger('click');
    await nextTick();
    // Menu appears
    expect(w.find('[data-testid="move-session-menu"]').exists()).toBe(true);
    // Project B is available (not Project A since session is already there)
    expect(w.find('[data-testid="move-session-to-p-b"]').exists()).toBe(true);
    expect(w.find('[data-testid="move-session-to-p-a"]').exists()).toBe(false);
    // "Global" option should appear (session is in a project)
    expect(w.find('[data-testid="move-session-to-global"]').exists()).toBe(true);
    // Click move to Project B
    await w.find('[data-testid="move-session-to-p-b"]').trigger('click');
    await flushPromises();
    expect(moveCalls).toEqual([{ id: 's-x', projectId: 'p-b' }]);
    // Menu closes after move
    expect(w.find('[data-testid="move-session-menu"]').exists()).toBe(false);
  });

  it('empty rename submission calls clearTitle rather than renameSession', async () => {
    const clearTitleMock = vi.fn().mockResolvedValue(undefined);
    const renameMock = vi.fn().mockResolvedValue(undefined);
    const sessions: Session[] = [
      {
        id: 'ct-s',
        name: 'Clear me',
        createdAt: '',
        updatedAt: '',
        autoTitled: true,
      },
    ];
    const { w } = await mountRail({
      sessions: {
        list: async () => sessions,
        get: async (id) => sessions.find((s) => s.id === id) ?? sessions[0]!,
        create: async (name) => ({ id: 'new', name, createdAt: '', updatedAt: '' }),
        rename: renameMock,
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
        saveAsArtifact: async () => ({} as any),
        suggestTitle: async () => 'New title',
        clearTitle: clearTitleMock,
      },
    });

    // Open the rename input.
    await w.find('[data-testid="rename-session-ct-s"]').trigger('click');
    await nextTick();

    // Submit with empty value.
    const input = w.find<HTMLInputElement>('[data-testid="rename-session-input-ct-s"]');
    expect(input.exists()).toBe(true);
    await input.setValue('');
    await input.trigger('keydown.enter');
    await flushPromises();

    expect(clearTitleMock).toHaveBeenCalledWith('ct-s');
    expect(renameMock).not.toHaveBeenCalled();
  });
});
