/**
 * useSession — the move-boundary consumer
 * (model-moves-transcript-01PMCH01 WP04).
 *
 * WP02 emits one `move_start` per persisted move, in persisted order,
 * carrying the same 0-based index the row will carry. These tests pin
 * the half WP04 owns: the boundary CLOSES the segment currently
 * receiving deltas and opens a new one, so a turn's segments never glue
 * into a single paragraph — and a stream that announces no boundaries at
 * all still produces exactly one bubble, as it did before the mission.
 */

import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { defineComponent, h, ref, nextTick } from 'vue';
import { mount } from '@vue/test-utils';
import { useSession } from '@/lib/useSession';
import { provideFakeClient } from '@/lib/harnessClientContext';
import type { HarnessClient } from '@/lib/harnessClient';
import { setConnectionState } from '@/lib/useConnectionState';
import type { Message } from '@/lib/types';

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

describe('useSession — move boundaries', () => {
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

  function mountSession() {
    let session: ReturnType<typeof useSession> | null = null;
    const Comp = defineComponent({
      setup() {
        session = useSession(ref('s-1'));
        return () => h('div');
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

  function chunk(rt: FakeRuntime, body: Record<string, unknown>) {
    rt.emit('llm:stream-chunk', {
      sub_id: SUB,
      session_id: 's-1',
      chunk: body,
    });
  }

  it('starts a NEW bubble at each boundary instead of gluing segments', async () => {
    const { w, session } = mountSession();
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();

    chunk(rt, { kind: 'move_start', move: { index: 0, kind: 'assistant_move' } });
    chunk(rt, { kind: 'text', text: 'Let me ' });
    chunk(rt, { kind: 'text', text: 'explore it.' });
    chunk(rt, { kind: 'move_start', move: { index: 1, kind: 'assistant_move' } });
    chunk(rt, { kind: 'text', text: 'The native tools are blocked.' });
    await nextTick();

    const moves = session.streamingMoves.value;
    expect(moves).toHaveLength(2);
    expect(moves.map((m) => m.content)).toEqual([
      'Let me explore it.',
      'The native tools are blocked.',
    ]);
    expect(moves.map((m) => m.moveIndex)).toEqual([0, 1]);
    // The regression this exists to stop: one bubble holding both.
    expect(moves[0].content).not.toContain('The native tools');
    w.unmount();
  });

  it('materialises tool boundaries as bindable tool rows', async () => {
    const { w, session } = mountSession();
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();

    chunk(rt, { kind: 'move_start', move: { index: 0, kind: 'assistant_move' } });
    chunk(rt, { kind: 'text', text: 'Looking.' });
    chunk(rt, {
      kind: 'move_start',
      move: {
        index: 1,
        kind: 'tool_call',
        tool_name: 'bash',
        tool_call_id: 'c1',
        args_summary: 'cmd:string',
      },
    });
    await nextTick();

    let rows = session.streamingMoves.value;
    expect(rows).toHaveLength(2);
    expect(rows[1].kind).toBe('tool_call');
    expect(rows[1].role).toBe('tool');
    expect(rows[1].content).toBe('cmd:string');
    expect(rows[1].toolCalls?.[0]).toMatchObject({ id: 'c1', name: 'bash' });

    chunk(rt, {
      kind: 'move_start',
      move: {
        index: 2,
        kind: 'tool_result',
        tool_name: 'bash',
        tool_call_id: 'c1',
        is_error: true,
      },
    });
    await nextTick();

    rows = session.streamingMoves.value;
    expect(rows).toHaveLength(3);
    expect(rows[2].kind).toBe('tool_result');
    expect(rows[2].toolCalls?.[0].isError).toBe(true);
    w.unmount();
  });

  it('routes deltas into the open segment, not into a tool row', async () => {
    const { w, session } = mountSession();
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();

    chunk(rt, { kind: 'move_start', move: { index: 0, kind: 'assistant_move' } });
    chunk(rt, { kind: 'text', text: 'first' });
    chunk(rt, {
      kind: 'move_start',
      move: { index: 1, kind: 'tool_call', tool_name: 'bash', tool_call_id: 'c1' },
    });
    chunk(rt, { kind: 'move_start', move: { index: 2, kind: 'assistant_move' } });
    chunk(rt, { kind: 'text', text: 'second' });
    await nextTick();

    const rows = session.streamingMoves.value;
    expect(rows.map((m) => m.content)).toEqual(['first', '', 'second']);
    w.unmount();
  });

  it('a stream with no boundaries still produces exactly one bubble', async () => {
    const { w, session } = mountSession();
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();

    chunk(rt, { kind: 'text', text: 'Hello' });
    chunk(rt, { kind: 'text', text: ', world' });
    await nextTick();

    const rows = session.streamingMoves.value;
    expect(rows).toHaveLength(1);
    expect(rows[0].content).toBe('Hello, world');
    // No move metadata — renders through the projection's classic branch.
    expect(rows[0].kind).toBeUndefined();
    expect(rows[0].moveIndex).toBeUndefined();
    w.unmount();
  });

  it('commits every move on close, dropping segments that produced no text', async () => {
    const { w, session } = mountSession();
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();

    chunk(rt, { kind: 'move_start', move: { index: 0, kind: 'assistant_move' } });
    chunk(rt, { kind: 'text', text: 'Looking.' });
    chunk(rt, {
      kind: 'move_start',
      move: {
        index: 1,
        kind: 'tool_call',
        tool_name: 'bash',
        tool_call_id: 'c1',
        args_summary: 'cmd:string',
      },
    });
    chunk(rt, {
      kind: 'move_start',
      move: { index: 2, kind: 'tool_result', tool_name: 'bash', tool_call_id: 'c1' },
    });
    // A fire that only emitted tool calls: boundary, no tokens.
    chunk(rt, { kind: 'move_start', move: { index: 3, kind: 'assistant_move' } });
    chunk(rt, { kind: 'move_start', move: { index: 4, kind: 'assistant_move' } });
    chunk(rt, { kind: 'text', text: 'Found it.' });
    rt.emit('llm:stream-closed', {
      sub_id: SUB,
      session_id: 's-1',
      reason: 'completed',
    });
    await nextTick();

    expect(session.streamingMoves.value).toHaveLength(0);
    const kinds = session.messages.value.map((m) => m.kind ?? 'classic');
    expect(kinds).toEqual([
      'classic', // the user turn
      'assistant_move',
      'tool_call',
      'tool_result',
      'assistant_move',
    ]);
    w.unmount();
  });

  it('marks only the last move when the stream drops', async () => {
    const { w, session } = mountSession();
    await vi.runAllTimersAsync();
    await session.send('q', 'p');
    await nextTick();

    chunk(rt, { kind: 'move_start', move: { index: 0, kind: 'assistant_move' } });
    chunk(rt, { kind: 'text', text: 'first segment' });
    chunk(rt, { kind: 'move_start', move: { index: 1, kind: 'assistant_move' } });
    chunk(rt, { kind: 'text', text: 'truncated' });
    rt.emit('llm:stream-closed', {
      sub_id: SUB,
      session_id: 's-1',
      reason: 'transient',
    });
    await nextTick();

    const assistant = session.messages.value.filter((m) => m.role === 'assistant');
    expect(assistant).toHaveLength(2);
    expect(assistant[0].streamingError).toBeUndefined();
    expect(assistant[1].streamingError).toBe('transient');
    w.unmount();
  });
});
