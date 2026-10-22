/**
 * useSession — re-read a completed turn's committed rows (audit finding
 * HR-7, 2026-10-21). Forked from useSession.moves.test.ts's harness.
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

let persisted: Message[] = [];

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
      listMessages: async () => persisted,
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

describe('useSession — tool output after a completed turn (HR-7)', () => {
  let rt: FakeRuntime;

  beforeEach(() => {
    persisted = [];
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

  function chunk(body: Record<string, unknown>) {
    rt.emit('llm:stream-chunk', { sub_id: SUB, session_id: 's-1', chunk: body });
  }

  function runToolTurn() {
    chunk({
      kind: 'move_start',
      move: { index: 0, kind: 'tool_call', tool_name: 'kenaz__list_dir', tool_call_id: 'c1', args_summary: 'path=<string>' },
    });
    chunk({
      kind: 'move_start',
      move: { index: 1, kind: 'tool_result', tool_name: 'kenaz__list_dir', tool_call_id: 'c1', is_error: true },
    });
    chunk({ kind: 'move_start', move: { index: 2, kind: 'final' } });
    chunk({ kind: 'text', text: 'That failed.' });
  }

  const row = (m: Partial<Message> & Pick<Message, 'id' | 'role'>): Message =>
    ({ sessionId: 's-1', content: '', createdAt: '2026-10-21T00:00:00Z', ...m }) as Message;

  it('swaps the live rows for the committed ones, so a failed chip has its error text', async () => {
    const { w, session } = mountSession();
    await vi.runAllTimersAsync();
    await session.send('list it', 'p');
    await nextTick();
    runToolTurn();

    persisted = [
      row({ id: 'u-1', role: 'user', content: 'list it' }),
      row({
        id: 'm-call', role: 'tool', kind: 'tool_call', moveIndex: 0, turnSpanId: 'u-1',
        content: 'path=<string>',
        toolCalls: [{ id: 'c1', name: 'kenaz__list_dir', argsSummary: 'path=<string>' }],
      }),
      row({
        id: 'm-res', role: 'tool', kind: 'tool_result', moveIndex: 1, turnSpanId: 'u-1',
        content: 'permission denied: /workspace',
        toolCalls: [{ id: 'c1', name: 'kenaz__list_dir', argsSummary: '', isError: true }],
      }),
      row({ id: 'm-final', role: 'assistant', kind: 'final', moveIndex: 2, turnSpanId: 'u-1', content: 'That failed.' }),
    ];
    rt.emit('llm:stream-closed', { sub_id: SUB, session_id: 's-1', reason: 'completed' });
    await vi.runAllTimersAsync();
    await nextTick();

    const ids = session.messages.value.map((m) => m.id);
    expect(ids).toEqual(['u-1', 'm-call', 'm-res', 'm-final']);
    const res = session.messages.value.find((m) => m.kind === 'tool_result');
    expect(res?.content).toBe('permission denied: /workspace');
    w.unmount();
  });

  it('keeps the live rows when nothing has been committed yet', async () => {
    const { w, session } = mountSession();
    await vi.runAllTimersAsync();
    await session.send('list it', 'p');
    await nextTick();
    runToolTurn();
    persisted = [row({ id: 'u-1', role: 'user', content: 'list it' })];
    rt.emit('llm:stream-closed', { sub_id: SUB, session_id: 's-1', reason: 'completed' });
    await vi.runAllTimersAsync();
    await nextTick();
    expect(session.messages.value.map((m) => m.kind ?? 'classic')).toEqual([
      'classic',
      'tool_call',
      'tool_result',
      // A live segment is always announced as assistant_move; the
      // persisted row would say `final` — the live rows were kept.
      'assistant_move',
    ]);
    w.unmount();
  });
});
