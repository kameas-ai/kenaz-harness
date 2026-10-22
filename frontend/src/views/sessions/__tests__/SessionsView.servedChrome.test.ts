/**
 * SessionsView — served-mode chrome (audit findings HR-10, HR-12, HR-13,
 * 2026-10-21). Mount helpers forked from SessionsView.remotePurge.spec.ts.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { ref } from 'vue';

let servedFlag = false;
vi.mock('@/lib/useServedMode', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/lib/useServedMode')>();
  return {
    ...actual,
    isServedMode: () => servedFlag,
    useServedMode: () => ref(servedFlag),
  };
});
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
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
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

async function mountLoadedSession(seed: Partial<HarnessClient>, path = '/sessions/s-1') {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      { path: '/providers', component: defineComponent({ render: () => h('div', 'providers') }) },
    ],
  });
  await router.push(path);
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
    attachTo: document.body,
  });
  await flushPromises();
  return w;
}

function baseSeed(overrides: Partial<HarnessClient> = {}): Partial<HarnessClient> {
  const messages: Message[] = [makeMessage({ id: 'q', role: 'user', content: 'How are you?' })];
  const providers: Provider[] = [
    { id: 'anthropic-p-1', name: 'Anthropic', tier: 'cloud', kind: 'anthropic', model: 'claude' },
  ];
  return {
    sessions: {
      list: async () => [],
      get: async (id: string) => ({ id, name: 'Onboarding', createdAt: '', updatedAt: '' }),
      create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
      rename: async () => undefined,
      delete: async () => undefined,
      reorder: async () => undefined,
      startStream: async () => 'sub',
      stopStream: async () => undefined,
      listMessages: async () => messages,
      listMessagesActive: async () => ({ messages, sweptCount: 0 }),
      listMessagesAll: async () => ({ messages, sweptCount: 0 }),
      appendMessage: async (id: string, role: string, content: string) =>
        makeMessage({ id: 'new', sessionId: id, role: role as Message['role'], content }),
      sendMessageWithBlocks: async () => makeMessage({ id: 'b' }),
      saveDraft: async () => undefined,
      loadDraft: async () => '',
      setSystemPrompt: async () => undefined,
      moveToProject: async () => undefined,
      getUsage: async () => ({
        promptTokens: 0, completionTokens: 0, totalTokens: 0, costUsd: 0,
        costSource: 'unknown' as const, messageCount: 0, pricingDataDate: '',
      }),
      saveAsArtifact: async () => ({
        id: '', sessionId: '', title: '', mimeType: 'text/plain', contentHash: '',
        byteSize: 0, source: 'user_pin' as const,
        sourceRef: { messageId: '', offset: 0, length: 0 },
        scopeKind: 'session' as const, createdAt: '',
      }),
    } as any,
    llm: {
      listProviders: async () => providers,
      startStream: async () => 'sub-llm',
      stopStream: async () => undefined,
    } as any,
    ...overrides,
  };
}

describe('SessionsView — served chrome (HR-10 / HR-12 / HR-13)', () => {
  beforeEach(() => {
    setConnectionState('ready');
  });
  afterEach(() => {
    servedFlag = false;
    uninstallRuntime();
    document.body.innerHTML = '';
  });

  it('desktop keeps sync, share, purge and export (control)', async () => {
    servedFlag = false;
    installFakeRuntime();
    const w = await mountLoadedSession(baseSeed());
    expect(w.find('[data-testid="session-sync-toggle"]').exists()).toBe(true);
    expect(w.find('[data-testid="session-share-btn"]').exists()).toBe(true);
    expect(w.find('[data-testid="session-purge-remote-btn"]').exists()).toBe(true);
    expect(w.find('[data-testid="export-session-btn"]').exists()).toBe(true);
    w.unmount();
  });

  it('served withholds the controls it cannot back', async () => {
    servedFlag = true;
    const w = await mountLoadedSession(baseSeed());
    expect(w.find('[data-testid="session-sync-toggle"]').exists()).toBe(false);
    expect(w.find('[data-testid="session-share-btn"]').exists()).toBe(false);
    expect(w.find('[data-testid="session-purge-remote-btn"]').exists()).toBe(false);
    expect(w.find('[data-testid="session-sync-toolbar"]').exists()).toBe(false);
    expect(w.find('[data-testid="export-session-btn"]').exists()).toBe(false);
    w.unmount();
  });

  it('shows the session name once: no canvas title over the session header', async () => {
    servedFlag = true;
    const w = await mountLoadedSession(baseSeed());
    // CanvasHead renders the page title as a serif <h1>; SessionHeader
    // owns the name while the chat surface is up.
    expect(w.find('header h1').exists()).toBe(false);
    expect(w.text()).toContain('Onboarding');
    w.unmount();
  });

  it('served welcome subtitle agrees with the body and points at the host', async () => {
    servedFlag = true;
    const seed = baseSeed();
    const w = await mountLoadedSession(seed, '/sessions');
    const head = w.get('header').text();
    expect(head).toContain('This workbench has a model ready');
    expect(head).not.toContain('Configure a provider');
    w.unmount();

    const noProv = baseSeed({
      llm: {
        listProviders: async () => [],
        startStream: async () => 'x',
        stopStream: async () => undefined,
      } as any,
    });
    const w2 = await mountLoadedSession(noProv, '/sessions');
    expect(w2.get('header').text()).toContain('Kenaz on the host');
    w2.unmount();
  });
});
