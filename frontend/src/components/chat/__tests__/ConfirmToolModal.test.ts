import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import ConfirmToolModal from '@/components/chat/ConfirmToolModal.vue';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { ConfirmToolRequest } from '@/lib/types';
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

function mountModal(seed: Partial<HarnessClient> = {}) {
  return mount(ConfirmToolModal, {
    // Mount into document.body so BaseDialog's <Teleport to="body"> renders
    // content into the live DOM, reachable via document.body.querySelector.
    attachTo: document.body,
    global: {
      plugins: [
        {
          install(app) {
            provideFakeClient(app, seed);
          },
        },
      ],
    },
  });
}

function makeRequest(overrides: Partial<ConfirmToolRequest> = {}): ConfirmToolRequest {
  return {
    request_id: 'req-1',
    session_id: 'sess-1',
    parent_sub_id: 'sub-1',
    server: 'github',
    tool: 'create_issue',
    tool_use_id: 'tu-1',
    args_redacted: '{"title":"hello"}',
    ...overrides,
  };
}

/** Query a testid from document.body (needed for Teleport-rendered content). */
function q(testid: string): HTMLElement | null {
  return document.body.querySelector(`[data-testid="${testid}"]`);
}

describe('ConfirmToolModal', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
    // Clean up DOM nodes left by attachTo: document.body mounts.
    document.body.innerHTML = '';
  });

  it('renders nothing when no confirm request has arrived', () => {
    const w = mountModal();
    // The inner div (data-testid="confirm-tool-modal") only renders when head != null.
    expect(q('confirm-tool-modal')).toBeNull();
    w.unmount();
  });

  it('renders the modal when a confirm request arrives on the event stream', async () => {
    const w = mountModal();
    const rt = (window as unknown as { runtime: FakeRuntime }).runtime;
    rt.emit('llm:tool-confirm-request', makeRequest());
    await flushPromises();

    expect(q('confirm-tool-modal')).not.toBeNull();
    expect(document.body.textContent).toContain('github.create_issue');
    // Args summary is pretty-printed JSON.
    expect(q('confirm-tool-modal-args')).not.toBeNull();
    expect(q('confirm-tool-modal-args')!.textContent).toContain('hello');
    w.unmount();
  });

  it('calls resolveConfirm with the right decision on Allow click', async () => {
    const resolveSpy = vi.fn(async () => undefined);
    const w = mountModal({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        addProvider: async () => undefined,
        updateProvider: async () => undefined,
        removeProvider: async () => undefined,
        testProvider: async () => ({ success: true, latency_ms: 0, message: 'ok' }),
        listModels: async () => [],
        resolveConfirm: resolveSpy,
      } as any,
    });
    const rt = (window as unknown as { runtime: FakeRuntime }).runtime;
    rt.emit('llm:tool-confirm-request', makeRequest({ request_id: 'r-allow' }));
    await flushPromises();

    const allowBtn = q('confirm-tool-allow');
    expect(allowBtn).not.toBeNull();
    allowBtn!.click();
    await flushPromises();

    expect(resolveSpy).toHaveBeenCalledWith('r-allow', 'allow');
    // After resolving the modal closes (queue is empty).
    expect(q('confirm-tool-modal')).toBeNull();
    w.unmount();
  });

  it('calls resolveConfirm with always_deny on the Always deny click', async () => {
    const resolveSpy = vi.fn(async () => undefined);
    const w = mountModal({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        addProvider: async () => undefined,
        updateProvider: async () => undefined,
        removeProvider: async () => undefined,
        testProvider: async () => ({ success: true, latency_ms: 0, message: 'ok' }),
        listModels: async () => [],
        resolveConfirm: resolveSpy,
      } as any,
    });
    const rt = (window as unknown as { runtime: FakeRuntime }).runtime;
    rt.emit('llm:tool-confirm-request', makeRequest({ request_id: 'r-deny' }));
    await flushPromises();

    const alwaysDenyBtn = q('confirm-tool-always-deny');
    expect(alwaysDenyBtn).not.toBeNull();
    alwaysDenyBtn!.click();
    await flushPromises();

    expect(resolveSpy).toHaveBeenCalledWith('r-deny', 'always_deny');
    w.unmount();
  });

  it('queues multiple requests and renders the head only', async () => {
    const w = mountModal();
    const rt = (window as unknown as { runtime: FakeRuntime }).runtime;
    rt.emit('llm:tool-confirm-request', makeRequest({ request_id: 'r-1', tool: 'first' }));
    rt.emit('llm:tool-confirm-request', makeRequest({ request_id: 'r-2', tool: 'second' }));
    await flushPromises();

    expect(document.body.textContent).toContain('first');
    expect(document.body.textContent).not.toContain('second');
    expect(document.body.textContent).toContain('+1 more pending');
    w.unmount();
  });

  it('dedups duplicate request ids', async () => {
    const w = mountModal();
    const rt = (window as unknown as { runtime: FakeRuntime }).runtime;
    const payload = makeRequest({ request_id: 'dup' });
    rt.emit('llm:tool-confirm-request', payload);
    rt.emit('llm:tool-confirm-request', payload);
    await flushPromises();

    // Only one queued; "more pending" indicator should not show.
    expect(document.body.textContent).not.toContain('more pending');
    w.unmount();
  });

  it('surfaces an error message when resolveConfirm rejects', async () => {
    const resolveSpy = vi.fn(async () => {
      throw new Error('backend down');
    });
    const w = mountModal({
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      llm: {
        listProviders: async () => [],
        startStream: async () => 'sub',
        stopStream: async () => undefined,
        addProvider: async () => undefined,
        updateProvider: async () => undefined,
        removeProvider: async () => undefined,
        testProvider: async () => ({ success: true, latency_ms: 0, message: 'ok' }),
        listModels: async () => [],
        resolveConfirm: resolveSpy,
      } as any,
    });
    const rt = (window as unknown as { runtime: FakeRuntime }).runtime;
    rt.emit('llm:tool-confirm-request', makeRequest());
    await flushPromises();

    const allowBtn = q('confirm-tool-allow');
    expect(allowBtn).not.toBeNull();
    allowBtn!.click();
    await flushPromises();

    expect(document.body.textContent).toContain('backend down');
    // The modal stays open so the user can retry.
    expect(q('confirm-tool-modal')).not.toBeNull();
    w.unmount();
  });
});
