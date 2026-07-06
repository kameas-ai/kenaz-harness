import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
import { defineComponent, h, nextTick } from 'vue';
import NewSessionDialog from '@/shell/NewSessionDialog.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { Provider } from '@/lib/types';

interface SystemPromptCall {
  id: string;
  content: string;
  kind: 'system' | 'user_seed';
}

interface AppendMessageCall {
  id: string;
  role: string;
  content: string;
}

async function mountDialog(seed: Partial<HarnessClient> = {}) {
  const router = createRouter({
    history: createMemoryHistory(),
    routes: [
      {
        path: '/sessions/:id?',
        component: defineComponent({ render: () => h('div', 'sessions') }),
      },
    ],
  });
  await router.push('/sessions');
  await router.isReady();

  const w = mount(NewSessionDialog, {
    props: { open: false },
    // Mount into document.body so that BaseDialog's <Teleport to="body">
    // renders into the live DOM and is reachable via document.querySelector.
    attachTo: document.body,
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
  // Flip open so the dialog's open-watcher fires and providers load.
  await w.setProps({ open: true });
  await flushPromises();
  await nextTick();
  return { w, router };
}


function makeProviders(): Provider[] {
  return [
    {
      id: 'anthropic-1',
      name: 'Anthropic',
      tier: 'cloud',
      kind: 'anthropic',
      model: 'claude-3-haiku',
      models: ['claude-3-haiku'],
    },
  ];
}

// Synthesise the FileReader-driven file.text() path. Vue test-utils
// can't dispatch a real File; happy-dom supplies File but we need a
// minimal stub so the dialog's onFileChange path runs end-to-end.
function makeFile(content: string, name = 'context.md'): File {
  // happy-dom's File.text() doesn't always resolve under our test
  // setup; expose a deterministic stub so the dialog's onFileChange
  // path runs end-to-end without depending on the polyfill.
  const f = new File([content], name, { type: 'text/markdown' });
  Object.defineProperty(f, 'text', {
    value: () => Promise.resolve(content),
    configurable: true,
  });
  Object.defineProperty(f, 'size', {
    value: content.length,
    configurable: true,
  });
  return f;
}

describe('NewSessionDialog (Mission A: starting context)', () => {
  beforeEach(() => {
    // localStorage is provided by happy-dom; nothing to install.
  });
  afterEach(() => {
    window.localStorage.clear();
    // Clean up any stray DOM nodes left by attachTo: document.body mounts.
    document.body.innerHTML = '';
  });

  it('calls setSystemPrompt with kind=system when system radio is selected', async () => {
    const calls: SystemPromptCall[] = [];
    const appendCalls: AppendMessageCall[] = [];
    const { w, router } = await mountDialog({
      llm: {
        listProviders: async () => makeProviders(),
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        addProvider: async () => undefined,
        updateProvider: async () => undefined,
        removeProvider: async () => undefined,
        testProvider: async () => ({ success: true, latency_ms: 0, message: '' }),
        listModels: async () => [],
      } as any,
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async (name: string) => ({
          id: 'new-session-id',
          name,
          createdAt: '',
          updatedAt: '',
        }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) => {
          appendCalls.push({ id, role, content });
          return {
            id: 'm1',
            sessionId: id,
            role,
            content,
            createdAt: '',
          };
        },
        saveDraft: async () => undefined,
        loadDraft: async () => '',
        setSystemPrompt: async (id: string, content: string, kind: string) => {
          calls.push({ id, content, kind: kind as 'system' | 'user_seed' });
        },
        moveToProject: async () => undefined,
      } as any,
    });
    const pushSpy = vi.spyOn(router, 'push');

    // Default kind is 'system' — leave it alone. Drive the file-input
    // change handler with a stub File.
    const fileInputEl = document.body.querySelector<HTMLInputElement>('input[type="file"]');
    expect(fileInputEl).not.toBeNull();
    const file = makeFile('# system seed\nbe concise.');
    Object.defineProperty(fileInputEl!, 'files', {
      value: [file],
      configurable: true,
    });
    fileInputEl!.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();
    await nextTick();

    // Choice should auto-select the lone provider/model on load.
    const createBtn = document.body.querySelector<HTMLElement>('[data-testid="new-session-create"]');
    expect(createBtn).not.toBeNull();
    createBtn!.click();
    await flushPromises();
    await flushPromises();

    expect(calls).toHaveLength(1);
    expect(calls[0].id).toBe('new-session-id');
    expect(calls[0].kind).toBe('system');
    expect(calls[0].content).toBe('# system seed\nbe concise.');
    expect(appendCalls).toHaveLength(0);
    expect(pushSpy).toHaveBeenCalledWith('/sessions/new-session-id');

    w.unmount();
  });

  it('calls appendMessage with role=user when user_seed radio is selected', async () => {
    const calls: SystemPromptCall[] = [];
    const appendCalls: AppendMessageCall[] = [];
    const { w } = await mountDialog({
      llm: {
        listProviders: async () => makeProviders(),
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        addProvider: async () => undefined,
        updateProvider: async () => undefined,
        removeProvider: async () => undefined,
        testProvider: async () => ({ success: true, latency_ms: 0, message: '' }),
        listModels: async () => [],
      } as any,
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async (name: string) => ({
          id: 'new-session-id',
          name,
          createdAt: '',
          updatedAt: '',
        }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) => {
          appendCalls.push({ id, role, content });
          return {
            id: 'm1',
            sessionId: id,
            role,
            content,
            createdAt: '',
          };
        },
        saveDraft: async () => undefined,
        loadDraft: async () => '',
        setSystemPrompt: async (id: string, content: string, kind: string) => {
          calls.push({ id, content, kind: kind as 'system' | 'user_seed' });
        },
        moveToProject: async () => undefined,
      } as any,
    });

    const fileInputEl = document.body.querySelector<HTMLInputElement>('input[type="file"]');
    expect(fileInputEl).not.toBeNull();
    const file = makeFile('please summarise the docs.');
    Object.defineProperty(fileInputEl!, 'files', {
      value: [file],
      configurable: true,
    });
    fileInputEl!.dispatchEvent(new Event('change', { bubbles: true }));
    await flushPromises();
    await nextTick();

    // Switch to user_seed.
    const userSeedRadioEl = document.body.querySelector<HTMLInputElement>('[data-testid="new-session-context-kind-user-seed"]');
    expect(userSeedRadioEl).not.toBeNull();
    userSeedRadioEl!.dispatchEvent(new Event('change', { bubbles: true }));
    await nextTick();

    const createBtn = document.body.querySelector<HTMLElement>('[data-testid="new-session-create"]');
    expect(createBtn).not.toBeNull();
    createBtn!.click();
    await flushPromises();
    await flushPromises();

    expect(appendCalls).toHaveLength(1);
    expect(appendCalls[0].role).toBe('user');
    expect(appendCalls[0].content).toBe('please summarise the docs.');
    // user_seed flow ALSO records the kind so the rail can render badges.
    expect(calls).toHaveLength(1);
    expect(calls[0].kind).toBe('user_seed');
    expect(calls[0].content).toBe('');

    w.unmount();
  });

  it('attaches the new session to the chosen project via moveToProject', async () => {
    const moveCalls: { id: string; projectId: string }[] = [];
    const { w } = await mountDialog({
      llm: {
        listProviders: async () => makeProviders(),
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        addProvider: async () => undefined,
        updateProvider: async () => undefined,
        removeProvider: async () => undefined,
        testProvider: async () => ({ success: true, latency_ms: 0, message: '' }),
        listModels: async () => [],
      } as any,
      projects: {
        list: async () => [
          {
            id: 'p1',
            name: 'Foo',
            description: '',
            createdAt: '',
            updatedAt: '',
          },
        ],
        get: async (id: string) => ({
          id,
          name: id,
          description: '',
          createdAt: '',
          updatedAt: '',
        }),
        create: async (name: string) => ({
          id: 'new-proj-id',
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
      } as any,
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async (name: string) => ({
          id: 'new-session-id',
          name,
          createdAt: '',
          updatedAt: '',
        }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) => ({
          id: 'm1',
          sessionId: id,
          role,
          content,
          createdAt: '',
        }),
        saveDraft: async () => undefined,
        loadDraft: async () => '',
        setSystemPrompt: async () => undefined,
        moveToProject: async (id: string, projectId: string) => {
          moveCalls.push({ id, projectId });
        },
      } as any,
    });

    const selectEl = document.body.querySelector<HTMLSelectElement>('[data-testid="new-session-project"]');
    expect(selectEl).not.toBeNull();
    selectEl!.value = 'p1';
    selectEl!.dispatchEvent(new Event('change', { bubbles: true }));
    await nextTick();

    const createBtn = document.body.querySelector<HTMLElement>('[data-testid="new-session-create"]');
    expect(createBtn).not.toBeNull();
    createBtn!.click();
    await flushPromises();
    await flushPromises();

    expect(moveCalls).toHaveLength(1);
    expect(moveCalls[0]).toEqual({ id: 'new-session-id', projectId: 'p1' });
    w.unmount();
  });

  it('skips both calls when no file is attached', async () => {
    const calls: SystemPromptCall[] = [];
    const appendCalls: AppendMessageCall[] = [];
    const { w } = await mountDialog({
      llm: {
        listProviders: async () => makeProviders(),
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        addProvider: async () => undefined,
        updateProvider: async () => undefined,
        removeProvider: async () => undefined,
        testProvider: async () => ({ success: true, latency_ms: 0, message: '' }),
        listModels: async () => [],
      } as any,
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        create: async (name: string) => ({
          id: 'new-session-id',
          name,
          createdAt: '',
          updatedAt: '',
        }),
        rename: async () => undefined,
        delete: async () => undefined,
        reorder: async () => undefined,
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        listMessages: async () => [],
        appendMessage: async (id: string, role: string, content: string) => {
          appendCalls.push({ id, role, content });
          return {
            id: 'm1',
            sessionId: id,
            role,
            content,
            createdAt: '',
          };
        },
        saveDraft: async () => undefined,
        loadDraft: async () => '',
        setSystemPrompt: async (id: string, content: string, kind: string) => {
          calls.push({ id, content, kind: kind as 'system' | 'user_seed' });
        },
        moveToProject: async () => undefined,
      } as any,
    });

    const createBtn = document.body.querySelector<HTMLElement>('[data-testid="new-session-create"]');
    expect(createBtn).not.toBeNull();
    createBtn!.click();
    await flushPromises();

    expect(calls).toHaveLength(0);
    expect(appendCalls).toHaveLength(0);
    w.unmount();
  });
});
