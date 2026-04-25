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
import type { Message, Session, StreamChunk } from './types';

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
  refresh(): Promise<void>;
  /** Append a user message and start the assistant stream. */
  send(content: string, profileID: string): Promise<void>;
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

  let streamTimeoutHandle: ReturnType<typeof setTimeout> | null = null;
  let draftDebounceHandle: ReturnType<typeof setTimeout> | null = null;
  let lastSavedDraft = '';

  function clearStreamTimeout() {
    if (streamTimeoutHandle) {
      clearTimeout(streamTimeoutHandle);
      streamTimeoutHandle = null;
    }
  }

  async function load(sessionId: string) {
    if (!sessionId) {
      session.value = null;
      messages.value = [];
      draft.value = '';
      return;
    }
    loading.value = true;
    error.value = null;
    try {
      const [s, msgs, d] = await Promise.all([
        client.sessions.get(sessionId),
        client.sessions.listMessages(sessionId),
        client.sessions.loadDraft(sessionId).catch(() => ''),
      ]);
      session.value = s;
      messages.value = msgs;
      draft.value = d;
      lastSavedDraft = d;
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err));
      error.value = e.message;
      session.value = null;
      messages.value = [];
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

  // Subscribe to streaming chunks. Splice into currentlyStreaming.
  useEventStream<StreamChunk>('llm:stream-chunk', (chunk) => {
    if (!chunk || chunk.sessionId !== id.value) return;
    if (
      streamSubscriptionId.value &&
      chunk.subscriptionId !== streamSubscriptionId.value
    ) {
      return;
    }
    clearStreamTimeout();
    streamingTimedOut.value = false;
    const existing = currentlyStreaming.value;
    if (!existing || existing.id !== chunk.messageId) {
      currentlyStreaming.value = {
        id: chunk.messageId,
        sessionId: chunk.sessionId,
        role: 'assistant',
        content: chunk.delta,
        createdAt: new Date().toISOString(),
        streaming: true,
        toolCalls: chunk.toolCallDelta ? [chunk.toolCallDelta] : undefined,
      };
    } else {
      const next: Message = {
        ...existing,
        content: existing.content + chunk.delta,
        toolCalls: chunk.toolCallDelta
          ? [...(existing.toolCalls ?? []), chunk.toolCallDelta]
          : existing.toolCalls,
      };
      currentlyStreaming.value = next;
    }
    if (chunk.done) {
      const finished = currentlyStreaming.value;
      if (finished) {
        const committed: Message = { ...finished, streaming: false };
        messages.value = [...messages.value, committed];
      }
      currentlyStreaming.value = null;
      streamSubscriptionId.value = null;
    }
  });

  useEventStream<{ id: string; reason?: string }>(
    'llm:stream-closed',
    (payload) => {
      if (!payload) return;
      if (
        streamSubscriptionId.value &&
        payload.id !== streamSubscriptionId.value
      ) {
        return;
      }
      clearStreamTimeout();
      const finished = currentlyStreaming.value;
      if (finished) {
        const committed: Message = { ...finished, streaming: false };
        messages.value = [...messages.value, committed];
      }
      currentlyStreaming.value = null;
      streamSubscriptionId.value = null;
    },
  );

  async function send(content: string, profileID: string) {
    const sid = id.value;
    if (!sid) return;
    error.value = null;
    try {
      const userMsg = await client.sessions.appendMessage(sid, 'user', content);
      messages.value = [...messages.value, userMsg];
      const subId = await client.llm.startStream(profileID, sid);
      streamSubscriptionId.value = subId;
      streamingTimedOut.value = false;
      clearStreamTimeout();
      streamTimeoutHandle = setTimeout(() => {
        if (streamSubscriptionId.value === subId && !currentlyStreaming.value) {
          streamingTimedOut.value = true;
        }
      }, STREAM_TIMEOUT_MS);
    } catch (err) {
      const e = err instanceof Error ? err : new Error(String(err));
      error.value = e.message;
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

  watch(
    id,
    (next) => {
      currentlyStreaming.value = null;
      streamSubscriptionId.value = null;
      streamingTimedOut.value = false;
      clearStreamTimeout();
      void load(next);
    },
    { immediate: true },
  );

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
    refresh,
    send,
    cancel,
  };
}
