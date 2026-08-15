<script setup lang="ts">
/**
 * MessageList — scrollable list of MessageBubbles.
 *
 * Behaviour (mirrors Kenaz visual register + WP12 useStream conventions):
 *   - Auto-scrolls to the bottom on new message *unless* the user has
 *     scrolled up — in which case we surface a subtle "new" pill.
 *   - Tracks the last scrollTop so a user scroll-up sticks; we only
 *     resume auto-stick when the user returns within ~32px of the bottom.
 *   - The "virtualization" here is keyed re-rendering only: for v1 chat
 *     surfaces the message count stays well under the threshold where
 *     window-virtualization pays off. The real virtualizer lands when
 *     long-context replays (>1k messages) become a thing.
 *
 * Move rendering (model-moves-transcript-01PMCH01 WP04): rows are no
 * longer one-bubble-each. `projectTranscript` folds a turn's
 * intermediate moves + tool chips into a MoveTrail and leaves the
 * turn's answer as a full MessageBubble; classic (kind-less) rows come
 * through the projection unchanged. In-flight moves arrive as ordinary
 * `Message` rows via `streamingMessages` and are concatenated BEFORE the
 * projection runs, which is what makes the live view and a reload the
 * same render rather than two renders that agree.
 *
 * The render loop below emits a `MoveTrail` for a trail item and the
 * unchanged `MessageBubble` for everything else — classic rows and turn
 * answers. It carries no explanatory comments deliberately: Vue renders
 * template comments as DOM comment nodes, and the classic-session golden
 * (`MessageList.classic-golden.test.ts`, captured against the pre-WP04
 * component) is byte-exact.
 */

import { computed, nextTick, onMounted, ref, watch } from 'vue';
import MessageBubble from './MessageBubble.vue';
import MoveTrail from './MoveTrail.vue';
import { projectTranscript } from '@/lib/transcript';
import type { Artifact, MemoryScopeKind, Message } from '@/lib/types';

const props = defineProps<{
  messages: ReadonlyArray<Message>;
  /**
   * In-flight moves of the turn currently streaming, oldest first, each
   * already shaped as the transcript row it will become. Empty when no
   * stream is open. The chat surface owns the buffer so this component
   * stays presentational.
   */
  streamingMessages?: ReadonlyArray<Message>;
  /**
   * True while a stream is open but no chunks have arrived yet — render
   * a small "thinking…" indicator so the user sees the system is alive.
   */
  waiting?: boolean;
  /**
   * Inline error to render at the bottom of the conversation. Useful
   * when send/startStream fails — the error must surface in the chat
   * itself, not just the page subtitle.
   */
  errorMessage?: string | null;
  /**
   * Discriminator for `errorMessage`, mirroring the backend's
   * StreamClosedPayload.ErrorKind. "session_full" swaps the generic
   * "Send failed" framing for accurate copy: the send DID succeed — the
   * user's message is in the transcript above — and what failed is that
   * the conversation no longer fits the model's context window. Retrying
   * cannot help, so the banner offers the way out instead.
   */
  errorKind?: string | null;
  /**
   * When true, MessageBubbles render a 📌 button on hover so the user
   * can pin individual messages to long-term memory. Off by default
   * (privacy posture); the parent flips it on when memory is enabled.
   */
  rememberable?: boolean;
  /**
   * Project membership of the active session. Threaded down to each
   * MessageBubble so the pin menu can disable "Pin to project" when
   * the session is loose. Empty / undefined means no project.
   */
  projectId?: string;
  /**
   * When true, MessageBubbles surface a "Save as artifact" affordance.
   * Off when the parent doesn't intend to handle the corresponding
   * `save-artifact` event.
   */
  saveable?: boolean;
  /**
   * Map of message id → artifacts captured from that message. The
   * parent fetches the full session artifact list once via
   * `useArtifacts(sessionId)` and projects to a per-message map; we
   * intentionally avoid per-bubble rpc fetches.
   */
  artifactsByMessage?: ReadonlyMap<string, readonly Artifact[]>;
  /**
   * Count of soft-archived rows that have since been hard-deleted by
   * the soft-archive sweep (compaction-strategy-ui WP07). When > 0,
   * MessageList renders an inline placeholder above the earliest
   * surviving message: `[N earlier turns no longer available — archived
   * past <archiveDays>-day retention]`. Defaults to 0.
   */
  sweptCount?: number;
  /**
   * Settings.compactionArchiveDays — drives the placeholder copy so
   * the user sees the same number they configured. Falls back to 90
   * (the locked default in plan §2.9) when undefined.
   */
  archiveDays?: number;
  /**
   * When true, each completed assistant bubble shows a TokenMeterChip.
   * Driven by the global settings.showPerMessageTokenMeter toggle.
   * (per-message-token-meter-01KR3PQR)
   */
  showTokenMeter?: boolean;
}>();

