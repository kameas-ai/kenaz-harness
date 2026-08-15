/**
 * foldedTurnCounts — the "Summary of N turns" arithmetic
 * (model-moves-transcript-01PMCH01 WP05).
 *
 * The indicator counted archived ROWS. That was already 2x wrong on a
 * classic session (one exchange is two rows) and became ~13x wrong once
 * WP02 started persisting a turn's whole trajectory: three compacted
 * turns rendered as "Summary of 39 turns".
 *
 * MUTATION CHECK. Replacing the body with the pre-WP05 row count —
 *   counts.set(into, (counts.get(into) ?? 0) + 1)
 * — makes `counts three move-bearing turns as three` and
 * `counts a classic exchange as one turn, not two rows` both fail.
 */

import { describe, it, expect } from 'vitest';
import { foldedTurnCounts } from '@/lib/transcript';
import type { Message } from '@/lib/types';

const SUMMARY = 'm-summary';

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

/** One human turn, persisted the way WP02 persists it: 13 rows. */
function moveBearingTurn(n: number): Message[] {
  const span = `u-${n}`;
  const out: Message[] = [
    row({
      id: span,
      role: 'user',
      content: `question ${n}`,
      archivedAt: '2026-08-14T00:00:00Z',
      compactedIntoId: SUMMARY,
    }),
  ];
  for (let i = 0; i < 12; i++) {
    out.push(
      row({
        id: `${span}-r${i}`,
        role: i % 3 === 0 ? 'assistant' : 'tool',
        kind:
          i % 3 === 0 ? 'assistant_move' : i % 3 === 1 ? 'tool_call' : 'tool_result',
        moveIndex: i,
        turnSpanId: span,
        content: `step ${i}`,
        archivedAt: '2026-08-14T00:00:00Z',
        compactedIntoId: SUMMARY,
      }),
    );
  }
  return out;
}

describe('foldedTurnCounts', () => {
  it('counts a classic exchange as one turn, not two rows', () => {
    const rows = [
      row({
        id: 'm-pre-1',
        role: 'user',
        content: 'q',
        compactedIntoId: SUMMARY,
      }),
      row({
        id: 'm-pre-2',
        role: 'assistant',
        content: 'a',
        compactedIntoId: SUMMARY,
      }),
      row({ id: SUMMARY, role: 'system', content: 'summary' }),
    ];
    expect(foldedTurnCounts(rows).get(SUMMARY)).toBe(1);
  });

  it('counts three move-bearing turns as three', () => {
    // 39 rows. The pre-WP05 indicator said "Summary of 39 turns".
    const rows = [
      ...moveBearingTurn(1),
      ...moveBearingTurn(2),
      ...moveBearingTurn(3),
      row({ id: SUMMARY, role: 'system', content: 'summary' }),
    ];
    expect(rows.filter((r) => r.compactedIntoId === SUMMARY)).toHaveLength(39);
    expect(foldedTurnCounts(rows).get(SUMMARY)).toBe(3);
  });

  it('ignores rows that were not folded into a summary', () => {
    const rows = [
      row({ id: 'u-live', role: 'user', content: 'live question' }),
      row({ id: 'a-live', role: 'assistant', content: 'live answer' }),
      ...moveBearingTurn(1),
    ];
    expect(foldedTurnCounts(rows).get(SUMMARY)).toBe(1);
  });

  it('counts an orphaned folded row as its own turn', () => {
    // Its opening user row was archived into an earlier summary and is
    // no longer in the list. Attributing it to nothing would under-count
    // the summary to zero.
    const rows = [
      row({ id: 'a-orphan', role: 'assistant', content: 'a', compactedIntoId: SUMMARY }),
    ];
    expect(foldedTurnCounts(rows).get(SUMMARY)).toBe(1);
  });

  it('keeps separate summaries separate', () => {
    const rows = [
      row({ id: 'u-1', role: 'user', content: 'q1', compactedIntoId: 's-a' }),
      row({ id: 'a-1', role: 'assistant', content: 'a1', compactedIntoId: 's-a' }),
      row({ id: 'u-2', role: 'user', content: 'q2', compactedIntoId: 's-b' }),
      row({ id: 'a-2', role: 'assistant', content: 'a2', compactedIntoId: 's-b' }),
      row({ id: 'u-3', role: 'user', content: 'q3', compactedIntoId: 's-b' }),
      row({ id: 'a-3', role: 'assistant', content: 'a3', compactedIntoId: 's-b' }),
    ];
    const counts = foldedTurnCounts(rows);
    expect(counts.get('s-a')).toBe(1);
    expect(counts.get('s-b')).toBe(2);
  });
});
