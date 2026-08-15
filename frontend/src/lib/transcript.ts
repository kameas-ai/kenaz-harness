/**
 * transcript — projects a flat list of `Message` rows into the items the
 * chat surface renders (model-moves-transcript-01PMCH01 WP04, FR-003).
 *
 * WHY THIS IS ONE FUNCTION AND NOT TWO
 *
 * A single human turn drives many model iterations. WP02 persists each
 * one as its own row and announces each one on the chat stream, with the
 * same 0-based `moveIndex` on both halves (see the contract on
 * `llm.MoveBoundary`). FR-003's requirement is that the trajectory you
 * WATCH and the trajectory you RELOAD are the same trajectory — "no
 * post-hoc mismatch between what you watched and what's stored".
 *
 * The way to make that true by construction rather than by two renderers
 * agreeing to stay in step is to have exactly one renderer. `useSession`
 * materialises in-flight moves as `Message` rows carrying the same
 * `kind` / `moveIndex` / `turnSpanId` / `toolCalls` the persisted rows
 * will carry, `MessageList` concatenates them onto the persisted list,
 * and this function runs over the concatenation. Live and reloaded
 * differ in where the rows came from and in nothing else.
 *
 * RECONCILE ON INDEX, NOT ON KIND. `MoveBoundary.Kind` is a rendering
 * hint: a streamed segment always announces `assistant_move`, but the
 * turn's last one is PERSISTED as `final`. Keying "which move is the
 * answer" off the kind would therefore flip the layout on reload. So the
 * answer is derived POSITIONALLY: within a turn span, the highest
 * `moveIndex` among the assistant-ish moves is the answer and everything
 * before it is an intermediate step. That rule gives `final` on a
 * completed turn and the last surviving segment on an interrupted one —
 * which is also what WP02's journal says the truth is for a turn that
 * never produced an answer.
 *
 * BACKWARD COMPATIBILITY. A row with no `kind` is a classic entry and is
 * emitted verbatim as a `message` item — the pre-mission render path,
 * byte for byte. Nothing here indexes, groups or chips it.
 */

import type { Message } from './types';

/** Lifecycle of an inline tool chip. */
type ToolChipStatus = 'running' | 'ok' | 'error';

/**
 * ToolChip is one tool call rendered inline in the trajectory: the
 * `tool_call` move supplies identity + the args summary, and the
 * `tool_result` move that names the same call id resolves it.
 *
 * `argsSummary` is the DISPLAY-LAYER summary the backend already
 * computed (`displayArgsSummary` — argument names and value types). Raw
 * argument values never reach this shape; the wire type has no field
 * that could carry them.
 */
export interface ToolChip {
  /** Tool-call id, the value `tool_result` binds on. */
  callId: string;
  name: string;
  argsSummary: string;
  status: ToolChipStatus;
  /**
   * What the tool returned, from the bound `tool_result` row. Shown only
   * on demand — never inlined into the conversation flow, which is the
   * regression the ledger recorded against the pre-WP04 surface.
   * Empty while the call is still running, and on the live stream, which
   * carries boundaries rather than tool output.
   */
  output: string;
}

/** An intermediate model move: a step on the way to the answer. */
interface MoveStepItem {
  type: 'move';
  key: string;
  message: Message;
  /** 1-based position among the turn's intermediate moves. */
  ordinal: number;
}

/** A tool call rendered as an inline chip. */
interface ToolStepItem {
  type: 'tool';
  key: string;
  /**
   * Row id of the `tool_result` that folded into this chip, when one
   * has landed. The folded row still exists in the store and is still
   * full-text indexed, so a search hit can deep-link to it; without its
   * id on the rendered chip the app-wide `[data-message-id]` scroll-to
   * target does not exist and the link dead-ends.
   */
  resultKey?: string;
  chip: ToolChip;
}

export type TrailStep = MoveStepItem | ToolStepItem;

/** A row rendered as a full MessageBubble: classic entries and answers. */
interface MessageItem {
  type: 'message';
  key: string;
  message: Message;
}

/**
 * A contiguous run of intermediate steps from one turn, grouped so the
 * surface can draw them as ONE trajectory under a shared rail instead of
 * as N stacked chat bubbles (spec §2 FR-003).
 */
interface TrailItem {
  type: 'trail';
  key: string;
  /** The turn span these steps belong to. */
  spanId: string;
  steps: TrailStep[];
}

export type TranscriptItem = MessageItem | TrailItem;

function chipKey(spanId: string, callId: string): string {
  return `${spanId}::${callId}`;
}

/**
 * countTurns counts HUMAN TURNS — one per user message, however many
 * model moves and tool entries answering it produced.
 *
 * It lives beside the projection because it is the same fact the
 * projection is built on: rows are not turns. The long-session nudge
 * used to approximate a turn as `rows / 2`, which held only while every
 * turn was exactly one user row plus one assistant row. A move-bearing
 * turn persists ~13 rows, so that arithmetic counted one turn as six and
 * tripped the "this session is getting long" banner roughly 3x early.
 * On a classic session this returns exactly what the halving did.
 */
