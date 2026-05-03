/**
 * useSession — composable that loads + caches a single session, listens
 * for `sessions:event` updates routed via the message_appended emitter,
 * exposes the message list + an in-flight assistant placeholder, and
 * persists the input draft on a 400ms debounce via `client.sessions.saveDraft`.
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
} from 'vue';
import { useHarnessClient } from './harnessClientContext';
import { useEventStream } from './useEventStream';
import { logEvent } from './eventLog';
import type { ContentBlock, Message, Session } from './types';

export interface UseSessionResult {
  session: Ref<Session | null>;
  messages: Ref<readonly Message[]>;
  loading: Ref<boolean>;
  error: Ref<string | null>;
  /** The current in-flight assistant message, or null. */
  currentlyStreaming: Ref<Message | null>;
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
  refresh(): Promise<void>;
  /**
   * Append a user message and start the assistant stream. modelOverride
   * (optional) selects a non-default model from the profile's
   * authorised set; the chat surface's switcher pill picks it.
   */
  send(content: string, profileID: string, modelOverride?: string): Promise<void>;
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
  const currentlyStreaming = ref<Message | null>(null);
  const streamSubscriptionId = ref<string | null>(null);
  const streamingTimedOut = ref(false);
  const draft = ref('');
  const showFullHistory = ref(false);
  const sweptCount = ref(0);

  let streamTimeoutHandle: ReturnType<typeof setTimeout> | null = null;
  let draftDebounceHandle: ReturnType<typeof setTimeout> | null = null;
  let lastSavedDraft = '';

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
    if (typeof fn === 'function') {
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
      draft.value = '';
      return;
    }
    loading.value = true;
    error.value = null;
    try {
      const [s, msgsResult, d] = await Promise.all([
        client.sessions.get(sessionId),
        fetchMessages(sessionId, showFullHistory.value),
        client.sessions.loadDraft(sessionId).catch(() => ''),
      ]);
      session.value = s;
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
  useEventStream<SessionEvent>('sessions:event', (payload) => {
    if (!payload || payload.kind !== 'message_appended') return;
    if (payload.sessionId !== id.value) return;
    const m = payload.message;
    if (!m) return;
    // Append, dedup by id.
    if (messages.value.some((x) => x.id === m.id)) return;
    messages.value = [...messages.value, m];
  });

  // Wire-shape payload from core/rpc/views/llm.StreamChunkPayload:
  //   { sub_id, session_id, chunk: StreamEvent }
  // where StreamEvent = { kind, text?, tool?, reasoning?, finish?, err? }.
  type WireChunk = {
    sub_id?: string;
    session_id?: string;
    chunk?: {
      kind?: string;
      text?: string;
      finish?: string;
      err?: string;
    };
  };
  type WireClosed = {
    sub_id?: string;
    session_id?: string;
    reason?: string;
    message?: string;
    finish_reason?: string;
  };

  // Subscribe to streaming chunks. Splice text deltas into
  // currentlyStreaming; surface errors via error.value.
  useEventStream<WireChunk>('llm:stream-chunk', (payload) => {
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
    const subID = payload.sub_id ?? streamSubscriptionId.value ?? 'sub';
    switch (ev.kind) {
      case 'text': {
        const delta = ev.text ?? '';
        if (!delta) return;
        const existing = currentlyStreaming.value;
        if (!existing) {
          currentlyStreaming.value = {
            id: `streaming-${subID}`,
            sessionId: id.value,
            role: 'assistant',
            content: delta,
            createdAt: new Date().toISOString(),
            streaming: true,
          };
        } else {
          currentlyStreaming.value = {
            ...existing,
            content: existing.content + delta,
          };
        }
        return;
      }
      case 'error': {
        // long-turn-resilience WP00: commit any partial assistant
        // content BEFORE surfacing the error so the bubble survives the
        // failure. We deliberately leave currentlyStreaming non-null so
        // the stream-closed handler still owns the canonical cleanup
        // path; stream-closed dedupes by id so the second commit is a
        // no-op.
        const partial = currentlyStreaming.value;
        if (partial && partial.content.length > 0) {
          const committed: Message = {
            ...partial,
            streaming: false,
            streamingError: ev.err || 'stream-error',
          };
          if (!messages.value.some((m) => m.id === committed.id)) {
            messages.value = [...messages.value, committed];
          }
        }
        if (ev.err) error.value = ev.err;
        return;
      }
      case 'finish': {
        // The terminal "finish" event arrives just before stream-closed;
        // the close handler does the commit so we don't double-append.
        return;
      }
      default:
        // tool / reasoning / usage frames not yet rendered.
        return;
    }
  });

  useEventStream<WireClosed>('llm:stream-closed', (payload) => {
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
    const finished = currentlyStreaming.value;
    if (finished) {
      // long-turn-resilience WP00: commit on every close reason, not
      // just `completed`. When the close arrived because of an error /
      // cancel / EOF-without-finish, stamp `streamingError` so the
      // bubble can render a "Connection lost" hint. Dedupe by id in
      // case the case 'error' branch above already committed.
      const isClean = payload.reason === 'completed';
      const committed: Message = {
        ...finished,
        streaming: false,
        ...(isClean
          ? {}
          : { streamingError: payload.reason || 'closed-without-finish' }),
      };
      if (!messages.value.some((m) => m.id === committed.id)) {
        messages.value = [...messages.value, committed];
      }
    }
    currentlyStreaming.value = null;
    streamSubscriptionId.value = null;
    if (payload.reason === 'backend-error' && payload.message) {
      error.value = payload.message;
    }
  });

  async function send(content: string, profileID: string, modelOverride?: string) {
    const sid = id.value;
    if (!sid) return;
    error.value = null;
    logEvent('info', 'send.requested', {
      session_id: sid,
      profile_id: profileID,
      model_override: modelOverride ?? '',
      content_bytes: content.length,
    });
    try {
      const userMsg = await client.sessions.appendMessage(sid, 'user', content);
      messages.value = [...messages.value, userMsg];
      logEvent('info', 'send.user_message_appended', {
        session_id: sid,
        message_id: userMsg.id,
      });
      const subId = await client.llm.startStream(profileID, sid, modelOverride);
      streamSubscriptionId.value = subId;
      streamingTimedOut.value = false;
      clearStreamTimeout();
      logEvent('info', 'send.stream_opened', {
        session_id: sid,
        sub_id: subId,
      });
      streamTimeoutHandle = setTimeout(() => {
        if (streamSubscriptionId.value === subId && !currentlyStreaming.value) {
          streamingTimedOut.value = true;
          logEvent('warn', 'send.stream_timed_out', {
            session_id: sid,
            sub_id: subId,
            timeout_ms: STREAM_TIMEOUT_MS,
          });
        }
      }, STREAM_TIMEOUT_MS);
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err));
      error.value = e.message;
      logEvent('error', 'send.failed', {
        session_id: sid,
        profile_id: profileID,
        message: e.message,
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
    logEvent('info', 'send.requested', {
      session_id: sid,
      profile_id: profileID,
      model_override: modelOverride ?? '',
      block_count: contentBlocks.length,
    });
    try {
      const userMsg = await client.sessions.sendMessageWithBlocks(
        sid,
        [...contentBlocks],
      );
      messages.value = [...messages.value, userMsg];
      logEvent('info', 'send.user_message_appended', {
        session_id: sid,
        message_id: userMsg.id,
      });
      const subId = await client.llm.startStream(profileID, sid, modelOverride);
      streamSubscriptionId.value = subId;
      streamingTimedOut.value = false;
      clearStreamTimeout();
      logEvent('info', 'send.stream_opened', {
        session_id: sid,
        sub_id: subId,
      });
      streamTimeoutHandle = setTimeout(() => {
        if (streamSubscriptionId.value === subId && !currentlyStreaming.value) {
          streamingTimedOut.value = true;
          logEvent('warn', 'send.stream_timed_out', {
            session_id: sid,
            sub_id: subId,
            timeout_ms: STREAM_TIMEOUT_MS,
          });
        }
      }, STREAM_TIMEOUT_MS);
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err));
      error.value = e.message;
      logEvent('error', 'send.failed', {
        session_id: sid,
        profile_id: profileID,
        message: e.message,
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
      const finished = currentlyStreaming.value;
      if (finished) {
        const committed: Message = { ...finished, streaming: false };
        messages.value = [...messages.value, committed];
      }
      currentlyStreaming.value = null;
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
      currentlyStreaming.value = null;
      streamSubscriptionId.value = null;
      streamingTimedOut.value = false;
      // showFullHistory is per-session UI state — reset on every
      // session reopen so a switch-back never resurrects the previous
      // view (compaction-strategy-ui WP07 plan §2.8).
      if (showFullHistory.value) {
        resettingShowFullHistory = true;
        showFullHistory.value = false;
      }
      clearStreamTimeout();
      void load(next);
    },
    { immediate: true },
  );

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
  });

  return {
    session,
    messages: computed(() => messages.value) as Ref<readonly Message[]>,
    loading,
    error,
    currentlyStreaming,
    streamSubscriptionId,
    streamingTimedOut,
    draft,
    showFullHistory,
    sweptCount,
    refresh,
    send,
    sendBlocks,
    cancel,
  };
}
