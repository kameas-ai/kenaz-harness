/**
 * projectTranscript — the one projection the live view and a reload both
 * run through (model-moves-transcript-01PMCH01 WP04, FR-003).
 */

import { describe, it, expect } from 'vitest';
import { countTurns, projectTranscript, type TranscriptItem } from '@/lib/transcript';
import type { Message } from '@/lib/types';

function row(overrides: Partial<Message>): Message {
  return {
    id: 'm',
    sessionId: 's-1',
    role: 'assistant',
    content: '',
    createdAt: '2026-08-14T00:00:00Z',
    ...overrides,
  };
}

/**
 * The turn from spec §1: five model iterations, four tool calls, one of
 * which fails. 13 persisted rows for one human question — exactly the
 * shape the unwired ledger recorded as rendering wrong.
 */
const SPAN = 'u-1';
function fiveIterationTurn(): Message[] {
  return [
    row({ id: 'u-1', role: 'user', content: 'Where is the config read?' }),
    row({
      id: 'r-0',
      kind: 'assistant_move',
      moveIndex: 0,
      turnSpanId: SPAN,
      content: 'Let me explore it.',
    }),
    row({
      id: 'r-1',
      role: 'tool',
      kind: 'tool_call',
      moveIndex: 1,
      turnSpanId: SPAN,
      content: 'path:string',
      toolCalls: [{ id: 'call-a', name: 'read_file', argsSummary: 'path:string' }],
    }),
    row({
      id: 'r-2',
      role: 'tool',
      kind: 'tool_result',
      moveIndex: 2,
      turnSpanId: SPAN,
      content: 'line one\nline two\nline three',
      toolCalls: [{ id: 'call-a', name: 'read_file', argsSummary: '' }],
    }),
    row({
      id: 'r-3',
      kind: 'assistant_move',
      moveIndex: 3,
      turnSpanId: SPAN,
      content: 'The native filesystem tools are blocked, but I can use bash.',
    }),
    row({
      id: 'r-4',
      role: 'tool',
      kind: 'tool_call',
      moveIndex: 4,
      turnSpanId: SPAN,
      content: 'cmd:string',
      toolCalls: [{ id: 'call-b', name: 'bash', argsSummary: 'cmd:string' }],
    }),
    row({
      id: 'r-5',
      role: 'tool',
      kind: 'tool_result',
      moveIndex: 5,
      turnSpanId: SPAN,
      content: 'permission denied',
      toolCalls: [{ id: 'call-b', name: 'bash', argsSummary: '', isError: true }],
    }),
    row({
      id: 'r-6',
      kind: 'assistant_move',
      moveIndex: 6,
      turnSpanId: SPAN,
      content: "Good, I've found it.",
    }),
    row({
      id: 'r-7',
      kind: 'final',
      moveIndex: 7,
      turnSpanId: SPAN,
      content: 'The config is read in core/config/load.go.',
    }),
  ];
}

function kinds(items: TranscriptItem[]): string[] {
  return items.map((i) => i.type);
}

describe('projectTranscript — move segmentation', () => {
  it('gives every model segment its own item — no run-on concatenation', () => {
    const items = projectTranscript(fiveIterationTurn());
    // user bubble, trail, answer bubble.
    expect(kinds(items)).toEqual(['message', 'trail', 'message']);

    const trail = items[1];
    if (trail.type !== 'trail') throw new Error('expected trail');
    const moves = trail.steps.filter((s) => s.type === 'move');
    expect(moves).toHaveLength(3);
    // Each segment stands alone; nothing is glued to its neighbour.
    expect(moves.map((m) => (m.type === 'move' ? m.message.content : ''))).toEqual([
      'Let me explore it.',
      'The native filesystem tools are blocked, but I can use bash.',
      "Good, I've found it.",
    ]);
    expect(moves.map((m) => (m.type === 'move' ? m.ordinal : 0))).toEqual([1, 2, 3]);
  });

  it('renders the turn answer as its own full bubble, not a trail step', () => {
    const items = projectTranscript(fiveIterationTurn());
    const last = items[items.length - 1];
    if (last.type !== 'message') throw new Error('expected message');
    expect(last.message.content).toBe('The config is read in core/config/load.go.');
  });

  it('folds each tool_result into the chip its tool_call opened', () => {
    const items = projectTranscript(fiveIterationTurn());
    const trail = items[1];
    if (trail.type !== 'trail') throw new Error('expected trail');
    const chips = trail.steps.filter((s) => s.type === 'tool');
    // Four tool ROWS in, two chips out — results never get a row of
    // their own in the conversation flow.
    expect(chips).toHaveLength(2);
  });
});