export function countTurns(messages: readonly Message[]): number {
  let turns = 0;
  for (const m of messages) {
    if (m.role === 'user') turns++;
  }
  return turns;
}

/**
 * projectTranscript turns rows into render items. Pure and total: every
 * input row either becomes an item or is folded into one (a
 * `tool_result` folds into its chip), so nothing is dropped.
 */
export function projectTranscript(
  messages: readonly Message[],
): TranscriptItem[] {
  // Pass 1 — which move is each turn's answer. Positional, per the
  // header: the highest moveIndex among the span's assistant-ish moves.
  //
  // TEXTLESS MOVES RENDER NOTHING, AND SO CANNOT BE THE ANSWER. A
  // boundary reaches the surface strictly BEFORE the first token of the
  // segment it opens, so between the two the newest row is a real move
  // with no text yet; and the `final` boundary of a turn whose exit gate
  // revised the draft announces a segment whose text never streams at
  // all. Letting an empty row hold the answer position renders a blank
  // full-weight bubble while the text the user is watching demotes into
  // the muted trail — verified by rendering exactly that case.
  //
  // `useSession.commitStreamingMoves` already drops empty assistant rows
  // when the turn lands, so skipping them here is what makes the
  // streaming render and the committed render the same render, which is
  // the whole contract of this file. The promotion then happens exactly
  // once, when the first delta arrives.
  const answerIndexBySpan = new Map<string, number>();
  for (const m of messages) {
    if (m.kind !== 'assistant_move' && m.kind !== 'final') continue;
    if (m.content.length === 0) continue;
    const span = m.turnSpanId ?? '';
    const idx = m.moveIndex ?? 0;
    const current = answerIndexBySpan.get(span);
    if (current === undefined || idx >= current) {
      answerIndexBySpan.set(span, idx);
    }
  }

  const items: TranscriptItem[] = [];
  const chips = new Map<string, ToolStepItem>();
  const ordinals = new Map<string, number>();
  let trail: TrailItem | null = null;

  function closeTrail() {
    trail = null;
  }

  function pushStep(spanId: string, step: TrailStep) {
    if (trail === null || trail.spanId !== spanId) {
      trail = { type: 'trail', key: `trail-${step.key}`, spanId, steps: [] };
      items.push(trail);
    }
    trail.steps.push(step);
  }

  for (const m of messages) {
    const kind = m.kind;

    // Classic entry — the pre-mission path, untouched.
    if (!kind) {
      closeTrail();
      items.push({ type: 'message', key: m.id, message: m });
      continue;
    }

    const spanId = m.turnSpanId ?? '';

    if (kind === 'tool_call') {
      const call = m.toolCalls?.[0];
      const callId = call?.id ?? m.id;
      const chip: ToolChip = {
        callId,
        name: call?.name ?? '',
        // The tool_call row's content IS the args summary WP02 persisted.
        argsSummary: m.content ?? '',
        status: 'running',
        output: '',
      };
      const step: ToolStepItem = { type: 'tool', key: m.id, chip };
      chips.set(chipKey(spanId, callId), step);
      pushStep(spanId, step);
      continue;
    }

    if (kind === 'tool_result') {
      const call = m.toolCalls?.[0];
      const callId = call?.id ?? '';
      const status: ToolChipStatus = call?.isError ? 'error' : 'ok';
      const bound = chips.get(chipKey(spanId, callId));
      if (bound) {
        bound.chip.status = status;
        bound.chip.output = m.content ?? '';
        bound.resultKey = m.id;
        // Folded into the chip its call opened — no item of its own.
        continue;
      }
      // Orphaned result: its tool_call row was compacted away or the
      // pairing is broken. Render it as its own resolved chip rather
      // than silently dropping the fact that a tool ran.
      pushStep(spanId, {
        type: 'tool',
        key: m.id,
        chip: {
          callId,
          name: call?.name ?? '',
          argsSummary: '',
          status,
          output: m.content ?? '',
        },
      });
      continue;
    }

    // assistant_move | final. A textless one has nothing to draw — see
    // the answer-selection note above; it neither takes an ordinal nor
    // closes the open trail.
    if (m.content.length === 0) continue;
    const isAnswer = (m.moveIndex ?? 0) === answerIndexBySpan.get(spanId);
    if (isAnswer) {
      closeTrail();
      items.push({ type: 'message', key: m.id, message: m });
      continue;
    }
    const ordinal = (ordinals.get(spanId) ?? 0) + 1;
    ordinals.set(spanId, ordinal);
    pushStep(spanId, { type: 'move', key: m.id, message: m, ordinal });
  }

  return items;
}