const emit = defineEmits<{
  /**
   * Forwarded from MessageBubble's pin menu. The second argument is the
   * scope the user picked; defaults to `'session'` for legacy callers.
   */
  (e: 'remember', message: Message, scope: MemoryScopeKind): void;
  /** Forwarded from MessageBubble's "Save as artifact" affordance. */
  (e: 'save-artifact', message: Message): void;
  /**
   * Emitted from the session-full banner's CTA. The parent opens the
   * same new-session flow the LongSessionNudge banner uses — one
   * destination for one problem.
   */
  (e: 'new-session'): void;
  /** Forwarded from a message's per-message artifact chip. */
  (e: 'open-artifact', artifact: Artifact): void;
  /**
   * Forwarded from an archived MessageBubble's "→ summary" chip
   * (compaction-strategy-ui WP07). MessageList scrolls the matching
   * summary row into view.
   */
  (e: 'jump-to-summary', summaryId: string): void;
  /**
   * Forwarded from MessageBubble's "Branch from this turn" button
   * (branching-ux-polish-01KQ8TD7 WP05). Parent calls
   * client.branches.createExplicit and navigates to the child session.
   */
  (e: 'branch-from-turn', message: Message): void;
  /**
   * Forwarded from MessageBubble's "Resume" button on a partial message
   * (long-turn-resilience WP03). Parent calls
   * client.sessions.resumeMessage to continue the interrupted stream.
   */
  (e: 'resume', messageId: string): void;
}>();

function artifactsFor(messageId: string): readonly Artifact[] {
  if (!props.artifactsByMessage) return [];
  return props.artifactsByMessage.get(messageId) ?? [];
}

/**
 * summaryFoldCounts — for each summary row id, the number of archived
 * messages currently in `messages` that fold into it (i.e. have
 * `compactedIntoId === summaryId`). The "Summary of N turns" indicator
 * pulls from this map. Computed reactively so reload + toggle render
 * stable counts.
 */
const summaryFoldCounts = computed<ReadonlyMap<string, number>>(() => {
  const counts = new Map<string, number>();
  for (const m of props.messages) {
    const into = m.compactedIntoId;
    if (!into) continue;
    counts.set(into, (counts.get(into) ?? 0) + 1);
  }
  return counts;
});

function isSummaryRow(m: Message): boolean {
  // A row is the synthetic summary when compactedAt is set AND
  // compactedIntoId is empty (the summary itself doesn't reference
  // another summary). Plan §2.4 step 5 carries this convention.
  return Boolean(m.compactedAt) && !m.compactedIntoId;
}

function isArchivedRow(m: Message): boolean {
  return Boolean(m.archivedAt);
}

function onJumpToSummary(summaryId: string) {
  // Scroll the summary row into view — the data attribute is set on
  // every message wrapper below so a querySelector can find it.
  emit('jump-to-summary', summaryId);
  const el = scrollEl.value;
  if (!el) return;
  const target = el.querySelector(
    `[data-message-id="${CSS.escape(summaryId)}"]`,
  );
  if (target instanceof HTMLElement) {
    target.scrollIntoView({ behavior: 'smooth', block: 'center' });
  }
}

const archivedCopyDays = computed(() => props.archiveDays ?? 90);

