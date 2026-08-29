/**
 * useSession — composable that loads + caches a single session, listens
 * for `sessions:event` updates routed via the message_appended emitter,
 * exposes the message list + the in-flight turn's moves, and persists
 * the input draft on a 400ms debounce via `client.sessions.saveDraft`.
 *
 * MOVES (model-moves-transcript-01PMCH01 WP04). One human turn drives
 * many model iterations. The stream announces each with a `move_start`
 * boundary carrying the same 0-based index the persisted row will carry,
 * and this composable materialises each as its own in-flight `Message`.
 * The single buffer that used to accumulate `existing.content + delta`
 * across a whole turn — and so rendered three distinct model moves as
 * one run-on paragraph (spec §1) — is gone, not gated: a stream that
 * announces no boundary still produces exactly one bubble, because that
 * is what one segment looks like, not because a flag says so.
 *
 * The composable is route-driven: `id` is a Ref so a navigation from
 * `#/sessions#sess-a` to `#/sessions#sess-b` reloads transparently.
 *
 * Cross-mission contract: the backend session-manager emits
 *   { kind: 'message_appended', sessionId, message } on `sessions:event`
 * and the LLM stream emits per-token chunks on `llm:stream-chunk` with
 * `{ subscriptionId, sessionId, messageId, delta, done? }`. If the
 * backend isn't wired yet (chunks never arrive) the composable surfaces
 * a `streamingTimedOut` flag after 30 s so the surface can show a
 * friendly "waiting for stream…" message.
 */

import {
  computed,
  onBeforeUnmount,
  ref,
  shallowRef,
  watch,
  type Ref,
} from "vue";
import { useHarnessClient } from "./harnessClientContext";
import { useEventStream } from "./useEventStream";
import { logEvent } from "./eventLog";
import { isServedMode } from "./useServedMode";
import { useConnectionState } from "./useConnectionState";
import { friendly } from "./errors";
import type { ContentBlock, Message, Session } from "./types";

/**
 * SessionUsagePayload is the wire shape emitted on `session.usage.updated`
 * (backend-context-window-length-01KQ8TD3 WP03). The context-window
 * indicator reads promptTokens to update its numerator in near-real-time.
 */
export interface SessionUsagePayload {
  sessionId: string;
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
  costUsd: number;
  costSource: string;
}

/**
 * StreamTruncatedPayload mirrors serve.StreamTruncatedPayload
 * (core/serve/wsstream.go). It arrives ONLY in served mode, on the
 * transport-level `served:stream-truncated` event, when this browser could
 * not keep up with the live stream and the server had to drop frames.
 *
 * It exists so the surface can say "you are missing part of this reply"
 * instead of rendering a truncated answer that looks complete.
 */
export interface StreamTruncatedPayload {
  dropped: number;
  reason: string;
  message: string;
}

/**
 * STREAM_TRUNCATED_COPY / streamTruncatedCopy — SD-15
 * (served-mode-is-a-real-mode-01PMZ707 WP08).
 *
 * `reason` is a stable, machine-readable key (core/serve/wsstream.go's own
 * docstring says so) that nothing consumed until this WP: the truncation
 * notice rendered `.message` — server-composed prose — directly, and
 * `.reason` rode the wire unread. This is the "give it a consumer" branch
 * of the disposition, not deletion: keying UI copy off `.reason` decouples
 * the notice's wording from the server's prose (a copy change on either
 * side no longer requires touching the other) and degrades gracefully —
 * any `reason` this table does not recognise falls back to `.message`, so
 * a future server-side reason this frontend hasn't shipped a translation
 * for still shows real text instead of going blank.
 *
 * Only "slow-consumer" is emitted today (RAN: `grep -n 'Reason:' wsstream.go`
 * finds exactly one literal). Adding a second server-side value needs a
 * matching entry here or it silently falls back to `.message` — that
 * fallback is intentional, not a gap to close by keeping the two in
 * lockstep.
 */
export const STREAM_TRUNCATED_COPY: Record<string, string> = {
  'slow-consumer':
    'Part of this reply was dropped because this browser tab could not keep up with the stream. Reopen the conversation to see the full turn.',
};
export function streamTruncatedCopy(payload: StreamTruncatedPayload): string {
  return STREAM_TRUNCATED_COPY[payload.reason] ?? payload.message;
}