describe('projectTranscript — chip status machine', () => {
  it('running while unanswered, ok once its result lands', () => {
    const call = row({
      id: 't-1',
      role: 'tool',
      kind: 'tool_call',
      moveIndex: 0,
      turnSpanId: SPAN,
      content: 'path:string',
      toolCalls: [{ id: 'c1', name: 'read_file', argsSummary: 'path:string' }],
    });
    const running = projectTranscript([call]);
    const first = running[0];
    if (first.type !== 'trail' || first.steps[0].type !== 'tool') {
      throw new Error('expected tool chip');
    }
    expect(first.steps[0].chip.status).toBe('running');
    expect(first.steps[0].chip.output).toBe('');

    const resolved = projectTranscript([
      call,
      row({
        id: 't-2',
        role: 'tool',
        kind: 'tool_result',
        moveIndex: 1,
        turnSpanId: SPAN,
        content: 'file contents',
        toolCalls: [{ id: 'c1', name: 'read_file', argsSummary: '' }],
      }),
    ]);
    const done = resolved[0];
    if (done.type !== 'trail' || done.steps[0].type !== 'tool') {
      throw new Error('expected tool chip');
    }
    expect(done.steps[0].chip.status).toBe('ok');
    expect(done.steps[0].chip.output).toBe('file contents');
  });

  it('running → error when the result is flagged isError', () => {
    const items = projectTranscript([
      row({
        id: 't-1',
        role: 'tool',
        kind: 'tool_call',
        moveIndex: 0,
        turnSpanId: SPAN,
        content: 'cmd:string',
        toolCalls: [{ id: 'c1', name: 'bash', argsSummary: 'cmd:string' }],
      }),
      row({
        id: 't-2',
        role: 'tool',
        kind: 'tool_result',
        moveIndex: 1,
        turnSpanId: SPAN,
        content: 'permission denied',
        toolCalls: [{ id: 'c1', name: 'bash', argsSummary: '', isError: true }],
      }),
    ]);
    const trail = items[0];
    if (trail.type !== 'trail' || trail.steps[0].type !== 'tool') {
      throw new Error('expected tool chip');
    }
    expect(trail.steps[0].chip.status).toBe('error');
  });

  it('binds a result to its own call, not to whichever ran first', () => {
    const items = projectTranscript([
      row({
        id: 'a-call',
        role: 'tool',
        kind: 'tool_call',
        moveIndex: 0,
        turnSpanId: SPAN,
        toolCalls: [{ id: 'c1', name: 'read_file', argsSummary: '' }],
      }),
      row({
        id: 'b-call',
        role: 'tool',
        kind: 'tool_call',
        moveIndex: 1,
        turnSpanId: SPAN,
        toolCalls: [{ id: 'c2', name: 'bash', argsSummary: '' }],
      }),
      // Parallel dispatch: c2 answers first, and it failed.
      row({
        id: 'b-res',
        role: 'tool',
        kind: 'tool_result',
        moveIndex: 2,
        turnSpanId: SPAN,
        content: 'boom',
        toolCalls: [{ id: 'c2', name: 'bash', argsSummary: '', isError: true }],
      }),
    ]);
    const trail = items[0];
    if (trail.type !== 'trail') throw new Error('expected trail');
    const chips = trail.steps.flatMap((s) => (s.type === 'tool' ? [s.chip] : []));
    expect(chips.map((c) => [c.callId, c.status])).toEqual([
      ['c1', 'running'],
      ['c2', 'error'],
    ]);
  });
});