const scrollEl = ref<HTMLElement | null>(null);
const stickToBottom = ref(true);
const newPillVisible = ref(false);

const allMessages = computed<ReadonlyArray<Message>>(() => {
  const live = props.streamingMessages ?? [];
  if (live.length === 0) return props.messages;
  return [...props.messages, ...live];
});

/**
 * The render list. One projection over persisted + in-flight rows —
 * see the note in `lib/transcript.ts` on why this is deliberately not
 * two code paths.
 */
const transcript = computed(() => projectTranscript(allMessages.value));

function isNearBottom(el: HTMLElement, threshold = 32): boolean {
  return el.scrollHeight - el.scrollTop - el.clientHeight <= threshold;
}

function scrollToBottom() {
  const el = scrollEl.value;
  if (!el) return;
  el.scrollTop = el.scrollHeight;
  newPillVisible.value = false;
  stickToBottom.value = true;
}

function onScroll() {
  const el = scrollEl.value;
  if (!el) return;
  if (isNearBottom(el)) {
    stickToBottom.value = true;
    newPillVisible.value = false;
  } else {
    stickToBottom.value = false;
  }
}

onMounted(() => {
  void nextTick(scrollToBottom);
});

const isStreaming = computed(() => (props.streamingMessages ?? []).length > 0);

/**
 * Total in-flight text length — the cheap scalar that changes on every
 * token, so the auto-scroll watcher fires per delta without deep-watching
 * the whole move list.
 */
const streamingContentLength = computed(() => {
  let total = 0;
  for (const m of props.streamingMessages ?? []) total += m.content.length;
  return total;
});

watch(
  () => allMessages.value.length,
  async () => {
    await nextTick();
    if (stickToBottom.value) {
      scrollToBottom();
    } else if (isStreaming.value) {
      newPillVisible.value = true;
    }
  },
);

watch(streamingContentLength, async () => {
  await nextTick();
  if (stickToBottom.value) scrollToBottom();
  else if (isStreaming.value) newPillVisible.value = true;
});

defineExpose({ scrollToBottom });
</script>

