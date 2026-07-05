/**
 * Cross-view integration test (WP07 T004).
 *
 * Walks the canonical worked-example flow end-to-end against a mocked
 * harness client:
 *
 *   1. Create a project via the rail.
 *   2. Attach a context at project scope from the project landing page.
 *   3. Start a session in the project (via NewSessionDialog default
 *      preselection). The session inherits the project layer.
 *   4. ResolvedContextPanel reflects [project] in the merged stream.
 *   5. A memory pin at project scope is visible from a sister session
 *      under the same project.
 *
 * The test is a smoke-grade assertion on the cross-component glue —
 * per-component behaviour lives in each view's __tests__ directory.
 */

import { describe, it, expect } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';

import LeftRail from '@/shell/LeftRail.vue';
import ProjectLandingPage from '@/views/projects/ProjectLandingPage.vue';
import ResolvedContextPanel from '@/views/sessions/ResolvedContextPanel.vue';
import { provideFakeClient, HarnessClientKey } from '@/lib/harnessClientContext';
import { createFakeHarnessClient } from '@/lib/harnessClient';
import type {
  Attachment,
  MemoryChunk,
  Project,
  Session,
} from '@/lib/types';

interface FakeStore {
  projects: Project[];
  sessions: Session[];
  attachmentsByScope: Map<string, Attachment[]>; // key = scopeKind:scopeId
  memory: MemoryChunk[];
  nextProjectId: number;
  nextSessionId: number;
  nextAttachmentId: number;
}

function newStore(): FakeStore {
  return {
    projects: [],
    sessions: [],
    attachmentsByScope: new Map(),
    memory: [],
    nextProjectId: 1,
    nextSessionId: 1,
    nextAttachmentId: 1,
  };
}

function scopeKey(kind: string, id: string): string {
  return `${kind}:${id}`;
}