describe('projectTranscript — live/reload reconciliation', () => {
  /**
   * The same turn as the live stream produced it. Two differences from
   * the persisted rows, both from the MoveBoundary contract:
   *   - the last segment announces `assistant_move`; persistence calls
   *     it `final`;
   *   - the live rows carry the per-stream span key, not the durable id.
   * Reconciling on Kind would therefore mismatch. Reconciling on Index
   * does not.
   */
  function liveTurn(): Message[] {
    const persisted = fiveIterationTurn();
    return persisted.map((m) => {
      if (!m.kind) return m;
      return {
        ...m,
        id: `streaming-sub-x-${m.moveIndex}`,
        kind: m.kind === 'final' ? 'assistant_move' : m.kind,
        turnSpanId: 'live:sub-x',
        // The live stream carries boundaries, not tool output.
        content: m.kind === 'tool_result' ? '' : m.content,
      } as Message;
    });
  }

  function trajectory(items: TranscriptItem[]): unknown[] {
    return items.map((item) => {
      if (item.type === 'message') {
        return ['message', item.message.role, item.message.content];
      }
      return [
        'trail',
        item.steps.map((s) =>
          s.type === 'move'
            ? ['move', s.ordinal, s.message.content]
            : ['tool', s.chip.name, s.chip.argsSummary, s.chip.status],
        ),
      ];
    });
  }

  it('renders the same trajectory live and reloaded', () => {
    const live = trajectory(projectTranscript(liveTurn()));
    const reloaded = trajectory(projectTranscript(fiveIterationTurn()));
    expect(live).toEqual(reloaded);
  });

  it('an interrupted turn falls back to its last move as the answer', () => {
    // No `final` at all — the stop landed mid-trajectory.
    const rows = fiveIterationTurn().slice(0, -1);
    const items = projectTranscript(rows);
    const last = items[items.length - 1];
    if (last.type !== 'message') throw new Error('expected message');
    expect(last.message.content).toBe("Good, I've found it.");
  });
});

describe('projectTranscript — classic rows', () => {
  it('passes rows with no move metadata through unchanged and unindexed', () => {
    const classic: Message[] = [
      row({ id: 'a', role: 'user', content: 'q' }),
      row({ id: 'b', role: 'assistant', content: 'r' }),
      row({ id: 'c', role: 'tool', content: 'legacy tool line' }),
      row({ id: 'd', role: 'system', content: 'note' }),
    ];
    const items = projectTranscript(classic);
    expect(items).toEqual(
      classic.map((m) => ({ type: 'message', key: m.id, message: m })),
    );
  });

  it('does not group a classic assistant row into a neighbouring trail', () => {
    const items = projectTranscript([
      row({
        id: 'r-0',
        kind: 'assistant_move',
        moveIndex: 0,
        turnSpanId: SPAN,
        content: 'step',
      }),
      row({ id: 'legacy', role: 'assistant', content: 'classic' }),
      row({ id: 'r-1', kind: 'final', moveIndex: 1, turnSpanId: SPAN, content: 'answer' }),
    ]);
    expect(kinds(items)).toEqual(['trail', 'message', 'message']);
  });
});