/**
 * OverflowRecoveryPayload mirrors the `chat:overflow-recovery` payload
 * emitted by ChatRunner.recoverFromOverflow
 * (core/rpc/views/agentgraph/chat/chat_runner.go). One per recovery
 * attempt: the provider rejected the request as too long, the runner
 * compacted the session and re-drove the kernel on a fresh context.
 *
 * Arrives in BOTH desktop and served mode — core/serve/wsstream.go
 * forwards the topic verbatim, so the served surface is not a fake.
 */
export interface OverflowRecoveryPayload {
  sub_id?: string;
  session_id?: string;
  /** 1-based recovery attempt within this turn. */
  attempt?: number;
  /** The turn's MaxOverflowRecoveries budget. */
  budget?: number;
}

export interface UseSessionResult {
  session: Ref<Session | null>;
  messages: Ref<readonly Message[]>;
  loading: Ref<boolean>;
  error: Ref<string | null>;
  /**
   * Discriminator for `error` when the backend supplied one on the
   * stream-closed payload. "session_full" means the conversation
   * exceeded the model's context window and retrying will not help; the
   * surface renders different copy and a different CTA. null otherwise.
   */
  errorKind: Ref<string | null>;
  /**
   * The in-flight turn's moves, oldest first — empty when no stream is
   * open (model-moves-transcript-01PMCH01 WP04).
   *
   * Each entry is already shaped as the transcript row it will become:
   * same `kind`, same `moveIndex`, same `toolCalls`. The chat surface
   * concatenates them onto the persisted list and runs ONE projection
   * over the result, so what you watch and what you reload are the same
   * render (FR-003). A stream that never announces a move boundary —
   * every pre-mission code path — produces exactly one kind-less entry
   * here, which is byte-for-byte the old single-buffer behaviour.
   */
  streamingMoves: Ref<readonly Message[]>;
  /** Active subscription id from `client.llm.startStream`, or null. */
  streamSubscriptionId: Ref<string | null>;
  /** True if the surface should warn the user a stream never started. */
  streamingTimedOut: Ref<boolean>;
  /** Two-way draft buffer; debounced-saved automatically. */
  draft: Ref<string>;
  /**
   * Per-session UI state for the compaction-strategy-ui WP07 "Show
   * full history" toggle. Two-way; flipping it triggers a refetch
   * (active vs. all). NOT persisted — resets on session reopen.
   */
  showFullHistory: Ref<boolean>;
  /**
   * Count of soft-archived rows that have since been hard-deleted by
   * the soft-archive sweep. Surfaced from the WP07 ListMessagesResult
   * envelope. The frontend renders the
   * "N earlier turns no longer available" placeholder when this is > 0.
   * Always 0 today — the per-session counter lands in WP09.
   */
  sweptCount: Ref<number>;
  /**
   * Most-recent per-turn usage snapshot. Updated two ways: live, via the
   * `session.usage.updated` broker event
   * (backend-context-window-length-01KQ8TD3 WP03); and on every `load()`,
   * from the fetched session's own `lastUsage` field
   * (chat-turn-integrity-01PMZ606 WP11, task #37) — which is what makes
   * this survive a session reopen or app restart instead of resetting to
   * null until the next live turn. null only when the session genuinely
   * has no completed turn yet. The composer footer
   * (composerUsageEstimate) and the context-window indicator
   * (contextNumerator) both read promptTokens / costUsd from this ref.
   */
  lastUsage: Ref<SessionUsagePayload | null>;
  /**
   * Set when the served transport tells us it could not deliver every
   * stream frame to this browser. Non-null means the transcript on screen
   * is INCOMPLETE and the surface must say so — silently showing a short
   * answer as if it were the whole answer is the failure mode this
   * exists to prevent. Always null in desktop mode. Cleared when a new
   * turn starts or the session changes.
   */
  streamTruncated: Ref<StreamTruncatedPayload | null>;
  /**
   * Set when the backend hit a context overflow MID-TURN, compacted the
   * session, and re-drove the run (`chat:overflow-recovery`). Non-null
   * means the turn is still coming and the pause on screen has an
   * explanation.
   *
   * Deliberately not the same signal as `errorKind === "session_full"`.
   * That one arrives on stream-closed and means the turn is over with no
   * answer; this one is the recovery that keeps it from getting there.
   * Cleared when a new turn starts or the session changes.
   */
  overflowRecovery: Ref<OverflowRecoveryPayload | null>;
  refresh(): Promise<void>;
  /**
   * Append a user message and start the assistant stream. modelOverride
   * (optional) selects a non-default model from the profile's
   * authorised set; the chat surface's switcher pill picks it.
   */
  send(
    content: string,
    profileID: string,
    modelOverride?: string,
  ): Promise<void>;
  /**
   * Multimodal-aware send (multimodal-io WP04). Persists the user
   * turn through Sessions_SendMessageWithBlocks, then opens the
   * assistant stream the same way `send` does. Caller passes the
   * full `contentBlocks` array — image / document blocks first, text
   * block last.
   */
  sendBlocks(
    contentBlocks: readonly ContentBlock[],
    profileID: string,
    modelOverride?: string,
  ): Promise<void>;
  /** Cancel an in-flight stream. */
  cancel(): Promise<void>;
}

