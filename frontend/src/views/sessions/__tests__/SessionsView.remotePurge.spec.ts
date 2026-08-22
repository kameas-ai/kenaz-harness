/**
 * SessionsView.remotePurge.spec.ts — fleet-enforcement-truth-01PMZ505 WP13
 *
 * SessionSync_DeleteRemote purges every fleet event uploaded for a session
 * AND disables sync (core/fleet/session_sync.go:209). Every layer up to the
 * UI existed already (§1.12 of the mission spec) — no `.vue` file called it.
 * This pins the affordance: a "Purge remote" control in the session sync
 * toolbar, gated behind a confirm dialog whose copy names both effects.
 *
 * AC-025 requires the test to drive the action *through the rendered UI*
 * (not just assert the RPC exists) and to assert the confirm copy — a test
 * that only checks the RPC fired would pass even if the control were never
 * mounted, which is the whole defect this WP closes.
 */
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
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

async function mountLoadedSession(seed: Partial<HarnessClient>) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/sessions/:id?', component: SessionsView },
      { path: '/providers', component: defineComponent({ render: () => h('div', 'providers') }) },
    ],
  });
  await router.push('/sessions/s-1');
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

describe('SessionsView — remote purge (WP13)', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
    document.body.innerHTML = '';
  });

  it('renders a Purge remote control in the session sync toolbar', async () => {
    const w = await mountLoadedSession(baseSeed());
    const btn = document.body.querySelector('[data-testid="session-purge-remote-btn"]');
    expect(btn).not.toBeNull();
    w.unmount();
  });

  it('AC-025: drives the action through the confirm dialog to SessionSync_DeleteRemote, and the confirm copy names both effects', async () => {
    const deleteRemote = vi.fn(async (_id: string) => undefined);
    const w = await mountLoadedSession(baseSeed({ SessionSync_DeleteRemote: deleteRemote }));

    const purgeBtn = document.body.querySelector(
      '[data-testid="session-purge-remote-btn"]',
    ) as HTMLButtonElement;
    expect(purgeBtn).not.toBeNull();
    purgeBtn.click();
    await flushPromises();

    // The confirm dialog must actually be open — asserting the RPC fired
    // without ever rendering the confirmation is exactly the "control
    // nobody could reach" failure mode AC-025 exists to catch.
    const dialogTitle = document.body.querySelector('[data-testid="confirm-dialog-title"]');
    const dialogMessage = document.body.querySelector('[data-testid="confirm-dialog-message"]');
    expect(dialogTitle).not.toBeNull();
    expect(dialogMessage).not.toBeNull();

    const messageText = dialogMessage!.textContent ?? '';
    // Both effects named: the remote purge and sync being disabled.
    expect(messageText.toLowerCase()).toContain('delete');
    expect(messageText.toLowerCase()).toContain('sync');
    expect(messageText.toLowerCase()).toMatch(/off|disable/);

    // The RPC must not have fired yet — only after confirming.
    expect(deleteRemote).not.toHaveBeenCalled();

    const confirmBtn = document.body.querySelector(
      '[data-testid="confirm-dialog-confirm"]',
    ) as HTMLButtonElement;
    expect(confirmBtn).not.toBeNull();
    confirmBtn.click();
    await flushPromises();

    expect(deleteRemote).toHaveBeenCalledWith('s-1');
    w.unmount();
  });

  it('cancelling the confirm dialog does not call SessionSync_DeleteRemote', async () => {
    const deleteRemote = vi.fn(async (_id: string) => undefined);
    const w = await mountLoadedSession(baseSeed({ SessionSync_DeleteRemote: deleteRemote }));

    const purgeBtn = document.body.querySelector(
      '[data-testid="session-purge-remote-btn"]',
    ) as HTMLButtonElement;
    purgeBtn.click();
    await flushPromises();

    const cancelBtn = document.body.querySelector(
      '[data-testid="confirm-dialog-cancel"]',
    ) as HTMLButtonElement;
    expect(cancelBtn).not.toBeNull();
    cancelBtn.click();
    await flushPromises();

    expect(deleteRemote).not.toHaveBeenCalled();
    w.unmount();
  });
});