describe('projectTranscript — degenerate data', () => {
  it('keeps an orphaned tool_result visible as its own resolved chip', () => {
    const items = projectTranscript([
      row({
        id: 'orphan',
        role: 'tool',
        kind: 'tool_result',
        moveIndex: 4,
        turnSpanId: SPAN,
        content: 'the call row was compacted away',
        toolCalls: [{ id: 'gone', name: 'bash', argsSummary: '', isError: true }],
      }),
      row({ id: 'r-9', kind: 'final', moveIndex: 5, turnSpanId: SPAN, content: 'answer' }),
    ]);
    const trail = items[0];
    if (trail.type !== 'trail' || trail.steps[0].type !== 'tool') {
      throw new Error('expected tool chip');
    }
    expect(trail.steps[0].chip.status).toBe('error');
    expect(trail.steps[0].chip.output).toBe('the call row was compacted away');
  });

  it('separates two turns that sit back to back', () => {
    const items = projectTranscript([
      row({ id: 'a-0', kind: 'assistant_move', moveIndex: 0, turnSpanId: 'span-a', content: 's1' }),
      row({ id: 'a-1', kind: 'final', moveIndex: 1, turnSpanId: 'span-a', content: 'answer a' }),
      row({ id: 'b-0', kind: 'assistant_move', moveIndex: 0, turnSpanId: 'span-b', content: 's2' }),
      row({ id: 'b-1', kind: 'final', moveIndex: 1, turnSpanId: 'span-b', content: 'answer b' }),
    ]);
    expect(kinds(items)).toEqual(['trail', 'message', 'trail', 'message']);
  });
});

describe('countTurns — the long-session nudge input', () => {
  it('counts one turn per human message, not per row', () => {
    // 9 rows, one question: the shape the old `rows / 2` arithmetic read
    // as four-and-a-half turns.
    const rows = fiveIterationTurn();
    expect(rows).toHaveLength(9);
    expect(countTurns(rows)).toBe(1);
  });

  it('agrees with the old arithmetic on a classic session', () => {
    const classic: Message[] = [];
    for (let i = 0; i < 30; i++) {
      classic.push(row({ id: `u-${i}`, role: 'user', content: 'q' }));
      classic.push(row({ id: `a-${i}`, role: 'assistant', content: 'r' }));
    }
    expect(countTurns(classic)).toBe(30);
    expect(countTurns(classic)).toBe(Math.floor(classic.length / 2));
  });

  it('is zero for a transcript with no human messages', () => {
    expect(countTurns([row({ id: 'sys', role: 'system', content: 'note' })])).toBe(0);
  });
});

