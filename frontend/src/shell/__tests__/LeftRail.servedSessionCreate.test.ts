/**
 * LeftRail.servedSessionCreate — audit finding HR-3 (2026-10-21).
 *
 * In served mode a session created from the Sessions page never appeared in
 * the rail: useSessions() only refreshed on the Wails `session.list_changed`
 * event, the served client dropped its `sessions:update` WS frames, and the
 * rail loaded its list once on mount. The served client now re-announces
 * every session mutation (and every sessions:update / sessions:snapshot
 * frame) as `session.list_changed` on the in-process served-event bus, and
 * useSessions() subscribes to it when there is no Wails runtime.
 *
 * The sessions sub-client here is the REAL served client over a stubbed
 * fetch, so the test goes through the transport, not a fake.
 */
import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import LeftRail from '@/shell/LeftRail.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import { createServedHarnessClient } from '@/lib/harnessClient';
import { dispatchServedEvent } from '@/lib/useServedEvents';
import type { Session } from '@/lib/types';

const Stub = defineComponent({ render: () => h('div') });

let serverSessions: Session[] = [];
let origFetch: typeof globalThis.fetch;

function jsonOk(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { 'Content-Type': 'application/json' },
  });
}

beforeEach(() => {
  serverSessions = [];
  origFetch = globalThis.fetch;
  globalThis.fetch = (async (_input: RequestInfo | URL, init?: RequestInit) => {
    const req = JSON.parse(String(init?.body ?? '{}')) as {
      method?: string;
      params?: { name?: string };
    };
    switch (req.method) {
      case 'Sessions_List':
        return jsonOk({ result: serverSessions });
      case 'Sessions_Create': {
        const s: Session = {
          id: `s-${serverSessions.length + 1}`,
          name: req.params?.name ?? 'New session',
          createdAt: '',
          updatedAt: '',
        };
        serverSessions = [...serverSessions, s];
        return jsonOk({ result: s });
      }
      default:
        return jsonOk({ result: null });
    }
  }) as typeof globalThis.fetch;
});

afterEach(() => {
  globalThis.fetch = origFetch;
  vi.useRealTimers();
});

async function mountRail() {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', name: 'sessions', component: Stub },
      { path: '/:pathMatch(.*)*', component: Stub },
    ],
  });
  await router.push('/sessions');
  await router.isReady();
  const served = createServedHarnessClient({ baseURL: 'http://127.0.0.1:7880', token: 't' });
  const w = mount(LeftRail, {
    global: {
      plugins: [
        router,
        {
          install(app) {
            provideFakeClient(app, { sessions: served.sessions });
          },
        },
      ],
    },
  });
  await flushPromises();
  await nextTick();
  return { w, served };
}

async function settle() {
  // useSessions debounces list_changed by 150 ms.
  await new Promise((r) => setTimeout(r, 200));
  await flushPromises();
  await nextTick();
}

describe('LeftRail — served session list stays live (HR-3)', () => {
  it('shows a session created elsewhere through the served client', async () => {
    const { w, served } = await mountRail();
    expect(w.text()).toContain('No sessions yet');

    // Created from the Sessions page's dialog, i.e. NOT via the rail's
    // own useSessions().create — the rail has to hear about it.
    const s = await served.sessions.create('Alpha');
    await settle();

    expect(w.find(`[data-testid="open-session-${s.id}"]`).exists()).toBe(true);
    expect(w.text()).not.toContain('No sessions yet');
  });

  it('refreshes when a sessions:update frame is re-dispatched as session.list_changed', async () => {
    const { w } = await mountRail();
    // A session appears server-side (another tab, auto-branch…) and the
    // served WS frame handler re-announces it.
    serverSessions = [{ id: 'remote-1', name: 'Remote', createdAt: '', updatedAt: '' }];
    dispatchServedEvent('session.list_changed', { sessions: serverSessions });
    await settle();
    expect(w.find('[data-testid="open-session-remote-1"]').exists()).toBe(true);
  });
});
