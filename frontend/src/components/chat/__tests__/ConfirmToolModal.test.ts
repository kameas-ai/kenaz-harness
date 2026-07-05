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

describe('ConfirmToolModal', () => {
  beforeEach(() => {
    installFakeRuntime();
    setConnectionState('ready');
  });
  afterEach(() => {
    uninstallRuntime();
  });

  it('renders nothing when no confirm request has arrived', () => {
    const w = mountModal();
    expect(w.find('[data-testid="confirm-tool-modal"]').exists()).toBe(false);
    w.unmount();
  });

  it('renders the modal when a confirm request arrives on the event stream', async () => {
    const w = mountModal();
    const rt = (window as unknown as { runtime: FakeRuntime }).runtime;
    rt.emit('llm:tool-confirm-request', makeRequest());
    await flushPromises();

    const modal = w.find('[data-testid="confirm-tool-modal"]');
    expect(modal.exists()).toBe(true);
    expect(w.text()).toContain('github.create_issue');
    // Args summary is pretty-printed JSON.
    const args = w.find('[data-testid="confirm-tool-modal-args"]');
    expect(args.exists()).toBe(true);
    expect(args.text()).toContain('hello');
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

    await w.find('[data-testid="confirm-tool-allow"]').trigger('click');
    await flushPromises();

    expect(resolveSpy).toHaveBeenCalledWith('r-allow', 'allow');
    // After resolving the modal closes (queue is empty).
    expect(w.find('[data-testid="confirm-tool-modal"]').exists()).toBe(false);
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

    await w.find('[data-testid="confirm-tool-always-deny"]').trigger('click');
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

    expect(w.text()).toContain('first');
    expect(w.text()).not.toContain('second');
    expect(w.text()).toContain('+1 more pending');
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
    expect(w.text()).not.toContain('more pending');
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

    await w.find('[data-testid="confirm-tool-allow"]').trigger('click');
    await flushPromises();

    expect(w.text()).toContain('backend down');
    // The modal stays open so the user can retry.
    expect(w.find('[data-testid="confirm-tool-modal"]').exists()).toBe(true);
    w.unmount();
  });
});
