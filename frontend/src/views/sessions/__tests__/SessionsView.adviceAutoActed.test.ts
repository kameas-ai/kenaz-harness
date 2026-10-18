/**
 * SessionsView.adviceAutoActed.test.ts — laya-advisors-01LAYA001 WP07
 * (review blocker 1).
 *
 * useAdviceChips.ts computed autoActedBanner/clearAutoActedBanner with
 * zero .vue consumers before AdviceAutoActedBanner.vue existed — a
 * composable-only test could assert the ref populated and still miss
 * that nothing ever rendered it, which is exactly what happened here.
 * This test mounts the REAL SessionsView, fires the real
 * `advice:auto-acted` topic through the same fake-runtime injection
 * point every other SessionsView.*.test.ts file uses, and asserts the
 * banner actually appears in the DOM — not just that a composable value
 * changed.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { Session } from '@/lib/types';
import { setConnectionState } from '@/lib/useConnectionState';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  emit: (topic: string, payload: unknown) => void;
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
    emit: (topic, payload) => {
      const s = handlers.get(topic);
      if (!s) return;
      for (const cb of s) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

function uninstallRuntime() {
  delete (window as unknown as { runtime?: unknown }).runtime;
}

async function mountWithRoute(
  sessionId: string,
  seed: Partial<HarnessClient> = {},
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
                get: async (id: string) =>
                  sessionList.find((s) => s.id === id) ?? {
                    id,
                    name: 'Session',
                    createdAt: '',
                    updatedAt: '',
                  },
                create: async (name: string) => ({ id: 'new', name, createdAt: '', updatedAt: '' }),
                rename: async () => undefined,
                delete: async () => undefined,
                reorder: async () => undefined,
                startStream: async () => 'sub',
                stopStream: async () => undefined,
                listMessages: async () => [],
                appendMessage: async (sid: string, role: string, content: string) => ({
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
              } as any,
              llm: {
                listProviders: async () => [],
                startStream: async () => 'sub',
                stopStream: async () => undefined,
              } as any,
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

describe('SessionsView — advisor auto-act banner (laya-advisors-01LAYA001 WP07)', () => {
  let fakeRuntime: FakeRuntime;

  beforeEach(() => {
    fakeRuntime = installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
  });

  it('renders the auto-act banner when advice:auto-acted fires for the active session', async () => {
    const { w } = await mountWithRoute('sess-1');

    expect(w.find('[data-testid="advice-auto-acted-banner"]').exists()).toBe(false);

    fakeRuntime.emit('advice:auto-acted', {
      session_id: 'sess-1',
      kind_id: 'branch_now',
      child_session_id: 'sess-1-child',
      confidence: 95,
      model: 'heuristic/branch-regex-v1',
    });
    await flushPromises();

    const banner = w.find('[data-testid="advice-auto-acted-banner"]');
    expect(banner.exists()).toBe(true);
    expect(banner.text()).toContain('branched off automatically');
    w.unmount();
  });

  it('does not render the banner for a different session', async () => {
    const { w } = await mountWithRoute('sess-1');

    fakeRuntime.emit('advice:auto-acted', {
      session_id: 'sess-OTHER',
      kind_id: 'branch_now',
      child_session_id: 'sess-other-child',
      confidence: 95,
      model: 'heuristic/branch-regex-v1',
    });
    await flushPromises();

    expect(w.find('[data-testid="advice-auto-acted-banner"]').exists()).toBe(false);
    w.unmount();
  });

  it('dismissing the banner removes it from the DOM', async () => {
    const { w } = await mountWithRoute('sess-1');

    fakeRuntime.emit('advice:auto-acted', {
      session_id: 'sess-1',
      kind_id: 'branch_now',
      child_session_id: 'sess-1-child',
      confidence: 95,
      model: 'heuristic/branch-regex-v1',
    });
    await flushPromises();
    expect(w.find('[data-testid="advice-auto-acted-banner"]').exists()).toBe(true);

    await w.find('[data-testid="advice-auto-acted-banner-dismiss"]').trigger('click');
    await flushPromises();
    expect(w.find('[data-testid="advice-auto-acted-banner"]').exists()).toBe(false);
    w.unmount();
  });

  it('"View branch" navigates to the child session and clears the banner', async () => {
    const { w, router } = await mountWithRoute('sess-1', {}, [
      { id: 'sess-1', name: 'Parent', createdAt: '', updatedAt: '' },
      { id: 'sess-1-child', name: 'Parent (branch)', createdAt: '', updatedAt: '' },
    ]);

    fakeRuntime.emit('advice:auto-acted', {
      session_id: 'sess-1',
      kind_id: 'branch_now',
      child_session_id: 'sess-1-child',
      confidence: 95,
      model: 'heuristic/branch-regex-v1',
    });
    await flushPromises();

    await w.find('[data-testid="advice-auto-acted-banner-open"]').trigger('click');
    await flushPromises();

    expect(router.currentRoute.value.fullPath).toBe('/sessions/sess-1-child');
    expect(w.find('[data-testid="advice-auto-acted-banner"]').exists()).toBe(false);
    w.unmount();
  });
});