const STREAM_TIMEOUT_MS = 30_000;
const DRAFT_DEBOUNCE_MS = 400;

export function useSession(id: Ref<string>): UseSessionResult {
  const client = useHarnessClient();

  const session = ref<Session | null>(null);
  const messages = shallowRef<readonly Message[]>([]);
  const loading = ref(false);
  const error = ref<string | null>(null);
  /**
   * Discriminator for the current `error`, when the backend supplied one
   * on the stream-closed payload. "session_full" means the conversation
   * no longer fits the model's context window and retrying will not
   * help — the surface renders different copy and a different CTA for
   * it. null for every ordinary error.
   */
  const errorKind = ref<string | null>(null);
  const streamingMoves = shallowRef<readonly Message[]>([]);
  /**
   * Position of the assistant move currently receiving text deltas
   * within `streamingMoves`, or -1 when none is open. A move boundary
   * closes the open segment; the next boundary opens the next one.
   * THIS is what replaced the unconditional `existing.content + delta`
   * concatenation that glued a whole turn into one run-on paragraph.
   */
  let openMoveSlot = -1;
  const streamSubscriptionId = ref<string | null>(null);
  const streamingTimedOut = ref(false);
  const draft = ref("");
  const showFullHistory = ref(false);
  const sweptCount = ref(0);
  const lastUsage = ref<SessionUsagePayload | null>(null);
  const streamTruncated = ref<StreamTruncatedPayload | null>(null);
  const overflowRecovery = ref<OverflowRecoveryPayload | null>(null);

  let streamTimeoutHandle: ReturnType<typeof setTimeout> | null = null;
  let draftDebounceHandle: ReturnType<typeof setTimeout> | null = null;
  let lastSavedDraft = "";

  // ── served-mode Sessions_Stream wiring (FR-007) ─────────────────────────
  //
  // In native (Wails) mode, session/elicit/usage events arrive over the
  // window.runtime event bridge, so no explicit subscription is needed —
  // useEventStream() is already listening. In served mode there is no Wails
  // bridge; the only way those events reach the browser is the Sessions_Stream
  // WebSocket, which harnessClient.ts's served client opens via
  // sessions.startStream(). Nothing was calling it, which left the served UI
  // inert after a reconnect (pending elicitations never re-surfaced).
  //
  // We open the stream for the active session and re-open it on reconnect so
  // the elicit:pending / elicit:pending:snapshot frames flow again after the
  // WS drops. This is a no-op in native mode (guarded by isServedMode()).
  const served = isServedMode();
  const connection = useConnectionState();
  let servedStreamSubId: string | null = null;

  async function openServedStream(sessionID: string): Promise<void> {
    if (!served || !sessionID) return;
    await closeServedStream();
    try {
      servedStreamSubId = await client.sessions.startStream(sessionID);
    } catch (err) {
      // Best-effort: the reconnect poller in servedTransport will retry the
      // underlying connection; surfacing this would be noise.
      logEvent("warn", "served.session_stream.open_failed", {
        session_id: sessionID,
        message: err instanceof Error ? err.message : String(err),
      });
    }
  }

  async function closeServedStream(): Promise<void> {
    const sub = servedStreamSubId;
    servedStreamSubId = null;
    if (!sub) return;
    try {
      await client.sessions.stopStream(sub);
    } catch {
      // Best-effort teardown.
    }
  }

  function clearStreamTimeout() {
    if (streamTimeoutHandle) {
      clearTimeout(streamTimeoutHandle);
      streamTimeoutHandle = null;
    }
  }

  // fetchMessages dispatches to the WP07 envelope methods when the
  // client exposes them (the harness binding ships them by default; a
  // few legacy fake clients in tests stub only listMessages). Falls
  // back to the flat listMessages call so pre-WP07 fakes keep working.
  async function fetchMessages(
    sessionId: string,
    fullHistory: boolean,
  ): Promise<{ messages: readonly Message[]; sweptCount: number }> {
    const sessionsClient = client.sessions as typeof client.sessions & {
      listMessagesActive?: typeof client.sessions.listMessagesActive;
      listMessagesAll?: typeof client.sessions.listMessagesAll;
    };
    const fn = fullHistory
      ? sessionsClient.listMessagesAll
      : sessionsClient.listMessagesActive;
    if (typeof fn === "function") {
      const res = await fn.call(sessionsClient, sessionId);
      return { messages: res.messages, sweptCount: res.sweptCount };
    }
    const flat = await sessionsClient.listMessages(sessionId);
    return { messages: flat, sweptCount: 0 };
  }

  async function load(sessionId: string) {
    if (!sessionId) {
      session.value = null;
      messages.value = [];
      sweptCount.value = 0;
      draft.value = "";
      return;
    }
    loading.value = true;
    error.value = null;
    errorKind.value = null;
    try {
      const [s, msgsResult, d] = await Promise.all([
        client.sessions.get(sessionId),
        fetchMessages(sessionId, showFullHistory.value),
        client.sessions.loadDraft(sessionId).catch(() => ""),
      ]);
      session.value = s;
      // chat-turn-integrity-01PMZ606 WP11 (task #37, C-5): repopulate the
      // per-turn usage snapshot from the fetched session on every load —
      // not just from the live `session.usage.updated` broker event. The
      // composer footer (composerUsageEstimate) and the context-window
      // meter (contextNumerator) both read this SAME ref
      // (SessionsView.vue), so without this line both readouts showed
      // 0 tok · $0.0000 on every session reopen and every app restart,
      // even though `s.lastUsage` — read from the persisted
      // sessions.last_usage_json column — already carried the real
      // number. Explicitly null when the session has no persisted usage
      // yet (a session that has never completed a turn), matching the
      // watch(id, …) session-switch reset below rather than leaking a
      // stale value from the previously loaded session.
      lastUsage.value = s.lastUsage
        ? { sessionId, ...s.lastUsage }
        : null;
      messages.value = msgsResult.messages;
      sweptCount.value = msgsResult.sweptCount;
      draft.value = d;
      lastSavedDraft = d;
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err));
      error.value = e.message;
      session.value = null;
      messages.value = [];
      sweptCount.value = 0;
    } finally {
      loading.value = false;
    }
  }

  async function refresh() {
    await load(id.value);
  }

  // Subscribe to backend message_appended events for this session.
  type SessionEvent = {
    kind?: string;
    sessionId?: string;
    message?: Message;
  };
  useEventStream<SessionEvent>("sessions:event", (payload) => {
    if (!payload || payload.kind !== "message_appended") return;
    if (payload.sessionId !== id.value) return;
    const m = payload.message;
    if (!m) return;
    // Append, dedup by id.
    if (messages.value.some((x) => x.id === m.id)) return;
    messages.value = [...messages.value, m];
  });

  // Subscribe to per-turn usage snapshots emitted after each completed
  // LLM turn (backend-context-window-length-01KQ8TD3 WP03). Updates
  // lastUsage so the context-window indicator reads promptTokens
  // without calling GetUsage.
  useEventStream<SessionUsagePayload>("session.usage.updated", (payload) => {
    if (!payload) return;
    if (payload.sessionId !== id.value) return;
    lastUsage.value = payload;
  });

  // Wire-shape payload from core/rpc/views/llm.StreamChunkPayload:
  //   { sub_id, session_id, chunk: StreamEvent }
  // where StreamEvent = { kind, text?, tool?, reasoning?, finish?, err? }.
  /**
   * WireMoveBoundary mirrors core/llm.MoveBoundary — the payload of a
   * `move_start` chunk (model-moves-transcript-01PMCH01 WP02/WP04).
   *
   * `kind` is a RENDERING HINT, not the persisted kind: a streamed
   * segment always announces "assistant_move" even when the turn will
   * persist its last one as "final". Everything downstream therefore
   * reconciles on `index` — see lib/transcript.ts.
   */
  type WireMoveBoundary = {
    index?: number;
    kind?: string;
    tool_name?: string;
    tool_call_id?: string;
    args_summary?: string;
    is_error?: boolean;
  };
  type WireChunk = {
    sub_id?: string;
    session_id?: string;
    chunk?: {
      kind?: string;
      text?: string;
      finish?: string;
      err?: string;
      move?: WireMoveBoundary;
    };
  };
  type WireClosed = {
    sub_id?: string;
    session_id?: string;
    reason?: string;
    message?: string;
    finish_reason?: string;
    /**
     * Discriminates WHY the stream closed, for the cases where `message`
     * alone leaves the surface guessing. Mirrors
     * StreamClosedPayload.ErrorKind in
     * core/rpc/views/agentgraph/chat/stream_bridge.go.
     *
     * "session_full" is the only value today. Match on this rather than
     * on `message` — that field is a human sentence and will be reworded.
     */
    error_kind?: string;
  };

  /**
   * The turn span id stamped on in-flight moves. The stream announces a
   * move's position but not the id of the user message that opened the
   * turn, so live rows carry a per-stream key instead. It only has to
   * group this turn's moves together and separate them from every other
   * turn's — which it does — and a reload replaces these rows with the
   * server's, carrying the durable span. The projection's output is the
   * same either way.
   */
  function liveSpanID(subID: string): string {
    return `live:${subID}`;
  }

  /**
   * appendMoveBoundary opens the next move of the in-flight turn.
   *
   * This is the consumer the WP02 ledger entry named: a boundary
   * CLOSES the segment currently receiving deltas and starts a new row,
   * which is what stops a turn's segments gluing into one paragraph.
   */
  function appendMoveBoundary(subID: string, mv: WireMoveBoundary) {
    const index = mv.index ?? 0;
    const rowID = `streaming-${subID}-${index}`;
    const common = {
      id: rowID,
      sessionId: id.value,
      createdAt: new Date().toISOString(),
      moveIndex: index,
      turnSpanId: liveSpanID(subID),
    };

    if (mv.kind === "tool_call" || mv.kind === "tool_result") {
      const isResult = mv.kind === "tool_result";
      const summary = mv.args_summary ?? "";
      const row: Message = {
        ...common,
        role: "tool",
        // The tool_call row's content is the args SUMMARY, exactly as
        // the persisted row carries it. Raw arguments are not on this
        // wire at all. A tool_result carries its output only on the
        // persisted row — the stream announces boundaries, it is not a
        // channel for unbounded tool output.
        content: isResult ? "" : summary,
        kind: mv.kind,
        toolCalls: [
          {
            id: mv.tool_call_id ?? "",
            name: mv.tool_name ?? "",
            argsSummary: isResult ? "" : summary,
            ...(isResult ? { isError: mv.is_error === true } : {}),
          },
        ],
      };
      openMoveSlot = -1;
      streamingMoves.value = [...streamingMoves.value, row];
      return;
    }

    // An assistant segment. Announced BEFORE its first token, per the
    // MoveBoundary contract, so the bubble exists to receive them.
    const row: Message = {
      ...common,
      role: "assistant",
      content: "",
      kind: "assistant_move",
      streaming: true,
    };
    const next = [...streamingMoves.value, row];
    openMoveSlot = next.length - 1;
    streamingMoves.value = next;
  }

  /** appendDelta routes one text delta into the open move. */
  function appendDelta(subID: string, delta: string) {
    const rows = streamingMoves.value;
    if (openMoveSlot >= 0 && openMoveSlot < rows.length) {
      const target = rows[openMoveSlot];
      const next = rows.slice();
      next[openMoveSlot] = { ...target, content: target.content + delta };
      streamingMoves.value = next;
      return;
    }
    // No segment is open. Fall back to the newest assistant row: a delta
    // that arrived without its boundary belongs to the most recent
    // segment, and inventing a move index for it would break the
    // index-based reconciliation with the persisted rows.
    for (let i = rows.length - 1; i >= 0; i--) {
      if (rows[i].role !== "assistant") continue;
      const next = rows.slice();
      next[i] = { ...rows[i], content: rows[i].content + delta };
      openMoveSlot = i;
      streamingMoves.value = next;
      return;
    }
    // Nothing at all yet: a stream with no move boundaries — every
    // pre-mission path. One kind-less bubble that grows delta by delta,
    // which is precisely the behaviour this surface had before WP04 and
    // renders through the projection's classic branch untouched.
    const row: Message = {
      id: `streaming-${subID}`,
      sessionId: id.value,
      role: "assistant",
      content: delta,
      createdAt: new Date().toISOString(),
      streaming: true,
    };
    openMoveSlot = rows.length;
    streamingMoves.value = [...rows, row];
  }

  /**
   * commitStreamingMoves lands the in-flight moves in `messages` and
   * clears the buffer. `failure` non-null stamps the partial-output
   * marker on the LAST move — the only one the drop actually truncated.
   *
   * Empty assistant rows are dropped: a boundary whose fire produced
   * only tool calls opened no visible segment.
   */
  function commitStreamingMoves(failure: string | null) {
    const rows = streamingMoves.value;
    streamingMoves.value = [];
    openMoveSlot = -1;
    if (rows.length === 0) return;
    const keep = rows.filter(
      (m) => m.role !== "assistant" || m.content.length > 0,
    );
    if (keep.length === 0) return;
    const existing = new Set(messages.value.map((m) => m.id));
    const committed: Message[] = [];
    keep.forEach((m, i) => {
      if (existing.has(m.id)) return;
      const last = i === keep.length - 1;
      committed.push(
        failure !== null && last
          ? { ...m, streaming: false, streamingError: failure }
          : { ...m, streaming: false },
      );
    });
    if (committed.length === 0) return;
    messages.value = [...messages.value, ...committed];
  }

  // Subscribe to streaming chunks. Route move boundaries + text deltas
  // into streamingMoves; surface errors via error.value.
  useEventStream<WireChunk>("llm:stream-chunk", (payload) => {
    if (!payload || !payload.chunk) return;
    if (payload.session_id && payload.session_id !== id.value) return;
    if (
      streamSubscriptionId.value &&
      payload.sub_id &&
      payload.sub_id !== streamSubscriptionId.value
    ) {
      return;
    }
    clearStreamTimeout();
    streamingTimedOut.value = false;
    const ev = payload.chunk;
    const subID = payload.sub_id ?? streamSubscriptionId.value ?? "sub";
    switch (ev.kind) {
      case "move_start": {
        appendMoveBoundary(subID, ev.move ?? {});
        return;
      }
      case "text": {
        const delta = ev.text ?? "";
        if (!delta) return;
        appendDelta(subID, delta);
        return;
      }
      case "error": {
        // long-turn-resilience WP00: when the stream errors mid-flight,
        // commit any partial assistant content BEFORE surfacing the
        // error so the user's bubble persists with a "Connection lost"
        // sub-line. Dedupe by id — the subsequent stream-closed handler
        // would otherwise re-append the same row.
        commitStreamingMoves(ev.err || "stream error");
        if (ev.err) error.value = ev.err;
        return;
      }
      case "finish": {
        // The terminal "finish" event arrives just before stream-closed;
        // the close handler does the commit so we don't double-append.
        return;
      }
      default:
        // tool / reasoning / usage frames not yet rendered.
        return;
    }
  });

  useEventStream<WireClosed>("llm:stream-closed", (payload) => {
    if (!payload) return;
    if (payload.session_id && payload.session_id !== id.value) return;
    if (
      streamSubscriptionId.value &&
      payload.sub_id &&
      payload.sub_id !== streamSubscriptionId.value
    ) {
      return;
    }
    clearStreamTimeout();
    // long-turn-resilience WP00: commit the partial buffer regardless of
    // `reason`. Previously only `completed` would commit; any other
    // reason silently dropped the bubble. Dedupes against the case-error
    // path, which may already have committed under the same stable ids.
    commitStreamingMoves(
      payload.reason === "completed"
        ? null
        : payload.reason || "closed-without-finish",
    );
    streamSubscriptionId.value = null;
    if (payload.reason === "backend-error" && payload.message) {
      error.value = payload.message;
      // A session that ran out of context is not a failed send: the
      // user's message IS in the transcript and the model simply never
      // got to answer. The surface needs to say so and offer the way
      // out, rather than the generic retry framing every other
      // backend-error gets.
      errorKind.value = payload.error_kind ?? null;
    }
  });

  // Mid-turn context-overflow recovery (chat:overflow-recovery,
  // core/rpc/views/agentgraph/chat/chat_runner.go). The runner hit a
  // provider context overflow, compacted the session and re-drove the
  // kernel on a fresh context — the turn is still coming.
  //
  // Without this the event had no subscriber at all: the backend emitted
  // it "so the surface can show the user what happened" and the surface
  // showed nothing, leaving a multi-second stall during compaction+redrive
  // indistinguishable from a hang. The terminal session-full report
  // (errorKind on stream-closed) does not cover this — by the time it
  // fires, recovery has already failed.
  useEventStream<OverflowRecoveryPayload>(
    "chat:overflow-recovery",
    (payload) => {
      if (!payload) return;
      if (payload.session_id && payload.session_id !== id.value) return;
      overflowRecovery.value = payload;
      logEvent("info", "chat.overflow_recovery", {
        session_id: payload.session_id ?? "",
        attempt: payload.attempt ?? 0,
        budget: payload.budget ?? 0,
      });
    },
  );

  // Served-mode transport backpressure (core/serve/wsstream.go). The
  // server drops frames rather than blocking the harness-wide event bus
  // when this browser cannot keep up, and tells us how many. We surface
  // it instead of letting the user read a half-answer as a whole one.
  //
  // Not filtered by session id: the notice is a property of THIS
  // connection, and the server cannot attribute a frame it never
  // delivered to a particular conversation.
  useEventStream<StreamTruncatedPayload>(
    "served:stream-truncated",
    (payload) => {
      if (!payload) return;
      streamTruncated.value = payload;
      logEvent("warn", "served.stream.truncated", {
        dropped: payload.dropped,
        reason: payload.reason,
      });
    },
  );

  // Auth-resume seam (provider-keychain-rotation-01KQ8TD9 follow-up):
  // RedriveLastTurn on the backend re-issues StartStream with a fresh
  // sub_id but the auth-pause path leaves the frontend with the OLD
  // sub_id still in streamSubscriptionId AND a "paused for key rotation"
  // streamingError committed onto the last bubble. Without this handler,
  // events from the resumed stream get filtered out by the stream-chunk
  // sub_id guard and the chat surface stays wedged with a stale banner.
  useEventStream<{ profile_id: string; new_sub_id: string }>(
    "provider:auth-resumed",
    (payload) => {
      if (!payload?.new_sub_id) return;
      streamSubscriptionId.value = payload.new_sub_id;
      streamingTimedOut.value = false;
      // Clear the synthetic "auth failure — paused for key rotation"
      // streamingError off the most-recent message if that's what set it.
      const last = messages.value[messages.value.length - 1];
      if (
        last &&
        typeof last.streamingError === "string" &&
        last.streamingError.includes("auth failure")
      ) {
        const cleaned: Message = { ...last, streamingError: undefined };
        messages.value = [...messages.value.slice(0, -1), cleaned];
      }
      if (
        typeof error.value === "string" &&
        error.value.includes("auth failure")
      ) {
        error.value = null;
        errorKind.value = null;
      }
    },
  );

  async function send(
    content: string,
    profileID: string,
    modelOverride?: string,
  ) {
    const sid = id.value;
    if (!sid) return;
    error.value = null;
    errorKind.value = null;
    // A new turn starts a fresh stream, so any truncation notice from the
    // previous one no longer describes what is on screen.
    streamTruncated.value = null;
    overflowRecovery.value = null;
    logEvent("info", "send.requested", {
      session_id: sid,
      profile_id: profileID,
      model_override: modelOverride ?? "",
      content_bytes: content.length,
    });
    try {
      const userMsg = await client.sessions.appendMessage(sid, "user", content);
      messages.value = [...messages.value, userMsg];
      logEvent("info", "send.user_message_appended", {
        session_id: sid,
        message_id: userMsg.id,
      });
      const subId = await client.llm.startStream(profileID, sid, modelOverride);
      streamSubscriptionId.value = subId;
      streamingTimedOut.value = false;
      clearStreamTimeout();
      logEvent("info", "send.stream_opened", {
        session_id: sid,
        sub_id: subId,
      });
      streamTimeoutHandle = setTimeout(() => {
        if (
          streamSubscriptionId.value === subId &&
          streamingMoves.value.length === 0
        ) {
          streamingTimedOut.value = true;
          logEvent("warn", "send.stream_timed_out", {
            session_id: sid,
            sub_id: subId,
            timeout_ms: STREAM_TIMEOUT_MS,
          });
        }
      }, STREAM_TIMEOUT_MS);
    } catch (err) {
      // FR-008 (agent-loop-robustness-parity WP08): prefer the structured
      // RPCErrorEnvelope hint over the raw Go string. friendly() also
      // humanises served-mode and attachment errors, so the served chat
      // surface never renders an internal error verbatim.
      const msg = friendly(err);
      error.value = msg;
      logEvent("error", "send.failed", {
        session_id: sid,
        profile_id: profileID,
        message: msg,
      });
    }
  }

  async function sendBlocks(
    contentBlocks: readonly ContentBlock[],
    profileID: string,
    modelOverride?: string,
  ) {
    const sid = id.value;
    if (!sid) return;
    if (contentBlocks.length === 0) return;
    error.value = null;
    errorKind.value = null;
    streamTruncated.value = null;
    overflowRecovery.value = null;
    logEvent("info", "send.requested", {
      session_id: sid,
      profile_id: profileID,
      model_override: modelOverride ?? "",
      block_count: contentBlocks.length,
    });
    try {
      const userMsg = await client.sessions.sendMessageWithBlocks(sid, [
        ...contentBlocks,
      ]);
      messages.value = [...messages.value, userMsg];
      logEvent("info", "send.user_message_appended", {
        session_id: sid,
        message_id: userMsg.id,
      });
      const subId = await client.llm.startStream(profileID, sid, modelOverride);
      streamSubscriptionId.value = subId;
      streamingTimedOut.value = false;
      clearStreamTimeout();
      logEvent("info", "send.stream_opened", {
        session_id: sid,
        sub_id: subId,
      });
      streamTimeoutHandle = setTimeout(() => {
        if (
          streamSubscriptionId.value === subId &&
          streamingMoves.value.length === 0
        ) {
          streamingTimedOut.value = true;
          logEvent("warn", "send.stream_timed_out", {
            session_id: sid,
            sub_id: subId,
            timeout_ms: STREAM_TIMEOUT_MS,
          });
        }
      }, STREAM_TIMEOUT_MS);
    } catch (err) {
      // FR-008: surface the structured RPCErrorEnvelope hint when available;
      // humanise everything else rather than leaking a Go error string.
      const msg = friendly(err);
      error.value = msg;
      logEvent("error", "send.failed", {
        session_id: sid,
        profile_id: profileID,
        message: msg,
      });
    }
  }

  async function cancel() {
    const subId = streamSubscriptionId.value;
    if (!subId) return;
    try {
      await client.llm.stopStream(subId);
    } catch {
      // Best-effort; the stream-closed event will tidy up.
    } finally {
      clearStreamTimeout();
      streamSubscriptionId.value = null;
      // A cancel is a clean stop, not a drop: the moves the user already
      // watched stay, unmarked.
      commitStreamingMoves(null);
    }
  }

  // Debounced draft persistence.
  watch(draft, (next) => {
    if (next === lastSavedDraft) return;
    if (draftDebounceHandle) clearTimeout(draftDebounceHandle);
    const sid = id.value;
    if (!sid) return;
    draftDebounceHandle = setTimeout(() => {
      lastSavedDraft = next;
      void client.sessions.saveDraft(sid, next).catch(() => {
        // Soft-fail: drafts are best-effort.
      });
    }, DRAFT_DEBOUNCE_MS);
  });

  // resettingShowFullHistory suppresses the showFullHistory watcher
  // when the id watcher resets the flag during a session switch — we
  // don't want a session-switch to issue a redundant load() call.
  let resettingShowFullHistory = false;

  watch(
    id,
    (next) => {
      streamingMoves.value = [];
      openMoveSlot = -1;
      streamSubscriptionId.value = null;
      streamingTimedOut.value = false;
      lastUsage.value = null;
      streamTruncated.value = null;
      overflowRecovery.value = null;
      // showFullHistory is per-session UI state — reset on every
      // session reopen so a switch-back never resurrects the previous
      // view (compaction-strategy-ui WP07 plan §2.8).
      if (showFullHistory.value) {
        resettingShowFullHistory = true;
        showFullHistory.value = false;
      }
      clearStreamTimeout();
      void load(next);
      // (Re)open the served-mode Sessions_Stream for the new active session so
      // elicit/session events reach the browser (no-op in native mode).
      void openServedStream(next);
    },
    { immediate: true },
  );

  // Re-open the served-mode stream when the connection recovers. The WS is
  // torn down on connection loss; without re-subscribing, a reconnect would
  // leave the browser without the elicit:pending:snapshot frame that
  // re-surfaces an in-flight ask (FR-007). No-op in native mode.
  if (served) {
    watch(connection, (state, prev) => {
      if (state === "ready" && prev !== "ready" && id.value) {
        void openServedStream(id.value);
      }
    });
  }

  // Refetch on toggle. Vue's default-lazy watch only fires on
  // value change, so the very first fire is whatever the user
  // (or the session-reset above) flipped to.
  watch(showFullHistory, () => {
    if (resettingShowFullHistory) {
      resettingShowFullHistory = false;
      return;
    }
    if (!id.value) return;
    void load(id.value);
  });

  onBeforeUnmount(() => {
    clearStreamTimeout();
    if (draftDebounceHandle) clearTimeout(draftDebounceHandle);
    void closeServedStream();
  });

  return {
    session,
    messages: computed(() => messages.value) as Ref<readonly Message[]>,
    loading,
    error,
    errorKind,
    streamingMoves,
    streamSubscriptionId,
    streamingTimedOut,
    draft,
    showFullHistory,
    sweptCount,
    lastUsage,
    streamTruncated,
    overflowRecovery,
    refresh,
    send,
    sendBlocks,
    cancel,
  };
}
