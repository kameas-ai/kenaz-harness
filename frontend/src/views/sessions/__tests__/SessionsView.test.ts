import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h } from 'vue';
import SessionsView from '@/views/sessions/SessionsView.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { Message, Provider } from '@/lib/types';
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

function makeMessage(overrides: Partial<Message>): Message {
  return {
    id: 'm-1',
    sessionId: 's-1',
    role: 'user',
    content: 'hello',
    createdAt: '2026-04-25T00:00:00Z',
    ...overrides,
  };
}

async function mountWithRoute(
  idFragment: string,
  seed: Partial<HarnessClient>,
) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      { path: '/providers', component: defineComponent({ render: () => h('div', 'providers') }) },
    ],
  });
  // Legacy callers pass `'#<id>'`; the new router uses /sessions/<id>.
  // Strip a leading `#` when present so existing tests continue to work.
  const id = idFragment.startsWith('#') ? idFragment.slice(1) : idFragment;
  await router.push(id ? `/sessions/${id}` : '/sessions');
  await router.isReady();

  const w = mount(SessionsView, {
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
  return { w, router };
}

describe('SessionsView (chat-ui)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
  });

  it('renders the empty placeholder with no session id in the hash', async () => {
    const { w } = await mountWithRoute('', {});
    expect(w.text()).toContain('No session selected');
    w.unmount();
  });

  it('shows "no provider configured" when llm.listProviders is empty', async () => {
    const { w } = await mountWithRoute('#sess-1', {
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      },
    });
    expect(w.text()).toContain('No provider configured');
    expect(w.text()).toContain('Configure providers');
    w.unmount();
  });

  it('renders the chat surface (CanvasHead + MessageList + ChatInput) when a session is loaded', async () => {
    const messages: Message[] = [
      makeMessage({ id: 'q', role: 'user', content: 'How are you?' }),
      makeMessage({ id: 'a', role: 'assistant', content: 'I am well.' }),
    ];
    const providers: Provider[] = [
      { id: 'p-1', name: 'Anthropic', tier: 'cloud', model: 'claude' },
    ];
    const { w } = await mountWithRoute('#s-1', {
      sessions: {
        list: async () => [],
        get: async (id) => ({
          id,
          name: 'Onboarding',
          createdAt: '',
          updatedAt: '',
        }),
        create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => messages,
        appendMessage: async (id, role, content) =>
          makeMessage({ id: 'new', sessionId: id, role, content }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
      },
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub-llm',
        stopStream: async () => undefined,
      },
    });
    // CanvasHead title reflects the loaded session name.
    expect(w.text()).toContain('Onboarding');
    expect(w.text()).toContain('How are you?');
    expect(w.text()).toContain('I am well.');
    // ChatInput textarea is present and enabled.
    const textarea = w.find('textarea');
    expect(textarea.exists()).toBe(true);
    expect(textarea.attributes('disabled')).toBeUndefined();
    w.unmount();
  });

  it('does not render the no-provider banner when a provider is configured', async () => {
    const providers: Provider[] = [
      { id: 'p-1', name: 'Anthropic', tier: 'cloud', model: 'claude' },
    ];
    const { w } = await mountWithRoute('#s-1', {
      llm: {
        listProviders: async () => providers,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
      },
    });
    expect(w.text()).not.toContain('No provider configured');
    w.unmount();
  });
});