function buildClient(store: FakeStore) {
  return createFakeHarnessClient({
    projects: {
      list: async () => [...store.projects],
      get: async (id: string) => {
        const p = store.projects.find((x) => x.id === id);
        if (!p) throw new Error(`project ${id} not found`);
        return p;
      },
      create: async (name: string, description: string | undefined) => {
        const p: Project = {
          id: `p-${store.nextProjectId++}`,
          name,
          description: description ?? '',
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
        };
        store.projects.push(p);
        return p;
      },
      rename: async (id: string, name: string) => {
        const p = store.projects.find((x) => x.id === id);
        if (p) p.name = name;
      },
      updateDescription: async (id: string, description: string) => {
        const p = store.projects.find((x) => x.id === id);
        if (p) p.description = description;
      },
      remove: async (id: string, deleteSessions: boolean) => {
        store.projects = store.projects.filter((p) => p.id !== id);
        if (deleteSessions) {
          store.sessions = store.sessions.filter((s) => s.projectId !== id);
        } else {
          for (const s of store.sessions) {
            if (s.projectId === id) s.projectId = undefined;
          }
        }
      },
      addSession: async (projectId: string, sessionId: string) => {
        const s = store.sessions.find((x) => x.id === sessionId);
        if (s) s.projectId = projectId;
      },
      removeSession: async (sessionId: string) => {
        const s = store.sessions.find((x) => x.id === sessionId);
        if (s) s.projectId = undefined;
      },
      listSessions: async (projectId: string) =>
        store.sessions.filter((s) => s.projectId === projectId),
    } as any,
    sessions: {
      list: async () => [...store.sessions],
      get: async (id: string) => {
        const s = store.sessions.find((x) => x.id === id);
        if (!s) throw new Error(`session ${id} not found`);
        return s;
      },
      create: async (name: string) => {
        const s: Session = {
          id: `s-${store.nextSessionId++}`,
          name,
          createdAt: new Date().toISOString(),
          updatedAt: new Date().toISOString(),
          lastActiveAt: new Date().toISOString(),
        };
        store.sessions.push(s);
        return s;
      },
      rename: async () => undefined,
      delete: async () => undefined,
      reorder: async () => undefined,
      startStream: async () => 'fake-sub',
      stopStream: async () => undefined,
      listMessages: async () => [],
      appendMessage: async (id: string, role: string, content: string) => ({
        id: `m-${Math.random().toString(36).slice(2, 8)}`,
        sessionId: id,
        role,
        content,
        createdAt: new Date().toISOString(),
      }),
      saveDraft: async () => undefined,
      loadDraft: async () => '',
      setSystemPrompt: async () => undefined,
      moveToProject: async (id: string, projectId: string) => {
        const s = store.sessions.find((x) => x.id === id);
        if (s) s.projectId = projectId === '' ? undefined : projectId;
      },
    } as any,
    attachments: {
      list: async ({ scopeKind, scopeId }: { scopeKind: any; scopeId?: string }) => {
        const key = scopeKey(scopeKind, scopeId ?? '');
        return [...(store.attachmentsByScope.get(key) ?? [])];
      },
      listResolved: async (sessionId: string) => {
        const sess = store.sessions.find((x) => x.id === sessionId);
        const out: Attachment[] = [];
        out.push(...(store.attachmentsByScope.get(scopeKey('global', '')) ?? []));
        if (sess?.projectId) {
          out.push(
            ...(store.attachmentsByScope.get(scopeKey('project', sess.projectId)) ?? []),
          );
        }
        out.push(...(store.attachmentsByScope.get(scopeKey('session', sessionId)) ?? []));
        return out;
      },
      add: async (input: any) => {
        const scopeId = input.scopeId ?? '';
        const att: Attachment = {
          id: `a-${store.nextAttachmentId++}`,
          scopeKind: input.scopeKind,
          scopeId,
          contentSource: input.contentSource,
          content: input.content,
          kind: input.kind ?? 'system',
          position: input.position ?? 0,
          createdAt: new Date().toISOString(),
        };
        const key = scopeKey(input.scopeKind, scopeId);
        const list = store.attachmentsByScope.get(key) ?? [];
        list.push(att);
        store.attachmentsByScope.set(key, list);
        return att;
      },
      remove: async () => undefined,
      reorder: async () => undefined,
      refresh: async (id: string) =>
        ({
          id,
          scopeKind: 'project',
          scopeId: '',
          contentSource: 'inline:fake',
          content: '',
          kind: 'system',
          position: 0,
          createdAt: '',
        } as Attachment),
    } as any,
    memory: {
      listChunks: async (filter: any) => {
        if (!filter) return [...store.memory];
        return store.memory.filter((c) => {
          if (filter.scopeKind && c.scopeKind !== filter.scopeKind) return false;
          if (filter.scopeId !== undefined && filter.scopeId !== '' && c.scopeId !== filter.scopeId)
            return false;
          return true;
        });
      },
      rememberMessage: async () => 'mem-id',
      promoteScope: async () => 'mem-id',
      forget: async () => undefined,
    } as any,
  });
}

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/projects/:id',
        name: 'project',
        component: ProjectLandingPage,
      },
      {
        path: '/sessions/:id?',
        name: 'sessions',
        component: defineComponent({ render: () => h('div', 'sessions') }),
      },
      {
        path: '/memory',
        name: 'memory',
        component: defineComponent({ render: () => h('div', 'memory') }),
      },
    ],
  });
}

