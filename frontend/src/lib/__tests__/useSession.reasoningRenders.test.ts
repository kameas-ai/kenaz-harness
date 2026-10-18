/**
 * useSession — the reasoning-frame render path
 * (model-settings-reach-the-model-01PMZ101 WP16, closing CHAT-09).
 *
 * `useSession.ts`'s `llm:stream-chunk` switch had a `default:` arm whose
 * body was `return;` — reasoning deltas (already reaching this handler
 * on Anthropic, and as of WP09 on Bedrock and Gemini too) were dropped
 * one line short of the screen. The user paid for reasoning tokens and
 * never saw them, live or on reload.
 *
 * These tests drive the REAL `llm:stream-chunk` handler through the fake
 * runtime (not a hand-built store mutation) and assert the reasoning
 * text actually RENDERS in MessageList's DOM output — not merely that
 * `session.streamingMoves.value` picked up a `reasoning` field. A
 * state-only assertion would pass against the pre-WP16 bug: the
 * `default:` arm updates nothing, so any test that stops at "the event
 * was accepted" or "some ref changed" proves nothing about the screen.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { defineComponent, h, ref, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { useSession } from '@/lib/useSession';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import type { Message } from '@/lib/types';
import MessageList from '@/components/chat/MessageList.vue';

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

const SUB = 'sub-reasoning';

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

describe('useSession -> MessageList — reasoning frames render', () => {
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

  /**
   * Mounts useSession AND MessageList in the same tree, with
   * MessageList bound live to `session.streamingMoves.value` — exactly
   * how a real chat view wires them. This is what makes the assertion
   * below a render assertion rather than a state assertion.
   */
  function mountLiveView() {
    let session: ReturnType<typeof useSession> | null = null;
    const Comp = defineComponent({
      setup() {
        session = useSession(ref('s-1'));
        return () => h(MessageList, { messages: session!.streamingMoves.value });
      },
    });
    const w = mount(Comp, {
      global: {
        plugins: [{ install: (app) => provideFakeClient(app, seed()) }],
      },
    });
    return {
      w,
      get session() {
        if (!session) throw new Error('no session');
        return session;
      },
    };
  }

  function chunk(body: Record<string, unknown>) {
    rt.emit('llm:stream-chunk', {
      sub_id: SUB,
      session_id: 's-1',
      chunk: body,
    });
  }

  it('renders a reasoning delta in the DOM, not just in streamingMoves state', async () => {
    const { w, session } = mountLiveView();
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();

    chunk({ kind: 'move_start', move: { index: 0, kind: 'assistant_move' } });
    chunk({ kind: 'reasoning', reasoning: { type: 'thinking', content: 'Let me weigh the options. ' } });
    chunk({ kind: 'reasoning', reasoning: { type: 'thinking', content: 'The second is safer.' } });
    chunk({ kind: 'text', text: 'Use the second approach.' });
    await nextTick();

    // The real bug this WP fixes lived downstream of this line ever
    // being true — but a passing state assertion alone does not prove
    // the screen shows anything, hence the DOM check below.
    expect(session.streamingMoves.value[0].reasoning).toBe(
      'Let me weigh the options. The second is safer.',
    );

    const block = w.find('[data-testid="message-reasoning"]');
    expect(block.exists()).toBe(true);
    expect(block.text()).toBe('Let me weigh the options. The second is safer.');

    // The reasoning text must not leak into the answer bubble's content.
    expect(w.text()).toContain('Use the second approach.');
    const bodyText = w.text();
    const reasoningIndex = bodyText.indexOf('Let me weigh the options.');
    const answerIndex = bodyText.indexOf('Use the second approach.');
    expect(reasoningIndex).toBeGreaterThanOrEqual(0);
    expect(answerIndex).toBeGreaterThan(reasoningIndex);

    w.unmount();
  });

  it('reasoning surfaces even with no move boundary (fallback ladder parity with text)', async () => {
    const { w, session } = mountLiveView();
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();

    chunk({ kind: 'reasoning', reasoning: { type: 'thinking', content: 'Thinking without a boundary.' } });
    chunk({ kind: 'text', text: 'Answer.' });
    await nextTick();

    expect(session.streamingMoves.value).toHaveLength(1);
    const block = w.find('[data-testid="message-reasoning"]');
    expect(block.exists()).toBe(true);
    expect(block.text()).toBe('Thinking without a boundary.');
    w.unmount();
  });

  it('the no-reasoning control: an ordinary text-only stream renders no reasoning block', async () => {
    const { w, session } = mountLiveView();
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();

    chunk({ kind: 'move_start', move: { index: 0, kind: 'assistant_move' } });
    chunk({ kind: 'text', text: 'Just an ordinary answer.' });
    await nextTick();

    expect(session.streamingMoves.value[0].reasoning).toBeUndefined();
    expect(w.find('[data-testid="message-reasoning"]').exists()).toBe(false);
    expect(w.text()).toContain('Just an ordinary answer.');
    w.unmount();
  });

  it('an empty-content reasoning frame is ignored (no empty reasoning block)', async () => {
    const { w, session } = mountLiveView();
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();

    chunk({ kind: 'move_start', move: { index: 0, kind: 'assistant_move' } });
    chunk({ kind: 'reasoning', reasoning: { type: 'thinking', content: '' } });
    chunk({ kind: 'text', text: 'Answer.' });
    await nextTick();

    expect(session.streamingMoves.value[0].reasoning).toBeUndefined();
    expect(w.find('[data-testid="message-reasoning"]').exists()).toBe(false);
    w.unmount();
  });
});
