/**
 * useSession — a user Stop whose provider error chunk lands before the
 * stop-called close (dogfood 2026-10-08 round 2). The close is
 * authoritative: no error bar, the partial kept and marked as stopped.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { defineComponent, h, ref, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { useSession } from '@/lib/useSession';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import type { Message } from '@/lib/types';
import MessageBubble from '@/components/chat/MessageBubble.vue';

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
      return () => {
        s!.delete(cb);
      };
    },
    emit: (topic, payload) => {
      for (const cb of handlers.get(topic) ?? []) cb(payload);
    },
  };
  (window as unknown as { runtime: FakeRuntime }).runtime = rt;
  return rt;
}

const SUB = 'sub-x';

function seed(): Partial<HarnessClient> {
  return {
    sessions: {
      list: async () => [],
      get: async (id: string) => ({ id, name: id, createdAt: '', updatedAt: '' }),
      create: async () => ({ id: '', name: '', createdAt: '', updatedAt: '' }),
      rename: async () => undefined,
      delete: async () => undefined,
      reorder: async () => undefined,
      startStream: async () => 'srv',
      stopStream: async () => undefined,
      listMessages: async () => [],
      appendMessage: async (id: string, role: string, content: string) =>
        ({
          id: 'u-1',
          sessionId: id,
          role: role as Message['role'],
          content,
          createdAt: '2026-08-14T00:00:00Z',
        }) as Message,
      saveDraft: async () => undefined,
      loadDraft: async () => '',
    } as never,
    llm: {
      listProviders: async () => [],
      startStream: async () => SUB,
      stopStream: async () => undefined,
    } as never,
  };
}

describe('useSession — Stop after an error chunk', () => {
  let rt: FakeRuntime;
  beforeEach(() => {
    rt = installFakeRuntime();
    setConnectionState('ready');
    vi.useFakeTimers();
  });
  afterEach(() => {
    delete (window as unknown as { runtime?: unknown }).runtime;
    vi.useRealTimers();
  });

  it('error chunk then stop-called close: no error bar, partial kept as "Stopped by you"', async () => {
    let session: ReturnType<typeof useSession> | null = null;
    const Comp = defineComponent({
      setup() {
        session = useSession(ref('s-1'));
        return () => h('div');
      },
    });
    const w = mount(Comp, { global: { plugins: [{ install: (app) => provideFakeClient(app, seed()) }] } });
    await vi.runAllTimersAsync();
    const s = session as unknown as ReturnType<typeof useSession>;
    await s.send('write 500 words', 'p');
    await nextTick();

    const chunk = (body: Record<string, unknown>) =>
      rt.emit('llm:stream-chunk', { sub_id: SUB, session_id: 's-1', chunk: body });
    chunk({ kind: 'text', text: 'Here is the first part' });
    chunk({ kind: 'error', err: 'context canceled' });
    await nextTick();
    rt.emit('llm:stream-closed', {
      sub_id: SUB,
      session_id: 's-1',
      reason: 'stop-called',
      turn_span_id: 'u-1',
      delivered: true,
    });
    await nextTick();

    expect(s.error.value).toBeNull();
    const last = s.messages.value[s.messages.value.length - 1];
    expect(last.role).toBe('assistant');
    expect(last.content).toBe('Here is the first part');
    expect(last.streamingError).toBe('stop-called');

    const bubble = mount(MessageBubble, {
      props: { role: 'assistant', content: last.content, streamingError: last.streamingError },
    });
    expect(bubble.text()).toContain('Stopped by you');
    expect(bubble.text()).not.toContain('Connection lost');
    w.unmount();
  });
});
