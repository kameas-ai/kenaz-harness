/**
 * SessionsView.branching.test.ts — branching-ux-polish-01KQ8TD7 WP07.
 *
 * Integration smoke tests wiring:
 *  - BranchBreadcrumb appears for branch sessions.
 *  - "Branch from this turn" button on assistant messages triggers
 *    client.branches.createExplicit and navigates to the child session.
 *  - BranchBreadcrumb hidden for root sessions.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { Branch, Message, Session } from '@/lib/types';
import { setConnectionState } from '@/lib/useConnectionState';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  handlers: Map<string, Set<(payload: unknown) => void>>;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    handlers,
    EventsOn: (topic, cb) => {
      let s = handlers.get(topic);
      if (!s) {
        s = new Set();
        handlers.set(topic, s);
      }
      s.add(cb);
      return () => s!.delete(cb);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

function makeMessage(overrides: Partial<Message>): Message {
  return {
    id: 'm-1',
    sessionId: 's-branch',
    role: 'user',
    content: 'hello',
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

function makeBranch(overrides: Partial<Branch>): Branch {
  return {
    id: 'br-1',
    parentSessionId: 's-parent',
    childSessionId: 's-branch',
    kind: 'fork',
    status: 'active',
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

async function mountWithRoute(
  sessionId: string,
  seed: Partial<HarnessClient>,
  sessionList: Session[] = [],
) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      {
        path: '/sessions',
        component: defineComponent({ render: () => h('div', 'sessions') }),
      },
    ],
  });
  await router.push(`/sessions/${sessionId}`);
  await router.isReady();

  const w = mount(SessionsView, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, {
              sessions: {
                list: async () => sessionList,
                get: async (id) =>
                  sessionList.find((s) => s.id === id) ?? {
                    id,
                    name: 'Session',
                    createdAt: '',
                    updatedAt: '',
                  },
                create: async (name) => ({ id: 'new', name, createdAt: '', updatedAt: '' }),
                rename: async () => undefined,
                delete: async () => undefined,
                reorder: async () => undefined,
                startStream: async () => 'sub',
                stopStream: async () => undefined,
                listMessages: async () => [],
                appendMessage: async (sid, role, content) => ({
                  id: 'm-x',
                  sessionId: sid,
                  role,
                  content,
                  createdAt: '',
                }),
                saveDraft: async () => undefined,
                loadDraft: async () => '',
                setSystemPrompt: async () => undefined,
                moveToProject: async () => undefined,
              },
              llm: {
                listProviders: async () => [],
                startStream: async () => 'sub',
                stopStream: async () => undefined,
              },
              ...seed,
            });
          },
        },
      ],
    },
  });
  await flushPromises();
  return { w, router };
}

describe('SessionsView (branching-ux-polish WP07)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
    localStorage.removeItem('harness.feature.branchingPolish');
  });
  afterEach(() => {
    uninstallRuntime();
    localStorage.removeItem('harness.feature.branchingPolish');
  });

  it('BranchBreadcrumb is hidden for root sessions', async () => {
    const sessions: Session[] = [
      { id: 's-root', name: 'Root Session', createdAt: '', updatedAt: '' },
    ];
    const { w } = await mountWithRoute('s-root', {}, sessions);
    // Root session has no parentSessionId so breadcrumb must be absent.
    expect(w.find('[data-testid="branch-breadcrumb"]').exists()).toBe(false);
    w.unmount();
  });

  it('BranchBreadcrumb appears for branch sessions with known parent', async () => {
    const sessions: Session[] = [
      { id: 's-parent', name: 'Alpha', createdAt: '', updatedAt: '' },
      {
        id: 's-branch',
        name: 'Alpha (branch)',
        parentSessionId: 's-parent',
        createdAt: '',
        updatedAt: '',
      },
    ];
    const { w } = await mountWithRoute('s-branch', {}, sessions);
    const bc = w.find('[data-testid="branch-breadcrumb"]');
    expect(bc.exists()).toBe(true);
    // Parent title "Alpha" should appear in the breadcrumb.
    expect(bc.text()).toContain('Alpha');
    w.unmount();
  });

  it('Branch-from-turn button triggers createExplicit and navigates to child', async () => {
    const createCalls: { parentSessionId: string; parentMessageId: string }[] = [];
    const fakeBranch: Branch = makeBranch({ childSessionId: 's-child' });

    const sessions: Session[] = [
      { id: 's-parent', name: 'Parent', createdAt: '', updatedAt: '' },
    ];
    const messages: Message[] = [
      makeMessage({ id: 'm-assistant', sessionId: 's-parent', role: 'assistant', content: 'hi' }),
    ];

    const { w, router } = await mountWithRoute(
      's-parent',
      {
        sessions: {
          list: async () => sessions,
          get: async () => sessions[0]!,
          create: async (name) => ({ id: 'new', name, createdAt: '', updatedAt: '' }),
          rename: async () => undefined,
          delete: async () => undefined,
          reorder: async () => undefined,
          startStream: async () => 'sub',
          stopStream: async () => undefined,
          listMessages: async () => messages,
          appendMessage: async (sid, role, content) => ({
            id: 'm-x',
            sessionId: sid,
            role,
            content,
            createdAt: '',
          }),
          saveDraft: async () => undefined,
          loadDraft: async () => '',
          setSystemPrompt: async () => undefined,
          moveToProject: async () => undefined,
        },
        branches: {
          list: async () => [],
          create: async () => fakeBranch,
          createExplicit: async (opts) => {
            createCalls.push({
              parentSessionId: opts.parentSessionId,
              parentMessageId: opts.parentMessageId ?? '',
            });
            return fakeBranch;
          },
          status: async () => ({
            id: 'br-1',
            childSessionId: 's-child',
            parentSessionId: 's-parent',
            status: 'active',
          }),
          merge: async () => undefined,
          abandon: async () => undefined,
          recommendModel: async () => ({
            providerId: 'p',
            modelId: 'm',
            reasoning: '',
          }),
          proposeReintegrationSummary: async () => ({
            branchSessionId: 's-child',
            parentSessionId: 's-parent',
            proposedSummary: '',
            keyDecisions: [],
          }),
          commitReintegration: async () => undefined,
          setAdvisorDismissed: async () => undefined,
          listWithBranchTree: async () => [],
        },
      },
      sessions,
    );

    // The branch-from-turn button is on the assistant message bubble.
    await flushPromises();
    const btn = w.find('[data-testid="branch-from-turn-button"]');
    expect(btn.exists()).toBe(true);
    await btn.trigger('click');
    await flushPromises();

    expect(createCalls).toHaveLength(1);
    expect(createCalls[0]?.parentSessionId).toBe('s-parent');
    expect(createCalls[0]?.parentMessageId).toBe('m-assistant');

    // Should have navigated to the child session.
    expect(router.currentRoute.value.path).toBe('/sessions/s-child');
    w.unmount();
  });
});