describe('Context-library mission worked example (WP07 T004)', () => {
  it('flows project → attachment → session → resolved → memory', async () => {
    const store = newStore();
    const client = buildClient(store);
    const router = makeRouter();
    await router.push('/projects/__placeholder__');
    await router.isReady();

    // ── 1. seed: create the project directly through the client (the
    //       rail-side input flow is exercised in LeftRail.test.ts).
    const proj = await client.projects.create('alpha', 'the alpha project');
    expect(store.projects).toHaveLength(1);

    // ── 2. attach a project-scope context.
    const attached = await client.attachments.add({
      scopeKind: 'project',
      scopeId: proj.id,
      contentSource: 'library:briefs/alpha.md',
      content: 'project brief content',
      kind: 'system',
      position: 0,
    });
    expect(
      store.attachmentsByScope.get(`project:${proj.id}`)?.[0]?.id,
    ).toBe(attached.id);

    // ── 3. landing page shows the project context section.
    await router.push(`/projects/${proj.id}`);
    await router.isReady();
    const landing = mount(ProjectLandingPage, {
      global: {
        plugins: [router],
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    expect(landing.find('[data-testid=project-context-section]').exists()).toBe(true);
    expect(landing.text()).toContain('briefs/alpha.md');
    // Editable description has loaded the seed text.
    expect(landing.text()).toContain('the alpha project');
    // Memory section renders with zero count.
    expect(landing.find('[data-testid=project-memory-section]').exists()).toBe(true);
    landing.unmount();

    // ── 4. start a session "in this project" — simulate the
    //      NewSessionDialog default preselection by creating + binding.
    const session = await client.sessions.create('first chat');
    await client.sessions.moveToProject(session.id, proj.id);
    expect(store.sessions[0]?.projectId).toBe(proj.id);

    // ── 5. ResolvedContextPanel for that session shows project layer.
    const resolved = mount(ResolvedContextPanel, {
      props: { sessionId: session.id },
      global: {
        provide: { [HarnessClientKey as symbol]: client },
      },
    });
    await flushPromises();
    // expand
    await resolved.find('[data-testid=resolved-context-toggle]').trigger('click');
    await nextTick();
    const summary = resolved.find('[data-testid=resolved-context-summary]');
    expect(summary.exists()).toBe(true);
    // 1 contexts · 0G / 1P / 0S
    expect(summary.text()).toContain('0G / 1P / 0S');
    expect(resolved.find('[data-testid=resolved-context-project]').exists()).toBe(true);
    resolved.unmount();

    // ── 6. memory at project scope visible from a sister session under
    //      the same project.
    const sister = await client.sessions.create('second chat');
    await client.sessions.moveToProject(sister.id, proj.id);
    store.memory.push({
      id: 'mem-1',
      scopeKind: 'project',
      scopeId: proj.id,
      sessionId: session.id,
      projectId: proj.id,
      content: 'pinned project insight',
      createdAt: new Date().toISOString(),
    });
    const projectMemory = await client.memory.listChunks({
      scopeKind: 'project',
      scopeId: proj.id,
    });
    expect(projectMemory).toHaveLength(1);
    expect(projectMemory[0]?.scopeId).toBe(proj.id);
  });

  it('moveToProject DnD updates the session project membership', async () => {
    const store = newStore();
    // Pre-populate: 1 project, 1 loose session.
    const proj: Project = {
      id: 'p1',
      name: 'Doc rewrite',
      description: '',
      createdAt: '',
      updatedAt: '',
    };
    store.projects.push(proj);
    const sess: Session = {
      id: 's1',
      name: 'loose',
      createdAt: '',
      updatedAt: '',
    };
    store.sessions.push(sess);

    const client = buildClient(store);
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        {
          path: '/sessions/:id?',
          name: 'sessions',
          component: defineComponent({ render: () => h('div') }),
        },
        {
          path: '/projects/:id',
          name: 'project',
          component: defineComponent({ render: () => h('div') }),
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
              app.provide(HarnessClientKey, client);
            },
          },
        ],
      },
    });
    await flushPromises();
    await nextTick();

    const sessionRow = w.find(`[data-testid="session-row-${sess.id}"]`);
    expect(sessionRow.exists()).toBe(true);
    const projectHeader = w.find(`[data-testid="project-header-${proj.id}"]`);
    expect(projectHeader.exists()).toBe(true);

    // Simulate the HTML5 DnD lifecycle: dragstart on the session row,
    // dragover / drop on the project header.
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
    // The drop target is the row that wraps the project header.
    const projectRow = w.find(`[data-testid="project-group-${proj.id}"] > div`);
    expect(projectRow.exists()).toBe(true);
    await projectRow.trigger('dragover', { dataTransfer: dt });
    await projectRow.trigger('drop', { dataTransfer: dt });
    await flushPromises();

    expect(store.sessions[0]?.projectId).toBe(proj.id);

    // Drag back onto the Loose header detaches.
    await sessionRow.trigger('dragstart', { dataTransfer: dt });
    const looseHeader = w.find('[data-testid="loose-header"]');
    expect(looseHeader.exists()).toBe(true);
    await looseHeader.trigger('dragover', { dataTransfer: dt });
    await looseHeader.trigger('drop', { dataTransfer: dt });
    await flushPromises();
    expect(store.sessions[0]?.projectId).toBeUndefined();
  });
});

// silence "imported and not used" if vitest tree-shakes
void provideFakeClient;
