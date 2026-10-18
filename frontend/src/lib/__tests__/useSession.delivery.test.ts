/**
 * undelivered-message-retry (owner dogfood 2026-10-07): the OpenRouter
 * account ran out of credits; the 402 arrived before any token; the user
 * had to retype the message after topping up — a duplicate user row.
 *
 * useSession now:
 *   - marks the message NOT DELIVERED from the live close AND from the
 *     persisted run outcomes (so it survives reload);
 *   - Retry re-dispatches the same turn through llm.startStream ONCE,
 *     never sessions.appendMessage (no new user row), and a double click
 *     cannot double-send;
 *   - auto-retries TRANSIENT failures on a 2s/8s/30s schedule, never
 *     user_actionable ones, and Stop cancels a pending auto-retry.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { defineComponent, h, ref, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { useSession } from '@/lib/useSession';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import type { TurnRun } from '@/lib/types';
import { setConnectionState } from '@/lib/useConnectionState';

interface FakeRuntime {
  EventsOn: (topic: string, cb: (payload: unknown) => void) => () => void;
  emit: (topic: string, payload: unknown) => void;
}

function installFakeRuntime(): FakeRuntime {
  const handlers = new Map<string, Set<(payload: unknown) => void>>();
  const rt: FakeRuntime = {
    EventsOn: (topic, cb) => {
      let set = handlers.get(topic);
      if (!set) {
        set = new Set();
        handlers.set(topic, set);
      }
      set.add(cb);
      return () => set!.delete(cb);
    },
    emit: (topic, payload) => {
      for (const cb of handlers.get(topic) ?? []) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

const PAY_CLOSE = {
  session_id: 's-1',
  reason: 'backend-error',
  message: 'openrouter rejected this request for insufficient credits',
  turn_span_id: 'u-1',
  delivered: false,
  failure_class: 'user_actionable',
  failure_code: 'payment_required',
  failure_status: 402,
  failure_provider: 'openrouter',
  failure_summary: 'Out of credits with OpenRouter',
};

const RATE_CLOSE = {
  ...PAY_CLOSE,
  message: 'rate limited',
  failure_class: 'transient',
  failure_code: 'rate_limited',
  failure_status: 429,
  failure_summary: 'OpenRouter is rate-limiting requests',
};

describe('useSession — undelivered messages + retry', () => {
  let rt: FakeRuntime;
  let runs: TurnRun[];
  let startStream: ReturnType<typeof vi.fn>;
  let appendMessage: ReturnType<typeof vi.fn>;
  let subCounter: number;

  function seed(): Partial<HarnessClient> {
    return {
      sessions: {
        list: async () => [],
        get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
        listMessages: async () => [
          { id: 'u-1', sessionId: 's-1', role: 'user', content: 'summarise', createdAt: '' },
        ],
        saveDraft: async () => undefined,
        loadDraft: async () => '',
        getUsage: async () => null,
        turnRuns: async () => runs,
        appendMessage,
      } as never,
      llm: { listProviders: async () => [], startStream, stopStream: async () => undefined } as never,
    };
  }

  beforeEach(() => {
    rt = installFakeRuntime();
    setConnectionState('ready');
    runs = [];
    subCounter = 0;
    startStream = vi.fn(async () => `chat-${++subCounter}`);
    appendMessage = vi.fn(async (_sid: string, role: string, content: string) => ({
      id: `u-new-${subCounter}`, sessionId: 's-1', role, content, createdAt: '',
    }));
  });

  afterEach(() => {
    vi.useRealTimers();
    delete (window as unknown as { runtime?: unknown }).runtime;
  });

  function boot() {
    const idRef = ref<string>('s-1');
    let api!: ReturnType<typeof useSession>;
    const Host = defineComponent({
      setup() {
        api = useSession(idRef);
        return () => h('div');
      },
    });
    mount(Host, {
      global: { plugins: [{ install: (app) => provideFakeClient(app, seed()) }] },
    });
    return { idRef, api: () => api };
  }

  async function settle() {
    for (let i = 0; i < 8; i++) await nextTick();
    await Promise.resolve();
    for (let i = 0; i < 8; i++) await nextTick();
  }

  it('renders NOT DELIVERED from the persisted run outcome on load (survives reload)', async () => {
    runs = [{
      runId: 'chat-old', turnSpanId: 'u-1', graphId: 'g', specDigest: 'd', createdAt: '',
      outcome: 'failed', delivered: false, failureClass: 'user_actionable',
      failureCode: 'payment_required', failureSummary: 'Out of credits with OpenRouter',
    }];
    const { api } = boot();
    await settle();
    expect(api().undelivered.value.get('u-1')?.code).toBe('payment_required');
  });

  it('marks the message from the live close, keeps the transcript error banner clear, and never auto-retries user_actionable', async () => {
    vi.useFakeTimers();
    const { api } = boot();
    await settle();
    await api().send('summarise', 'or-profile', 'moonshotai/kimi-k3');
    await settle();
    expect(startStream).toHaveBeenCalledTimes(1);
    rt.emit('llm:stream-closed', { ...PAY_CLOSE, sub_id: 'chat-1' });
    await settle();
    expect(api().undelivered.value.get('u-1')?.summary).toBe('Out of credits with OpenRouter');
    expect(api().deliveryFailure.value?.code).toBe('payment_required');
    expect(api().error.value).toBeNull(); // reported ON the message, not as "Send failed"
    expect(api().autoRetry.value).toBeNull();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(startStream).toHaveBeenCalledTimes(1); // no automatic retry, ever
  });

  it('Retry re-dispatches the same turn once — startStream, never appendMessage — and a delivered close clears it', async () => {
    const { api } = boot();
    await settle();
    rt.emit('llm:stream-closed', { ...PAY_CLOSE, sub_id: 'chat-0' });
    await settle();
    expect(api().undelivered.value.has('u-1')).toBe(true);

    // A double click: the second call must be a no-op.
    await Promise.all([
      api().retry('or-profile', 'moonshotai/kimi-k3'),
      api().retry('or-profile', 'moonshotai/kimi-k3'),
    ]);
    await settle();
    expect(startStream).toHaveBeenCalledTimes(1);
    expect(startStream).toHaveBeenCalledWith('or-profile', 's-1', 'moonshotai/kimi-k3');
    expect(appendMessage).not.toHaveBeenCalled();
    // While the retry stream is open, another Retry is refused too.
    await api().retry('or-profile', 'moonshotai/kimi-k3');
    expect(startStream).toHaveBeenCalledTimes(1);

    rt.emit('llm:stream-closed', {
      session_id: 's-1', sub_id: 'chat-1', reason: 'completed', turn_span_id: 'u-1', delivered: true,
    });
    await settle();
    expect(api().undelivered.value.size).toBe(0);
    expect(api().deliveryFailure.value).toBeNull();
  });

  it('auto-retries a transient failure on the 2s/8s/30s schedule, then stops and leaves it NOT DELIVERED', async () => {
    vi.useFakeTimers();
    const { api } = boot();
    await settle();
    await api().send('summarise', 'or-profile', 'm');
    await settle();
    expect(startStream).toHaveBeenCalledTimes(1);

    const delays = [2_000, 8_000, 30_000];
    for (let i = 0; i < delays.length; i++) {
      rt.emit('llm:stream-closed', { ...RATE_CLOSE, sub_id: `chat-${i + 1}` });
      await settle();
      expect(api().autoRetry.value).toEqual({ attempt: i + 1, max: 3, delayMs: delays[i] });
      await vi.advanceTimersByTimeAsync(delays[i] - 1);
      expect(startStream).toHaveBeenCalledTimes(i + 1);
      await vi.advanceTimersByTimeAsync(1);
      await settle();
      expect(startStream).toHaveBeenCalledTimes(i + 2);
      expect(appendMessage).toHaveBeenCalledTimes(1); // only the original send
    }
    // The third automatic retry fails too: budget spent, surfaced.
    rt.emit('llm:stream-closed', { ...RATE_CLOSE, sub_id: 'chat-4' });
    await settle();
    expect(api().autoRetry.value).toBeNull();
    expect(api().undelivered.value.get('u-1')?.code).toBe('rate_limited');
    await vi.advanceTimersByTimeAsync(120_000);
    expect(startStream).toHaveBeenCalledTimes(4);
  });

  it('Stop during a pending auto-retry cancels it — nothing is dispatched', async () => {
    vi.useFakeTimers();
    const { api } = boot();
    await settle();
    await api().send('summarise', 'or-profile', 'm');
    await settle();
    rt.emit('llm:stream-closed', { ...RATE_CLOSE, sub_id: 'chat-1' });
    await settle();
    expect(api().autoRetry.value?.attempt).toBe(1);
    await api().cancel();
    expect(api().autoRetry.value).toBeNull();
    await vi.advanceTimersByTimeAsync(60_000);
    expect(startStream).toHaveBeenCalledTimes(1);
    expect(api().undelivered.value.has('u-1')).toBe(true); // still not delivered, manual Retry remains
  });

  it('a new send while an auto-retry is pending cancels the retry (the new turn carries the message)', async () => {
    vi.useFakeTimers();
    const { api } = boot();
    await settle();
    await api().send('summarise', 'or-profile', 'm');
    await settle();
    rt.emit('llm:stream-closed', { ...RATE_CLOSE, sub_id: 'chat-1' });
    await settle();
    expect(api().autoRetry.value).not.toBeNull();
    await api().send('and also this', 'or-profile', 'm');
    await settle();
    expect(startStream).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(60_000);
    expect(startStream).toHaveBeenCalledTimes(2); // the old timer never fired
  });

  it('session_full is not a NOT DELIVERED failure: no badge entry, no delivery banner, its own banner via errorKind', async () => {
    const { api } = boot();
    await settle();
    rt.emit('llm:stream-closed', {
      ...PAY_CLOSE, sub_id: 'chat-1', error_kind: 'session_full',
      failure_code: 'session_full', message: 'session full',
    });
    await settle();
    expect(api().undelivered.value.size).toBe(0);
    expect(api().deliveryFailure.value).toBeNull();
    expect(api().errorKind.value).toBe('session_full');
    expect(api().error.value).toBe('session full');
  });
});