<template>
  <div class="relative flex flex-col h-full">
    <div
      ref="scrollEl"
      class="flex-1 overflow-y-auto px-4 py-3 space-y-3"
      role="log"
      aria-live="polite"
      aria-relevant="additions"
      aria-label="Conversation messages"
      @scroll="onScroll"
    >
      <div
        v-if="allMessages.length === 0"
        class="grid place-items-center h-full font-ui text-sm text-ink-subtle"
      >
        No messages yet — start the conversation below.
      </div>

      <!-- Swept-count placeholder (compaction-strategy-ui WP07). Renders
           above the first message when archived rows have been hard-
           deleted by the soft-archive sweep. SweptCount > 0 only after
           WP09 wires the per-session counter; until then the path is
           exercised but never visible. -->
      <div
        v-if="(sweptCount ?? 0) > 0"
        class="rounded-md border border-dashed border-border-muted px-3 py-2 font-ui text-[11px] text-ink-subtle"
        role="status"
        data-testid="swept-history-placeholder"
      >
        {{ sweptCount }} earlier turns no longer available — archived past
        {{ archivedCopyDays }}-day retention.
      </div>

      <template v-for="item in transcript" :key="item.key">
        <MoveTrail v-if="item.type === 'trail'" :steps="item.steps" />

        <div v-else :data-message-id="item.message.id">
          <MessageBubble
            :role="item.message.role"
            :content="item.message.content"
            :streaming="item.message.streaming === true"
            :streaming-error="item.message.streamingError"
            :tool-calls="item.message.toolCalls"
            :content-blocks="item.message.contentBlocks"
            :rememberable="rememberable === true && item.message.streaming !== true"
            :project-id="projectId"
            :message-id="item.message.id"
            :saveable="saveable === true && item.message.streaming !== true"
            :artifacts="artifactsFor(item.message.id)"
            :is-summary="isSummaryRow(item.message)"
            :summary-folded-count="summaryFoldCounts.get(item.message.id) ?? 0"
            :is-archived="isArchivedRow(item.message)"
            :archived-from-summary-id="item.message.compactedIntoId ?? ''"
            :show-token-meter="showTokenMeter === true"
            :prompt-tokens="item.message.promptTokens"
            :completion-tokens="item.message.completionTokens"
            :cost-usd="item.message.costUsd"
            :message-cost-source="item.message.messageCostSource"
            :streaming-failed-at="item.message.streamingFailedAt"
            :streaming-recoverable="item.message.streamingRecoverable"
            @remember="(scope) => emit('remember', item.message, scope)"
            @save-artifact="() => emit('save-artifact', item.message)"
            @open-artifact="(a) => emit('open-artifact', a)"
            @jump-to-summary="onJumpToSummary"
            @branch-from-turn="() => emit('branch-from-turn', item.message)"
            @resume="(mid) => emit('resume', mid)"
          />
        </div>
      </template>

      <!-- Thinking indicator: visible from the moment send() opens a
           stream until the first chunk arrives. -->
      <div
        v-if="waiting"
        class="flex items-center gap-2 font-ui text-[12px] text-ink-muted"
        role="status"
        aria-live="polite"
      >
        <span class="thinking-dots" aria-hidden="true">
          <span class="dot"></span>
          <span class="dot"></span>
          <span class="dot"></span>
        </span>
        <span>Thinking…</span>
      </div>

      <!-- Session-full banner. Distinct from the generic error below
           because "Send failed" is factually wrong here: the send
           succeeded, the message is in the transcript, and the model
           never got to answer. Retrying is not the remedy, so this
           banner offers the same escape the long-session nudge does. -->
      <div
        v-if="errorMessage && errorKind === 'session_full'"
        data-testid="session-full-banner"
        class="rounded-md border border-accent-hairline bg-surface-1 px-3 py-2 font-ui text-[12px]"
        role="alert"
      >
        <div class="text-[10px] uppercase tracking-[0.18em] text-accent">
          Session full
        </div>
        <p class="mt-1 break-words text-ink">
          This conversation no longer fits the model's context window, so
          the assistant could not reply. Your message was saved. Start a
          new session to keep going, or raise the compaction
          aggressiveness in Settings.
        </p>
        <button
          type="button"
          data-testid="session-full-new-session"
          class="mt-2 px-3 py-1 rounded-md border border-accent bg-surface-2 text-accent font-ui text-[11px] uppercase tracking-[0.18em] hover:bg-surface-3"
          @click="emit('new-session')"
        >
          + New session
        </button>
      </div>

      <!-- Inline error from useSession.error.value when send fails. -->
      <div
        v-else-if="errorMessage"
        class="rounded-md border border-signal-danger bg-surface-1 px-3 py-2 font-ui text-[12px] text-signal-danger"
        role="alert"
      >
        <div class="text-[10px] uppercase tracking-[0.18em] text-signal-danger">
          Send failed
        </div>
        <p class="mt-1 break-words text-ink">{{ errorMessage }}</p>
      </div>
    </div>

    <button
      v-if="newPillVisible"
      type="button"
      class="absolute bottom-3 left-1/2 -translate-x-1/2 px-3 py-1 rounded-md border border-accent bg-surface-2 text-accent font-ui text-[11px] uppercase tracking-[0.18em] hover:bg-surface-3"
      aria-label="Scroll to newest message"
      @click="scrollToBottom"
    >
      new ↓
    </button>
  </div>
</template>

<style scoped>
.thinking-dots {
  display: inline-flex;
  gap: 3px;
}
.thinking-dots .dot {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: currentColor;
  opacity: 0.4;
  animation: thinking-bounce 1.2s infinite ease-in-out;
}
.thinking-dots .dot:nth-child(2) { animation-delay: 0.15s; }
.thinking-dots .dot:nth-child(3) { animation-delay: 0.3s; }

@keyframes thinking-bounce {
  0%, 60%, 100% { transform: translateY(0); opacity: 0.4; }
  30% { transform: translateY(-3px); opacity: 1; }
}
</style>
