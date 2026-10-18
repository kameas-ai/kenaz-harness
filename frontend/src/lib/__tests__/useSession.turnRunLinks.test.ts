/**
 * The live and just-finished turn link to their run graph
 * (agentgraph-settings-linkage-01DOGF0D review H2).
 *
 * Rows here are built by the REAL useSession stream path — move_start /
 * text chunks, then llm:stream-closed committing the buffer — not hand
 * fixtures. That path stamps `turnSpanId = "live:<sub id>"` on every
 * in-flight move and the commit keeps it until a reload, so the
 * recorded turn -> run mapping (keyed by the durable span) can never
 * match those rows. Before the fix the newest turn therefore read "not
 * recorded" until the user switched sessions. Since WP02 the sub id IS
 * the kernel run id, so MessageList links a live span directly.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { defineComponent, h, ref, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { createMemoryHistory, createRouter } from 'vue-router';
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

// The shape WP02's newChatRunID produces: the stream sub id the frontend
// holds is the kernel run id.
const RUN = 'chat-01J9ZZZZZZZZZZZZZZZZZZZZZZ';

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
          createdAt: '2026-10-04T00:00:00Z',
        }) as Message,
      saveDraft: async () => undefined,
      loadDraft: async () => '',
    } as never,
    llm: {
      listProviders: async () => [],
      startStream: async () => RUN,
      stopStream: async () => undefined,
    } as never,
  };
}

function router() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [
      { path: '/', component: { template: '<div />' } },
      { path: '/agentgraph/run/:runId', component: { template: '<div />' } },
      { path: '/agentgraph/run/:runId/graph', component: { template: '<div />' } },
    ],
  });
}

describe('useSession -> MessageList — the live turn links to its run', () => {
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

  it('links the streaming answer and, after close, the committed one — with an empty mapping', async () => {
    let session: ReturnType<typeof useSession> | null = null;
    const Comp = defineComponent({
      setup() {
        session = useSession(ref('s-1'));
        return () => h('div');
      },
    });
    const host = mount(Comp, {
      global: { plugins: [{ install: (app) => provideFakeClient(app, seed()) }] },
    });
    await vi.runAllTimersAsync();
    const s = session!;
    await s.send('what changed?', 'p');
    await nextTick();

    const chunk = (body: Record<string, unknown>) =>
      rt.emit('llm:stream-chunk', { sub_id: RUN, session_id: 's-1', chunk: body });
    chunk({ kind: 'move_start', move: { index: 0, kind: 'assistant_move' } });
    chunk({ kind: 'text', text: 'Two files changed.' });
    await nextTick();

    // The production shape, not a guess: the live span is live:<sub id>.
    expect(s.streamingMoves.value[0]?.turnSpanId).toBe(`live:${RUN}`);

    // The mapping fetch has not caught up (or keyed the durable span):
    // the strip must still link the live turn to its run.
    const turnRuns = new Map<string, string>();
    const live = mount(MessageList, {
      props: {
        messages: s.messages.value,
        streamingMessages: s.streamingMoves.value,
        turnRuns,
      },
      global: { plugins: [router()] },
    });
    expect(live.get('[data-testid="turn-run-graph-link"]').attributes('href')).toBe(
      `/agentgraph/run/${RUN}/graph`,
    );
    expect(live.find('[data-testid="turn-run-unrecorded"]').exists()).toBe(false);

    // Close the stream: the buffer is committed into messages with the
    // live span intact, and nothing reloads the transcript.
    rt.emit('llm:stream-closed', { sub_id: RUN, session_id: 's-1', reason: 'completed' });
    await nextTick();
    const committed = s.messages.value.find((m) => m.content === 'Two files changed.');
    expect(committed?.turnSpanId).toBe(`live:${RUN}`);

    const after = mount(MessageList, {
      props: { messages: s.messages.value, streamingMessages: [], turnRuns },
      global: { plugins: [router()] },
    });
    expect(after.get('[data-testid="turn-run-graph-link"]').attributes('href')).toBe(
      `/agentgraph/run/${RUN}/graph`,
    );
    expect(after.get('[data-testid="turn-run-details-link"]').attributes('href')).toBe(
      `/agentgraph/run/${RUN}`,
    );
    expect(after.find('[data-testid="turn-run-unrecorded"]').exists()).toBe(false);

    live.unmount();
    after.unmount();
    host.unmount();
  });
});