describe('projectTranscript — adversarial review follow-ups (WP04)', () => {
  /**
   * The pre-existing "binds a result to its own call" case only pins the
   * FORWARD direction (a later call's result must not land on an earlier
   * chip). Mutation evidence: replacing chipKey's body with
   * `${spanId}::` — i.e. binding to whichever call was registered last,
   * ignoring the id entirely — survived the whole suite. It survives
   * because the fixture never lets the EARLIER call resolve second,
   * which is the ordering parallel dispatch actually produces half the
   * time. This is that ordering.
   */
  it('an earlier call resolving LAST still binds to its own chip', () => {
    const items = projectTranscript([
      row({
        id: 'c1',
        role: 'tool',
        kind: 'tool_call',
        moveIndex: 0,
        turnSpanId: SPAN,
        toolCalls: [{ id: 'c1', name: 'read_file', argsSummary: '' }],
      }),
      row({
        id: 'c2',
        role: 'tool',
        kind: 'tool_call',
        moveIndex: 1,
        turnSpanId: SPAN,
        toolCalls: [{ id: 'c2', name: 'bash', argsSummary: '' }],
      }),
      // c2 answers first and failed; c1 answers second and succeeded.
      row({
        id: 'r2',
        role: 'tool',
        kind: 'tool_result',
        moveIndex: 2,
        turnSpanId: SPAN,
        content: 'boom',
        toolCalls: [{ id: 'c2', name: 'bash', argsSummary: '', isError: true }],
      }),
      row({
        id: 'r1',
        role: 'tool',
        kind: 'tool_result',
        moveIndex: 3,
        turnSpanId: SPAN,
        content: 'fine',
        toolCalls: [{ id: 'c1', name: 'read_file', argsSummary: '' }],
      }),
    ]);
    const trail = items[0];
    if (trail.type !== 'trail') throw new Error('expected trail');
    const chips = trail.steps.flatMap((s) => (s.type === 'tool' ? [s.chip] : []));
    expect(chips.map((c) => [c.callId, c.status, c.output])).toEqual([
      ['c1', 'ok', 'fine'],
      ['c2', 'error', 'boom'],
    ]);
  });

  /**
   * A folded tool_result keeps living in the store and in the FTS index,
   * so a search hit can deep-link to it. The chip is where it renders,
   * so the chip has to carry its row id or SessionsView's scroll-to
   * poller spins for five seconds and gives up silently.
   */
  it('a folded tool_result keeps its row id on the chip step', () => {
    const items = projectTranscript(fiveIterationTurn());
    const trail = items[1];
    if (trail.type !== 'trail') throw new Error('expected trail');
    const tools = trail.steps.filter((s) => s.type === 'tool');
    expect(tools.map((s) => (s.type === 'tool' ? [s.key, s.resultKey] : []))).toEqual([
      ['r-1', 'r-2'],
      ['r-4', 'r-5'],
    ]);
  });

  /**
   * A boundary reaches the surface BEFORE the first token of the segment
   * it opens, and a turn whose exit gate revised the draft announces a
   * final segment whose text never streams at all. If an empty row can
   * take the answer position, the surface renders a blank full-weight
   * bubble while the text the user is watching demotes into the muted
   * trail.
   */
  it('an empty newest move does not steal the answer slot', () => {
    const items = projectTranscript([
      row({ id: 'u-1', role: 'user', content: 'go' }),
      row({
        id: 'r-0',
        kind: 'assistant_move',
        moveIndex: 0,
        turnSpanId: SPAN,
        content: 'the text the user is watching',
      }),
      row({ id: 'r-1', kind: 'assistant_move', moveIndex: 1, turnSpanId: SPAN, content: '' }),
    ]);
    // The empty row draws nothing at all; the texted one stays the
    // full-weight answer instead of demoting into a trail behind a
    // blank bubble.
    expect(kinds(items)).toEqual(['message', 'message']);
    const last = items[1];
    if (last.type !== 'message') throw new Error('expected the texted move as the answer');
    expect(last.message.content).toBe('the text the user is watching');
  });

  it('draws nothing for a span whose every move is textless', () => {
    const items = projectTranscript([
      row({ id: 'r-0', kind: 'assistant_move', moveIndex: 0, turnSpanId: SPAN, content: '' }),
      row({ id: 'r-1', kind: 'assistant_move', moveIndex: 1, turnSpanId: SPAN, content: '' }),
    ]);
    // Not a headless trail of two empty ordinals, and not a blank
    // bubble. The same rows commitStreamingMoves drops.
    expect(items).toEqual([]);
  });

  it('a textless move does not break the open trail in two', () => {
    const items = projectTranscript([
      row({ id: 'r-0', kind: 'assistant_move', moveIndex: 0, turnSpanId: SPAN, content: 'one' }),
      row({ id: 'r-1', kind: 'assistant_move', moveIndex: 1, turnSpanId: SPAN, content: '' }),
      row({ id: 'r-2', kind: 'assistant_move', moveIndex: 2, turnSpanId: SPAN, content: 'two' }),
      row({ id: 'r-3', kind: 'final', moveIndex: 3, turnSpanId: SPAN, content: 'answer' }),
    ]);
    expect(kinds(items)).toEqual(['trail', 'message']);
    const trail = items[0];
    if (trail.type !== 'trail') throw new Error('expected trail');
    expect(trail.steps.map((s) => (s.type === 'move' ? s.ordinal : -1))).toEqual([1, 2]);
  });
});
